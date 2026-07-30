package engine

import (
	"math"
	"testing"
)

// TestADPEffIsTheSurvivalPrice pins the whole mechanism of the room warp: exactly
// two readers switch to the warped price, and they switch to it completely.
//
// The bug it prevents is a half-wired warp. ADP is read in six places — the
// survival curve, the faller flag, the data tab's column, Available's sort
// tie-break, the group headers, the mock's picker — and only the first two may
// ever see ADPEff. If PSurviveAt kept reading raw ADP the flag would silently do
// nothing; if it read ADPEff while Falling did not, the board would put amber
// discount numbers on players the model still thinks are fairly priced.
//
// Hand-computed. p = exp(softplus((now-adp)/sigma) - softplus((at-adp)/sigma)):
// unwarped, adp 30, sigma 5, now 10, at 20 -> exp(sp(-4) - sp(-2)) =
// exp(0.01814993 - 0.12692801) = 0.896929. Warped to adp 20 -> exp(sp(-2) - sp(0))
// = exp(0.12692801 - 0.69314718) = 0.567668. The warped man is far likelier to be
// gone, which is the point: this room takes him ten picks before the market does.
func TestADPEffIsTheSurvivalPrice(t *testing.T) {
	s := New(map[string]Player{}, 12, 15, 1)
	s.PickNo = 10

	cases := []struct {
		name   string
		adp    float64
		adpEff float64
		want   float64
	}{
		{"no warp reads raw adp", 30, 0, 0.896929},
		{"a warp replaces it", 30, 20, 0.567668},
		{"warping to his own adp changes nothing", 30, 30, 0.896929},
		// A warp that never fired leaves the field at 0, which must mean "raw",
		// not "adp 0" — an adp of zero is the undrafted-radar sentinel and would
		// read as survives-always.
		{"zero is absent, not free", 20, 0, 0.567668},
	}
	for _, c := range cases {
		p := Player{ID: "fake", Pos: "RB", ADP: c.adp, ADPEff: c.adpEff, Sigma: 5}
		if got := s.PSurviveAt(p, 20); math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s: psurviveat = %.6f, want %.6f", c.name, got, c.want)
		}
	}
}

// TestFallingReadsTheWarpedPrice is the second reader, and it has to agree with
// the first. "The market has passed him" means the price the survival model is
// using has been passed — a board where the two disagree flags discounts the
// model does not believe in.
func TestFallingReadsTheWarpedPrice(t *testing.T) {
	s := New(map[string]Player{}, 12, 15, 1)
	s.PickNo = 30 // FallerSigmas is 1.0, so sigma 5 means falling once pickNo - price >= 5

	cases := []struct {
		name   string
		adp    float64
		adpEff float64
		want   bool
	}{
		{"raw adp, still ahead of the pick", 40, 0, false},
		{"raw adp, well past it", 20, 0, true},
		// The warp is the only thing that moves in each of these two.
		{"warped earlier: now falling", 40, 20, true},
		{"warped later: no longer falling", 20, 40, false},
		// No price at all is off the drafted radar, never falling — the sentinel
		// survives the warp because an uncovered player keeps his zero.
		{"no adp anywhere", 0, 0, false},
	}
	for _, c := range cases {
		p := Player{ID: "fake", Pos: "WR", ADP: c.adp, ADPEff: c.adpEff, Sigma: 5}
		if got := s.Falling(p); got != c.want {
			t.Errorf("%s: falling = %v, want %v", c.name, got, c.want)
		}
	}
}
