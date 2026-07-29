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
	// Expected values are S(22)/S(4) — survival to my next pick, conditioned on
	// having survived to the current one.
	cases := []struct {
		adp, sigma, want float64
	}{
		{22, 6, 0.5248935}, // adp at my pick: a coin flip, nudged up by conditioning
		{16, 6, 0.3053387}, // one sigma early
		{28, 6, 0.7444484}, // one sigma late
		{10, 6, 0.1630552}, // two sigma early
		{34, 6, 0.8867318},
		// The per-player sigma pair: the same 1-pick gap is "he's gone" for a
		// predictable player and a coin flip for a volatile one. This is the
		// whole reason sigma is per-player and not a constant.
		{21, 0.5, 0.1192029},
		{21, 6, 0.4853927},
	}
	for _, c := range cases {
		p := Player{ID: "x", Pos: "RB", ADP: c.adp, Sigma: c.sigma}
		if got := s.PSurvive(p); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("PSurvive(adp=%v, sigma=%v) = %v, want %v", c.adp, c.sigma, got, c.want)
		}
	}
}

// The conditioning regression: a faller — adp 18, still here at pick 21, one
// pick before mine — can lose at most one team's pick, so he survives at ~78%.
// The unconditional logistic said ~21%, a number that ignores him being right
// there on the board. Far from my pick the two models agree (18 intervening
// picks really are likely to eat him), which is exactly the shape we want.
func TestPSurviveConditionsOnThePresent(t *testing.T) {
	faller := Player{ID: "x", Pos: "RB", ADP: 18, Sigma: 3}

	s := newTestState(12, 15, 3)
	s.PickNo = 21 // NextPick() = 22: one pick intervenes
	if got := s.PSurvive(faller); math.Abs(got-0.7756653) > 1e-6 {
		t.Errorf("PSurvive(faller, 1 pick out) = %v, want 0.7756653", got)
	}

	s.PickNo = 4 // NextPick() = 22: eighteen picks intervene
	if got := s.PSurvive(faller); math.Abs(got-0.2105702) > 1e-6 {
		t.Errorf("PSurvive(faller, 18 picks out) = %v, want 0.2105702", got)
	}
}

// A zero sigma must behave as SigmaDefault exactly — not as infinity (flat coin
// flips everywhere) and not as a division blowup.
func TestPSurviveSigmaFallback(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	for _, sigma := range []float64{0, -1} {
		p := Player{ID: "x", Pos: "RB", ADP: 16, Sigma: sigma}
		if got := s.PSurvive(p); math.Abs(got-0.3053387) > 1e-6 {
			t.Errorf("PSurvive(sigma=%v) = %v, want the SigmaDefault answer 0.3053387", sigma, got)
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

// Extreme exponents must come through exact, not clamped into an artifact: the
// log-space form gives the true 2.33e-16 here, where clamping each S at ±30
// used to manufacture 1.71e-11. The window fails on the old artifact, on NaN,
// and on a flushed-to-zero underflow.
func TestPSurviveExtremeExponents(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	p := Player{ID: "x", Pos: "RB", ADP: 1.4, Sigma: 0.5} // exponents 5.2 and 41.2
	got := s.PSurvive(p)
	if math.IsNaN(got) || got <= 1e-16 || got >= 1e-15 {
		t.Errorf("PSurvive(extreme) = %v, want ~2.33e-16 (the exact value)", got)
	}
}

// The deep-faller regression: with a tight sigma, a player far past his ADP
// used to saturate both exponents and read 100% — the further he fell, the
// safer he looked, which put a green "safe to wait" on the hottest discount on
// the board. The conditional tail is a per-pick hazard, exp(-(next-now)/sigma):
// two intervening picks at sigma 0.5 is e^-4 no matter how deep the fall.
func TestPSurviveDeepFallerTail(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 25 // NextPick() = 27: two picks intervene
	want := math.Exp(-4)
	for _, adp := range []float64{10, 5} { // 30 and 40 own-sigmas past his price
		p := Player{ID: "x", Pos: "RB", ADP: adp, Sigma: 0.5}
		if got := s.PSurvive(p); math.Abs(got-want) > 1e-6 {
			t.Errorf("PSurvive(deep faller, adp=%v) = %v, want e^-4 = %v", adp, got, want)
		}
	}
}

// After the draft NextPick's fallback is the final pick, which is in the past;
// unguarded, the ratio tops 1 and a replay frame prints "105%". Everyone left
// on a finished board "survives" at exactly 100%.
func TestPSurviveAfterDraftDone(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 12*15 + 1 // Done
	for _, adp := range []float64{182, 185, 190} {
		p := Player{ID: "x", Pos: "RB", ADP: adp, Sigma: 6}
		if got := s.PSurvive(p); got != 1 {
			t.Errorf("PSurvive(adp=%v) after the draft = %v, want exactly 1", adp, got)
		}
	}
}

// On my own pick no picks intervene, so everyone still on the board survives
// with certainty — even a deep faller. The board's ordering job then falls to
// the value tie-break, not to survival.
func TestPSurviveOnMyPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick; NextPick() = 3
	for _, adp := range []float64{3, 1.5, 50} {
		p := Player{ID: "x", Pos: "RB", ADP: adp, Sigma: 6}
		if got := s.PSurvive(p); got != 1 {
			t.Errorf("PSurvive(adp=%v) on my own pick = %v, want exactly 1", adp, got)
		}
	}
}

// BestLater must pick the highest *value* among likely survivors — not the
// likeliest survivor, and not a higher value who won't be there.
func TestBestLaterPicksValueAmongSurvivors(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2}, // p ~ 0.0002: gone
		Player{ID: "b", Pos: "RB", Value: 90, ADP: 20, Sigma: 4}, // p = 0.384: below threshold
		Player{ID: "c", Pos: "RB", Value: 80, ADP: 26, Sigma: 4}, // p = 0.734: survives
		Player{ID: "d", Pos: "RB", Value: 70, ADP: 40, Sigma: 6}, // p = 0.955: survives harder
	)
	later, ok := s.BestLater("RB")
	if !ok || later.ID != "c" {
		t.Errorf("BestLater = %q ok=%v, want c (highest value among survivors)", later.ID, ok)
	}
}

// A coin-flip player is candidate territory. Conditioning makes an exact 0.5
// unreachable (S(now) < 1 always nudges the coin flip up, here to 0.525), so
// the >= in BestLater is no longer distinguishable from > by any test — this
// pins the nearest realizable thing: adp on my pick means candidate, and
// urgency prices only the small gap above him.
func TestBestLaterCoinFlipIsACandidate(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 4
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2},
		Player{ID: "e", Pos: "RB", Value: 95, ADP: 22, Sigma: 6}, // p = 0.525
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
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2}, // p ~ 0.0002
		Player{ID: "b", Pos: "RB", Value: 90, ADP: 10, Sigma: 4}, // p = 0.058
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
			Player{ID: "a", Pos: "RB", Value: 100, ADP: 4, Sigma: 2}, // gone by 22
			Player{ID: "b", Pos: "RB", Value: 60, ADP: 30, Sigma: 5}, // survives
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
		Player{ID: "k1", Pos: "K", Value: 100, ADP: 140, Sigma: 2},
		Player{ID: "k2", Pos: "K", Value: 10, ADP: 200, Sigma: 10},
	)
	s.PickNo = 4
	if got := s.Urgency("K"); got != 0 {
		t.Errorf("Urgency(K) in round 1 = %v, want 0", got)
	}
	s.PickNo = 157 // round 14 of 15: NextPick 166, kickers now in play
	// k1 won't last the nine picks (p = 0.011), k2 survives at 0.981:
	// urgency = (100-10) * 1.0.
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

// On my own pick everyone survives (no picks intervene), so urgency is zero
// across the board and the UI's value tie-break does the pointing. Anything
// nonzero here would claim waiting costs value when waiting isn't happening.
func TestUrgencyZeroOnMyOwnPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick
	addPlayers(s,
		Player{ID: "a", Pos: "WR", Value: 100, ADP: 1.5, Sigma: 6}, // a faller, and still p = 1
		Player{ID: "b", Pos: "WR", Value: 60, ADP: 10, Sigma: 4},
	)
	if got := s.Urgency("WR"); got != 0 {
		t.Errorf("Urgency on my own pick = %v, want 0", got)
	}
}

// Falling is measured in the player's own sigma: the same 6-pick slide is a
// screaming discount for a locked-in player and noise for a volatile one.
func TestFalling(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 21
	cases := []struct {
		adp, sigma float64
		want       bool
	}{
		{15, 3, true},   // 2 sigma past his price
		{18, 3, true},   // exactly 1 sigma: >= fires
		{20, 3, false},  // barely past adp: not yet a faller
		{15, 25, false}, // volatile flier: 6 picks is nothing
		{25, 3, false},  // adp still ahead
		{0, 3, false},   // off the radar: never falling
	}
	for _, c := range cases {
		p := Player{ID: "x", Pos: "RB", ADP: c.adp, Sigma: c.sigma}
		if got := s.Falling(p); got != c.want {
			t.Errorf("Falling(adp=%v, sigma=%v) at pick 21 = %v, want %v", c.adp, c.sigma, got, c.want)
		}
	}
}
