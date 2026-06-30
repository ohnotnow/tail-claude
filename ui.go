package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"tail-claude/theme"
)

// pollInterval is how often we re-read the session file for new lines. 300ms is
// invisible for a side-terminal watcher and avoids fsnotify's footguns.
const pollInterval = 300 * time.Millisecond

type focusArea int

const (
	focusSidebar focusArea = iota
	focusMain
)

const (
	sidebarMaxWidth = 42
	dividerWidth    = 3 // " │ "
	previewWords    = 10
)

// model is the single Bubble Tea model for tc. Mode/focus dispatch is kept
// deliberately small — there are only two panes and no input forms.
type model struct {
	cwd      string // project dir, for re-targeting to newer sessions
	path     string // session file currently followed
	offset   int64  // byte offset of next unread line
	messages []Message

	list     list.Model
	viewport viewport.Model
	renderer *glamour.TermRenderer

	width, height int
	contentHeight int
	mainWidth     int
	focus         focusArea
	ready         bool

	following bool // true = follow the tail; false = scroll-locked on history

	brake     *matcher
	doom      *matcher
	threshold int
	drought   int // assistant turns since the last brake-word
}

// tickMsg fires on the poll interval; tailResult carries what a poll found.
type tickMsg struct{}

type tailResult struct {
	msgs     []Message
	switched bool // re-targeted to a new session file (msgs is a full reload)
	path     string
	offset   int64
	err      error
}

func newModel(cwd, path string, cfg Config) model {
	l := list.New(nil, itemDelegate{width: sidebarMaxWidth}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)

	brake, doom, threshold := cfg.resolve()
	return model{
		cwd:       cwd,
		path:      path,
		list:      l,
		focus:     focusSidebar,
		following: true,
		brake:     newMatcher(brake),
		doom:      newMatcher(doom),
		threshold: threshold,
	}
}

func (m model) Init() tea.Cmd {
	// Read the file immediately, and start the poll loop.
	return tea.Batch(pollCmd(m.cwd, m.path, m.offset), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// pollCmd reads new lines off the main thread. It works on a copy of the tail
// state and reports the result back as a tailResult — the model never does file
// IO inside Update itself.
func pollCmd(cwd, path string, offset int64) tea.Cmd {
	return func() tea.Msg {
		t := tailer{cwd: cwd, path: path, offset: offset}
		switched := t.retarget()
		msgs, err := t.read()
		return tailResult{msgs: msgs, switched: switched, path: t.path, offset: t.offset, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)
		m.ready = true
		return m, nil

	case tickMsg:
		return m, tea.Batch(pollCmd(m.cwd, m.path, m.offset), tickCmd())

	case tailResult:
		return m.applyTail(msg), nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.toggleFocus()
			return m, nil
		case "right", "l":
			// Directional focus. Intercepted here so they switch panes rather
			// than driving the bubbles list's built-in h/l/←/→ pagination.
			m.focus = focusMain
			return m, nil
		case "left", "h":
			m.focus = focusSidebar
			return m, nil
		case "a":
			m.snapToLive()
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.focus == focusSidebar {
		prev := m.list.Index()
		m.list, cmd = m.list.Update(msg)
		if m.list.Index() != prev {
			m.onSelectionChanged()
		}
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

// applyTail folds a poll result into the model. This is where scroll-lock lives:
// when the user is following we jump to the newest message; when they're not, we
// append silently and keep their cursor exactly where it was.
func (m model) applyTail(r tailResult) model {
	m.path, m.offset = r.path, r.offset
	if r.err != nil {
		return m // transient (file mid-rotate, etc.) — try again next tick
	}

	if r.switched {
		m.messages = r.msgs
		m.list.SetItems(messagesToItems(m.messages))
		m.following = true
		if len(m.messages) > 0 {
			m.list.Select(len(m.messages) - 1)
		}
		m.drought = brakeDrought(m.messages, m.brake)
		m.renderSelected()
		return m
	}

	if len(r.msgs) == 0 {
		return m
	}

	keep := m.list.Index()
	wasFollowing := m.following
	m.messages = append(m.messages, r.msgs...)
	m.list.SetItems(messagesToItems(m.messages))
	m.drought = brakeDrought(m.messages, m.brake)

	if wasFollowing {
		m.list.Select(len(m.messages) - 1)
		m.following = true
		m.renderSelected()
	} else {
		m.list.Select(keep) // scroll-lock: a new message must never yank the view
	}
	return m
}

func (m model) View() string {
	if !m.ready {
		return "loading…"
	}
	if len(m.messages) == 0 {
		return fmt.Sprintf("\n  %s\n\n  Waiting for messages in:\n  %s\n",
			theme.Title.Render("🐱 Top Cat"), theme.Faint.Render(m.path))
	}

	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.list.View(),
		dividerView(m.contentHeight),
		m.viewport.View(),
	)
	return strings.Join([]string{m.headerView(), body, m.footerView()}, "\n")
}

// --- layout & selection helpers ---

func (m *model) setSize(w, h int) {
	m.width, m.height = w, h

	const headerHeight, footerHeight = 2, 1 // title+mode / meter+caveat / help
	m.contentHeight = max(h-headerHeight-footerHeight, 1)

	sidebarWidth := clamp(w/3, 16, sidebarMaxWidth)
	m.mainWidth = max(w-sidebarWidth-dividerWidth, 10)

	m.list.SetSize(sidebarWidth, m.contentHeight)
	m.list.SetDelegate(itemDelegate{width: sidebarWidth})

	if m.viewport.Width == 0 && m.viewport.Height == 0 {
		m.viewport = viewport.New(m.mainWidth, m.contentHeight)
	} else {
		m.viewport.Width = m.mainWidth
		m.viewport.Height = m.contentHeight
	}

	// glamour wraps to the pane width; leave a little room for its left margin.
	if r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(m.mainWidth-2, 10)),
	); err == nil {
		m.renderer = r
	}

	m.renderSelected()
}

// onSelectionChanged reacts to the user moving the sidebar cursor: re-render
// the newly selected message and update whether we're still on the tail.
func (m *model) onSelectionChanged() {
	m.following = m.list.Index() == len(m.messages)-1
	m.renderSelected()
}

func (m *model) renderSelected() {
	i := m.list.Index()
	if i < 0 || i >= len(m.messages) || m.renderer == nil {
		m.viewport.SetContent("")
		return
	}
	msg := m.messages[i]
	if msg.Tool != nil {
		m.viewport.SetContent(m.toolDetail(msg.Tool))
		m.viewport.GotoTop()
		return
	}

	rendered, err := m.renderer.Render(msg.Text)
	if err != nil {
		rendered = msg.Text
	}
	// glamour brackets output with blank lines; drop them so content starts at
	// the top of the pane. Then mark up any doom-words as attention-directors.
	content := m.doom.highlight(strings.Trim(rendered, "\n"))
	m.viewport.SetContent(content)
	m.viewport.GotoTop()
}

// toolDetail renders a surfaced tool action for the reading pane: a header, then
// a plain old/new diff — Edit shows old_string (red, '-') then new_string
// (green, '+'); Write shows its content as additions. Lines are truncated to
// the pane width so the layout never overflows.
func (m *model) toolDetail(ev *ToolEvent) string {
	var b strings.Builder
	b.WriteString(theme.Title.Render(fmt.Sprintf("%s %s  %s", toolGlyph(ev.Name), ev.Name, ev.Path)))
	b.WriteString("\n\n")

	red := lipgloss.NewStyle().Foreground(theme.Error)
	green := lipgloss.NewStyle().Foreground(theme.Success)

	if ev.Old != "" {
		for _, ln := range strings.Split(ev.Old, "\n") {
			b.WriteString(red.Render(truncate("- "+ln, m.mainWidth)) + "\n")
		}
	}
	for _, ln := range strings.Split(ev.New, "\n") {
		b.WriteString(green.Render(truncate("+ "+ln, m.mainWidth)) + "\n")
	}
	return b.String()
}

func (m *model) toggleFocus() {
	if m.focus == focusSidebar {
		m.focus = focusMain
	} else {
		m.focus = focusSidebar
	}
}

// snapToLive jumps to the newest message and re-enables live-follow, however
// far back the user had scrolled.
func (m *model) snapToLive() {
	if len(m.messages) == 0 {
		return
	}
	m.list.Select(len(m.messages) - 1)
	m.following = true
	m.focus = focusSidebar
	m.renderSelected()
}

// --- header / footer / divider ---

func (m model) headerView() string {
	title := theme.Title.Render("🐱 Top Cat")

	mode := lipgloss.NewStyle().Foreground(theme.Success).Render("● live")
	if !m.following {
		mode = lipgloss.NewStyle().Foreground(theme.Warning).Render("❚❚ paused — a to follow")
	}

	gap := max(m.width-lipgloss.Width(title)-lipgloss.Width(mode), 1)
	line1 := title + strings.Repeat(" ", gap) + mode

	// The caveat is always on screen so a red meter is never read as a verdict,
	// nor a green one as an all-clear (PRD §5).
	caveat := theme.Faint.Render("a nudge to glance — never a verdict")
	line2 := truncate(m.meterView()+"   "+caveat, m.width)
	return line1 + "\n" + line2
}

// meterView renders the brake-drought meter: green below threshold, red and
// flagged at or above it.
func (m model) meterView() string {
	unit := "turns"
	if m.drought == 1 {
		unit = "turn"
	}
	label := fmt.Sprintf("brake-drought: %d %s", m.drought, unit)
	if m.drought >= m.threshold {
		return lipgloss.NewStyle().Foreground(theme.Error).Bold(true).
			Render("⚠ " + label + " — glance / fresh eyes?")
	}
	return lipgloss.NewStyle().Foreground(theme.Success).Render(label)
}

func (m model) footerView() string {
	focus := "list"
	if m.focus == focusMain {
		focus = "reading"
	}
	help := fmt.Sprintf("[%s] · j/k move · h/l or ←/→ switch pane · tab · a follow · q quit", focus)
	return theme.Faint.Render(truncate(help, m.width))
}

func dividerView(h int) string {
	bar := lipgloss.NewStyle().Foreground(theme.FgMuted).Render("│")
	rows := make([]string, h)
	for i := range rows {
		rows[i] = " " + bar + " "
	}
	return strings.Join(rows, "\n")
}

// --- list item plumbing ---

// item adapts a Message to the bubbles list.Item interface.
type item struct{ msg Message }

func (i item) FilterValue() string { return i.msg.Text }

func messagesToItems(msgs []Message) []list.Item {
	items := make([]list.Item, len(msgs))
	for i := range msgs {
		items[i] = item{msg: msgs[i]}
	}
	return items
}

// itemDelegate renders one compact sidebar row: "HH:MM · first words", coloured
// by role and highlighted when selected.
type itemDelegate struct{ width int }

func (d itemDelegate) Height() int                         { return 1 }
func (d itemDelegate) Spacing() int                        { return 0 }
func (d itemDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}
	ts := it.msg.Timestamp.Format("15:04")

	var line string
	var fg lipgloss.Color
	switch {
	case it.msg.Tool != nil:
		line = fmt.Sprintf("%s %s %s", ts, toolGlyph(it.msg.Tool.Name), filepath.Base(it.msg.Tool.Path))
		fg = theme.Warning
	case it.msg.Role == RoleUser:
		line = fmt.Sprintf("%s ▸ %s", ts, preview(it.msg.Text, previewWords))
		fg = theme.Accent
	default:
		line = fmt.Sprintf("%s · %s", ts, preview(it.msg.Text, previewWords))
		fg = theme.Fg
	}
	line = truncate(line, d.width)

	style := lipgloss.NewStyle().Foreground(fg)
	if index == m.Index() {
		style = theme.Selected.Width(d.width)
	}
	fmt.Fprint(w, style.Render(line))
}

// toolGlyph is the sidebar marker for a surfaced tool action.
func toolGlyph(name string) string {
	switch name {
	case "Write":
		return "＋"
	case "Edit":
		return "✎"
	default:
		return "⚙"
	}
}

// --- small string/number helpers ---

// preview collapses whitespace and keeps the first n words, adding an ellipsis
// if it truncated.
func preview(text string, n int) string {
	fields := strings.Fields(text)
	if len(fields) > n {
		return strings.Join(fields[:n], " ") + "…"
	}
	return strings.Join(fields, " ")
}

// truncate shortens s to a visible width of w (ANSI-aware), adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
