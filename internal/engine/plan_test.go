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
// The kicker and the defenses carry Value 0. That used to be what the real board
// held; `fetch` now anchors k/def onto the skill curve, so this is the residual
// case rather than the common one — a defense a live feed registered off-board,
// with no adp to anchor to and therefore no value. It is still the harder test:
// a starting slot that must be filled by somebody the score cannot see.
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
//
// TWO MECHANISMS ENFORCE IT NOW, and the split is worth knowing. mustFill in
// BestPlan is one; the endgame feasibility guard in needFrom is the other, and
// at R == U it gets there first — a bench position's need is zero, so the greedy
// pair is not merely outranked, it never becomes a candidate. At R == U+1 the
// guard only halves the bench weight, so mustFill is still what does the work.
//
// The invariant is therefore checked against the ROSTER rather than against the
// scores: whatever pair wins, the two legs together have to close every starting
// slot two picks can still close. That is the actual bug ("no defense at all"),
// and unlike a score comparison it cannot be satisfied by the arithmetic that
// produced the plan.
func TestBestPlanFillsStartersWhenThePicksRunOut(t *testing.T) {
	cases := []struct {
		name         string
		roster       []string // positions already on my roster, in draft order
		first, secnd string
		score        float64
	}{
		// One spare pick, R == U+1: the bench back is legal at 14.10 as long as
		// the defense still gets 15.03, and taking the man you can SEE with the
		// spare pick beats taking an expectation of him one pick later.
		// score(rb, def) = 800(0.25) + EBest(def at 15.03)(1.0) = 200, since the
		// defenses on this board carry no value at all. The reverse pair is
		// 0(1.0) + EBest(rb at 15.03)(0.25) = 575(0.25) = 143.75, and loses.
		//
		// Both orderings end at the same roster, so their scores may differ only
		// by what the board can still offer at each pick. They did not while the
		// endgame slack was charged to leg one and not to leg two: it halved the
		// back at 14.10 and not at 15.03, def -> rb won on 143.75 against 100, and
		// the plan gave up a back worth 800 to take a defense that was equally
		// available at either pick.
		{"one slot open", []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "K"},
			"RB", "DEF", 200},
		// No spare pick, R == U: both legs must start, and needFrom has already
		// zeroed every position that cannot. score(te, k) = 100(1.0) + 0, and the
		// reverse pair is worth EBest(te) = 50 instead of te1's own 100.
		{"every pick must start", []string{"QB", "RB", "RB", "WR", "WR", "RB", "DEF"},
			"TE", "K", 100},
	}
	for _, c := range cases {
		s := endgameBoard(c.roster)
		open := len(s.UnfilledStarters(s.MySlot))
		plan, ok := s.BestPlan()
		if !ok {
			t.Fatalf("%s: no plan with two picks to come", c.name)
		}
		if plan.First != c.first || plan.Second != c.secnd {
			t.Errorf("%s: plan = %s then %s, want %s then %s",
				c.name, plan.First, plan.Second, c.first, c.secnd)
		}
		if math.Abs(plan.Score-c.score) > 1e-3 {
			t.Errorf("%s: plan score = %v, want %v", c.name, plan.Score, c.score)
		}

		// Play the plan out on a copy of my roster and count the slots it closed.
		// Two picks cannot close more slots than are open, hence the min.
		for i, pos := range []string{plan.First, plan.Second} {
			id := "planned" + string(rune('a'+i))
			s.Players[id] = Player{ID: id, Name: id, Pos: pos}
			s.Rosters[s.MySlot] = append(s.Rosters[s.MySlot], id)
		}
		want := open
		if want > 2 {
			want = 2
		}
		if closed := open - len(s.UnfilledStarters(s.MySlot)); closed != want {
			t.Errorf("%s: plan %s then %s closes %d of %d open starting slots, want %d",
				c.name, plan.First, plan.Second, closed, open, want)
		}
	}
}

// A pair's two legs end at the same roster, so a position has to be worth the
// same in either of them. The endgame slack broke that: it multiplied the bench
// weight inside the shared need rule, leg one saw an open starting slot and leg
// two saw the lineup that leg one had just completed, and the same back was
// worth 0.125 first and 0.25 second. mustFill already forces the starter to be
// filled in BOTH orderings, so the discount was a second, order-sensitive charge
// for a constraint that was already met — and the plan line contradicted the
// group order on the board directly under it.
//
// The board keeps the discount, and must: a single greedy pick has no mustFill
// to lean on, so there the multiplier is the only thing saying a flier costs the
// last spare pick.
//
// R == U+1 at pick 166 with only the defense slot open, which is the regime the
// slack applies to; anything else would pass with the bug still in.
func TestPlanLegsPriceAPositionTheSameInEitherOrder(t *testing.T) {
	s := endgameBoard([]string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "K"})
	filled, _ := s.FilledSlots(s.MySlot)
	if r, u := s.MyPicksLeft(), len(s.UnfilledStarters(s.MySlot)); r != u+1 {
		t.Fatalf("fixture is not the one-spare-pick regime: %d picks, %d unfilled", r, u)
	}

	cases := []struct {
		pos       string
		after     string  // the id the first leg would have taken
		want      float64 // what both legs must pay
		wantBoard float64 // ...and what the board's single-pick need still is
	}{
		{"RB", "def1", NeedBench, NeedBench * EndgameSlack}, // bench either way
		{"DEF", "rb1", NeedStarter, NeedStarter},            // the open slot, never discounted
	}
	for _, c := range cases {
		leg1 := s.needFrom(c.pos, filled)
		leg2 := s.NeedAfter(c.pos, c.after)
		if leg1 != leg2 {
			t.Errorf("%s: leg one pays %v, leg two pays %v — same roster, same weight",
				c.pos, leg1, leg2)
		}
		if leg1 != c.want {
			t.Errorf("%s: plan legs pay %v, want %v", c.pos, leg1, c.want)
		}
		if got := s.Need(c.pos); got != c.wantBoard {
			t.Errorf("%s: board need = %v, want %v", c.pos, got, c.wantBoard)
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

// THE REPRICING: both legs are priced over replacement, and this is the board
// that shows why. The quarterbacks run 100, 96, 94, 92 with four startable
// slots, so R(QB) = 92 and the headline man is worth 8 over the one this league
// would have handed you anyway; the lone back is worth his whole 90. Raw value
// says qb first (100 > 90, and the second legs are shared); value over
// replacement says rb first, by an order of magnitude. The old formula scored
// legs on raw value and recommended the quarterback — the exact shape of the
// live 3.01 frame that kept arguing for early QBs.
//
// Both contested men survive at 5%, so neither position's second leg can
// compensate: the best second leg for either choice is the same tight end, and
// a shared leg cancels. What decides is leg one, which is the claim under test.
func TestPickChoicesPriceLegsOverReplacement(t *testing.T) {
	s := newTestState(4, 15, 2)
	s.PickNo = 1 // NextPick 2, FollowingPick 7
	q2 := s.FollowingPick()
	survivorAt(s, "qb1", "QB", 1, 100, q2, 0.05)
	survivorAt(s, "qb2", "QB", 1, 96, q2, 0.9)
	survivorAt(s, "qb3", "QB", 1, 94, q2, 0.9)
	survivorAt(s, "qb4", "QB", 1, 92, q2, 0.9)
	survivorAt(s, "rb1", "RB", 1, 90, q2, 0.05)
	survivorAt(s, "te1", "TE", 1, 60, q2, 0.9)
	survivorAt(s, "te2", "TE", 1, 10, q2, 0.9)

	// Fixture sanity: the startable index puts qb replacement at the 4th man and
	// leaves the back free, so vor inverts the raw-value order.
	if got := s.Replacement("QB"); got != 92 {
		t.Fatalf("replacement(qb) = %v, want 92 — 4 teams x 1 startable slot", got)
	}
	if got := s.Replacement("RB"); got != 0 {
		t.Fatalf("replacement(rb) = %v, want 0 — one back valued against nine startable", got)
	}
	if qb, rb := s.VOR(s.Players["qb1"]), s.VOR(s.Players["rb1"]); qb >= rb {
		t.Fatalf("fixture is not a disagreement: vor(qb1) %v does not trail vor(rb1) %v", qb, rb)
	}

	plan, ok := s.BestPlan()
	if !ok {
		t.Fatal("no plan on a board with three live positions and two picks to come")
	}
	if plan.First != "RB" {
		t.Errorf("plan.First = %s, want rb — the 8-point qb discount must not outrank a 90-point back", plan.First)
	}

	choices := s.PickChoices()
	var qbScore, rbScore float64
	for _, c := range choices {
		switch c.Pos {
		case "QB":
			qbScore = c.Score
		case "RB":
			rbScore = c.Score
		}
	}
	if qbScore >= rbScore {
		t.Errorf("score(qb) %v >= score(rb) %v: leg one is being priced on raw value again", qbScore, rbScore)
	}
}

// One brain: BestPlan is PickChoices' first row, by construction and now by
// assertion, because the whole reason PickChoices exists is that the plan line
// and the ordering under it used to rank the same frame differently.
func TestBestPlanIsTheTopPickChoice(t *testing.T) {
	boards := map[string]*State{
		"lookahead": lookaheadBoard(),
		"mirror":    mirrorBoard(),
	}
	turn := newTestState(12, 15, 12)
	turn.PickNo = 11
	q2 := turn.FollowingPick()
	for i, v := range []int{100, 90, 80, 70} {
		survivorAt(turn, "rb"+string(rune('1'+i)), "RB", 1, v, q2, 0.75)
	}
	boards["turn"] = turn

	for name, s := range boards {
		plan, ok := s.BestPlan()
		if !ok {
			t.Fatalf("%s: no plan", name)
		}
		choices := s.PickChoices()
		if len(choices) == 0 {
			t.Fatalf("%s: no choices", name)
		}
		top := choices[0]
		if plan.First != top.Pos || plan.Second != top.Second || plan.Score != top.Score {
			t.Errorf("%s: plan (%s→%s, %v) is not the top choice (%s→%s, %v)",
				name, plan.First, plan.Second, plan.Score, top.Pos, top.Second, top.Score)
		}
	}
}

// On my last pick there is no second leg: BestPlan honestly says no plan, but
// PickChoices still ranks the frame — on vor x need alone, with no second-leg
// position named — because the board still has to order itself on the one frame
// where "what does he buy over replacement" is the only question left.
func TestPickChoicesOnMyLastPick(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 171 // slot 3's round-15 pick: 14*12 + 3
	addPlayers(s,
		Player{ID: "rb1", Pos: "RB", Value: 100, ADP: 20, Sigma: 6},
		Player{ID: "wr1", Pos: "WR", Value: 80, ADP: 25, Sigma: 6},
	)
	if got := s.NextPick(); got != 171 {
		t.Fatalf("fixture: NextPick = %d, want 171 to be my own last pick", got)
	}
	if _, ok := s.BestPlan(); ok {
		t.Error("BestPlan said ok on my last pick; there is nothing to plan")
	}
	choices := s.PickChoices()
	if len(choices) != 2 {
		t.Fatalf("choices = %d, want 2", len(choices))
	}
	if choices[0].Pos != "RB" || choices[1].Pos != "WR" {
		t.Errorf("order = %s, %s; want rb then wr on vor x need", choices[0].Pos, choices[1].Pos)
	}
	if choices[0].Score != 100 || choices[1].Score != 80 {
		t.Errorf("scores = %v, %v; want the bare vor x need 100 and 80", choices[0].Score, choices[1].Score)
	}
	for _, c := range choices {
		if c.Second != "" {
			t.Errorf("%s: second leg = %q on my last pick, want none", c.Pos, c.Second)
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
