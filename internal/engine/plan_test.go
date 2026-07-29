package engine

import (
	"math"
	"reflect"
	"testing"
)

// survivorAt is expect_test.go's survivor for an arbitrary horizon: the second
// leg of a plan prices survival to FollowingPick, not to NextPick, so the
// closed-form sigma has to be solved against that gap or nothing on the board is
// hand-computable. Same rule as ever — the tilt is global, so sum(1 - p) over
// every available player must equal the intervening opponent picks or the solve
// moves all of these numbers and the expectations below mean nothing.
func survivorAt(s *State, id, pos string, tier, value, at int, p float64) {
	s.Players[id] = Player{
		ID: id, Name: id, Pos: pos, Tier: tier, Value: value,
		ADP: float64(s.PickNo), Sigma: survivalSigma(at-s.PickNo, p),
	}
}

// lookaheadBoard is the disagreement fixture. 4 teams, slot 2, standing at pick
// 1: my picks are 2 and 7, so five opponent picks fall between them and the
// second leg's tilt is a no-op — sum(1 - p) over the eight available players is
// 0.1 + 0.1 + 0.95 + 0.95 + 4(0.725) = 5.
//
// The rbs hold to pick 7 at 90%; the wrs are 5% to last; the tes are cheap
// filler that carry the rest of the removals so the tilt has nothing to correct.
func lookaheadBoard() *State {
	s := newTestState(4, 15, 2)
	s.PickNo = 1 // NextPick 2, FollowingPick 7
	q2 := s.FollowingPick()
	survivorAt(s, "rb1", "RB", 1, 100, q2, 0.9)
	survivorAt(s, "rb2", "RB", 1, 80, q2, 0.9)
	survivorAt(s, "wr1", "WR", 1, 90, q2, 0.05)
	survivorAt(s, "wr2", "WR", 1, 70, q2, 0.05)
	for i := 0; i < 4; i++ {
		survivorAt(s, "te"+string(rune('1'+i)), "TE", 1, 10, q2, 0.275)
	}
	return s
}

// THE REASON THIS FEATURE EXISTS. The greedy board reads the clock and points at
// the rb: he is the most valuable man available and rb is an open starter. But
// the rbs will still be there at my second pick and the wrs will not, so taking
// the wr first collects both, and taking the rb first collects the rb and
// whatever is left of a gutted wr position.
//
// Hand-computed at the fixture's probabilities, with every need 1.0 (an empty
// roster leaves rb, wr and te starters all open):
//
//	EBest(rb, 7) = 100(.9) + 80(.1)(.9)  = 97.2
//	EBest(wr, 7) = 90(.05) + 70(.95)(.05) = 7.825
//	score(wr, rb) = 90 + 97.2  = 187.2   <- the plan
//	score(rb, rb) = 100 + 72   = 172     (72 is the pool with rb1 removed)
//	score(rb, wr) = 100 + 7.825 = 107.825
//
// so the lookahead says wr-then-rb while value x need says rb, which is the
// whole disagreement in one board.
func TestBestPlanBeatsGreedyWhenATierEvaporates(t *testing.T) {
	s := lookaheadBoard()
	q2 := s.FollowingPick()

	// The greedy read, stated explicitly so the disagreement is pinned rather
	// than assumed: rb tops the board on value x need.
	rb, _ := s.BestNow("RB")
	wr, _ := s.BestNow("WR")
	if float64(rb.Value)*s.Need("RB") <= float64(wr.Value)*s.Need("WR") {
		t.Fatalf("fixture is not a disagreement: rb %v does not lead wr %v on value x need",
			float64(rb.Value)*s.Need("RB"), float64(wr.Value)*s.Need("WR"))
	}
	// And the second-leg expectations the plan is built from.
	if got := s.EBest("RB", q2); math.Abs(got-97.2) > 1e-3 {
		t.Errorf("EBest(rb, %d) = %v, want 97.2", q2, got)
	}
	if got := s.EBest("WR", q2); math.Abs(got-7.825) > 1e-3 {
		t.Errorf("EBest(wr, %d) = %v, want 7.825", q2, got)
	}

	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan on a board with three live positions and two picks to come")
	}
	if plan.First != "WR" || plan.Second != "RB" {
		t.Errorf("plan = %s then %s, want wr then rb", plan.First, plan.Second)
	}
	if math.Abs(plan.Score-187.2) > 1e-3 {
		t.Errorf("plan score = %v, want 187.2", plan.Score)
	}
	// Both legs render by pick number, never as "now" — the plan is computed
	// as-if standing at NextPick but drawn on every frame.
	if plan.FirstPick != 2 || plan.SecondPick != 7 {
		t.Errorf("plan picks = %d then %d, want 2 then 7", plan.FirstPick, plan.SecondPick)
	}
}

// At the turn my two picks are adjacent, so double-tapping one position is the
// obvious plan — and the only thing that makes it priced correctly is excluding
// the man the first leg takes. 12 teams, slot 12, standing at pick 11: my picks
// are 12 and 13 with exactly one opponent pick between them, and four rbs at 75%
// carry exactly that one removal, so the tilt is a no-op.
//
//	ebest(rb, 13, rb1) = 90(.75) + 80(.25)(.75) + 70(.0625)(.75) = 85.78125
//	score(rb, rb)      = 100 + 85.78125 = 185.78125
//
// Leave rb1 in the pool and the second leg happily plans to take him twice, for
// 196.445 — a plan that is not only wrong but flatters itself by ten points.
func TestBestPlanExcludesTheFirstLegsPlayerAtTheTurn(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 11 // NextPick 12, FollowingPick 13
	q2 := s.FollowingPick()
	for i, v := range []int{100, 90, 80, 70} {
		survivorAt(s, "rb"+string(rune('1'+i)), "RB", 1, v, q2, 0.75)
	}

	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan at the turn")
	}
	if plan.First != "RB" || plan.Second != "RB" {
		t.Errorf("plan = %s then %s, want rb then rb", plan.First, plan.Second)
	}
	if math.Abs(plan.Score-185.78125) > 1e-3 {
		t.Errorf("plan score = %v, want 185.78125", plan.Score)
	}
	if noExclusion := 100 + s.EBest("RB", q2); math.Abs(plan.Score-noExclusion) < 1 {
		t.Errorf("plan score %v is indistinguishable from the unexcluded %v: the second leg is still counting the player the first leg took",
			plan.Score, noExclusion)
	}
}

// The second leg is priced against the roster the FIRST leg leaves behind, and
// that is what stops the plan recommending a position twice out of habit. Same
// turn geometry, but one rb is already on my roster, so taking rb1 fills the
// second rb slot and drops rb from starter to flex weight for pick 13:
//
//	ebest(rb, 13, rb1) = 90(.75)             = 67.5,  x NeedAfter 0.6 = 40.5
//	EBest(wr, 13)      = 52(.75) + 48(.25)(.75) = 48, x NeedAfter 1.0 = 48
//	score(rb, wr) = 100 + 48   = 148   <- the plan
//	score(rb, rb) = 100 + 40.5 = 140.5
//	score(wr, rb) = 52 + 91.875 = 143.875
//
// Price the second leg with plain Need instead and rb-then-rb reads 167.5 and
// wins — the plan would tell me to spend both picks on a position whose starter
// slots the first pick already closed.
func TestBestPlanPricesTheSecondLegAgainstTheFirstLegsRoster(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 11 // NextPick 12, FollowingPick 13
	q2 := s.FollowingPick()
	survivorAt(s, "rb1", "RB", 1, 100, q2, 0.75)
	survivorAt(s, "rb2", "RB", 1, 90, q2, 0.75)
	survivorAt(s, "wr1", "WR", 1, 52, q2, 0.75)
	survivorAt(s, "wr2", "WR", 1, 48, q2, 0.75) // sum(1-p) = 1 = the one opponent pick
	addPlayers(s, Player{ID: "rbA", Name: "rbA", Pos: "RB", Tier: 1, Value: 95})
	s.Taken["rbA"] = true // taken, or he pollutes Available and outranks rb1
	s.Rosters[12] = []string{"rbA"}

	if got := s.Need("RB"); got != NeedStarter {
		t.Fatalf("fixture: need(rb) = %v with one rb rostered, want the second slot still open", got)
	}
	if got := s.NeedAfter("RB", "rb1"); got != NeedFlex {
		t.Fatalf("fixture: NeedAfter(rb | rb1) = %v, want flex weight", got)
	}

	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan at the turn")
	}
	if plan.First != "RB" || plan.Second != "WR" {
		t.Errorf("plan = %s then %s, want rb then wr", plan.First, plan.Second)
	}
	if math.Abs(plan.Score-148) > 1e-3 {
		t.Errorf("plan score = %v, want 148", plan.Score)
	}
}

// mirrorBoard is an exact tie, which is a real board state and not a contrived
// one: two positions with equal value, equal need and equal survival is what a
// fresh board looks like before anyone has picked. 4 teams, slot 2, standing at
// pick 1 — the lookahead fixture's geometry, so the tilt is again a no-op:
// sum(1 - p) over the eight available players is 4(0.5) + 4(0.75) = 5, exactly
// the opponent picks between my 2 and my 7.
//
//	EBest(rb, 7) = EBest(wr, 7) = 100(.5) + 80(.5)(.5) = 70
//	score(rb, wr) = score(wr, rb) = 100 + 70 = 170   <- tied, and the argmax
//	score(rb, rb) = 100 + 40 = 140                   (the pool minus rb1)
//
// The tes are the tilt's padding and are too cheap to win anything.
func mirrorBoard() *State {
	s := newTestState(4, 15, 2)
	s.PickNo = 1 // NextPick 2, FollowingPick 7
	q2 := s.FollowingPick()
	survivorAt(s, "rb1", "RB", 1, 100, q2, 0.5)
	survivorAt(s, "rb2", "RB", 1, 80, q2, 0.5)
	survivorAt(s, "wr1", "WR", 1, 100, q2, 0.5)
	survivorAt(s, "wr2", "WR", 1, 80, q2, 0.5)
	for i := 0; i < 4; i++ {
		survivorAt(s, "te"+string(rune('1'+i)), "TE", 1, 10, q2, 0.25)
	}
	return s
}

// Ties resolve by planPositions order, and nothing else pinned that. The pair
// loop's strict > is what makes it true — relax it to >= and the answer becomes
// whatever the last equal pair happened to be, which on this board is wr-then-rb
// instead of rb-then-wr. Both are deterministic today because the loop walks a
// fixed slice, so the mutation is silent: it passes the whole suite. It stops
// being silent the day somebody iterates a map instead, and by then the symptom
// is a plan line that flickers between two answers on every frame, which is the
// thing the tie-break comment in plan.go claims to prevent.
func TestBestPlanBreaksExactTiesByPositionOrder(t *testing.T) {
	s := mirrorBoard()
	q2 := s.FollowingPick()

	// The tie has to be exact, not merely close, or the assertion below is about
	// arithmetic rather than about the tie-break. Both sides are the same
	// operations over mirrored inputs, so bit-identical is the right bar.
	rbFirst := float64(s.Players["rb1"].Value)*s.Need("RB") +
		s.ebest("WR", q2, "rb1")*s.NeedAfter("WR", "rb1")
	wrFirst := float64(s.Players["wr1"].Value)*s.Need("WR") +
		s.ebest("RB", q2, "wr1")*s.NeedAfter("RB", "wr1")
	if rbFirst != wrFirst {
		t.Fatalf("fixture is not a tie: rb-then-wr %v, wr-then-rb %v", rbFirst, wrFirst)
	}

	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan on a mirrored board with two picks to come")
	}
	if plan.First != "RB" || plan.Second != "WR" {
		t.Errorf("plan = %s then %s, want rb then wr — the earlier pair in planPositions",
			plan.First, plan.Second)
	}
	if math.Abs(plan.Score-170) > 1e-3 {
		t.Errorf("plan score = %v, want 170", plan.Score)
	}
}

// endgameBoard is the last two picks of a draft: 12 teams, slot 3, standing on
// pick 166, so my only remaining picks are 166 and 171 and four opponent picks
// fall between them — which the eight players at 0.5 carry exactly, leaving the
// tilt a no-op. The backs are worth far more than anything else on the board and
// every one of my rb slots is already full, so pure score wants to spend both
// picks on the bench.
//
// The kicker and the defenses carry Value 0, which is not a fixture convenience:
// no source we import prices K or DEF, so that is what the real board holds.
func endgameBoard(rostered []string) *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 166 // round 14; my picks are 166 and 171, and nothing after
	q2 := s.FollowingPick()
	survivorAt(s, "rb1", "RB", 1, 800, q2, 0.5)
	survivorAt(s, "rb2", "RB", 1, 700, q2, 0.5)
	survivorAt(s, "te1", "TE", 1, 100, q2, 0.5)
	survivorAt(s, "k1", "K", 0, 0, q2, 0.5)
	survivorAt(s, "def1", "DEF", 0, 0, q2, 0.5)
	survivorAt(s, "def2", "DEF", 0, 0, q2, 0.5)
	survivorAt(s, "wr1", "WR", 1, 5, q2, 0.5)
	survivorAt(s, "wr2", "WR", 1, 5, q2, 0.5)
	for i, pos := range rostered {
		id := "mine" + string(rune('a'+i))
		s.Players[id] = Player{ID: id, Name: id, Pos: pos, Value: 500}
		s.Taken[id] = true // rostered players are off the board and out of the tilt
		s.Rosters[3] = append(s.Rosters[3], id)
	}
	return s
}

// The plan spends two of my remaining picks, so it cannot spend them both on the
// bench while a starting slot is still empty. Score alone does exactly that, and
// not marginally: K and DEF are the positions the endgame is about and they carry
// no value from any source, so every pair containing one scores 0 and loses the
// argmax to any pair of skill players. On the real board at 14.10 with a defense
// still to fill, the line read "rb at 14.10 → rb at 15.03" — two bench backs and
// no defense at all, on sixteen consecutive frames.
//
// Feasibility ranks first and score still decides the order of the two legs,
// which is what makes the second row right as well as legal: take the contested
// tight end at 14.10 and the fungible kicker at 15.03, not the reverse.
func TestBestPlanFillsStartersWhenThePicksRunOut(t *testing.T) {
	cases := []struct {
		name         string
		roster       []string // positions already on my roster, in draft order
		first, secnd string
		score        float64
	}{
		// One spare pick: the bench back is legal at 14.10 as long as the defense
		// still gets 15.03. score(rb, def) = 800(0.25) + 0.
		{"one slot open", []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "K"},
			"RB", "DEF", 200},
		// No spare pick: both legs must start. score(te, k) = 100(1.0) + 0, and
		// the reverse pair is worth EBest(te) = 50 instead of te1's own 100.
		{"every pick must start", []string{"QB", "RB", "RB", "WR", "WR", "RB", "DEF"},
			"TE", "K", 100},
	}
	for _, c := range cases {
		s := endgameBoard(c.roster)
		q2 := s.FollowingPick()

		// The fixture only proves something if score alone would answer differently.
		greedy := float64(s.Players["rb1"].Value)*s.Need("RB") +
			s.ebest("RB", q2, "rb1")*s.NeedAfter("RB", "rb1")
		plan, ok := s.BestPlan()
		if !ok {
			t.Fatalf("%s: no plan with two picks to come", c.name)
		}
		if greedy <= plan.Score {
			t.Fatalf("%s: fixture proves nothing — rb then rb scores %v against the plan's %v, so feasibility never had to break the tie",
				c.name, greedy, plan.Score)
		}
		if plan.First != c.first || plan.Second != c.secnd {
			t.Errorf("%s: plan = %s then %s, want %s then %s",
				c.name, plan.First, plan.Second, c.first, c.secnd)
		}
		if math.Abs(plan.Score-c.score) > 1e-3 {
			t.Errorf("%s: plan score = %v, want %v", c.name, plan.Score, c.score)
		}
	}
}

// A plan needs a second pick to plan for. On my last pick of the draft there is
// no q2, and inventing one — falling back to NextPick, or to the final pick of
// the draft the way NextPick itself does — would price a leg that never happens.
// The mid-draft case is the control: without it this table would pass just as
// well on a board too empty to plan from. It asserts nothing about the numbers,
// so its tilt clamping is harmless; do not copy the fixture for one that does.
func TestBestPlanNeedsASecondPick(t *testing.T) {
	cases := []struct {
		pickNo int
		wantOK bool
	}{
		{4, true},    // mid-draft: picks 22 and 27 to come
		{171, false}, // standing on my last pick of round 15
		{175, false}, // past it, still inside the draft
		{181, false}, // the draft is over
	}
	for _, c := range cases {
		s := newTestState(12, 15, 3)
		addPlayers(s,
			Player{ID: "a", Pos: "RB", Value: 100, ADP: 20, Sigma: 6},
			Player{ID: "b", Pos: "WR", Value: 90, ADP: 25, Sigma: 6},
		)
		s.PickNo = c.pickNo
		plan, ok := s.BestPlan()
		if ok != c.wantOK {
			t.Errorf("BestPlan at pick %d = %+v ok=%v, want ok=%v", c.pickNo, plan, ok, c.wantOK)
		}
		if !ok && plan != (Plan{}) {
			t.Errorf("BestPlan at pick %d returned %+v with ok=false, want the zero plan", c.pickNo, plan)
		}
	}
}

// The second leg's tilt asks how many OPPONENT picks stand between now and q2,
// and ebest derives that itself rather than being told. That only works if the
// identity holds: q2 - PickNo - 1, every intervening pick except my own at
// NextPick. Counting my own would price a removal I am the one making, and it is
// worst exactly at the turn, where my picks are adjacent and the second leg has
// one opponent rather than two.
func TestSecondLegOpponentCount(t *testing.T) {
	for _, teams := range []int{4, 12} {
		for slot := 1; slot <= teams; slot++ {
			s := newTestState(teams, 15, slot)
			for _, pick := range []int{1, teams - 1, teams, teams + 1, 3 * teams} {
				s.PickNo = pick
				q2 := s.FollowingPick()
				if q2 == 0 {
					continue
				}
				if got, want := s.opponentPicksBefore(q2), q2-s.PickNo-1; got != want {
					t.Errorf("teams=%d slot=%d pick=%d: opponents before q2=%d = %d, want %d",
						teams, slot, pick, q2, got, want)
				}
			}
		}
	}
}

// NeedAfter is the roster arithmetic the second leg turns on: taking a rb with
// the first pick is what drops rb from starter to flex weight for the second.
func TestNeedAfter(t *testing.T) {
	cases := []struct {
		roster []string
		pos    string
		add    string
		want   float64
	}{
		{nil, "RB", "rb1", NeedStarter},                    // two rb slots, one still open
		{[]string{"rbA"}, "RB", "rb1", NeedFlex},           // both dedicated slots full, flex open
		{[]string{"rbA", "rbB"}, "RB", "rb1", NeedBench},   // flex goes too
		{[]string{"rbA", "rbB"}, "WR", "rb1", NeedStarter}, // a rb never fills a wr slot
		{nil, "K", "rb1", 0},                               // suppression outranks the roster
	}
	for _, c := range cases {
		s := newTestState(12, 15, 3)
		addPlayers(s,
			Player{ID: "rb1", Pos: "RB", Value: 100},
			Player{ID: "rbA", Pos: "RB", Value: 90},
			Player{ID: "rbB", Pos: "RB", Value: 80},
		)
		s.Rosters[3] = c.roster
		if got := s.NeedAfter(c.pos, c.add); got != c.want {
			t.Errorf("roster %v + %s: NeedAfter(%s) = %v, want %v", c.roster, c.add, c.pos, got, c.want)
		}
	}
}

// The plan is read during a render, so it must not touch the roster it reasons
// about. A mutate-and-restore would be shorter and wrong twice over: a panic
// between the two leaves the board describing a roster that never existed, and
// appending in place writes into the roster slice's spare capacity — a write no
// comparison of State can see, which is why this checks the backing array
// directly and not just the slice.
func TestBestPlanDoesNotTouchTheRoster(t *testing.T) {
	s := lookaheadBoard()
	s.Players["rbA"] = Player{ID: "rbA", Name: "rbA", Pos: "RB", Tier: 1, Value: 95}
	s.Taken["rbA"] = true
	mine := make([]string, 0, 4) // spare capacity: the shape the aliasing bug needs
	mine = append(mine, "rbA")
	s.Rosters[s.MySlot] = mine

	before := map[int][]string{}
	for slot, ids := range s.Rosters {
		before[slot] = append([]string(nil), ids...)
	}

	if _, ok := s.BestPlan(); !ok {
		t.Fatal("no plan; the purity check below would be vacuous")
	}
	if _, ok := s.BestPlan(); !ok { // twice: a mutation would compound
		t.Fatal("no plan on the second call")
	}

	if !reflect.DeepEqual(before, s.Rosters) {
		t.Errorf("rosters changed: %v, want %v", s.Rosters, before)
	}
	if spare := mine[:cap(mine)]; spare[1] != "" {
		t.Errorf("a planned player was appended into the roster's spare capacity: %q", spare[1])
	}
}
