package engine

import "testing"

// The deny scenario: I'm on the clock, the seat after me has an open te slot,
// exactly one te left in the best remaining band, and my own te is settled.
func denyState() *State {
	s := newTestState(12, 15, 3)
	s.PickNo = 51 // my round-5 pick; pick 52 is slot 4
	s.Survival = SurvivalSim
	// My te slot is filled, so my need for te is flex weight at most.
	addPlayers(s, Player{ID: "myte", Pos: "TE", Value: 400, Tier: 1})
	s.Taken["myte"] = true
	s.Rosters[3] = []string{"myte"}
	// Slot 4 has rb/rb/wr/wr filled and no te: te reads NeedStarter for them,
	// and everything else dedicated reads at most starter too — so give them
	// the rest of a lineup so te is their one gaping hole.
	for i, pos := range []string{"RB", "RB", "WR", "WR", "QB"} {
		id := "s4" + pos + string(rune('0'+i))
		addPlayers(s, Player{ID: id, Pos: pos, Value: 300})
		s.Taken[id] = true
		s.Rosters[4] = append(s.Rosters[4], id)
	}
	// One te left in tier 2 — the last of the best remaining band — plus board
	// depth at other positions so nothing is empty.
	addPlayers(s,
		Player{ID: "lastte", Pos: "TE", ADP: 55, Sigma: 4, Value: 380, Tier: 2},
		Player{ID: "deepte", Pos: "TE", ADP: 90, Sigma: 6, Value: 100, Tier: 4},
		Player{ID: "rb1", Pos: "RB", ADP: 50, Sigma: 4, Value: 500, Tier: 2},
		Player{ID: "wr1", Pos: "WR", ADP: 52, Sigma: 4, Value: 490, Tier: 2},
		Player{ID: "wr2", Pos: "WR", ADP: 56, Sigma: 4, Value: 450, Tier: 2},
		Player{ID: "qb1", Pos: "QB", ADP: 60, Sigma: 5, Value: 470, Tier: 2},
	)
	return s
}

func TestDeny(t *testing.T) {
	s := denyState()
	p, them, ok := s.Deny()
	if !ok {
		t.Fatal("deny: want a chip, got none")
	}
	if p.ID != "lastte" || them != 4 {
		t.Fatalf("deny = %s vs slot %d, want lastte vs 4", p.ID, them)
	}
}

func TestDenyOnlyOnMyPick(t *testing.T) {
	s := denyState()
	s.PickNo = 50 // slot 11's pick, not mine
	if _, _, ok := s.Deny(); ok {
		t.Fatal("deny fired off the clock")
	}
}

func TestDenySkipsTheTurn(t *testing.T) {
	s := newTestState(12, 15, 12)
	s.PickNo = 12 // the turn: pick 13 is mine too
	addPlayers(s, Player{ID: "x", Pos: "TE", ADP: 12, Sigma: 4, Value: 300, Tier: 1})
	if _, _, ok := s.Deny(); ok {
		t.Fatal("deny fired against myself at the turn")
	}
}

func TestDenyNotWhenINeedHim(t *testing.T) {
	s := denyState()
	// Take my te back: now I need the last te at starter weight myself.
	delete(s.Taken, "myte")
	s.Rosters[3] = nil
	if _, _, ok := s.Deny(); ok {
		t.Fatal("deny fired on a player I need at starter weight — that's a pick, not a deny")
	}
}

func TestDenyNeedsALastMan(t *testing.T) {
	s := denyState()
	// A second te in the band: no longer the last one.
	addPlayers(s, Player{ID: "otherte", Pos: "TE", ADP: 58, Sigma: 4, Value: 370, Tier: 2})
	if _, _, ok := s.Deny(); ok {
		t.Fatal("deny fired with two men left in the band")
	}
}
