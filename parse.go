package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Role distinguishes who produced a message in the timeline.
type Role int

const (
	RoleAssistant Role = iota
	RoleUser
)

func (r Role) String() string {
	if r == RoleUser {
		return "user"
	}
	return "assistant"
}

// ToolEvent is a surfaced file-mutating tool call (Edit or Write). Only the
// "doing" tools that aren't already narrated by surrounding prose are surfaced
// — see ant tc-AkRXV / the tool-surfacing epic.
type ToolEvent struct {
	Name string // "Edit" or "Write"
	Path string // file_path
	Old  string // Edit: old_string (empty for Write)
	New  string // Edit: new_string; Write: content
}

// Message is a single timeline entry: assistant prose, a user prompt, or — when
// Tool != nil — a surfaced tool action. Tool calls we don't surface, tool
// results, system lines and the (empty) thinking blocks are filtered out.
type Message struct {
	UUID      string // stable per-message id — reference by this, never line numbers
	Role      Role
	Timestamp time.Time  // already converted to local time
	Text      string     // surfaced prose (empty for tool events)
	Tool      *ToolEvent // non-nil iff this entry is a surfaced tool action
}

// surfacedTools is the NARROW set: the file-mutating tools whose action vanishes
// without the surrounding prose describing what changed.
var surfacedTools = map[string]bool{"Edit": true, "Write": true}

// --- raw JSONL shapes (only the fields we actually use) ---

type rawLine struct {
	Type      string      `json:"type"` // "assistant", "user", "system", ...
	UUID      string      `json:"uuid"`
	Timestamp string      `json:"timestamp"` // ISO-8601
	Message   *rawMessage `json:"message"`
}

type rawMessage struct {
	Content json.RawMessage `json:"content"` // string OR []block — handled below
}

type rawBlock struct {
	Type  string          `json:"type"` // "text", "thinking", "tool_use", "tool_result"
	Text  string          `json:"text"`
	Name  string          `json:"name"`  // tool_use: the tool name
	Input json.RawMessage `json:"input"` // tool_use: the tool arguments
	// thinking blocks carry only an encrypted "signature" on disk; their
	// "thinking" text is always empty (verified) — see PRD §2. We never read it.
}

type toolInput struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
}

// ParseLine parses one JSONL line into zero or more timeline messages. One
// assistant turn can yield a prose message plus one tool event per surfaced
// tool_use block (parallel tool calls happen). Returns:
//
//	(msgs, nil) — zero or more renderable messages (nil/empty means "skip")
//	(nil, err)  — a malformed line (the caller may skip and continue)
func ParseLine(data []byte) ([]Message, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var rl rawLine
	if err := json.Unmarshal(data, &rl); err != nil {
		return nil, err
	}
	if rl.Message == nil {
		return nil, nil
	}

	switch rl.Type {
	case "assistant":
		return assistantMessages(rl), nil
	case "user":
		text := userText(rl.Message.Content)
		if strings.TrimSpace(text) == "" {
			return nil, nil // tool_result-only synthetic turn, no prose
		}
		return []Message{{
			UUID:      rl.UUID,
			Role:      RoleUser,
			Timestamp: parseTime(rl.Timestamp),
			Text:      text,
		}}, nil
	default:
		return nil, nil
	}
}

// assistantMessages turns one assistant line into its surfaced entries: a prose
// message (if it has text) followed by a tool event for each surfaced tool_use.
func assistantMessages(rl rawLine) []Message {
	var blocks []rawBlock
	if err := json.Unmarshal(rl.Message.Content, &blocks); err != nil {
		return nil
	}
	ts := parseTime(rl.Timestamp)

	var textParts []string
	var tools []Message
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				textParts = append(textParts, b.Text)
			}
		case "tool_use":
			if ev := toolEventFor(b); ev != nil {
				tools = append(tools, Message{UUID: rl.UUID, Role: RoleAssistant, Timestamp: ts, Tool: ev})
			}
		}
	}

	var out []Message
	if len(textParts) > 0 {
		out = append(out, Message{
			UUID:      rl.UUID,
			Role:      RoleAssistant,
			Timestamp: ts,
			Text:      strings.Join(textParts, "\n"),
		})
	}
	return append(out, tools...)
}

// toolEventFor builds a ToolEvent from a tool_use block, or nil if it's not a
// tool we surface.
func toolEventFor(b rawBlock) *ToolEvent {
	if !surfacedTools[b.Name] {
		return nil
	}
	var in toolInput
	_ = json.Unmarshal(b.Input, &in)
	ev := &ToolEvent{Name: b.Name, Path: in.FilePath}
	if b.Name == "Edit" {
		ev.Old, ev.New = in.OldString, in.NewString
	} else { // Write
		ev.New = in.Content
	}
	return ev
}

// blocksText extracts and joins the surfaced "text" blocks from a content
// array, ignoring tool_use / thinking / tool_result blocks.
func blocksText(raw json.RawMessage) string {
	var blocks []rawBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// userText handles the two shapes a user message's content can take: a bare
// string (a typed prompt) or an array of blocks (tool_results we skip, or text
// blocks we keep).
func userText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return blocksText(raw)
}

// parseTime parses an ISO-8601 timestamp into local time, tolerating both
// second and nanosecond precision. A bad/empty value yields the zero time
// rather than an error — a missing timestamp must never crash the tail.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Local()
	}
	return time.Time{}
}
