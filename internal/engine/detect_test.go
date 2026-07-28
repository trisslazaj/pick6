package engine

import (
	"fmt"
	"testing"
)

// board builds n players at a position, all in the given tier, descending value.
func board(pos string, tier, n, baseValue int) map[string]Player {
	out := map[string]Player{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", pos, i)
		out[id] = Player{ID: id, Name: id, Pos: pos, Tier: tier, Value: baseValue - i}
	}
	return out
}

func TestCliffLevels(t *testing.T) {
	players := board("RB", 1, 4, 100)
	s := New(players, 12, 15, 1)

	if level, tier, n := s.Cliff("RB"); level != CliffNone || tier != 1 || n != 4 {
		t.Errorf("4 left: got level=%v tier=%d n=%d, want none/1/4", level, tier, n)
	}
	s.Draft("RB0")
	s.Draft("RB1")
	if level, _, n := s.Cliff("RB"); level != CliffWarning || n != 2 {
		t.Errorf("2 left: got level=%v n=%d, want warning/2", level, n)
	}
	s.Draft("RB2")
	if level, _, n := s.Cliff("RB"); level != CliffLast || n != 1 {
		t.Errorf("1 left: got level=%v n=%d, want last/1", level, n)
	}
	s.Draft("RB3")
	if level, tier, _ := s.Cliff("RB"); level != CliffNone || tier != 0 {
		t.Errorf("position empty: got level=%v tier=%d, want none/0", level, tier)
	}
}

// A tier emptying must roll bestNow into the next tier on its own — the spec is
// explicit that no special-case logic handles this.
func TestCliffRollsIntoNextTierWhenOneEmpties(t *testing.T) {
	players := map[string]Player{
		"a": {ID: "a", Pos: "RB", Tier: 1, Value: 100},
		"b": {ID: "b", Pos: "RB", Tier: 2, Value: 50},
		"c": {ID: "c", Pos: "RB", Tier: 2, Value: 49},
		"d": {ID: "d", Pos: "RB", Tier: 2, Value: 48},
	}
	s := New(players, 12, 15, 1)

	// Tier 1 holds one player and nobody has been drafted, so it is not a cliff —
	// it is simply a tier of one.
	if level, tier, n := s.Cliff("RB"); level != CliffNone || tier != 1 || n != 1 {
		t.Fatalf("untouched one-player tier: got level=%v tier=%d n=%d, want none/1/1", level, tier, n)
	}
	s.Draft("a")
	if level, tier, n := s.Cliff("RB"); level != CliffNone || tier != 2 || n != 3 {
		t.Errorf("after tier 1 empties: got level=%v tier=%d n=%d, want none/2/3", level, tier, n)
	}
}

// Untiered players (no value from any source, i.e. K and DEF today) must never
// register a cliff, or the board would cry wolf on kickers.
func TestUntieredNeverCliffs(t *testing.T) {
	s := New(map[string]Player{
		"k1": {ID: "k1", Pos: "K", Tier: 0, Value: 0},
	}, 12, 15, 1)
	if level, tier, _ := s.Cliff("K"); level != CliffNone || tier != 0 {
		t.Errorf("untiered: got level=%v tier=%d, want none/0", level, tier)
	}
	if n := s.TierRemaining("K", 0); n != 0 {
		t.Errorf("TierRemaining for tier 0 = %d, want 0", n)
	}
}

func TestDetectRun(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 10, 100) {
		players[k] = v
	}
	for k, v := range board("WR", 1, 10, 90) {
		players[k] = v
	}
	s := New(players, 12, 15, 1)

	// Three RBs inside the window is below RunThreshold (4).
	s.Draft("RB0")
	s.Draft("WR0")
	s.Draft("RB1")
	s.Draft("RB2")
	if _, ok := s.DetectRun(); ok {
		t.Error("3 of 6 should not trigger a run")
	}

	s.Draft("RB3")
	run, ok := s.DetectRun()
	if !ok || run.Pos != "RB" || run.Count != 4 {
		t.Fatalf("4 of 6 should trigger an rb run, got %+v ok=%v", run, ok)
	}

	// Push RB picks out of the 6-pick window; the banner must clear.
	for i := 1; i <= 6; i++ {
		s.Draft(fmt.Sprintf("WR%d", i))
	}
	if run, ok := s.DetectRun(); ok && run.Pos == "RB" {
		t.Error("rb run should have expired out of the window")
	}
}

// When a run empties the position's tier, the banner has to say so — that's the
// difference between "act now" and "you already missed it".
func TestRunReportsBrokenTier(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 4, 100) {
		players[k] = v
	}
	s := New(players, 12, 15, 1)
	for i := 0; i < 4; i++ {
		s.Draft(fmt.Sprintf("RB%d", i))
	}
	run, ok := s.DetectRun()
	if !ok || run.Pos != "RB" {
		t.Fatalf("expected an rb run, got %+v ok=%v", run, ok)
	}
	if !run.TierBroke {
		t.Error("tier is empty; run should report TierBroke")
	}
}

func TestDetectRunIsDeterministicOnTies(t *testing.T) {
	players := map[string]Player{}
	for k, v := range board("RB", 1, 10, 100) {
		players[k] = v
	}
	for k, v := range board("WR", 1, 10, 90) {
		players[k] = v
	}
	// Exactly 3-3 in the window: neither reaches RunThreshold, so no run at all.
	s := New(players, 12, 15, 1)
	for i := 0; i < 3; i++ {
		s.Draft(fmt.Sprintf("RB%d", i))
		s.Draft(fmt.Sprintf("WR%d", i))
	}
	if _, ok := s.DetectRun(); ok {
		t.Error("a 3-3 split should not be a run")
	}
}

// A cliff means a tier is emptying, not that it is small. Without this, a tier
// the rankings file drew with one player in it — Josh Allen alone at QB1 — puts
// "last one, take him or lose the tier" on screen at pick 1.01 of an empty draft.
func TestSmallButUntouchedTierIsNotACliff(t *testing.T) {
	// A genuine singleton tier.
	s := New(map[string]Player{
		"solo": {ID: "solo", Pos: "QB", Tier: 1, Value: 100},
		"next": {ID: "next", Pos: "QB", Tier: 2, Value: 50},
	}, 12, 15, 1)
	if level, _, _ := s.Cliff("QB"); level != CliffNone {
		t.Errorf("untouched singleton tier reported %v, want none", level)
	}

	// A two-player tier with nobody taken is not "ending" either.
	s2 := New(board("RB", 1, 2, 100), 12, 15, 1)
	if level, _, n := s2.Cliff("RB"); level != CliffNone || n != 2 {
		t.Errorf("untouched two-player tier reported %v (n=%d), want none", level, n)
	}

	// Draft one and it becomes a real cliff.
	s2.Draft("RB0")
	if level, _, n := s2.Cliff("RB"); level != CliffLast || n != 1 {
		t.Errorf("after depletion got %v (n=%d), want last/1", level, n)
	}
}
