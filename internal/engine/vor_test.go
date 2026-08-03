package engine

import "testing"

// vorBoard is a five-deep running back board with a receiver alongside, so the
// per-position ranking has something to get wrong.
func vorBoard() *State {
	s := newTestState(12, 15, 3)
	for i, v := range []int{100, 80, 60, 40, 20} {
		id := "rb" + string(rune('1'+i))
		s.Players[id] = Player{ID: id, Name: id, Pos: "RB", Value: v}
	}
	for i, v := range []int{90, 70} {
		id := "wr" + string(rune('1'+i))
		s.Players[id] = Player{ID: id, Name: id, Pos: "WR", Value: v}
	}
	return s
}

// Replacement is the value of the D_P-th best player at the position, and vor is
// what a player is worth above him. With D_RB = 3 the replacement back is worth
// 60, so the board's five backs are worth 40, 20, 0, 0, 0 over him — the bottom
// two are floored rather than going negative, which would sort their position
// below one with nobody left at all.
func TestReplacementAndVOR(t *testing.T) {
	s := vorBoard()
	s.Demand = map[string]int{"RB": 3, "WR": 1}

	cases := []struct {
		id   string
		want float64
	}{
		{"rb1", 40}, // 100 - 60
		{"rb2", 20},
		{"rb3", 0}, // he IS replacement level
		{"rb4", 0}, // below it, floored
		{"rb5", 0},
		{"wr1", 0}, // D_WR = 1: the best receiver is his own replacement
		{"wr2", 0}, // 70 - 90 = -20, floored
	}
	if got := s.Replacement("RB"); got != 60 {
		t.Errorf("replacement(rb) = %v, want 60", got)
	}
	if got := s.Replacement("WR"); got != 90 {
		t.Errorf("replacement(wr) = %v, want 90", got)
	}
	for _, c := range cases {
		if got := s.VOR(s.Players[c.id]); got != c.want {
			t.Errorf("vor(%s) = %v, want %v", c.id, got, c.want)
		}
	}
}

// The replacement level is a PRE-DRAFT number and must not move as the position
// empties. If it rose with every pick, waiting would make a position look
// scarcer and scarcer while the board it is measured against shrank — the
// opposite of what vor is for, and it would re-sort the board on every pick for
// no reason. Taken players stay in s.Players, so this falls out of ranking the
// whole map; the test is here because "don't filter by Taken" is one edit away
// from being helpfully broken.
//
// The second half is the other way a replacement level could drift: a live feed
// registering a player nobody ranked. He arrives with value 0 and must sort
// below everyone rather than displacing the D_P-th man.
func TestReplacementIsStaticThroughTheDraft(t *testing.T) {
	s := vorBoard()
	s.Demand = map[string]int{"RB": 3}
	before := s.Replacement("RB")

	s.Draft("rb1")
	s.Draft("rb2")
	if got := s.Replacement("RB"); got != before {
		t.Errorf("replacement(rb) moved to %v after two backs went; want %v", got, before)
	}
	s.EnsurePlayer(Player{ID: "handcuff", Name: "handcuff", Pos: "RB"})
	if got := s.Replacement("RB"); got != before {
		t.Errorf("replacement(rb) moved to %v after an unranked back registered; want %v", got, before)
	}
}

// The room drafts more backs than any source values, which is not a corner case:
// it drafts 58 and the 2026 board values 51. Replacement is then somebody worth
// nothing, so vor collapses to plain value — and specifically must not fall back
// to the shallowest valued player, which would hand every back a discount the
// depth of the position says he hasn't earned.
func TestReplacementWhenTheBoardRunsOut(t *testing.T) {
	s := vorBoard()
	s.Demand = map[string]int{"RB": 58}
	if got := s.Replacement("RB"); got != 0 {
		t.Errorf("replacement(rb) = %v with 58 drafted and 5 valued, want 0", got)
	}
	if got := s.VOR(s.Players["rb1"]); got != 100 {
		t.Errorf("vor(rb1) = %v, want his whole value of 100", got)
	}
}

// Without a measured demand table the fallback reads the league's own shape:
// every team's dedicated slots plus an equal share of each flex slot the
// position can fill. On the default 12-team lineup that is qb 12, rb and wr
// 24 + 12/3 = 28, te 12 + 4 = 16, k and def 12.
//
// For a flex position it is deliberately a floor and not a guess at the truth —
// measured, this room takes 58 backs, not 28 — because the alternative is a
// hardcoded constant pretending to be a measurement. A superflex lineup splits
// its extra slot four ways instead of three, which is the case a hardcoded
// table would miss.
func TestDemandFallsBackToLeagueShape(t *testing.T) {
	s := newTestState(12, 15, 3)
	cases := []struct {
		pos  string
		want int
	}{
		{"QB", 12},
		{"RB", 28},
		{"WR", 28},
		{"TE", 16},
		{"K", 12},
		{"DEF", 12},
	}
	for _, c := range cases {
		if got := s.demandAt(c.pos); got != c.want {
			t.Errorf("default lineup: demand(%s) = %d, want %d", c.pos, got, c.want)
		}
	}

	sf := newTestState(12, 15, 3)
	sf.SetRoster(Roster{Slots: []string{"QB", "RB", "RB", "WR", "WR", "TE", "SUPERFLEX", "K", "DEF"}, Bench: 6})
	if got := sf.demandAt("QB"); got != 12+3 {
		t.Errorf("superflex: demand(qb) = %d, want 15 — a quarter of the flex slot", got)
	}

	// A measured table wins where it has an entry AND the position's bench can
	// score, and a flex position it never saw still gets the shape fallback
	// rather than zero.
	s.Demand = map[string]int{"RB": 58}
	if got := s.demandAt("RB"); got != 58 {
		t.Errorf("measured demand(rb) = %d, want 58", got)
	}
	if got := s.demandAt("WR"); got != 28 {
		t.Errorf("unmeasured demand(wr) = %d, want the shape fallback 28", got)
	}
}

// The two-index rule itself: a position whose bench cannot score ignores the
// measured demand outright. This room drafts 19 quarterbacks, and 7 of them are
// bench arms that never start in a 1QB league — settling for the 19th QB is not
// a thing anyone does, so replacement sits at the 12th (the worst starter).
// Measured on the real 2026 board the difference is qb replacement 243 vs 1173:
// a ~930-point vor subsidy for early quarterbacks, which is exactly how the
// board kept recommending them.
//
// The rule reads the roster, not a position list: the same measured table in a
// superflex league flips QB back to the measured index, because there a second
// quarterback genuinely starts.
func TestReplacementIgnoresBenchDemandWhereBenchCannotScore(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.Demand = map[string]int{"QB": 19, "RB": 58, "K": 14, "DEF": 14}

	if got := s.demandAt("QB"); got != 12 {
		t.Errorf("1qb league: demand(qb) = %d, want 12 startable, not the 19 drafted", got)
	}
	if got := s.demandAt("K"); got != 12 {
		t.Errorf("demand(k) = %d, want 12 startable", got)
	}
	if got := s.demandAt("DEF"); got != 12 {
		t.Errorf("demand(def) = %d, want 12 startable", got)
	}
	if got := s.demandAt("RB"); got != 58 {
		t.Errorf("demand(rb) = %d, want the measured 58 — bench backs score", got)
	}

	sf := newTestState(12, 15, 3)
	sf.SetRoster(Roster{Slots: []string{"QB", "RB", "RB", "WR", "WR", "TE", "SUPERFLEX", "K", "DEF"}, Bench: 6})
	sf.Demand = map[string]int{"QB": 19}
	if got := sf.demandAt("QB"); got != 19 {
		t.Errorf("superflex: demand(qb) = %d, want the measured 19 — a second qb starts there", got)
	}
}
