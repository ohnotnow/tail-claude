package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tailer follows a single session file by byte offset, and can re-target to a
// newer session file if a fresh session starts. It reads only *complete* lines
// (ending in a newline), so a half-written final line written by Claude Code
// mid-flush is left in place and picked up on the next read.
type tailer struct {
	cwd    string // project dir, used to spot newer sessions; "" disables re-targeting
	path   string // file currently being followed
	offset int64  // byte offset of the next unread line
}

// retarget switches to a newer session file if one has appeared, resetting the
// offset to the start of it. It returns true if it switched — the caller should
// then treat the subsequent read as a full (re)load.
func (t *tailer) retarget() bool {
	if t.cwd == "" {
		return false
	}
	newest, err := findNewestSession(t.cwd)
	if err != nil || newest == t.path {
		return false
	}
	t.path = newest
	t.offset = 0
	return true
}

// read consumes complete new lines from t.path starting at t.offset, advancing
// the offset only past lines that ended in a newline. bufio.Reader.ReadBytes
// grows to fit arbitrarily long lines, so long assistant turns are no problem.
func (t *tailer) read() ([]Message, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}

	r := bufio.NewReaderSize(f, 1024*1024)
	var msgs []Message
	for {
		line, err := r.ReadBytes('\n')
		if err == nil {
			t.offset += int64(len(line))
			if ms, perr := ParseLine(line); perr == nil {
				msgs = append(msgs, ms...)
			}
			continue
		}
		if err == io.EOF {
			break // a trailing partial line (if any) is left for the next read
		}
		return msgs, err
	}
	return msgs, nil
}

// loadMessages reads an entire session file in one shot (used by --print).
func loadMessages(path string) ([]Message, error) {
	t := tailer{path: path}
	return t.read()
}

// encodeProjectPath turns an absolute project path into the directory name
// Claude Code uses under ~/.claude/projects/.
//
// VERIFIED 2026-06-30 against real session dirs: both '/' AND '.' are replaced
// with '-' (e.g. /Users/test-user/.claude -> -Users-test-user--claude). The PRD's
// "every / replaced by -" was incomplete; a dotted path would not be found
// under the documented rule.
func encodeProjectPath(abs string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(abs)
}

// projectDir returns ~/.claude/projects/{encoded-cwd}.
func projectDir(cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects", encodeProjectPath(cwd)), nil
}

// findNewestSession returns the path of the newest-modified *.jsonl in the
// project directory for cwd. The newest file is the current live session.
func findNewestSession(cwd string) (string, error) {
	dir, err := projectDir(cwd)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no session directory for this project (%s): %w", dir, err)
	}

	var newest string
	var newestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest = filepath.Join(dir, e.Name())
			newestMod = info.ModTime()
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no .jsonl session files in %s", dir)
	}
	return newest, nil
}
