package engine

import "testing"

// sidelinedBoard is the shape the bug had: the best player at a position cannot
// play and the source has not repriced him, so he leads the position by value
// and every survival number on him is honest and useless.
func sidelinedBoard() *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 1
	s.Players["hurt"] = Player{ID: "hurt", Name: "hurt", Pos: "FWD", Tier: 1, Value: 100,
		ADP: 1, Sigma: SigmaDefault, Sidelined: true}
	s.Players["fit"] = Player{ID: "fit", Name: "fit", Pos: "FWD", Tier: 1, Value: 90,
		ADP: 2, Sigma: SigmaDefault}
	return s
}

// The whole fix in one assertion: a sidelined player is not a candidate for my
// board, and bestNow — which urgency, the plan, the field rows and PickChoices
// all read — names the man I could actually pick.
func TestSidelinedIsNotAvailableToMe(t *testing.T) {
	s := sidelinedBoard()
	avail := s.Available("FWD")
	if len(avail) != 1 || avail[0].ID != "fit" {
		t.Fatalf("Available(FWD) = %v, want just fit", ids(avail))
	}
	best, ok := s.BestNow("FWD")
	if !ok || best.ID != "fit" {
		t.Errorf("BestNow(FWD) = %q ok=%v, want fit", best.ID, ok)
	}
}

// He stays a PLAYER, though, and that half is not cosmetic: the room drafts
// injured men, the sim builds its pool straight off s.Players, and a board that
// hid him from the opponents would spend their picks on somebody else.
func TestSidelinedStaysOnTheRoomsBoard(t *testing.T) {
	s := sidelinedBoard()
	if _, ok := s.Players["hurt"]; !ok {
		t.Fatal("sidelined player was removed from s.Players")
	}
	c := s.newSimCore(1)
	found := false
	for _, cand := range c.pool {
		if cand.id == "hurt" {
			found = true
		}
	}
	if !found {
		t.Error("sidelined player is missing from the sim pool — opponents could never draft him")
	}
	if s.Taken["hurt"] {
		t.Error("sidelined must not be spelled as taken")
	}
}

// TierRemaining and TierSize have to agree about him, and Cliff is why: it
// calls a tier untouched when remaining >= size. Count him in one and not the
// other and a tier nobody has drafted out of reads as emptying, which puts
// "last one, take him or lose the tier" on the opening frame.
func TestSidelinedDoesNotFakeACliff(t *testing.T) {
	s := sidelinedBoard()
	if got, want := s.TierRemaining("FWD", 1), 1; got != want {
		t.Errorf("TierRemaining = %d, want %d", got, want)
	}
	if got, want := s.TierSize("FWD", 1), 1; got != want {
		t.Errorf("TierSize = %d, want %d", got, want)
	}
	if level, _, _ := s.Cliff("FWD"); level != CliffNone {
		t.Errorf("Cliff on an untouched tier = %v, want CliffNone", level)
	}
}

// bestTier speaks for the cliff copy and the group headers. Left unfiltered it
// would name a band whose only member is a man the rows below it never list.
func TestSidelinedDoesNotHoldABand(t *testing.T) {
	s := newTestState(12, 15, 3)
	s.PickNo = 1
	s.Players["hurt"] = Player{ID: "hurt", Pos: "FWD", Tier: 1, Value: 100, Sidelined: true}
	s.Players["fit"] = Player{ID: "fit", Pos: "FWD", Tier: 2, Value: 90}
	if tier, ok := s.bestTier("FWD"); !ok || tier != 2 {
		t.Errorf("bestTier = %d ok=%v, want tier 2 — tier 1 has nobody I can take", tier, ok)
	}
}

// Replacement is what I settle for, and I never settle for a man I cannot pick.
// Two fwd on the board, replacement indexed at 2: counting the sidelined one
// puts R at his value, which is the subsidy the whole vor score would inherit.
func TestSidelinedIsNotTheReplacementPlayer(t *testing.T) {
	s := sidelinedBoard()
	s.SetRoster(Roster{Slots: []string{"FWD", "FWD"}, Bench: 0, Hold: NoHold})
	s.Teams = 1
	if got := s.Replacement("FWD"); got != 0 {
		// One draftable fwd against a demand of two: the settle-for man is
		// somebody nobody valued, which is 0, not the injured man's 100.
		t.Errorf("Replacement(FWD) = %v, want 0", got)
	}
}

func ids(ps []Player) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// Squad is the sidebar's shape for a capped roster: one row per place you own,
// in lineup order, which for fpl is the 2/5/5/3 the official app enforces.
func TestRosterSquadIsTheDraftCap(t *testing.T) {
	got := FPLRoster.Squad()
	want := []string{"GKP", "GKP", "DEF", "DEF", "DEF", "DEF", "DEF",
		"MID", "MID", "MID", "MID", "MID", "FWD", "FWD", "FWD"}
	if len(got) != len(want) {
		t.Fatalf("Squad() = %v (%d places), want %d", got, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Squad()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if n := len(got); n != len(FPLRoster.Slots)+FPLRoster.Bench {
		t.Errorf("squad is %d places but the draft is %d rounds", n, len(FPLRoster.Slots)+FPLRoster.Bench)
	}
	// An uncapped league has one shape and it is the lineup. nil rather than a
	// derived list, or the sidebar would silently redraw every nfl board too.
	if got := DefaultRoster.Squad(); got != nil {
		t.Errorf("DefaultRoster.Squad() = %v, want nil", got)
	}
}

// A capped position the lineup never names still owes you a row — it is a place
// you have to fill, and the pane exists to show the places you have to fill.
func TestSquadIncludesACapNoSlotNames(t *testing.T) {
	r := Roster{
		Slots: []string{"MID", "FLEX"},
		Bench: 1,
		Max:   map[string]int{"MID": 2, "GKP": 1},
		Flex:  map[string]bool{"MID": true, "GKP": true},
		Hold:  NoHold,
	}
	want := []string{"MID", "MID", "GKP"}
	got := r.Squad()
	if len(got) != len(want) {
		t.Fatalf("Squad() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Squad() = %v, want %v", got, want)
		}
	}
}
