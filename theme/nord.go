// Nord palette for Bubble Tea / Charm apps.
//
// Drop this file into your project as `theme/nord.go`. Use the semantic
// aliases (Accent, Success, Error, ...) in your code rather than the raw
// Nord* names — switching to Gruvbox or Catppuccin later then becomes a
// one-file change to the alias mapping.
//
// Reference: https://www.nordtheme.com/docs/colors-and-palettes

package theme

import "github.com/charmbracelet/lipgloss"

// Polar Night (backgrounds)
var (
	Nord0 = lipgloss.Color("#2e3440")
	Nord1 = lipgloss.Color("#3b4252")
	Nord2 = lipgloss.Color("#434c5e")
	Nord3 = lipgloss.Color("#4c566a")
)

// Snow Storm (foregrounds)
var (
	Nord4 = lipgloss.Color("#d8dee9")
	Nord5 = lipgloss.Color("#e5e9f0")
	Nord6 = lipgloss.Color("#eceff4")
)

// Frost (blues / teals — primary accents)
var (
	Nord7  = lipgloss.Color("#8fbcbb") // teal
	Nord8  = lipgloss.Color("#88c0d0") // light blue
	Nord9  = lipgloss.Color("#81a1c1") // blue
	Nord10 = lipgloss.Color("#5e81ac") // dark blue
)

// Aurora (reds / oranges / yellows / greens / purples)
var (
	Nord11 = lipgloss.Color("#bf616a") // red
	Nord12 = lipgloss.Color("#d08770") // orange
	Nord13 = lipgloss.Color("#ebcb8b") // yellow
	Nord14 = lipgloss.Color("#a3be8c") // green
	Nord15 = lipgloss.Color("#b48ead") // purple
)

// Semantic aliases — use these in your app, not the raw Nord* names.
var (
	Bg      = Nord0
	BgAlt   = Nord1
	Fg      = Nord4
	FgMuted = Nord3
	Accent  = Nord8
	Success = Nord14
	Warning = Nord13
	Error   = Nord11
	Info    = Nord9
)

// Common lipgloss styles. Extend per-component as needed.
var (
	Title    = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	Selected = lipgloss.NewStyle().Foreground(Nord0).Background(Accent).Bold(true)
	Normal   = lipgloss.NewStyle().Foreground(Fg)
	Faint    = lipgloss.NewStyle().Foreground(FgMuted)
	Border   = lipgloss.NewStyle().BorderForeground(Nord3)
)
