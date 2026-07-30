package main

import (
	"math"
	"testing"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The shrink is the whole reason scout is more than a tally: one draft where
// somebody happened to take a tight end in round 2 must not read as a fact.
// Hand-computed against (count + 2*leagueRate) / (n + 2).
func TestShrunkFirstCDF(t *testing.T) {
	cases := []struct {
		name  string
		count []int
		n     int
		rate  []float64
		want  []float64
	}{
		{
			// one manager, one draft, first qb in round 2. his own evidence is
			// worth 1/3 and the room's 2/3, so round 2 reads .33 and not 1.0.
			"one draft leans on the room",
			[]int{0, 1, 1}, 1, []float64{0, 0.5, 1},
			[]float64{0, 2.0 / 3, 1},
		},
		{
			// three drafts, always round 1: 3/5 of the way to certain, which is
			// what "seen three times" should buy against a room that never does it.
			"three drafts still shrink",
			[]int{3, 3, 3}, 3, []float64{0, 0, 0},
			[]float64{0.6, 0.6, 0.6},
		},
		{
			// a manager we have never seen collapses to exactly the league rate.
			"no drafts is the room",
			[]int{0, 0, 0}, 0, []float64{0.1, 0.4, 0.9},
			[]float64{0.1, 0.4, 0.9},
		},
		{
			// a position nobody in the room has ever taken has no rate at all;
			// a nil prior must read as zero rather than panic on the index.
			"missing league rate is zero",
			[]int{0, 0, 1}, 1, nil,
			[]float64{0, 0, 1.0 / 3},
		},
	}
	for _, c := range cases {
		got := shrunkFirstCDF(c.count, c.n, c.rate)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %d rounds, want %d", c.name, len(got), len(c.want))
		}
		for i := range got {
			if math.Abs(got[i]-c.want[i]) > 1e-9 {
				t.Errorf("%s: round %d got %.4f, want %.4f", c.name, i+1, got[i], c.want[i])
			}
		}
		// A cdf that goes backwards would make expectedRound nonsense, and both
		// inputs are cumulative, so this can only break if the blend does.
		for i := 1; i < len(got); i++ {
			if got[i] < got[i-1]-1e-12 {
				t.Errorf("%s: cdf went backwards at round %d: %.4f then %.4f",
					c.name, i+1, got[i-1], got[i])
			}
		}
	}
}

// expectedRound is the number that actually reaches the table, so its two ends
// matter: a certainty must come back as the round itself, and "never took one"
// must land off the end of the draft rather than at zero, which would print as
// the earliest possible round — the exact opposite of the truth.
func TestExpectedRound(t *testing.T) {
	cases := []struct {
		name string
		cdf  []float64
		want float64
	}{
		{"always round 1", []float64{1, 1, 1}, 1},
		{"always round 3", []float64{0, 0, 1}, 3},
		{"never", []float64{0, 0, 0}, 4},
		{"half in round 2, half never", []float64{0, 0.5, 0.5}, 3},
	}
	for _, c := range cases {
		if got := expectedRound(c.cdf); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %.4f, want %.4f", c.name, got, c.want)
		}
	}
}

// scoutTally end to end on a two-manager, three-round draft, every number
// computed by hand above the table. m2 never takes a te, which is the case that
// has to survive the round trip: an absent first is not round zero.
func TestScoutTally(t *testing.T) {
	picks := []scoutPick{
		{Draft: "d1", User: "m1", Round: 1, Pos: "RB"},
		{Draft: "d1", User: "m2", Round: 1, Pos: "WR"},
		{Draft: "d1", User: "m1", Round: 2, Pos: "QB"},
		{Draft: "d1", User: "m2", Round: 2, Pos: "WR", Auto: true},
		{Draft: "d1", User: "m1", Round: 3, Pos: "TE"},
		{Draft: "d1", User: "m2", Round: 3, Pos: "QB"},
	}
	base, managers := scoutTally(picks, map[string]int{"d1": 3})

	if base.Picks != 6 || base.Managers != 2 || base.ManagerDrafts != 2 || base.Rounds != 3 {
		t.Fatalf("baseline: %d picks, %d managers, %d manager-drafts, %d rounds; want 6/2/2/3",
			base.Picks, base.Managers, base.ManagerDrafts, base.Rounds)
	}
	// qb: m1 by round 2, m2 by round 3, over two manager-drafts.
	wantQB := []float64{0, 0.5, 1}
	for i, w := range wantQB {
		if got := base.First["QB"].ByRound[i]; math.Abs(got-w) > 1e-9 {
			t.Errorf("league qb rate round %d: got %.4f, want %.4f", i+1, got, w)
		}
	}
	if got := base.First["QB"].Mean; math.Abs(got-2.5) > 1e-9 {
		t.Errorf("league mean first qb: got %.4f, want 2.5", got)
	}
	if got := base.ByRound[0].Pos["RB"]; got != 1 {
		t.Errorf("league round 1 rb count: got %d, want 1", got)
	}

	byUser := map[string]scoutManager{}
	for _, m := range managers {
		byUser[m.UserID] = m
	}
	m1, m2 := byUser["m1"], byUser["m2"]

	// m1 qb: count [0,1,1], n=1, rate [0,.5,1] -> [0, 2/3, 1]; e = 1 + 1 + 1/3.
	if got := m1.First["QB"].Expected; math.Abs(got-(1+1+1.0/3)) > 1e-9 {
		t.Errorf("m1 expected first qb: got %.4f, want %.4f", got, 1+1+1.0/3)
	}
	// m2 qb: count [0,0,1] -> [0, 1/3, 1]; e = 1 + 1 + 2/3.
	if got := m2.First["QB"].Expected; math.Abs(got-(1+1+2.0/3)) > 1e-9 {
		t.Errorf("m2 expected first qb: got %.4f, want %.4f", got, 1+1+2.0/3)
	}
	// m2 never took a te: his record is a zero, and the shrunk curve leaves him
	// at rounds+1 rather than pretending he took one.
	if got := m2.First["TE"].Rounds; len(got) != 1 || got[0] != 0 {
		t.Errorf("m2 te record: got %v, want [0]", got)
	}
	if got := m2.First["TE"].Mean; got != 0 {
		t.Errorf("m2 te mean: got %.4f, want 0 (he never took one)", got)
	}
	// count [0,0,0], n=1, rate [0,0,0.5] -> (0 + 2*rate)/3 = [0,0,1/3];
	// e = 1 + 1 + 1 + 2/3, i.e. later than the draft is long, which is right.
	if got := m2.First["TE"].Expected; math.Abs(got-(3+2.0/3)) > 1e-9 {
		t.Errorf("m2 expected first te: got %.4f, want %.4f", got, 3+2.0/3)
	}
	// the autodraft floor is one pick in three for m2 and zero for m1. it reads
	// 0.0 for everybody on the real drafts, which is measured and honest; this is
	// the only place the arithmetic itself gets checked.
	if math.Abs(m2.AutoFloor-1.0/3) > 1e-9 || m1.AutoFloor != 0 {
		t.Errorf("autodraft floor: m1 %.4f, m2 %.4f; want 0 and 1/3", m1.AutoFloor, m2.AutoFloor)
	}
	if base.AutoPicks != 1 {
		t.Errorf("league autodraft picks: got %d, want 1", base.AutoPicks)
	}
	if len(m1.ByRound) != 3 || m1.ByRound[2].Pos["TE"] != 1 {
		t.Errorf("m1 by-round: got %v", m1.ByRound)
	}
}

// A traded pick belongs to the roster that RECEIVES the player, not the seat
// that makes the selection — 17 of the 2024 draft's 180 picks were traded, and
// attributing by seat would file another man's tendencies under your leaguemate.
func TestScoutAttribute(t *testing.T) {
	d := &sleeper.Draft{
		DraftOrder:     map[string]int{"userA": 1, "userB": 2},
		SlotToRosterID: map[string]int{"1": 10, "2": 20},
	}
	picks := []sleeper.DraftPick{
		{PickNo: 1, Round: 1, DraftSlot: 1, RosterID: 10, PlayerID: "p1", PickedBy: "userA"},
		// slot 2 picks, but roster 10 receives: userA traded for it.
		{PickNo: 2, Round: 1, DraftSlot: 2, RosterID: 10, PlayerID: "p2", PickedBy: "userA"},
		// an autodrafted pick carries no picked_by; the seat still says whose it is.
		{PickNo: 3, Round: 2, DraftSlot: 2, RosterID: 20, PlayerID: "p3", PickedBy: ""},
		// a pick with no player is a hole in the feed, not a selection.
		{PickNo: 4, Round: 2, DraftSlot: 1, RosterID: 10, PlayerID: ""},
	}
	got := scoutAttribute(d, picks, "d1")
	if len(got) != 3 {
		t.Fatalf("got %d attributed picks, want 3", len(got))
	}
	cases := []struct {
		i    int
		user string
		auto bool
	}{
		{0, "userA", false},
		{1, "userA", false}, // by roster, not by slot
		{2, "userB", true},
	}
	for _, c := range cases {
		if got[c.i].User != c.user || got[c.i].Auto != c.auto {
			t.Errorf("pick %d: got user %s auto %v, want %s %v",
				c.i+1, got[c.i].User, got[c.i].Auto, c.user, c.auto)
		}
	}
}

// roundPick has to zero-pad. "5.2" next to "5.12" scans as the earlier pick and
// sorts wrong, which the board has already gotten wrong once.
func TestScoutRoundPick(t *testing.T) {
	cases := []struct {
		pick  float64
		teams int
		want  string
	}{
		{1, 12, "1.01"},
		{12, 12, "1.12"},
		{13, 12, "2.01"},
		{23.0, 12, "2.11"},   // adp_room(qb, 1), measured
		{117.7, 12, "10.10"}, // adp_room(k, 1): rounds to 118
		{0, 12, "1.01"},      // a curve that somehow reads zero must not go negative
		{25, 10, "3.05"},
	}
	for _, c := range cases {
		if got := roundPick(c.pick, c.teams); got != c.want {
			t.Errorf("roundPick(%.1f, %d): got %s, want %s", c.pick, c.teams, got, c.want)
		}
	}
}

// The modal league size decides how the room curve numbers its rounds. A single
// 10-team dynasty draft in the set must not renumber three 12-team drafts.
func TestScoutModalTeams(t *testing.T) {
	cases := []struct {
		name  string
		teams map[int]int
		want  int
	}{
		{"three twelves and one ten", map[int]int{12: 3, 10: 1}, 12},
		{"empty falls back to the league default", map[int]int{}, 12},
		{"a tie takes the smaller", map[int]int{10: 2, 14: 2}, 10},
		{"zero is not a league size", map[int]int{0: 9, 12: 1}, 12},
	}
	for _, c := range cases {
		if got := scoutModalTeams(c.teams); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
