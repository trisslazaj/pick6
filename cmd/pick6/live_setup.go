package main

import (
	"fmt"
	"strings"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/fpl"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// live_setup.go is the only sport-shaped part of `pick6 live`.
//
// Everything past it — the board, the brain, the replay frame, the poll loop —
// is one code path, because the engine never asks where a pick came from and
// ui.LiveModel never asks whose api it is talking to. What differs is the four
// facts below plus which feed produces them, and they differ enough (sleeper
// hands you a draft object with a validated type and a slot-to-roster map; fpl
// hands you a league and derives the order from round one as it happens) that
// one function with two branches inside it would be two functions wearing a
// trench coat.
type liveDraft struct {
	teams, rounds, mySlot int
	roster                engine.Roster
	feed                  sleeper.Feed
	// draft is sleeper's metadata, for resolving a traded pick to the roster
	// that receives it. nil under fpl, where there are no draft-pick trades and
	// the seat that picks is always the seat that keeps.
	draft  *sleeper.Draft
	header string

	// drop is the ids to take OFF the board before the state is built: players
	// the pool still lists who can no longer be drafted. Empty for sleeper.
	drop []string
}

func liveSetup(sp sport, id, user string, slot int, replay bool) (liveDraft, error) {
	if sp.name == fplSport.name {
		return fplLive(sp, id, user, slot, replay)
	}
	return sleeperLive(id, user, slot)
}

func sleeperLive(id, user string, slot int) (liveDraft, error) {
	draft, err := sleeper.GetDraft(id)
	if err != nil {
		return liveDraft{}, err
	}
	if err := draft.Validate(); err != nil {
		return liveDraft{}, err
	}
	mySlot, err := resolveSlot(draft, user, slot)
	if err != nil {
		return liveDraft{}, err
	}
	roster := engine.DefaultRoster
	if slots := draft.RosterSlots(); len(slots) > 0 {
		roster = engine.Roster{Slots: slots, Bench: draft.Settings.SlotsBench}
	}
	return liveDraft{
		teams: draft.Settings.Teams, rounds: draft.Settings.Rounds, mySlot: mySlot,
		roster: roster,
		feed:   sleeper.NewFeed(id),
		draft:  draft,
		header: fmt.Sprintf("draft %s — %d teams, %d rounds, %s",
			id, draft.Settings.Teams, draft.Settings.Rounds, draft.Status),
	}, nil
}

// fplLive reads the room out of league/{id}/details and the squad shape out of
// bootstrap-static, so a change to fpl's own quota moves the roster with it
// rather than leaving a fifteen-man constant lying about being current.
//
// THE ID IS THE LEAGUE ID. league/4250/details names a draft 4512, and
// draft/4512/choices returns a real, populated feed — for a stranger's league
// 4512. It would poll somebody else's draft all night looking exactly like it
// was working. See fpl.GetChoices.
func fplLive(sp sport, id, user string, slot int, replay bool) (liveDraft, error) {
	league, err := fpl.GetLeague(id)
	if err != nil {
		return liveDraft{}, err
	}
	bs, _, err := fpl.GetBootstrap(fplMaxAge)
	if err != nil {
		return liveDraft{}, err
	}
	roster := fplRoster(bs.Settings.Squad)

	teams := league.Teams()
	if teams < 2 {
		return liveDraft{}, fmt.Errorf("league %s has %d managers in it", id, teams)
	}

	mySlot, err := fplSlot(league, id, user, slot, teams)
	if err != nil {
		return liveDraft{}, err
	}

	status := "waiting to start"
	if league.Complete() {
		status = "complete"
		if !replay {
			// The loudest guard available against the id trap. Passing the DRAFT
			// id instead of the league id returns a real, populated feed for a
			// stranger's league with that number — it does not error and it does
			// not come back empty, so the only tells are the league's NAME in
			// the header and this. A draft you are about to sit through is not
			// one that has already finished; if it says complete, you are
			// pointed at somebody else's.
			return liveDraft{}, fmt.Errorf(
				"league %s (%s) has already drafted — check the id, and note that it is the "+
					"LEAGUE id, not the draft id the league details name. -replay to look at it anyway",
				id, strings.ToLower(league.Info.Name))
		}
	}
	return liveDraft{
		teams: teams, rounds: len(roster.Slots), mySlot: mySlot,
		roster: roster,
		feed:   fpl.NewFeed(id, bs.Roll()),
		draft:  nil, // no trades in an fpl draft: owner is always the seat
		drop:   bs.Departed(),
		header: fmt.Sprintf("league %s — %s, %d managers, %d rounds, %s",
			id, strings.ToLower(league.Info.Name), teams, len(roster.Slots), status),
	}, nil
}

// fplSlot answers "which seat am I", which fpl only publishes by drafting.
//
// The order is assigned at draft start and appears nowhere in the api before
// round one begins, so -slot is the answer pre-draft — the same gap an unseeded
// sleeper draft has, with the same fix. -user works the moment the feed shows
// that manager picking, which is what a mid-draft reconnect actually needs.
func fplSlot(league *fpl.League, id, user string, slot, teams int) (int, error) {
	if slot > teams {
		return 0, fmt.Errorf("slot %d is outside a %d-manager league", slot, teams)
	}
	if slot <= 0 && user == "" {
		return 0, fmt.Errorf("pass -slot N (fpl publishes the draft order only as round one happens) " +
			"or -user <your name> once you have picked")
	}
	if user == "" {
		// Nothing to check it against. A -slot nobody can verify runs a
		// confident board off it for 150 picks, which is the residual risk of a
		// draft order that exists nowhere until it happens — the same one an
		// unseeded sleeper draft has. Pass -user too once you have made a pick
		// and the feed settles it.
		return slot, nil
	}
	entry, err := league.FindEntry(user)
	if err != nil {
		return 0, err
	}
	choices, err := fpl.GetChoices(id)
	if err != nil {
		return 0, err
	}
	seat, ok := fpl.SeatOfEntry(choices, entry.EntryID)
	switch {
	case ok && slot > 0 && seat != slot:
		// The one place the seat can be CHECKED rather than believed, and the
		// disagreement matters more than either answer: every survival number,
		// every need weight and the whole roster pane hang off which seat is
		// mine, and being wrong about it is a board that is confidently wrong
		// all night rather than obviously broken.
		return 0, fmt.Errorf("-slot %d disagrees with the feed: %s picked at seat %d in round one",
			slot, strings.ToLower(entry.Manager()), seat)
	case ok:
		return seat, nil
	case slot > 0:
		return slot, nil // round one has not reached them yet; -slot stands
	}
	return 0, fmt.Errorf("%s has not picked yet, so their seat is not in the feed — pass -slot N",
		strings.ToLower(entry.Manager()))
}

// fplRoster turns the squad quota into a lineup: every slot dedicated, in
// position order, no bench and no flex.
func fplRoster(sq fpl.Squad) engine.Roster {
	r := engine.Roster{Bench: 0, Quota: true, Hold: engine.NoHold}
	for _, c := range []struct {
		pos string
		n   int
	}{{"GKP", sq.GKP}, {"DEF", sq.DEF}, {"MID", sq.MID}, {"FWD", sq.FWD}} {
		for i := 0; i < c.n; i++ {
			r.Slots = append(r.Slots, c.pos)
		}
	}
	if len(r.Slots) == 0 {
		// bootstrap-static has always carried the quota, but a board with no
		// lineup has no need weights, no urgency and no roster pane — falling
		// back to the known shape beats rendering an empty one.
		return engine.FPLRoster
	}
	return r
}

// ownerSlotFunc resolves a traded pick to the roster that receives it, or nil
// when the feed has no trades to resolve.
func ownerSlotFunc(d *sleeper.Draft) func(sleeper.DraftPick) int {
	if d == nil {
		return nil
	}
	return d.OwnerSlot
}
