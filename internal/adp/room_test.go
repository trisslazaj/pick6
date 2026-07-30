package adp

import (
	"math"
	"testing"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

// TestBuildRoomCurve pins the running max, which is the one part of the average
// that is not obvious. A deep k is supported by fewer drafts than a shallow one,
// so the MEANS can invert even though no single draft ever takes its second
// running back before its first: here rb1 averages 10.5 over two drafts while rb2
// is one draft's pick 3. Unmonotonized the curve would tell the board the second
// back at the position goes seven picks before the first.
func TestBuildRoomCurve(t *testing.T) {
	// Two obviously fake drafts. "" is a pick that carried no position.
	shallow := RoomDraft{"RB", "", "RB", "QB"} // rb at 1 and 3, qb at 4
	deep := make(RoomDraft, 20)
	deep[19] = "RB" // one rb, at pick 20

	c := BuildRoomCurve([]RoomDraft{shallow, deep})

	cases := []struct {
		name   string
		pos    string
		k      int
		want   float64
		drafts int
		ok     bool
	}{
		{"rb1 averages both drafts", "RB", 1, 10.5, 2, true},       // (1 + 20) / 2
		{"rb2 is monotonized up", "RB", 2, 10.5, 1, true},          // raw 3.0, lifted to rb1
		{"rb3 was never reached", "RB", 3, 0, 0, false},            //
		{"qb1 is one draft only", "QB", 1, 4, 1, true},             //
		{"a position nobody took", "WR", 1, 0, 0, false},           //
		{"k is 1-indexed, not 0", "RB", 0, 0, 0, false},            //
		{"a pick with no position is skipped", "", 1, 0, 0, false}, //
	}
	for _, c2 := range cases {
		got, drafts, ok := c.At(c2.pos, c2.k)
		if ok != c2.ok || drafts != c2.drafts || math.Abs(got-c2.want) > 1e-9 {
			t.Errorf("%s: at(%q, %d) = %.4f/%d/%v, want %.4f/%d/%v",
				c2.name, c2.pos, c2.k, got, drafts, ok, c2.want, c2.drafts, c2.ok)
		}
	}
	if c.Drafts != 2 {
		t.Errorf("curve carries %d drafts, want 2", c.Drafts)
	}
	if got := c.Depth("RB"); got != 2 {
		t.Errorf("rb depth = %d, want 2", got)
	}
	if !BuildRoomCurve(nil).Empty() {
		t.Errorf("a curve built from no drafts is not empty")
	}
}

// TestWarp checks the blend weight is per-k, not per-curve. A k only one draft
// ever reached is thinner evidence than one all three reached, and pricing them
// the same would let a single deep pick move a player as hard as a consensus.
// It is also what makes "w -> 0 outside the observed range" true without a
// special case: no drafts, no weight.
func TestWarp(t *testing.T) {
	shallow := RoomDraft{"RB", "", "RB"}
	deep := make(RoomDraft, 20)
	deep[19] = "RB"
	c := BuildRoomCurve([]RoomDraft{shallow, deep}) // rb1 10.5 (n2), rb2 10.5 (n1)

	cases := []struct {
		name string
		k    int
		adp  float64
		want float64
		ok   bool
	}{
		// n = 2: w = 2/(2+2) = 0.5 -> 0.5*10.5 + 0.5*20
		{"two drafts of support", 1, 20, 15.25, true},
		// n = 1: w = 1/(1+2) = 1/3 -> 10.5/3 + 30*2/3
		{"one draft of support", 2, 30, 23.5, true},
		// Past the deepest k the room ever reached the curve has no opinion.
		{"outside the observed range", 3, 40, 40, false},
		// No market price to blend with. Warping him would be inventing an adp for
		// a player no source ranked.
		{"no adp", 1, 0, 0, false},
	}
	for _, c2 := range cases {
		got, ok := c.Warp("RB", c2.k, c2.adp)
		if ok != c2.ok || math.Abs(got-c2.want) > 1e-9 {
			t.Errorf("%s: warp(rb, %d, %.1f) = %.4f/%v, want %.4f/%v",
				c2.name, c2.k, c2.adp, got, ok, c2.want, c2.ok)
		}
	}
}

// TestEffectiveADP pins the ranking, which is where a subtle wrong answer would
// hide: k is the player's rank by adp WITHIN his position, over the rows handed
// in — not his overall rank, not the order the caller happened to build the slice
// in, and not a rank that counts players with no adp. Any of those would compare
// the room's fifth receiver against somebody else entirely and warp him by the
// wrong number, silently.
func TestEffectiveADP(t *testing.T) {
	shallow := RoomDraft{"RB", "", "RB"}
	deep := make(RoomDraft, 20)
	deep[19] = "RB"
	c := BuildRoomCurve([]RoomDraft{shallow, deep}) // rb1 10.5 (n2), rb2 10.5 (n1)

	rows := []RoomRow{
		{ID: "second", Pos: "RB", ADP: 30}, // deliberately first in the slice
		{ID: "first", Pos: "RB", ADP: 20},
		{ID: "third", Pos: "RB", ADP: 35}, // rb3: past the curve, untouched
		{ID: "noadp", Pos: "RB", ADP: 0},  // must not consume a rank
		{ID: "wr", Pos: "WR", ADP: 5},     // a position the room never took
		{ID: "nopos", Pos: "", ADP: 12},   //
	}
	got := c.EffectiveADP(rows)
	want := map[string]float64{"first": 15.25, "second": 23.5}
	if len(got) != len(want) {
		t.Fatalf("warped %d rows (%v), want %d", len(got), got, len(want))
	}
	for id, w := range want {
		if math.Abs(got[id]-w) > 1e-9 {
			t.Errorf("%s warped to %.4f, want %.4f", id, got[id], w)
		}
	}

	// The graded-only variant: same warp, first k players at a position only.
	top := c.EffectiveADPTopK(rows, 1)
	if len(top) != 1 || math.Abs(top["first"]-15.25) > 1e-9 {
		t.Errorf("top-1 warp = %v, want only first at 15.25", top)
	}
}

// The warped board must stay non-decreasing in k, and the blend does not give
// that for free. adp_room is monotonized and adp is sorted, but the weight
// between them is the drafts that reached each k, so a thinly supported deep
// player stays nearer his market price than the well-supported one ahead of him
// and overtakes him.
//
// Two obviously fake drafts make it happen at k2: rb1 is pick 10.5 over both
// drafts (w = 0.5), rb2 is pick 10.5 over one (w = 1/3). The room is LATER than
// the market on both, which is the regime every real inversion was in — a
// ranked list runs deeper than any finite draft. Against adps of 6 and 6.4 the
// well-supported first back is dragged most of the way to the room's 10.5, for
// 8.25, while the thin second back only reaches 7.77 and overtakes him. That
// inverted price curve tells the board the second running back comes off the
// position before the first, and is exactly what the 12th and 13th defenses did
// on the real 2026 board.
func TestEffectiveADPStaysMonotone(t *testing.T) {
	shallow := RoomDraft{"RB", "", "RB"}
	deep := make(RoomDraft, 20)
	deep[19] = "RB"
	c := BuildRoomCurve([]RoomDraft{shallow, deep})

	rows := []RoomRow{
		{ID: "first", Pos: "RB", ADP: 6},
		{ID: "second", Pos: "RB", ADP: 6.4},
	}
	raw, _ := c.Warp("RB", 2, 6.4) // what the blend alone would have said
	got := c.EffectiveADP(rows)
	if raw >= got["first"] {
		t.Fatalf("fixture is not an inversion: the raw blend prices rb2 at %.4f against rb1's %.4f",
			raw, got["first"])
	}
	if math.Abs(got["first"]-8.25) > 1e-9 {
		t.Errorf("rb1 warped to %.4f, want 8.25", got["first"])
	}
	if got["second"] < got["first"] {
		t.Errorf("rb2 warped to %.4f, ahead of rb1's %.4f — the price curve inverted",
			got["second"], got["first"])
	}
	if math.Abs(got["second"]-got["first"]) > 1e-9 {
		t.Errorf("rb2 warped to %.4f, want the running max to hold him at rb1's %.4f",
			got["second"], got["first"])
	}
}

// TestRoomDraftOf pins the indexing. The curve is made of pick NUMBERS, so a
// missing pick has to leave a hole: packing the slice instead would shift every
// later pick one earlier and move the whole room's price by a pick per gap, with
// nothing on screen looking wrong. It also normalizes PK to K, or kickers would
// build a curve nobody ever reads.
func TestRoomDraftOf(t *testing.T) {
	// Built field by field: Metadata is an anonymous struct on sleeper.DraftPick,
	// so a composite literal would have to restate the whole type and its tags.
	// Out of pick order on purpose — the pick number is the index, not the loop.
	kicker := sleeper.DraftPick{PickNo: 3}
	kicker.Metadata.Position = "PK"
	back := sleeper.DraftPick{PickNo: 1}
	back.Metadata.Position = "rb"

	got := RoomDraftOf([]sleeper.DraftPick{kicker, back})
	want := RoomDraft{"RB", "", "K"}
	if len(got) != len(want) {
		t.Fatalf("got %d picks (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pick %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

// TestRoomCurveOf pins the exclusion the whole gate rests on. A warp built from
// the draft it is scored against has memorized the answer; leave-one-out is the
// only thing standing between "the room's prices generalize" and "the room's
// prices describe the night they came from".
func TestRoomCurveOf(t *testing.T) {
	drafts := map[string]RoomDraft{
		"a": {"RB"},             // rb1 at pick 1
		"b": {"", "", "", "RB"}, // rb1 at pick 4
	}
	all := RoomCurveOf(drafts)
	if p, n, _ := all.At("RB", 1); p != 2.5 || n != 2 {
		t.Errorf("both drafts: rb1 = %.2f over %d drafts, want 2.50 over 2", p, n)
	}
	held := RoomCurveOf(drafts, "b")
	if p, n, _ := held.At("RB", 1); p != 1 || n != 1 {
		t.Errorf("b held out: rb1 = %.2f over %d drafts, want 1.00 over 1", p, n)
	}
	if got := RoomCurveOf(drafts, "a", "b"); !got.Empty() {
		t.Errorf("holding out every draft still produced a curve")
	}
}
