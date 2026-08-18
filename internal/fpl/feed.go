package fpl

import (
	"fmt"
	"strconv"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The feed returns sleeper.Snapshot, and that import is deliberate rather than
// an accident of who was written first. Snapshot and DraftPick are the ui's feed
// CONTRACT — ui.LiveModel takes a sleeper.Feed, nil-guards the sleeper draft it
// uses for traded picks, and reads Status off the snapshot rather than off any
// sleeper type. So an fpl feed is one more implementation of an interface that
// already exists, and moving three types into a neutral package to spare this
// one import would be a rename with a draft on Thursday at the end of it.

// Feed polls an FPL draft. Same shape as the sleeper feed: whole list every
// time, no since parameter, idempotent application, so a dropped poll self-heals
// instead of leaving a hole.
type feed struct {
	leagueID string
	seats    map[int]int // entry id -> slot, learned from round one
	// who names the drafted player, because the choices feed does not. Its
	// player_first_name / player_last_name are the MANAGER's, which is a trap
	// worth one comment: they look exactly like the field you want.
	//
	// The map is the WHOLE bootstrap, not the draftable pool, and that is the
	// point of it. A player who has since left the premier league is filtered off
	// the board correctly — nobody can draft him — but a draft that ALREADY took
	// him still needs him named, or he lands on the drafting roster as "unknown
	// player" with no position, fills no squad slot, and leaves a hole in a
	// lineup that is actually complete. Measured: exactly one of league 2400's
	// 105 picks.
	who    map[int]Named
	status string
	polls  int
}

// Named is the minimum a pick needs to say about the player it took.
type Named struct{ Name, Pos, Team string }

// Roll builds the name lookup over every element, filtered or not.
func (b *Bootstrap) Roll() map[int]Named {
	out := make(map[int]Named, len(b.Elements))
	for _, e := range b.Elements {
		out[e.ID] = Named{Name: e.Name(), Pos: b.Pos(e), Team: b.TeamCode(e)}
	}
	return out
}

// statusEvery is how many polls pass between league-metadata fetches. FPL has no
// status on the choices feed itself, so "is it over" costs a second request —
// which is worth paying every tenth poll and not worth paying every third second.
const statusEvery = 10

// NewFeed returns a Feed backed by the FPL Draft API.
//
// seats may be pre-seeded from an earlier fetch of the same draft (a reconnect
// mid-draft); nil is the normal case and round one fills it in.
//
// No CDN nonce, unlike sleeper's. Sleeper's picks endpoint is served by
// cloudflare with s-maxage=15 and was measured up to 26 seconds stale mid-draft;
// FPL's was not, so the honest thing is to poll it plainly and add the trick
// only if staleness is ever observed. Measure, then bypass — inventing the
// workaround first is how you end up with a nonce nobody can justify.
func NewFeed(leagueID string, who map[int]Named) sleeper.Feed {
	return &feed{leagueID: leagueID, seats: map[int]int{}, who: who}
}

func (f *feed) Poll() (sleeper.Snapshot, error) {
	choices, err := GetChoices(f.leagueID)
	if err != nil {
		return sleeper.Snapshot{}, err
	}
	f.polls++

	if f.status == "" || f.polls%statusEvery == 0 {
		if l, err := GetLeague(f.leagueID); err == nil {
			f.status = draftStatus(l, len(choices))
		} else if f.status == "" {
			// Picks are the important half; a status blip shouldn't stall the board.
			f.status = "drafting"
		}
	} else if f.status == "pre_draft" && len(choices) > 0 {
		// The draft started between metadata fetches. Waiting nine more polls to
		// notice would be nine polls of a header saying nobody has picked while
		// picks scroll past in the ticker.
		f.status = "drafting"
	}

	picks, err := f.toPicks(choices)
	if err != nil {
		return sleeper.Snapshot{}, err
	}
	return sleeper.Snapshot{Status: f.status, Picks: picks}, nil
}

// draftStatus maps FPL's two-state draft_status onto the four-state vocabulary
// the ui already speaks. "post" is complete; "pre" with picks in the feed is a
// draft in progress that the metadata has not caught up with.
func draftStatus(l *League, picks int) string {
	if l.Complete() {
		return "complete"
	}
	if picks > 0 {
		return "drafting"
	}
	return "pre_draft"
}

// toPicks converts choices into the pick shape ApplyRemote checks.
//
// Slot comes from the entry's ROUND-ONE seat, never from the snake math, and
// that is the whole point: ApplyRemote compares this slot against SlotAt(index)
// and refuses on a mismatch. Deriving the slot from the arithmetic we are trying
// to verify would make the cross-check pass by construction and tell us nothing.
//
// There are no trades in an FPL draft — the seat that picks is the roster that
// receives, always — so OwnerSlot is left zero and ApplyRemote falls back to
// Slot, which is the same rule ui.LiveModel already applies when it has no
// sleeper draft metadata to resolve a trade against.
func (f *feed) toPicks(choices []Choice) ([]sleeper.DraftPick, error) {
	for _, c := range choices {
		if c.Round == 1 && c.Pick > 0 {
			f.seats[c.Entry] = c.Pick
		}
	}
	out := make([]sleeper.DraftPick, 0, len(choices))
	for _, c := range choices {
		slot, ok := f.seats[c.Entry]
		if !ok {
			// Round one seats every entry before round two starts, so this can
			// only mean the feed handed us a pick from a manager who never made
			// one. Refuse rather than guess a seat: a wrong seat is a wrong
			// roster, and a wrong roster is a wrong board all night.
			return nil, fmt.Errorf("pick %d is from entry %d, who has no round-one seat", c.Index, c.Entry)
		}
		p := sleeper.DraftPick{
			PickNo:    c.Index,
			Round:     c.Round,
			DraftSlot: slot,
			PlayerID:  strconv.Itoa(c.Element),
		}
		if n, ok := f.who[c.Element]; ok {
			p.Metadata.FirstName, p.Metadata.Position, p.Metadata.Team = n.Name, n.Pos, n.Team
		}
		out = append(out, p)
	}
	return out, nil
}

// Seats exposes what round one has taught the feed so far, so `live` can resolve
// "which seat am I" once the user's own entry has picked.
func (f *feed) Seats() map[int]int { return f.seats }
