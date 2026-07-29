package ui

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
)

// adder writes one obviously-fake player into a fixture board.
type adder func(id, pos string, value int, adp, sigma float64, tier int)

func newBoard() (map[string]engine.Player, adder) {
	players := map[string]engine.Player{}
	return players, func(id, pos string, value int, adp, sigma float64, tier int) {
		players[id] = engine.Player{ID: id, Name: "fake " + id, Pos: pos, Team: "AAA",
			Value: value, ADP: adp, Sigma: sigma, Tier: tier, Bye: 7}
	}
}

// addDepth gives a fixture the mid-board a real one has: twenty-eight players
// whose adp falls inside the stretch of picks between a vantage and my turn.
//
// It is not decoration. The exactly-N tilt solves sum(1 - p^c) = N over every
// available player, so a board has to be able to SUPPLY the removals its own
// draft geometry implies — at pick 4 of a 12-team draft from slot 3, eighteen
// players go before my turn. The old seventeen-player fixture could not: even
// at the c = TiltCMax end of the bracket its expected removals topped out
// around 12, the solve clamped, every survival collapsed to roughly zero, and
// "safe to wait" became unreachable — which would have quietly gutted the
// milestone-4 DoD test below into asserting nothing.
// TestFixturesAreNotTiltClamped pins the condition so a future fixture edit
// can't re-break it silently.
//
// Depth is what fixes that, not filler: a player with adp 150 survives at any
// c and contributes nothing to the sum. These sit at adp 12-50, are valued
// under every named player at their position so they never take over a group,
// and carry deep tiers so they never touch the narrative tiers above them.
func addDepth(add adder) {
	add("rx1", "RB", 52, 12, 3, 4)
	add("rx2", "RB", 48, 18, 4, 4)
	add("rx3", "RB", 44, 24, 4, 5)
	add("rx4", "RB", 40, 32, 5, 5)
	add("rx5", "RB", 36, 44, 6, 5)
	add("wx1", "WR", 50, 13, 3, 4)
	add("wx2", "WR", 46, 19, 4, 4)
	add("wx3", "WR", 42, 27, 5, 5)
	add("wx4", "WR", 38, 36, 5, 5)
	add("wx5", "WR", 34, 46, 6, 5)
	add("qx1", "QB", 44, 16, 4, 4)
	add("qx2", "QB", 40, 22, 4, 4)
	add("qx3", "QB", 36, 30, 5, 5)
	add("qx4", "QB", 32, 40, 6, 5)
	add("qx5", "QB", 28, 48, 7, 5)
	add("tx1", "TE", 34, 14, 3, 3)
	add("tx2", "TE", 30, 20, 4, 3)
	add("tx3", "TE", 26, 28, 5, 4)
	add("tx4", "TE", 22, 38, 5, 4)
	add("tx5", "TE", 18, 50, 7, 4)
	add("rx6", "RB", 34, 15, 3, 6)
	add("rx7", "RB", 32, 28, 5, 6)
	add("wx6", "WR", 32, 16, 4, 6)
	add("wx7", "WR", 30, 30, 5, 6)
	add("qx6", "QB", 26, 18, 4, 6)
	add("qx7", "QB", 24, 34, 5, 6)
	add("tx6", "TE", 16, 17, 4, 5)
	add("tx7", "TE", 14, 32, 5, 5)
}

// runState is a board tuned so a scripted rb run genuinely moves the urgency
// math: rb1 is an elite faller (tight sigma, gone by my next pick), rb2-5 are
// the safety net the room is about to eat, and the wr tier goes so early that
// waiting on receivers is expensive until it suddenly isn't.
//
// wr1 and wr2 are a two-man tier 1 so that draining them leaves bestNow(wr) in
// an untouched tier 2 — otherwise the opening frame carries a cliff banner and
// the pre-run "nothing is alarming yet" premise is gone. qb2 sits alone in
// tier 2 for the same reason: drafting qb1 must not leave a half-eaten tier
// behind.
func runState() *engine.State {
	players, add := newBoard()
	add("rb1", "RB", 100, 4, 2, 1)
	add("rb2", "RB", 98, 26, 4, 1)
	add("rb3", "RB", 96, 30, 5, 1)
	add("rb4", "RB", 94, 34, 5, 1)
	add("rb5", "RB", 92, 38, 6, 1)
	add("rb6", "RB", 55, 60, 8, 2)
	add("wr1", "WR", 95, 2, 2, 1)
	add("wr2", "WR", 93, 3, 2, 1)
	add("wr3", "WR", 90, 8, 3, 2)
	add("wr4", "WR", 88, 15, 4, 2)
	add("wr5", "WR", 60, 45, 7, 2)
	add("wr6", "WR", 58, 52, 7, 2)
	add("qb1", "QB", 85, 10, 3, 1)
	add("qb2", "QB", 80, 25, 5, 2)
	add("qb3", "QB", 50, 55, 8, 3)
	add("te1", "TE", 70, 28, 5, 1)
	add("te2", "TE", 40, 70, 9, 2)
	addDepth(add)
	return engine.New(players, 12, 15, 3)
}

// waitState is the safe-to-wait board: an untouched rb tier whose leader will
// keep, with the depth board behind it. The test drains the leader and walks
// the vantage forward to where my next pick is a round away, at which point the
// two men left are genuinely contested and the tier stops being safe.
//
// Both halves of that transition are load-bearing now. Under tier-hold, a tier
// is not "ending" because it got small — it is ending because what is left
// probably will not reach me, so touching the tier is not enough on its own.
func waitState() *engine.State {
	players, add := newBoard()
	add("r1", "RB", 100, 10, 3, 1) // deep enough to keep across two picks
	add("r2", "RB", 96, 26, 5, 1)  // ...and these two are coin flips over a round
	add("r3", "RB", 92, 25, 5, 1)
	addDepth(add)
	return engine.New(players, 12, 15, 3)
}

// cliffState is the two-cliffs-at-once board: rb and wr have each had their
// tier-1 leader drafted and what is left of both tiers is on its way out, so
// both read cliff-last. What separates them is what sits behind: rb3 is a
// near-certain survivor worth almost as much as the man on the edge, so waiting
// on rb costs little; wr's only fallback is a fraction of wb2's value, so wr is
// the position actually bleeding.
//
// Under tier-hold this fixture has to work harder than its count-based ancestor
// did. A "cliff" whose last man trivially survives is no longer a cliff — that
// is the whole point of pricing tiers by probability — so the men left here are
// genuinely at risk rather than merely few.
//
// The two tiers also end up on opposite sides of the copy rule on purpose: rb
// has one man left and can still be called the last one, wr has two and cannot.
func cliffState() *engine.State {
	players, add := newBoard()
	add("ra", "RB", 100, 2, 2, 1)   // taken below
	add("rb2", "RB", 90, 14, 4, 1)  // the last one, and he really is going
	add("rb3", "RB", 85, 65, 8, 2)  // ...but the drop behind him is tiny
	add("wa", "WR", 100, 2, 2, 1)   // taken below
	add("wb2", "WR", 98, 1, 0.5, 1) // gone by pick 22 with near certainty
	add("wb3", "WR", 94, 19, 4, 1)  // two left, both contested: not "last one"
	add("wc", "WR", 55, 60, 8, 2)   // the drop behind them is not tiny
	add("td1", "TE", 30, 80, 9, 1)  // my own pick 3, so picks intervene again
	addDepth(add)
	s := engine.New(players, 12, 15, 3)
	s.Draft("ra")
	s.Draft("wa")
	s.Draft("td1")
	return s
}

// lookaheadState is the ui side of engine/plan_test.go's lookahead board, and
// the only fixture here that is about the plan line rather than the groups under
// it. 4 teams, slot 2, standing at pick 1: my picks are 1.02 and 2.03, so five
// opponent picks fall between them. The rbs hold to the second pick and the
// receivers do not, so the plan is to take the wr while he exists and collect
// the rb on the way back.
//
// Borrowed rather than invented because its score is hand-computed there
// (90 + EBest(rb, 7) = 90 + 97.2), which is what lets the rendered line be
// asserted as an exact string instead of against whatever the engine returns.
// That only holds while the tilt stays neutral: sum(1 - p) over the eight
// available players is 0.1 + 0.1 + 0.95 + 0.95 + 4(0.725) = 5, exactly the picks
// that happen, so the tes are load-bearing and not filler.
//
// Values are that board's times 100 — 10000 rather than 100 — because
// FantasyCalc puts 10465 on the best player alive and five figures is the regime
// the real board renders in. Nothing on screen depends on the multiplier:
// survival, need and the ordering are all scale-free, and the plan line quotes
// no value at all.
//
// Sigmas come from the closed form for a player whose adp is the current pick —
// survival to `at` is 2/(1 + e^(gap/sigma)), so sigma = gap/ln(2/p - 1) puts any
// probability on the board exactly. gap is 6 here, pick 1 to pick 7.
func lookaheadState() *engine.State {
	players, add := newBoard()
	sigma := func(p float64) float64 { return 6 / math.Log(2/p-1) }
	add("rb1", "RB", 10000, 1, sigma(0.9), 1)
	add("rb2", "RB", 8000, 1, sigma(0.9), 1)
	add("wr1", "WR", 9000, 1, sigma(0.05), 1)
	add("wr2", "WR", 7000, 1, sigma(0.05), 1)
	for i := 1; i <= 4; i++ {
		add(fmt.Sprintf("te%d", i), "TE", 1000, 1, sigma(0.275), 1)
	}
	return engine.New(players, 4, 15, 2)
}

// The scripted drafts the tests replay. Shared so the fixture guard below can
// stand at exactly the vantages the assertions do.
var (
	openingPicks = []string{"wr1", "wr2", "qb1"}                      // picks 1-3; pick 3 is mine
	runPicks     = []string{"wr3", "rb2", "rb3", "wr4", "rb4", "rb5"} // picks 4-9: four backs
	waitPicks    = []string{"r1", "wx3", "qx3"}                       // the leader goes, my turn slides out
)

var topGroupRe = regexp.MustCompile(`▏ ([a-z]+)`)

// topGroup reads the urgency leader off the one visual cue that marks it: only
// the top group's header starts with the ▏ accent.
func topGroup(view string) string {
	m := topGroupRe.FindStringSubmatch(view)
	if m == nil {
		return ""
	}
	return m[1]
}

// groupHeadRe matches a group header on the board tab: the two-column edge
// (blank, or the accent on the top group) then the position tag then the
// header's two spaces. Player rows are indented two further, and the sidebar
// sits past the divider on the same lines, so neither can match.
//
// The tag is spelled out rather than [a-z]+ because the plan line sits at the
// same indent in the same shape — "  plan  wr at 1.02 → …" — and matched as a
// group called "plan", which put a position that does not exist at the head of
// every order assertion.
var groupHeadRe = regexp.MustCompile(`(?m)^(?:▏ |  )(qb|rb|wr|te|k|def)  \S`)

// groupOrder reads the board tab's position groups in the order they render,
// which is the order urgency put them in.
func groupOrder(view string) []string {
	var out []string
	for _, m := range groupHeadRe.FindAllStringSubmatch(view, -1) {
		out = append(out, m[1])
	}
	return out
}

// groupLine returns a position's group header off the board tab. Assertions
// scope to it because the frame now legitimately carries "safe to wait" on
// several groups at once, and a whole-frame Contains would pass on the wrong
// one.
func groupLine(view, pos string) string {
	for _, line := range strings.Split(view, "\n") {
		l := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "▏"))
		// The data tab pads its position column wider, so require the tag to be
		// followed by exactly the header's two spaces and then copy.
		if strings.HasPrefix(l, pos+"  ") && !strings.HasPrefix(l, pos+"   ") {
			return l
		}
	}
	return ""
}

// planRow returns the board tab's plan line, or "" when it isn't rendered. The
// sidebar sits on the same physical line past the divider, so cut there — the
// assertion is about the left pane's row, not about what happens to be beside it.
func planRow(view string) string {
	for _, line := range strings.Split(view, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "plan  ") {
			continue
		}
		if i := strings.Index(l, "│"); i >= 0 {
			l = l[:i]
		}
		return strings.TrimSpace(l)
	}
	return ""
}

// leadingBlanks counts the blank left-pane rows between the "best available"
// section head and whatever renders first under it. groupBlock opens with one
// blank of its own, so a planless pane reads 1 and a planned pane reads 0 — and
// 2 means a row was reserved and left empty, which is the failure this is here
// to name rather than the plan simply being absent.
func leadingBlanks(view string) int {
	n, found := 0, false
	for _, line := range strings.Split(view, "\n") {
		l := line
		if i := strings.Index(l, "│"); i >= 0 {
			l = l[:i] // the sidebar shares these lines; it is not the left pane
		}
		l = strings.TrimSpace(l)
		if !found {
			found = strings.HasPrefix(l, "best available")
			continue
		}
		if l != "" {
			break
		}
		n++
	}
	return n
}

// playerRow returns the first rendered row mentioning a player, on either tab.
func playerRow(view, name string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, name+" ") {
			return line
		}
	}
	return ""
}

// tiltRemovals is the exactly-N tilt's own objective, f(c) = sum(1 - p^c) over
// every available player, rebuilt here from the exported survival because
// survivalTilt is unexported. The horizon is a parameter because the frame now
// solves the tilt at two of them: my next pick, and — for the plan line's second
// leg — the pick after that. Used only by the clamp guard below.
func tiltRemovals(s *engine.State, at int, c float64) float64 {
	total := 0.0
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		total += 1 - math.Pow(s.PSurviveAt(p, at), c)
	}
	return total
}

// A fixture has to be able to supply the removals its own draft geometry
// implies, and this pins that it does.
//
// The tilt solves sum(1 - p^c) = N over every available player. When the board
// cannot reach N even at c = TiltCMax — eighteen players go before my turn on a
// seventeen-player toy board — the solve clamps there, every survival collapses
// to roughly zero, every position reads maximally urgent and "safe to wait"
// becomes unreachable. The clamp is correct; a fixture that triggers it is not.
// The failure is silent: the run test below would keep passing while asserting
// nothing, which is exactly what it did before addDepth existed.
//
// f is continuous and strictly increasing in c, so the solve lands strictly
// inside the bracket precisely when f(1/TiltCMax) < N < f(TiltCMax). Vantages
// with N = 0 are excluded: on my own pick there is nothing to solve and c is 1
// by definition.
func TestFixturesAreNotTiltClamped(t *testing.T) {
	runA := runState()
	for _, id := range openingPicks {
		runA.Draft(id)
	}
	runB := runState()
	for _, id := range append(append([]string{}, openingPicks...), runPicks...) {
		runB.Draft(id)
	}
	waitAfter := waitState()
	for _, id := range waitPicks {
		waitAfter.Draft(id)
	}
	wait, cliff, look := waitState(), cliffState(), lookaheadState()

	cases := []struct {
		name string
		s    *engine.State
		at   int // the horizon whose solve has to land inside the bracket
		n    int // opponent picks before it — the tilt's target
	}{
		{"run frame a", runA, runA.NextPick(), runA.PicksUntilMine()},
		{"run frame b", runB, runB.NextPick(), runB.PicksUntilMine()},
		{"wait, untouched tier", wait, wait.NextPick(), wait.PicksUntilMine()},
		{"wait, tier ending", waitAfter, waitAfter.NextPick(), waitAfter.PicksUntilMine()},
		{"two cliffs at once", cliff, cliff.NextPick(), cliff.PicksUntilMine()},
		// The plan line's second leg prices survival to my pick AFTER next, so
		// that horizon has to clear the bracket too — a clamp there would leave
		// the rendered plan naming a pair chosen from survivals that are all
		// zero, which is the same silent nothing-asserted failure one horizon
		// down. My own next pick is not an opponent's, hence the extra -1.
		{"lookahead, first leg", look, look.NextPick(), look.PicksUntilMine()},
		{"lookahead, second leg", look, look.FollowingPick(),
			look.FollowingPick() - look.PickNo - 1},
	}
	for _, c := range cases {
		lo := tiltRemovals(c.s, c.at, 1/engine.TiltCMax)
		hi := tiltRemovals(c.s, c.at, engine.TiltCMax)
		if lo >= float64(c.n) || float64(c.n) >= hi {
			t.Errorf("%s: tilt clamps at pick %d, horizon %d — want %.3f < %d < %.3f",
				c.name, c.s.PickNo, c.at, lo, c.n, hi)
		}
	}
}

// The milestone 4 DoD: a scripted rb run flips the banner and re-sorts the
// board. Frame A has receivers evaporating (wr on top, no banner, and waiting
// on wr expressly not safe); six picks later four rbs are gone, the banner is
// live, rb owns the board, and the wr group — its survivors now safe — has
// collapsed to "safe to wait".
func TestScriptedRunFlipsBannerAndResortsBoard(t *testing.T) {
	s := runState()
	for _, id := range openingPicks {
		s.Draft(id)
	}
	b := Board{State: s, Width: 92, Height: 40}
	before := ansi.ReplaceAllString(b.View(), "")

	if strings.Contains(before, "run in progress") {
		t.Fatalf("no run has happened yet, but the banner is up:\n%s", before)
	}
	// wr urgency 28: bestNow wr3 won't reach pick 22 and neither will wr4, so
	// what is left of the position is a third less valuable. rb urgency 3: rb2-5
	// back the fallen rb1 up four deep.
	if got := topGroup(before); got != "wr" {
		t.Errorf("before the run, top group = %q, want wr", got)
	}
	if head := groupLine(before, "wr"); strings.Contains(head, "safe to wait") {
		t.Errorf("receivers are evaporating; wr must not read safe, got %q", head)
	}

	// picks 4-9: the room eats the safe mid rbs. four of the last six.
	for _, id := range runPicks {
		s.Draft(id)
	}
	after := ansi.ReplaceAllString(b.View(), "")

	if !strings.Contains(after, "rb run in progress") {
		t.Errorf("expected the rb run banner, got:\n%s", after)
	}
	// rb urgency 45: only the doomed rb1 and tier-2 rb6 remain. wr urgency 0.06:
	// its survivors are safe now, which is exactly why the board flips.
	if got := topGroup(after); got != "rb" {
		t.Errorf("after the run, top group = %q, want rb", got)
	}
	if head := groupLine(after, "wr"); !strings.Contains(head, "safe to wait") {
		t.Errorf("the collapsed wr group should read safe to wait, got %q", head)
	}
}

// Cliff copy always beats the safe-to-wait tag: a tier that probably will not
// reach me is not safe, whatever its best player's own odds look like.
//
// Both halves of the transition are load-bearing under tier-hold. Draining the
// leader is no longer enough to end a tier — what ends it is the men left being
// contested — so the fixture also walks the vantage out to where my next pick
// is a round away.
func TestSafeToWaitYieldsToCliffCopy(t *testing.T) {
	s := waitState()
	b := Board{State: s, Width: 92, Height: 40}

	head := groupLine(ansi.ReplaceAllString(b.View(), ""), "rb")
	if !strings.Contains(head, "safe to wait") {
		t.Errorf("an untouched tier of safe players should read safe to wait, got %q", head)
	}
	if !strings.Contains(head, "3 left in tier 1 · holds") {
		t.Errorf("the header should carry the count and the hold, got %q", head)
	}

	for _, id := range waitPicks {
		s.Draft(id)
	}
	head = groupLine(ansi.ReplaceAllString(b.View(), ""), "rb")
	if !strings.Contains(head, "ending") {
		t.Errorf("an emptying tier should read as ending, got %q", head)
	}
	if strings.Contains(head, "safe to wait") {
		t.Errorf("cliff copy must win over safe to wait, got %q", head)
	}
}

// With two tiers about to vanish at once, the cliff banner must name the
// position bleeding more value — not whichever comes first in display order.
// rb's man on the edge is backed by a near-equal survivor, so waiting costs 5;
// wr's fallback is worth half of what is leaving, so waiting costs 40. Display
// order would say rb; urgency says wr.
func TestCliffBannerPicksTheUrgentPosition(t *testing.T) {
	b := Board{State: cliffState(), Width: 92, Height: 40}
	view := ansi.ReplaceAllString(b.View(), "")

	if !strings.Contains(view, "wr tier 1 unlikely to hold") {
		t.Errorf("banner should name wr, the higher-urgency cliff:\n%s", view)
	}
	if strings.Contains(view, "rb tier 1") {
		t.Errorf("banner named rb, the display-order pick, not the urgent one:\n%s", view)
	}
}

// "last one" is a claim about the count, and the count no longer fires the
// alarm — the probability the tier holds does, and that can go red with two men
// left. Both wordings therefore have to be right in the same frame: rb has one
// man on the edge and is still the last one, wr has two and is not.
func TestCliffCopyMatchesTheRemainingCount(t *testing.T) {
	b := Board{State: cliffState(), Width: 92, Height: 40}
	view := ansi.ReplaceAllString(b.View(), "")

	if head := groupLine(view, "rb"); !strings.Contains(head, "last one in tier 1") {
		t.Errorf("one man left is still the last one, got %q", head)
	}
	head := groupLine(view, "wr")
	if !strings.Contains(head, "tier 1 unlikely to hold — holds") {
		t.Errorf("two contested men should read as a tier unlikely to hold, got %q", head)
	}
	if strings.Contains(head, "last one") {
		t.Errorf("two players are not the last one, got %q", head)
	}
}

// The banner and the group header describe the same tier in the same frame, so
// they cannot disagree. "act now or lose it" is a claim about the tier holding,
// and probability-driven cliff levels made runs onto tiers that will comfortably
// keep routine: on the scripted mock, 21 of 41 run frames had the run position
// tagged "safe to wait" while the banner shouted at the reader to act.
//
// A six-team board keeps the vantage short — slot 5 picks at 5 and 8, so two
// picks intervene — and the two men in rb tier 1 sit thirty picks deep in adp,
// so the run around them costs nothing. The wr and te are padding: the tilt
// solves over the whole available board and needs it to be able to supply the
// picks that actually happen.
func TestRunBannerDoesNotShoutAtATierThatHolds(t *testing.T) {
	players, add := newBoard()
	add("r1", "RB", 100, 30, 6, 1) // the tier the run never touches
	add("r2", "RB", 96, 32, 6, 1)
	for i := 1; i <= 4; i++ { // the run itself, drafted below
		add(fmt.Sprintf("rd%d", i), "RB", 60-i, float64(i+1), 3, 4)
	}
	add("w1", "WR", 80, 6, 3, 1)
	add("w2", "WR", 70, 9, 4, 1)
	add("t1", "TE", 50, 10, 4, 1)
	s := engine.New(players, 6, 15, 5)
	for _, id := range []string{"rd1", "rd2", "rd3", "rd4", "w1"} {
		s.Draft(id)
	}
	view := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40}.View(), "")

	if !strings.Contains(view, "rb run in progress") {
		t.Fatalf("fixture proves nothing without a run banner:\n%s", view)
	}
	head := groupLine(view, "rb")
	if !strings.Contains(head, "safe to wait") {
		t.Fatalf("fixture proves nothing unless the header calls rb safe, got %q", head)
	}
	if strings.Contains(view, "act now or lose it") {
		t.Errorf("the tier holds and the header says so; the banner must not demand action:\n%s", view)
	}
}

// Both legs of the plan are named by their pick number, never by "now": the
// plan is computed as-if standing at my next pick but drawn on every frame, and
// standing at 1.01 here that pick is already somebody else's. A line that said
// "take a wr now" would be wrong on eleven frames out of twelve.
//
// The whole string is pinned rather than a substring, because every part of it
// has failed differently at some point: "%d.%d" instead of "%d.%02d" renders 2.3
// for 2.03, and the second leg is the half that silently reads NextPick when
// nothing checks it. engine/plan_test.go owns the argument for the pair itself;
// this owns the row.
//
// The width sweep is the other half, and the row is now the same at every one of
// them: the copy carries no optional clause, because the pair's score used to
// ride on the end and no longer does. Its widest form is 33 cells against a pane
// that is 43 at the narrowest terminal the board supports, so any difference
// across the sweep is a truncation or a wrap — either of which reads as a
// rendering fault rather than as a tight fit.
func TestPlanLineNamesBothLegsByPick(t *testing.T) {
	const legs = "plan  wr at 1.02 → rb at 2.03"
	for _, w := range []int{80, 92, 104, 140} {
		view := ansi.ReplaceAllString(
			Board{State: lookaheadState(), Width: w, Height: 40}.View(), "")
		if got := planRow(view); got != legs {
			t.Errorf("width %d: plan line = %q, want %q\n%s", w, got, legs, view)
		}
		if got := leadingBlanks(view); got != 0 {
			t.Errorf("width %d: %d blank rows above the plan, want it directly under the head", w, got)
		}
	}

	// Round 15 is my last pick of a 4-team draft, so there is no second leg to
	// plan for. The row then disappears OUTRIGHT rather than rendering empty —
	// the rule the alert banner already follows, and the difference between the
	// two is invisible to anything looking for the copy, since a reserved blank
	// carries no copy either.
	s := lookaheadState()
	s.PickNo = 58
	view := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40}.View(), "")
	if got := planRow(view); got != "" {
		t.Errorf("no second pick exists, but the board still plans: %q", got)
	}
	if got := leadingBlanks(view); got != 1 {
		t.Errorf("%d blank rows under the section head, want the group separator alone", got)
	}
}

// The header must never claim a pick that is not mine. Past my last pick of the
// draft NextPick has no answer and falls back to the final pick of the draft,
// which is behind us, so PicksUntilMine reads 0 — and 0 is also how "you're on
// the clock" is spelled. Standing at the last pick of a draft this seat finished
// two picks ago, the header read "your pick"; live it would say that on every
// frame after the user's own last pick, which is the whole tail of the draft.
//
// The count is a lie one pick earlier too, where it read "1 pick until yours"
// about a pick belonging to somebody else, so the fix is to ask whether I have a
// pick left at all rather than to special-case the zero.
func TestHeaderStopsCountingAfterMyLastPick(t *testing.T) {
	cases := []struct {
		pickNo  int
		want    string
		notWant string
	}{
		{1, "1 pick until yours", "your pick"}, // the control: my pick is 1.02
		{58, "your pick", "no picks left"},     // my last pick of the draft, slot 2 in round 15
		{59, "no picks left", "until yours"},
		{60, "no picks left", "your pick"}, // the final pick of the draft, and not mine
	}
	for _, c := range cases {
		s := lookaheadState()
		s.PickNo = c.pickNo
		view := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40}.View(), "")
		head := strings.SplitN(view, "\n", 2)[0]
		if !strings.Contains(head, c.want) {
			t.Errorf("pick %d: header %q should read %q", c.pickNo, head, c.want)
		}
		if strings.Contains(head, c.notWant) {
			t.Errorf("pick %d: header %q claims %q", c.pickNo, head, c.notWant)
		}
	}
}

// One truth on screen: every survival the ui prints is the tilted probability,
// the same number urgency, tier-hold and the safe tag consume. A frame quoting
// the raw logistic beside an ordering computed from the corrected one leaves
// nobody able to tell which number the board believed. The two are far apart on
// this fixture — 70% raw against 24% tilted — so a regression to PSurvive shows
// up on both tabs immediately.
func TestSurvivalColumnsShowTheTiltedNumber(t *testing.T) {
	s := waitState()
	for _, id := range waitPicks {
		s.Draft(id)
	}
	p := s.Players["r2"]
	tilted := fmt.Sprintf("%.0f%%", 100*s.PSurviveTilted(p))
	raw := fmt.Sprintf("%.0f%%", 100*s.PSurvive(p))
	if tilted == raw {
		t.Fatalf("fixture proves nothing: tilted and raw survival both render %s", tilted)
	}
	for _, tab := range []int{0, 1} {
		view := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40, Tab: tab}.View(), "")
		row := playerRow(view, "fake r2")
		if !strings.Contains(row, tilted) {
			t.Errorf("tab %d: row %q should carry the tilted survival %s", tab, row, tilted)
		}
		if strings.Contains(row, raw) {
			t.Errorf("tab %d: row %q carries the raw survival %s", tab, row, raw)
		}
	}
}
