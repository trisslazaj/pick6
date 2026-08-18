package engine

import "sort"

// positions.go derives the draft's position vocabulary from the roster.
//
// It used to be three hardcoded six-string literals — plan.go's planPositions,
// sim.go's simPositions and the ui's own — which is exactly right until a second
// sport turns up with four positions and none of them named "RB". The lineup is
// the only place that knowledge ever really lived: a league that starts a
// position is a league whose board should rank it, and one that doesn't isn't.

// flexPosOrder is the order positions that reach a lineup ONLY through a flex
// slot get appended in. A fixed slice rather than a range over FlexEligible,
// because map order would hand the tie-break a different answer on every run —
// and the tie-break is the whole reason plan.go iterates a slice in the first
// place. Inert under both shipped rosters: nfl's lineup names all four of these
// outright, and fpl has no flex slot at all.
var flexPosOrder = []string{"QB", "RB", "WR", "TE"}

// Positions is this draft's position vocabulary: the distinct dedicated slots of
// the lineup in lineup order, then any position that can reach the lineup only
// through a flex slot.
//
// DefaultRoster derives to exactly QB, RB, WR, TE, K, DEF in that order — the
// literal all three old lists carried — so every nfl tie-break, need weight and
// rng draw is unchanged by construction rather than by hope.
// TestPositionsAreTheOldLiterals pins it, and golden_test.go pins the output.
//
// Computed fresh on every call rather than memoised on State: it is a walk over
// nine strings, the two callers that want it in a hot loop hoist it once each,
// and a cached copy would go stale the moment a test assigned Roster directly —
// which two of them do.
func (s *State) Positions() []string { return rosterPositions(s.Roster) }

// RosterPositions is Positions for a caller holding a roster and no state — the
// cmd layer's print order, which has to agree with the board's or `fetch` and
// `tiers` name their columns in an order the frame doesn't use.
func RosterPositions(r Roster) []string { return rosterPositions(r) }

func rosterPositions(r Roster) []string {
	seen := make(map[string]bool, len(r.Slots))
	out := make([]string, 0, len(r.Slots))
	for _, slot := range r.Slots {
		if isFlexSlot(slot) || seen[slot] {
			continue
		}
		seen[slot] = true
		out = append(out, slot)
	}
	for _, slot := range r.Slots {
		if !isFlexSlot(slot) {
			continue
		}
		for _, pos := range flexPosOrder {
			if !seen[pos] && EligibleFor(slot, pos) {
				seen[pos] = true
				out = append(out, pos)
			}
		}
	}
	return out
}

// DisplayPositions is Positions in READING order rather than tie-break order,
// and it is a second derivation because the ui already had a second order.
//
// The engine ranks positions by what a pick is worth and uses the lineup's order
// only to settle exact ties. The board's own lists were written the other way
// round — the positions that fill the most of your lineup first, specialists
// after — which for the default nfl shape is rb, wr, te, then qb, k, def. That
// is the order the data tab's filter key cycles and the tiers view groups by,
// and reordering it would be a gratuitous change to a frame nobody asked to
// change.
//
// The rule that reproduces it: positions that can reach a flex slot first, in
// lineup order, then everything else in lineup order. Under fpl nothing is
// flex-eligible, so it degrades to plain lineup order — gkp, def, mid, fwd.
// TestPositionsAreTheOldLiterals pins both.
func (s *State) DisplayPositions() []string {
	all := s.Positions()
	out := make([]string, 0, len(all))
	for _, pos := range all {
		if s.canReachFlex(pos) {
			out = append(out, pos)
		}
	}
	for _, pos := range all {
		if !s.canReachFlex(pos) {
			out = append(out, pos)
		}
	}
	return out
}

// posIndex maps a position to its index in a derived list, returning len(list)
// — the "unknown position" bucket — for anything the lineup does not name.
//
// A linear walk over four to six strings, called once per pool player at sim
// setup and never inside a rollout. A map would be slower at this size and would
// have to be built.
func posIndex(positions []string, pos string) int {
	for i, p := range positions {
		if p == pos {
			return i
		}
	}
	return len(positions)
}

// simPositions is Positions plus any position that is on the BOARD without being
// in the lineup — the opponents' vocabulary rather than the tool's.
//
// The difference matters because the two lists answer different questions.
// Positions is "what may this tool recommend", and a league with no kicker slot
// should never be told to draft a kicker. The sim's is "what may the room
// spend a pick on", and the room can spend one on anybody the board holds.
//
// Without the extras they all share the pool's "unknown position" bucket, which
// is priced at the bench weight UNCONDITIONALLY — it never asks opponentNeed,
// so it never sees the early-draft hold. Measured consequence: in a league whose
// lineup omits K and DEF (legal, and sleeper reports it), the simulated
// opponents started drafting kickers in round 4 instead of round 10, and every
// survival number on the board moved with them. Every position with its own
// bucket gets asked properly, and the unknown bucket goes back to meaning what
// it says: a player whose position no source gave us.
//
// Sorted, so the extras cannot arrive in map order. Under every roster whose
// lineup names every position the board holds — which is both shipped ones —
// nothing is appended and this is Positions exactly.
func (s *State) simPositions() []string {
	out := s.Positions()
	seen := make(map[string]bool, len(out))
	for _, pos := range out {
		seen[pos] = true
	}
	var extra []string
	for id, p := range s.Players {
		if s.Taken[id] || p.Pos == "" || seen[p.Pos] {
			continue
		}
		seen[p.Pos] = true
		extra = append(extra, p.Pos)
	}
	sort.Strings(extra)
	return append(out, extra...)
}
