package engine

import (
	"fmt"
	"testing"
)

// A realistic board: 200 ranked players, 12 teams, vantage mid-round-5 with a
// full window to FollowingPick (~23 picks). This is the per-pick recompute the
// TUI pays.
func BenchmarkRunSim(b *testing.B) {
	s := newTestState(12, 16, 6)
	pos := []string{"QB", "RB", "WR", "TE", "RB", "WR"}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("p%d", i)
		s.Players[id] = Player{ID: id, Pos: pos[i%len(pos)], ADP: float64(i + 1), Sigma: 6, Value: 5000 - 20*i}
	}
	// 50 picks made, round-robin-ish rosters
	n := 0
	for id := range s.Players {
		if n >= 50 {
			break
		}
		s.Taken[id] = true
		slot := n%12 + 1
		s.Rosters[slot] = append(s.Rosters[slot], id)
		n++
	}
	s.PickNo = 51
	s.Survival = SurvivalSim
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.sim = nil
		_ = s.simFor()
	}
}
