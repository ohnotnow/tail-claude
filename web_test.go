package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestWebServer builds a webServer over a real (temp) session file and
// performs the initial poll, exactly as runWeb does.
func newTestWebServer(t *testing.T, lines ...string) *webServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	// cwd "" disables re-targeting, so the test file stays the target.
	s := newWebServer("", path, newMatcher(defaultBrakeWords), newMatcher(defaultDoomWords), defaultThreshold)
	s.poll()
	return s
}

const (
	proseLine = `{"type":"assistant","uuid":"a1","timestamp":"2026-07-10T10:00:00Z","message":{"content":[{"type":"text","text":"a **bold** move"}]}}`
	userLine  = `{"type":"user","uuid":"u1","timestamp":"2026-07-10T10:01:00Z","message":{"content":"hello <script>alert(1)</script> world"}}`
	editLine  = `{"type":"assistant","uuid":"a2","timestamp":"2026-07-10T10:02:00Z","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/proj/y.go","old_string":"a<b","new_string":"c>d"}}]}}`
)

func payloadAt(t *testing.T, s *webServer, i int) appendPayload {
	t.Helper()
	if i >= len(s.events) {
		t.Fatalf("want event %d, server only has %d", i, len(s.events))
	}
	var p appendPayload
	if err := json.Unmarshal(s.events[i].data, &p); err != nil {
		t.Fatalf("unmarshal event %d: %v", i, err)
	}
	return p
}

func TestWebPayloads(t *testing.T) {
	s := newTestWebServer(t, proseLine, userLine, editLine)
	if len(s.events) != 3 {
		t.Fatalf("events = %d, want 3", len(s.events))
	}

	t.Run("assistant prose renders as markdown", func(t *testing.T) {
		p := payloadAt(t, s, 0)
		if p.Kind != "claude" || p.Head != "CLAUDE" {
			t.Errorf("kind/head = %q/%q", p.Kind, p.Head)
		}
		if !strings.Contains(p.HTML, "<strong>bold</strong>") {
			t.Errorf("markdown not rendered: %q", p.HTML)
		}
		if !strings.HasPrefix(p.Side, "· ") {
			t.Errorf("sidebar label = %q", p.Side)
		}
	})

	t.Run("raw HTML in a message never survives to the page", func(t *testing.T) {
		p := payloadAt(t, s, 1)
		if p.Kind != "you" {
			t.Errorf("kind = %q, want you", p.Kind)
		}
		if strings.Contains(p.HTML, "<script>") {
			t.Fatalf("raw HTML passed through goldmark unescaped: %q", p.HTML)
		}
	})

	t.Run("edit renders an escaped old/new diff", func(t *testing.T) {
		p := payloadAt(t, s, 2)
		if p.Kind != "tool" {
			t.Errorf("kind = %q, want tool", p.Kind)
		}
		if p.Side != "✎ y.go" {
			t.Errorf("sidebar label = %q, want ✎ y.go", p.Side)
		}
		for _, want := range []string{`class="del">- a&lt;b`, `class="add">+ c&gt;d`} {
			if !strings.Contains(p.HTML, want) {
				t.Errorf("diff HTML missing %q: %q", want, p.HTML)
			}
		}
	})

	t.Run("event ids are generation-index", func(t *testing.T) {
		if got := s.events[2].id; got != "0-2" {
			t.Errorf("event id = %q, want 0-2", got)
		}
	})
}

func TestWebServeIndex(t *testing.T) {
	s := newTestWebServer(t, proseLine)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Top Cat") {
		t.Error("index page does not contain the app title")
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if rec.Code != 404 {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
}

// eventsBody runs the (blocking) SSE handler until the request context times
// out, then returns everything it wrote.
func eventsBody(t *testing.T, s *webServer, lastEventID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestWebEventsReplay(t *testing.T) {
	s := newTestWebServer(t, proseLine, userLine)
	body := eventsBody(t, s, "")

	for _, want := range []string{"event: config", "event: meter", "id: 0-0", "id: 0-1"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream missing %q:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "event: append"); got != 2 {
		t.Errorf("append events = %d, want 2", got)
	}
	if !strings.Contains(body, "doomPatterns") {
		t.Error("config event missing doomPatterns")
	}
}

func TestWebEventsResume(t *testing.T) {
	s := newTestWebServer(t, proseLine, userLine)
	body := eventsBody(t, s, "0-0")

	if strings.Contains(body, "id: 0-0") {
		t.Error("resume replayed an event the browser already holds")
	}
	if !strings.Contains(body, "id: 0-1") {
		t.Errorf("resume did not deliver the next event:\n%s", body)
	}
}

func TestWebEventsStaleGenerationReloads(t *testing.T) {
	s := newTestWebServer(t, proseLine)
	body := eventsBody(t, s, "7-3") // a generation this server has never had

	if !strings.Contains(body, "event: reload") {
		t.Errorf("stale generation did not trigger a reload:\n%s", body)
	}
	if strings.Contains(body, "event: append") {
		t.Error("stale generation must reload, not append onto a foreign DOM")
	}
}

func TestWebLiveAppendWakesStream(t *testing.T) {
	s := newTestWebServer(t, proseLine)

	// Append a new line mid-stream, then poll — the handler must push it to
	// an already-connected client without a reconnect.
	go func() {
		time.Sleep(20 * time.Millisecond)
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return
		}
		f.WriteString(userLine + "\n")
		f.Close()
		s.poll()
	}()

	body := eventsBody(t, s, "")
	if got := strings.Count(body, "event: append"); got != 2 {
		t.Errorf("append events = %d, want 2 (live append not delivered):\n%s", got, body)
	}
}

func TestParseEventID(t *testing.T) {
	cases := []struct {
		in       string
		gen, idx int
		ok       bool
	}{
		{"0-0", 0, 0, true},
		{"3-42", 3, 42, true},
		{"", 0, 0, false},
		{"nonsense", 0, 0, false},
		{"1-", 0, 0, false},
		{"-1", 0, 0, false},
		{"a-b", 0, 0, false},
	}
	for _, tc := range cases {
		gen, idx, ok := parseEventID(tc.in)
		if gen != tc.gen || idx != tc.idx || ok != tc.ok {
			t.Errorf("parseEventID(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tc.in, gen, idx, ok, tc.gen, tc.idx, tc.ok)
		}
	}
}
