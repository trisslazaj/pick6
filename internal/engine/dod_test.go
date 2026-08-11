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
// What the lookahead adds on its own is separable and is measured here: leg two
// under the rollouts is the best man across ALL positions in each future, while
// the formula takes the best of the per-position expectations. E[max] >= max E,
// always, and the gap is the value of having more than one way for a future to
// go well. It is real, it is smaller than the survival model's contribution on
// this board, and saying so is cheaper than discovering it later.
func TestConditionedLegBeatsTheMaxOfMarginals(t *testing.T) {
	s := dodBoard()
	s.Survival = SurvivalSim
	q2 := s.FollowingPick()

	for _, pos := range []string{"RB", "WR"} {
		bn, ok := s.BestNow(pos)
		if !ok {
			t.Fatalf("%s: no best available", pos)
		}
		// The formula's second leg, on the same sim survivals the rollouts see.
		marginal := 0.0
		for _, q := range planPositions {
			leg2 := s.ebest(q, q2, bn.ID) - s.Replacement(q)
			if leg2 < 0 {
				leg2 = 0
			}
			if v := leg2 * s.NeedAfter(q, bn.ID); v > marginal {
				marginal = v
			}
		}
		var conditioned float64
		for _, c := range s.PickChoices() {
			if c.Pos == pos {
				conditioned = c.Score - s.VOR(bn)*s.Need(pos)
			}
		}
		if conditioned < marginal {
			t.Errorf("%s: conditioned leg two %.1f is below the max of marginals %.1f — "+
				"E[max] cannot be under max E unless the two are pricing different boards",
				pos, conditioned, marginal)
		}
		t.Logf("%s: conditioned leg two %.1f vs max-of-marginals %.1f (+%.1f)",
			pos, conditioned, marginal, conditioned-marginal)
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
	_ = s.planRollout(core, c, s.MyUpcomingPicks(2), 0, allPositions(s))

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
