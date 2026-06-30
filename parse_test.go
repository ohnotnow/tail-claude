package main

import "testing"

func TestEncodeProjectPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain code path", "/Users/test-user/Documents/code/tail-claude", "-Users-test-user-Documents-code-tail-claude"},
		{"dotted path (.claude)", "/Users/test-user/.claude", "-Users-test-user--claude"},
		{"dotted nested", "/Users/test-user/.claude/skills/skill-creator", "-Users-test-user--claude-skills-skill-creator"},
		{"hyphens preserved", "/Users/test-user/Documents/code/claude-bumblebee", "-Users-test-user-Documents-code-claude-bumblebee"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := encodeProjectPath(tc.in); got != tc.want {
				t.Errorf("encodeProjectPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseLineProse(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantLen  int
		wantRole Role
		wantText string
	}{
		{
			name:    "assistant text block",
			line:    `{"type":"assistant","uuid":"a1","timestamp":"2026-06-30T17:35:00.000Z","message":{"content":[{"type":"text","text":"Hello there"}]}}`,
			wantLen: 1, wantRole: RoleAssistant, wantText: "Hello there",
		},
		{
			name:    "assistant joins multiple text blocks",
			line:    `{"type":"assistant","uuid":"a2","message":{"content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}}`,
			wantLen: 1, wantRole: RoleAssistant, wantText: "one\ntwo",
		},
		{
			name:    "assistant Bash tool_use is not surfaced",
			line:    `{"type":"assistant","uuid":"a3","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
			wantLen: 0,
		},
		{
			name:    "assistant Read tool_use is not surfaced",
			line:    `{"type":"assistant","uuid":"a4","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`,
			wantLen: 0,
		},
		{
			name:    "empty thinking block is skipped",
			line:    `{"type":"assistant","uuid":"a5","message":{"content":[{"type":"thinking","thinking":"","signature":"abc"}]}}`,
			wantLen: 0,
		},
		{
			name:    "user typed prompt (string content)",
			line:    `{"type":"user","uuid":"u1","timestamp":"2026-06-30T17:34:00Z","message":{"content":"Hi, can you help?"}}`,
			wantLen: 1, wantRole: RoleUser, wantText: "Hi, can you help?",
		},
		{
			name:    "user text block (array content)",
			line:    `{"type":"user","uuid":"u2","message":{"content":[{"type":"text","text":"in an array"}]}}`,
			wantLen: 1, wantRole: RoleUser, wantText: "in an array",
		},
		{
			name:    "user tool_result only is skipped",
			line:    `{"type":"user","uuid":"u3","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"output"}]}}`,
			wantLen: 0,
		},
		{
			name:    "system line is skipped",
			line:    `{"type":"system","uuid":"s1","message":{"content":"some system note"}}`,
			wantLen: 0,
		},
		{
			name:    "line with no message field is skipped",
			line:    `{"type":"file-history-snapshot","uuid":"x1"}`,
			wantLen: 0,
		},
		{name: "blank line is skipped", line: `   `, wantLen: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := ParseLine([]byte(tc.line))
			if err != nil {
				t.Fatalf("ParseLine returned error: %v", err)
			}
			if len(msgs) != tc.wantLen {
				t.Fatalf("got %d messages, want %d (%+v)", len(msgs), tc.wantLen, msgs)
			}
			if tc.wantLen == 1 {
				if msgs[0].Role != tc.wantRole {
					t.Errorf("role = %v, want %v", msgs[0].Role, tc.wantRole)
				}
				if msgs[0].Text != tc.wantText {
					t.Errorf("text = %q, want %q", msgs[0].Text, tc.wantText)
				}
			}
		})
	}
}

func TestParseLineTools(t *testing.T) {
	t.Run("Edit emits a tool event with old/new", func(t *testing.T) {
		line := `{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/proj/ui.go","old_string":"foo","new_string":"bar","replace_all":false}}]}}`
		msgs, err := ParseLine([]byte(line))
		if err != nil || len(msgs) != 1 {
			t.Fatalf("got %d msgs, err %v", len(msgs), err)
		}
		ev := msgs[0].Tool
		if ev == nil {
			t.Fatal("expected a tool event")
		}
		if ev.Name != "Edit" || ev.Path != "/proj/ui.go" || ev.Old != "foo" || ev.New != "bar" {
			t.Errorf("tool event = %+v", ev)
		}
	})

	t.Run("Write emits a tool event with content as New", func(t *testing.T) {
		line := `{"type":"assistant","uuid":"a2","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/proj/README.md","content":"# Title\nbody"}}]}}`
		msgs, _ := ParseLine([]byte(line))
		if len(msgs) != 1 || msgs[0].Tool == nil {
			t.Fatalf("expected one tool event, got %+v", msgs)
		}
		ev := msgs[0].Tool
		if ev.Name != "Write" || ev.Path != "/proj/README.md" || ev.New != "# Title\nbody" || ev.Old != "" {
			t.Errorf("tool event = %+v", ev)
		}
	})

	t.Run("text + Edit in one turn yields prose then tool", func(t *testing.T) {
		line := `{"type":"assistant","uuid":"a3","message":{"content":[{"type":"text","text":"let me fix that"},{"type":"tool_use","name":"Edit","input":{"file_path":"/x","old_string":"a","new_string":"b"}}]}}`
		msgs, _ := ParseLine([]byte(line))
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
		if msgs[0].Tool != nil || msgs[0].Text != "let me fix that" {
			t.Errorf("first message should be prose: %+v", msgs[0])
		}
		if msgs[1].Tool == nil || msgs[1].Tool.Name != "Edit" {
			t.Errorf("second message should be an Edit tool event: %+v", msgs[1])
		}
	})

	t.Run("two parallel Edits yield two tool events", func(t *testing.T) {
		line := `{"type":"assistant","uuid":"a4","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/x","old_string":"1","new_string":"2"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/y","old_string":"3","new_string":"4"}}]}}`
		msgs, _ := ParseLine([]byte(line))
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
	})
}

func TestParseLineMalformed(t *testing.T) {
	_, err := ParseLine([]byte(`{"type":"assistant", broken`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestParseTimeTolerant(t *testing.T) {
	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Errorf("parseTime(bad) = %v, want zero", got)
	}
	if got := parseTime(""); !got.IsZero() {
		t.Errorf("parseTime(empty) = %v, want zero", got)
	}
	if got := parseTime("2026-06-30T17:35:00Z"); got.IsZero() {
		t.Error("parseTime(valid RFC3339) returned zero")
	}
}
