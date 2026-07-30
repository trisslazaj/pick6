// Package ui holds the shared lipgloss palette and styles.
//
// Everything rendered is lowercase. The only exception is team abbreviations,
// which stay uppercase because lowercase team codes are hard to read at a glance.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette. Every position gets its own hue; red, amber and green are reserved
// for alert states, so the six position colours live in the
// blue/cyan/purple/orange/mauve/slate band.
const (
	ColQB  = "#7AA2F7" // blue
	ColRB  = "#FF9E64" // orange, never coral — must not read as the cliff red
	ColWR  = "#2AC3DE" // cyan
	ColTE  = "#BB9AF7" // purple
	ColK   = "#D6A2C8" // mauve
	ColDEF = "#8FA3B8" // slate

	ColCliff = "#F7768E" // red: last one in tier
	ColRun   = "#E0AF68" // amber: positional run / tier ending
	ColWait  = "#9ECE6A" // green: safe to wait

	ColFG     = "#C0CAF5"
	ColDim    = "#565F89"
	ColBorder = "#414868"
	ColAccent = "#7DCFFF"

	// ColInk is what goes ON an alert colour — the background is doing the
	// shouting, so the text has to step back out of its way. Shared by the alert
	// banner and the injury chip so the two cannot drift apart.
	ColInk = "#1A1B26"
)

var posColor = map[string]string{
	"QB": ColQB, "RB": ColRB, "WR": ColWR,
	"TE": ColTE, "K": ColK, "DEF": ColDEF,
}

// Pos styles a position tag or a player name in that position's colour.
// K and DEF render faint while the engine is suppressing them — same hue, less
// intensity, so the change to full strength is itself the "kicker o'clock" signal.
func Pos(pos string, suppressed bool) lipgloss.Style {
	c, ok := posColor[pos]
	if !ok {
		c = ColFG
	}
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	if suppressed && (pos == "K" || pos == "DEF") {
		s = s.Faint(true)
	}
	return s
}

var (
	Dim    = lipgloss.NewStyle().Foreground(lipgloss.Color(ColDim))
	FG     = lipgloss.NewStyle().Foreground(lipgloss.Color(ColFG))
	Cliff  = lipgloss.NewStyle().Foreground(lipgloss.Color(ColCliff))
	Run    = lipgloss.NewStyle().Foreground(lipgloss.Color(ColRun))
	Wait   = lipgloss.NewStyle().Foreground(lipgloss.Color(ColWait))
	Accent = lipgloss.NewStyle().Foreground(lipgloss.Color(ColAccent))
	Bold   = lipgloss.NewStyle().Bold(true)

	// ChipAlarm is the injury badge: ink on the cliff red, the same treatment the
	// cliff banner gets, so the two loudest things on the board read as one
	// family. Reverse video rather than red text follows the deny chip's
	// precedent — a chip is a badge, not a category — and a filled badge survives
	// the 256-colour downgrade legibly, which three letters of red text beside a
	// coloured name does not.
	ChipAlarm = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColInk)).
			Background(lipgloss.Color(ColCliff)).
			Bold(true)
)
