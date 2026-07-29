package main

import (
	"math"
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
)

// calibrate carries its own copy of the exactly-n tilt because the engine's is
// unexported and hardwired to the NextPick() horizon, which a backtest vantage
// never occupies. Two implementations of one model is the failure mode this file
// exists to prevent: if they drift, calibrate starts grading a tilt the board
// never renders and the gate it produces means nothing.
func TestLocalTiltMatchesTheEngine(t *testing.T) {
	worst, c, players := tiltSelfCheck()
	if players != 60 {
		t.Fatalf("self-check board is %d players, want 60", players)
	}
	// An exponent pinned to a clamp would make the two agree for the wrong
	// reason — both would return TiltCMax without bisecting at all.
	if c <= 1/engine.TiltCMax || c >= engine.TiltCMax {
		t.Fatalf("self-check clamped at c %g; it must exercise the bisection", c)
	}
	if worst > 1e-12 {
		t.Fatalf("local solve and engine.PSurviveTilted disagree by %g, want <= 1e-12", worst)
	}
}

// The tilt's whole definition is that expected removals equal the picks that
// really happen, so solving it and then measuring the sum is the one assertion
// that cannot pass by accident.
func TestSolveTiltHitsTheTarget(t *testing.T) {
	cases := []struct {
		name string
		ps   []float64
		n    int
		want float64 // hand-computed c, or the clamp it must reach
	}{
		// eight players at 0.5 and two picks: (1-0.5^c)*8 = 2 -> 0.5^c = 0.75
		// -> c = ln(0.75)/ln(0.5) = 0.41503749927884376.
		{"lifts an over-pessimistic board", []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}, 2, 0.4150374992},
		// four at 0.5 and three picks: (1-0.5^c)*4 = 3 -> 0.5^c = 0.25 -> c = 2.
		{"lowers an over-optimistic board", []float64{0.5, 0.5, 0.5, 0.5}, 3, 2.0},
		// sum(1-p) is already 2 at c = 1, so the correction is a no-op.
		{"leaves a calibrated board alone", []float64{0.5, 0.5, 0.5, 0.5}, 2, 1.0},
		// three players cannot supply eighteen removals however hungry the room
		// gets. this is a real board state — a thin endgame, or a toy fixture
		// driven as a 12-team draft — not defensive padding.
		{"clamps high on a thin board", []float64{0.9, 0.9, 0.9}, 18, engine.TiltCMax},
		// five players the model has already written off, one pick left to take
		// them: no exponent can push expected removals down to 1.
		{"clamps low on a written-off board", []float64{1e-8, 1e-8, 1e-8, 1e-8, 1e-8}, 1, 1 / engine.TiltCMax},
		// my own pick: nothing intervenes, so there is nothing to correct.
		{"skips when nothing intervenes", []float64{0.2, 0.3}, 0, 1.0},
	}
	for _, c := range cases {
		got := solveTilt(c.ps, c.n)
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s: c = %.9f, want %.9f", c.name, got, c.want)
		}
		// A clamped solve is the model admitting it cannot hit the target, so
		// only check the sum where the bisection actually ran.
		if got > 1/engine.TiltCMax && got < engine.TiltCMax && c.n > 0 {
			sum := 0.0
			for _, p := range c.ps {
				sum += 1 - math.Pow(p, got)
			}
			if math.Abs(sum-float64(c.n)) > 1e-4 {
				t.Errorf("%s: expected removals %.6f, want %d", c.name, sum, c.n)
			}
		}
	}
}

// p = 1 is my own pick and the undrafted-radar player; p = 0 is a man who is
// already gone. Both must survive any exponent, or the tilt starts inventing
// uncertainty about players nobody was uncertain about — and 0*Inf in the log
// space would surface as NaN rather than as a wrong number.
func TestSolveTiltKeepsItsFixedPointsAndOrdering(t *testing.T) {
	ps := []float64{1, 0.9, 0.6, 0.3, 0}
	c := solveTilt(ps, 2)
	tilted := make([]float64, len(ps))
	for i, p := range ps {
		tilted[i] = math.Pow(p, c)
	}
	if tilted[0] != 1 {
		t.Errorf("p = 1 moved to %.9f", tilted[0])
	}
	if tilted[len(tilted)-1] != 0 {
		t.Errorf("p = 0 moved to %.9f", tilted[len(tilted)-1])
	}
	for i := 1; i < len(tilted); i++ {
		if tilted[i] >= tilted[i-1] {
			t.Errorf("ordering broke at %d: %.9f then %.9f", i, tilted[i-1], tilted[i])
		}
	}
}

// The tilt is solved over a whole vantage's board and then read off per row. A
// segment filter is a grading choice, not a different draft — if the exponent
// were re-solved over the filtered subset, "13+ picks" rows would be corrected
// against a draft in which only long-horizon players exist. Keying by pred.idx
// is what prevents that, so assert it survives filtering.
func TestTiltedSurvivesSegmentFiltering(t *testing.T) {
	// Two vantages with deliberately opposite corrections: the first is
	// over-pessimistic (eight players at 0.5, only two picks to take them,
	// c = ln(0.75)/ln(0.5) = 0.4150375, lifting everyone to 0.75), the second
	// over-optimistic (four at 0.5, three picks, c = 2, dropping everyone to
	// 0.25). If the solve leaked into the filter these would blur together.
	var preds []pred
	for i := 0; i < 8; i++ {
		pos := "RB"
		if i%2 == 1 {
			pos = "WR"
		}
		preds = append(preds, pred{vantage: 0, from: 1, to: 3, q: 0.5, y: 1, pos: pos})
	}
	for i := 0; i < 4; i++ {
		pos := "RB"
		if i%2 == 1 {
			pos = "WR"
		}
		preds = append(preds, pred{vantage: 1, from: 9, to: 12, q: 0.5, y: 1, pos: pos})
	}
	for i := range preds {
		preds[i].idx = i
	}

	f, vts := tilted(preds, modelEngine)
	if len(vts) != 2 {
		t.Fatalf("solved %d vantages, want 2", len(vts))
	}
	if math.Abs(vts[0].c-0.4150374992) > 1e-6 {
		t.Errorf("vantage 0 c = %.9f, want 0.415037499", vts[0].c)
	}
	if math.Abs(vts[1].c-2.0) > 1e-6 {
		t.Errorf("vantage 1 c = %.9f, want 2.0", vts[1].c)
	}
	// Grading only the rbs must not move a single number: each row still reads
	// the exponent solved over its own whole board.
	var rbs []pred
	for _, p := range preds {
		if p.pos == "RB" {
			rbs = append(rbs, p)
		}
	}
	if len(rbs) != 6 {
		t.Fatalf("filtered %d rbs, want 6", len(rbs))
	}
	for _, p := range rbs {
		want := math.Pow(p.q, vts[p.vantage].c)
		if f(p) != want {
			t.Errorf("row %d graded %.12f, want %.12f", p.idx, f(p), want)
		}
	}
	// And the two vantages really did land on different answers, which is the
	// only reason the check above can fail if the keying breaks.
	if f(rbs[0]) == f(rbs[len(rbs)-1]) {
		t.Error("both vantages tilted to the same value; the fixture proves nothing")
	}
}

// A vantage's target is every pick in [from, to), including my own at `from` —
// the labels count it, because a player I take at `from` has y = 0. Getting this
// off by one would quietly mis-tilt every row in the backtest.
func TestTiltTargetsEveryPickInTheWindow(t *testing.T) {
	preds := []pred{
		{idx: 0, vantage: 0, from: 3, to: 22, q: 0.5, y: 1},
		{idx: 1, vantage: 0, from: 3, to: 22, q: 0.5, y: 1},
	}
	_, vts := tilted(preds, modelEngine)
	if vts[0].n != 19 {
		t.Errorf("target n = %d, want 19 (picks 3..21 inclusive)", vts[0].n)
	}
	if vts[0].model != 1.0 {
		t.Errorf("model expected %.4f removals, want 1.0", vts[0].model)
	}
	if vts[0].actual != 0.0 {
		t.Errorf("board really lost %.4f, want 0.0", vts[0].actual)
	}
}
