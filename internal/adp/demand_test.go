package adp

import "testing"

// draftOf builds a RoomDraft from a shorthand run of positions, so a fixture
// reads as the shape it is testing rather than as a wall of strings.
func draftOf(runs map[string]int) RoomDraft {
	var d RoomDraft
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		for i := 0; i < runs[pos]; i++ {
			d = append(d, pos)
		}
	}
	return d
}

// D_P is the MEDIAN count per draft, not the mean, and the difference is not
// cosmetic: one of this user's three drafts is 16 rounds and two are 15, so a
// mean would fold the long draft's extra twelve picks into every position. Here
// the odd draft takes 30 backs against 10 and 12, and the median ignores it.
//
// The zero row is the other half. A position one draft never touched has to
// count as zero for that draft — skipping it would compute the median over only
// the rooms that wanted the position, which is the opposite of what a demand
// figure means.
func TestPositionDemandIsAMedianOverDrafts(t *testing.T) {
	drafts := map[string]RoomDraft{
		"a": draftOf(map[string]int{"RB": 10, "WR": 20, "TE": 3}),
		"b": draftOf(map[string]int{"RB": 12, "WR": 18, "TE": 4, "K": 1}),
		"c": draftOf(map[string]int{"RB": 30, "WR": 19, "TE": 5, "K": 2}),
	}
	cases := []struct {
		pos  string
		want int
	}{
		{"RB", 12}, // 10, 12, 30
		{"WR", 19}, // 18, 19, 20
		{"TE", 4},  // 3, 4, 5
		{"K", 1},   // 0, 1, 2 — the draft that took none still votes
		{"QB", 0},  // nobody took one, so the position isn't in the table at all
	}
	got := PositionDemand(drafts)
	for _, c := range cases {
		if got[c.pos] != c.want {
			t.Errorf("demand(%s) = %d, want %d", c.pos, got[c.pos], c.want)
		}
	}

	// Even counts round half up rather than truncating, so two drafts taking 11
	// and 12 backs read 12 and not 11. Rounding a demand DOWN would put the
	// replacement level on a better player than the room really settles for.
	even := PositionDemand(map[string]RoomDraft{
		"a": draftOf(map[string]int{"RB": 11}),
		"b": draftOf(map[string]int{"RB": 12}),
	})
	if even["RB"] != 12 {
		t.Errorf("even-count demand(rb) = %d, want 12", even["RB"])
	}

	// No drafts at all means no table, and the engine falls back to the league's
	// lineup shape. An empty map would look like a measured zero.
	if got := PositionDemand(nil); got != nil {
		t.Errorf("demand with no drafts = %v, want nil", got)
	}
}

// A pick whose metadata carried no position is a hole in the draft, not a
// player. RoomDraftOf leaves "" at that index deliberately (it indexes by pick
// number so a missing pick can't shift every later one), and counting those as
// a position would inflate whichever one they landed next to.
func TestPositionDemandSkipsUnknownPositions(t *testing.T) {
	got := PositionDemand(map[string]RoomDraft{
		"a": {"RB", "", "RB", "", "WR"},
	})
	if got["RB"] != 2 || got["WR"] != 1 {
		t.Errorf("demand = %v, want rb 2 and wr 1", got)
	}
	if _, ok := got[""]; ok {
		t.Error("the empty position made it into the demand table")
	}
}
