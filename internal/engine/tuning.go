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

	// RuleOfThree is the numerator of the support floor: if something happened
	// zero times in n trials, the 95% upper bound on its rate is -ln(0.05)/n =
	// 2.996/n, i.e. about 3/n. FFC reports the earliest pick anyone in its
	// sample ever took a player at, so a horizon at or before that pick is one
	// no observed draft ever removed him inside, and the honest floor on his
	// survival is 1 - 3/n rather than whatever the logistic curve says.
	RuleOfThree = 3.0

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

	// EndgameSlack is what a bench pick is worth when I have exactly one spare
	// pick left over my unfilled starting slots. Half, because the situation is
	// genuinely half a problem: the flier is still affordable, and it spends the
	// only pick standing between me and a lineup with a hole in it. At R == U the
	// multiplier is 0 rather than this, which is not a tuneable — it is the
	// arithmetic of having no spare picks at all.
	EndgameSlack = 0.5

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
	// RunSurprise is how far above the market's own forecast the count has to
	// run before the window is a "run" rather than the base rate. The forecast
	// is the position mix of the next RunWindow available players by adp — in
	// round 1 the market expects the window to be nearly all backs and
	// receivers, so a flat 4-of-6 threshold fired on ~69% of round-1 windows by
	// chance and the banner meant nothing exactly when the room was watching
	// closest. A run is a SURPRISE in concentration, not concentration itself.
	RunSurprise = 2.0

	// Value fallback, when no source gives a value.
	ValueBase  = 250.0
	ValueDecay = 40.0

	// Freshness. Neither is engine math — the engine never asks how old its own
	// data is — but they are tuneable numbers a human will want to find, and
	// this file is where tuneable numbers live.
	//
	// StaleADPHours is when the live board should start saying so out loud. FFC
	// recomputes once a day, so a board over a day old has definitionally missed
	// a refresh, and injury status is frozen at the same moment adp is.
	StaleADPHours = 24
	// ADPWindowStaleDays is the other half of that question. FetchedAt can be
	// minutes old while the data behind it is days old, because every source is
	// disk-cached and a fetch that hits cache all the way through moves only the
	// timestamp. ffc's own sample window is the thing that actually went stale,
	// and it recomputes daily, so two days late means at least one refresh was
	// missed on their side too.
	ADPWindowStaleDays = 2
	// NewsFreshHours is how recently sleeper's news_updated must have moved for
	// the board to call it news. Two days: long enough to survive a fetch on
	// draft morning about something that broke friday night, short enough that
	// the chip means "go read something" rather than decorating half the board.
	NewsFreshHours = 48
	// TripwireSlack is how far past a player's worst observed pick the draft has
	// to run before the board says so. `low` is a maximum over ~1,187 ffc drafts,
	// and a room sitting one pick behind national adp on somebody is unremarkable
	// — two picks of slack keeps the chip off a rounding-level difference and
	// still fires the moment the room is genuinely behind every draft in the
	// sample at once.
	TripwireSlack = 2

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
