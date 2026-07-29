package engine

// Every tuneable number lives here so it's greppable in one place.
const (
	// Survival, v1 (ADP logistic).
	SigmaDefault     = 6.0    // fallback when a player has no observed stdev
	SigmaFromStdev   = 1.8138 // pi/sqrt(3): stdev of a logistic = scale * this
	SigmaMin         = 0.5
	SigmaMax         = 25.0
	SurviveThreshold = 0.5   // "he himself will keep": the safe-to-wait cutoff
	SurvivalExpClamp = 30.0  // softplus goes linear past this; keeps exp from overflowing
	UndraftedADP     = 999.0 // missing/zero ADP: off the drafted radar, always survives
	FallerSigmas     = 1.0   // picks past ADP, in units of own sigma, before a player is "falling"

	// The exactly-N tilt. Independent survivals expect sum(1-p) removals before
	// my turn; a draft makes exactly PicksUntilMine(). They disagree, measurably
	// and in one direction: the backtest over the real 2024 draft (15,923
	// predictions) put mean predicted survival at 0.8231 against 0.8844 observed,
	// with every reliability bin under 0.9 under-predicting. Solving for the
	// exponent c that reconciles them is the correction.
	//
	// TiltCMax brackets the bisection at [1/64, 64]. On the shipped 201-player
	// board c solves to 0.33..0.86 at every vantage measured, so the bracket is
	// wide enough to never bind on real data; it binds on toy boards, which is
	// where the clamps earn their keep.
	TiltCMax = 64.0
	TiltTol  = 1e-6 // bisection width; ~26 halvings of the bracket

	// EBestEpsilon stops the expected-best walk once "everyone better is gone"
	// gets this unlikely. The remaining terms cannot move the answer by more
	// than epsilon times a value, i.e. under a millionth of a point.
	EBestEpsilon = 1e-6

	// Tier hold: the probability at least one player in the current tier reaches
	// my next pick. Cliff levels read this instead of the raw remaining count,
	// because three players the room is about to eat is a worse cliff than one
	// player nobody wants.
	TierHoldWarn  = 0.5
	TierHoldCliff = 0.15

	// Need weights.
	NeedStarter = 1.0  // an unfilled dedicated starter slot exists for pos
	NeedFlex    = 0.6  // dedicated slots filled, FLEX open, pos is flex-eligible
	NeedBench   = 0.25 // otherwise

	// Tiers. Mirrored in internal/adp, which precomputes them at fetch time.
	TierDropPct  = 0.10
	TierFloorPct = 0.015
	TierMaxSize  = 8
	TierMinSize  = 2

	// ByeConflictThreshold is how many starters must share a bye week before it's
	// worth saying anything. Two is unremarkable across a nine-slot lineup; three
	// is a week you cannot field a legal team without scrambling.
	ByeConflictThreshold = 3

	// Runs. (The old CliffWarn count threshold is gone: cliff levels come from
	// TierHoldWarn/TierHoldCliff now, and a leftover count nothing reads would
	// be the next reader's first wrong guess about how cliffs work.)
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

// SuperFlexEligible adds quarterbacks. Sleeper reports superflex as its own slot
// count, so a league that uses it gets modelled correctly rather than having its
// second QB spill onto the bench.
var SuperFlexEligible = map[string]bool{"QB": true, "RB": true, "WR": true, "TE": true}
