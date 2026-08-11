package engine

import (
	"fmt"
	"testing"
)

// lookaheadState is the milestone-7 fixture: 12 teams, slot 3, on the clock at
// 22 with my next pick at 27 — four opponent picks in between, the smallest
// window that still has futures to disagree about. Ten men at each of the four
// skill positions, all valued, so replacement is off the bottom of every board
// and vor is plain value (Replacement returns 0 below its demand index).
func lookaheadState() *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 22
	s.Survival = SurvivalSim
	for i := 0; i < 10; i++ {
		addPlayers(s,
			Player{ID: fmt.Sprintf("rb%d", i+1), Pos: "RB", ADP: float64(22 + i), Sigma: 4, Value: 600 - 20*i, Tier: 1 + i/3},
			Player{ID: fmt.Sprintf("wr%d", i+1), Pos: "WR", ADP: float64(22 + i), Sigma: 4, Value: 590 - 25*i, Tier: 1 + i/3},
			Player{ID: fmt.Sprintf("te%d", i+1), Pos: "TE", ADP: float64(30 + 3*i), Sigma: 5, Value: 400 - 30*i, Tier: 1 + i/3},
			Player{ID: fmt.Sprintf("qb%d", i+1), Pos: "QB", ADP: float64(35 + 4*i), Sigma: 5, Value: 380 - 25*i, Tier: 1 + i/3},
		)
	}
	return s
}

// planLegs is one candidate's conditioned score on an explicit seed, which is
// the only way a test can ask for the UNpaired comparison: the shipped path
// derives one seed from the vantage precisely so it cannot.
func planLegs(s *State, id string, seed int64) planResult {
	p := s.Players[id]
	c := planCand{pos: p.Pos, best: p, need: s.Need(p.Pos)}
	if c.need > NeedBench {
		c.fills1 = 1
	}
	core := s.newSimCore(seed)
	core.reseed(seed)
	return s.planRollout(core, c, s.MyUpcomingPicks(PlanDepth), 0)
}

// A re-render must not move the plan. Same state, same seed: bit-identical
// scores, modal positions and odds, however many times the state is rebuilt.
func TestConditionedPlanIsDeterministic(t *testing.T) {
	a, b := lookaheadState(), lookaheadState()
	ca, cb := a.PickChoices(), b.PickChoices()
	if len(ca) == 0 || len(ca) != len(cb) {
		t.Fatalf("choices %d vs %d", len(ca), len(cb))
	}
	for i := range ca {
		if ca[i] != cb[i] {
			t.Errorf("choice %d differs across identical states:\n %+v\n %+v", i, ca[i], cb[i])
		}
	}
	// And the second call at the same vantage reads the cache, which must be
	// the same answer rather than merely a fast one.
	for i, c := range a.PickChoices() {
		if c != ca[i] {
			t.Errorf("choice %d moved on the second call: %+v vs %+v", i, c, ca[i])
		}
	}
}

// Common random numbers, the property the ranking hangs on. Two candidates
// scored on ONE seed differ by the difference their choice makes; on two seeds
// they differ by that plus sampling noise. Measured as the variance of the
// score gap across seeds — paired has to be visibly smaller, or the board would
// reorder itself between renders on nothing.
func TestConditionedPlanUsesCommonRandomNumbers(t *testing.T) {
	const trials = 24
	pairedD := make([]float64, 0, trials)
	indepD := make([]float64, 0, trials)
	for k := 0; k < trials; k++ {
		s := lookaheadState()
		seed := int64(k * 7919)
		a := planLegs(s, "rb1", seed).Legs
		pairedD = append(pairedD, a-planLegs(s, "wr1", seed).Legs)
		indepD = append(indepD, a-planLegs(s, "wr1", seed+104729).Legs)
	}
	pv, iv := variance(pairedD), variance(indepD)
	if pv >= iv {
		t.Errorf("paired variance %.1f is not below independent %.1f: the seed is not shared", pv, iv)
	}
	// A token reduction would pass the line above while leaving the ranking as
	// noisy as before. The pairing is worth most of the variance or it is not
	// worth the constraint.
	if pv > 0.5*iv {
		t.Errorf("paired variance %.1f is %.0f%% of independent %.1f: pairing is barely working",
			pv, 100*pv/iv, iv)
	}
}

func variance(xs []float64) float64 {
	mean := 0.0
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	v := 0.0
	for _, x := range xs {
		v += (x - mean) * (x - mean)
	}
	return v / float64(len(xs))
}

// The turn: back-to-back picks, nothing intervenes, and the conditioned rollout
// degenerates to picking the best pair off the board in front of you. One
// future, so the odds are an exact 1 and the leg-two value is the argmax of
// vor x need over everyone but the candidate — arithmetic, not a sample.
func TestConditionedPlanAtTheTurnIsExact(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 12 // picks 12 and 13 are both mine
	s.Survival = SurvivalSim
	addPlayers(s,
		Player{ID: "rb1", Pos: "RB", ADP: 12, Sigma: 3, Value: 500, Tier: 1},
		Player{ID: "rb2", Pos: "RB", ADP: 13, Sigma: 3, Value: 480, Tier: 1},
		Player{ID: "wr1", Pos: "WR", ADP: 14, Sigma: 3, Value: 470, Tier: 2},
		Player{ID: "wr2", Pos: "WR", ADP: 15, Sigma: 3, Value: 460, Tier: 2},
	)
	choices := s.PickChoices()
	if len(choices) == 0 {
		t.Fatal("no choices at the turn")
	}
	var rb PickChoice
	for _, c := range choices {
		if c.Pos == "RB" {
			rb = c
		}
	}
	if rb.Pos == "" {
		t.Fatal("rb is not a choice")
	}
	// Taking rb1 leaves rb2 (480, need drops to the second rb slot: still a
	// starter) against wr1 (470, starter). The greedy leg-two policy takes rb2.
	if rb.Second != "RB" {
		t.Errorf("leg two = %q, want RB: rb2 at 480 beats wr1 at 470 on the board in front of us", rb.Second)
	}
	if rb.SecondOdds != 1 {
		t.Errorf("odds %.4f at the turn, want exactly 1: no opponent picks means one future", rb.SecondOdds)
	}
	if want := 500*NeedStarter + 480*NeedStarter; rb.Score != want {
		t.Errorf("score %.2f, want %.2f — the turn is arithmetic, not a sample", rb.Score, want)
	}
}

// My last pick of the draft: no leg two, so the score is vor x need and the
// conditioned machinery must not run at all. The v1 pin
// (TestPickChoicesOnMyLastPick) says the same thing under the formula; this
// says it under the sim, where the failure mode would be a rollout over an
// empty window.
func TestConditionedPlanOnMyLastPick(t *testing.T) {
	s := newTestState(12, 2, 3)
	s.PickNo = 22 // round 2, slot 3's only remaining pick
	s.Survival = SurvivalSim
	addPlayers(s,
		Player{ID: "rb1", Pos: "RB", ADP: 22, Sigma: 3, Value: 500, Tier: 1},
		Player{ID: "wr1", Pos: "WR", ADP: 23, Sigma: 3, Value: 400, Tier: 1},
	)
	if got := s.FollowingPick(); got != 0 {
		t.Fatalf("FollowingPick = %d, want 0: the fixture is not on the last pick", got)
	}
	for _, c := range s.PickChoices() {
		if c.Second != "" {
			t.Errorf("%s named a second leg (%s) on my last pick", c.Pos, c.Second)
		}
		if c.SecondOdds != 0 {
			t.Errorf("%s carries odds %.3f on my last pick", c.Pos, c.SecondOdds)
		}
		if want := s.VOR(c.Best) * s.Need(c.Pos); c.Score != want {
			t.Errorf("%s score %.2f, want vor x need %.2f", c.Pos, c.Score, want)
		}
	}
	if _, ok := s.BestPlan(); ok {
		t.Error("BestPlan ok on my last pick: there is no plan")
	}
}

// A rollout must not "actually do" what the tool would never recommend. Give
// the kickers and defenses the best value on the board mid-draft and the
// leg-two policy still has to ignore them — the same suppression that keeps
// them off the pane, enforced on the pick the plan simulates.
func TestConditionedLegNeverTakesASuppressedPosition(t *testing.T) {
	s := lookaheadState()
	addPlayers(s,
		Player{ID: "k1", Pos: "K", ADP: 23, Sigma: 3, Value: 5000},
		Player{ID: "def1", Pos: "DEF", ADP: 24, Sigma: 3, Value: 4900},
	)
	if !s.Suppressed("K") || !s.Suppressed("DEF") {
		t.Fatal("fixture is past the k/def hold; it must be inside it")
	}
	for _, c := range s.PickChoices() {
		if c.Second == "K" || c.Second == "DEF" {
			t.Errorf("%s: leg two landed a suppressed %s despite the hold", c.Pos, c.Second)
		}
	}
	// Directly, over every candidate rather than only the modal answer: the
	// policy order itself must not contain them.
	core := s.newSimCore(1)
	order, _, _ := s.legPolicy(core, s.Rosters[s.MySlot], false)
	for _, i := range order {
		if p := core.pool[i].pos; p == "K" || p == "DEF" {
			t.Fatalf("legPolicy offered a %s while suppressed", p)
		}
	}
}

// Feasibility outranks value inside the rollout too: when every remaining pick
// is spoken for, the leg-two policy may only take a man who closes an open
// starting slot, however much value is sitting on the bench-only side of the
// board.
func TestConditionedLegRespectsMustFill(t *testing.T) {
	s := newTestState(12, 3, 3)
	s.Roster = Roster{Slots: []string{"QB", "RB", "WR"}, Bench: 0}
	s.PickNo = 22 // mine at 22 and 27, and nothing after: two picks, three slots
	s.Survival = SurvivalSim
	// One slot already filled, so two open starters against my two picks: R == U.
	s.Players["mine_rb"] = Player{ID: "mine_rb", Pos: "RB", Value: 100}
	s.Taken["mine_rb"] = true
	s.Rosters[3] = []string{"mine_rb"}
	// Deep enough that the four opponent picks in the window cannot empty the
	// board — an exhausted pool stops the rollout before my second pick, which
	// would pass this test for the wrong reason.
	addPlayers(s, Player{ID: "rb_rich", Pos: "RB", ADP: 22, Sigma: 3, Value: 9000, Tier: 1}) // bench-only now, and the best man alive
	for i := 0; i < 8; i++ {
		addPlayers(s,
			Player{ID: fmt.Sprintf("qb%d", i+1), Pos: "QB", ADP: float64(23 + i), Sigma: 3, Value: 300 - 10*i, Tier: 1},
			Player{ID: fmt.Sprintf("wr%d", i+1), Pos: "WR", ADP: float64(24 + i), Sigma: 3, Value: 280 - 10*i, Tier: 1},
			Player{ID: fmt.Sprintf("rb%d", i+1), Pos: "RB", ADP: float64(25 + i), Sigma: 3, Value: 260 - 10*i, Tier: 1},
		)
	}
	if !s.MustFillStarters() {
		t.Fatal("fixture is not in the R == U state")
	}
	for _, c := range s.PickChoices() {
		if c.Second == "RB" {
			t.Errorf("%s: leg two took the bench rb with every pick spoken for", c.Pos)
		}
		if c.Second == "" {
			t.Errorf("%s: leg two took nobody, but two legal starters are on the board", c.Pos)
		}
	}
}

// ...and feasibility is a PREFERENCE, not a filter. Found on the scripted mock
// at 12.09: only k and def unfilled with three picks left, so mustFill demanded
// leg two close a starter while our own k/def suppression forbade the only two
// positions that could. Written as a filter the policy had nothing legal to
// take and the plan line rendered "rb at 13.07 →  at 14.06", a hole where the
// position goes. The leg has to fall back to the best available man.
func TestConditionedLegFallsBackWhenNoFillerIsAllowed(t *testing.T) {
	s := newTestState(12, 15, 7)
	s.PickNo = 141 // mine at 151 and 162, one more at 175: three picks, two open slots
	s.Survival = SurvivalSim
	// A complete lineup except k and def, both still inside the suppression.
	for i, pos := range []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB"} {
		id := fmt.Sprintf("mine%d", i)
		s.Players[id] = Player{ID: id, Pos: pos, Value: 100}
		s.Taken[id] = true
		s.Rosters[7] = append(s.Rosters[7], id)
	}
	for i := 0; i < 40; i++ {
		addPlayers(s, Player{ID: fmt.Sprintf("w%d", i), Pos: []string{"RB", "WR", "TE", "QB"}[i%4],
			ADP: float64(141 + i), Sigma: 6, Value: 400 - 5*i, Tier: 6})
	}
	if u := s.UnfilledStarters(7); len(u) != 2 || !s.Suppressed("K") {
		t.Fatalf("fixture: unfilled %v, k suppressed %v", u, s.Suppressed("K"))
	}
	for _, c := range s.PickChoices() {
		if c.Second == "" {
			t.Errorf("%s: leg two took nobody with a board full of legal men", c.Pos)
		}
		if c.Second == "K" || c.Second == "DEF" {
			t.Errorf("%s: leg two broke the suppression to satisfy mustFill (%s)", c.Pos, c.Second)
		}
	}
}

// The odds clause has to be a real fraction of real futures: strictly inside
// (0, 1] on a board with opponent picks in the window, and about a position the
// plan actually named.
func TestConditionedPlanReportsItsOdds(t *testing.T) {
	s := lookaheadState()
	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan")
	}
	if plan.Second == "" {
		t.Fatal("plan named no second leg on a full board")
	}
	if plan.SecondOdds <= 0 || plan.SecondOdds > 1 {
		t.Errorf("odds %.4f outside (0, 1]", plan.SecondOdds)
	}
	if plan.SecondTier <= 0 {
		t.Errorf("tier %d: every man on this fixture is tiered", plan.SecondTier)
	}
	// The v1 formula has no futures to count and must not look like it does.
	s2 := lookaheadState()
	s2.Survival = SurvivalADP
	p2, _ := s2.BestPlan()
	if p2.SecondOdds != 0 {
		t.Errorf("adp mode reported odds %.4f", p2.SecondOdds)
	}
}

// A mutation has to drop the plan cache along with the survival one, or the
// board quotes last pick's plan beside this pick's survivals.
func TestConditionedPlanCacheDiesOnAPick(t *testing.T) {
	s := lookaheadState()
	before, _ := s.BestPlan()
	s.Draft("rb1")
	s.Draft("rb2")
	if s.plan != nil {
		t.Fatal("plan cache survived a pick")
	}
	after, _ := s.BestPlan()
	if before == after {
		t.Errorf("plan unchanged after two picks off the board: %+v", after)
	}
}
