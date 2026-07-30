package engine

import "testing"

// endgameState puts me at pick 147 — round 13 of 15, slot 3 — where my only
// remaining picks are 147, 166 and 171. R is therefore exactly 3, and the roster
// handed in decides U.
//
// Round 13 matters twice over: RoundsRemaining is 3, so K and DEF have come out
// of suppression and can be starting slots that actually need filling, which is
// the whole situation the guard exists for.
func endgameState(roster []string) *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 147
	for i, pos := range roster {
		id := "mine" + string(rune('a'+i))
		s.Players[id] = Player{ID: id, Name: id, Pos: pos, Value: 500}
		s.Taken[id] = true
		s.Rosters[3] = append(s.Rosters[3], id)
	}
	return s
}

// The three regimes, plus the flex case that a naive implementation gets wrong.
//
// The naive version asks "is this position in UnfilledStarters?" — and
// UnfilledStarters returns SLOT names. With only FLEX, K and DEF open, "RB" is
// not in that list, so the naive check zeroes running backs at the exact moment
// a running back is one of the three players I must still draft. FLEX is a slot,
// not a position. needFrom sidesteps it by construction: the flex branch has
// already returned NeedFlex for anyone eligible, so the guard only ever sees
// positions that fill nothing.
//
// R = 3 at every row (picks 147, 166, 171); the roster sets U.
func TestEndgameFeasibilityGuard(t *testing.T) {
	// U = 3: FLEX, K and DEF open. Every pick is spoken for.
	locked := []string{"QB", "RB", "RB", "WR", "WR", "TE"}
	// U = 2: K and DEF open, the flex slot taken by a third back. One spare pick.
	slack := []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB"}
	// U = 4: TE, FLEX, K and DEF open against three picks. Already lost.
	lost := []string{"QB", "RB", "RB", "WR", "WR"}

	cases := []struct {
		name   string
		roster []string
		pos    string
		want   float64
	}{
		// R == U. A position that fills nothing is worth nothing.
		{"locked, bench position", locked, "QB", 0},
		{"locked, open starter", locked, "K", NeedStarter},
		// ...and the flex case: rb fills the open FLEX slot, so it keeps flex
		// weight. Zeroing it here is the bug this row exists for.
		{"locked, flex-eligible", locked, "RB", NeedFlex},
		{"locked, flex-eligible te", locked, "TE", NeedFlex},

		// R == U+1. One spare pick, so a bench flier is half a pick's worth.
		{"one spare, bench position", slack, "QB", NeedBench * EndgameSlack},
		{"one spare, open starter", slack, "DEF", NeedStarter},
		{"one spare, flex filled", slack, "RB", NeedBench * EndgameSlack},

		// R < U. Already lost: nothing is suppressed, because the hole cannot be
		// filled and burying the rest of the board over it helps nobody.
		{"already lost, bench position", lost, "QB", NeedBench},
		{"already lost, open starter", lost, "TE", NeedStarter},
	}
	for _, c := range cases {
		s := endgameState(c.roster)
		if got := s.Need(c.pos); got != c.want {
			t.Errorf("%s: need(%s) = %v, want %v", c.name, c.pos, got, c.want)
		}
	}

	// Early in the same draft none of this applies: fifteen picks against nine
	// slots is not an endgame, and a bench pick is a plain bench pick.
	early := endgameState(locked)
	early.PickNo = 1
	if got := early.Need("QB"); got != NeedBench {
		t.Errorf("round 1: need(qb) = %v, want the plain bench weight %v", got, NeedBench)
	}
}

// MustFillStarters is the one thing the board renders about all this, so it has
// to be true in exactly one regime. R < U is not it: telling somebody every pick
// must fill a starter when there are more holes than picks is both false and
// useless. A finished draft falls out of the same test — R is 0, U is not.
func TestMustFillStarters(t *testing.T) {
	cases := []struct {
		name   string
		roster []string
		pickNo int
		want   bool
	}{
		{"three picks, three holes", []string{"QB", "RB", "RB", "WR", "WR", "TE"}, 147, true},
		{"three picks, two holes", []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB"}, 147, false},
		{"three picks, four holes", []string{"QB", "RB", "RB", "WR", "WR"}, 147, false},
		// Two picks left (166, 171) against the same three holes: past saving.
		{"two picks, three holes", []string{"QB", "RB", "RB", "WR", "WR", "TE"}, 148, false},
		// One pick left (171) and one hole.
		{"one pick, one hole",
			[]string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "K"}, 167, true},
		// A complete lineup is never in the endgame however few picks remain.
		{"lineup complete",
			[]string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "K", "DEF"}, 167, false},
		// The draft is over; there is nothing left to fill anything with.
		{"draft done", []string{"QB", "RB", "RB", "WR", "WR", "TE"}, 181, false},
	}
	for _, c := range cases {
		s := endgameState(c.roster)
		s.PickNo = c.pickNo
		if got := s.MustFillStarters(); got != c.want {
			t.Errorf("%s: MustFillStarters = %v, want %v (picks left %d, unfilled %d)",
				c.name, got, c.want, s.MyPicksLeft(), len(s.UnfilledStarters(3)))
		}
	}
}
