package main

import (
	"os"
	"path/filepath"
	"testing"
)

// appendToFile appends raw bytes to a file, creating it if needed.
func appendToFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("append: %v", err)
	}
}

const (
	lineUser   = `{"type":"user","uuid":"u1","message":{"content":"first prompt"}}` + "\n"
	lineAsst   = `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"first reply"}]}}` + "\n"
	lineSystem = `{"type":"system","uuid":"s1","message":{"content":"a system note"}}` + "\n"
	lineAsst2  = `{"type":"assistant","uuid":"a2","message":{"content":[{"type":"text","text":"second reply"}]}}` + "\n"
)

// TestTailerIncrementalRead is the core live-follow guarantee: each read returns
// only the new complete lines, the offset advances past skipped lines too (so
// they aren't re-read), and a half-written final line is left for next time.
func TestTailerIncrementalRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")

	appendToFile(t, path, lineUser+lineAsst)
	tl := tailer{path: path}

	msgs, err := tl.read()
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("first read got %d messages, want 2", len(msgs))
	}

	// A non-renderable line must be skipped but still consumed (offset advances),
	// alongside a real new message.
	appendToFile(t, path, lineSystem+lineAsst2)
	msgs, err = tl.read()
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "second reply" {
		t.Fatalf("second read got %d messages (%v), want 1 'second reply'", len(msgs), msgs)
	}

	// Nothing new -> no messages, no error.
	msgs, _ = tl.read()
	if len(msgs) != 0 {
		t.Fatalf("idle read returned %d messages, want 0", len(msgs))
	}
}

// TestTailerPartialLine confirms a line written without its trailing newline is
// NOT consumed until it's complete — Claude Code flushes lines incrementally.
func TestTailerPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendToFile(t, path, lineUser)

	tl := tailer{path: path}
	if msgs, _ := tl.read(); len(msgs) != 1 {
		t.Fatalf("setup read got %d, want 1", len(msgs))
	}
	offsetBefore := tl.offset

	// Write the next line WITHOUT a newline (a mid-flush partial).
	partial := `{"type":"assistant","uuid":"a9","message":{"content":[{"type":"text","text":"half`
	appendToFile(t, path, partial)

	msgs, err := tl.read()
	if err != nil {
		t.Fatalf("partial read: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("partial line should yield no messages, got %d", len(msgs))
	}
	if tl.offset != offsetBefore {
		t.Errorf("offset advanced over a partial line: %d -> %d", offsetBefore, tl.offset)
	}

	// Now complete the line; it should appear exactly once.
	appendToFile(t, path, ` written"}]}}`+"\n")
	msgs, err = tl.read()
	if err != nil {
		t.Fatalf("completed read: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "half written" {
		t.Errorf("completed read got %d messages (%v), want 1 'half written'", len(msgs), msgs)
	}
}
