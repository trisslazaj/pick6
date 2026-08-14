package engine

import (
	"math"
	"testing"
)

// U's table tests. The objective is the one piece of milestone 8 that has an
// exact right answer for every input, so it gets exact assertions and the
// rollouts around it get scenarios.

// rv builds a state with a given lineup shape and a roster of (id, pos, value).
func rv(slots []string, bench int, roster ...Player) (*State, []string) {
	s := newTestState(12, len(slots)+bench, 1)
	s.Roster = Roster{Slots: slots, Bench: bench}
	ids := make([]string, 0, len(roster))
	for _, p := range roster {
		s.Players[p.ID] = p
		ids = append(ids, p.ID)
	}
	return s, ids
}

func p(id, pos string, value int) Player {
	return Player{ID: id, Pos: pos, Value: value}
}

var (
	oneFlex = []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "K", "DEF"}
	twoFlex = []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "FLEX", "K", "DEF"}
	superFl = []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "SUPERFLEX", "K", "DEF"}
)

func TestRosterValue(t *testing.T) {
	cases := []struct {
		name  string
		slots []string
		bench int
		ids   []Player
		want  float64
	}{
		{
			// Nothing drafted: every slot unfilled, and an unfilled slot is
			// worth nothing. This is the endgame guard as a price rather than
			// a guard, at its most extreme.
			name: "empty roster", slots: oneFlex, bench: 6,
			want: 0,
		},
		{
			// The straight case: one man per dedicated slot, all at full value.
			name: "dedicated slots only", slots: oneFlex, bench: 6,
			ids: []Player{
				p("qb", "QB", 1000), p("rb1", "RB", 900), p("rb2", "RB", 800),
				p("wr1", "WR", 700), p("wr2", "WR", 600), p("te", "TE", 500),
			},
			want: 4500,
		},
		{
			// The flex is worth exactly the man in it — 400, not 0.6 × 400,
			// and not a guess. This is the NeedFlex retirement in one row.
			name: "flex takes the spare", slots: oneFlex, bench: 6,
			ids: []Player{
				p("rb1", "RB", 900), p("rb2", "RB", 800), p("rb3", "RB", 400),
			},
			want: 900 + 800 + 400,
		},
		{
			// Four backs, three lineup spots (rb, rb, flex): the fourth is
			// bench at BenchWeight, and it is the LOWEST-valued one who rides
			// it however the draft order ran. fillSlots over draft order would
			// bench rb4 only by luck; U sorts first.
			name: "surplus benches the cheapest", slots: oneFlex, bench: 6,
			ids: []Player{
				p("rb4", "RB", 100), p("rb1", "RB", 900), p("rb2", "RB", 800), p("rb3", "RB", 400),
			},
			want: 900 + 800 + 400 + 0.25*100,
		},
		{
			// The user's real 2025 shape. Two flex slots take the two best
			// spare flex-eligible men, and the te who cannot start anywhere
			// else lands in one of them at full value.
			name: "two flex slots", slots: twoFlex, bench: 6,
			ids: []Player{
				p("rb1", "RB", 900), p("rb2", "RB", 800), p("rb3", "RB", 700),
				p("wr1", "WR", 600), p("wr2", "WR", 500), p("te1", "TE", 450), p("te2", "TE", 400),
			},
			want: 900 + 800 + 600 + 500 + 450 + 700 + 400,
		},
		{
			// Superflex: the second quarterback starts, which is the whole
			// reason the slot exists, and he is worth his value and not a
			// bench discount.
			name: "superflex starts the second qb", slots: superFl, bench: 6,
			ids: []Player{
				p("qb1", "QB", 1000), p("qb2", "QB", 950),
				p("rb1", "RB", 900), p("rb2", "RB", 800),
			},
			want: 1000 + 950 + 900 + 800,
		},
		{
			// A dedicated slot is filled before a flex, so a tight end never
			// squats on the flex while the te slot sits open. Without that
			// ordering this roster scores 700 + 0.25×650 instead of 1350.
			name: "dedicated before flex", slots: oneFlex, bench: 6,
			ids:  []Player{p("te1", "TE", 700), p("te2", "TE", 650)},
			want: 700 + 650,
		},
		{
			// A player no source ranked contributes nothing through either
			// path — the same blindness vor has, stated as a test so nobody
			// reads a zero-value starter as a bug.
			name: "value-0 players", slots: oneFlex, bench: 6,
			ids:  []Player{p("k", "K", 0), p("handcuff", "RB", 0), p("wr1", "WR", 500)},
			want: 500,
		},
		{
			// Kicker and defense are starters like any other. U has no opinion
			// about when you take them, only about whether the slot is full.
			name: "k and def fill slots", slots: oneFlex, bench: 6,
			ids:  []Player{p("k", "K", 300), p("def", "DEF", 250)},
			want: 550,
		},
		{
			// Everything past the lineup is bench, including past the declared
			// bench size — a roster longer than the draft cannot happen, and
			// pricing the overflow at bench weight beats pretending it is free.
			name: "bench discount", slots: []string{"RB", "FLEX"}, bench: 1,
			ids:  []Player{p("rb1", "RB", 1000), p("rb2", "RB", 800), p("rb3", "RB", 400), p("rb4", "RB", 200)},
			want: 1000 + 800 + 0.25*400 + 0.25*200,
		},
		{
			// A backup quarterback in a 1QB league scores zero by construction —
			// vor.go's startable index already says so, and U says it too or the
			// board recommends a second one for a quarter of an inflated value.
			name: "the backup qb is worth nothing", slots: oneFlex, bench: 6,
			ids:  []Player{p("qb1", "QB", 1000), p("qb2", "QB", 900), p("k", "K", 300), p("k2", "K", 250)},
			want: 1000 + 300,
		},
		{
			// ...and under superflex he starts, so he is worth his value. Same
			// rule, read off the roster, answering differently by itself.
			name: "superflex pays for the second qb", slots: superFl, bench: 6,
			ids:  []Player{p("qb1", "QB", 1000), p("qb2", "QB", 900), p("qb3", "QB", 800)},
			want: 1000 + 900 + 0.25*800,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ids := rv(c.slots, c.bench, c.ids...)
			if got := s.RosterValue(ids); math.Abs(got-c.want) > 1e-9 {
				t.Errorf("RosterValue = %.4f, want %.4f", got, c.want)
			}
		})
	}
}

// The claim RosterValue's comment makes about diverging from FilledSlots: the
// two never disagree about WHICH slots are filled, only about who fills them.
// If that ever stopped being true, need and U would be describing different
// rosters on the same frame.
func TestRosterValueFillsTheSameSlotsAsTheRosterPane(t *testing.T) {
	s, ids := rv(twoFlex, 6,
		p("rb4", "RB", 10), p("rb1", "RB", 900), p("wr1", "WR", 600),
		p("rb2", "RB", 800), p("te1", "TE", 450), p("rb3", "RB", 700),
	)
	pane, _ := s.fillSlots(ids)
	u, _ := s.RosterLineup(ids)
	for i := range pane {
		if (pane[i] == "") != (u[i] == "") {
			t.Errorf("slot %d (%s): pane filled=%v, U filled=%v", i, twoFlex[i], pane[i] != "", u[i] != "")
		}
	}
	// ...and it really is a different assignment, or the test above is vacuous:
	// the pane's dedicated rb slots take rb4 because he was drafted first, so
	// its flex gets rb2 while U's gets rb3.
	const flex1 = 6
	if pane[flex1] != "rb2" || u[flex1] != "rb3" {
		t.Errorf("first flex: pane %q, U %q — want rb2 and rb3, the two orderings disagreeing",
			pane[flex1], u[flex1])
	}
}

// U is order-independent. The rollouts hand it rosters built pick by pick and
// the roster pane hands it one built by a human; a score that depended on the
// order would make two frames disagree about the same team.
func TestRosterValueIgnoresDraftOrder(t *testing.T) {
	forward := []Player{
		p("rb1", "RB", 900), p("wr1", "WR", 800), p("te1", "TE", 700),
		p("rb2", "RB", 600), p("wr2", "WR", 500), p("rb3", "RB", 400),
	}
	reversed := make([]Player, len(forward))
	for i, q := range forward {
		reversed[len(forward)-1-i] = q
	}
	a, aids := rv(oneFlex, 6, forward...)
	b, bids := rv(oneFlex, 6, reversed...)
	if av, bv := a.RosterValue(aids), b.RosterValue(bids); math.Abs(av-bv) > 1e-9 {
		t.Errorf("same multiset scored %.2f forward and %.2f reversed", av, bv)
	}
}
