package engine

import (
	"math"
	"testing"
)

// The forecast is the rollouts summed per pick. In the flagship rb-room
// scenario the two intervening seats are full at rb with wr open, so the
// forecast has to say wr at both of them — the same fact the survival number
// encodes, read the other way round.
func TestRoomForecastReadsTheRooms(t *testing.T) {
	s := simRBRoomState()
	s.Survival = SurvivalSim
	fc := s.RoomForecast()
	if len(fc) != 3 {
		t.Fatalf("window has 3 opponent picks (96, 97, 98), forecast has %d", len(fc))
	}
	for i, f := range fc {
		if f.PickNo != 96+i {
			t.Errorf("pick %d: PickNo %d", i, f.PickNo)
		}
		total := f.OffBoard
		for _, sh := range f.Mix {
			total += sh
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("pick %d: shares sum to %.4f, want 1", f.PickNo, total)
		}
		if f.Pos != "WR" {
			t.Errorf("pick %d: full rb rooms should forecast wr, got %q at %.0f%%", f.PickNo, f.Pos, 100*f.Share)
		}
		if f.Share <= 0.5 {
			t.Errorf("pick %d: modal wr share %.2f should dominate", f.PickNo, f.Share)
		}
		if f.PlayerID == "" || f.PlayerShare <= 0 {
			t.Errorf("pick %d: no modal player", f.PickNo)
		}
	}
	rem := s.ExpectedRemovals()
	if len(rem) == 0 || rem[0].Pos != "WR" || rem[0].Expect <= 1.5 {
		t.Errorf("expected removals should lead with wr well above one, got %+v", rem)
	}
}

// No rollouts, no forecast: the adp logistic has nothing to sum, and the
// board must draw nothing rather than a strip of zeros.
func TestRoomForecastIsSimOnly(t *testing.T) {
	s := simRBRoomState()
	if fc := s.RoomForecast(); fc != nil {
		t.Errorf("adp mode should have no forecast, got %d picks", len(fc))
	}
	s.Survival = SurvivalSim
	s.PickNo = 99 // my own pick: window is the picks AFTER it, to FollowingPick
	if fc := s.RoomForecast(); len(fc) == 0 {
		t.Error("on the clock the forecast covers the picks after mine")
	} else if fc[0].PickNo <= 99 {
		t.Errorf("on the clock the window starts after my pick, got %d", fc[0].PickNo)
	}
}

// The forecast reads the rollout table, which is a map, and the modal player
// and modal position were both picked with a bare '>' as it ranged. Two men
// removed at a pick equally often then swapped places between renders, and
// "likely gone" named a different man each time. Ties resolve the way
// lookahead.go's modalID and modalPos resolve them: id order for players,
// planPositions order for positions.
func TestRoomForecastBreaksTiesTheSameWayEveryTime(t *testing.T) {
	cases := []struct {
		name      string
		pos       map[string]string // id -> position
		wantID    string
		wantPos   string
		wantShare float64
	}{
		{
			name:      "two men tied, lower id wins",
			pos:       map[string]string{"aaa": "WR", "zzz": "WR"},
			wantID:    "aaa",
			wantPos:   "WR",
			wantShare: 1,
		},
		{
			name:      "two positions tied, planPositions order wins",
			pos:       map[string]string{"aaa": "WR", "zzz": "RB"},
			wantID:    "aaa",
			wantPos:   "RB",
			wantShare: 0.5,
		},
	}
	for _, c := range cases {
		s := newTestState(12, 15, 3)
		s.Survival = SurvivalSim
		s.Players = map[string]Player{}
		for id, pos := range c.pos {
			s.Players[id] = Player{ID: id, Name: id, Pos: pos}
		}
		// Hand-built table: pick 1 removed each of them in exactly half the
		// rollouts. simFor keeps it, since its vantage matches ours.
		half := uint16(Rollouts / 2)
		s.sim = &simTable{
			pickNo:   1,
			far:      3,
			removals: map[string][]uint16{"aaa": {half}, "zzz": {half}},
			oppPicks: []int{1, 2},
		}
		for i := 0; i < 50; i++ {
			fc := s.RoomForecast()
			if len(fc) == 0 {
				t.Fatalf("%s: empty forecast", c.name)
			}
			f := fc[0]
			if f.PlayerID != c.wantID {
				t.Fatalf("%s run %d: modal player %q, want %q", c.name, i, f.PlayerID, c.wantID)
			}
			if f.Pos != c.wantPos {
				t.Fatalf("%s run %d: modal pos %q, want %q", c.name, i, f.Pos, c.wantPos)
			}
			if math.Abs(f.Share-c.wantShare) > 1e-9 {
				t.Fatalf("%s run %d: share %.4f, want %.4f", c.name, i, f.Share, c.wantShare)
			}
		}
	}
}
