package engine

import "sort"

// Value over replacement: what taking a player actually buys, measured against
// the man this league would have ended up with at his position anyway.
//
// Raw value ranks a position by its best available player, which answers "who is
// the best player left" — not the question at the clock. The best receiver on a
// board 69 receivers deep and the best tight end on a board 19 deep can carry the
// same number while buying completely different things, because behind the
// receiver sit sixty more and behind the tight end sit two.
//
//	D_P  = how many players at position P this league drafts in one draft
//	R(P) = the value of the D_P-th best P on the pre-draft board
//	vor  = max(0, v(j) - R(pos_j))
//
// D_P is measured from this room's own completed drafts, not assumed: qb 19,
// rb 58, wr 67, te 17, k 12, def 12 (median over the three cached drafts —
// adp.PositionDemand computes it, the cmd layer hands it in, `pick6 fetch`
// prints it).
//
// On the shipped 2026 board that gives replacement levels of qb 314, te 182,
// wr 10 and rb 0 — the room drafts 58 backs while only 51 carry a value at all,
// so replacement at running back is off the bottom of the board and a back's vor
// is his whole value. That spread is the entire content of this: the shallow
// positions' headline numbers overstate what taking one early really wins you,
// and the deep ones' understate it.

// Replacement is R(P): the value of the D_P-th best player at a position on the
// PRE-DRAFT board.
//
// Static by construction rather than by snapshot. s.Players is never pruned —
// a pick sets Taken — so ranking over it ignores the draft entirely, which is
// the specified behaviour and not a happy accident of the data structure. A
// replacement level that climbed as a position emptied would make every position
// look scarcer the longer you waited, i.e. exactly backwards. Mid-draft
// EnsurePlayer registrations cannot move it either: they arrive with value 0 and
// sort below everybody.
func (s *State) Replacement(pos string) float64 {
	d := s.demandAt(pos)
	if d < 1 {
		return 0
	}
	vals := make([]int, 0, 64)
	for _, p := range s.Players {
		if p.Pos == pos && p.Value > 0 {
			vals = append(vals, p.Value)
		}
	}
	if len(vals) < d {
		// The room drafts deeper at this position than any source values it, so
		// the replacement player is somebody nobody rated: worth nothing, and vor
		// collapses to plain value. That is running back on today's board (58
		// drafted, 51 valued), not a degenerate case.
		return 0
	}
	sort.Sort(sort.Reverse(sort.IntSlice(vals)))
	return float64(vals[d-1])
}

// VOR is value over replacement for one player, floored at zero — a player below
// replacement is not worth negative points, he is worth nothing, and letting the
// number go negative would sort a position BELOW an empty one.
func (s *State) VOR(p Player) float64 {
	if v := float64(p.Value) - s.Replacement(p.Pos); v > 0 {
		return v
	}
	return 0
}

// demandAt is D_P: how many players at a position come off the board in one of
// this league's drafts. The measured table is handed in through State.Demand by
// the cmd layer, which is the only place that can read the cached drafts — the
// engine does no i/o.
//
// The fallback is the league's own SHAPE rather than an invented constant, so a
// board running without the draft cache is still ranked against something real:
// every team's dedicated slots at the position, plus an equal share of each flex
// slot the position is eligible for. It is a floor and not an equal — the default
// 12-team shape gives rb 28 against a measured 58, because rooms hoard backs on
// the bench and a lineup says nothing about benches. `pick6 fetch` prints which
// of the two is in play.
func (s *State) demandAt(pos string) int {
	if d := s.Demand[pos]; d > 0 {
		return d
	}
	n := 0
	for _, slot := range s.Roster.Slots {
		switch {
		case slot == pos:
			n += s.Teams
		case isFlexSlot(slot) && EligibleFor(slot, pos):
			n += s.Teams / flexEligibleCount(slot)
		}
	}
	return n
}

// flexEligibleCount is how many positions compete for one flex slot, which is
// how the fallback splits it between them.
func flexEligibleCount(slot string) int {
	if slot == "SUPERFLEX" {
		return len(SuperFlexEligible)
	}
	return len(FlexEligible)
}
