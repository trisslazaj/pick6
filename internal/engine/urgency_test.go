package engine

import (
	"math"
	"testing"
)

// addPlayers drops players straight into the pool.
func addPlayers(s *State, ps ...Player) {
	for _, p := range ps {
		s.Players[p.ID] = p
	}
}

// Fixture geometry used throughout: 12 teams, slot 3, PickNo 4. My next pick is
// 22 (round 2 reverses: 12 + (12-3+1)), pinned by TestNextPickAndPicksUntilMine.

func TestPSurviveLogistic(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4 // NextPick() = 22
	cases := []struct {
		adp, sigma, want float64
	}{
		{22, 6, 0.5},       // adp at my pick: literally a coin flip
		{16, 6, 0.2689414}, // one sigma early
		{28, 6, 0.7310586}, // one sigma late
		{10, 6, 0.1192029}, // two sigma early
		{34, 6, 0.8807971},
		// The per-player sigma pair: the same 1-pick gap is "he's gone" for a
		// predictable player and a coin flip for a volatile one. This is the
		// whole reason sigma is per-player and not a constant.
		{21, 0.5, 0.1192029},
		{21, 6, 0.4584295},
	}
	for _, c := range cases {
		p := Player{ID: "x", Pos: "RB", ADP: c.adp, Sigma: c.sigma}
		if got := s.PSurvive(p); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("PSurvive(adp=%v, sigma=%v) = %v, want %v", c.adp, c.sigma, got, c.want)
		}
	}
}

// A zero sigma must behave as SigmaDefault exactly — not as infinity (flat coin
// flips everywhere) and not as a division blowup.
func TestPSurviveSigmaFallback(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	for _, sigma := range []float64{0, -1} {
		p := Player{ID: "x", Pos: "RB", ADP: 16, Sigma: sigma}
		if got := s.PSurvive(p); math.Abs(got-0.2689414) > 1e-6 {
			t.Errorf("PSurvive(sigma=%v) = %v, want the SigmaDefault answer 0.2689414", sigma, got)
		}
	}
}

// Missing ADP means the player is off the drafted radar; he always survives.
// Live drafts register such players mid-draft, so this path is real, not
// defensive.
func TestPSurviveMissingADP(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	for _, adp := range []float64{0, -5} {
		p := Player{ID: "x", Pos: "RB", ADP: adp, Sigma: 6}
		if got := s.PSurvive(p); got < 0.999999 {
			t.Errorf("PSurvive(adp=%v) = %v, want ~1 via the UndraftedADP sentinel", adp, got)
		}
	}
}

// The exponent clamp can't be seen by a tolerance table — clamped (9.36e-14)
// and unclamped (1.28e-18) are both "about zero". The window below fails if the
// clamp is missing and fails if exp overflowed to NaN/0.
func TestPSurviveClampsExponent(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	p := Player{ID: "x", Pos: "RB", ADP: 1.4, Sigma: 0.5} // quotient 41.2, clamped to 30
	got := s.PSurvive(p)
	if math.IsNaN(got) || got <= 1e-14 || got >= 1e-12 {
		t.Errorf("PSurvive(extreme) = %v, want ~9.36e-14 (the clamped value)", got)
	}
}

// On my own pick NextPick == PickNo and the logistic still answers sanely.
func TestPSurviveOnMyPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick; NextPick() = 3
	cases := []struct {
		adp, want float64
	}{
		{3, 0.5},
		{1.5, 0.4378235},
	}
	for _, c := range cases {
		p := Player{ID: "x", Pos: "RB", ADP: c.adp, Sigma: 6}
		if got := s.PSurvive(p); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("PSurvive(adp=%v) on my pick = %v, want %v", c.adp, got, c.want)
		}
	}
}

// BestLater must pick the highest *value* among likely survivors — not the
// likeliest survivor, and not a higher value who won't be there.
func TestBestLaterPicksValueAmongSurvivors(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2},  // p ~ 0.0001: gone
		Player{ID: "b", Pos: "RB", Value: 90, ADP: 20, Sigma: 4},  // p = 0.378: below threshold
		Player{ID: "c", Pos: "RB", Value: 80, ADP: 26, Sigma: 4},  // p = 0.731: survives
		Player{ID: "d", Pos: "RB", Value: 70, ADP: 40, Sigma: 6},  // p = 0.953: survives harder
	)
	later, ok := s.BestLater("RB")
	if !ok || later.ID != "c" {
		t.Errorf("BestLater = %q ok=%v, want c (highest value among survivors)", later.ID, ok)
	}
}

// A coin-flip player (p exactly 0.5) counts as a candidate: >= not >.
func TestBestLaterThresholdIsInclusive(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2},
		Player{ID: "e", Pos: "RB", Value: 95, ADP: 22, Sigma: 6}, // adp == next pick: p = 0.5
	)
	if later, _ := s.BestLater("RB"); later.ID != "e" {
		t.Errorf("BestLater = %q, want the coin-flip player e", later.ID)
	}
	if got := s.Urgency("RB"); math.Abs(got-5.0) > 1e-9 {
		t.Errorf("Urgency = %v, want 5 (100-95, need 1.0)", got)
	}
}

// When nobody clears the threshold, someone always technically survives: the
// likeliest survivor wins even if a doomed player is worth more.
func TestBestLaterFallbackToLikeliest(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2},  // p ~ 0.0001
		Player{ID: "b", Pos: "RB", Value: 90, ADP: 10, Sigma: 4},  // p = 0.047
	)
	if later, _ := s.BestLater("RB"); later.ID != "b" {
		t.Errorf("BestLater = %q, want b (highest survival when no one clears 0.5)", later.ID)
	}
	if _, ok := s.BestLater("WR"); ok {
		t.Error("BestLater on an empty position reported ok")
	}
}

// Zero urgency when bestNow will still be there IS the wait signal — the value
// gap to the next man down is irrelevant if you don't have to take it yet.
func TestUrgencyZeroWhenBestNowSurvives(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 30, Sigma: 5}, // p = 0.832: safe
		Player{ID: "b", Pos: "RB", Value: 50, ADP: 50, Sigma: 8},
	)
	if got := s.Urgency("RB"); got != 0 {
		t.Errorf("Urgency = %v, want exactly 0 when bestNow survives", got)
	}
}

// Urgency is the at-risk value gap scaled by need: the same board reads 40,
// then 24, then 10 as my RB slots fill and the position stops mattering.
func TestUrgencyNeedLadder(t *testing.T) {
	cases := []struct {
		filler int
		want   float64
	}{
		{0, 40.0}, // starter open: (100-60) * 1.0
		{2, 24.0}, // both RB slots filled, flex open: * 0.6
		{3, 10.0}, // flex filled too: * 0.25
	}
	for _, c := range cases {
		s := newTestState(12, 15, 3)
		s.PickNo = 4
		addPlayers(s,
			Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2},  // gone by 22
			Player{ID: "b", Pos: "RB", Value: 60, ADP: 30, Sigma: 5},  // survives
		)
		for i := 0; i < c.filler; i++ {
			id := "taken" + string(rune('0'+i))
			addPlayers(s, Player{ID: id, Pos: "RB", Value: 99})
			s.Taken[id] = true // must be taken, or the fillers pollute Available
			s.Rosters[3] = append(s.Rosters[3], id)
		}
		if got := s.Urgency("RB"); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Urgency with %d rostered rbs = %v, want %v", c.filler, got, c.want)
		}
	}
}

// A 90-point kicker gap is still worth exactly nothing until the last rounds —
// then the same board springs to life. Need gates urgency, not the reverse.
func TestUrgencyKickerSuppression(t *testing.T) {
	s := newTestState(12, 15, 3)
	addPlayers(s,
		Player{ID: "k1", Pos: "K", Value: 100, ADP: 4, Sigma: 2},
		Player{ID: "k2", Pos: "K", Value: 10, ADP: 200, Sigma: 10},
	)
	s.PickNo = 4
	if got := s.Urgency("K"); got != 0 {
		t.Errorf("Urgency(K) in round 1 = %v, want 0", got)
	}
	s.PickNo = 157 // round 14 of 15: NextPick 166, kickers now in play
	// k1 is long gone (clamped ~0), k2 survives at 0.968: urgency = (100-10) * 1.0.
	if got := s.Urgency("K"); math.Abs(got-90.0) > 1e-9 {
		t.Errorf("Urgency(K) in round 14 = %v, want 90", got)
	}
}

// A position down to one player reads urgency 0: the fallback returns him as
// his own bestLater. Whether he vanishes outright is the cliff banner's job;
// urgency only prices the drop between now and later.
func TestUrgencySinglePlayerPosition(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s, Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2})
	if got := s.Urgency("RB"); got != 0 {
		t.Errorf("Urgency with one player = %v, want 0", got)
	}
	if got := s.Urgency("WR"); got != 0 {
		t.Errorf("Urgency on an empty position = %v, want 0", got)
	}
}

// On my own pick the degenerate case must still point at the fallen elite:
// he fails the survival cut, bestLater drops to the safe man, and the gap says
// "take bestNow" — exactly the advice the spec promises.
func TestUrgencyOnMyPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick
	addPlayers(s,
		Player{ID: "a", Pos: "WR", Value: 100, ADP: 1.5, Sigma: 6}, // p = 0.438: fails the cut
		Player{ID: "b", Pos: "WR", Value: 60, ADP: 10, Sigma: 4},   // p = 0.852: survives
	)
	if got := s.Urgency("WR"); math.Abs(got-40.0) > 1e-9 {
		t.Errorf("Urgency on my pick = %v, want 40", got)
	}
}
