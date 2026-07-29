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

// BestLater names the man you'd most likely end up taking — not the doomed
// higher value above him, and not the deepest, safest player on the board.
// Weights are p~ * prod(1 - p~) over the earlier men: a 0.02, b 0.98(.6) =
// 0.588, c 0.98(.4)(.95) = 0.3724. b wins on the modal question even though c
// is the likelier survivor by a mile.
func TestBestLaterIsTheModalSurvivor(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1 // NextPick 3: 2 picks intervene
	survivor(s, "a", "RB", 1, 100, 0.02)
	survivor(s, "b", "RB", 1, 90, 0.6)
	survivor(s, "c", "RB", 1, 80, 0.95)
	survivor(s, "pad", "WR", 1, 50, 0.43) // sum(1-p) = .98+.4+.05+.57 = the 2 picks: c = 1

	later, ok := s.BestLater("RB")
	if !ok || later.ID != "b" {
		t.Errorf("BestLater = %q ok=%v, want b (the modal survivor)", later.ID, ok)
	}
}

// When nobody is likely to survive, the modal answer flips to the man at the
// TOP of the board, not the deepest one — he only has to outlast the picks
// themselves, while everyone under him also needs the men above to go first.
// a at 0.40 beats b at 0.60(0.45) = 0.27. The old threshold rule returned b
// here, which told you to plan around the player you were least likely to be
// choosing from.
func TestBestLaterWhenNobodyIsLikelyToSurvive(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1
	survivor(s, "a", "RB", 1, 100, 0.4)
	survivor(s, "b", "RB", 1, 90, 0.45)
	survivor(s, "pad", "WR", 1, 50, 0.15) // sum(1-p) = .6+.55+.85 = the 2 picks: c = 1

	if later, _ := s.BestLater("RB"); later.ID != "a" {
		t.Errorf("BestLater = %q, want a", later.ID)
	}
	if _, ok := s.BestLater("WR2"); ok {
		t.Error("BestLater on an empty position reported ok")
	}
}

// A bestNow who will still be there is the wait signal, and it is now a claim
// about HIM rather than about urgency being exactly zero — urgency is
// continuous, so an exact zero only happens on my own pick. Here rb1 survives
// at 0.8 and the position costs a token 0.9 to wait on:
// E = 100(.8) + 60(.2)(.5) = 80 + 6 = 86.
func TestSafeToWaitFollowsBestNowsOwnSurvival(t *testing.T) {
	// The two pads absorb whatever bestNow doesn't, so sum(1-p) over the
	// available board stays at exactly the 2 intervening picks however p moves
	// and the tilt is a no-op. Skip that and lowering bestNow's survival
	// silently raises everyone else's, which is the whole point of the tilt and
	// the death of any hand-computed expectation.
	board := func(p float64) *State {
		s := newTestState(12, 15, 3)
		s.PickNo = 1 // NextPick 3
		survivor(s, "a", "RB", 1, 100, p)
		survivor(s, "b", "RB", 1, 60, 0.5)
		pad := 1 - (2-(1-p)-0.5)/2
		survivor(s, "pad1", "WR", 1, 50, pad)
		survivor(s, "pad2", "WR", 1, 40, pad)
		return s
	}

	s := board(0.8)
	if !s.SafeToWait("RB") {
		t.Error("bestNow survives at 80%: rb should read safe to wait")
	}
	if got := s.Urgency("RB"); math.Abs(got-14) > 1e-3 {
		t.Errorf("Urgency = %v, want 14 (100 - 86)", got)
	}

	// Drop bestNow's own survival under the threshold and the tag goes, even
	// though the position is otherwise identical.
	if board(0.4).SafeToWait("RB") {
		t.Error("bestNow survives at 40%: rb must not read safe to wait")
	}
	// An untiered position makes no claim either way, and neither does a
	// position with nobody in it.
	survivor(s, "k1", "K", 0, 100, 0.9)
	if s.SafeToWait("K") {
		t.Error("an untiered position reported safe to wait")
	}
	if s.SafeToWait("TE") {
		t.Error("an empty position reported safe to wait")
	}
}

// Urgency is the at-risk value scaled by need: the same board reads 35, then
// 21, then 8.75 as my RB slots fill and the position stops mattering. Two
// players at 50% give E = 100(.5) + 60(.5)(.5) = 50 + 15 = 65, so the cost of
// waiting is 35 before need touches it. The ratios are exact whatever the tilt
// does, because taken fillers change need and nothing else.
func TestUrgencyNeedLadder(t *testing.T) {
	cases := []struct {
		filler int
		want   float64
	}{
		{0, 35.00}, // starter open: (100-65) * 1.0
		{2, 21.00}, // both rb slots filled, flex open: * 0.6
		{3, 8.75},  // flex filled too: * 0.25
	}
	var starter float64
	for _, c := range cases {
		s := newTestState(12, 15, 3)
		s.PickNo = 1 // NextPick 3: 2 picks intervene
		survivor(s, "a", "RB", 1, 100, 0.5)
		survivor(s, "b", "RB", 1, 60, 0.5)
		survivor(s, "x", "WR", 1, 50, 0.5) // padding: sum(1-p) over the four
		survivor(s, "y", "WR", 1, 40, 0.5) // available players = the 2 picks, c = 1
		for i := 0; i < c.filler; i++ {
			id := "taken" + string(rune('0'+i))
			addPlayers(s, Player{ID: id, Pos: "RB", Value: 99})
			s.Taken[id] = true // must be taken, or the fillers pollute Available
			s.Rosters[3] = append(s.Rosters[3], id)
		}
		got := s.Urgency("RB")
		// 1e-3, not 1e-9: the tilt bisects to TiltTol, so c lands within 1e-6
		// of 1 rather than on it.
		if math.Abs(got-c.want) > 1e-3 {
			t.Errorf("Urgency with %d rostered rbs = %v, want %v", c.filler, got, c.want)
		}
		if c.filler == 0 {
			starter = got
		} else if want := starter * c.want / cases[0].want; math.Abs(got-want) > 1e-9 {
			t.Errorf("need ladder is not a clean scaling: %v, want %v", got, want)
		}
	}
}

// A 90-point kicker gap is still worth exactly nothing until the last rounds —
// then the same board springs to life. Need gates urgency, not the reverse, and
// the early zero must be exact rather than merely small.
func TestUrgencyKickerSuppression(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 157 // round 14 of 15: NextPick 166, so 9 picks intervene
	survivor(s, "k1", "K", 0, 100, 0.1)
	survivor(s, "k2", "K", 0, 10, 0.9)
	// Nine picks against a two-man board would clamp the tilt and crush both
	// kickers to zero, so the rest of the room's board is here too: ten wrs at
	// 0.2 carry 8.0 of the 9 removals, k1 and k2 the other 1.0.
	for i := 0; i < 10; i++ {
		survivor(s, "wr"+string(rune('0'+i)), "WR", 1, 50-i, 0.2)
	}

	s.PickNo = 4 // rounds remaining 15, kickers suppressed
	if got := s.Urgency("K"); got != 0 {
		t.Errorf("Urgency(K) in round 1 = %v, want exactly 0", got)
	}

	s.PickNo = 157
	// E = 100(.1) + 10(.9)(.9) = 10 + 8.1 = 18.1, so waiting costs 81.9.
	if got := s.Urgency("K"); math.Abs(got-81.9) > 1e-3 {
		t.Errorf("Urgency(K) in round 14 = %v, want 81.9", got)
	}
}

// A position down to one player prices exactly the risk of losing him:
// E = 100(.4), so urgency is the 60 that walks out of the door if he goes.
// Under the old rule this read 0 — the lone player was returned as his own
// bestLater — which said "no cost to waiting" about a position that might
// vanish, and left the cliff banner as the only warning.
func TestUrgencySinglePlayerPosition(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1
	survivor(s, "a", "RB", 1, 100, 0.4)
	survivor(s, "w1", "WR", 1, 50, 0.3) // padding: .6 + .7 + .7 = the 2 picks
	survivor(s, "w2", "WR", 1, 40, 0.3)

	if got := s.Urgency("RB"); math.Abs(got-60) > 1e-3 {
		t.Errorf("Urgency with one player = %v, want 60", got)
	}
	if got := s.Urgency("TE"); got != 0 {
		t.Errorf("Urgency on an empty position = %v, want 0", got)
	}
}

// On my own pick everyone survives (no picks intervene), so urgency is zero
// across the board and the UI's value tie-break does the pointing. Anything
// nonzero here would claim waiting costs value when waiting isn't happening —
// and it must be an exact zero, not a rounding-error zero, or the tie-break
// never gets a chance to run.
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
	if s.SafeToWait("WR") {
		t.Error("nothing is safe to wait on when the wait is zero picks long")
	}
}

// The reason EBest exists. The old rule counted a player at 0.501 in full and
// the same player at 0.499 not at all, so urgency jumped ~40 points on a
// rounding error and the whole board re-sorted: below the line bestLater was
// the 50-point c, above it the 90-point b. Here the same two boards differ by
// 0.0002 in one survival and the answers differ by under a hundredth.
func TestUrgencyIsContinuousAcrossTheOldThreshold(t *testing.T) {
	urgency := func(p float64) float64 {
		s := newTestState(12, 15, 3)
		s.PickNo = 1 // NextPick 3: 2 picks intervene
		survivor(s, "a", "RB", 1, 100, 0.3)
		survivor(s, "b", "RB", 1, 90, p)
		survivor(s, "c", "RB", 1, 50, 0.99)
		survivor(s, "pad", "WR", 1, 40, 0.21) // .7 + .5 + .01 + .79 = the 2 picks
		return s.Urgency("RB")
	}
	below, above := urgency(0.4999), urgency(0.5001)
	if math.Abs(below-above) > 0.01 {
		t.Errorf("urgency jumped across p=0.5: %v vs %v", below, above)
	}
	// E = 100(.3) + 90(.7)(.5) + 50(.7)(.5)(.99) = 30 + 31.5 + 17.325 = 78.825.
	for _, got := range []float64{below, above} {
		if math.Abs(got-21.175) > 0.01 {
			t.Errorf("Urgency = %v, want 21.175", got)
		}
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
