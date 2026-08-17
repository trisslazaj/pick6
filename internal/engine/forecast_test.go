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
