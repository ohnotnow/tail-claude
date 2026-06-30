package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func key(m model, k tea.KeyMsg) model {
	next, _ := m.Update(k)
	return next.(model)
}

func runeKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// TestDirectionalFocus covers the navigation keys: →/l focus the reading pane,
// ←/h focus the list, and tab still toggles.
func TestDirectionalFocus(t *testing.T) {
	m := newTestModel(t, 90, 18)
	if m.focus != focusSidebar {
		t.Fatal("should start focused on the list")
	}

	if m = key(m, tea.KeyMsg{Type: tea.KeyRight}); m.focus != focusMain {
		t.Error("→ should focus the reading pane")
	}
	if m = key(m, tea.KeyMsg{Type: tea.KeyLeft}); m.focus != focusSidebar {
		t.Error("← should focus the list")
	}
	if m = key(m, runeKey("l")); m.focus != focusMain {
		t.Error("l should focus the reading pane")
	}
	if m = key(m, runeKey("h")); m.focus != focusSidebar {
		t.Error("h should focus the list")
	}
	if m = key(m, tea.KeyMsg{Type: tea.KeyTab}); m.focus != focusMain {
		t.Error("tab should toggle focus to the reading pane")
	}
}

// TestVimListNavigation confirms j/k move the sidebar cursor (vim binds, via the
// list component defaults) when the list is focused.
func TestVimListNavigation(t *testing.T) {
	m := newTestModel(t, 90, 18)
	m.list.Select(0)

	if m = key(m, runeKey("j")); m.list.Index() != 1 {
		t.Errorf("j should move down to index 1, got %d", m.list.Index())
	}
	if m = key(m, runeKey("k")); m.list.Index() != 0 {
		t.Errorf("k should move up to index 0, got %d", m.list.Index())
	}
}

func sampleMessages() []Message {
	base := time.Date(2026, 6, 30, 17, 35, 0, 0, time.Local)
	return []Message{
		{UUID: "u1", Role: RoleUser, Timestamp: base, Text: "Hi there! Could we have a look at building the watcher tool together?"},
		{UUID: "a1", Role: RoleAssistant, Timestamp: base.Add(time.Minute), Text: "## Plan\n\nHere's what I'd suggest:\n\n- Read the session log\n- Render it nicely\n- Flag the momentum tell\n\nLet me verify the data plumbing first."},
		{UUID: "u2", Role: RoleUser, Timestamp: base.Add(2 * time.Minute), Text: "Sounds good, go for it."},
		{UUID: "a2", Role: RoleAssistant, Timestamp: base.Add(3 * time.Minute), Text: "Done — the core works and all the tests are green."},
	}
}

// newTestModel builds a sized, ready model populated with sample messages (via
// the same applyTail path the live tail uses), following the newest message.
func newTestModel(t *testing.T, w, h int) model {
	t.Helper()
	m := newModel("/cwd", "/path", Config{})
	m.setSize(w, h)
	m.ready = true
	return m.applyTail(tailResult{msgs: sampleMessages(), switched: true, path: "/path"})
}

func newTurn(text string) Message {
	return Message{Role: RoleAssistant, Timestamp: time.Now(), Text: text}
}

// TestViewNoOverflow is the automated guard against the lipgloss Width() footgun
// (TUI.md): after layout, no rendered line may exceed the terminal width.
func TestViewNoOverflow(t *testing.T) {
	const w, h = 100, 24
	m := newTestModel(t, w, h)
	for i, line := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("line %d width %d exceeds terminal width %d:\n%q", i, got, w, line)
		}
	}
}

// TestViewSmoke renders the full view so a human can eyeball it with `go test -v`.
func TestViewSmoke(t *testing.T) {
	m := newTestModel(t, 90, 18)
	t.Logf("\n%s", m.View())
}

// TestSnapToLive confirms 'a' selects the newest message and re-follows.
func TestSnapToLive(t *testing.T) {
	m := newTestModel(t, 90, 18)
	m.list.Select(0)
	m.onSelectionChanged()
	if m.following {
		t.Fatal("expected following=false after selecting an older message")
	}
	m.snapToLive()
	if !m.following {
		t.Error("expected following=true after snapToLive")
	}
	if want := len(m.messages) - 1; m.list.Index() != want {
		t.Errorf("snapToLive selected index %d, want %d", m.list.Index(), want)
	}
}

// TestScrollLockHoldsPosition is the linchpin test: while scroll-locked on an
// older message, a newly-arrived message must append WITHOUT moving the view.
func TestScrollLockHoldsPosition(t *testing.T) {
	m := newTestModel(t, 90, 18)
	m.list.Select(1) // navigate back to history
	m.onSelectionChanged()
	if m.following {
		t.Fatal("precondition: should not be following after navigating back")
	}

	before := m.list.Index()
	n := len(m.messages)
	m = m.applyTail(tailResult{msgs: []Message{newTurn("a new turn lands")}, path: "/path"})

	if m.list.Index() != before {
		t.Errorf("scroll-lock broken: selection moved %d -> %d when a message arrived", before, m.list.Index())
	}
	if len(m.messages) != n+1 {
		t.Errorf("expected the new message to be appended: got %d msgs, want %d", len(m.messages), n+1)
	}
	if m.following {
		t.Error("a new message must not silently re-enable following")
	}
}

// TestFollowAdvances confirms that while following, a new message advances the
// selection to the new tail.
func TestFollowAdvances(t *testing.T) {
	m := newTestModel(t, 90, 18) // starts following, on the newest
	n := len(m.messages)
	m = m.applyTail(tailResult{msgs: []Message{newTurn("freshly arrived")}, path: "/path"})

	if !m.following {
		t.Error("expected to still be following")
	}
	if m.list.Index() != n { // new last index
		t.Errorf("follow did not advance: index %d, want %d", m.list.Index(), n)
	}
}

// TestDoomHighlightThroughGlamour proves the reverse-video doom highlight
// survives glamour's own ANSI in the rendered reading pane — the fragile bit.
func TestDoomHighlightThroughGlamour(t *testing.T) {
	m := newModel("/cwd", "/path", Config{})
	m.setSize(120, 20) // wide pane so the phrase doesn't wrap mid-match
	m.ready = true
	m = m.applyTail(tailResult{
		msgs:     []Message{newTurn("Honestly it should be fine, all green now.")},
		switched: true, path: "/path",
	})

	got := m.viewport.View()
	if !strings.Contains(got, "\x1b[7mall green\x1b[27m") {
		t.Errorf("doom phrase 'all green' was not highlighted through glamour:\n%q", got)
	}
}

// TestToolDetailRender confirms a selected tool event shows the old/new render
// in the reading pane, and that long tool lines don't overflow the layout.
func TestToolDetailRender(t *testing.T) {
	const w = 80
	m := newModel("/cwd", "/path", Config{})
	m.setSize(w, 20)
	m.ready = true

	long := strings.Repeat("x", 200)
	ev := &ToolEvent{Name: "Edit", Path: "/proj/session.go", Old: "return nil", New: "return " + long}
	m = m.applyTail(tailResult{
		msgs:     []Message{{Role: RoleAssistant, Timestamp: time.Now(), Tool: ev}},
		switched: true, path: "/path",
	})

	if got := m.viewport.View(); !strings.Contains(got, "- return nil") {
		t.Errorf("expected old line '- return nil' in tool detail, got:\n%q", got)
	}
	for i, line := range strings.Split(m.View(), "\n") {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("line %d width %d exceeds %d (tool detail overflow):\n%q", i, got, w, line)
		}
	}
}

// TestToolViewSmoke renders a timeline with a prose turn, an edit, and a write
// so the layout can be eyeballed with `go test -v`.
func TestToolViewSmoke(t *testing.T) {
	base := time.Date(2026, 6, 30, 17, 40, 0, 0, time.Local)
	msgs := []Message{
		{Role: RoleAssistant, Timestamp: base, Text: "Right, let me fix that off."},
		{Role: RoleAssistant, Timestamp: base.Add(time.Minute), Tool: &ToolEvent{
			Name: "Edit", Path: "/proj/session.go",
			Old: `return "", fmt.Errorf("no dir")`, New: `return "", fmt.Errorf("no session dir for %s", dir)`}},
		{Role: RoleAssistant, Timestamp: base.Add(2 * time.Minute), Text: "That's sorted, tests are green."},
	}
	m := newModel("/cwd", "/path", Config{})
	m.setSize(96, 14)
	m.ready = true
	m = m.applyTail(tailResult{msgs: msgs, switched: true, path: "/path"})
	m.list.Select(1) // select the edit
	m.onSelectionChanged()
	t.Logf("\n%s", m.View())
}

// TestRetargetReplaces confirms a switched (newer session) result replaces the
// timeline rather than appending to it.
func TestRetargetReplaces(t *testing.T) {
	m := newTestModel(t, 90, 18)
	fresh := []Message{newTurn("brand new session, message one")}
	m = m.applyTail(tailResult{msgs: fresh, switched: true, path: "/new"})

	if len(m.messages) != 1 {
		t.Errorf("expected timeline replaced with 1 message, got %d", len(m.messages))
	}
	if m.path != "/new" {
		t.Errorf("path = %q, want /new", m.path)
	}
	if !m.following {
		t.Error("a re-target should re-enable following")
	}
}
