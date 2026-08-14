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
	//
	// Milestone 8 retired these from the OBJECTIVE and left them everywhere
	// else. They still price legPolicy's ordering, urgency, SafeToWait, the
	// opponents' own need model and the whole of adp mode — all of which are
	// policies and heuristics, which is where a guessed step function belongs.
	// What they no longer do is stand in for "what is a filled flex slot
	// worth", which RosterValue now answers by assignment.
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

	// MarketGapPicks is how far the market must price a player ahead of his
	// position's value-best before the board treats the disagreement as real.
	//
	// It lived in the ui as display judgement until milestone 8, and moved here
	// when the market's dissenting man stopped being a note and became a scored
	// candidate: the same threshold now decides what the rollouts spend futures
	// on, which makes it engine math. The judgement behind the number is
	// unchanged — the value column and the adp column disagree constantly by a
	// pick or two, and contesting every wobble would bury the one that matters
	// (rashee rice, priced ten picks ahead of the receiver the value curve
	// preferred).
	MarketGapPicks = 4

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
	//
	// 7, not 6, and the difference is the draft's LENGTH: "first kicker in round
	// 10" was read off a 15-round draft (remaining = 6), but the user's own room
	// — draft 1261824503076360192, the shape of the 2026 league — ran 16 rounds
	// and took two kickers in round 10 (picks 114 and 118), where remaining is
	// 7. At 6 the sim zeroes kicker need on picks the room actually spends on
	// kickers, which is exactly the corruption this constant exists to prevent.
	// Too loose by a round is the cheaper error: an early-round kicker never
	// cracks the candidate pool's top ranks anyway.
	OpponentKDefLastRounds = 7

	// Engine v2 (opponent-aware sim, sim.go). Tau is the ADP desirability decay
	// in ranks: an opponent is e times likelier to take the k-th best available
	// than the (k+Tau)-th. CandidatePool bounds who they'd consider at all —
	// nobody is taking the 60th-best player. Rollouts is how many futures one
	// recompute samples; at 500 the standard error on a 90% survival is ~1.3%,
	// under the board's own rounding.
	Tau           = 5.0
	CandidatePool = 25
	Rollouts      = 500

	// Milestone 7 (the conditioned lookahead, lookahead.go). PlanDepth is how
	// many of MY OWN picks a plan rollout plays through. Two ships; the
	// three-leg variant is implemented and tested (TestConditionedPlanAtDepthThree)
	// and stays off.
	//
	// It is off despite clearing the spec's stated bar. The wheel is where the
	// spec said to look and it looked back loudly: over 54 scripted frames at
	// slots 1 and 12, depth 3 changes the FIRST leg on 16 of them, and every
	// flip moves away from running back — which is what you would expect at the
	// wheel, where two back-to-back picks let you double-tap the deep position
	// later and take the scarce one now. That is a real strategic argument.
	//
	// What is missing is evidence it is RIGHT, and this repo does not ship a
	// change of that size on plausibility (see the room warp's cap, which won
	// on one fold, was kept as never-worse, and was eventually retracted). A
	// decision score has no labels, so the only honest promotion path is a
	// scenario fixture like the depth-2 one in dod_test.go, showing a wheel
	// frame where depth 2 is demonstrably wrong and depth 3 is right. Two
	// confounds to separate first: mustFill is computed FROM the leg count, so
	// depth 3 carries one more unit of feasibility pressure at the endgame and
	// some of those 16 flips may be that rather than the third leg; and the
	// cost is 115ms per pick event against 36ms, past the spec's ~50ms budget,
	// so promotion means dropping PlanRollouts too.
	// PlanDepth is RETIRED as of milestone 8 and kept only as documentation of
	// a question that dissolved. The conditioned rollouts now run to my LAST
	// pick, because the thing being scored is the finished roster; there is no
	// depth to choose, no third leg to promote, and no mustFill-versus-horizon
	// confound to separate. Nothing reads it.
	PlanDepth = 2
)

// The two numbers a harness sweeps rather than a reader tunes. Vars, not
// consts, because `pick6 regret` grades them against real drafts and a test
// that walked a scripted mock at full resolution would crawl.
var (
	// BenchWeight is what a bench body is worth inside U (roster.go), and it is
	// the ONE constant the milestone-8 objective owns. It inherits NeedBench's
	// role and its number: a player who is not in the lineup only scores through
	// byes, injuries and the flex churn, which is the same claim NeedBench was
	// making about a pick that fills no slot.
	BenchWeight = 0.25

	// PlanRollouts is its own number rather than Rollouts because the two want
	// different resolution: the survival column is a percentage a human reads
	// off the screen, the plan is a ranking.
	//
	// It stayed at 500 through milestone 8, which had pre-authorised a drop to
	// 300 for the full-horizon window. The drop was NOT taken, because the
	// roster objective did not clear its gate and so does not ship on by
	// default: leaving the shipped path at the count it was measured at keeps
	// the board byte-identical to milestone 7's, which is worth more than the
	// milliseconds.
	//
	// Measured on an M2, cold cache, 208-player board (sim_bench_test.go):
	// the shipped pair score costs 17ms in round 1, 34ms in round 8, 47ms in
	// round 15. The roster objective costs 366ms / 226ms / 41ms — over the
	// ~250ms budget at the top of the draft, and monotone downward because the
	// horizon is what costs. That is affordable where it is: the cache fills
	// when the PREVIOUS pick lands, against a 2-3 second poll. A promotion
	// should take the drop to 300 with it (~220ms in round 1) rather than
	// optimise anything.
	PlanRollouts = 500
)

// Survival mode names for State.Survival. The zero value means adp: the
// engine's own tests and any caller that never sets the field get v1, and the
// PRODUCT default of sim is set where flags live (cmd), not here.
const (
	SurvivalADP = "adp"
	SurvivalSim = "sim"
)

// Decision-score names for State.Scorer, which only means anything under sim
// (adp mode has one formula and keeps it wholesale).
//
// THE ZERO VALUE IS MILESTONE 7'S PAIR SCORE, and that is a result rather than
// an ordering accident. Milestone 8 built the finished-roster objective, built
// the referee that could grade it (`pick6 regret`), and did not clear its own
// gate: over 120 paired seeds the roster scorer came out -3096 (se 2390) on the
// 2025-a fold and -729 (deterministic) on 2025-b, the two folds whose room
// curve and escape rates predate them. Both differences are inside the noise
// the harness can resolve — the honest verdict is "indistinguishable" and not
// "worse" — but the gate asks for better on both, and this repo does not ship a
// decision-shaped change on plausibility. The room warp's cap is the scar.
//
// So the roster objective is built, tested, and OFF, exactly as milestone 7's
// third leg was: `-scorer roster` turns it on, and the display features that
// only it can support (the plan skeleton past two legs, "your team from here",
// the consequence clause) come with it. What would promote it is a causal fold
// on which it wins — or the continuation policy inside the rollouts learning to
// maximise U rather than vor x need, which is the strongest live hypothesis for
// why it doesn't. See docs/milestone-8.md.
//
// The Survival precedent is followed exactly: the engine's zero value is the
// conservative brain, and the product default lives where the flags do.
const (
	ScorerPair   = "" // milestone 7's two-leg pair score
	ScorerRoster = "roster"
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
