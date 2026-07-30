package adp

import "sort"

// PositionDemand is D_P: how many players at each position this league actually
// drafts in one draft, as the median over the completed drafts we have.
//
// It is the denominator of the engine's replacement level — R(P) is the value of
// the D_P-th best P on the board — so it has to be a measurement of THIS room
// and not of a lineup card. Measured over the three cached drafts it comes out
// qb 19, rb 58, wr 67, te 17, k 12, def 12, against a default 12-team lineup
// shape that would imply qb 12, rb 28, wr 28, te 16. The gap is the bench: a
// league starting one quarterback still drafts nineteen of them, and it hoards
// twice as many backs as it can start.
//
// Median rather than mean, per the spec, and it is doing real work here: one of
// the three drafts is 16 rounds and two are 15, so the raw counts are not
// comparable across them and a mean would quietly weight the long draft's extra
// twelve picks into every position. The median just picks the middle draft.
//
// Positions absent from a draft count as zero for that draft rather than being
// skipped, or a position one room never touched would take its median from the
// rooms that did.
func PositionDemand(drafts map[string]RoomDraft) map[string]int {
	ids := make([]string, 0, len(drafts))
	for id := range drafts {
		ids = append(ids, id)
	}
	sort.Strings(ids) // map order is not an order; keep two runs identical

	seen := map[string]bool{}
	per := make([]map[string]int, 0, len(ids))
	for _, id := range ids {
		c := map[string]int{}
		for _, pos := range drafts[id] {
			if pos == "" {
				continue // a pick whose metadata carried no position
			}
			c[pos]++
			seen[pos] = true
		}
		per = append(per, c)
	}
	if len(per) == 0 {
		return nil // no drafts: the engine falls back to the league's shape
	}

	out := map[string]int{}
	for pos := range seen {
		counts := make([]int, 0, len(per))
		for _, c := range per {
			counts = append(counts, c[pos])
		}
		out[pos] = median(counts)
	}
	return out
}

// median sorts in place and returns the middle value, rounding half up on an
// even count. Whole players only: D_P indexes into a board.
func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	sort.Ints(xs)
	lo, hi := xs[(len(xs)-1)/2], xs[len(xs)/2]
	return (lo + hi + 1) / 2
}
