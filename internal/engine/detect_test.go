package engine

import (
	"fmt"
	"math"
	"testing"
)

// board builds n players at a position, all in the given tier, descending value.
func board(pos string, tier, n, baseValue int) map[string]Player {
	out := map[string]Player{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", pos, i)
		out[id] = Player{ID: id, Name: id, Pos: pos, Tier: tier, Value: baseValue - i}
	}
	return out
}

// The cliff ladder, by probability rather than by headcount. Five rbs in tier
// 1, one already drafted out of it (an untouched tier is never a cliff), each
// survivor 40% to reach my next pick. The tier holds while there are enough of
// them and stops holding as they go:
//
//	4 left: 1 - 0.6^4 = 0.8704   none
//	2 left: 1 - 0.6^2 = 0.64     none  (the old count rule called this "ending")
//	1 left: 1 - 0.6   = 0.40     ending
//
// One board per step, because taking a player out of the tier also takes his
// removal out of the tilt's budget. Each departed rb is replaced by a padded
// wr, so sum(1-p) over the available board is always exactly the 3 intervening
// picks, the tilt is a no-op, and the arithmetic above is what the code sees.
func TestCliffLevels(t *testing.T) {
	frame := func(remaining int) *State {
		s := newTestState(12, 15, 4)
		s.PickNo = 1 // NextPick 4: 3 picks intervene
		for i := 0; i < remaining; i++ {
			survivor(s, fmt.Sprintf("rb%d", i), "RB", 1, 100-i, 0.4)
		}
		for i := remaining; i < 5; i++ { // already drafted out of the tier
			id := fmt.Sprintf("gone%d", i)
			s.Players[id] = Player{ID: id, Pos: "RB", Tier: 1, Value: 200 + i}
			s.Taken[id] = true
		}
		for i := 0; i < 5-remaining; i++ { // 5 * 0.6 = the 3 picks, always
			survivor(s, fmt.Sprintf("wr%d", i), "WR", 1, 90-i, 0.4)
		}
		return s
	}
	cases := []struct {
		remaining int
		hold      float64
		want      CliffLevel
	}{
		{4, 0.8704, CliffNone},
		{2, 0.6400, CliffNone},
		{1, 0.4000, CliffWarning},
	}
	for _, c := range cases {
		s := frame(c.remaining)
		hold, ok := s.TierHold("RB")
		if !ok || math.Abs(hold-c.hold) > 1e-4 {
			t.Errorf("%d left: TierHold = %v ok=%v, want %v", c.remaining, hold, ok, c.hold)
		}
		if level, tier, n := s.Cliff("RB"); level != c.want || tier != 1 || n != c.remaining {
			t.Errorf("%d left: got level=%v tier=%d n=%d, want %v/1/%d",
				c.remaining, level, tier, n, c.want, c.remaining)
		}
	}
	// Nobody left at all: no tier, no cliff, and the group is hidden.
	if level, tier, _ := frame(0).Cliff("RB"); level != CliffNone || tier != 0 {
		t.Errorf("position empty: got level=%v tier=%d, want none/0", level, tier)
	}
}

// The tier-hold anchor, small enough to check on paper: three players in the
// tier, each a coin flip to reach my next pick, and the tier fails me only if
// all three go — 1 - 0.5^3 = 0.875. "At least one survives" is awkward to sum
// directly and trivial as a complement, which is the whole reason the formula
// is written as one.
func TestTierHoldIsTheComplementOfAllTaken(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1 // NextPick 3: 2 picks intervene
	for i := 0; i < 3; i++ {
		survivor(s, fmt.Sprintf("rb%d", i), "RB", 1, 100-i, 0.5)
	}
	survivor(s, "wr0", "WR", 1, 50, 0.5) // padding: 4 * 0.5 = the 2 picks, c = 1

	hold, ok := s.TierHold("RB")
	if !ok || math.Abs(hold-0.875) > 1e-4 {
		t.Errorf("TierHold = %v ok=%v, want 0.875", hold, ok)
	}
}

// On the clock the tier question has to look PAST my own pick. "Will this tier
// still have somebody at my next pick" is a tautology when my next pick is this
// one: every survival is 1, every tier holds at exactly 1.0, and the alarm goes
// quiet on the single frame where the alarm is the entire point — measured, it
// fired on none of the fifteen on-the-clock vantages of the scripted mock. The
// horizon is therefore the pick after this one, which is what passing costs.
//
// Slot 2 of a 4-team draft keeps the arithmetic on paper: standing on my own
// pick 2, my next one is 7 and four opponent picks fall in between, which the
// four receivers at 0.4 carry (4 x 0.6 = the 4 picks) so the tilt is a no-op.
// Two backs at 0.2 apiece then leave the tier holding 1 - 0.8^2 = 0.36 — amber.
// At the horizon this replaced, the same board read 1.0 and no cliff at all.
//
// Explicitly NOT the turn, which is where this fixture used to stand. At slot T
// picks 12 and 13 are both mine and nobody else picks in between, so the tier
// cannot be eaten before I act on it again and the honest hold there is ~1. That
// board made the arithmetic even easier and proved the wrong thing: it read 0.36
// only because the tilt was skipped whenever no opponent pick intervened, i.e.
// because the model was charging removals to a pick I make myself.
func TestTierHoldOnTheClockLooksPastMyOwnPick(t *testing.T) {
	s := newTestState(4, 15, 2)
	s.PickNo = 2 // my pick, and the following one is 7
	// survivor()'s closed form keyed to the FOLLOWING pick: the gap to NextPick
	// is zero here, which is the whole problem being fixed.
	add := func(id, pos string, value int, p float64) {
		s.Players[id] = Player{ID: id, Name: id, Pos: pos, Tier: 1, Value: value,
			ADP: float64(s.PickNo), Sigma: survivalSigma(s.FollowingPick()-s.PickNo, p)}
	}
	add("rb1", "RB", 100, 0.2)
	add("rb2", "RB", 90, 0.2)
	for i := 0; i < 4; i++ {
		add("wr"+string(rune('1'+i)), "WR", 50, 0.4) // the tilt's padding, not filler
	}
	s.Players["gone"] = Player{ID: "gone", Pos: "RB", Tier: 1, Value: 200}
	s.Taken["gone"] = true // the tier has to be touched before any level can fire

	hold, ok := s.TierHold("RB")
	if !ok || math.Abs(hold-0.36) > 1e-4 {
		t.Errorf("TierHold on the clock = %v ok=%v, want 0.36", hold, ok)
	}
	if level, _, n := s.Cliff("RB"); level != CliffWarning || n != 2 {
		t.Errorf("on the clock: got level=%v n=%d, want warning/2", level, n)
	}

	// My last pick of the draft has no "after": there is no later pick to lose
	// the tier before, so it holds outright and nothing is alarming.
	s.PickNo = 58 // round 15, slot 2: my final pick, FollowingPick 0
	if hold, _ := s.TierHold("RB"); hold != 1 {
		t.Errorf("TierHold on my last pick = %v, want exactly 1", hold)
	}
	if level, _, _ := s.Cliff("RB"); level != CliffNone {
		t.Errorf("last pick of the draft: got level=%v, want none", level)
	}
}

// The regression the whole rewrite is for: a tier of three the room is about to
// eat is a redder cliff than a tier of one nobody wants. A count cannot tell
// them apart — it calls the first fine and the second a catastrophe.
func TestCliffLevelsPriceTheTierNotTheHeadcount(t *testing.T) {
	s := newTestState(12, 15, 6)
	s.PickNo = 1 // NextPick 6: 5 picks intervene
	for i := 0; i < 3; i++ {
		survivor(s, fmt.Sprintf("rb%d", i), "RB", 1, 100-i, 0.1)
	}
	survivor(s, "te0", "TE", 1, 80, 0.1)              // alone in his tier
	survivor(s, "wr0", "WR", 1, 50, 0.3)              // padding: 3(.9) + .9 + 2(.7)
	survivor(s, "wr1", "WR", 1, 49, 0.3)              // = the 5 picks, so c = 1
	for _, id := range []string{"rbGone", "teGone"} { // both tiers are touched
		pos := "RB"
		if id == "teGone" {
			pos = "TE"
		}
		s.Players[id] = Player{ID: id, Pos: pos, Tier: 1, Value: 200}
		s.Taken[id] = true
	}

	// Three left, but 1 - 0.9^3 = 0.271: the tier is going, headcount or not.
	if hold, ok := s.TierHold("RB"); !ok || math.Abs(hold-0.271) > 1e-4 {
		t.Errorf("TierHold(rb) = %v ok=%v, want 0.271", hold, ok)
	}
	if level, _, n := s.Cliff("RB"); level != CliffWarning || n != 3 {
		t.Errorf("3 left holding at 27%%: got level=%v n=%d, want warning/3", level, n)
	}
	// One left at 10%: all but gone, and the "last one" copy is still honest
	// because remaining really is 1.
	if hold, ok := s.TierHold("TE"); !ok || math.Abs(hold-0.1) > 1e-4 {
		t.Errorf("TierHold(te) = %v ok=%v, want 0.1", hold, ok)
	}
	if level, _, n := s.Cliff("TE"); level != CliffLast || n != 1 {
		t.Errorf("1 left holding at 10%%: got level=%v n=%d, want last/1", level, n)
	}
	// K and DEF carry no value from any source, so they carry no tier and the
	// question has no answer rather than a zero one.
	if _, ok := s.TierHold("K"); ok {
		t.Error("TierHold on an untiered position reported ok")
	}
}

// An untouched tier is never a cliff however badly it holds. This guard is what
// keeps "last one, take him or lose the tier" off the screen at pick 1.01, and
// probability-driven levels make it more load-bearing, not less: a tier whose
// members are all about to go would otherwise fire red on a board where nothing
// has happened yet.
func TestUntouchedTierNeverCliffsHoweverBadlyItHolds(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1 // NextPick 3: 2 picks intervene
	survivor(s, "rb0", "RB", 1, 100, 0.05)
	survivor(s, "wr0", "WR", 1, 50, 0.05) // padding: .95 + .95 + .1
	survivor(s, "wr1", "WR", 1, 49, 0.90) // = the 2 picks, so c = 1

	if hold, _ := s.TierHold("RB"); math.Abs(hold-0.05) > 1e-4 {
		t.Fatalf("fixture is wrong: hold = %v, want 0.05 (deep red territory)", hold)
	}
	if level, _, n := s.Cliff("RB"); level != CliffNone || n != 1 {
		t.Errorf("untouched tier: got level=%v n=%d, want none/1", level, n)
	}
	// Draft somebody out of it and the very same probability is now a cliff.
	s.Players["gone"] = Player{ID: "gone", Pos: "RB", Tier: 1, Value: 200}
	s.Taken["gone"] = true
	if level, _, n := s.Cliff("RB"); level != CliffLast || n != 1 {
		t.Errorf("after depletion: got level=%v n=%d, want last/1", level, n)
	}
}

// A tier emptying must roll bestNow into the next tier on its own — the spec is
// explicit that no special-case logic handles this.
func TestCliffRollsIntoNextTierWhenOneEmpties(t *testing.T) {
	players := map[string]Player{
		"a": {ID: "a", Pos: "RB", Tier: 1, Value: 100},
		"b": {ID: "b", Pos: "RB", Tier: 2, Value: 50},
		"c": {ID: "c", Pos: "RB", Tier: 2, Value: 49},
		"d": {ID: "d", Pos: "RB", Tier: 2, Value: 48},
	}
	s := New(players, 12, 15, 1)

	// Tier 1 holds one player and nobody has been drafted, so it is not a cliff —
	// it is simply a tier of one.
	if level, tier, n := s.Cliff("RB"); level != CliffNone || tier != 1 || n != 1 {
		t.Fatalf("untouched one-player tier: got level=%v tier=%d n=%d, want none/1/1", level, tier, n)
	}
	s.Draft("a")
	if level, tier, n := s.Cliff("RB"); level != CliffNone || tier != 2 || n != 3 {
		t.Errorf("after tier 1 empties: got level=%v tier=%d n=%d, want none/2/3", level, tier, n)
	}
}

// Untiered players (no value from any source, i.e. K and DEF today) must never
// register a cliff, or the board would cry wolf on kickers.
func TestUntieredNeverCliffs(t *testing.T) {
	s := New(map[string]Player{
		"k1": {ID: "k1", Pos: "K", Tier: 0, Value: 0},
	}, 12, 15, 1)
	if level, tier, _ := s.Cliff("K"); level != CliffNone || tier != 0 {
		t.Errorf("untiered: got level=%v tier=%d, want none/0", level, tier)
	}
	if n := s.TierRemaining("K", 0); n != 0 {
		t.Errorf("TierRemaining for tier 0 = %d, want 0", n)
	}
}

func TestDetectRun(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 10, 100) {
		players[k] = v
	}
	for k, v := range board("WR", 1, 10, 90) {
		players[k] = v
	}
	s := New(players, 12, 15, 1)

	// Three RBs inside the window is below RunThreshold (4).
	s.Draft("RB0")
	s.Draft("WR0")
	s.Draft("RB1")
	s.Draft("RB2")
	if _, ok := s.DetectRun(); ok {
		t.Error("3 of 6 should not trigger a run")
	}

	s.Draft("RB3")
	run, ok := s.DetectRun()
	if !ok || run.Pos != "RB" || run.Count != 4 {
		t.Fatalf("4 of 6 should trigger an rb run, got %+v ok=%v", run, ok)
	}

	// Push RB picks out of the 6-pick window; the banner must clear.
	for i := 1; i <= 6; i++ {
		s.Draft(fmt.Sprintf("WR%d", i))
	}
	if run, ok := s.DetectRun(); ok && run.Pos == "RB" {
		t.Error("rb run should have expired out of the window")
	}
}

// When a run empties the position's tier, the banner has to say so — that's the
// difference between "act now" and "you already missed it".
func TestRunReportsBrokenTier(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 4, 100) {
		players[k] = v
	}
	s := New(players, 12, 15, 1)
	for i := 0; i < 4; i++ {
		s.Draft(fmt.Sprintf("RB%d", i))
	}
	run, ok := s.DetectRun()
	if !ok || run.Pos != "RB" {
		t.Fatalf("expected an rb run, got %+v ok=%v", run, ok)
	}
	if !run.TierBroke {
		t.Error("tier is empty; run should report TierBroke")
	}
}

func TestDetectRunIsDeterministicOnTies(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 10, 100) {
		players[k] = v
	}
	for k, v := range board("WR", 1, 10, 90) {
		players[k] = v
	}
	// Exactly 3-3 in the window: neither reaches RunThreshold, so no run at all.
	s := New(players, 12, 15, 1)
	for i := 0; i < 3; i++ {
		s.Draft(fmt.Sprintf("RB%d", i))
		s.Draft(fmt.Sprintf("WR%d", i))
	}
	if _, ok := s.DetectRun(); ok {
		t.Error("a 3-3 split should not be a run")
	}
}

// A cliff means a tier is emptying, not that it is small. Without this, a tier
// the rankings file drew with one player in it — Josh Allen alone at QB1 — puts
// "last one, take him or lose the tier" on screen at pick 1.01 of an empty draft.
func TestSmallButUntouchedTierIsNotACliff(t *testing.T) {
	// A genuine singleton tier.
	s := New(map[string]Player{
		"solo": {ID: "solo", Pos: "QB", Tier: 1, Value: 100},
		"next": {ID: "next", Pos: "QB", Tier: 2, Value: 50},
	}, 12, 15, 1)
	if level, _, _ := s.Cliff("QB"); level != CliffNone {
		t.Errorf("untouched singleton tier reported %v, want none", level)
	}

	// A two-player tier with nobody taken is not "ending" either.
	s2 := New(board("RB", 1, 2, 100), 12, 15, 1)
	if level, _, n := s2.Cliff("RB"); level != CliffNone || n != 2 {
		t.Errorf("untouched two-player tier reported %v (n=%d), want none", level, n)
	}

	// Draft one and the tier is touched — but these players are off the drafted
	// radar (no adp at all), so the survivor is certain to still be there and
	// the tier holds at 100%. Depletion is necessary for a cliff, not
	// sufficient; TestCliffLevels covers the case where it is both.
	s2.Draft("RB0")
	if hold, _ := s2.TierHold("RB"); hold != 1 {
		t.Errorf("undrafted-radar survivor: hold = %v, want exactly 1", hold)
	}
	if level, _, n := s2.Cliff("RB"); level != CliffNone || n != 1 {
		t.Errorf("after depletion got %v (n=%d), want none/1 — nobody is taking him", level, n)
	}
}

// K and DEF are tier 0 permanently, by design, with a full board of players on
// it. TierBroke used to be inferred from "Cliff returned tier 0", which is true
// of them at every pick of every draft, so a def run in round 13 reported the
// tier broken with eight defenses still available — and the ui turns TierBroke
// into "no value left", printed directly above the def group it had just sorted
// to the top of the pane. Exhausted and untiered are different states.
func TestUntieredRunDoesNotReportABrokenTier(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("DEF", 0, 8, 0) {
		players[k] = v
	}
	for k, v := range board("RB", 1, 8, 100) {
		players[k] = v
	}
	s := New(players, 12, 15, 1)
	for i := 0; i < 4; i++ {
		s.Draft(fmt.Sprintf("DEF%d", i))
	}
	run, ok := s.DetectRun()
	if !ok || run.Pos != "DEF" {
		t.Fatalf("expected a def run, got %+v ok=%v", run, ok)
	}
	if run.TierBroke {
		t.Error("4 defenses still available; the run must not claim the tier broke")
	}
	if run.Tier != 0 {
		t.Errorf("def carries no tier, got %d", run.Tier)
	}

	// ...and a position with nobody left still does report it.
	for i := 4; i < 8; i++ {
		s.Draft(fmt.Sprintf("DEF%d", i))
	}
	if run, _ := s.DetectRun(); !run.TierBroke {
		t.Error("every defense is gone; that is what TierBroke means")
	}
}

// TierHold prices to my next chance to act, which ON THE CLOCK is the pick after
// this one — every survival is 1 across zero intervening picks, so the alarm
// would go silent on the frame where I am choosing. The horizon is therefore not
// the one PSurviveTilted uses, and the copy has to be able to name it or the
// board prints "holds 3%" above three men reading 100% with nothing to
// distinguish them.
func TestTierHoldPickNamesItsOwnHorizon(t *testing.T) {
	s := New(board("RB", 1, 8, 100), 12, 15, 3)

	s.PickNo = 1 // slot 3 picks at 3: off the clock
	if got, want := s.TierHoldPick(), s.NextPick(); got != want {
		t.Errorf("off the clock the horizon is my next pick: got %d, want %d", got, want)
	}

	s.PickNo = 3 // on the clock
	if got, want := s.TierHoldPick(), s.FollowingPick(); got != want {
		t.Errorf("on the clock the horizon is the pick after: got %d, want %d", got, want)
	}
	if s.TierHoldPick() == s.NextPick() {
		t.Error("on the clock the two horizons must differ, or nothing needs labelling")
	}
}
