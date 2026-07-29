package engine

import "math"

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
	Score      float64 // the pair's expected need-weighted value, both legs
}

// planPositions is the order pairs are considered in, and therefore the
// tie-break: two pairs that score identically resolve to the earlier one here.
// The order carries no meaning of its own (it's the lineup's), but iterating a
// fixed slice rather than a map is what stops an exact tie — two positions with
// equal value and equal need, which a fresh board produces more often than it
// sounds — from recommending a different pair on every frame.
var planPositions = []string{"QB", "RB", "WR", "TE", "K", "DEF"}

// BestPlan answers the question the rest of the engine is too greedy to ask:
// wr now and rb on the way back, or the reverse? Urgency prices one pick at a
// time, so it cannot see that the wr tier will be gone by my second pick while
// the rb tier holds. This is the cheap 80% of a real multi-pick lookahead —
// closed form, no simulation, at most 36 pairs.
//
// For every ordered pair of positions with nonzero need (P == Q is legitimate:
// double-tapping a position at the turn is a real strategy):
//
//	score(P, Q) = v(bestNow(P)) * Need(P)
//	            + EBest'(Q, q2) * NeedAfter(Q | bestNow(P))
//
// The second leg excludes bestNow(P) from the pool — I took him — which is a
// no-op unless both legs want the same position, and that is the case worth
// getting right. It also prices survival to q2 with the second leg's own pick
// count: ebest derives it from opponentPicksBefore(q2), which is q2 - PickNo - 1
// because my own pick at NextPick is not a removal somebody else makes. The
// excluded man does still count in the tilt's normalization pool, since he is
// demonstrably on the board at the moment the tilt is solved; that is ebest's
// own call and this doesn't second-guess it.
//
// The deliberate simplification, so nobody reads it as an oversight: the first
// leg is priced with v(bestNow(P)) — today's best available — even when my next
// pick is seventeen picks away and that man will plainly be gone by then. It is
// the spec's formula and it stays coherent for the comparison, because every
// pair is scored against the same board and carries the same optimism. Score is
// a ranking, not a forecast of what I will actually get.
//
// The one thing that outranks the score is finishing the lineup: see mustFill
// below.
//
// ok is false when there is no second pick to plan for (the draft ends first) or
// when nothing available is worth anything to my roster.
func (s *State) BestPlan() (Plan, bool) {
	q2 := s.FollowingPick()
	if q2 == 0 {
		return Plan{}, false // the draft ends before I pick again: there is no plan
	}

	// A position needs both a need and a player to be either leg of a plan. The
	// second condition isn't decoration: EBest on an empty position is 0, so
	// without it a board with one live position still names a dead one as the
	// second leg whenever every pair ties.
	type candidate struct {
		pos  string
		best Player
		need float64
	}
	var cands []candidate
	for _, pos := range planPositions {
		need := s.Need(pos)
		if need == 0 {
			continue
		}
		best, ok := s.BestNow(pos)
		if !ok {
			continue
		}
		cands = append(cands, candidate{pos: pos, best: best, need: need})
	}
	if len(cands) == 0 {
		return Plan{}, false
	}

	// mustFill is how many of the plan's two legs have to take an open starting
	// slot. The plan spends two of my R remaining picks against U unfilled
	// starters, so only R - U of them can go on a bench player.
	//
	// Without it the endgame prescribes a pair that cannot finish the lineup, and
	// not by a rounding error: K and DEF carry no value from any source, so both
	// legs of any pair containing one score 0 and it can never win an argmax over
	// value. At 14.10 with a defense still to fill and exactly two picks left, the
	// line read "rb at 14.10 -> rb at 15.03" — two bench backs and no defense at
	// all. That the board also under-sorts kickers is a known gap with a scheduled
	// fix; a printed two-pick instruction to leave a starter empty is not something
	// to leave sitting there in the meantime.
	//
	// R < U is already lost, and filling as many as possible is still the best
	// available answer, so it clamps to the two legs rather than going higher.
	mustFill := 2 - (len(s.MyUpcomingPicks(s.Rounds)) - len(s.UnfilledStarters(s.MySlot)))
	if mustFill > 2 {
		mustFill = 2
	}
	if mustFill < 0 {
		mustFill = 0
	}

	// Every pair re-solves the same tilt: both legs of all 36 share the horizon
	// q2 and its pick count, so the bisection returns the same c every time.
	// Measured at ~2ms per call on the full 201-player board against ~0.4ms for
	// a whole urgency pass — a render happens on a pick or a keypress, so this
	// stays the formula as written rather than hoisting the solve out and
	// drifting from it.
	plan := Plan{FirstPick: s.NextPick(), SecondPick: q2, Score: math.Inf(-1)}
	bestFills := -1
	for _, first := range cands {
		leg1 := float64(first.best.Value) * first.need
		for _, second := range cands {
			needAfter := s.NeedAfter(second.pos, first.best.ID)
			// Need above bench weight IS "this pick takes an open starting slot",
			// dedicated or flex — that is exactly what needFrom encodes, so asking
			// it here cannot drift from the need the score is weighted by.
			fills := 0
			if first.need > NeedBench {
				fills++
			}
			if needAfter > NeedBench {
				fills++
			}
			if fills > mustFill {
				fills = mustFill // past the requirement, extra fills buy nothing
			}
			score := leg1 + s.ebest(second.pos, q2, first.best.ID)*needAfter
			// Feasibility first, score second — and strictly greater on both, so a
			// tie keeps the pair found first and the answer is planPositions order
			// rather than whatever the last loop happened to leave behind. When
			// mustFill is 0 every pair clamps to 0 fills and this is a plain argmax
			// on score, which is the whole draft up to the last couple of rounds.
			if fills > bestFills || (fills == bestFills && score > plan.Score) {
				plan.First, plan.Second, plan.Score, bestFills = first.pos, second.pos, score, fills
			}
		}
	}
	return plan, true
}
