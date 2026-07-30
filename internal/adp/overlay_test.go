package adp

import (
	"math"
	"testing"

	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// overlayIndex builds a tiny sleeper pool with obviously fake names.
func overlayIndex() *rankings.Index {
	return rankings.NewIndex(map[string]sleeper.Player{
		"1": {PlayerID: "1", FullName: "fake alpha", Position: "RB", Team: "AAA", Active: true},
		"2": {PlayerID: "2", FullName: "fake bravo", Position: "RB", Team: "BBB", Active: true},
		"3": {PlayerID: "3", FullName: "fake charlie", Position: "RB", Team: "CCC", Active: true},
	})
}

// The overlay must take the ORDER from the second feed and the UNITS from the
// first. Overlaying the raw numbers was the bug: FFC's adp is a mean pick
// bounded by draft length while a FantasyPros column is an unbounded rank, so
// the raw values disagree by a scale factor that has nothing to do with any
// player. Here the overlay ranks charlie cheapest and alpha dearest — the exact
// reverse of the primary — so every primary price must be re-dealt in that new
// order while the SET of prices stays identical.
func TestOverlayTakesOrderAndKeepsUnits(t *testing.T) {
	players := map[string]*Player{
		"1": {SleeperID: "1", Name: "fake alpha", Pos: "RB", Team: "AAA", ADP: 10, Stdev: 4},
		"2": {SleeperID: "2", Name: "fake bravo", Pos: "RB", Team: "BBB", ADP: 50, Stdev: 6},
		"3": {SleeperID: "3", Name: "fake charlie", Pos: "RB", Team: "CCC", ADP: 90, Stdev: 8},
	}
	// A rank-scaled feed: values nowhere near the primary's, order reversed.
	board := FFCResult{Entries: []Entry{
		{Name: "fake alpha", Pos: "RB", Team: "AAA", ADP: 300},
		{Name: "fake bravo", Pos: "RB", Team: "BBB", ADP: 200},
		{Name: "fake charlie", Pos: "RB", Team: "CCC", ADP: 100},
	}}

	r := OverlayADP(players, board, overlayIndex())
	if r.Repriced != 3 || r.Kept != 0 {
		t.Fatalf("repriced %d kept %d, want 3 and 0", r.Repriced, r.Kept)
	}
	cases := []struct {
		id   string
		want float64
	}{
		{"3", 10}, // cheapest in the overlay takes the cheapest primary price
		{"2", 50},
		{"1", 90}, // dearest in the overlay takes the dearest
	}
	for _, c := range cases {
		if got := players[c.id].ADP; math.Abs(got-c.want) > 1e-9 {
			t.Errorf("player %s adp = %v, want %v", c.id, got, c.want)
		}
	}
	// No value from the overlay's own scale may survive anywhere.
	for id, p := range players {
		if p.ADP > 100 {
			t.Errorf("player %s kept the overlay's raw scale (%v)", id, p.ADP)
		}
	}
}

// A player the overlay cannot reach keeps his primary price. Dropping him would
// shrink the board, which costs more than one slightly stale price.
func TestOverlayKeepsUnreachablePlayers(t *testing.T) {
	players := map[string]*Player{
		"1": {SleeperID: "1", Name: "fake alpha", Pos: "RB", Team: "AAA", ADP: 10},
		"2": {SleeperID: "2", Name: "fake bravo", Pos: "RB", Team: "BBB", ADP: 50},
	}
	board := FFCResult{Entries: []Entry{
		{Name: "fake alpha", Pos: "RB", Team: "AAA", ADP: 300},
		{Name: "fake nobody", Pos: "RB", Team: "ZZZ", ADP: 400},
	}}

	r := OverlayADP(players, board, overlayIndex())
	if r.Repriced != 1 || r.Kept != 1 {
		t.Errorf("repriced %d kept %d, want 1 and 1", r.Repriced, r.Kept)
	}
	if got := players["2"].ADP; got != 50 {
		t.Errorf("unreached player adp = %v, want his primary 50", got)
	}
	// The one reached player is alone in the map, so he takes the only price in it.
	if got := players["1"].ADP; got != 10 {
		t.Errorf("reached player adp = %v, want 10", got)
	}
	if len(r.Unmatched) != 1 {
		t.Errorf("unmatched = %v, want the one export row that hits nobody", r.Unmatched)
	}
}
