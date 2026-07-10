package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

//go:embed webui/index.html
var indexHTML []byte

// webServer serves the timeline to a browser (tc --web). It is the third
// consumer of the tail plumbing (after the TUI and --print): one poll
// goroutine tails the session file exactly as the TUI does, renders each new
// message to an HTML fragment, and pushes it to every connected browser over
// SSE. A fresh page load is just a replay of the same event stream, so old
// and new messages share a single rendering path.
type webServer struct {
	mu         sync.Mutex
	cwd        string
	path       string
	offset     int64
	msgs       []Message
	events     []sseEvent // one 'append' event per message, current session only
	generation int        // bumped when the tail re-targets to a new session
	drought    int
	subs       map[chan struct{}]struct{}

	md        goldmark.Markdown
	brake     *matcher
	doom      *matcher
	threshold int
}

// sseEvent is one pre-marshalled server-sent event. Append events carry an id
// of the form "<generation>-<index>" so a reconnecting browser (laptop wake is
// the common case) resumes via Last-Event-ID instead of duplicating the whole
// timeline.
type sseEvent struct {
	id, name string
	data     []byte // single-line JSON (json.Marshal never emits raw newlines)
}

// appendPayload is the JSON body of an 'append' event. The server decides all
// display text; the browser only places it.
type appendPayload struct {
	I    int    `json:"i"`    // message index — becomes the anchor id "m<i>"
	Kind string `json:"kind"` // "you" | "claude" | "tool"
	Time string `json:"time"` // HH:MM
	Head string `json:"head"` // article header, e.g. "CLAUDE", "✎ Edit ui.go"
	Side string `json:"side"` // sidebar label, e.g. "▸ Could you add…"
	HTML string `json:"html"` // rendered message body
}

func newWebServer(cwd, path string, brake, doom *matcher, threshold int) *webServer {
	return &webServer{
		cwd:  cwd,
		path: path,
		subs: make(map[chan struct{}]struct{}),
		// goldmark's default (no html.WithUnsafe) drops raw HTML instead of
		// passing it through. That's load-bearing: session transcripts can
		// quote attacker-influenced text (fetched web pages, pasted logs),
		// and none of it may ever execute in the viewer.
		md:        goldmark.New(goldmark.WithExtensions(extension.GFM)),
		brake:     brake,
		doom:      doom,
		threshold: threshold,
	}
}

// runWeb serves the timeline at 127.0.0.1:port instead of running the TUI.
// The listener binds before the URL is printed, so the printed URL is always
// real; the browser auto-open is best-effort on top of it.
func runWeb(cwd, path string, cfg Config, port int) error {
	brakeWords, doomWords, threshold := cfg.resolve()
	s := newWebServer(cwd, path, newMatcher(brakeWords), newMatcher(doomWords), threshold)
	s.poll() // synchronous first read so the first page load isn't empty
	go s.pollLoop()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("cannot listen on port %d (already in use? try --port): %w", port, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	fmt.Printf("🐱 Top Cat watching %s\n%s\n", path, url)
	openBrowser(url)
	return http.Serve(ln, s)
}

// openBrowser makes a best-effort attempt to open url in the default browser.
// The URL is printed before this is called, so failure is deliberately silent
// — the user can still click or copy the printed link.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func (s *webServer) pollLoop() {
	for range time.Tick(pollInterval) {
		s.poll()
	}
}

// poll performs one tail step: read new complete lines, fold them into the
// timeline, and wake the SSE connections. It must only ever run from one
// goroutine at a time (the initial synchronous load, then pollLoop) — the
// tail offset is not safe against concurrent polls.
func (s *webServer) poll() {
	s.mu.Lock()
	t := tailer{cwd: s.cwd, path: s.path, offset: s.offset}
	s.mu.Unlock()

	switched := t.retarget()
	msgs, err := t.read()
	if err != nil {
		return // transient (file mid-rotate, etc.) — try again next tick
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.path, s.offset = t.path, t.offset

	if switched {
		// New session: throw the timeline away and bump the generation. Each
		// connection notices the bump and tells its browser to reload.
		s.generation++
		s.msgs = nil
		s.events = nil
	} else if len(msgs) == 0 {
		return
	}
	for i := range msgs {
		s.appendLocked(msgs[i])
	}
	s.drought = brakeDrought(s.msgs, s.brake)
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // this connection already has a pending wake-up
		}
	}
}

func (s *webServer) appendLocked(m Message) {
	i := len(s.msgs)
	s.msgs = append(s.msgs, m)
	data, _ := json.Marshal(s.payloadFor(i, &s.msgs[i]))
	s.events = append(s.events, sseEvent{
		id:   fmt.Sprintf("%d-%d", s.generation, i),
		name: "append",
		data: data,
	})
}

func (s *webServer) payloadFor(i int, m *Message) appendPayload {
	p := appendPayload{I: i, Time: m.Timestamp.Format("15:04")}
	switch {
	case m.Tool != nil:
		p.Kind = "tool"
		p.Head = fmt.Sprintf("%s %s %s", toolGlyph(m.Tool.Name), m.Tool.Name, m.Tool.Path)
		p.Side = fmt.Sprintf("%s %s", toolGlyph(m.Tool.Name), filepath.Base(m.Tool.Path))
		p.HTML = diffHTML(m.Tool)
	case m.Role == RoleUser:
		p.Kind = "you"
		p.Head = "YOU"
		p.Side = "▸ " + preview(m.Text, previewWords)
		p.HTML = s.markdownHTML(m.Text)
	default:
		p.Kind = "claude"
		p.Head = "CLAUDE"
		p.Side = "· " + preview(m.Text, previewWords)
		p.HTML = s.markdownHTML(m.Text)
	}
	return p
}

// markdownHTML renders message prose to HTML. On a parse failure the text is
// shown escaped rather than lost — same spirit as the TUI's glamour fallback.
func (s *webServer) markdownHTML(text string) string {
	var buf bytes.Buffer
	if err := s.md.Convert([]byte(text), &buf); err != nil {
		return "<pre>" + html.EscapeString(text) + "</pre>"
	}
	return buf.String()
}

// diffHTML renders a surfaced Edit/Write as an old→new diff, mirroring the
// TUI's toolDetail: old_string lines as removals, new/content as additions.
// Every line is escaped — tool inputs are arbitrary file content.
func diffHTML(ev *ToolEvent) string {
	var b strings.Builder
	b.WriteString(`<pre class="diff">`)
	if ev.Old != "" {
		for _, ln := range strings.Split(ev.Old, "\n") {
			b.WriteString(`<span class="del">- ` + html.EscapeString(ln) + "</span>\n")
		}
	}
	for _, ln := range strings.Split(ev.New, "\n") {
		b.WriteString(`<span class="add">+ ` + html.EscapeString(ln) + "</span>\n")
	}
	b.WriteString(`</pre>`)
	return b.String()
}

func (s *webServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	case "/events":
		s.handleEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleEvents is the SSE stream. Protocol, in send order:
//
//	config           — doom-word regexes, threshold, session filename
//	append (id: g-i) — one per message: rendered HTML + sidebar metadata
//	meter            — brake-drought count, sent whenever it changes
//	reload           — the session re-targeted; the browser reloads itself
//
// A fresh connection replays every append event (the page load IS the
// replay). A reconnection with a matching generation resumes from
// Last-Event-ID; a stale generation gets a reload instead, because the
// browser's DOM belongs to a session we no longer hold.
func (s *webServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	wake := make(chan struct{}, 1)
	s.mu.Lock()
	gen := s.generation
	next, stale := 0, false
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		if g, i, ok := parseEventID(lastID); ok && g == gen {
			next = i + 1 // resume: skip what the browser already holds
		} else {
			stale = true // reconnected across a session switch or garbled id
		}
	}
	s.subs[wake] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, wake)
		s.mu.Unlock()
	}()

	if stale {
		writeEvent(w, sseEvent{name: "reload", data: []byte("{}")})
		fl.Flush()
		return
	}
	writeEvent(w, s.configEvent())

	lastMeter := -1
	for {
		s.mu.Lock()
		if s.generation != gen {
			s.mu.Unlock()
			writeEvent(w, sseEvent{name: "reload", data: []byte("{}")})
			fl.Flush()
			return // the reloading browser reconnects with a fresh page
		}
		// Safe to use outside the lock: events are append-only within a
		// generation and entries are never mutated, so this view stays valid
		// even if the backing array is re-allocated by a later append.
		pending := s.events[next:]
		next = len(s.events)
		drought := s.drought
		s.mu.Unlock()

		for _, ev := range pending {
			writeEvent(w, ev)
		}
		if drought != lastMeter {
			lastMeter = drought
			data, _ := json.Marshal(map[string]int{"drought": drought})
			writeEvent(w, sseEvent{name: "meter", data: data})
		}
		fl.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-wake:
		}
	}
}

func (s *webServer) configEvent() sseEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(map[string]any{
		"doomPatterns": s.doom.jsSources(),
		"threshold":    s.threshold,
		"session":      filepath.Base(s.path),
	})
	return sseEvent{name: "config", data: data}
}

func writeEvent(w io.Writer, ev sseEvent) {
	if ev.id != "" {
		fmt.Fprintf(w, "id: %s\n", ev.id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
}

// parseEventID splits an append-event id, "<generation>-<index>". A missing or
// garbled header simply fails the match; the caller falls back to a full
// replay (no header) or a reload (unparseable/stale header).
func parseEventID(s string) (gen, idx int, ok bool) {
	g, i, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	gv, gerr := strconv.Atoi(g)
	iv, ierr := strconv.Atoi(i)
	if gerr != nil || ierr != nil {
		return 0, 0, false
	}
	return gv, iv, true
}
