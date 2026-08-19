package engine

import (
	"fmt"
	"testing"
)

// simRBRoomState is the flagship v2 scenario from docs/engine-v2.md: three
// opponent picks before mine, both intervening teams holding full rb rooms
// with their wr slots open, and the market insisting the next three picks are
// exactly the top three rbs. v1 can only read the market; v2 reads the rooms.
//
// Geometry: 12 teams, slot 3, PickNo 96. Pick 96 is round 8 (slot 1, at the
// turn), 97 round 9 (slot 1 again), 98 slot 2, 99 mine — so slot 1 picks twice
// inside one rollout, which is the roster-copy case the spec calls REQUIRED.
func simRBRoomState() *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 96
	// Slots 1 and 2 each hold three rbs: both dedicated rb slots and the flex,
	// so their remaining rb need is bench weight while wr reads starter.
	for i, slot := range []int{1, 1, 1, 2, 2, 2} {
		id := fmt.Sprintf("rbfill%d", i)
		s.Players[id] = Player{ID: id, Pos: "RB"}
		s.Taken[id] = true
		s.Rosters[slot] = append(s.Rosters[slot], id)
	}
	addPlayers(s,
		Player{ID: "rb1", Pos: "RB", ADP: 96, Sigma: 2, Value: 500, Tier: 1},
		Player{ID: "rb2", Pos: "RB", ADP: 97, Sigma: 2, Value: 490, Tier: 1},
		Player{ID: "rb3", Pos: "RB", ADP: 98, Sigma: 2, Value: 480, Tier: 1},
	)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("wr%d", i+1)
		addPlayers(s, Player{ID: id, Pos: "WR", ADP: float64(99 + i), Sigma: 2, Value: 470 - 10*i, Tier: 1})
	}
	return s
}

// The DoD scenario: teams with full rb rooms make my rb materially safer than
// the adp logistic says. This is the entire feature in one number.
func TestSimFullRBRoomsBeatTheLogistic(t *testing.T) {
	s := simRBRoomState()
	rb1 := s.Players["rb1"]

	s.Survival = SurvivalSim
	sim := s.PSurviveTilted(rb1)
	s.Survival = "" // back to v1
	adp := s.PSurviveTilted(rb1)

	if sim-adp < 0.2 {
		t.Errorf("sim = %.3f, adp = %.3f: full rb rooms should lift rb survival by >= 0.2", sim, adp)
	}
	if sim <= 0 || sim >= 1 {
		t.Errorf("sim survival %.3f outside (0, 1): smoothing missing", sim)
	}

	// The whole downstream stack consumes the sim number without special cases:
	// a smoke over the heaviest consumer, which also exercises the q2 horizon.
	s.Survival = SurvivalSim
	if choices := s.PickChoices(); len(choices) == 0 {
		t.Fatal("PickChoices empty under sim")
	}
	if _, ok := s.BestPlan(); !ok {
		t.Fatal("BestPlan not ok under sim")
	}
}

// Same state, same seed: bit-identical numbers, however many times the state
// is rebuilt. Different seed: different rollouts. Determinism is a rendering
// requirement — a keypress re-render must not jiggle the board.
func TestSimDeterminism(t *testing.T) {
	a, b := simRBRoomState(), simRBRoomState()
	a.Survival, b.Survival = SurvivalSim, SurvivalSim
	for id := range a.Players {
		if a.Taken[id] {
			continue
		}
		pa, pb := a.PSurviveTilted(a.Players[id]), b.PSurviveTilted(b.Players[id])
		if pa != pb {
			t.Errorf("player %s: %.6f vs %.6f from identical states", id, pa, pb)
		}
	}

	c := simRBRoomState()
	c.Survival = SurvivalSim
	c.SimSeed = 42
	differs := false
	for id := range a.Players {
		if a.Taken[id] {
			continue
		}
		if a.PSurviveTilted(a.Players[id]) != c.PSurviveTilted(c.Players[id]) {
			differs = true
			break
		}
	}
	if !differs {
		t.Error("changing SimSeed changed nothing: the seed is not reaching the rng")
	}
}

// Nobody can take a player from me across my own turn. Back-to-back picks have
// zero opponent picks between them, and the answer is exactly 1 — arithmetic,
// not a smoothed 0.999.
func TestSimAcrossMyOwnTurnIsExactlyOne(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 12 // on the clock at the turn: picks 12 and 13 are both mine
	s.Survival = SurvivalSim
	p := Player{ID: "x", Pos: "RB", ADP: 12, Sigma: 2, Value: 100}
	addPlayers(s, p)
	if got := s.PSurviveTilted(p); got != 1 {
		t.Errorf("across my own turn = %v, want exactly 1", got)
	}
}

// On my own pick every survival is 1 and urgency is exactly 0 — the chain the
// vor tie-break hangs off. The sim must not turn that arithmetic into sampling.
func TestSimOnMyOwnPickKeepsUrgencyZero(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick, round 1
	s.Survival = SurvivalSim
	addPlayers(s,
		Player{ID: "wr1", Pos: "WR", ADP: 3, Sigma: 2, Value: 500, Tier: 1},
		Player{ID: "wr2", Pos: "WR", ADP: 5, Sigma: 2, Value: 400, Tier: 1},
	)
	if got := s.Urgency("WR"); got != 0 {
		t.Errorf("urgency on my own pick = %v, want exactly 0", got)
	}
}

// A candidate pool that is entirely K/DEF while the opponents' gate still
// zeroes them must fall back to adp-only sampling — never skip the pick, never
// divide by zero.
//
// The vantage must sit where beta > 0 AND the gate is still on, or the guard
// never runs: in rounds 1-3 beta is 0, need is never consulted, and the first
// version of this test (PickNo 14, round 2) passed with the guard deleted —
// caught by the review pass, verified by planting a panic in the guard branch.
// PickNo 52 is round 5: beta 0.5, eleven rounds remaining against the gate's
// seven, eighteen opponent picks to my next at 70, and every one of them takes
// the guard path.
func TestSimZeroWeightGuard(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 52
	s.Survival = SurvivalSim
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("k%d", i+1)
		addPlayers(s, Player{ID: id, Pos: "K", ADP: float64(130 + i), Sigma: 6, Value: 50 - i})
	}
	addPlayers(s,
		Player{ID: "d1", Pos: "DEF", ADP: 140, Sigma: 6, Value: 40},
		Player{ID: "d2", Pos: "DEF", ADP: 141, Sigma: 6, Value: 39},
	)
	got := s.PSurviveTilted(s.Players["k1"])
	if got <= 0 || got >= 0.5 {
		t.Errorf("top k survival on an all-k/def board = %v, want (0, 0.5): the guard should sample by adp alone", got)
	}
}

// On my last pick there is no after: everything survives at exactly 1 and the
// sim never has to run against an empty window.
func TestSimLastPick(t *testing.T) {
	s := newTestState(12, 2, 3)
	s.PickNo = 22 // my last pick, on the clock
	s.Survival = SurvivalSim
	p := Player{ID: "x", Pos: "RB", ADP: 20, Sigma: 2, Value: 100}
	addPlayers(s, p)
	if got := s.PSurviveTilted(p); got != 1 {
		t.Errorf("on my last pick = %v, want exactly 1", got)
	}
	// And the table itself is buildable at this vantage without panicking.
	if tbl := s.simFor(); tbl == nil {
		t.Fatal("simFor returned nil")
	}
}

// With opponent picks in the window nobody reads exactly 0 or exactly 1 off a
// finite sample — including a registered-but-unranked player, who on a board
// deeper than the candidate pool is never even considered and still reads the
// smoothed ceiling rather than a hard 1.
func TestSimSmoothingBounds(t *testing.T) {
	s := simRBRoomState()
	s.Survival = SurvivalSim
	// Pad the board past CandidatePool so the unranked player sits genuinely
	// outside the window opponents sample from. (On the fixture's bare 11-man
	// board he was inside it and drew real picks — correct for a tiny board,
	// not the claim under test.)
	for i := 0; i < CandidatePool+10; i++ {
		id := fmt.Sprintf("pad%d", i)
		addPlayers(s, Player{ID: id, Pos: "WR", ADP: float64(107 + i), Sigma: 6, Value: 300 - i})
	}
	addPlayers(s, Player{ID: "deep", Pos: "WR", ADP: 0, Value: 1})
	for _, id := range []string{"rb1", "wr8", "deep"} {
		got := s.PSurviveTilted(s.Players[id])
		if got <= 0 || got >= 1 {
			t.Errorf("%s = %v, want strictly inside (0, 1)", id, got)
		}
	}
	if got := s.PSurviveTilted(s.Players["deep"]); got < 0.99 {
		t.Errorf("unranked deep player = %v, want >= 0.99: he is outside the candidate pool entirely", got)
	}
}

// SimBacktest simulates every pick in the window, INCLUDING the vantage
// seat's own — the labels count my removal too, so the sim must expect it.
// Pinned through the exact-removals property: over a pool deeper than the
// window, expected removals sum to the window's pick count. runSim at the same
// vantage skips my picks and expects one fewer.
func TestSimBacktestIncludesMyOwnPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.Survival = SurvivalSim
	for i := 0; i < 45; i++ {
		id := fmt.Sprintf("wr%02d", i)
		addPlayers(s, Player{ID: id, Pos: "WR", ADP: float64(95 + i), Sigma: 6, Value: 500 - i})
	}
	s.PickNo = 99 // my round-9 pick; my next is 118, so [99, 118) holds 19 picks, one mine

	removals := func(p func(string) float64) float64 {
		total := 0.0
		for id := range s.Players {
			total += 1 - p(id)
		}
		return total
	}

	back := removals(s.SimBacktest(99, 118))
	if back < 18.5 || back > 19.2 {
		t.Errorf("backtest expected removals = %.2f, want ~19: every window pick including mine", back)
	}

	live := s.simFor()
	total := 0.0
	for id := range s.Players {
		total += 1 - live.pAt(id, 118)
	}
	if total < 17.5 || total > 18.2 {
		t.Errorf("live expected removals = %.2f, want ~18: my own pick removes nobody", total)
	}
}

// The off-board escape: at rate r a pick removes nobody from the ranked pool,
// so expected removals scale by 1-r. Pinned at 0.5 (half the removals), 1.0
// (none — everyone survives at the smoothed ceiling), and by the byte-identity
// of a nil OffBoard with the pre-escape build (the zero-rate guard never draws
// from the rng).
func TestSimOffBoardEscape(t *testing.T) {
	build := func(rate []float64) *State {
		s := newTestState(12, 15, 3)
		s.Survival = SurvivalSim
		s.OffBoard = rate
		for i := 0; i < 45; i++ {
			id := fmt.Sprintf("wr%02d", i)
			addPlayers(s, Player{ID: id, Pos: "WR", ADP: float64(95 + i), Sigma: 6, Value: 500 - i})
		}
		s.PickNo = 100 // round 9; my next pick 111, 10 opponent picks in [100, 111)
		return s
	}
	removals := func(s *State) float64 {
		f := s.SimBacktest(100, 111) // 11 picks, all depths rem 5-6
		total := 0.0
		for id := range s.Players {
			total += 1 - f(id)
		}
		return total
	}

	flat := func(r float64) []float64 {
		out := make([]float64, 15)
		for i := range out {
			out[i] = r
		}
		return out
	}
	if got := removals(build(nil)); got < 10.5 || got > 11.2 {
		t.Errorf("no escape: removals %.2f, want ~11", got)
	}
	if got := removals(build(flat(0.5))); got < 4.4 || got > 6.6 {
		t.Errorf("escape 0.5: removals %.2f, want ~5.5", got)
	}
	if got := removals(build(flat(1))); got > 0.5 {
		t.Errorf("escape 1.0: removals %.2f, want ~0 — every pick left the pool", got)
	}
}

func TestOpponentNeedKDefGate(t *testing.T) {
	s := newTestState(12, 15, 3)
	filled := make([]string, len(s.Roster.Slots)) // all open
	cases := []struct {
		round int
		want  float64
	}{
		{2, 0},            // 14 rounds remaining: gated
		{8, 0},            // 8 remaining: still gated
		{9, NeedStarter},  // 7 remaining: open
		{15, NeedStarter}, // the last round is certainly open
	}
	for _, c := range cases {
		if got := s.opponentNeed("K", nil, filled, c.round); got != c.want {
			t.Errorf("opponentNeed(K, round %d) = %v, want %v", c.round, got, c.want)
		}
	}

	// The evidence the constant is set by: the user's own room ran 16 rounds
	// and took two kickers in round 10, where remaining is 7. The gate must be
	// open there — at 6 it wasn't, and the sim kept kickers "available" that
	// the room had already drafted.
	s.Rounds = 16
	if got := s.opponentNeed("K", nil, filled, 10); got != NeedStarter {
		t.Errorf("opponentNeed(K, round 10 of 16) = %v, want %v: the real room drafts kickers here", got, NeedStarter)
	}
	if got := s.opponentNeed("K", nil, filled, 9); got != 0 {
		t.Errorf("opponentNeed(K, round 9 of 16) = %v, want 0: still gated", got)
	}
	// The gate is the opponents' — our own stricter one must not leak in.
	// Round 10 leaves 6 rounds: OUR suppression (KDefLastRounds 3) still holds
	// there, and needFrom would say 0.
	if got := s.needFrom("K", nil, filled); got != 0 {
		t.Errorf("needFrom(K) early = %v, want 0: our own gate should still hold", got)
	}
}

func TestBetaRamp(t *testing.T) {
	cases := []struct {
		round int
		want  float64
	}{
		{1, 0}, {2, 0}, {3, 0}, // best-available rounds, measured not assumed
		{5, 0.5},
		{7, 1.0},
		{9, 1.5},
		{15, 1.5}, // clamped
	}
	for _, c := range cases {
		if got := betaAt(c.round); got != c.want {
			t.Errorf("betaAt(%d) = %v, want %v", c.round, got, c.want)
		}
	}
}
