package sleeper

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

type httpFeed struct {
	draftID string
}

// NewFeed returns a Feed backed by the Sleeper API.
func NewFeed(draftID string) Feed { return &httpFeed{draftID: draftID} }

// Poll fetches the current picks and draft status.
//
// Picks are re-fetched whole rather than incrementally: Sleeper has no "since"
// parameter, the payload is small, and pulling the full list means a dropped
// poll self-heals on the next one instead of leaving a permanent hole.
func (f *httpFeed) Poll() (Snapshot, error) {
	picks, err := GetPicks(f.draftID)
	if err != nil {
		return Snapshot{}, err
	}
	// Status comes from the draft object; a finished draft stops the poll loop.
	d, err := GetDraft(f.draftID)
	if err != nil {
		// Picks are the important half. A status blip shouldn't stall the board.
		return Snapshot{Status: "drafting", Picks: picks}, nil
	}
	return Snapshot{Status: d.Status, Picks: picks}, nil
}
