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

// Every hue is still Sleeper's own, which is the whole principle: the board is
// read beside another screen all night, and a receiver that is cyan here and
// blue there costs a beat every glance. Fpl reuses them rather than inventing a
// palette, because the four positions it needs are four Sleeper already has and
// the eye has already learned them here.
var posColor = map[string]string{
	"QB": ColQB, "RB": ColRB, "WR": ColWR,
	"TE": ColTE, "K": ColK, "DEF": ColDEF,
}

// fplColor is the second vocabulary, and it exists for ONE string.
//
// Gkp takes violet, the hue nfl spends on the kicker; mid takes mint, the
// busiest position getting the colour nfl gives its busiest; fwd takes rose.
// None of those collide, so they could have gone in the map above.
//
// DEF is why they didn't. It means opposite things in the two sports — one team
// unit per club in nfl, a hundred and eighty-four outfield defenders in fpl —
// and slate is the colour nfl gives it BECAUSE it is a unit you draft last and
// think about least: the least saturated hue in the palette, twelve degrees of
// hue and 1.7:1 of luminance from the dim prose it sits beside. That is exactly
// right for a position rendered faint for twelve of sixteen rounds.
//
// On an fpl board it was unreadable. Defenders are a third of the pool, they are
// never suppressed, and on the opening frame DEF is the TOP-RANKED position —
// so the most important row on the board was the one row you could not see.
// Eyeballed, not reasoned: `board -sport fpl -snapshot` through freeze.
// Cornflower is nfl's receiver blue, which fpl leaves unemployed, and it is
// clear of violet, mint and rose.
var fplColor = map[string]string{
	"GKP": ColK, "DEF": ColWR, "MID": ColRB, "FWD": ColQB,
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
//
// Which positions are suppressed is the caller's question, not this one's —
// ask engine.State.Suppressed and pass the answer. This used to re-check
// K/DEF here, which was a belt on top of the one caller that asked with
// Need == 0; a sport whose suppressed set is empty (or somebody else's) has
// every right to un-faint everything.
func Pos(pos string, suppressed bool) lipgloss.Style { return posStyle(posColor, pos, suppressed) }

// PosFPL is Pos read in fpl's vocabulary, for the printers outside a Board.
func PosFPL(pos string, suppressed bool) lipgloss.Style { return posStyle(fplColor, pos, suppressed) }

// pos is Pos in whichever vocabulary THIS board is drawn in. Every renderer in
// the package is a Board method, so this is the one every one of them calls and
// the package function is left for `pick6 tiers`, which has no board.
func (b Board) pos(pos string, suppressed bool) lipgloss.Style {
	if b.State != nil && b.State.Capped() {
		return PosFPL(pos, suppressed)
	}
	return Pos(pos, suppressed)
}

func posStyle(palette map[string]string, pos string, suppressed bool) lipgloss.Style {
	c, ok := palette[pos]
	if !ok {
		c = ColFG
	}
	s := lipgloss.NewStyle().Foreground(lipgloss.Color(c))
	if suppressed {
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

	// ChipNote is the opinion badge — the rankings file's "avoid" beside a name.
	// Reverse video on dim rather than a hue: every position colour and alert
	// colour on this board means a fact somewhere, and an opinion the board is
	// merely repeating stays in the grey band. Same badge-over-text reasoning as
	// ChipAlarm, one notch quieter.
	ChipNote = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColInk)).
			Background(lipgloss.Color(ColDim))
)

// PriceNoun is what this board calls the number in its price column.
//
// Nfl's is an average draft position: pick units, a mean over a thousand real
// drafts, with a measured spread under it. Fpl's is fpl's own draft_rank — an
// ordering, and nothing but — so printing "adp 41.0" beside it claims a
// measurement nobody made, and the ".0" claims a precision a rank does not have.
//
// The arithmetic around it survives the change and only the noun moves: a
// ten-manager fpl draft spends 150 picks over the top 150 ranks, so "12 before
// his price" reads as true there as it does on an adp board. What does not
// survive is the decimal, hence Digits.
//
// One method rather than a sport flag threaded to eight print sites, because
// eight print sites is how the verdict says rank and the search overlay says adp
// about the same number.
func (b Board) PriceNoun() string {
	if b.State != nil && b.State.Capped() {
		return "rank"
	}
	return "adp"
}

// PriceFmt is "%.1f" for a measured mean and "%.0f" for a rank, because a
// trailing .0 on an integer ordering is exactly the tell that it is a mean.
func (b Board) PriceFmt() string {
	if b.PriceNoun() == "rank" {
		return "%.0f"
	}
	return "%.1f"
}
