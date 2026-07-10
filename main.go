package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	fs := flag.NewFlagSet("tc", flag.ContinueOnError)
	dir := fs.String("dir", "", "project directory to watch (default: current directory)")
	printMode := fs.Bool("print", false, "print the timeline to stdout and exit (no TUI)")
	webMode := fs.Bool("web", false, "serve the timeline to a browser instead of the TUI")
	port := fs.Int("port", 8467, `port for --web (8467 is "TC" in ASCII)`)
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	cwd := *dir
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			fmt.Fprintln(os.Stderr, "tc: cannot determine current directory:", err)
			os.Exit(1)
		}
	}

	path, err := findNewestSession(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tc:", err)
		os.Exit(1)
	}

	if *printMode {
		msgs, err := loadMessages(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tc:", err)
			os.Exit(1)
		}
		printTimeline(msgs)
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		// A broken config shouldn't stop the watcher — warn and use defaults.
		fmt.Fprintln(os.Stderr, "tc: ignoring config:", err)
		cfg = Config{}
	}

	if *webMode {
		if err := runWeb(cwd, path, cfg, *port); err != nil {
			fmt.Fprintln(os.Stderr, "tc:", err)
			os.Exit(1)
		}
		return
	}

	p := tea.NewProgram(newModel(cwd, path, cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tc:", err)
		os.Exit(1)
	}
}

// printTimeline writes the surfaced assistant/user timeline to stdout. This is
// the read-only debug view (tc --print) that proves the data plumbing without
// the TUI (PRD §9).
func printTimeline(msgs []Message) {
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for i := range msgs {
		fmt.Fprint(w, formatMessage(&msgs[i]))
	}
}

// formatMessage renders one timeline message for the plain-text stream, with
// user and assistant turns visually distinct via ANSI colour.
func formatMessage(m *Message) string {
	ts := m.Timestamp.Format("15:04")
	const (
		cyan   = "\033[1;36m"
		green  = "\033[1;32m"
		yellow = "\033[1;33m"
		reset  = "\033[0m"
	)
	if m.Tool != nil {
		return fmt.Sprintf("\n%s[%s · %s]%s %s\n", yellow, ts, strings.ToUpper(m.Tool.Name), reset, m.Tool.Path)
	}
	if m.Role == RoleUser {
		return fmt.Sprintf("\n%s[%s · YOU]%s\n%s\n", cyan, ts, reset, m.Text)
	}
	return fmt.Sprintf("\n%s[%s · CLAUDE]%s\n%s\n", green, ts, reset, m.Text)
}
