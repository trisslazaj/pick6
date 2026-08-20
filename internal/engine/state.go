// Package engine holds the draft state and the pure math over it: snake
// arithmetic, roster filling, cliff and run detection. No I/O, no rendering.
package engine

import (
	"fmt"
	"sort"
)

// Player is the engine's view of a draftable player. Everything below the blank
// line is carried for display only — the engine never reads it, but the whole
// point of the data tab is showing every number the sources gave us.
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

	// ADPEff is ADP as THIS room prices it: the national number blended with
	// where the k-th player at his position actually goes in this league's own
	// completed drafts (adp.RoomCurve). It is populated by default on mock and
	// live, at every depth the curve reaches — the top-k cap this comment used
	// to describe won one fold, lost both causal ones, and was retracted along
	// with the constant that named it.
	//
	// 0 therefore means "no warp for this man": the curve never reached his
	// rank, no draft is cached, or `-room=false`. Every one of those is the same instruction — price him off
	// raw ADP — which is what price() does.
	//
	// Exactly two things read it, PSurviveAt and Falling, and that is the whole
	// design. Every other reader of ADP — the display columns, Available's sort
	// tie-break, the mock's picker — must keep seeing the raw market price, so
	// this is a separate field rather than an overwritten ADP.
	ADPEff float64

	Stdev        float64 // observed draft-position spread, 0 when unknown
	FormatSpread float64 // largest ADP gap across scoring formats
	TierSrc      string  // "rankings" or "derived", "" when untiered

	// Sentiment is the rankings file's opinion — "target", "pass" or "avoid",
	// "" when it said nothing. Display only: nothing in this package reads it.
	Sentiment string

	// Sample support behind ADP: the drafts it averages, and the earliest and
	// latest pick he actually went at in any of them. TimesDrafted has already
	// done its work at fetch time, where it weights the sigma shrink — the
	// number that arrives here is a display column. High is the input
	// SupportFloor would read if it were wired into survival; it is not, and
	// that function carries the measurement that decided it.
	TimesDrafted int
	High         int
	Low          int

	// Injury truth, frozen at fetch time and never priced in. Survival and value
	// must not read these: our values are imported, and marking one down for an
	// injury would be a projection of our own, which this project doesn't make.
	// The board shows the fact and lets the human do the discounting.
	InjuryStatus string // "" is the normal case; "" in Status means unknown, not hurt
	Status       string
	NewsUpdated  int64 // epoch milliseconds, 0 when unknown

	// Sidelined is the one injury fact that DOES reach the engine, and it is not
	// a value judgement — it is "he cannot play, and the source cannot say when
	// he will". Nothing is marked down: he is simply not a candidate for my own
	// board, the way a drafted player is not.
	//
	// The exception exists because a market can fail to reprice. Fpl's
	// draft_rank had Ekitiké at 19 on draft morning with an achilles tear and no
	// return date, so the recommendation led with him for fifteen rounds and
	// nothing downstream could argue: value came from the rank, survival came
	// from the rank, and every chip on him was display-only by design. Set by
	// fpl.Element.Sidelined; false everywhere in nfl, where adp does reprice.
	//
	// It takes him off MY board and not off the room's. Opponents still draft
	// him in the sim (they really do draft him), the feed still registers him,
	// search still finds him, and the data tab still lists him. See State.OffMyBoard.
	Sidelined bool
}

// Roster describes a league's starting lineup.
type Roster struct {
	Slots []string // e.g. QB, RB, RB, WR, WR, TE, FLEX, K, DEF
	Bench int

	// Max is the most players you may DRAFT at a position, which is a different
	// limit from how many may start. Fpl caps a squad at 2 gkp / 5 def / 5 mid /
	// 3 fwd; a sixth defender is not a bad pick there, it is one the official app
	// refuses. nil means no cap, which is every nfl league.
	//
	// Deliberately not inferred from the lineup: fpl starts eleven of fifteen, so
	// "all my def lineup slots are full" and "I may not draft another def" are
	// four picks apart.
	Max map[string]int

	// Flex is which positions a FLEX slot takes, when the sport's own answer is
	// not the nfl one. nil means FlexEligible — rb, wr, te — because every
	// production site that builds a roster from sleeper metadata writes Slots and
	// Bench and nothing else.
	Flex map[string]bool

	// Hold is the set of positions nobody should be drafting early — k and def in
	// nfl, nothing at all in fpl, where every position is somebody's starter from
	// round one.
	//
	// NIL MEANS {K, DEF}, which is the trap, and NoHold is what makes it
	// survivable: every production site that builds a Roster out of sleeper
	// metadata writes Slots and Bench and nothing else, so a zero value meaning
	// "hold nothing" would quietly un-suppress kickers on the real board. A sport
	// that holds nothing has to say so out loud.
	Hold map[string]bool
}

// NoHold is an explicitly empty hold set: no position is held back. A named
// value so a roster that suppresses nothing reads as a decision at the call site
// rather than as somebody forgetting a field.
var NoHold = map[string]bool{}

// holds reports whether a position is subject to the early-draft hold. See the
// Hold field for why nil is the nfl default rather than the empty set.
func (r Roster) holds(pos string) bool {
	if r.Hold == nil {
		return pos == "K" || pos == "DEF"
	}
	return r.Hold[pos]
}

// capped reports whether this roster limits how many of a position you may draft.
func (r Roster) capped() bool { return len(r.Max) > 0 }

// Squad is the roster as one entry per player you may OWN — 2 gkp, 5 def, 5 mid,
// 3 fwd under fpl — rather than per lineup slot. nil when the roster has no
// draft cap, which is every nfl league: there the lineup plus a bench count IS
// the shape, and there is no second answer to give.
//
// It exists for the sidebar, and the reason is that a capped roster has two
// true shapes and the lineup is the less useful one to stare at while drafting.
// Eleven starting slots plus "however many spilled" means the bench rows appear
// one at a time as you draft into them, so the pane grew from eleven rows to
// fifteen over the back half of a draft and never once showed you the four
// empty squad places you still had to fill. The quota is the thing a capped
// draft is actually spending.
//
// Order is first appearance in Slots, so it comes out in lineup order — gkp,
// def, mid, fwd — rather than in map order, which would reshuffle the pane
// between renders. A capped position the lineup never names is appended in
// sorted order for the same reason.
func (r Roster) Squad() []string {
	if !r.capped() {
		return nil
	}
	var order []string
	seen := map[string]bool{}
	for _, slot := range r.Slots {
		if isFlexSlot(slot) || seen[slot] || r.Max[slot] == 0 {
			continue
		}
		order, seen[slot] = append(order, slot), true
	}
	var rest []string
	for pos := range r.Max {
		if !seen[pos] {
			rest = append(rest, pos)
		}
	}
	sort.Strings(rest)
	var out []string
	for _, pos := range append(order, rest...) {
		for i := 0; i < r.Max[pos]; i++ {
			out = append(out, pos)
		}
	}
	return out
}

// full reports whether a seat has already drafted every player it is allowed at
// a position. Always false without a cap.
func (r Roster) full(pos string, n int) bool {
	max, ok := r.Max[pos]
	return ok && n >= max
}

// flexTakes reports whether a FLEX slot accepts a position on this roster.
func (r Roster) flexTakes(pos string) bool {
	if r.Flex == nil {
		return FlexEligible[pos]
	}
	return r.Flex[pos]
}

// Pick is one completed selection.
type Pick struct {
	PickNo    int
	Round     int
	Slot      int // 1-indexed draft slot that made the pick
	OwnerSlot int // slot whose roster received the player; differs when traded
	PlayerID  string
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

	// Demand is D_P: how many players at each position this league drafts in one
	// draft, measured from its own completed drafts. It sets the replacement
	// level vor is computed against (see vor.go). nil is fine and is what every
	// test uses — the fallback derives a demand from the league's lineup shape
	// instead, which is a floor rather than a measurement.
	Demand map[string]int

	// Survival selects the survival model: SurvivalSim runs the opponent-aware
	// rollouts of sim.go, anything else (including the zero value) the ADP
	// logistic + tilt. Sim is the product default and the cmd layer sets it;
	// the engine's own zero value stays adp so the v1 math keeps its tests.
	Survival string
	// Scorer selects the decision score under sim: the ZERO VALUE is
	// ScorerPair, milestone 7's two-leg pair score, and ScorerRoster is
	// milestone 8's finished-roster objective. It does nothing under adp, which
	// has one formula and keeps it wholesale. See the constants for why the new
	// one is off.
	Scorer string
	// SimSeed is the base the per-vantage rollout seed mixes from, so a mock
	// or a test can replay identical futures. Zero is a fine base; what matters
	// is that the seed is derived, never wall-clock — a keypress re-render must
	// not jiggle every percentage on the board.
	SimSeed int64

	// OffBoard is the sim's escape hatch, indexed by full rounds remaining
	// AFTER a pick's own round (so index 0 is the final round): the measured
	// probability that a pick at that depth takes a player the ranked pool
	// cannot see — a handcuff, a rookie flier, somebody's third kicker. Without
	// it every simulated pick removes a ranked player, which real drafts don't
	// do, so the sim eats the board faster than reality and everyone reads more
	// gone than they are (measured: the whole of v2's first-run log-loss win
	// was this, supplied by a leak). nil means no escape, which is the engine
	// tests' regime and the honest answer when no prior drafts exist to
	// measure one from.
	OffBoard []float64

	sim  *simTable  // cached rollouts for this vantage; every mutator nils it
	plan *planTable // ...and the conditioned lookahead's, on the same rule
}

// invalidate drops every vantage-keyed cache. One method rather than two
// assignments at five call sites, because the failure mode of forgetting the
// second one is a board that quotes last pick's plan next to this pick's
// survivals — two vantages on one frame, which is the exact bug the caches are
// keyed to the vantage to prevent.
func (s *State) invalidate() {
	s.sim = nil
	s.plan = nil
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
		PickNo:    s.PickNo,
		Round:     s.Round(s.PickNo),
		Slot:      slot,
		OwnerSlot: slot,
		PlayerID:  playerID,
	})
	s.PickNo++
	s.invalidate()
}

// ApplyRemote records a pick reported by a live feed, trusting the feed's own
// slot and pick number rather than inferring them from the clock.
//
// It also cross-checks our snake math against reality: the feed says which slot
// made pick N, and so do we. A mismatch means our model of the draft order is
// wrong — a reversal round, a non-snake type that slipped past validation, a
// custom order — and every survival number downstream would be quietly bogus.
// Better to surface it than to keep drawing a confident board.
func (s *State) ApplyRemote(r RemotePick) error {
	if s.Taken[r.PlayerID] {
		return nil // already applied; polling returns the full list every time
	}
	// SlotAt indexes Order off the pick number, so a pick number outside the
	// draft indexes outside the slice: pick 0 asks for Order[-1] and panics
	// rather than reporting the nonsense it was handed.
	if last := s.Teams * s.Rounds; r.PickNo < 1 || r.PickNo > last {
		return fmt.Errorf("pick %d is outside this draft (1..%d)", r.PickNo, last)
	}
	if want := s.SlotAt(r.PickNo); want != r.Slot {
		return fmt.Errorf("draft order desync at pick %d: feed says slot %d, snake math says %d",
			r.PickNo, r.Slot, want)
	}
	// The slot that picks and the roster that receives are usually the same, but
	// diverge when a draft pick has been traded. Attribute the player to whoever
	// actually gets him, or a pick you traded for never lands on your board.
	owner := r.OwnerSlot
	if owner == 0 {
		owner = r.Slot
	}
	s.Taken[r.PlayerID] = true
	s.Rosters[owner] = append(s.Rosters[owner], r.PlayerID)
	s.Picks = append(s.Picks, Pick{
		PickNo: r.PickNo, Round: r.Round, Slot: r.Slot,
		OwnerSlot: owner, PlayerID: r.PlayerID,
	})
	if r.PickNo >= s.PickNo {
		s.PickNo = r.PickNo + 1
	}
	s.invalidate()
	return nil
}

// RemotePick is one pick as reported by a live feed.
type RemotePick struct {
	PickNo    int
	Round     int
	Slot      int // the seat that picked, used to verify the snake order
	OwnerSlot int // the seat whose roster receives the player (differs if traded)
	PlayerID  string
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
	s.invalidate()
}

// SetRoster replaces the assumed lineup, e.g. with one read from Sleeper.
func (s *State) SetRoster(r Roster) {
	if len(r.Slots) > 0 {
		s.Roster = r
		s.invalidate()
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
	owner := last.OwnerSlot
	if owner == 0 {
		owner = last.Slot
	}
	if r := s.Rosters[owner]; len(r) > 0 {
		s.Rosters[owner] = r[:len(r)-1]
	}
	s.PickNo = last.PickNo
	s.invalidate()
}

// ---- queries ----

// OffMyBoard reports whether a player is out of my own consideration: drafted,
// or sidelined long enough that recommending him would be absurd.
//
// One predicate rather than a check per site, because the frame is not allowed
// to disagree with itself — a man the row order skips while the tier count
// still holds a place for him prints "3 left in tier 2" over two names. Every
// "what could I take" read goes through here; every "what will the room do"
// read deliberately does not, and those are the ones spelled `s.Taken[id]` on
// their own (the sim pool, the tilt's removal budget, the run forecast).
func (s *State) OffMyBoard(id string, p Player) bool { return s.Taken[id] || p.Sidelined }

// Available returns every undrafted player at a position, best value first.
func (s *State) Available(pos string) []Player {
	var out []Player
	for id, p := range s.Players {
		if p.Pos == pos && !s.OffMyBoard(id, p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		if out[i].ADP != out[j].ADP {
			return out[i].ADP < out[j].ADP
		}
		return out[i].ID < out[j].ID // full ties would fall to map order otherwise
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
		if p.Pos == pos && p.Tier == tier && !s.OffMyBoard(id, p) {
			n++
		}
	}
	return n
}

// TierSize counts every player in a position's tier, drafted or not. Used to
// tell a tier that is emptying from one that was always small.
//
// Sidelined men are not counted, and the pairing with TierRemaining is why: a
// tier that never offered him has not lost him, and counting him here while
// TierRemaining skips him makes an untouched tier look eaten and fires a cliff
// on pick 1.01.
func (s *State) TierSize(pos string, tier int) int {
	if tier == 0 {
		return 0
	}
	n := 0
	for _, p := range s.Players {
		if p.Pos == pos && p.Tier == tier && !p.Sidelined {
			n++
		}
	}
	return n
}

// FilledSlots assigns a team's drafted players to starting slots, dedicated
// first and flex last, and returns one entry per slot (empty string if unfilled)
// plus whatever spilled onto the bench.
func (s *State) FilledSlots(slot int) (filled []string, bench []string) {
	return s.fillSlots(s.Rosters[slot])
}

// fillSlots is that same rule over an explicit list of ids rather than a seat,
// so the two-pick lookahead can ask what my lineup would look like with one more
// player on it. Mutating Rosters and restoring it afterwards would be shorter
// and wrong: the ui calls this during a render, and a panic between the mutation
// and the restore would leave every later frame describing a roster that never
// existed.
func (s *State) fillSlots(ids []string) (filled []string, bench []string) {
	filled = make([]string, len(s.Roster.Slots))
	used := make([]bool, len(ids))
	s.assign(ids, filled, used, make([]string, len(ids)))
	for i, id := range ids {
		if !used[i] {
			bench = append(bench, id)
		}
	}
	return filled, bench
}

// assign is the fill rule itself, over buffers the caller owns. It exists as a
// separate function for one reason: the milestone-8 rollouts run it a few
// hundred thousand times per pick event — once per simulated opponent pick, to
// price that seat's need — and the map lookups and allocations fillSlots used to
// do inside its inner loop were most of the cost of the whole objective.
//
// The buffers are `filled` (one per lineup slot), and `used`/`pos` (one per id).
// pos is filled first so a player's position costs ONE map lookup rather than
// one per slot it is compared against; `used` is indexed by position in `ids`
// rather than keyed by player, which is the same thing for a roster (no team
// holds a player twice) and free.
func (s *State) assign(ids []string, filled []string, used []bool, pos []string) {
	for i, id := range ids {
		pos[i] = s.Players[id].Pos
		used[i] = false
	}
	for i := range filled {
		filled[i] = ""
	}
	// Dedicated slots first, so a flex-eligible player never squats on a flex
	// slot while his own position sits open.
	for si, want := range s.Roster.Slots {
		if isFlexSlot(want) {
			continue
		}
		for i := range ids {
			if used[i] || pos[i] != want {
				continue
			}
			filled[si] = ids[i]
			used[i] = true
			break
		}
	}
	for si, want := range s.Roster.Slots {
		if !isFlexSlot(want) {
			continue
		}
		for i := range ids {
			if used[i] || !s.Roster.eligible(want, pos[i]) {
				continue
			}
			filled[si] = ids[i]
			used[i] = true
			break
		}
	}
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
//
// The endgame slack multiplies the BENCH weight and nothing else: needFrom has
// already returned for anyone who fills an open slot, and for a suppressed k/def
// whose weight is zero either way. Keeping the multiplier here rather than
// inside needFrom is what lets the two-pick plan price a pair without it — see
// BestPlan, where feasibility is modelled exactly instead of approximated.
func (s *State) Need(pos string) float64 {
	mine := s.Rosters[s.MySlot]
	filled, _ := s.FilledSlots(s.MySlot)
	n := s.needFrom(pos, mine, filled)
	if n == NeedBench {
		n *= endgameSlack(s.MyPicksLeft(), unfilledCount(filled))
	}
	return n
}

// MyPicksLeft counts the picks I still have, including this one when it's mine.
func (s *State) MyPicksLeft() int { return len(s.MyUpcomingPicks(s.Rounds)) }

// NeedAfter is Need as it would read with playerID already on my roster. The
// second leg of a two-pick plan is chosen against the lineup the first leg
// leaves behind — taking a rb with the first pick is exactly what drops rb to
// flex weight for the second — so the lookahead needs this and the board does
// not.
//
// The id goes onto a copy with its own backing array. Appending straight onto
// Rosters[MySlot] would write into that slice's spare capacity: invisible to any
// equality check on State, and still a write into live state during a render.
//
// No endgame slack, deliberately, and Need's leg of the same plan does not apply
// it either. The slack is a one-pick proxy for "a bench pick is a starting slot
// you didn't fill", and a pair already models that exactly through mustFill.
// Charging it on top made the pair score depend on the ORDER of two legs that
// end at the same roster: at R == U+1 leg one saw an open starter and paid
// NeedBench*EndgameSlack, leg two saw the lineup already complete and paid the
// full NeedBench, so the plan preferred to spend the fungible pick first. On the
// real board at 14.10 that printed "k at 14.10 -> rb at 15.03" while the pane
// under it put the accent border on rb.
func (s *State) NeedAfter(pos, playerID string) float64 {
	mine := s.Rosters[s.MySlot]
	ids := make([]string, 0, len(mine)+1)
	ids = append(ids, mine...)
	ids = append(ids, playerID)
	filled, _ := s.fillSlots(ids)
	return s.needFrom(pos, ids, filled)
}

// Suppressed reports whether the k/def hold is on: nobody needs a tool to tell
// them to draft a kicker in round 6.
//
// Exported because it is not the same question as Need == 0 and callers kept
// asking the wrong one. Need reaches zero for a skill position too once the
// endgame guard bites, so a ui check written as "Need == 0 means k/def" started
// silently covering half the board the moment MustFillStarters turned true.
func (s *State) Suppressed(pos string) bool {
	return s.Roster.holds(pos) && s.RoundsRemaining() > KDefLastRounds
}

// needFrom is the need rule itself, over an already-filled lineup. The endgame
// slack is applied by Need, not here — see NeedAfter for why the plan wants this
// number without it.
func (s *State) needFrom(pos string, ids, filled []string) float64 {
	if s.Suppressed(pos) {
		return 0
	}
	return s.needSlots(pos, ids, filled)
}

// needSlots is the roster-shape half of the need rule — what an open slot at
// pos is worth given an already-filled lineup — with no K/DEF suppression
// applied. needFrom wraps it with OUR suppression; the sim's opponentNeed
// wraps it with the room's looser one. The split is what keeps two different
// questions (what the tool recommends, what the room does) from sharing a
// constant by accident.
func (s *State) needSlots(pos string, ids, filled []string) float64 {
	// The draft cap first, because it is a different question from the lineup
	// and it outranks it. Fpl lets you own two keepers and start one, so "every
	// gkp lineup slot is taken" and "you may not draft another gkp" are a pick
	// apart — and only the second is a zero. Every consumer that already knows
	// how to hide a zero-need position (the field, the plan's membership test,
	// the opponents' sampling) hides an illegal pick for free.
	if s.Roster.capped() && s.Roster.full(pos, s.countAt(pos, ids)) {
		return 0
	}
	for i, want := range s.Roster.Slots {
		if want == pos && filled[i] == "" {
			return NeedStarter
		}
	}
	for i, want := range s.Roster.Slots {
		if isFlexSlot(want) && filled[i] == "" && s.Roster.eligible(want, pos) {
			return NeedFlex
		}
	}
	return NeedBench
}

// countAt is how many of a position a seat already owns.
func (s *State) countAt(pos string, ids []string) int {
	n := 0
	for _, id := range ids {
		if s.Players[id].Pos == pos {
			n++
		}
	}
	return n
}

// endgameSlack is the feasibility guard: late enough in a draft, a bench pick is
// a starting slot you didn't fill.
//
// It applies only to a position that fills NONE of my open starting slots: Need
// reaches it only when needFrom came back with the bench weight, and needFrom
// has already returned for everyone who fills one — dedicated or flex. That
// ordering is the whole reason this can be a multiplier instead of a membership
// test: FLEX is a slot name, not a position, so "is rb among my unfilled
// starters" is false in the one case that matters most, when the flex slot an rb
// would fill is the thing still open.
//
// One caller, deliberately. The two-pick plan does NOT apply it — see NeedAfter.
//
// With R = my remaining picks and U = my unfilled starters:
//
//	R <  U    already lost. Nothing changes: the hole cannot be filled, and
//	          suppressing the rest of the board over it would bury the value
//	          still there behind a demand that can no longer be met.
//	R == U    every remaining pick must fill a starter, so a bench flier is worth
//	          exactly nothing. Zero need hides the position outright, and the
//	          board says why in one line.
//	R == U+1  one pick of slack. Half weight — a flier is still affordable, but
//	          it costs the only spare pick left.
func endgameSlack(picksLeft, unfilled int) float64 {
	if unfilled == 0 {
		return 1 // lineup complete: every pick left is a bench pick by definition
	}
	switch {
	case picksLeft == unfilled:
		return 0
	case picksLeft == unfilled+1:
		return EndgameSlack
	default:
		return 1
	}
}

// unfilledCount is how many starting slots a filled lineup still has open.
func unfilledCount(filled []string) int {
	n := 0
	for _, id := range filled {
		if id == "" {
			n++
		}
	}
	return n
}

// MustFillStarters is the R == U state: I have exactly as many picks left as
// open starting slots, so every one of them is spoken for. The board draws a
// line saying so, and needFrom has already zeroed everything that can't fill one.
//
// False once R < U as well as when R > U — a lineup I can no longer complete is
// a different situation, and telling someone every pick must fill a starter when
// that is arithmetically impossible is just noise. A finished draft falls out of
// the same test: R is 0 and U is not.
func (s *State) MustFillStarters() bool {
	filled, _ := s.FilledSlots(s.MySlot)
	u := unfilledCount(filled)
	return u > 0 && s.MyPicksLeft() == u
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

// eligible is EligibleFor with this roster's own flex vocabulary, which is what
// every site inside the engine should ask. An fpl flex takes def, mid and fwd;
// the package-level answer is nfl's and would take none of them.
func (r Roster) eligible(slot, pos string) bool {
	if slot == "FLEX" {
		return r.flexTakes(pos)
	}
	return EligibleFor(slot, pos)
}

// Capped reports whether this roster limits how many of a position you may
// draft, which is the question the ui asks when it wants to know whether it is
// drawing a squad or a lineup-plus-bench.
func (s *State) Capped() bool { return s.Roster.capped() }

// CapReached reports whether a roster has already drafted every player it is
// allowed at a position. False for any roster with no cap.
func (s *State) CapReached(pos string, ids []string) bool {
	return s.Roster.capped() && s.Roster.full(pos, s.countAt(pos, ids))
}

// flexOrder is the positions a flex slot might take, in a fixed order so the
// derivation cannot come out of a go map. Nil Flex means nfl's.
func (r Roster) flexOrder() []string {
	if r.Flex == nil {
		return flexPosOrder
	}
	out := make([]string, 0, len(r.Flex))
	for pos := range r.Flex {
		out = append(out, pos)
	}
	sort.Strings(out)
	return out
}

// flexCount is how many positions compete for one flex slot on this roster,
// which is how the startable fallback splits it between them.
func (r Roster) flexCount(slot string) int {
	if slot == "FLEX" && r.Flex != nil {
		if n := len(r.Flex); n > 0 {
			return n
		}
	}
	return flexEligibleCount(slot)
}
