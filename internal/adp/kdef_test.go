package adp

import "testing"

// anchorBoard is a skill curve with the property that broke the spec's original
// rule: value is NOT monotone in adp. The 500 at adp 40 sits behind the 200 at
// adp 30, exactly as Tyler Allgeier (adp 173.4, value 538) sits behind Keaton
// Mitchell (172.9, value 194) on the real board.
//
// Interpolating a defense priced at 35 between his adp neighbours would put him
// between 200 and 500 — above three quarters of the board — while the defense
// priced at 25 landed below him. The rank rule cannot do that: k only grows with
// adp and the value list only shrinks.
func anchorBoard() map[string]*Player {
	return map[string]*Player{
		"s1":  {SleeperID: "s1", Pos: "RB", ADP: 10, Value: 1000},
		"s2":  {SleeperID: "s2", Pos: "WR", ADP: 20, Value: 800},
		"s3":  {SleeperID: "s3", Pos: "WR", ADP: 30, Value: 200},
		"s4":  {SleeperID: "s4", Pos: "RB", ADP: 40, Value: 500},
		"s5":  {SleeperID: "s5", Pos: "TE", ADP: 50, Value: 100},
		"d25": {SleeperID: "d25", Pos: "DEF", ADP: 25},
		"d35": {SleeperID: "d35", Pos: "DEF", ADP: 35},
		"k60": {SleeperID: "k60", Pos: "K", ADP: 60},
		"k5":  {SleeperID: "k5", Pos: "K", ADP: 5},
	}
}

// The rank rule: k is how many valued skill players are priced ahead of him, and
// he takes the k-th largest skill value. Values descending are 1000, 800, 500,
// 200, 100 — note the sort, which is what makes the answer monotone even though
// the adp-ordered values are not.
func TestAnchorKDefValuesByRank(t *testing.T) {
	players := anchorBoard()
	AnchorKDefValues(players)

	cases := []struct {
		id   string
		want int
	}{
		{"k5", 1000}, // nobody ahead of him: the top of the curve, not a wrap to 0
		{"d25", 800}, // two ahead (adp 10, 20) -> the 2nd largest value
		{"d35", 500}, // three ahead -> the 3rd largest
		{"k60", 100}, // all five ahead -> the 5th largest, the floor
	}
	for _, c := range cases {
		if got := players[c.id].Value; got != c.want {
			t.Errorf("value(%s) = %d, want %d", c.id, got, c.want)
		}
	}

	// Monotone in adp, which neighbour interpolation is not: on the real board it
	// gave denver DEF (adp 103.3) 1698.6, houston (106.4) 989.9 and the la rams
	// (108.3) 251.1 — three defenses ordered nonsensically against their own
	// prices, which then corrupts Available(), since that sorts by value.
	order := []string{"k5", "d25", "d35", "k60"}
	for i := 1; i < len(order); i++ {
		if players[order[i]].Value > players[order[i-1]].Value {
			t.Errorf("value is not monotone in adp: %s (%d) beats %s (%d)",
				order[i], players[order[i]].Value, order[i-1], players[order[i-1]].Value)
		}
	}

	// Skill values are never touched — the anchor reads that curve, it does not
	// rewrite it.
	if players["s3"].Value != 200 {
		t.Errorf("anchor moved a skill value: s3 = %d, want 200", players["s3"].Value)
	}

	// Idempotent, because fetch calls it again after a rankings file has moved
	// the skill curve underneath it.
	AnchorKDefValues(players)
	if got := players["d25"].Value; got != 800 {
		t.Errorf("second pass moved d25 to %d, want 800", got)
	}
}

// A kicker with no adp has nothing to be ranked against, and inventing a value
// for him would be inventing data. That is the live-feed case: a defense nobody
// ranked, registered off pick metadata.
func TestAnchorKDefValuesNeedsAPrice(t *testing.T) {
	players := anchorBoard()
	players["ghost"] = &Player{SleeperID: "ghost", Pos: "DEF"}
	AnchorKDefValues(players)
	if got := players["ghost"].Value; got != 0 {
		t.Errorf("value of a defense with no adp = %d, want 0", got)
	}

	// No skill curve at all: nothing to anchor to, and k/def stay where they were
	// rather than picking up a value from nowhere.
	bare := map[string]*Player{"d": {SleeperID: "d", Pos: "DEF", ADP: 100}}
	AnchorKDefValues(bare)
	if got := bare["d"].Value; got != 0 {
		t.Errorf("value with no skill board = %d, want 0", got)
	}
}

// The trap this pairs with: AssignTiers used to tier anyone with a value, so the
// moment k/def got one they would have started carrying tiers — and a tier is
// what cliff logic keys off. Their value is BORROWED from a skill player at the
// same price, so a gap between two kickers is a gap between two receivers
// somewhere else; it cannot draw a real boundary. Tier 0 is the contract with
// the engine, and this pins it.
func TestKDefStayUntieredEvenWithAValue(t *testing.T) {
	players := anchorBoard()
	AnchorKDefValues(players)
	AssignTiers(players)

	for _, id := range []string{"k5", "d25", "d35", "k60"} {
		p := players[id]
		if p.Value == 0 {
			t.Fatalf("%s has no value, so this test proves nothing about tiering him", id)
		}
		if p.Tier != 0 {
			t.Errorf("%s has tier %d, want 0 — cliff logic only skips tier 0", id, p.Tier)
		}
		if p.TierSrc != TierNone {
			t.Errorf("%s has tier source %q, want none", id, p.TierSrc)
		}
	}

	// A rankings file that hand-tiers kickers doesn't get to override it either:
	// the decision is about what a borrowed value can support, not about who
	// typed it.
	players["k5"].Tier, players["k5"].TierSrc = 2, TierFromRankings
	AssignTiers(players)
	if players["k5"].Tier != 0 {
		t.Errorf("a hand-typed kicker tier survived at %d, want 0", players["k5"].Tier)
	}
}
