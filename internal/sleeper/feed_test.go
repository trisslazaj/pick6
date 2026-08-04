package sleeper

import "testing"

// Two feeds must not open on the same nonce, because the nonce is the only
// thing standing between a poll and cloudflare's fifteen-second-old copy.
//
// The failure this pins is not visible from inside one process: a long-lived
// tui increments past the cache on its second poll and looks fine forever. It
// bites the headless harness, which starts a new process per poll and therefore
// only ever sends the FIRST nonce — so if that first one is a constant, every
// poll in the run reads the same cached body. Measured against a live mock at a
// 4s interval: the pick count advanced in ~30s steps of 5 to 15 picks.
func TestFeedsDoNotShareAStartingNonce(t *testing.T) {
	a := NewFeed("draft-a").(*httpFeed)
	b := NewFeed("draft-b").(*httpFeed)
	if a.nonce == b.nonce {
		t.Fatalf("both feeds opened on nonce %d — every process would request the same url", a.nonce)
	}
	if a.nonce == 0 {
		t.Error("a zero nonce means the first request of every process is ?_=1")
	}
}
