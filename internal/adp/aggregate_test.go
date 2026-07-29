package adp

import (
	"math"
	"testing"
)

func TestSigma(t *testing.T) {
	cases := []struct {
		name  string
		stdev float64
		want  float64
	}{
		{"missing stdev falls back", 0, SigmaDefault},
		{"negative stdev falls back", -1, SigmaDefault},
		// The measured median of the 2026 pool: should land ~6.1, i.e. right on
		// top of the old hardcoded constant. This is the calibration check.
		{"median player", 11.1, 11.1 / SigmaFromStdev},
		{"elite player clamps to floor", 0.6, SigmaMin},
		{"deep flier clamps to ceiling", 90.0, SigmaMax},
	}
	for _, c := range cases {
		if got := Sigma(c.stdev); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: Sigma(%v) = %v, want %v", c.name, c.stdev, got, c.want)
		}
	}

	// The whole point of per-player sigma: the tails must actually differ.
	if Sigma(0.8) >= Sigma(11.1) || Sigma(11.1) >= Sigma(45.6) {
		t.Error("sigma must increase with stdev across the observed range")
	}
}

func TestSigmaMedianMatchesLegacyConstant(t *testing.T) {
	// Documents why SigmaDefault is 6.0: it is what the median observed stdev
	// implies. If this drifts far, the default is stale.
	got := Sigma(11.1)
	if math.Abs(got-6.0) > 0.25 {
		t.Errorf("median stdev implies sigma %.2f, expected ~6.0", got)
	}
}

// The shrink is on VARIANCE, not on stdev. Blending square roots instead would
// pull a thin sample too far toward the prior at the small end and not far
// enough at the large one, and the whole point of the correction is the size of
// the move at the thin end.
//
// Hand-computed: sqrt((n*s^2 + n0*p^2)/(n + n0)).
//
//	n=11,  s=12.7, p=16.5, n0=25 -> sqrt((1774.19 + 6806.25)/36)
//	                             =  sqrt(238.34556) = 15.4384441
//	n=190, s=1.4,  p=1.6,  n0=25 -> sqrt((372.4 + 64)/215)
//	                             =  sqrt(2.0297674) = 1.4246991
func TestBlendShrinksVariance(t *testing.T) {
	cases := []struct {
		name   string
		stdev  float64
		prior  float64
		drafts int
		want   float64
	}{
		{"thin sample moves most of the way", 12.7, 16.5, 11, 15.4384441},
		{"well-sampled player barely moves", 1.4, 1.6, 190, 1.4246991},
		// A player with no reported support is left alone: n = 0 would hand the
		// whole answer to the prior and discard the one measurement there is.
		{"no support reported changes nothing", 8.0, 20.0, 0, 8.0},
		{"no stdev reported changes nothing", 0, 20.0, 500, 0},
		// Equal stdev and prior is a fixed point at any sample size.
		{"nothing to move", 6.0, 6.0, 3, 6.0},
	}
	for _, c := range cases {
		got := Blend(c.stdev, c.prior, c.drafts, ShrinkPseudoDrafts)
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s: Blend(%v, %v, %d) = %.6f, want %.6f",
				c.name, c.stdev, c.prior, c.drafts, got, c.want)
		}
	}

	// The direction that justifies the whole thing: the same observed spread
	// moves further toward the prior the less evidence stands behind it.
	thin := Blend(12.7, 16.5, 11, ShrinkPseudoDrafts)
	fat := Blend(12.7, 16.5, 190, ShrinkPseudoDrafts)
	if !(thin > fat && fat > 12.7) {
		t.Errorf("shrink must scale with sample size: 11 drafts -> %.4f, 190 -> %.4f, raw 12.7", thin, fat)
	}
}

// The prior is a LINE through the pool, not one pooled number, because spread
// grows with depth: the 2024 half-ppr pool fits stdev = 0.76 + 0.0876*adp. A
// flat prior would tell a round-15 flier the market agrees about him as tightly
// as it does about a round-5 back.
func TestFitStdevPrior(t *testing.T) {
	// An exact line, so least squares must reproduce it: stdev = 1 + 0.1*adp.
	adps := []float64{10, 20, 30, 40, 50}
	stdevs := []float64{2, 3, 4, 5, 6}
	p := FitStdevPrior(adps, stdevs)
	if math.Abs(p.Intercept-1) > 1e-9 || math.Abs(p.Slope-0.1) > 1e-9 {
		t.Errorf("fit = %.6f + %.6f*adp, want 1 + 0.1*adp", p.Intercept, p.Slope)
	}
	if math.Abs(p.At(100)-11) > 1e-9 {
		t.Errorf("At(100) = %.6f, want 11", p.At(100))
	}
	if p.Pseudo != ShrinkPseudoDrafts {
		t.Errorf("prior weight = %v, want %v", p.Pseudo, ShrinkPseudoDrafts)
	}

	// A degenerate fit must fall back to the median rather than to a vertical
	// line: every player at one adp gives sxx = 0, and dividing by it would put
	// NaN into every sigma on the board.
	flat := FitStdevPrior([]float64{5, 5, 5}, []float64{2, 4, 9})
	if flat.Slope != 0 || flat.At(80) != 4 {
		t.Errorf("degenerate fit = %.4f + %.4f*adp, At(80) = %.4f, want the median 4",
			flat.Intercept, flat.Slope, flat.At(80))
	}
	// And a line that would predict a non-positive spread somewhere hands back
	// the median there instead — a negative prior variance is not a prior.
	down := FitStdevPrior([]float64{10, 20, 30}, []float64{9, 6, 3})
	if down.At(200) != down.Median {
		t.Errorf("extrapolated prior %.4f at adp 200, want the median %.4f", down.At(200), down.Median)
	}
	if FitStdevPrior(nil, nil).At(50) != 0 {
		t.Error("an empty pool has no prior to offer")
	}
}

// ShrinkSigma is the only place Player.Sigma is written, and it has to see the
// whole pool to fit the prior. The bug it prevents is a per-player sigma that
// trusts an 11-draft spread as much as a 190-draft one — measured on the 2024
// pool, where the median player's adp is summarised over 54 drafts and the
// thinnest over 11.
func TestShrinkSigmaUsesTheWholePool(t *testing.T) {
	players := map[string]*Player{}
	// A pool on the line stdev = 1 + 0.1*adp, every player well sampled.
	for i := 1; i <= 10; i++ {
		id := string(rune('a' + i))
		adp := float64(i) * 10
		players[id] = &Player{SleeperID: id, Pos: "RB", ADP: adp, Stdev: 1 + 0.1*adp, TimesDrafted: 500}
	}
	// Two players at the same price, disagreeing wildly, with opposite support.
	players["thin"] = &Player{SleeperID: "thin", Pos: "WR", ADP: 50, Stdev: 1.0, TimesDrafted: 5}
	players["fat"] = &Player{SleeperID: "fat", Pos: "WR", ADP: 50, Stdev: 1.0, TimesDrafted: 5000}
	// No stdev at all: the default, untouched by any of this.
	players["blank"] = &Player{SleeperID: "blank", Pos: "TE", ADP: 50}

	// The exact arithmetic is pinned in TestFitStdevPrior and TestBlendShrinksVariance;
	// what this test owns is the wiring, so it asserts relationships the fit's own
	// numbers cannot fake.
	prior := ShrinkSigma(players)
	if prior.At(20) >= prior.At(150) {
		t.Errorf("prior must grow with depth: %.3f at adp 20, %.3f at adp 150",
			prior.At(20), prior.At(150))
	}
	if players["thin"].Sigma <= players["fat"].Sigma {
		t.Errorf("thin sample %.4f must be pulled further toward the prior than the well-sampled %.4f",
			players["thin"].Sigma, players["fat"].Sigma)
	}
	// Same observed stdev, same adp, so the same prior — the only difference is
	// how much evidence stands behind it, and 5,000 drafts should move an order
	// of magnitude less than 5 do.
	own := Sigma(1.0)
	if fat, thin := players["fat"].Sigma-own, players["thin"].Sigma-own; fat > 0.1*thin {
		t.Errorf("5000-draft player moved %.4f against the 5-draft player's %.4f", fat, thin)
	}
	if got := players["blank"].Sigma; got != SigmaDefault {
		t.Errorf("player with no stdev got sigma %.4f, want the default %.4f", got, SigmaDefault)
	}
}

func TestAssignTiers(t *testing.T) {
	mk := func(id, pos string, v int) *Player {
		return &Player{SleeperID: id, Pos: pos, Value: v}
	}
	players := map[string]*Player{
		// Two elites, then a cliff, then a flat cluster.
		"a": mk("a", "RB", 10000),
		"b": mk("b", "RB", 9700), // -3%, same tier
		"c": mk("c", "RB", 8000), // -17%, breaks
		"d": mk("d", "RB", 7900), // -1.2%, same tier
		"e": mk("e", "RB", 7800), // -1.3%, same tier
		// A different position tiers independently.
		"q": mk("q", "QB", 500),
		"r": mk("r", "QB", 200), // -60% but below the absolute floor scaled to 500
		// No value: must stay tier 0 and not disturb anyone.
		"z": mk("z", "K", 0),
	}
	AssignTiers(players)

	if players["a"].Tier != 1 || players["b"].Tier != 1 {
		t.Errorf("elite pair should share tier 1, got %d and %d", players["a"].Tier, players["b"].Tier)
	}
	if players["c"].Tier != 2 {
		t.Errorf("cliff should start tier 2, got %d", players["c"].Tier)
	}
	if players["d"].Tier != 2 || players["e"].Tier != 2 {
		t.Errorf("flat cluster should stay in tier 2, got %d and %d", players["d"].Tier, players["e"].Tier)
	}
	if players["q"].Tier != 1 {
		t.Errorf("first qb should be tier 1, got %d", players["q"].Tier)
	}
	if players["z"].Tier != 0 {
		t.Errorf("valueless player must stay tier 0, got %d", players["z"].Tier)
	}
}

func TestAssignTiersEmptyAndSingle(t *testing.T) {
	AssignTiers(map[string]*Player{}) // must not panic

	one := map[string]*Player{"a": {SleeperID: "a", Pos: "TE", Value: 100}}
	AssignTiers(one)
	if one["a"].Tier != 1 {
		t.Errorf("single player should be tier 1, got %d", one["a"].Tier)
	}
}

func TestComparable(t *testing.T) {
	if !Comparable("half-ppr", "ppr") || !Comparable("ppr", "standard") {
		t.Error("1qb formats must be comparable to each other")
	}
	if Comparable("half-ppr", "2qb") || Comparable("2qb", "standard") {
		t.Error("2qb is a different roster shape and must not be comparable")
	}
	if !Comparable("2qb", "2qb") {
		t.Error("a format is comparable to itself")
	}
}

// A rankings file that only covers the top of a position must not leave the
// deeper players sharing tier numbers with the elites.
func TestDerivedTiersContinueAfterRankingsTiers(t *testing.T) {
	players := map[string]*Player{
		"a": {SleeperID: "a", Pos: "RB", Value: 10000, Tier: 1, TierSrc: TierFromRankings},
		"b": {SleeperID: "b", Pos: "RB", Value: 9000, Tier: 2, TierSrc: TierFromRankings},
		// Not covered by the file — must land in tier 3 or later, never tier 1.
		"c": {SleeperID: "c", Pos: "RB", Value: 400},
		"d": {SleeperID: "d", Pos: "RB", Value: 100},
	}
	AssignTiers(players)

	if players["a"].Tier != 1 || players["b"].Tier != 2 {
		t.Errorf("rankings tiers were overwritten: a=%d b=%d", players["a"].Tier, players["b"].Tier)
	}
	if players["c"].Tier <= 2 {
		t.Errorf("derived tier collided with rankings tiers: c=%d, want >2", players["c"].Tier)
	}
	if players["c"].TierSrc != TierFromValue {
		t.Errorf("derived player should be marked derived, got %q", players["c"].TierSrc)
	}
	if players["d"].Tier < players["c"].Tier {
		t.Errorf("worse player got a better tier: c=%d d=%d", players["c"].Tier, players["d"].Tier)
	}
}
