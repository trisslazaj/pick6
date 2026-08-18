package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// fakeFeed serves canned snapshots so the live path is testable offline.
type fakeFeed struct {
	snaps []sleeper.Snapshot
	errs  []error
	calls int
}

func (f *fakeFeed) Poll() (sleeper.Snapshot, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return sleeper.Snapshot{}, f.errs[i]
	}
	if i >= len(f.snaps) {
		i = len(f.snaps) - 1
	}
	return f.snaps[i], nil
}

// pick builds a feed pick landing on whichever slot the snake says owns it, so
// tests describe intent rather than recomputing draft order by hand.
func pick(s *engine.State, pickNo int, playerID, pos string) sleeper.DraftPick {
	p := sleeper.DraftPick{
		PickNo:    pickNo,
		Round:     s.Round(pickNo),
		DraftSlot: s.SlotAt(pickNo),
		PlayerID:  playerID,
	}
	p.Metadata.FirstName = "fake"
	p.Metadata.LastName = playerID
	p.Metadata.Position = pos
	p.Metadata.Team = "AAA"
	return p
}

func liveModel(s *engine.State, f sleeper.Feed) LiveModel {
	return NewLiveModel(s, f, 2, false)
}

func sendLive(m LiveModel, msg tea.Msg) LiveModel {
	next, _ := m.Update(msg)
	return next.(LiveModel)
}

func TestLiveAppliesPicks(t *testing.T) {
	s := testState()
	feed := &fakeFeed{snaps: []sleeper.Snapshot{{
		Status: "drafting",
		Picks: []sleeper.DraftPick{
			pick(s, 1, "RB00", "RB"),
			pick(s, 2, "WR00", "WR"),
		},
	}}}
	m := liveModel(s, feed)
	snap, _ := feed.Poll()
	m = sendLive(m, pollMsg{snap: snap})

	if got := len(s.Picks); got != 2 {
		t.Fatalf("applied %d picks, want 2", got)
	}
	if s.PickNo != 3 {
		t.Errorf("pick number = %d, want 3", s.PickNo)
	}
	if !s.Taken["RB00"] || !s.Taken["WR00"] {
		t.Error("drafted players should be marked taken")
	}
}

// Polling returns the whole pick list every time, so re-applying must be a no-op.
func TestLivePollingIsIdempotent(t *testing.T) {
	s := testState()
	snap := sleeper.Snapshot{Status: "drafting", Picks: []sleeper.DraftPick{
		pick(s, 1, "RB00", "RB"),
		pick(s, 2, "WR00", "WR"),
	}}
	m := liveModel(s, &fakeFeed{snaps: []sleeper.Snapshot{snap}})

	m = sendLive(m, pollMsg{snap: snap})
	m = sendLive(m, pollMsg{snap: snap})
	m = sendLive(m, pollMsg{snap: snap})

	if got := len(s.Picks); got != 2 {
		t.Errorf("after 3 identical polls, %d picks recorded; want 2", got)
	}
	if got := len(s.Rosters[s.SlotAt(1)]); got != 1 {
		t.Errorf("slot roster grew to %d on repeat polls; want 1", got)
	}
}

// If the feed's draft order disagrees with our snake math, every survival number
// downstream is wrong. The board must say so rather than keep drawing.
func TestLiveDetectsDesync(t *testing.T) {
	s := testState()
	bad := pick(s, 1, "RB00", "RB")
	bad.DraftSlot = bad.DraftSlot%s.Teams + 1 // deliberately the wrong seat

	m := liveModel(s, &fakeFeed{})
	m = sendLive(m, pollMsg{snap: sleeper.Snapshot{
		Status: "drafting", Picks: []sleeper.DraftPick{bad},
	}})

	if m.desync == "" {
		t.Fatal("a draft-order mismatch should be reported")
	}
	if !strings.Contains(m.View(), "not trustworthy") {
		t.Error("the view should warn that the board can't be trusted")
	}
	if len(s.Picks) != 0 {
		t.Error("no picks should be applied once desync is detected")
	}
}

// Flaky wifi at a draft party is normal. A failed poll must keep the last good
// board on screen and retry, not crash or blank out.
func TestLiveSurvivesPollErrors(t *testing.T) {
	s := testState()
	good := sleeper.Snapshot{Status: "drafting", Picks: []sleeper.DraftPick{
		pick(s, 1, "RB00", "RB"),
	}}
	m := liveModel(s, &fakeFeed{})
	m = sendLive(m, pollMsg{snap: good})

	m = sendLive(m, pollMsg{err: errors.New("dial tcp: no route to host")})
	if m.pollErr == "" {
		t.Error("poll error should be recorded")
	}
	if len(s.Picks) != 1 {
		t.Error("the previously applied pick should survive a failed poll")
	}
	if !strings.Contains(m.View(), "your roster") {
		t.Error("the last good board should stay on screen")
	}

	// A later success clears the error.
	m = sendLive(m, pollMsg{snap: good})
	if m.pollErr != "" {
		t.Errorf("a successful poll should clear the error, got %q", m.pollErr)
	}
}

// A live draft picks players outside our ADP board constantly — 192 picks against
// a 201-player list. They must render, not appear as blank rows.
func TestLiveRegistersUnknownPlayers(t *testing.T) {
	s := testState()
	p := pick(s, 1, "nobody-we-know", "RB")
	m := liveModel(s, &fakeFeed{})
	m = sendLive(m, pollMsg{snap: sleeper.Snapshot{
		Status: "drafting", Picks: []sleeper.DraftPick{p},
	}})

	got, ok := s.Players["nobody-we-know"]
	if !ok {
		t.Fatal("an unknown drafted player should be registered")
	}
	if got.Name == "" {
		t.Error("registered player should carry a name from the feed metadata")
	}
	if got.Tier != 0 || got.Value != 0 {
		t.Error("an unranked player must stay untiered, so cliff logic ignores him")
	}
}

// Anything drawn under the board has to be charged to the board's height budget
// before the board is drawn. It was appended afterwards, so a stale board came
// out exactly one row taller than the terminal: at 104x40 the frame went 40 -> 41
// lines. Unlike a poll error that clears on the next tick, the stale line is
// sticky by design, so the state the warning exists for was also the state that
// scrolled the header — the round, the pick, the clock — off the top for the
// whole draft.
//
// Both tabs, because the data tab sizes its own row count off Height too. The
// heights below straddle the depth knob's steps; 28 is deliberately absent,
// since there the left pane is already pinned at MinDepth and has no row to give
// (a 24-line terminal renders 28 rows at HEAD — the separate, pre-existing
// overflow).
//
// Counted with rowCount, not strings.Count of newlines: the models used to
// append a trailing newline, which made the two agree by accident. It is gone —
// bubbletea counts that newline as a line and clips the header to fit — and a
// test that measures separators instead of rows is one row too generous.
//
// Tab 1 at 18 and 20 is here because visibleDataRows floors at 8 and cannot
// shrink further, so the data pane overflowed once Reserve was charged for a
// two-line trailer: it was exempt from the clamp on the assumption it always fit.
func TestStaleLineFitsInsideTheHeightBudget(t *testing.T) {
	cases := []struct {
		tab, height int
	}{
		{0, 30}, {0, 33}, {0, 37}, {0, 40}, {0, 41}, {0, 48}, {0, 60},
		{1, 18}, {1, 20}, {1, 24}, {1, 30}, {1, 40}, {1, 60},
	}
	for _, c := range cases {
		m := liveModel(testState(), &fakeFeed{})
		m.board.Width, m.board.Height, m.board.Tab = 104, c.height, c.tab
		base := rowCount(m.View())
		stale := rowCount(
			m.WithFreshness(Freshness{
				FetchedAt: time.Now().Add(-(engine.StaleADPHours + 7) * time.Hour),
			}).View())

		if base > c.height {
			t.Fatalf("tab %d h %d: the plain board already renders %d rows", c.tab, c.height, base)
		}
		if stale > c.height {
			t.Errorf("tab %d h %d: stale board renders %d rows, over the terminal by %d",
				c.tab, c.height, stale, stale-c.height)
		}
		if !strings.Contains(ansi.ReplaceAllString(
			m.WithFreshness(Freshness{FetchedAt: time.Now().Add(-31 * time.Hour)}).View(), ""),
			"stale — adp and injury flags are") {
			t.Errorf("tab %d h %d: the warning itself went missing", c.tab, c.height)
		}
	}
}

func TestLiveStopsPollingWhenComplete(t *testing.T) {
	s := testState()
	m := liveModel(s, &fakeFeed{})
	next, cmd := m.Update(pollMsg{snap: sleeper.Snapshot{Status: "complete"}})
	lm := next.(LiveModel)

	if !lm.complete {
		t.Error("a complete status should be recorded")
	}
	if cmd != nil {
		t.Error("polling should stop once the draft is complete")
	}
	if !strings.Contains(lm.View(), "polling stopped") {
		t.Error("the view should say polling has stopped")
	}
}

// The whole fpl live path through the model, with no fpl in it: a feed that
// returns snapshots, no sleeper draft metadata attached, and a quota squad.
//
// It exists because that combination is only ever exercised against the network
// otherwise, and the two things it proves are the two the port rests on — the
// model never asks whose api it is talking to, and with no draft metadata the
// seat that picks is the seat that keeps, which is fpl's rule always.
func TestLiveModelDrivesAQuotaSquadWithNoDraftMetadata(t *testing.T) {
	players := map[string]engine.Player{}
	for _, pos := range []string{"GKP", "DEF", "MID", "FWD"} {
		for i := 0; i < 40; i++ {
			id := pos + itoa2(i)
			players[id] = engine.Player{ID: id, Name: strings.ToLower(id), Pos: pos,
				Team: "AAA", Tier: i/4 + 1, Value: 9000 - i*100, ADP: float64(i + 1), Sigma: 6}
		}
	}
	s := engine.New(players, 10, 15, 3)
	s.SetRoster(engine.FPLRoster)

	var picks []sleeper.DraftPick
	for n := 1; n <= 12; n++ {
		pos := []string{"MID", "FWD", "DEF"}[n%3]
		picks = append(picks, pick(s, n, pos+itoa2(n), pos))
	}
	feed := &fakeFeed{snaps: []sleeper.Snapshot{{Status: "drafting", Picks: picks}}}

	// No WithDraft: there is nothing to resolve a traded pick against, and fpl
	// has no trades to resolve.
	m := NewLiveModel(s, feed, 3, true)
	m2, _ := m.Update(m.Init()())
	m = m2.(LiveModel)

	if m.desync != "" {
		t.Fatalf("desync on a clean snake: %s", m.desync)
	}
	if m.applied != 12 {
		t.Fatalf("applied %d of 12 picks", m.applied)
	}
	// Every pick landed on the seat that made it, which is the nil-draft rule.
	for _, p := range picks {
		found := false
		for _, id := range s.Rosters[p.DraftSlot] {
			found = found || id == p.PlayerID
		}
		if !found {
			t.Errorf("pick %d (%s) did not land on seat %d", p.PickNo, p.PlayerID, p.DraftSlot)
		}
	}
	// And the frame renders the squad's own vocabulary rather than football's.
	view := ansi.ReplaceAllString(m.View(), "")
	for _, want := range []string{"gkp", "def", "mid", "fwd"} {
		if !strings.Contains(view, want) {
			t.Errorf("the frame never mentions %s", want)
		}
	}
	for _, never := range []string{" rb ", " wr ", " qb ", " te "} {
		if strings.Contains(view, never) {
			t.Errorf("the frame mentions %q on a premier league board", never)
		}
	}
}

func itoa2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
