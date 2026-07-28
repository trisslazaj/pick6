// Package engine holds the draft state and the pure math over it: snake
// arithmetic, roster filling, cliff and run detection. No I/O, no rendering.
package engine

import (
	"fmt"
	"sort"
)

// Player is the engine's view of a draftable player.
type Player struct {
	ID    string
	Name  string
	Pos   string
	Team  string
	Bye   int
	ADP   float64
	Sigma float64
	Value int
	Tier  int
}

// Roster describes a league's starting lineup.
type Roster struct {
	Slots []string // e.g. QB, RB, RB, WR, WR, TE, FLEX, K, DEF
	Bench int
}

// Pick is one completed selection.
type Pick struct {
	PickNo   int
	Round    int
	Slot     int // 1-indexed draft slot that made the pick
	PlayerID string
}

// State is everything the board needs. Recomputed from scratch on every pick
// event — it's a few thousand floats, don't optimize, don't cache.
type State struct {
	Players map[string]Player
	Taken   map[string]bool
	Picks   []Pick // in pick order
	Rosters map[int][]string

	MySlot int
	Teams  int // T
	Rounds int
	PickNo int // current overall pick, 1-indexed (the next pick to be made)
	Order  []int
	Roster Roster
}

// New builds a state for a snake draft with the natural slot order 1..T.
func New(players map[string]Player, teams, rounds, mySlot int) *State {
	order := make([]int, teams)
	for i := range order {
		order[i] = i + 1
	}
	return &State{
		Players: players,
		Taken:   map[string]bool{},
		Rosters: map[int][]string{},
		MySlot:  mySlot,
		Teams:   teams,
		Rounds:  rounds,
		PickNo:  1,
		Order:   order,
		Roster:  DefaultRoster,
	}
}

// ---- snake math. 1-indexed everywhere; Order is a 0-indexed Go slice. ----

// Round returns the round containing overall pick p.
func (s *State) Round(p int) int { return (p-1)/s.Teams + 1 }

// IndexInRound returns the 1-indexed position of pick p within its round.
func (s *State) IndexInRound(p int) int { return (p-1)%s.Teams + 1 }

// SlotAt returns the draft slot picking at overall pick p.
// Odd rounds run in order; even rounds reverse.
func (s *State) SlotAt(p int) int {
	i := s.IndexInRound(p)
	if s.Round(p)%2 == 1 {
		return s.Order[i-1]
	}
	return s.Order[s.Teams-i]
}

// MyPick returns my overall pick number in round r.
func (s *State) MyPick(r int) int {
	pos := s.slotPosition(s.MySlot)
	if r%2 == 1 {
		return (r-1)*s.Teams + pos
	}
	return (r-1)*s.Teams + (s.Teams - pos + 1)
}

// slotPosition is the 1-indexed index of slot within Order.
func (s *State) slotPosition(slot int) int {
	for i, v := range s.Order {
		if v == slot {
			return i + 1
		}
	}
	return 1
}

// NextPick is the smallest of my pick numbers at or after the current pick.
// When it's already my turn this returns PickNo, and the intervening set is
// empty — urgency then degenerates to "take bestNow", which is correct.
func (s *State) NextPick() int {
	for r := 1; r <= s.Rounds; r++ {
		if p := s.MyPick(r); p >= s.PickNo {
			return p
		}
	}
	return s.Rounds * s.Teams
}

// FollowingPick is the pick after NextPick. Returns 0 when the draft ends first.
func (s *State) FollowingPick() int {
	next := s.NextPick()
	for r := 1; r <= s.Rounds; r++ {
		if p := s.MyPick(r); p > next {
			return p
		}
	}
	return 0
}

// PicksUntilMine counts how many picks happen before my next one.
func (s *State) PicksUntilMine() int { return s.NextPick() - s.PickNo }

// RoundsRemaining counts rounds left including the current one.
func (s *State) RoundsRemaining() int {
	return s.Rounds - s.Round(s.PickNo) + 1
}

// OnTheClock reports the slot picking right now.
func (s *State) OnTheClock() int { return s.SlotAt(s.PickNo) }

// Done reports whether every pick has been made.
func (s *State) Done() bool { return s.PickNo > s.Teams*s.Rounds }

// ---- mutation ----

// Draft records a pick by whoever is on the clock and advances.
func (s *State) Draft(playerID string) {
	if s.Done() || s.Taken[playerID] {
		return
	}
	slot := s.OnTheClock()
	s.Taken[playerID] = true
	s.Rosters[slot] = append(s.Rosters[slot], playerID)
	s.Picks = append(s.Picks, Pick{
		PickNo:   s.PickNo,
		Round:    s.Round(s.PickNo),
		Slot:     slot,
		PlayerID: playerID,
	})
	s.PickNo++
}

// ApplyRemote records a pick reported by a live feed, trusting the feed's own
// slot and pick number rather than inferring them from the clock.
//
// It also cross-checks our snake math against reality: the feed says which slot
// made pick N, and so do we. A mismatch means our model of the draft order is
// wrong — a reversal round, a non-snake type that slipped past validation, a
// custom order — and every survival number downstream would be quietly bogus.
// Better to surface it than to keep drawing a confident board.
func (s *State) ApplyRemote(pickNo, round, slot int, playerID string) error {
	if s.Taken[playerID] {
		return nil // already applied; polling returns the full list every time
	}
	if want := s.SlotAt(pickNo); want != slot {
		return fmt.Errorf("draft order desync at pick %d: feed says slot %d, snake math says %d",
			pickNo, slot, want)
	}
	s.Taken[playerID] = true
	s.Rosters[slot] = append(s.Rosters[slot], playerID)
	s.Picks = append(s.Picks, Pick{PickNo: pickNo, Round: round, Slot: slot, PlayerID: playerID})
	if pickNo >= s.PickNo {
		s.PickNo = pickNo + 1
	}
	return nil
}

// EnsurePlayer registers a player we didn't know about, keeping whatever the
// caller knows (name, position, team) and leaving value and tier at zero.
//
// A live draft will absolutely pick players outside our board: it runs 192 picks
// against a 201-player ADP list, and people reach for handcuffs and rookies that
// never appear in ADP at all. Without this they'd render as blank roster rows.
// Zero value means they're untiered, so cliff logic ignores them — correct, since
// we have no basis to tier someone no source ranked.
func (s *State) EnsurePlayer(p Player) {
	if p.ID == "" {
		return
	}
	if _, known := s.Players[p.ID]; known {
		return
	}
	if p.Name == "" {
		p.Name = "unknown player"
	}
	s.Players[p.ID] = p
}

// SetRoster replaces the assumed lineup, e.g. with one read from Sleeper.
func (s *State) SetRoster(r Roster) {
	if len(r.Slots) > 0 {
		s.Roster = r
	}
}

// Undo reverses the most recent pick.
func (s *State) Undo() {
	if len(s.Picks) == 0 {
		return
	}
	last := s.Picks[len(s.Picks)-1]
	s.Picks = s.Picks[:len(s.Picks)-1]
	delete(s.Taken, last.PlayerID)
	r := s.Rosters[last.Slot]
	if len(r) > 0 {
		s.Rosters[last.Slot] = r[:len(r)-1]
	}
	s.PickNo = last.PickNo
}

// ---- queries ----

// Available returns every undrafted player at a position, best value first.
func (s *State) Available(pos string) []Player {
	var out []Player
	for id, p := range s.Players {
		if p.Pos == pos && !s.Taken[id] {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].ADP < out[j].ADP
	})
	return out
}

// BestNow is the highest-value available player at a position.
func (s *State) BestNow(pos string) (Player, bool) {
	a := s.Available(pos)
	if len(a) == 0 {
		return Player{}, false
	}
	return a[0], true
}

// TierRemaining counts undrafted players sharing a position and tier.
// Tier 0 means "no value from any source" and never counts as a tier.
func (s *State) TierRemaining(pos string, tier int) int {
	if tier == 0 {
		return 0
	}
	n := 0
	for id, p := range s.Players {
		if p.Pos == pos && p.Tier == tier && !s.Taken[id] {
			n++
		}
	}
	return n
}

// FilledSlots assigns a team's drafted players to starting slots, dedicated
// first and flex last, and returns one entry per slot (empty string if unfilled)
// plus whatever spilled onto the bench.
func (s *State) FilledSlots(slot int) (filled []string, bench []string) {
	filled = make([]string, len(s.Roster.Slots))
	used := map[string]bool{}

	// Dedicated slots first, so a flex-eligible player never squats on a flex
	// slot while his own position sits open.
	for i, want := range s.Roster.Slots {
		if isFlexSlot(want) {
			continue
		}
		for _, id := range s.Rosters[slot] {
			if used[id] {
				continue
			}
			if s.Players[id].Pos == want {
				filled[i] = id
				used[id] = true
				break
			}
		}
	}
	for i, want := range s.Roster.Slots {
		if !isFlexSlot(want) {
			continue
		}
		for _, id := range s.Rosters[slot] {
			if used[id] || !EligibleFor(want, s.Players[id].Pos) {
				continue
			}
			filled[i] = id
			used[id] = true
			break
		}
	}
	for _, id := range s.Rosters[slot] {
		if !used[id] {
			bench = append(bench, id)
		}
	}
	return filled, bench
}

// UnfilledStarters lists the starting slots a team still has open, in lineup
// order. This is the "what do I actually still need" read.
func (s *State) UnfilledStarters(slot int) []string {
	filled, _ := s.FilledSlots(slot)
	var out []string
	for i, want := range s.Roster.Slots {
		if filled[i] == "" {
			out = append(out, want)
		}
	}
	return out
}

// ByeLoad counts a team's filled *starters* by bye week. Bench players are
// excluded — a bench bye is not a problem, a starting bye is.
func (s *State) ByeLoad(slot int) map[int][]string {
	filled, _ := s.FilledSlots(slot)
	out := map[int][]string{}
	for _, id := range filled {
		if id == "" {
			continue
		}
		if bye := s.Players[id].Bye; bye > 0 {
			out[bye] = append(out[bye], id)
		}
	}
	return out
}

// ByeConflicts returns only the weeks where a team has ByeConflictThreshold or
// more starters idle at once, worst week first. Two starters sharing a bye is
// unremarkable across a nine-slot lineup; three is a week you can't field.
func (s *State) ByeConflicts(slot int) []ByeConflict {
	var out []ByeConflict
	for week, ids := range s.ByeLoad(slot) {
		if len(ids) >= ByeConflictThreshold {
			out = append(out, ByeConflict{Week: week, Players: ids})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Players) != len(out[j].Players) {
			return len(out[i].Players) > len(out[j].Players)
		}
		return out[i].Week < out[j].Week
	})
	return out
}

// ByeConflict is a week where too many starters are idle at once.
type ByeConflict struct {
	Week    int
	Players []string
}

// MyUpcomingPicks returns my next N pick numbers from the current pick onward.
func (s *State) MyUpcomingPicks(n int) []int {
	var out []int
	for r := 1; r <= s.Rounds && len(out) < n; r++ {
		if p := s.MyPick(r); p >= s.PickNo {
			out = append(out, p)
		}
	}
	return out
}

// Need returns the urgency weight for a position given my roster.
func (s *State) Need(pos string) float64 {
	// Nobody needs a tool to tell them to draft a kicker in round 6.
	if (pos == "K" || pos == "DEF") && s.RoundsRemaining() > KDefLastRounds {
		return 0
	}
	filled, _ := s.FilledSlots(s.MySlot)
	for i, want := range s.Roster.Slots {
		if want == pos && filled[i] == "" {
			return NeedStarter
		}
	}
	for i, want := range s.Roster.Slots {
		if isFlexSlot(want) && filled[i] == "" && EligibleFor(want, pos) {
			return NeedFlex
		}
	}
	return NeedBench
}

// isFlexSlot reports whether a lineup slot takes more than one position.
func isFlexSlot(slot string) bool {
	return slot == "FLEX" || slot == "SUPERFLEX"
}

// EligibleFor reports whether a position can fill a lineup slot.
func EligibleFor(slot, pos string) bool {
	switch slot {
	case "FLEX":
		return FlexEligible[pos]
	case "SUPERFLEX":
		return SuperFlexEligible[pos]
	default:
		return slot == pos
	}
}
