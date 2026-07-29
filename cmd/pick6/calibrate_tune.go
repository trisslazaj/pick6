package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
)

// calibrate -tune: sweep the sigma constants over the same backtest and print
// what wins. It prints and nothing else — the human moves numbers into
// tuning.go by hand, because a constant that changes without a decision is a
// constant nobody can explain later.

// The grid is deliberately small — seconds, not minutes, and the question is
// "are the shipped constants badly wrong", not "what is the fourth decimal on
// one draft".
//
// The floor axis is the long one because that is the axis this data can move:
// ffc reports a stdev for every player, so SigmaDefault never fires (its three
// values are here to show that as ties, not to be believed), and the ceiling
// barely binds. The floor is where the model's overconfidence about locked-in
// players lives, and it runs to 20 — absurd on its face — because a first pass
// stopping at 3 put the winner on the grid edge, which says the grid was too
// small rather than that 3 was right. edgeWarning below keeps that honest.
//
// The shipped combo (6.0/0.5/25.0) is in the grid deliberately: its row must
// reproduce the engine row above exactly, or this file and the engine drifted.
var (
	tuneDefaults = []float64{4.5, 6, 8}
	tuneMins     = []float64{0.5, 2, 4, 6, 8, 10, 12, 14, 16, 20}
	tuneMaxes    = []float64{10, 15, 25, 40}
)

// sigmaCoverage says which of the three axes this data can actually move, so
// nobody edits a constant on the strength of a tie. A count of zero on the "no
// stdev" column means every row in the grid's default column scored identically
// and none of them learned anything.
//
// The spread and the clamp counts are per PLAYER, over the joined era board.
// Counting them per prediction weights each player by how many vantages he
// survived to, and deep players survive to far more of them than early ones:
// on the 2024 board that reads the median as 5.57 against the pool's true 3.80
// and turns 3 low-clamped players into 8. This is the line someone reads before
// moving SigmaMin, so it has to describe the pool it claims to.
func sigmaCoverage(preds []pred, pool []engine.Player) string {
	var none int
	for _, p := range preds {
		if p.stdev <= 0 {
			none++
		}
	}
	head := fmt.Sprintf("inputs: %d predictions · %d with no stdev, so the default fires that often",
		len(preds), none)

	var floor, ceil int
	raws := make([]float64, 0, len(pool))
	for _, pl := range pool {
		if pl.Stdev <= 0 {
			continue
		}
		raw := pl.Stdev / adp.SigmaFromStdev
		if raw < engine.SigmaMin {
			floor++
		}
		if raw > engine.SigmaMax {
			ceil++
		}
		raws = append(raws, raw)
	}
	if len(raws) == 0 {
		return head // nothing but the default fired; the clamps are untested here
	}
	sort.Float64s(raws)
	return fmt.Sprintf("%s\n  unclamped sigma %.2f / %.2f / %.2f (min/median/max over %d players) · today's clamps bind %d low, %d high",
		head, raws[0], raws[len(raws)/2], raws[len(raws)-1], len(raws), floor, ceil)
}

// combo is one grid point: the three constants and what they scored.
type combo struct {
	def, lo, hi float64
	s           score
}

func tuneSigma(preds []pred, pool []engine.Player) {
	var out []combo
	for _, def := range tuneDefaults {
		for _, lo := range tuneMins {
			for _, hi := range tuneMaxes {
				if lo >= hi {
					continue
				}
				m := func(p pred) float64 {
					return psurvive(p.from, p.to, p.adp, sigmaOf(p.stdev, def, lo, hi))
				}
				out = append(out, combo{def, lo, hi, scoreOf(preds, m)})
			}
		}
	}
	// Ties are the norm here, not the exception — an axis that never binds scores
	// identically at every value — so break them deterministically or two runs of
	// the same command print different "winners".
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.s.brier != b.s.brier:
			return a.s.brier < b.s.brier
		case a.s.logLoss != b.s.logLoss:
			return a.s.logLoss < b.s.logLoss
		case a.def != b.def:
			return a.def < b.def
		case a.lo != b.lo:
			return a.lo < b.lo
		default:
			return a.hi < b.hi
		}
	})

	fmt.Printf("\ntune — %d sigma combinations by brier\n", len(out))
	// The grid scores the UNTILTED model, so its winner is the best sigma for a
	// board with no exactly-n correction on it. The tilt renormalizes the whole
	// vantage, which is exactly the kind of thing that can absorb a sigma error —
	// read this table as "are the shipped sigmas badly wrong on their own", not
	// as the sigma to ship alongside the tilt.
	fmt.Println("  scored without the tilt: this asks whether the sigmas are wrong on their own")
	fmt.Printf("  %s\n", sigmaCoverage(preds, pool))
	fmt.Printf("  %-8s %-8s %-8s %8s %9s %10s\n", "default", "min", "max", "brier", "log-loss", "predicted")
	show := func(c combo) {
		tag := ""
		if c.def == engine.SigmaDefault && c.lo == engine.SigmaMin && c.hi == engine.SigmaMax {
			tag = "   <- shipped"
		}
		fmt.Printf("  %-8.1f %-8.1f %-8.1f %8.4f %9.4f %10.4f%s\n",
			c.def, c.lo, c.hi, c.s.brier, c.s.logLoss, c.s.mean, tag)
	}
	// One row per distinct score, not per combo. Six copies of the winner
	// differing only on an axis that never binds says nothing; six distinct
	// scores show the shape of the curve around the optimum, which is the only
	// way to tell a real minimum from a flat region.
	shown, last := 0, math.NaN()
	for _, c := range out {
		if c.s.brier == last {
			continue
		}
		show(c)
		last = c.s.brier
		if shown++; shown >= 6 {
			break
		}
	}
	for _, c := range out {
		if c.def == engine.SigmaDefault && c.lo == engine.SigmaMin && c.hi == engine.SigmaMax {
			fmt.Println("  ...")
			show(c)
			break
		}
	}
	if w := edgeWarning(out); w != "" {
		fmt.Printf("  %s\n", w)
	}
	fmt.Println("\n  nothing was written. these live in internal/engine/tuning.go and are")
	fmt.Println("  mirrored in internal/adp/aggregate.go (fetch precomputes sigma); move them")
	fmt.Println("  by hand, both places, and refetch — one draft is not a mandate.")
}

// edgeWarning fires when the best score is only reachable at the outside of an
// axis. That is not a winner, it's the grid running out of road: the real
// optimum is past the edge and this row is the closest the search could get.
//
// It asks whether ANY combo achieving the best brier is interior, not whether
// the printed winner is — an axis that doesn't bind ties everywhere, and the
// tie-break lands on its lowest value, which would otherwise read as an edge.
func edgeWarning(sorted []combo) string {
	best := sorted[0].s.brier
	var axes []string
	interior := func(axis []float64, pick func(combo) float64) bool {
		for _, c := range sorted {
			if c.s.brier != best {
				break // sorted by brier, so the tied block is a prefix
			}
			v := pick(c)
			if v != axis[0] && v != axis[len(axis)-1] {
				return true
			}
		}
		return false
	}
	if len(tuneDefaults) > 2 && !interior(tuneDefaults, func(c combo) float64 { return c.def }) {
		axes = append(axes, "default")
	}
	if len(tuneMins) > 2 && !interior(tuneMins, func(c combo) float64 { return c.lo }) {
		axes = append(axes, "min")
	}
	if len(tuneMaxes) > 2 && !interior(tuneMaxes, func(c combo) float64 { return c.hi }) {
		axes = append(axes, "max")
	}
	if len(axes) == 0 {
		return ""
	}
	return "the winner sits on the edge of the " + strings.Join(axes, "/") +
		" axis — widen the grid before believing it"
}
