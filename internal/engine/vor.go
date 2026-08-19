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
//	D_P  = how deep the position's replacement level sits (see the rule below)
//	R(P) = the value of the D_P-th best P on the pre-draft board
//	vor  = max(0, v(j) - R(pos_j))
//
// D_P is NOT one measurement for every position, and the split is the point:
//
//   - A flex-eligible position (RB/WR/TE, plus QB under a superflex) indexes at
//     the room's own measured draft demand — qb 19, rb 58, wr 67, te 17 over the
//     three cached drafts (adp.PositionDemand; the cmd layer hands it in). A
//     bench back or receiver genuinely converts to lineup points all season —
//     flex, byes, injuries — so the man you'd have settled for really is the
//     last one this room rosters.
//
//   - A position that cannot reach a lineup through flex (QB in a 1QB league,
//     K, DEF) indexes at STARTABLE slots: teams x dedicated slots. A bench arm
//     in a 1QB league scores zero by construction, so the settle-for man is the
//     worst STARTER (qb12, ~1173 on the 2026 board), not the 19th quarterback
//     somebody benched (~243). Indexing on drafted counts there handed
//     quarterbacks a ~930-point vor subsidy and was how the board kept arguing
//     for early QBs.
//
// On the shipped 2026 board that gives replacement levels of qb 1173, te 128,
// wr 10 and rb 0 — the room drafts 58 backs while only 53 carry a value at all,
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

// ReplacementIndex is the depth Replacement actually indexed at, exported so
// `pick6 fetch` can name the settle-for player at the same depth the engine
// priced him — a printout that re-derived the index would drift the first time
// the rule changed, which is exactly what happened to it once.
func (s *State) ReplacementIndex(pos string) int { return s.demandAt(pos) }

// VOR is value over replacement for one player, floored at zero — a player below
// replacement is not worth negative points, he is worth nothing, and letting the
// number go negative would sort a position BELOW an empty one.
func (s *State) VOR(p Player) float64 {
	if v := float64(p.Value) - s.Replacement(p.Pos); v > 0 {
		return v
	}
	return 0
}

// demandAt is D_P, under the two-index rule the package comment explains: a
// position whose bench bodies can score (it can reach a lineup through a flex
// slot) indexes at the room's measured draft demand; one whose bench bodies
// cannot (QB in 1QB, K, DEF) indexes at startable slots, however many the room
// drafts. The measured table is handed in through State.Demand by the cmd
// layer, which is the only place that can read the cached drafts — the engine
// does no i/o.
//
// The rule reads the ROSTER, not a position list, so a superflex league flips
// QB to the measured index by itself — there a second quarterback genuinely
// starts, and his settle-for man really is deep.
//
// The startable arithmetic doubles as the fallback when no demand table was
// measured at all: every team's dedicated slots at the position, plus an equal
// share of each flex slot the position is eligible for. For a flex position it
// is then a floor and not an estimate — the default 12-team shape gives rb 28
// against a measured 58, because rooms hoard backs on the bench and a lineup
// says nothing about benches. `pick6 fetch` prints which index is in play.
func (s *State) demandAt(pos string) int {
	// Under a draft cap the demand is not estimated, it is arithmetic: every seat
	// must end with exactly its cap at every position, so the room rosters
	// cap x teams of them and the last one really is the replacement. No
	// measurement can improve on a rule.
	if s.Roster.capped() {
		if max, ok := s.Roster.Max[pos]; ok {
			return max * s.Teams
		}
	}
	if s.canReachFlex(pos) {
		if d := s.Demand[pos]; d > 0 {
			return d
		}
	}
	return s.startable(pos)
}

// canReachFlex reports whether a position has any route into a lineup beyond
// its dedicated slots — the question that decides which replacement index it
// gets.
func (s *State) canReachFlex(pos string) bool {
	for _, slot := range s.Roster.Slots {
		if isFlexSlot(slot) && s.Roster.eligible(slot, pos) {
			return true
		}
	}
	return false
}

// startable counts the lineup slots a position can fill across the league:
// dedicated slots at every team, plus an equal share of each flex slot it is
// eligible for.
func (s *State) startable(pos string) int {
	n := 0
	for _, slot := range s.Roster.Slots {
		switch {
		case slot == pos:
			n += s.Teams
		case isFlexSlot(slot) && s.Roster.eligible(slot, pos):
			n += s.Teams / s.Roster.flexCount(slot)
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
