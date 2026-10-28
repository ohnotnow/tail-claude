package main

import (
	"os/exec"
	"strings"
	"time"
)

// speakCommand is the TTS CLI that --speak pipes Claude's prose into. It reads
// markdown on stdin, so the text is passed through untouched.
const speakCommand = "readaloud"

// speakable returns the text of the assistant prose messages in msgs — not
// user prompts, not surfaced tool events. With midTurnOnly it keeps just the
// commentary between tool calls, leaving the final reply to the user's Stop
// hook (blether) so the two don't talk over each other.
func speakable(msgs []Message, midTurnOnly bool) []string {
	var out []string
	for _, m := range msgs {
		if midTurnOnly && m.StopReason != "tool_use" {
			continue
		}
		if m.Role == RoleAssistant && m.Tool == nil && strings.TrimSpace(m.Text) != "" {
			out = append(out, m.Text)
		}
	}
	return out
}

// speakLoop follows the session with its own tailer and speaks each new
// assistant message, one at a time. It starts at the end of the current
// session so the backlog isn't read out; a fresh session (re-target) is spoken
// from the top. Speaking blocks the loop, so a burst of messages queues up in
// the file rather than voices talking over each other.
func speakLoop(cwd, path string, midTurnOnly bool, say func(string)) {
	t := tailer{cwd: cwd, path: path}
	_, _ = t.read() // skip the backlog
	for range time.Tick(pollInterval) {
		t.retarget()
		msgs, err := t.read()
		if err != nil {
			continue // transient (file mid-rotate, etc.) — try again next tick
		}
		for _, text := range speakable(msgs, midTurnOnly) {
			say(text)
		}
	}
}

// sayAloud pipes text to speakCommand and waits for it to finish. Its output
// is discarded so it can't scribble over the TUI.
func sayAloud(text string) {
	cmd := exec.Command(speakCommand)
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}
