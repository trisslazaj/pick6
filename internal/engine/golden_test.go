package engine

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// golden_test.go pins one mid-draft frame's numbers, digit for digit, under both
// survival models.
//
// WHY IT EXISTS. docs/fpl.md's f1 pass replaces planPositions, simPositions and
// the {K,DEF} suppression literal with derivations from the roster, and the
// invariant it has to defend above all is "NFL output is bit-identical after
// f1". Nothing in the suite guarded that. TestSimDeterminism compares two builds
// of the SAME state, so a change to the order the rollouts draw from the rng, to
// the derived position SET, or to a need weight passes it untouched — both sides
// move together. These tables have no other side to move with; verified by
// perturbing each of those three in a scratch copy of the package (16, 5 and 32
// rows go red respectively).
//
// One thing it deliberately does not catch, because there is nothing to catch:
// permuting simPositions alone changes no output at all. The index is used only
// to look need up in a parallel array and the plan policy's ties break on pool
// index, so the ordering is self-consistent whatever it is. That is the f1
// invariant holding rather than a hole in the table.
//
// WHAT IT PINS, on a fixed synthetic board at a fixed vantage (12 teams, 16
// rounds, my slot 6, standing at pick 37 with 36 picks already made), for
// Survival = sim AND Survival = adp:
//
//   - PSurviveTilted for every available player, to 6 decimal places
//   - PickChoices' order and every score
//   - the plan's legs, its band and its odds
//
// Everything the rollouts read is pinned with it — SimSeed, OffBoard, Demand,
// the room-warped ADPEff on the few players that carry one, the roster shape and
// the picks already spent — because a golden whose inputs drift is a golden that
// pins nothing.
//
// HOW TO RE-PIN, when a change to the math is deliberate:
//
//	PICK6_GOLDEN=1 go test ./internal/engine -run Golden
//
// prints all three tables as go literals; paste them over the ones below. Do it
// with a reason written down. A golden re-pinned every time it goes red is
// decoration.
//
// The players are invented and obviously so — no real name, price or ranking
// appears here, and none ever should.

// goldenGonePos is the position cycle the already-drafted filler runs through.
// Seven long against twelve seats, so the snake hands each seat a different mix
// and my own three picks come back qb / te / wr rather than the same position
// three times.
var goldenGonePos = []string{"RB", "WR", "WR", "RB", "TE", "QB", "WR"}

// goldenGone is how many picks are already in the book: three full rounds, so
// the vantage sits in round 4 where betaAt is 0.25 and the opponents' need
// genuinely prices their sampling.
const goldenGone = 36

// goldenOffBoard is the escape table, indexed by full rounds remaining after a
// pick's own round. The window this fixture opens spans rounds 4-6, i.e. indices
// 12, 11 and 10, so the escape draw really fires inside the rollouts rather than
// riding along as a zero.
var goldenOffBoard = []float64{
	0.72, 0.60, 0.50, 0.42, 0.35, 0.30, 0.26, 0.22,
	0.18, 0.15, 0.12, 0.10, 0.08, 0.05, 0.03, 0.02,
}

// goldenDemand is the measured draft demand vor indexes replacement at. Chosen
// so every flex-reachable position has more valued players than its demand and
// the replacement level actually bites: qb 1900, rb 2000, wr 2700, te 2100.
// (QB is not flex-reachable under this lineup, so its 19 is inert and indexes at
// startable slots instead — pinning the two-index rule as much as the numbers.)
var goldenDemand = map[string]int{"QB": 19, "RB": 20, "WR": 22, "TE": 10, "K": 13, "DEF": 13}

// goldenBoard is what is left on the board at the vantage: 40 players across all
// six positions, with fixed adp, sigma, value and tier. A few carry ADPEff — the
// room-warped price — because this league takes quarterbacks earlier than the
// national market does, and that field is one of exactly two things PSurviveAt
// reads.
var goldenBoard = []Player{
	{ID: "rb01", Name: "rb01", Pos: "RB", ADP: 37, Sigma: 3.0, Value: 3700, Tier: 3},
	{ID: "wr01", Name: "wr01", Pos: "WR", ADP: 38, Sigma: 3.2, Value: 3650, Tier: 3},
	{ID: "wr02", Name: "wr02", Pos: "WR", ADP: 39, Sigma: 3.4, Value: 3600, Tier: 3},
	{ID: "te01", Name: "te01", Pos: "TE", ADP: 40, Sigma: 2.8, Value: 3500, Tier: 2, ADPEff: 36},
	{ID: "rb02", Name: "rb02", Pos: "RB", ADP: 41, Sigma: 3.6, Value: 3450, Tier: 3},
	{ID: "qb01", Name: "qb01", Pos: "QB", ADP: 42, Sigma: 4.0, Value: 3400, Tier: 4, ADPEff: 34},
	{ID: "wr03", Name: "wr03", Pos: "WR", ADP: 43, Sigma: 3.8, Value: 3380, Tier: 3},
	{ID: "rb03", Name: "rb03", Pos: "RB", ADP: 44, Sigma: 4.2, Value: 3300, Tier: 4},
	{ID: "wr04", Name: "wr04", Pos: "WR", ADP: 45, Sigma: 4.4, Value: 3250, Tier: 4},
	{ID: "te02", Name: "te02", Pos: "TE", ADP: 46, Sigma: 3.0, Value: 3150, Tier: 2},
	{ID: "qb02", Name: "qb02", Pos: "QB", ADP: 47, Sigma: 4.6, Value: 3100, Tier: 4, ADPEff: 39},
	{ID: "rb04", Name: "rb04", Pos: "RB", ADP: 48, Sigma: 4.8, Value: 3050, Tier: 4},
	{ID: "wr05", Name: "wr05", Pos: "WR", ADP: 49, Sigma: 5.0, Value: 3000, Tier: 4},
	{ID: "wr06", Name: "wr06", Pos: "WR", ADP: 50, Sigma: 5.2, Value: 2950, Tier: 4},
	{ID: "rb05", Name: "rb05", Pos: "RB", ADP: 51, Sigma: 5.4, Value: 2900, Tier: 4},
	{ID: "te03", Name: "te03", Pos: "TE", ADP: 52, Sigma: 5.6, Value: 2800, Tier: 3},
	{ID: "qb03", Name: "qb03", Pos: "QB", ADP: 53, Sigma: 5.8, Value: 2750, Tier: 5, ADPEff: 45},
	{ID: "wr07", Name: "wr07", Pos: "WR", ADP: 54, Sigma: 6.0, Value: 2700, Tier: 5},
	{ID: "rb06", Name: "rb06", Pos: "RB", ADP: 55, Sigma: 6.2, Value: 2650, Tier: 5},
	{ID: "wr08", Name: "wr08", Pos: "WR", ADP: 56, Sigma: 6.4, Value: 2600, Tier: 5},
	{ID: "qb04", Name: "qb04", Pos: "QB", ADP: 57, Sigma: 6.6, Value: 2500, Tier: 5},
	{ID: "te04", Name: "te04", Pos: "TE", ADP: 58, Sigma: 6.8, Value: 2450, Tier: 3},
	{ID: "rb07", Name: "rb07", Pos: "RB", ADP: 59, Sigma: 7.0, Value: 2400, Tier: 5},
	{ID: "wr09", Name: "wr09", Pos: "WR", ADP: 60, Sigma: 7.2, Value: 2350, Tier: 5},
	{ID: "wr10", Name: "wr10", Pos: "WR", ADP: 61, Sigma: 7.4, Value: 2300, Tier: 6},
	{ID: "rb08", Name: "rb08", Pos: "RB", ADP: 62, Sigma: 7.6, Value: 2250, Tier: 6},
	{ID: "qb05", Name: "qb05", Pos: "QB", ADP: 63, Sigma: 7.8, Value: 2200, Tier: 6},
	{ID: "te05", Name: "te05", Pos: "TE", ADP: 64, Sigma: 8.0, Value: 2100, Tier: 4},
	{ID: "wr11", Name: "wr11", Pos: "WR", ADP: 65, Sigma: 8.2, Value: 2050, Tier: 6},
	{ID: "rb09", Name: "rb09", Pos: "RB", ADP: 66, Sigma: 8.4, Value: 2000, Tier: 6},
	{ID: "qb06", Name: "qb06", Pos: "QB", ADP: 67, Sigma: 8.6, Value: 1950, Tier: 6},
	{ID: "qb07", Name: "qb07", Pos: "QB", ADP: 68, Sigma: 8.8, Value: 1900, Tier: 6},
	{ID: "te06", Name: "te06", Pos: "TE", ADP: 69, Sigma: 9.0, Value: 1850, Tier: 4},
	{ID: "rb10", Name: "rb10", Pos: "RB", ADP: 70, Sigma: 9.2, Value: 1800, Tier: 6},
	{ID: "def01", Name: "def01", Pos: "DEF", ADP: 71, Sigma: 10.0, Value: 950},
	{ID: "def02", Name: "def02", Pos: "DEF", ADP: 72, Sigma: 10.5, Value: 900},
	{ID: "def03", Name: "def03", Pos: "DEF", ADP: 73, Sigma: 11.0, Value: 850},
	{ID: "k01", Name: "k01", Pos: "K", ADP: 74, Sigma: 10.0, Value: 900},
	{ID: "k02", Name: "k02", Pos: "K", ADP: 75, Sigma: 10.5, Value: 850},
	{ID: "k03", Name: "k03", Pos: "K", ADP: 76, Sigma: 11.0, Value: 800},
}

// goldenState builds the fixture. Everything the two brains read is set here and
// nothing is left to a default that a later pass could quietly change.
func goldenState(survival string) *State {
	s := New(map[string]Player{}, 12, 16, 6)
	s.Roster = DefaultRoster
	s.Survival = survival
	s.Scorer = ScorerPair
	s.SimSeed = 20260817
	s.Demand = goldenDemand
	s.OffBoard = goldenOffBoard
	// The picks already made, spent through the real snake so Rosters, Picks and
	// PickNo agree with each other rather than being posed.
	for i := 0; i < goldenGone; i++ {
		id := fmt.Sprintf("gone%02d", i+1)
		s.Players[id] = Player{
			ID: id, Name: id, Pos: goldenGonePos[i%len(goldenGonePos)],
			ADP: float64(i + 1), Sigma: 2, Value: 9000 - 150*i, Tier: i/6 + 1,
		}
		s.Draft(id)
	}
	for _, p := range goldenBoard {
		s.Players[p.ID] = p
	}
	return s
}

// goldenIDs is every available player, in id order, so the table below has a
// stable shape independent of map iteration.
func goldenIDs(s *State) []string {
	ids := make([]string, 0, len(s.Players))
	for id := range s.Players {
		if !s.Taken[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// same compares at the six decimal places the goldens are written to. The engine
// is deterministic well past that; six is what a human can read off a diff.
func sameGolden(a, b float64) bool { return fmt.Sprintf("%.6f", a) == fmt.Sprintf("%.6f", b) }

func goldenRepin() bool { return os.Getenv("PICK6_GOLDEN") != "" }

// The fixture's own shape. If one of these moves, every number below is about a
// different board and the failures underneath are noise — so this fails first
// and says so.
func TestGoldenFixtureShape(t *testing.T) {
	s := goldenState(SurvivalSim)
	if got := len(s.Players); got != goldenGone+len(goldenBoard) {
		t.Fatalf("board size = %d, want %d", got, goldenGone+len(goldenBoard))
	}
	if got := len(goldenIDs(s)); got != len(goldenBoard) {
		t.Fatalf("available = %d, want %d", got, len(goldenBoard))
	}
	if s.PickNo != 37 || s.NextPick() != 43 || s.FollowingPick() != 54 {
		t.Fatalf("vantage = pick %d, next %d, following %d; want 37/43/54",
			s.PickNo, s.NextPick(), s.FollowingPick())
	}
	if got := s.Round(s.PickNo); got != 4 {
		t.Fatalf("round = %d, want 4 (where betaAt is 0.25)", got)
	}
	var mine []string
	for _, id := range s.Rosters[s.MySlot] {
		mine = append(mine, s.Players[id].Pos)
	}
	if fmt.Sprint(mine) != "[QB TE WR]" {
		t.Fatalf("my roster = %v, want [QB TE WR]", mine)
	}
	for pos, want := range map[string]float64{"QB": 1900, "RB": 2000, "WR": 2700, "TE": 2100, "K": 0, "DEF": 0} {
		if got := s.Replacement(pos); got != want {
			t.Errorf("replacement(%s) = %v, want %v", pos, got, want)
		}
	}
}

// (a) every available player's survival, both brains.
var goldenSurvival = []struct {
	id       string
	sim, adp float64
}{
	{"def01", 0.999002, 0.982077},
	{"def02", 0.999002, 0.982077},
	{"def03", 0.999002, 0.982117},
	{"k01", 0.999002, 0.986536},
	{"k02", 0.999002, 0.986343},
	{"k03", 0.999002, 0.986196},
	{"qb01", 0.352295, 0.432828},
	{"qb02", 0.513972, 0.608225},
	{"qb03", 0.797405, 0.806840},
	{"qb04", 0.979042, 0.955433},
	{"qb05", 0.993014, 0.973364},
	{"qb06", 0.999002, 0.979862},
	{"qb07", 0.999002, 0.981104},
	{"rb01", 0.384232, 0.371740},
	{"rb02", 0.665669, 0.606533},
	{"rb03", 0.711577, 0.754498},
	{"rb04", 0.839321, 0.867518},
	{"rb05", 0.935130, 0.912509},
	{"rb06", 0.965070, 0.945339},
	{"rb07", 0.985030, 0.963016},
	{"rb08", 0.989022, 0.971232},
	{"rb09", 0.997006, 0.978489},
	{"rb10", 0.999002, 0.983252},
	{"te01", 0.400200, 0.311205},
	{"te02", 0.815369, 0.833041},
	{"te03", 0.951098, 0.922871},
	{"te04", 0.979042, 0.959485},
	{"te05", 0.993014, 0.975264},
	{"te06", 0.999002, 0.982229},
	{"wr01", 0.513972, 0.435703},
	{"wr02", 0.563872, 0.500252},
	{"wr03", 0.671657, 0.705388},
	{"wr04", 0.791417, 0.790260},
	{"wr05", 0.881238, 0.885344},
	{"wr06", 0.903194, 0.900153},
	{"wr07", 0.941118, 0.939019},
	{"wr08", 0.971058, 0.950759},
	{"wr09", 0.981038, 0.966108},
	{"wr10", 0.987026, 0.968828},
	{"wr11", 0.997006, 0.976964},
}

func TestGoldenSurvival(t *testing.T) {
	sim, adp := goldenState(SurvivalSim), goldenState(SurvivalADP)
	ids := goldenIDs(sim)

	if goldenRepin() {
		fmt.Println("var goldenSurvival = []struct {")
		fmt.Println("\tid       string")
		fmt.Println("\tsim, adp float64")
		fmt.Println("}{")
		for _, id := range ids {
			fmt.Printf("\t{%q, %.6f, %.6f},\n",
				id, sim.PSurviveTilted(sim.Players[id]), adp.PSurviveTilted(adp.Players[id]))
		}
		fmt.Println("}")
	}

	if len(goldenSurvival) != len(ids) {
		t.Fatalf("golden table has %d rows, board has %d available", len(goldenSurvival), len(ids))
	}
	for i, want := range goldenSurvival {
		if ids[i] != want.id {
			t.Fatalf("row %d is %s, golden says %s", i, ids[i], want.id)
		}
		if got := sim.PSurviveTilted(sim.Players[want.id]); !sameGolden(got, want.sim) {
			t.Errorf("sim survival %s = %.6f, want %.6f", want.id, got, want.sim)
		}
		if got := adp.PSurviveTilted(adp.Players[want.id]); !sameGolden(got, want.adp) {
			t.Errorf("adp survival %s = %.6f, want %.6f", want.id, got, want.adp)
		}
	}
}

// (b) the ranking: every choice's position and score, in order.
var goldenChoicesSim = []struct {
	pos   string
	score float64
}{
	{"RB", 2690.120000},
	{"WR", 1968.868000},
	{"TE", 1852.960000},
	{"QB", 1397.700000},
}

var goldenChoicesADP = []struct {
	pos   string
	score float64
}{
	{"RB", 2729.627063},
	{"WR", 2008.288458},
	{"TE", 1898.288458},
	{"QB", 1433.288458},
}

func TestGoldenChoices(t *testing.T) {
	for _, c := range []struct {
		mode string
		want []struct {
			pos   string
			score float64
		}
	}{
		{SurvivalSim, goldenChoicesSim},
		{SurvivalADP, goldenChoicesADP},
	} {
		s := goldenState(c.mode)
		got := s.PickChoices()

		if goldenRepin() {
			fmt.Printf("// %s\n{\n", c.mode)
			for _, ch := range got {
				fmt.Printf("\t{%q, %.6f},\n", ch.Pos, ch.Score)
			}
			fmt.Println("}")
		}

		if len(got) != len(c.want) {
			t.Fatalf("%s: %d choices, want %d", c.mode, len(got), len(c.want))
		}
		for i, want := range c.want {
			if got[i].Pos != want.pos {
				t.Errorf("%s: choice %d is %s, want %s", c.mode, i, got[i].Pos, want.pos)
			}
			if !sameGolden(got[i].Score, want.score) {
				t.Errorf("%s: choice %d (%s) score = %.6f, want %.6f",
					c.mode, i, got[i].Pos, got[i].Score, want.score)
			}
		}
	}
}

// (c) the plan. Under sim the legs come out of the conditioned rollouts and
// carry a band and its odds; under adp the same two positions come out of the
// formula and there are no futures to summarise, so Legs is nil by design.
//
// Two shapes worth naming rather than leaving a reader to wonder at: there are
// exactly two legs because ScorerPair plans two (the full horizon is the roster
// scorer's, which is off), and leg zero's position is the empty string because
// leg one is the candidate himself and summarise records no draw for it.
func TestGoldenPlan(t *testing.T) {
	cases := []struct {
		mode       string
		first      string
		second     string
		legs       []string
		tier       int
		odds       float64
		wantNoLegs bool
	}{
		{
			mode: SurvivalSim, first: "RB", second: "RB",
			legs: []string{"", "RB"}, tier: 4, odds: 0.716000,
		},
		{
			mode: SurvivalADP, first: "RB", second: "RB",
			wantNoLegs: true,
		},
	}
	for _, c := range cases {
		s := goldenState(c.mode)
		p, ok := s.BestPlan()
		if !ok {
			t.Fatalf("%s: no plan", c.mode)
		}

		if goldenRepin() {
			var legs []string
			for _, l := range p.Legs {
				legs = append(legs, l.Pos)
			}
			fmt.Printf("// %s: first %q second %q legs %q tier %d odds %.6f\n",
				c.mode, p.First, p.Second, legs, p.SecondTier, p.SecondOdds)
		}

		if p.First != c.first || p.Second != c.second {
			t.Errorf("%s: plan = %s then %s, want %s then %s", c.mode, p.First, p.Second, c.first, c.second)
		}
		if p.FirstPick != 43 || p.SecondPick != 54 {
			t.Errorf("%s: plan picks = %d, %d; want 43, 54", c.mode, p.FirstPick, p.SecondPick)
		}
		if c.wantNoLegs {
			if p.Legs != nil {
				t.Errorf("%s: legs = %v, want nil — the formula has no futures to summarise", c.mode, p.Legs)
			}
			if p.SecondOdds != 0 {
				t.Errorf("%s: odds = %v, want 0", c.mode, p.SecondOdds)
			}
			continue
		}
		if len(p.Legs) != len(c.legs) {
			t.Fatalf("%s: %d legs, want %d", c.mode, len(p.Legs), len(c.legs))
		}
		for i, want := range c.legs {
			if p.Legs[i].Pos != want {
				t.Errorf("%s: leg %d = %q, want %q", c.mode, i, p.Legs[i].Pos, want)
			}
		}
		if p.SecondTier != c.tier {
			t.Errorf("%s: second tier = %d, want %d", c.mode, p.SecondTier, c.tier)
		}
		if !sameGolden(p.SecondOdds, c.odds) {
			t.Errorf("%s: second odds = %.6f, want %.6f", c.mode, p.SecondOdds, c.odds)
		}
	}
}
