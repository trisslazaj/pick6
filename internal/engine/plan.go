package engine

import (
	"math"
	"sort"
)

// Plan is the two-pick recommendation: take the best player at First with my
// next pick, and expect to be taking Second with the one after it.
//
// Both legs carry their pick number because the plan is computed as-if standing
// at NextPick but renders at every vantage — on eleven frames out of twelve
// "now" is somebody else's pick, and copy that says otherwise is lying.
type Plan struct {
	First      string  // position to take at FirstPick
	Second     string  // position to expect at SecondPick
	FirstPick  int     // NextPick()
	SecondPick int     // FollowingPick()
	Score      float64 // the pair's expected need-weighted value over replacement, both legs

	// SecondTier and SecondOdds are the conditioned lookahead's outcome claim:
	// the modal tier leg two lands at Second, and how often it lands that tier
	// or better across the simulated futures. "lands a tier-2 rb 78%."
	//
	// SecondOdds is 0 when the plan was priced by the v1 formula (sim off),
	// which has no futures to count and must not be made to look like it does.
	SecondTier int
	SecondOdds float64
}

// planPositions is the order candidates are considered in, and therefore the
// tie-break: two choices that score identically resolve to the earlier one here.
// The order carries no meaning of its own (it's the lineup's), but iterating a
// fixed slice rather than a map is what stops an exact tie — two positions with
// equal value and equal need, which a fresh board produces more often than it
// sounds — from recommending a different pair on every frame.
var planPositions = []string{"QB", "RB", "WR", "TE", "K", "DEF"}

// PickChoice is one way to spend my next pick: a position, its best available
// man, and what the two-pick lookahead says that choice is worth.
type PickChoice struct {
	Pos    string
	Best   Player
	Score  float64 // leg one's vor x need, plus the second leg's
	Fills  int     // how many of the legs fill open starting slots, capped at what feasibility demands
	Second string  // the second leg the score chose; "" on my last pick, where there is none

	// SecondTier and SecondOdds carry the conditioned rollouts' outcome claim
	// for this choice; both are zero under the v1 formula. See Plan.
	SecondTier int
	SecondOdds float64
}

// PickChoices ranks every way to spend my next pick, and it is THE PRIMARY KEY:
// both board frames order positions by it, and BestPlan is literally its first
// row, so the plan line and the ordering under it cannot disagree. They used to
// — the on-clock list ranked by CostOfPassing while the plan ranked pairs by
// value x need, and the two named different positions on ~47% of on-the-clock
// frames of the scripted mock.
//
// For every position P with nonzero need, the choice "spend this pick on P" is
// scored as the best pair it leads:
//
//	score(P) = (v(bestNow(P)) - R(P))⁺ · need(P)
//	         + max over Q of (EBest'(Q, q2) - R(Q))⁺ · NeedAfter(Q | bestNow(P))
//
// Both legs are priced over replacement — R(P) is the value of the man this
// league would have handed you at that position anyway (vor.go) — which is what
// keeps a steep-topped but cheap-to-fill position from winning the argmax on
// headline value. P == Q is legitimate (double-tapping a position at the turn
// is a real strategy), and the second leg excludes bestNow(P) from its pool and
// prices need against the roster the first leg leaves behind.
//
// The replacement discount only separates pairs that fill DIFFERENT position
// sets — comparing qb-then-wr against wr-then-qb, both R's appear once in each
// and cancel, so the order of two legs over the same positions is still decided
// by value and survival alone. That is correct: replacement is a claim about
// what a position costs to fill eventually, not about which order to fill two
// positions you have chosen to fill now.
//
// The deliberate simplification, so nobody reads it as an oversight: the first
// leg is priced with today's best available even when my next pick is seventeen
// picks away and that man will plainly be gone by then. Every choice is scored
// against the same board and carries the same optimism; Score is a ranking, not
// a forecast of what I will actually get.
//
// On my last pick of the draft there is no second leg: the score degenerates to
// vor x need, which is the honest answer when nothing can be planned — what
// does he buy over replacement is the only question left.
//
// Feasibility outranks score, exactly as before: with R remaining picks and U
// unfilled starters, only R - U of the legs may go on a bench player, so a
// choice is ranked first by how many open starting slots its legs close (capped
// at what is actually required) and only then by score. K and DEF carry
// synthesized values but can still arrive at 0 from a live feed, and a pair
// that cannot finish the lineup must not outrank one that can however the
// values compare.
func (s *State) PickChoices() []PickChoice {
	// A position needs both a need and a player to be a leg of a choice. The
	// second condition isn't decoration: EBest on an empty position is 0, so
	// without it a board with one live position still names a dead one as the
	// second leg whenever every pair ties.
	//
	// Membership is decided by Need — the same number that decides whether the
	// board shows the position at all, so a choice can never name a group the
	// reader cannot see. The WEIGHT is the slack-free need, because the second
	// leg is priced by NeedAfter, which has no slack either; mixing the two made
	// the score depend on the order of two legs that end at the same roster.
	filled, _ := s.FilledSlots(s.MySlot)
	var cands []planCand
	for _, pos := range planPositions {
		if s.Need(pos) == 0 {
			continue
		}
		best, ok := s.BestNow(pos)
		if !ok {
			continue
		}
		c := planCand{pos: pos, best: best, need: s.needFrom(pos, filled)}
		// Need above bench weight IS "this pick takes an open starting slot",
		// dedicated or flex — that is exactly what needFrom encodes, so asking
		// it here cannot drift from the need the score is weighted by.
		if c.need > NeedBench {
			c.fills1 = 1
		}
		cands = append(cands, c)
	}
	if len(cands) == 0 {
		return nil
	}

	// How many of my own picks the score plans over. Two under the v1 formula,
	// PlanDepth under the conditioned rollouts — identical today, since
	// PlanDepth ships at 2. Fewer when the draft ends first; one on my last
	// pick, where there is nothing to plan.
	depth := 2
	if s.Survival == SurvivalSim {
		depth = PlanDepth
	}
	mine := s.MyUpcomingPicks(depth)
	legs := len(mine)
	if legs < 1 {
		legs = 1 // the draft is over for me: score the one leg that is left
	}

	// mustFill is how many of the legs have to take an open starting slot. The
	// legs spend that many of my R remaining picks against U unfilled starters,
	// so only R - U of them can go on a bench player. R < U is already lost, and
	// filling as many as possible is still the best available answer, so it
	// clamps to the leg count rather than going higher.
	mustFill := legs - (s.MyPicksLeft() - len(s.UnfilledStarters(s.MySlot)))
	if mustFill > legs {
		mustFill = legs
	}
	if mustFill < 0 {
		mustFill = 0
	}

	// The conditioned second leg (milestone 7, lookahead.go), for every
	// candidate on one shared seed. Sim only: rollouts exist where the sim
	// does, and under -survival=adp the plan keeps the formula below — one
	// switch, the same chokepoint philosophy survivalAt follows.
	var cond map[string]planResult
	if legs > 1 && s.Survival == SurvivalSim {
		cond = s.conditionedLegs(cands, mine, mustFill)
	}

	// Every pair re-solves the same tilt: both second legs share the horizon q2
	// and its pick count, so the bisection returns the same c every time.
	// Measured at ~2ms per call on the full 201-player board against ~0.4ms for
	// a whole urgency pass — a render happens on a pick or a keypress, so this
	// stays the formula as written rather than hoisting the solve out and
	// drifting from it.
	choices := make([]PickChoice, 0, len(cands))
	for _, first := range cands {
		leg1 := s.VOR(first.best) * first.need
		if legs == 1 {
			f := first.fills1
			if f > mustFill {
				f = mustFill
			}
			choices = append(choices, PickChoice{Pos: first.pos, Best: first.best, Score: leg1, Fills: f})
			continue
		}
		if cond != nil {
			// The second leg is no longer chosen by a max over Q — it is
			// whatever the rollouts' own policy actually took, so Fills
			// describes the plan that HAPPENS rather than the best pair
			// available.
			//
			// The two agree wherever it matters and not by construction, which
			// is worth stating precisely rather than asserting: the policy
			// ranks slot-closers ahead of everyone else when mustFill demands
			// one (a preference, not a filter — legPolicy explains why), so the
			// modal leg two closes a slot whenever one is alive in most
			// futures, which is exactly when the old max could have closed one.
			// The residue is a genuine discontinuity — a filler alive in 51% of
			// futures scores a fill and in 49% does not, and Fills sorts above
			// Score. v1 carried the same 0/1 cliff off its own max, no frame of
			// the scripted mock reorders on it, and softening it means making
			// feasibility continuous everywhere rather than here.
			r := cond[first.best.ID]
			fills := first.fills1
			if r.Second != "" && s.NeedAfter(r.Second, first.best.ID) > NeedBench {
				fills++
			}
			if fills > mustFill {
				fills = mustFill // past the requirement, extra fills buy nothing
			}
			choices = append(choices, PickChoice{
				Pos: first.pos, Best: first.best,
				Score: leg1 + r.Legs, Fills: fills,
				Second: r.Second, SecondTier: r.SecondTier, SecondOdds: r.SecondOdds,
			})
			continue
		}
		q2 := mine[1]
		best := PickChoice{Pos: first.pos, Best: first.best, Score: math.Inf(-1), Fills: -1}
		for _, second := range cands {
			needAfter := s.NeedAfter(second.pos, first.best.ID)
			fills := first.fills1
			if needAfter > NeedBench {
				fills++
			}
			if fills > mustFill {
				fills = mustFill // past the requirement, extra fills buy nothing
			}
			leg2 := s.ebest(second.pos, q2, first.best.ID) - s.Replacement(second.pos)
			if leg2 < 0 {
				leg2 = 0 // an expectation below replacement is a loss you'd never take
			}
			score := leg1 + leg2*needAfter
			// Feasibility first, score second — and strictly greater on both, so a
			// tie keeps the pair found first and the answer is planPositions order
			// rather than whatever the last loop happened to leave behind.
			if fills > best.Fills || (fills == best.Fills && score > best.Score) {
				best.Score, best.Fills, best.Second = score, fills, second.pos
			}
		}
		choices = append(choices, best)
	}
	// Stable, so exact score ties keep planPositions order — the same guarantee
	// the pair loop's strict > gives the second leg.
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].Fills != choices[j].Fills {
			return choices[i].Fills > choices[j].Fills
		}
		return choices[i].Score > choices[j].Score
	})
	return choices
}

// BestPlan answers the question the rest of the engine is too greedy to ask:
// wr now and rb on the way back, or the reverse? It is PickChoices' top row —
// one brain, so the plan line can never contradict the ordering rendered under
// it — kept as its own function because the plan is a pair with pick numbers
// attached and most callers want exactly that.
//
// ok is false when there is no second pick to plan for (the draft ends first)
// or when nothing available is worth anything to my roster.
func (s *State) BestPlan() (Plan, bool) {
	q2 := s.FollowingPick()
	if q2 == 0 {
		return Plan{}, false // the draft ends before I pick again: there is no plan
	}
	choices := s.PickChoices()
	if len(choices) == 0 {
		return Plan{}, false
	}
	top := choices[0]
	return Plan{
		First:      top.Pos,
		Second:     top.Second,
		FirstPick:  s.NextPick(),
		SecondPick: q2,
		Score:      top.Score,
		SecondTier: top.SecondTier,
		SecondOdds: top.SecondOdds,
	}, true
}
