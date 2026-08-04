package sleeper

import "math/rand/v2"

// Feed polls a draft's picks. It exists so the UI can depend on an interface
// rather than the network, which makes the live model testable offline.
type Feed interface {
	Poll() (Snapshot, error)
}

// Snapshot is one poll's worth of draft state.
type Snapshot struct {
	Status string // pre_draft | drafting | paused | complete
	Picks  []DraftPick
}

// Complete reports whether the draft is over.
func (s Snapshot) Complete() bool { return s.Status == "complete" }

// statusEvery is how many polls pass between draft-metadata fetches. Status
// changes at most a handful of times in a draft, so re-fetching it every cycle
// doubles the request count to learn nothing.
const statusEvery = 10

type httpFeed struct {
	draftID string
	nonce   int64
	status  string
}

// NewFeed returns a Feed backed by the Sleeper API.
//
// The nonce is SEEDED RANDOMLY, not started at zero, and that is the whole
// point of this constructor. GetPicksFresh beats the CDN with a unique query
// parameter, but a counter starting at zero makes the first request of every
// process `?_=1` — the same url every time — so a caller that shells out once
// per poll reads cloudflare's copy forever. Measured against a live mock: a 4s
// polling harness saw the pick count move in ~30s steps of 5 to 15 picks, which
// is exactly the s-maxage=15 window, while the tui beside it was current. The
// tui was never affected, because one long-lived process increments past the
// cache on its second poll; only repeated short ones collide, and headless
// polling is how this tool gets exercised against real drafts.
//
// Random rather than the clock: UnixNano is only as fine as the platform's
// clock, and on this mac two feeds built in the same tick came back with the
// same number.
func NewFeed(draftID string) Feed {
	return &httpFeed{draftID: draftID, nonce: rand.Int64()}
}

// Poll fetches the current picks, bypassing the CDN cache.
//
// Picks are re-fetched whole rather than incrementally: Sleeper has no "since"
// parameter, the payload is a few kilobytes, and pulling the full list means a
// dropped poll self-heals on the next one instead of leaving a permanent hole.
func (f *httpFeed) Poll() (Snapshot, error) {
	f.nonce++
	picks, err := GetPicksFresh(f.draftID, f.nonce)
	if err != nil {
		return Snapshot{}, err
	}

	// Refresh status occasionally rather than every cycle.
	if f.status == "" || f.nonce%statusEvery == 0 {
		if d, err := GetDraft(f.draftID); err == nil {
			f.status = d.Status
		} else if f.status == "" {
			// Picks are the important half; a status blip shouldn't stall the board.
			f.status = "drafting"
		}
	}
	return Snapshot{Status: f.status, Picks: picks}, nil
}
