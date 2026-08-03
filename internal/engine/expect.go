package engine

import "math"

// opponentPicksBefore counts the picks in [PickNo, at) that are not mine — the
// removals that can actually take a player off the board before horizon `at`.
//
// It exists because the tilt's N differs by horizon and getting it from the
// snake math is safer than special-casing each caller. At at = NextPick() every
// intervening pick belongs to somebody else, so this is PicksUntilMine(). At
// at = FollowingPick() exactly one of them is mine, so it is q2 - PickNo - 1 —
// and my own pick must not count, or the second leg of a lookahead prices a
// removal I am the one making.
func (s *State) opponentPicksBefore(at int) int {
	n := 0
	for p := s.PickNo; p < at; p++ {
		if s.SlotAt(p) != s.MySlot {
			n++
		}
	}
	return n
}

// survivalTilt solves for the exponent c that makes the model's expected
// removals equal the n picks that actually happen before horizon `at`.
//
// Under independence the model expects sum(1 - p_j) players to come off the
// board, but a draft removes exactly n. The two disagree badly — on the shipped
// 201-player board at pick 100 with 6 picks to go the model expects 8.1 — and
// the backtest against the real 2024 draft found the same bias independently
// (mean predicted survival 0.8231 vs 0.8844 observed, every reliability bin
// under 0.9 under-predicting). This is aimed at a measured defect.
//
// The correction is one scalar: find c > 0 with sum(1 - p_j^c) = n. A power is
// the gentlest fix available, because ln p is the player's cumulative hazard
// over the intervening picks — so p^c scales every hazard by the same factor,
// which is exactly the one knob "this room drafts hungrier or slower than the
// model thinks" deserves. Its fixed points are the ones that must not move:
// p = 1 (my own pick, and undrafted-radar players) stays 1, p = 0 stays 0, and
// every pairwise ordering is preserved.
//
// The sum runs over ALL available players at every position, because the n
// picks span positions. Normalizing per position would assume each position
// takes its own share of them, which is precisely what a positional run isn't.
func (s *State) survivalTilt(at, n int) float64 {
	// The skip is on the HORIZON, not on n. At at == PickNo nothing intervenes at
	// all: every survival is already exactly 1, so returning exactly 1 costs
	// nothing and keeps the my-own-pick chain (EBest == v(bestNow), urgency == 0)
	// arithmetic rather than a bisection's 1.0000004.
	//
	// n == 0 with at > PickNo is a different state and must NOT skip. At the turn
	// the lookahead's second leg looks across a single intervening pick that is MY
	// OWN, so no opponent removes anybody and the honest answer is that everyone
	// survives — the clamp toward c = 1/TiltCMax, not "no correction". Skipping on
	// n alone left the raw logistic in place and priced the top rb at 13.5% across
	// a pick I am the one making, in exactly the double-tap-at-the-turn case the
	// lookahead exists for.
	if at <= s.PickNo {
		return 1
	}
	// Each player's log-survival is computed once and the bisection runs over
	// that array. Recomputing PSurviveAt inside the loop would be ~26x the
	// transcendental calls for a bit-identical answer; the no-caching rule is
	// about state between frames, not about waste inside one solve.
	lnp := make([]float64, 0, len(s.Players))
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		lnp = append(lnp, math.Log(s.PSurviveAt(p, at)))
	}
	// f(c) = sum(1 - p^c) is continuous and strictly increasing in c, so plain
	// bisection is enough. A survival that underflowed to zero gives ln p = -Inf
	// and exp(c*-Inf) = 0 at every c > 0 — the correct fixed point, reached
	// without a special case. c is never 0, so the 0*Inf NaN never arises.
	f := func(c float64) float64 {
		total := 0.0
		for _, l := range lnp {
			total += 1 - math.Exp(c*l)
		}
		return total
	}
	lo, hi := 1/TiltCMax, TiltCMax
	// Both clamps are real board states, not defensive padding. f(hi) < n means
	// the pool cannot supply n removals however hungry the room gets — a thin
	// endgame board, or a small test fixture driven as a 12-team draft. f(lo) > n
	// means the board is stacked with players the model has already given up on
	// while too few picks remain to take them all.
	if f(hi) < float64(n) {
		return hi
	}
	if f(lo) > float64(n) {
		return lo
	}
	for hi-lo > TiltTol {
		mid := (lo + hi) / 2
		if f(mid) < float64(n) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// PSurviveTilted is the survival probability the board shows: PSurvive
// corrected so the model expects exactly as many removals as the draft actually
// makes. One truth — an untilted probability must never reach the screen, or
// two panes quote different odds on the same player and the reader has no way
// to tell which one the ordering used.
//
// It prices to ActPick, not NextPick, and the difference is only visible on the
// clock. There NextPick is the pick being made, no picks intervene, and every
// cell read 100% — a whole column of the board dead on the one frame where you
// are choosing. ActPick asks the question you are actually asking instead: pass
// on him now and is he there when you come back? That is also the question the
// backtest has been grading all along — a calibrate vantage stands at my own
// pick and asks about my next one (see solveTilt) — so the number on screen is
// now the number `pick6 calibrate` scores.
//
// Urgency deliberately does NOT follow: it stays priced to NextPick and stays
// exactly 0 on the clock, which is what hands the group sort to the vor tie-break.
// Repointing it is a modelling change and needs its own gate, not a patch.
func (s *State) PSurviveTilted(p Player) float64 {
	at := s.ActPick()
	return math.Pow(s.PSurviveAt(p, at), s.survivalTilt(at, s.opponentPicksBefore(at)))
}

// EBest is the expected value of the best player still available at a position
// at pick `at`:
//
//	sum_j  v_j * p~_j * prod_{i<j} (1 - p~_i)
//
// walking the position best-value-first. Each term reads "his value times the
// probability he is the one you end up choosing" — he survives and everyone
// better is gone.
//
// It replaces the milestone-4 rule (best value clearing a 0.5 survival
// threshold), which made urgency jump: a player at 0.501 counted in full and
// the same player at 0.499 not at all, so one pick could re-sort the whole
// board on a rounding error. This is the same question asked continuously.
func (s *State) EBest(pos string, at int) float64 {
	return s.ebest(pos, at, "")
}

// ebest is EBest with one player removed from the pool. The lookahead's second
// leg asks what a position is worth at my pick after next *given that I just
// took bestNow* — a no-op unless both legs want the same position, which is
// exactly the case worth getting right.
func (s *State) ebest(pos string, at int, exclude string) float64 {
	avail := s.Available(pos)
	if len(avail) == 0 {
		return 0
	}
	c := s.survivalTilt(at, s.opponentPicksBefore(at))
	values := make([]float64, 0, len(avail))
	surv := make([]float64, 0, len(avail))
	for _, p := range avail {
		if p.ID == exclude {
			continue
		}
		values = append(values, float64(p.Value))
		surv = append(surv, math.Pow(s.PSurviveAt(p, at), c))
	}
	return expectedBest(values, surv)
}

// expectedBest is the sum above, kept as a pure function over two slices so the
// arithmetic can be checked against hand-computed numbers directly, rather than
// by reverse-engineering adp and sigma values that happen to produce them.
// values must be descending; survivals are the tilted probabilities.
//
// acc carries prod(1 - p~) — the probability every better player is already
// gone. On my own pick every survival is 1, so acc is exactly 0 after the first
// term and EBest is exactly v(bestNow), which is where urgency's exact zero
// comes from. That exactness is arithmetic, not layout: an exact zero times a
// value is an exact zero and adding it changes nothing, so where the epsilon
// check sits cannot break it. The check is an early exit — once "everyone
// better is gone" is this unlikely the remaining terms cannot move the answer
// by more than epsilon times a value — and at the top of the loop it also stops
// the my-own-pick walk after a single term instead of stepping the whole board.
//
// If the pool runs out with acc still positive, that residual is the "position
// empties completely" event — worth nothing, so nothing is added.
func expectedBest(values, survivals []float64) float64 {
	ev, acc := 0.0, 1.0
	for j := range values {
		if acc < EBestEpsilon {
			break
		}
		ev += acc * survivals[j] * values[j]
		acc *= 1 - survivals[j]
	}
	return ev
}
