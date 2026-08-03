// Package ui holds the shared lipgloss palette and styles.
//
// Everything rendered is lowercase. The only exception is team abbreviations,
// which stay uppercase because lowercase team codes are hard to read at a glance.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette. The six position hues are SLEEPER'S OWN, sampled off its roster
// editor, because the board is read next to Sleeper's draft screen and a
// receiver being cyan here and blue there costs a beat every glance. Matching
// costs nothing on Sleeper's side of the split and buys the whole thing.
//
// It does cost something here, and the cost lands on the alert colours. This
// palette used to reserve red, amber and green for alert states and keep the
// positions in the blue/cyan/purple/orange/mauve/slate band; Sleeper spends red
// on QB, green on RB and amber on TE, so that reservation is gone and the
// dodging now runs the other way. Three adjacencies are real, all of them
// same-line, and the alert hues below are picked to survive them:
//
//	falling numbers beside a TE name   apricot #F3B167 vs the run colour
//	"last one in tier" beside a qb tag rose    #EA436F vs the cliff colour
//	"safe to wait" beside an rb tag    mint    #5DCBB8 vs the wait colour
//
// Which is why cliff is a pure red rather than the old rose (its blue channel
// is what separates it from QB) and run is yellow rather than the old amber
// (which was within a few points of Sleeper's TE on every channel). Green
// survived untouched: lime and mint are already far apart in blue.
const (
	ColQB  = "#EA436F" // rose
	ColRB  = "#5DCBB8" // mint
	ColWR  = "#6CA5F8" // cornflower
	ColTE  = "#F3B167" // apricot
	ColK   = "#B16AF7" // violet
	ColDEF = "#7C879F" // slate

	ColCliff = "#FF3B3B" // red: last one in tier. pure, never rose — that is QB
	ColRun   = "#FFEA3D" // yellow: positional run / tier ending. lemon, never amber — that is TE
	ColWait  = "#9ECE6A" // green: safe to wait. lime, not mint — mint is RB

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
//
// The faint does more work than it used to. The old palette gave K and DEF the
// two least saturated hues so they could not fight the skill positions for
// attention even at full strength; Sleeper's kicker is a vivid violet, so on
// this palette the faint is the ONLY thing keeping round-2 kickers quiet. Do not
// drop it as decoration.
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
