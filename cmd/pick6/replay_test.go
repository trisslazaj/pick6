package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// loadDraft returns a real completed draft's metadata and picks.
//
// Offline by default: reads the JSON Sleeper served us on 2026-08-17 out of
// testdata/, decoded with the same exported types GetDraft/GetPicks use, so
// this is a byte-for-byte replay of what the network path would have parsed.
// Set PICK6_LIVE_TESTS=1 to hit Sleeper instead — useful to notice if the API
// shape ever moves, but not something CI should depend on. In that mode alone
// a network error skips rather than fails.
func loadDraft(t *testing.T, id string) (*sleeper.Draft, []sleeper.DraftPick) {
	t.Helper()

	if os.Getenv("PICK6_LIVE_TESTS") == "1" {
		d, err := sleeper.GetDraft(id)
		if err != nil {
			t.Skipf("network unavailable: %v", err)
		}
		picks, err := sleeper.GetPicks(id)
		if err != nil {
			t.Skipf("network unavailable: %v", err)
		}
		return d, picks
	}

	var d sleeper.Draft
	readTestdataJSON(t, "testdata/draft_"+id+".json", &d)
	var picks []sleeper.DraftPick
	readTestdataJSON(t, "testdata/picks_"+id+".json", &picks)
	return &d, picks
}

func readTestdataJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run with PICK6_LIVE_TESTS=1 to refetch)", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}

// Replays a real completed draft from the user's own league through the whole
// live path: metadata -> roster shape -> every pick -> snake-math cross-check.
//
// This is the strongest test of the snake math we have. The synthetic tests
// assert our formulas agree with themselves; this asserts they agree with what
// Sleeper actually did across 192 real picks.
func TestReplayRealDraft(t *testing.T) {
	const (
		draftID = "1261824503076360192" // 12-team half-ppr, 2025, completed
		userID  = "1133491374289981440"
	)

	d, picks := loadDraft(t, draftID)
	if err := d.Validate(); err != nil {
		t.Fatalf("real draft failed validation: %v", err)
	}
	mySlot, ok := d.SlotOf(userID)
	if !ok {
		t.Fatal("could not find the user's slot in draft_order")
	}

	players := map[string]engine.Player{}
	for _, p := range picks {
		players[p.PlayerID] = engine.Player{
			ID: p.PlayerID, Pos: p.Metadata.Position, Team: p.Metadata.Team,
			Name: p.Metadata.FirstName + " " + p.Metadata.LastName,
		}
	}
	s := engine.New(players, d.Settings.Teams, d.Settings.Rounds, mySlot)
	s.SetRoster(engine.Roster{Slots: d.RosterSlots(), Bench: d.Settings.SlotsBench})

	// The headline assertion: our SlotAt must agree with the feed's draft_slot
	// on every single pick, or ApplyRemote refuses.
	for _, p := range picks {
		if err := s.ApplyRemote(engine.RemotePick{
			PickNo: p.PickNo, Round: p.Round, Slot: p.DraftSlot,
			OwnerSlot: d.OwnerSlot(p), PlayerID: p.PlayerID,
		}); err != nil {
			t.Fatalf("snake math disagrees with a real draft: %v", err)
		}
	}

	if got, want := len(s.Picks), len(picks); got != want {
		t.Errorf("applied %d picks, feed had %d", got, want)
	}
	if got, want := len(s.Rosters[mySlot]), d.Settings.Rounds; got != want {
		t.Errorf("my roster holds %d players, expected one per round (%d)", got, want)
	}

	// Every team should end up with exactly `rounds` players.
	for slot := 1; slot <= d.Settings.Teams; slot++ {
		if got := len(s.Rosters[slot]); got != d.Settings.Rounds {
			t.Errorf("slot %d holds %d players, expected %d", slot, got, d.Settings.Rounds)
		}
	}

	// Lineup assignment must be sound: nobody in a slot they can't fill, and
	// nobody lost between the roster and filled+bench.
	filled, bench := s.FilledSlots(mySlot)
	n := 0
	for i, id := range filled {
		if id == "" {
			continue
		}
		n++
		if pos := s.Players[id].Pos; !engine.EligibleFor(s.Roster.Slots[i], pos) {
			t.Errorf("%s is in a %s slot", pos, s.Roster.Slots[i])
		}
	}
	if n+len(bench) != len(s.Rosters[mySlot]) {
		t.Errorf("lineup accounting lost players: %d filled + %d bench != %d drafted",
			n, len(bench), len(s.Rosters[mySlot]))
	}
	t.Logf("replayed %d picks, %d teams, %d rounds, lineup %v — no desync",
		len(picks), d.Settings.Teams, d.Settings.Rounds, s.Roster.Slots)
}

// Draft picks get traded, and when they do the player goes to a roster other
// than the one owning that draft slot. This user's real 2025 draft has 9 such
// picks out of 192, so attributing by draft slot alone silently puts players on
// the wrong team — including picks you traded for never showing on your board.
func TestReplayHandlesTradedPicks(t *testing.T) {
	const draftID = "1261824503076360192"

	d, picks := loadDraft(t, draftID)

	traded := 0
	for _, p := range picks {
		if d.OwnerSlot(p) != p.DraftSlot {
			traded++
		}
	}
	if traded == 0 {
		t.Fatal("expected this draft to contain traded picks; the fixture may have changed")
	}
	t.Logf("%d of %d picks went to a roster other than their draft slot", traded, len(picks))

	// Every pick must still resolve to a real seat.
	for _, p := range picks {
		if got := d.OwnerSlot(p); got < 1 || got > d.Settings.Teams {
			t.Errorf("pick %d resolved to slot %d, outside 1..%d", p.PickNo, got, d.Settings.Teams)
		}
	}

	// And rosters must stay conserved: total players placed equals total picks.
	players := map[string]engine.Player{}
	for _, p := range picks {
		players[p.PlayerID] = engine.Player{ID: p.PlayerID, Pos: p.Metadata.Position}
	}
	s := engine.New(players, d.Settings.Teams, d.Settings.Rounds, 1)
	for _, p := range picks {
		if err := s.ApplyRemote(engine.RemotePick{
			PickNo: p.PickNo, Round: p.Round, Slot: p.DraftSlot,
			OwnerSlot: d.OwnerSlot(p), PlayerID: p.PlayerID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	total := 0
	for slot := 1; slot <= d.Settings.Teams; slot++ {
		total += len(s.Rosters[slot])
	}
	if total != len(picks) {
		t.Errorf("rosters hold %d players, draft made %d picks", total, len(picks))
	}
}
