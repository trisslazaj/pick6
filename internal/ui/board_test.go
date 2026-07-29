package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
)

// runState is a small board tuned so a scripted rb run genuinely moves the
// urgency math: rb1 is an elite faller (tight sigma, gone by my next pick),
// rb2-5 are the safety net the room is about to eat, and the wr tier goes so
// early that waiting on receivers is expensive until it suddenly isn't.
// qb2 sits in tier 2 on purpose: drafting qb1 must not leave a one-man tier
// behind, or a stray cliff banner fires in the pre-run frame.
func runState() *engine.State {
	players := map[string]engine.Player{}
	add := func(id, pos string, value int, adp, sigma float64, tier int) {
		players[id] = engine.Player{ID: id, Name: "fake " + id, Pos: pos, Team: "AAA",
			Value: value, ADP: adp, Sigma: sigma, Tier: tier, Bye: 7}
	}
	add("rb1", "RB", 100, 4, 2, 1)
	add("rb2", "RB", 98, 26, 4, 1)
	add("rb3", "RB", 96, 30, 5, 1)
	add("rb4", "RB", 94, 34, 5, 1)
	add("rb5", "RB", 92, 38, 6, 1)
	add("rb6", "RB", 55, 60, 8, 2)
	add("wr1", "WR", 95, 2, 2, 1)
	add("wr2", "WR", 93, 3, 2, 1)
	add("wr3", "WR", 90, 8, 3, 1)
	add("wr4", "WR", 88, 15, 4, 1)
	add("wr5", "WR", 60, 35, 6, 2)
	add("wr6", "WR", 58, 40, 6, 2)
	add("qb1", "QB", 85, 10, 3, 1)
	add("qb2", "QB", 80, 25, 5, 2)
	add("qb3", "QB", 50, 55, 8, 3)
	add("te1", "TE", 70, 28, 5, 1)
	add("te2", "TE", 40, 70, 9, 2)
	return engine.New(players, 12, 15, 3)
}

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

// The milestone 4 DoD: a scripted rb run flips the banner and re-sorts the
// board. Frame A has receivers evaporating (wr on top, no banner); six picks
// later four rbs are gone, the banner is live, rb owns the board, and the wr
// group — its survivors now safe — has collapsed to "safe to wait".
func TestScriptedRunFlipsBannerAndResortsBoard(t *testing.T) {
	s := runState()
	for _, id := range []string{"wr1", "wr2", "qb1"} { // picks 1-3; pick 3 is mine
		s.Draft(id)
	}
	b := Board{State: s, Width: 92, Height: 40}
	before := ansi.ReplaceAllString(b.View(), "")

	if strings.Contains(before, "run in progress") {
		t.Fatalf("no run has happened yet, but the banner is up:\n%s", before)
	}
	// wr urgency 30: bestNow wr3 won't reach pick 22, bestLater is tier-2 wr5.
	// rb urgency 2: rb2 safely backs up the fallen rb1.
	if got := topGroup(before); got != "wr" {
		t.Errorf("before the run, top group = %q, want wr", got)
	}

	// picks 4-9: the room eats the safe mid rbs. four of the last six.
	for _, id := range []string{"wr3", "rb2", "rb3", "wr4", "rb4", "rb5"} {
		s.Draft(id)
	}
	after := ansi.ReplaceAllString(b.View(), "")

	if !strings.Contains(after, "rb run in progress") {
		t.Errorf("expected the rb run banner, got:\n%s", after)
	}
	// rb urgency 45: only the doomed rb1 and tier-2 rb6 remain. wr urgency 0:
	// its survivors are safe now, which is exactly why the board flips.
	if got := topGroup(after); got != "rb" {
		t.Errorf("after the run, top group = %q, want rb", got)
	}
	if !strings.Contains(after, "safe to wait") {
		t.Errorf("the collapsed wr group should read safe to wait, got:\n%s", after)
	}
}

// Cliff copy always beats the safe-to-wait tag: a tier that is emptying is not
// safe, even when its best player would survive.
func TestSafeToWaitYieldsToCliffCopy(t *testing.T) {
	players := map[string]engine.Player{}
	for i, id := range []string{"r1", "r2", "r3"} {
		players[id] = engine.Player{ID: id, Name: "fake " + id, Pos: "RB", Team: "AAA",
			Value: 100 - i, ADP: 50 + float64(5*i), Sigma: 6, Tier: 1, Bye: 7}
	}
	s := engine.New(players, 12, 15, 3)
	b := Board{State: s, Width: 92, Height: 40}

	untouched := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(untouched, "safe to wait") {
		t.Errorf("an untouched tier of safe players should read safe to wait:\n%s", untouched)
	}

	s.Draft("r1") // 2 of 3 left: warning territory, urgency still 0
	warned := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(warned, "ending") {
		t.Errorf("an emptying tier should read as ending:\n%s", warned)
	}
	if strings.Contains(warned, "safe to wait") {
		t.Errorf("cliff copy must win over safe to wait:\n%s", warned)
	}
}

// With two last-men-standing at once, the cliff banner must name the position
// bleeding more value — not whichever comes first in display order. rb's
// survivor here is safe (urgency 0); wr's is evaporating. Display order would
// say rb; urgency says wr.
func TestCliffBannerPicksTheUrgentPosition(t *testing.T) {
	players := map[string]engine.Player{}
	add := func(id, pos string, value int, adp, sigma float64, tier int) {
		players[id] = engine.Player{ID: id, Name: "fake " + id, Pos: pos, Team: "AAA",
			Value: value, ADP: adp, Sigma: sigma, Tier: tier, Bye: 7}
	}
	add("ra", "RB", 100, 2, 2, 1)
	add("rb2", "RB", 90, 60, 8, 1) // survives easily: safe cliff
	add("wa", "WR", 100, 2, 2, 1)
	add("wb2", "WR", 98, 1, 0.5, 1) // gone by my pick: urgent cliff
	add("wc", "WR", 40, 60, 8, 2)
	s := engine.New(players, 12, 15, 3)
	s.Draft("ra") // both tiers now emptying: CliffLast on rb and wr
	s.Draft("wa")
	b := Board{State: s, Width: 92, Height: 40}
	view := ansi.ReplaceAllString(b.View(), "")

	if !strings.Contains(view, "wr tier 1 — last one") {
		t.Errorf("banner should name wr, the higher-urgency cliff:\n%s", view)
	}
	if strings.Contains(view, "rb tier 1 — last one") {
		t.Errorf("banner named rb, the display-order pick, not the urgent one:\n%s", view)
	}
}

// The survival column is the engine's number on screen: a player whose adp is
// exactly my next pick is a coin flip and must render as 50%.
func TestPlayerRowShowsSurvival(t *testing.T) {
	players := map[string]engine.Player{
		"r1": {ID: "r1", Name: "fake r1", Pos: "RB", Team: "AAA",
			Value: 100, ADP: 3, Sigma: 6, Tier: 1, Bye: 7}, // my first pick is 3
	}
	s := engine.New(players, 12, 15, 3)
	b := Board{State: s, Width: 92, Height: 40}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, " 50%") {
		t.Errorf("a coin-flip player should render 50%%, got:\n%s", view)
	}
}
