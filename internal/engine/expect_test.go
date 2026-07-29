package engine

import (
	"math"
	"testing"
)

// Every fixture below states a survival probability directly instead of
// guessing adp and sigma until the number comes out right. Pin a player's adp
// to the current pick and PSurviveAt collapses to
// exp(softplus(0) - softplus(gap/sigma)), so p = 2/(1 + e^(gap/sigma)) and the
// sigma that produces any p is closed-form.
func survivalSigma(gap int, p float64) float64 {
	return float64(gap) / math.Log(2/p-1)
}

// survivor adds a player who survives to my next pick with probability exactly
// p. PickNo must already be set: the closed form above depends on the horizon.
//
// The tilt is GLOBAL, so these boards need padding — sum(1 - p) over every
// available player must equal PicksUntilMine() or the solve moves every
// probability and no hand-computed expectation survives. Where a test wants an
// untilted board it says so and the padding is part of the fixture; where it
// wants the tilt, the whole point is that the numbers move.
func survivor(s *State, id, pos string, tier, value int, p float64) {
	s.Players[id] = Player{
		ID: id, Name: id, Pos: pos, Tier: tier, Value: value,
		ADP: float64(s.PickNo), Sigma: survivalSigma(s.NextPick()-s.PickNo, p),
	}
}

// opponentPicksBefore is the general rule the tilt's N comes from, so it has to
// agree with the two horizons that are computed independently: every pick
// before my next one belongs to somebody else, and every pick before the one
// after that belongs to somebody else except my own next. Getting this wrong at
// the turn (slot 1 and slot T pick twice in a row) prices removals that never
// happen, or my own pick as an opponent's.
func TestOpponentPicksBefore(t *testing.T) {
	for _, teams := range []int{10, 12} {
		for slot := 1; slot <= teams; slot++ {
			s := newTestState(teams, 15, slot)
			for _, pick := range []int{1, 2, teams, teams + 1, 2*teams - 1, 5 * teams} {
				s.PickNo = pick
				next := s.NextPick()
				if next < s.PickNo {
					continue // past my last pick; NextPick's fallback is in the past
				}
				if got := s.opponentPicksBefore(next); got != s.PicksUntilMine() {
					t.Errorf("teams=%d slot=%d pick=%d: opponentPicksBefore(next)=%d, want PicksUntilMine()=%d",
						teams, slot, pick, got, s.PicksUntilMine())
				}
				q2 := s.FollowingPick()
				if q2 == 0 {
					continue
				}
				if got, want := s.opponentPicksBefore(q2), q2-s.PickNo-1; got != want {
					t.Errorf("teams=%d slot=%d pick=%d: opponentPicksBefore(q2)=%d, want %d",
						teams, slot, pick, got, want)
				}
			}
		}
	}
}

// The tilt exists because independent survivals expect the wrong number of
// removals: eight players at 50% "expect" four to go when exactly two picks
// happen. Solving 8(1 - 0.5^c) = 2 gives 0.5^c = 0.75, c = -log2(0.75) =
// 0.4150375, and every survival lifts to 0.75 — which sums back to exactly the
// two removals the draft actually makes.
//
// The board is deliberately split across two positions: the tilt normalizes
// globally, and a per-position solve would give each position its own c and
// quietly assume every position takes its share of the picks, which is the
// opposite of what a positional run is.
func TestSurvivalTiltMatchesActualPicks(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1 // NextPick 3, so exactly 2 picks intervene
	for i := 0; i < 4; i++ {
		survivor(s, "rb"+string(rune('1'+i)), "RB", 1, 100-i, 0.5)
		survivor(s, "wr"+string(rune('1'+i)), "WR", 1, 90-i, 0.5)
	}

	c := s.survivalTilt(s.NextPick(), s.PicksUntilMine())
	if math.Abs(c-0.4150375) > 1e-6 {
		t.Errorf("tilt exponent = %v, want 0.4150375", c)
	}

	removals := 0.0
	for _, p := range s.Players {
		pt := s.PSurviveTilted(p)
		if math.Abs(pt-0.75) > 1e-4 {
			t.Errorf("tilted survival of %s = %v, want 0.75", p.ID, pt)
		}
		removals += 1 - pt
	}
	if math.Abs(removals-2) > 1e-4 {
		t.Errorf("expected removals after the tilt = %v, want exactly the 2 picks that happen", removals)
	}
}

// The tilt's fixed points are the ones that must not move: a player off the
// drafted radar survives with certainty and p = 1 raised to anything is still
// 1. If the correction could shave him the board would start hedging about
// players no source ranked, which is the one thing it knows for sure.
//
// Ordering is the other invariant. p^c is monotone in p for any c > 0, so a
// tilt can make the board more or less pessimistic but can never claim a
// likelier survivor is less likely than a doomed one. Here c = 4.693 (the
// board is far too optimistic for the three picks that happen), which shoves
// every probability down hard without reordering anything.
func TestSurvivalTiltFixedPointsAndOrdering(t *testing.T) {
	s := newTestState(12, 15, 4)
	s.PickNo = 1 // NextPick 4: 3 picks intervene
	survivor(s, "a", "RB", 1, 100, 0.2)
	survivor(s, "b", "RB", 1, 90, 0.5)
	survivor(s, "c", "RB", 1, 80, 0.8)
	survivor(s, "d", "WR", 1, 70, 0.9)
	// Off the drafted radar: no adp at all, the sentinel path.
	s.Players["radar"] = Player{ID: "radar", Pos: "TE", Tier: 1, Value: 60}

	if got := s.PSurviveTilted(s.Players["radar"]); got != 1 {
		t.Errorf("tilted survival of an undrafted-radar player = %v, want exactly 1", got)
	}
	// 8(1-p^c) summed to 3 gives c = 4.6930082; the tilted values follow.
	want := map[string]float64{"a": 0.0005245, "b": 0.0386602, "c": 0.3509139, "d": 0.6099015}
	prev := 0.0
	for _, id := range []string{"a", "b", "c", "d"} {
		got := s.PSurviveTilted(s.Players[id])
		if math.Abs(got-want[id]) > 1e-6 {
			t.Errorf("tilted survival of %s = %v, want %v", id, got, want[id])
		}
		if got <= prev {
			t.Errorf("tilt reordered the board: %s = %v, not above the previous %v", id, got, prev)
		}
		prev = got
	}
}

// On my own pick nothing intervenes, so there are no removals to reconcile and
// the solve is skipped outright. It has to return exactly 1 — a c of
// 1.0000004 from a bisection that "converged" would make survivals 0.9999998
// and urgency a hair off zero, and the whole my-own-pick exactness chain
// (EBest == v(bestNow), urgency == 0) hangs off this.
func TestSurvivalTiltSkippedOnMyOwnPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick
	for i := 0; i < 5; i++ {
		survivor(s, "rb"+string(rune('1'+i)), "RB", 1, 100-i, 0.5)
	}
	if got := s.survivalTilt(s.NextPick(), s.PicksUntilMine()); got != 1 {
		t.Errorf("tilt on my own pick = %v, want exactly 1", got)
	}
}

// n == 0 does NOT mean "nothing intervenes", and conflating the two is how the
// model came to expect players to vanish across a pick only I make. At the turn
// — slot T picking at 12 and 13 — the lookahead's second leg looks across one
// intervening pick and that pick is mine, so no opponent removes anybody and the
// whole board survives: the low clamp, where a coin flip lifts to
// 0.5^(1/64) = 0.9892280. Skipping the solve on n alone returned c = 1 and left
// the raw logistic in place, pricing the top rb at 13.5% survival across his own
// drafter's pick, in exactly the double-tap case the lookahead exists for.
//
// The horizon is what earns the skip: at at == PickNo every survival is already
// exactly 1, which is what the test above pins.
func TestSurvivalTiltAcrossMyOwnPickAtTheTurn(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 12 // my pick; the next is 13, so only my own pick intervenes
	q2 := s.FollowingPick()
	if q2 != 13 {
		t.Fatalf("fixture is not the turn: following pick = %d, want 13", q2)
	}
	for i := 0; i < 4; i++ {
		survivorAt(s, "rb"+string(rune('1'+i)), "RB", 1, 100-i, q2, 0.5)
	}

	n := s.opponentPicksBefore(q2)
	if n != 0 {
		t.Fatalf("fixture: %d opponent picks before q2, want none", n)
	}
	if got := s.survivalTilt(q2, n); got != 1/TiltCMax {
		t.Errorf("tilt across my own pick = %v, want the low clamp at %v", got, 1/TiltCMax)
	}
	for _, p := range s.Players {
		tilted := math.Pow(s.PSurviveAt(p, q2), s.survivalTilt(q2, n))
		if math.Abs(tilted-0.9892280) > 1e-6 {
			t.Errorf("survival of %s to q2 = %v, want 0.9892280", p.ID, tilted)
		}
	}
}

// Both clamps are reachable board states, not defensive padding, and both must
// come back as finite numbers rather than a NaN that poisons every downstream
// product.
//
// High: eighteen picks against a three-player board. No exponent can make three
// players supply eighteen removals, so c pins at TiltCMax and the board reads
// "everyone is gone" — which is the honest answer to a pool that cannot cover
// the picks. Small ui fixtures land here, which is why they need padding.
//
// Low: five players the model has written off (1e-8) against a single
// intervening pick. Even at c = 1/TiltCMax they still account for 1.25 removals
// when only one happens, so c pins at the floor and they lift to 0.7499 apiece.
func TestSurvivalTiltClamps(t *testing.T) {
	thin := newTestState(12, 15, 3)
	thin.PickNo = 4 // NextPick 22: 18 picks against 3 players
	for i := 0; i < 3; i++ {
		survivor(thin, "rb"+string(rune('1'+i)), "RB", 1, 100-i, 0.5)
	}
	if got := thin.survivalTilt(thin.NextPick(), thin.PicksUntilMine()); got != TiltCMax {
		t.Errorf("thin-board tilt = %v, want the clamp at %v", got, TiltCMax)
	}
	for _, p := range thin.Players {
		pt := thin.PSurviveTilted(p)
		if math.IsNaN(pt) || pt < 0 || pt > 1e-15 {
			t.Errorf("clamped survival of %s = %v, want a finite ~5.4e-20", p.ID, pt)
		}
	}

	crowded := newTestState(12, 15, 3)
	crowded.PickNo = 2 // NextPick 3: exactly 1 pick intervenes
	for i := 0; i < 5; i++ {
		survivor(crowded, "rb"+string(rune('1'+i)), "RB", 1, 100-i, 1e-8)
	}
	if got := crowded.survivalTilt(crowded.NextPick(), crowded.PicksUntilMine()); got != 1/TiltCMax {
		t.Errorf("crowded-board tilt = %v, want the clamp at %v", got, 1/TiltCMax)
	}
	for _, p := range crowded.Players {
		if pt := crowded.PSurviveTilted(p); math.Abs(pt-0.7498942) > 1e-6 {
			t.Errorf("clamped survival of %s = %v, want 0.7498942", p.ID, pt)
		}
	}
}

// The spec's worked anchor, checked against the summation itself rather than
// against adp and sigma values reverse-engineered to produce it. A test a human
// cannot verify on paper is not protecting anything.
//
//	E = 100(.2) + 90(.8)(.6) + 60(.8)(.4)(.99)
//	  = 20 + 43.2 + 19.008
//	  = 82.208
//
// and with an open starter slot the urgency it implies is 100 - 82.208 = 17.792.
func TestExpectedBestAnchor(t *testing.T) {
	got := expectedBest([]float64{100, 90, 60}, []float64{0.2, 0.6, 0.99})
	if math.Abs(got-82.208) > 1e-9 {
		t.Errorf("expectedBest = %v, want 82.208", got)
	}
	if urgency := 100 - got; math.Abs(urgency-17.792) > 1e-9 {
		t.Errorf("implied urgency = %v, want 17.792", urgency)
	}
}

// The residual case: when the whole pool can be gone, the "position empties
// completely" event is worth nothing and contributes nothing. Two players at
// 50% are worth 100(.5) + 60(.5)(.5) = 50 + 15 = 65, and the remaining 25%
// chance that neither is there adds no value rather than falling back to
// somebody's number.
func TestExpectedBestChargesNothingForAnEmptyPosition(t *testing.T) {
	got := expectedBest([]float64{100, 60}, []float64{0.5, 0.5})
	if math.Abs(got-65) > 1e-9 {
		t.Errorf("expectedBest = %v, want 65", got)
	}
	if got := expectedBest(nil, nil); got != 0 {
		t.Errorf("expectedBest on an empty position = %v, want 0", got)
	}
}

// On my own pick EBest must equal bestNow's value EXACTLY, not to within a
// rounding error — urgency's exact zero is what the UI's value tie-break keys
// off, and a 1e-13 residue would leave every position "slightly urgent" and
// order the board by float noise. The exactness comes from acc hitting exactly
// 0 after the first term: every later term is 0 * v, which adds nothing.
func TestEBestOnMyOwnPickIsExact(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 3 // my pick
	addPlayers(s,
		Player{ID: "a", Pos: "RB", Value: 100, ADP: 3, Sigma: 6},
		Player{ID: "b", Pos: "RB", Value: 90, ADP: 4, Sigma: 6},
		Player{ID: "c", Pos: "RB", Value: 80, ADP: 5, Sigma: 6},
	)
	if got := s.EBest("RB", s.NextPick()); got != 100 {
		t.Errorf("EBest on my own pick = %v, want exactly 100", got)
	}
	if got := s.Urgency("RB"); got != 0 {
		t.Errorf("Urgency on my own pick = %v, want exactly 0", got)
	}
}

// ebest's exclusion is what the lookahead's second leg needs: having taken
// bestNow, the position is worth what is left behind him. Same board as the
// anchor shape — with the 100 removed, E = 90(.6) + 60(.4)(.99) = 54 + 23.76 =
// 77.76 — and excluding a player nobody asked about must change nothing.
func TestEBestExcludesATakenPlayer(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1 // NextPick 3, 2 picks intervene
	survivor(s, "a", "RB", 1, 100, 0.2)
	survivor(s, "b", "RB", 1, 90, 0.6)
	survivor(s, "c", "RB", 1, 60, 0.99)
	survivor(s, "pad", "WR", 1, 50, 0.21) // sum(1-p) = .8+.4+.01+.79 = the 2 picks: c = 1

	if got := s.ebest("RB", s.NextPick(), "a"); math.Abs(got-77.76) > 1e-3 {
		t.Errorf("EBest without a = %v, want 77.76", got)
	}
	if got := s.ebest("RB", s.NextPick(), "nobody"); math.Abs(got-82.208) > 1e-3 {
		t.Errorf("EBest excluding an unknown id = %v, want the full 82.208", got)
	}
}
