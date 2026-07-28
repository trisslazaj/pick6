package engine

// Every tuneable number lives here so it's greppable in one place.
const (
	// Survival, v1 (ADP logistic).
	SigmaDefault     = 6.0    // fallback when a player has no observed stdev
	SigmaFromStdev   = 1.8138 // pi/sqrt(3): stdev of a logistic = scale * this
	SigmaMin         = 0.5
	SigmaMax         = 25.0
	SurviveThreshold = 0.5 // bestLater candidate cutoff

	// Need weights.
	NeedStarter = 1.0  // an unfilled dedicated starter slot exists for pos
	NeedFlex    = 0.6  // dedicated slots filled, FLEX open, pos is flex-eligible
	NeedBench   = 0.25 // otherwise

	// Tiers. Mirrored in internal/adp, which precomputes them at fetch time.
	TierDropPct  = 0.10
	TierFloorPct = 0.015
	TierMaxSize  = 8
	TierMinSize  = 2

	// Cliffs and runs.
	CliffWarn    = 2 // players left in tier for amber
	RunWindow    = 6 // sliding window of recent picks
	RunThreshold = 4 // shared positions in that window to call a run

	// Value fallback, when no source gives a value.
	ValueBase  = 250.0
	ValueDecay = 40.0

	// K/DEF suppression. Two different questions, deliberately two constants:
	// what we're willing to recommend, and what the room actually does.
	KDefLastRounds = 3
	// OpponentKDefLastRounds is looser because this league takes its first kicker
	// in round 10. Used by engine v2's opponent model only.
	OpponentKDefLastRounds = 6
)

// DefaultRoster is the league shape we assume when Sleeper metadata is absent.
var DefaultRoster = Roster{
	Slots: []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "K", "DEF"},
	Bench: 6,
}

// FlexEligible positions, for filling the FLEX slot.
var FlexEligible = map[string]bool{"RB": true, "WR": true, "TE": true}
