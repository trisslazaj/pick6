package main

import (
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// Replays a real completed draft from the user's own league through the whole
// live path: metadata -> roster shape -> every pick -> snake-math cross-check.
//
// This is the strongest test of the snake math we have. The synthetic tests
// assert our formulas agree with themselves; this asserts they agree with what
// Sleeper actually did across 192 real picks. Skips when offline.
func TestReplayRealDraft(t *testing.T) {
	const (
		draftID = "1261824503076360192" // 12-team half-ppr, 2025, completed
		userID  = "1133491374289981440"
	)

	d, err := sleeper.GetDraft(draftID)
	if err != nil {
		t.Skipf("network unavailable: %v", err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("real draft failed validation: %v", err)
	}
	picks, err := sleeper.GetPicks(draftID)
	if err != nil {
		t.Skipf("network unavailable: %v", err)
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
		if err := s.ApplyRemote(p.PickNo, p.Round, p.DraftSlot, p.PlayerID); err != nil {
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
