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
