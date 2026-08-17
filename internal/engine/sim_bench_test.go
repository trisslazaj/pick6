package engine

import (
	"fmt"
	"sort"
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

// The same board, through the conditioned lookahead: every candidate, every
// future, from a cold cache — i.e. exactly what a pick event costs the TUI.
//
// The budget moved with milestone 8 and the reason is worth stating rather than
// silently blowing past: the old ~50ms was a budget for a TWO-PICK window, and
// the window is now the whole rest of the draft. What has not changed is where
// the number is spent — the cache is filled when the PREVIOUS pick lands, off a
// 2-3s poll, so this is a background recompute and not a render. ~250ms is the
// budget it is held to, and it shrinks every round because the window does.
func BenchmarkPickChoicesConditioned(b *testing.B) {
	benchPlan(b, 51, ScorerPair)
}

// Round 1 is the worst case the whole draft ever presents: sixteen legs, a
// hundred and ninety opponent picks per future, and every position still a
// candidate. Round 8 and round 15 are the same board later, which is the shape
// of the cost curve — the horizon is what costs, so it is monotone downward and
// the frame that matters most for latency is the one nobody is in a hurry for.
func BenchmarkPickChoicesRound1(b *testing.B)  { benchPlan(b, 6, ScorerPair) }
func BenchmarkPickChoicesRound8(b *testing.B)  { benchPlan(b, 90, ScorerPair) }
func BenchmarkPickChoicesRound15(b *testing.B) { benchPlan(b, 174, ScorerPair) }

// ...and the same three under the milestone-8 objective, which is what the
// ~250ms budget is actually about: its window is every pick I have left, so
// round one is the worst case the whole draft ever presents and the curve is
// monotone downward from there. It is off by default (see State.Scorer), and a
// promotion has to re-read these before it lands.
func BenchmarkRosterScoreRound1(b *testing.B)  { benchPlan(b, 6, ScorerRoster) }
func BenchmarkRosterScoreRound8(b *testing.B)  { benchPlan(b, 90, ScorerRoster) }
func BenchmarkRosterScoreRound15(b *testing.B) { benchPlan(b, 174, ScorerRoster) }

func benchPlan(b *testing.B, pickNo int, scorer string) {
	s := newTestState(12, 16, 6)
	pos := []string{"QB", "RB", "WR", "TE", "RB", "WR"}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("p%d", i)
		s.Players[id] = Player{ID: id, Pos: pos[i%len(pos)], ADP: float64(i + 1), Sigma: 6, Value: 5000 - 20*i, Tier: i/8 + 1}
	}
	for i := 0; i < 4; i++ { // a kicker and a defense apiece, so the endgame is real
		for _, p := range []string{"K", "DEF"} {
			id := fmt.Sprintf("%s%d", p, i)
			s.Players[id] = Player{ID: id, Pos: p, ADP: float64(150 + i), Sigma: 8, Value: 200 - 10*i}
		}
	}
	ids := make([]string, 0, len(s.Players))
	for id := range s.Players {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i := 0; i < pickNo-1; i++ {
		s.Taken[ids[i]] = true
		slot := i%12 + 1
		s.Rosters[slot] = append(s.Rosters[slot], ids[i])
	}
	s.PickNo = pickNo
	s.Survival, s.Scorer = SurvivalSim, scorer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.invalidate()
		_ = s.PickChoices()
	}
}
