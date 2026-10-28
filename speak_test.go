package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSpeakableKeepsOnlyAssistantProse(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Text: "a prompt"},
		{Role: RoleAssistant, Text: "some prose"},
		{Role: RoleAssistant, Tool: &ToolEvent{Name: "Edit", Path: "main.go"}},
		{Role: RoleAssistant, Text: "  \n"},
	}
	got := speakable(msgs, false)
	if len(got) != 1 || got[0] != "some prose" {
		t.Fatalf("speakable = %q, want just the assistant prose", got)
	}
}

// TestSpeakLoopSkipsBacklog: messages already in the session when --speak
// starts are not read out; only ones that land afterwards are.
func TestSpeakLoopSkipsBacklog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendToFile(t, path, lineUser+lineAsst)

	said := make(chan string, 10)
	go speakLoop("", path, false, func(s string) { said <- s })

	time.Sleep(2 * pollInterval) // let it skip the backlog before anything new lands
	appendToFile(t, path, lineAsst2)

	select {
	case got := <-said:
		if got != "second reply" {
			t.Fatalf("spoke %q, want %q (backlog should be skipped)", got, "second reply")
		}
	case <-time.After(5 * pollInterval):
		t.Fatal("new assistant message was never spoken")
	}
}

// TestSpeakableMidTurnOnly is --blether: the final reply (end_turn) is left to
// the Stop hook, and only the commentary between tool calls is spoken.
func TestSpeakableMidTurnOnly(t *testing.T) {
	commentary := `{"type":"assistant","uuid":"a1","message":{"stop_reason":"tool_use","content":[{"type":"text","text":"checking the tests"}]}}`
	final := `{"type":"assistant","uuid":"a2","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"all done"}]}}`

	var msgs []Message
	for _, line := range []string{commentary, final} {
		ms, err := ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		msgs = append(msgs, ms...)
	}

	if got := speakable(msgs, true); len(got) != 1 || got[0] != "checking the tests" {
		t.Errorf("midTurnOnly speakable = %q, want just the commentary", got)
	}
	if got := speakable(msgs, false); len(got) != 2 {
		t.Errorf("speakable = %q, want both messages", got)
	}
}
