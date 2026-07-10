package main

import (
	"strings"
	"testing"
	"time"
)

// TestDefaultsExcludeActually guards the central empirical finding: "actually"
// is noise (77% of raw brake hits, a discourse marker) and must never creep
// back into the default brake list. See ant foundation tc-AkRXV.
func TestDefaultsExcludeActually(t *testing.T) {
	for _, w := range defaultBrakeWords {
		if strings.EqualFold(w, "actually") {
			t.Fatal(`"actually" is back in defaultBrakeWords — it was deliberately removed as noise`)
		}
	}
}

func TestMatcherWordBoundary(t *testing.T) {
	m := newMatcher([]string{"wait"})
	hits := []string{"Wait, let me think.", "ok, wait.", "WAIT"}
	for _, s := range hits {
		if !m.matchesAny(s) {
			t.Errorf("expected %q to match 'wait'", s)
		}
	}
	misses := []string{"waiting for the build", "await the result", "kuwait"}
	for _, s := range misses {
		if m.matchesAny(s) {
			t.Errorf("did not expect %q to match 'wait'", s)
		}
	}
}

func TestMatcherPhraseAndEmoji(t *testing.T) {
	m := newMatcher([]string{"the good news is", "🎉"})
	if !m.matchesAny("Well, the good news is it compiles.") {
		t.Error("expected phrase 'the good news is' to match")
	}
	if !m.matchesAny("all done 🎉") {
		t.Error("expected emoji 🎉 to match")
	}
	if m.matchesAny("nothing notable here") {
		t.Error("unexpected match")
	}
}

func TestBrakeDrought(t *testing.T) {
	brake := newMatcher(defaultBrakeWords)
	a := func(s string) Message { return Message{Role: RoleAssistant, Text: s, Timestamp: time.Now()} }
	u := func(s string) Message { return Message{Role: RoleUser, Text: s, Timestamp: time.Now()} }

	t.Run("counts trailing assistant turns since a brake, ignoring user turns", func(t *testing.T) {
		msgs := []Message{
			a("let me verify the schema"), // brake
			a("doing the thing"),
			u("ok"), // ignored
			a("more progress"),
			a("all green, nothing loose"), // no brake word here
		}
		if got := brakeDrought(msgs, brake); got != 3 {
			t.Errorf("drought = %d, want 3", got)
		}
	})

	t.Run("zero when the latest assistant turn brakes", func(t *testing.T) {
		msgs := []Message{a("progress"), a("hold on, let me check that")}
		if got := brakeDrought(msgs, brake); got != 0 {
			t.Errorf("drought = %d, want 0", got)
		}
	})

	t.Run("counts every assistant turn when no brakes at all", func(t *testing.T) {
		msgs := []Message{a("one"), u("x"), a("two"), a("three")}
		if got := brakeDrought(msgs, brake); got != 3 {
			t.Errorf("drought = %d, want 3", got)
		}
	})

	t.Run("empty conversation is zero", func(t *testing.T) {
		if got := brakeDrought(nil, brake); got != 0 {
			t.Errorf("drought = %d, want 0", got)
		}
	})

	t.Run("tool events do not count toward the drought", func(t *testing.T) {
		tool := func() Message {
			return Message{Role: RoleAssistant, Tool: &ToolEvent{Name: "Edit", Path: "/x"}, Timestamp: time.Now()}
		}
		msgs := []Message{
			a("let me verify the schema"), // brake
			a("making progress"),          // counts: drought 2
			tool(), tool(),                // must NOT count
			a("still going"), // counts: drought 1
		}
		if got := brakeDrought(msgs, brake); got != 2 {
			t.Errorf("drought = %d, want 2 (tool events must be excluded)", got)
		}
	})
}

func TestJSSources(t *testing.T) {
	m := newMatcher([]string{"wait", "the good news is", "🎉"})
	got := m.jsSources()
	want := []string{`\bwait\b`, `\bthe good news is\b`, `🎉`}
	if len(got) != len(want) {
		t.Fatalf("jsSources() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("jsSources()[%d] = %q, want %q", i, got[i], want[i])
		}
		if strings.Contains(got[i], "(?i)") {
			t.Errorf("jsSources()[%d] = %q still contains the Go-only (?i) flag", i, got[i])
		}
	}
}

func TestHighlightWrapsMatches(t *testing.T) {
	m := newMatcher([]string{"all green"})
	out := m.highlight("the build is all green now")
	if !strings.Contains(out, "\x1b[7mall green\x1b[27m") {
		t.Errorf("expected reverse-video wrap around match, got %q", out)
	}
	// non-matching text is returned unchanged
	if got := m.highlight("plain text"); got != "plain text" {
		t.Errorf("unexpected change: %q", got)
	}
}
