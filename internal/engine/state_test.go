package engine

import "testing"

func newTestState(teams, rounds, mySlot int) *State {
	return New(map[string]Player{}, teams, rounds, mySlot)
}

func TestSnakeSlotAt(t *testing.T) {
	s := newTestState(12, 15, 3)
	cases := []struct {
		pick, round, slot int
	}{
		{1, 1, 1},   // start of an odd round
		{12, 1, 12}, // end of an odd round
		{13, 2, 12}, // the turn: last team picks again
		{14, 2, 11},
		{24, 2, 1}, // end of an even round
		{25, 3, 1}, // odd round resumes in order
		{36, 3, 12},
	}
	for _, c := range cases {
		if got := s.Round(c.pick); got != c.round {
			t.Errorf("Round(%d) = %d, want %d", c.pick, got, c.round)
		}
		if got := s.SlotAt(c.pick); got != c.slot {
			t.Errorf("SlotAt(%d) = %d, want %d", c.pick, got, c.slot)
		}
	}
}

func TestMyPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	// Slot 3 picks 3rd in odd rounds and 10th in even rounds.
	cases := []struct{ round, pick int }{
		{1, 3},
		{2, 22}, // 12 + (12-3+1)
		{3, 27},
		{4, 46},
	}
	for _, c := range cases {
		if got := s.MyPick(c.round); got != c.pick {
			t.Errorf("MyPick(%d) = %d, want %d", c.round, got, c.pick)
		}
		// Cross-check against SlotAt: the pick must actually belong to me.
		if got := s.SlotAt(c.pick); got != s.MySlot {
			t.Errorf("SlotAt(MyPick(%d)=%d) = %d, want %d", c.round, c.pick, got, s.MySlot)
		}
	}
}

// Every pick in the draft must belong to exactly one slot, and each slot must
// get exactly `rounds` picks. This catches off-by-ones the spot checks miss.
func TestSnakeCoversEveryPickExactlyOnce(t *testing.T) {
	for _, teams := range []int{8, 10, 12, 14} {
		s := newTestState(teams, 15, 1)
		counts := map[int]int{}
		for p := 1; p <= teams*15; p++ {
			slot := s.SlotAt(p)
			if slot < 1 || slot > teams {
				t.Fatalf("teams=%d pick=%d produced out-of-range slot %d", teams, p, slot)
			}
			counts[slot]++
		}
		for slot := 1; slot <= teams; slot++ {
			if counts[slot] != 15 {
				t.Errorf("teams=%d slot=%d got %d picks, want 15", teams, slot, counts[slot])
			}
		}
	}
}

// MyPick must agree with SlotAt for every slot and every round.
func TestMyPickAgreesWithSlotAt(t *testing.T) {
	for _, teams := range []int{8, 10, 12, 14} {
		for slot := 1; slot <= teams; slot++ {
			s := newTestState(teams, 15, slot)
			for r := 1; r <= 15; r++ {
				p := s.MyPick(r)
				if s.Round(p) != r {
					t.Errorf("teams=%d slot=%d: MyPick(%d)=%d lands in round %d", teams, slot, r, p, s.Round(p))
				}
				if got := s.SlotAt(p); got != slot {
					t.Errorf("teams=%d slot=%d: MyPick(%d)=%d belongs to slot %d", teams, slot, r, p, got)
				}
			}
		}
	}
}

func TestNextPickAndPicksUntilMine(t *testing.T) {
	s := newTestState(12, 15, 3)

	s.PickNo = 1
	if got := s.NextPick(); got != 3 {
		t.Errorf("NextPick at pick 1 = %d, want 3", got)
	}
	if got := s.PicksUntilMine(); got != 2 {
		t.Errorf("PicksUntilMine at pick 1 = %d, want 2", got)
	}

	// Standing on my own pick: NextPick is now, and nothing intervenes.
	s.PickNo = 3
	if got := s.NextPick(); got != 3 {
		t.Errorf("NextPick on my pick = %d, want 3", got)
	}
	if got := s.PicksUntilMine(); got != 0 {
		t.Errorf("PicksUntilMine on my pick = %d, want 0", got)
	}

	// Just past it, the next one is the round-2 wrap at 22.
	s.PickNo = 4
	if got := s.NextPick(); got != 22 {
		t.Errorf("NextPick after my pick = %d, want 22", got)
	}
	if got := s.FollowingPick(); got != 27 {
		t.Errorf("FollowingPick = %d, want 27", got)
	}
}

func TestDraftAndUndoRoundTrip(t *testing.T) {
	s := New(map[string]Player{
		"a": {ID: "a", Pos: "RB", Value: 100},
		"b": {ID: "b", Pos: "WR", Value: 90},
	}, 12, 15, 1)

	s.Draft("a")
	if !s.Taken["a"] || s.PickNo != 2 || len(s.Picks) != 1 {
		t.Fatalf("draft did not register: taken=%v pickNo=%d picks=%d", s.Taken["a"], s.PickNo, len(s.Picks))
	}
	if got := s.Rosters[1]; len(got) != 1 || got[0] != "a" {
		t.Errorf("roster = %v, want [a]", got)
	}

	s.Undo()
	if s.Taken["a"] || s.PickNo != 1 || len(s.Picks) != 0 || len(s.Rosters[1]) != 0 {
		t.Errorf("undo did not restore state: taken=%v pickNo=%d", s.Taken["a"], s.PickNo)
	}

	// Drafting someone already taken must not advance the draft.
	s.Draft("a")
	before := s.PickNo
	s.Draft("a")
	if s.PickNo != before {
		t.Errorf("double-draft advanced the pick from %d to %d", before, s.PickNo)
	}
}

func TestFilledSlotsPrefersDedicatedOverFlex(t *testing.T) {
	s := New(map[string]Player{
		"rb1": {ID: "rb1", Pos: "RB", Value: 100},
		"rb2": {ID: "rb2", Pos: "RB", Value: 90},
		"rb3": {ID: "rb3", Pos: "RB", Value: 80},
		"qb1": {ID: "qb1", Pos: "QB", Value: 70},
	}, 12, 15, 1)
	s.Rosters[1] = []string{"rb1", "rb2", "rb3", "qb1"}

	filled, bench := s.FilledSlots(1)
	// Two RBs fill the dedicated RB slots, the third takes FLEX.
	if filled[1] == "" || filled[2] == "" {
		t.Errorf("dedicated rb slots not filled: %v", filled)
	}
	flexIdx := -1
	for i, want := range s.Roster.Slots {
		if want == "FLEX" {
			flexIdx = i
		}
	}
	if filled[flexIdx] != "rb3" {
		t.Errorf("flex = %q, want rb3", filled[flexIdx])
	}
	if filled[0] != "qb1" {
		t.Errorf("qb slot = %q, want qb1", filled[0])
	}
	if len(bench) != 0 {
		t.Errorf("bench = %v, want empty", bench)
	}
}

func TestNeed(t *testing.T) {
	s := New(map[string]Player{
		"rb1": {ID: "rb1", Pos: "RB"},
		"rb2": {ID: "rb2", Pos: "RB"},
	}, 12, 15, 1)

	if got := s.Need("RB"); got != NeedStarter {
		t.Errorf("empty roster: need(rb) = %v, want %v", got, NeedStarter)
	}
	// K and DEF stay suppressed until the last rounds, whatever the roster says.
	if got := s.Need("K"); got != 0 {
		t.Errorf("early need(k) = %v, want 0", got)
	}

	// Both RB slots filled -> RB drops to flex weight.
	s.Rosters[1] = []string{"rb1", "rb2"}
	if got := s.Need("RB"); got != NeedFlex {
		t.Errorf("rb slots filled: need(rb) = %v, want %v", got, NeedFlex)
	}

	// Late in the draft, kickers become real.
	s.PickNo = 13*12 + 1
	if got := s.Need("K"); got != NeedStarter {
		t.Errorf("late need(k) = %v, want %v", got, NeedStarter)
	}
}

func TestUnfilledStarters(t *testing.T) {
	s := New(map[string]Player{
		"qb": {ID: "qb", Pos: "QB"},
		"rb": {ID: "rb", Pos: "RB"},
	}, 12, 15, 1)

	if got := len(s.UnfilledStarters(1)); got != len(s.Roster.Slots) {
		t.Errorf("empty roster: %d unfilled, want %d", got, len(s.Roster.Slots))
	}

	s.Rosters[1] = []string{"qb", "rb"}
	got := s.UnfilledStarters(1)
	for _, slot := range got {
		if slot == "QB" {
			t.Error("qb slot is filled but still reported unfilled")
		}
	}
	// One RB is filled; the second RB slot and FLEX both remain.
	rbs := 0
	for _, slot := range got {
		if slot == "RB" {
			rbs++
		}
	}
	if rbs != 1 {
		t.Errorf("expected 1 rb slot still open, got %d", rbs)
	}
}

func TestByeLoadCountsStartersOnly(t *testing.T) {
	// Four RBs all on bye 11: two start, one takes flex, one rides the bench.
	// The bench player must not count — a bench bye is not a problem.
	s := New(map[string]Player{
		"r1": {ID: "r1", Pos: "RB", Bye: 11, Value: 100},
		"r2": {ID: "r2", Pos: "RB", Bye: 11, Value: 90},
		"r3": {ID: "r3", Pos: "RB", Bye: 11, Value: 80},
		"r4": {ID: "r4", Pos: "RB", Bye: 11, Value: 70},
	}, 12, 15, 1)
	s.Rosters[1] = []string{"r1", "r2", "r3", "r4"}

	if got := len(s.ByeLoad(1)[11]); got != 3 {
		t.Errorf("week 11 load = %d, want 3 (two rb slots + flex, bench excluded)", got)
	}
}

func TestByeConflictsThreshold(t *testing.T) {
	mk := func(bye int) map[string]Player {
		return map[string]Player{
			"qb": {ID: "qb", Pos: "QB", Bye: bye},
			"r1": {ID: "r1", Pos: "RB", Bye: bye},
			"w1": {ID: "w1", Pos: "WR", Bye: 5},
		}
	}
	// Two starters sharing a bye is unremarkable and must stay quiet.
	s := New(mk(11), 12, 15, 1)
	s.Rosters[1] = []string{"qb", "r1", "w1"}
	if c := s.ByeConflicts(1); len(c) != 0 {
		t.Errorf("2 starters on a bye should not warn, got %+v", c)
	}

	// A third tips it over.
	s.Players["w1"] = Player{ID: "w1", Pos: "WR", Bye: 11}
	c := s.ByeConflicts(1)
	if len(c) != 1 || c[0].Week != 11 || len(c[0].Players) != 3 {
		t.Fatalf("3 starters on a bye should warn, got %+v", c)
	}
}

func TestMyUpcomingPicks(t *testing.T) {
	s := newTestState(12, 15, 3)
	got := s.MyUpcomingPicks(3)
	want := []int{3, 22, 27}
	if len(got) != 3 {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("upcoming[%d] = %d, want %d", i, got[i], want[i])
		}
	}

	// Past my round-1 pick, it must not be offered again.
	s.PickNo = 10
	if got := s.MyUpcomingPicks(2); got[0] != 22 {
		t.Errorf("after pick 10, next = %d, want 22", got[0])
	}

	// Near the end there may be fewer than n left; that's not an error.
	s.PickNo = 12*15 - 1
	if got := s.MyUpcomingPicks(3); len(got) > 1 {
		t.Errorf("at the end of the draft got %v, want at most 1", got)
	}
}
