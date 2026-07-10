package main

import (
	"regexp"
	"strings"
)

// Baked-in defaults so tc is useful the instant it runs. The config file
// extends/overrides these; it is never required.
//
// These lists are EMPIRICALLY CORRECTED — see ant foundation tc-AkRXV. Across
// 127 real sessions, "actually" was 77% of all brake hits yet is a discourse
// marker that frequently appears *inside* confident prose ("here's what I
// actually found"). It is deliberately excluded.
var defaultBrakeWords = []string{
	"wait", "hold on", "should we", "let me verify", "let me check",
	"before we", "correction", "i'm not sure", "i was wrong", "hmm",
}

var defaultDoomWords = []string{
	"the good news is", "should be fine", "will get caught",
	"nothing loose", "powering on", "all green", "🎉",
}

const defaultThreshold = 6

// matcher compiles phrases into case-insensitive regexes. Alphanumeric-edged
// phrases get word boundaries (so "wait" doesn't fire on "waiting"); others
// (emoji, punctuation-edged) match as plain substrings.
type matcher struct {
	pats []*regexp.Regexp
}

func newMatcher(phrases []string) *matcher {
	m := &matcher{}
	for _, p := range phrases {
		if p == "" {
			continue
		}
		left, right := "", ""
		if isASCIIAlnum(p[0]) {
			left = `\b`
		}
		if isASCIIAlnum(p[len(p)-1]) {
			right = `\b`
		}
		if re, err := regexp.Compile(`(?i)` + left + regexp.QuoteMeta(p) + right); err == nil {
			m.pats = append(m.pats, re)
		}
	}
	return m
}

// jsSources exports the compiled patterns for the browser (tc --web), keeping
// the word lists single-source. Go's inline "(?i)" flag is not JavaScript
// regex syntax, so it is stripped and the client compiles with the "i" flag
// instead. Everything else newMatcher builds (QuoteMeta escapes, \b word
// boundaries) is valid in both engines — provided the client does NOT use
// JavaScript's "u" flag, which rejects identity escapes like \! that
// QuoteMeta can produce.
func (m *matcher) jsSources() []string {
	srcs := make([]string, 0, len(m.pats))
	for _, re := range m.pats {
		srcs = append(srcs, strings.TrimPrefix(re.String(), "(?i)"))
	}
	return srcs
}

func (m *matcher) matchesAny(text string) bool {
	for _, re := range m.pats {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// highlight wraps every match in reverse-video (\e[7m … \e[27m). Reverse video
// is used deliberately: it makes the phrase pop using whatever colours glamour
// already set, and toggling it off (\e[27m) leaves glamour's own foreground /
// background state untouched — so there's no colour-bleed into the rest of the
// paragraph the way a full reset (\e[0m) would cause.
func (m *matcher) highlight(text string) string {
	for _, re := range m.pats {
		text = re.ReplaceAllString(text, "\x1b[7m${0}\x1b[27m")
	}
	return text
}

// brakeDrought counts the assistant turns at the tail of the conversation that
// contain no brake-word — i.e. "N assistant turns since the last brake-word".
// User turns are ignored. This is the PRIMARY signal: it tracks the *absence*
// of brakes, because the dangerous session is word-quiet (PRD §5).
func brakeDrought(msgs []Message, brake *matcher) int {
	drought := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		// Only assistant PROSE turns count. Tool events are assistant-role with
		// empty text; counting them would inflate the drought and break the
		// empirical calibration of the threshold.
		if msgs[i].Role != RoleAssistant || msgs[i].Tool != nil {
			continue
		}
		if brake.matchesAny(msgs[i].Text) {
			return drought
		}
		drought++
	}
	return drought
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
