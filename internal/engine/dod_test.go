package engine

import (
	"fmt"
	"testing"
)

// Milestone 7's acceptance evidence, and the reason it is a scenario rather
// than a metric: a decision score has no counterfactual labels, so `calibrate`
// cannot grade it. What CAN be shown is a board where the greedy plan and the
// conditioned one disagree and the conditioned one is demonstrably right.
//
// The setup is the canonical one, and it is the mirror of v2's own flagship
// scenario: THE MARKET AND THE ROOMS DISAGREE. National ADP says the backs are
// going now (adp 103-110 against a pick at 102) and the receivers will keep
// (adp 120-122 against my next pick at 115). The six seats that pick in between
// say the opposite — every one of them is full at running back (rb, rb and flex
// all filled) with both receiver slots open — so the picks that actually happen
// eat receivers and leave backs.
//
// Greedy prices both legs off today's board through the ADP logistic, sees
// receivers surviving at 65% and backs at 0.8%, and plans rb now → wr later.
// It is planning to come back for two receivers that will not be there.
func dodBoard() *State {
	s := newTestState(12, 15, 6)
	s.PickNo = 102 // round 9, slot 6 on the clock; my next is 115, in round 10

	// Me: qb and te filled, both rb slots, both wr slots and the flex open, so
	// rb and wr carry identical starter need and the pair score is symmetric.
	for i, pos := range []string{"QB", "TE"} {
		id := fmt.Sprintf("mine%d", i)
		s.Players[id] = Player{ID: id, Pos: pos, Value: 100}
		s.Taken[id] = true
		s.Rosters[6] = append(s.Rosters[6], id)
	}
	// Every seat that picks inside the window: three backs (rb/rb/flex full)
	// plus a qb and a te, so their one live starting need is receiver. Round 9
	// and 10 are where betaAt is pinned at 1.5, i.e. where need dominates the
	// opponent model — which is what makes this a room story and not a
	// coincidence of the draw.
	n := 0
	for _, slot := range []int{7, 8, 9, 10, 11, 12} {
		for _, pos := range []string{"RB", "RB", "RB", "QB", "TE"} {
			id := fmt.Sprintf("opp%d", n)
			n++
			s.Players[id] = Player{ID: id, Pos: pos, Value: 100}
			s.Taken[id] = true
			s.Rosters[slot] = append(s.Rosters[slot], id)
		}
	}

	// A deep band of backs the market is pricing as gone right now.
	for i := 0; i < 8; i++ {
		addPlayers(s, Player{ID: fmt.Sprintf("rb%d", i+1), Pos: "RB",
			ADP: float64(103 + i), Sigma: 3, Value: 950 - 10*i, Tier: 4})
	}
	// Two elite receivers the market prices past my next pick, then the cliff.
	addPlayers(s,
		Player{ID: "wrA", Pos: "WR", ADP: 120, Sigma: 6, Value: 1000, Tier: 2},
		Player{ID: "wrB", Pos: "WR", ADP: 122, Sigma: 6, Value: 980, Tier: 2},
	)
	for i := 0; i < 6; i++ {
		addPlayers(s, Player{ID: fmt.Sprintf("wrc%d", i+1), Pos: "WR",
			ADP: float64(126 + i), Sigma: 6, Value: 400 - 10*i, Tier: 5})
	}
	// Filler at the positions my lineup has already covered, so the opponents'
	// candidate pool is a real board rather than two positions.
	for i := 0; i < 6; i++ {
		addPlayers(s,
			Player{ID: fmt.Sprintf("qb%d", i+1), Pos: "QB", ADP: float64(118 + 2*i), Sigma: 6, Value: 300 - 10*i, Tier: 5},
			Player{ID: fmt.Sprintf("te%d", i+1), Pos: "TE", ADP: float64(119 + 2*i), Sigma: 6, Value: 290 - 10*i, Tier: 5},
		)
	}
	return s
}

func topChoice(t *testing.T, s *State, label string) PickChoice {
	t.Helper()
	choices := s.PickChoices()
	if len(choices) == 0 {
		t.Fatalf("%s: no choices", label)
	}
	return choices[0]
}

// The flip itself. This is the milestone's DoD centrepiece.
func TestConditionedPlanFlipsTheGreedyOne(t *testing.T) {
	greedy := dodBoard()
	greedy.Survival = SurvivalADP
	g := topChoice(t, greedy, "greedy")
	if g.Pos != "RB" || g.Second != "WR" {
		t.Fatalf("greedy planned %s → %s, want rb → wr: the fixture no longer sets up the disagreement",
			g.Pos, g.Second)
	}

	cond := dodBoard()
	cond.Survival = SurvivalSim
	c := topChoice(t, cond, "conditioned")
	if c.Pos != "WR" {
		t.Errorf("conditioned planned %s first, want WR: the rollouts did not flip the greedy plan", c.Pos)
	}
	if c.Second != "RB" {
		t.Errorf("conditioned leg two = %q, want RB", c.Second)
	}

	// And it is right, which is a claim about the board rather than about the
	// ranking. Two facts carry it, both measurable:
	q2 := cond.FollowingPick()
	surv := cond.survivalAt(q2)
	if p := surv(cond.Players["wrA"]); p > 0.15 {
		t.Errorf("the elite wr survives to %d at %.0f%%: he was supposed to be unrecoverable", q2, 100*p)
	}
	// ...and the backs ARE recoverable: the conditioned plan lands one of the
	// tier-4 band at leg two in the large majority of futures.
	if c.SecondOdds < 0.7 {
		t.Errorf("leg two lands a tier-%d rb in only %.0f%% of futures: the band was supposed to hold",
			c.SecondTier, 100*c.SecondOdds)
	}
	// Which is the whole argument in one line: take the man you cannot get
	// back, because the one you can wait for is still going to be there.
}

// Attribution, pinned so nobody overclaims it later. TWO things change between
// the two runs above — the survival model AND the second leg — and on this
// board the survival model carries most of the flip: greedy's own formula
// computed on top of SIM survivals already prefers the receiver.
//
// What the lookahead adds on its own is separable and is measured here. The
// formula commits to a leg-two POSITION before the board is dealt: it takes the
// best of the per-position expectations, and then that is the position you get,
// whatever the futures do. The rollouts choose in each future, so leg two is the
// best man across all positions in the world that actually happened. E[max]
// against max E, and the gap is the value of having more than one way for a
// future to go well.
//
// Milestone 8 re-expresses it rather than dropping it: the score is a roster
// value now, so there is no leg-two term to subtract out of it. Instead both
// arms are run as rollouts over the SAME two-pick window and the same seed, one
// free to choose and one nailed to the formula's position, and the finished
// teams are compared. Same claim, measured in the units the objective ships in.
func TestConditionedLegBeatsCommittingToOnePosition(t *testing.T) {
	s := dodBoard()
	s.Survival = SurvivalSim
	q2 := s.FollowingPick()
	mine := s.MyUpcomingPicks(2)

	for _, pos := range []string{"RB", "WR"} {
		bn, ok := s.BestNow(pos)
		if !ok {
			t.Fatalf("%s: no best available", pos)
		}
		// The position the v1 formula would have committed leg two to, computed
		// on the same sim survivals the rollouts see.
		commit, marginal := "", -1.0
		for _, q := range planPositions {
			leg2 := s.ebest(q, q2, bn.ID) - s.Replacement(q)
			if leg2 < 0 {
				leg2 = 0
			}
			if v := leg2 * s.NeedAfter(q, bn.ID); v > marginal {
				commit, marginal = q, v
			}
		}
		c := planCand{pos: pos, best: bn, need: s.Need(pos)}
		if c.need > NeedBench {
			c.fills1 = 1
		}
		run := func(allowed map[string]bool) float64 {
			core := s.newSimCore(s.planSeed())
			core.reseed(s.planSeed())
			return s.planRollout(core, s.newPlanPolicy(core, allowed, true), c, mine, 0).Score
		}
		free := run(allPositions(s))
		nailed := run(map[string]bool{commit: true})
		if free < nailed {
			t.Errorf("%s: choosing in the future finished at %.1f against %.1f for committing to %s — "+
				"E[max] cannot be under max E unless the two are pricing different boards",
				pos, free, nailed, commit)
		}
		t.Logf("%s: free leg two %.1f vs committed-to-%s %.1f (+%.1f)", pos, free, commit, nailed, free-nailed)
	}
}

// The conditioning honesty check the spec asks for in the dev loop. A player's
// survival to q2 inside the conditioned rollouts should differ from the
// unconditioned sim row calibrate already scores only through the knock-on
// effects of removing my leg-one man — one player off a board of ~90, over a
// window of a dozen picks. A large systematic gap would mean the conditioning
// is leaking something, so it is worth a number rather than an assumption.
func TestConditioningDoesNotLeak(t *testing.T) {
	s := dodBoard()
	s.Survival = SurvivalSim
	q2 := s.FollowingPick()
	uncond := s.survivalAt(q2)

	// The same window, conditioned on my taking the top back, measured by
	// running the plan's own machinery and reading who was still alive.
	bn, _ := s.BestNow("RB")
	c := planCand{pos: "RB", best: bn, need: s.Need("RB"), fills1: 1}
	core := s.newSimCore(s.planSeed())
	core.reseed(s.planSeed())
	// Deliberately a two-leg window even though the shipped horizon is the whole
	// draft: the leak this checks is about what removing my leg-one man does to
	// the survival column, and the survival column's own window is two picks.
	_ = s.planRollout(core, s.newPlanPolicy(core, allPositions(s), true), c, s.MyUpcomingPicks(2), 0)

	// Per-player: the unconditioned sim row against the conditioned one, over
	// the men who can actually be affected. Conditioned survival is read from a
	// second rollout batch that tallies removals the same way simTable does.
	worst, worstID := 0.0, ""
	for _, id := range []string{"wrA", "wrB", "rb2", "rb3", "wrc1", "qb1", "te1"} {
		p := s.Players[id]
		u := uncond(p)
		cnd := conditionedSurvival(s, c, id, q2)
		if d := abs(u - cnd); d > worst {
			worst, worstID = d, id
		}
		t.Logf("%-5s uncond %.3f  cond %.3f  Δ %+.3f", id, u, cnd, cnd-u)
	}
	if worst > 0.25 {
		t.Errorf("%s moves %.3f between the unconditioned and conditioned rollouts: "+
			"removing one player cannot do that, so the conditioning is leaking", worstID, worst)
	}
}

// conditionedSurvival replays the plan window with my leg-one man removed and
// reports how often `id` was still there at `at`. It is the test's own tally
// rather than a shipped API: nothing on the board asks this question, and an
// exported one would invite somebody to price the survival column off the
// plan's futures, which are a different window answering a different question.
func conditionedSurvival(s *State, c planCand, id string, at int) float64 {
	core := s.newSimCore(s.planSeed())
	core.reseed(s.planSeed())
	candIdx := core.index[c.best.ID]
	target := core.index[id]
	windowSlots := map[int]bool{}
	var opp []int
	for q := s.PickNo; q < at; q++ {
		if s.SlotAt(q) != s.MySlot {
			windowSlots[s.SlotAt(q)] = true
			opp = append(opp, q)
		}
	}
	alive := 0
	for m := 0; m < PlanRollouts; m++ {
		core.reset()
		core.alive[candIdx] = false
		rosters := s.copyRosters(windowSlots)
		for _, q := range opp {
			if _, _, done := core.oppPick(q, rosters); done {
				break
			}
		}
		if core.alive[target] {
			alive++
		}
	}
	return (float64(alive) + 0.5) / (float64(PlanRollouts) + 1)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// The wheel, which is where milestone 7 left its open question and milestone 8
// dissolves it.
//
// The retired depth-3 experiment flipped the FIRST leg on 16 of 54 wheel frames
// and every flip moved away from running back — coherent with the arithmetic
// (two back-to-back picks let you double-tap the deep position later and take
// the scarce one now) and unshippable, because "coherent" is not "right" and a
// two-leg score had no way to tell them apart. A full horizon has one: it plays
// the drought.
//
// Slot 12 on the clock at pick 12, so picks 12 and 13 are both mine and then
// nothing until 36. The backs are deep and priced late — the same band is still
// there after the drought. The tight ends are two men the market is taking right
// now, and behind them a cliff. A human reading that board takes the tight end
// with one of the two picks he is holding, because the backs keep and the tight
// ends do not.
//
// The pair score cannot see it: at the turn nothing intervenes, so it ranks the
// two best men by vor x need and the backs carry more value. The roster score
// plays the twenty-two picks that follow, watches the tight ends disappear into
// them, and takes the man it cannot get back.
func wheelBoard() *State {
	s := newTestState(12, 15, 12)
	s.PickNo = 12 // picks 12 and 13 are mine; the next is 36

	// Two tight ends the market is taking now, then the cliff.
	addPlayers(s,
		Player{ID: "teA", Pos: "TE", ADP: 12, Sigma: 2, Value: 800, Tier: 1},
		Player{ID: "teB", Pos: "TE", ADP: 14, Sigma: 2, Value: 780, Tier: 1},
	)
	for i := 0; i < 8; i++ {
		addPlayers(s, Player{ID: fmt.Sprintf("tec%d", i), Pos: "TE",
			ADP: float64(90 + 4*i), Sigma: 8, Value: 200 - 5*i, Tier: 6})
	}
	// A deep band of backs the market prices well past the drought — far enough
	// past that twenty-four opponent picks do not reach them — and worth
	// slightly MORE than the tight ends, which is what makes the greedy answer
	// wrong rather than merely different.
	for i := 0; i < 14; i++ {
		addPlayers(s, Player{ID: fmt.Sprintf("rb%d", i), Pos: "RB",
			ADP: float64(92 + 2*i), Sigma: 8, Value: 850 - 5*i, Tier: 2 + i/5})
	}
	// Filler so the opponents draft a real board rather than two positions.
	for i := 0; i < 20; i++ {
		addPlayers(s,
			Player{ID: fmt.Sprintf("wr%d", i), Pos: "WR", ADP: float64(15 + 3*i), Sigma: 8, Value: 700 - 10*i, Tier: 3 + i/6},
			Player{ID: fmt.Sprintf("qb%d", i), Pos: "QB", ADP: float64(20 + 4*i), Sigma: 8, Value: 600 - 10*i, Tier: 3 + i/6},
		)
	}
	for i := 0; i < 6; i++ {
		addPlayers(s,
			Player{ID: fmt.Sprintf("k%d", i), Pos: "K", ADP: float64(160 + i), Sigma: 8, Value: 120 - 5*i},
			Player{ID: fmt.Sprintf("df%d", i), Pos: "DEF", ADP: float64(150 + i), Sigma: 8, Value: 130 - 5*i},
		)
	}
	return s
}

func TestWheelTakesTheManTheDroughtWillEat(t *testing.T) {
	greedy := wheelBoard()
	greedy.Survival, greedy.Scorer = SurvivalSim, ScorerPair
	g := topChoice(t, greedy, "pair")
	if g.Pos != "RB" {
		t.Fatalf("the pair score picked %s: the fixture no longer sets up the disagreement "+
			"(it needs the backs to carry more raw value than the tight ends)", g.Pos)
	}

	full := wheelBoard()
	full.Survival, full.Scorer = SurvivalSim, ScorerRoster
	c := topChoice(t, full, "roster")
	if c.Pos != "TE" {
		t.Errorf("the roster score picked %s at the wheel, want TE: it did not price the drought", c.Pos)
	}

	// And it is RIGHT, which is a claim about the board rather than about the
	// ranking. Two facts carry it, both measurable at the horizon that matters:
	// the tight ends do not survive the drought, and the backs do.
	q := full.MyUpcomingPicks(3)
	if len(q) < 3 {
		t.Fatalf("fixture: my picks are %v, want at least three", q)
	}
	after := full.SimBacktest(full.PickNo, q[2])
	if p := after("teA"); p > 0.2 {
		t.Errorf("the tight end survives to %d at %.0f%%: he was supposed to be unrecoverable", q[2], 100*p)
	}
	if p := after("rb0"); p < 0.5 {
		t.Errorf("the back survives to %d at only %.0f%%: the band was supposed to keep", q[2], 100*p)
	}
	// ...and the plan says so out loud rather than only ranking that way: the
	// te slot ends up filled by the man it took now.
	var slot SlotOutlook
	for _, o := range c.Outlook {
		if o.Slot == "TE" {
			slot = o
		}
	}
	if slot.PlayerID != "teA" {
		t.Errorf("the te slot ends up holding %q, want teA — the plan is not the pick it made",
			slot.PlayerID)
	}
}
