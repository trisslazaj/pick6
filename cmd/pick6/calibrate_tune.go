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
// The shipped combo (6.0/0.5/25.0 with 25 pseudo-drafts) is in the grid
// deliberately: its untilted row must reproduce the "4b: shrunk sigma" row above
// exactly, and its tilted column the "shrunk sigma + tilt" row, or this file and
// the engine drifted.
//
// The shrink's pseudo-count is the fourth axis, and 0 — no shrink at all, the
// pre-4b model — is in it deliberately: the shrink has to win its place on this
// grid rather than being assumed into it. Its low end is a real floor, not a
// search limit, which is why edgeWarning treats that axis asymmetrically.
var (
	tuneDefaults = []float64{4.5, 6, 8}
	tuneMins     = []float64{0.5, 2, 4, 6, 8, 10, 12, 14, 16, 20}
	tuneMaxes    = []float64{10, 15, 25, 40}
	tuneShrinks  = []float64{0, 10, 25, 50, 100, 200}
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
	return fmt.Sprintf("%s\n  unclamped sigma before the shrink %.2f / %.2f / %.2f (min/median/max over %d players) · today's clamps bind %d low, %d high",
		head, raws[0], raws[len(raws)/2], raws[len(raws)-1], len(raws), floor, ceil)
}

// combo is one grid point: the four constants and what they scored.
type combo struct {
	def, lo, hi float64
	n0          float64 // shrink pseudo-drafts; 0 is no shrink
	s           score
	tiltBrier   float64 // the same combo re-scored with the tilt on top
}

// sigmaModel rebuilds a grid point's survival function. Kept as a function of
// the combo rather than captured in a closure per point so the sweep and the
// printed rows cannot drift into scoring two different things.
func sigmaModel(c combo) func(pred) float64 {
	return func(p pred) float64 {
		stdev := adp.Blend(p.stdev, p.prior, p.drafts, c.n0)
		return psurvive(p.from, p.to, p.adp, sigmaOf(stdev, c.def, c.lo, c.hi))
	}
}

func tuneSigma(preds []pred, pool []engine.Player) {
	var out []combo
	for _, def := range tuneDefaults {
		for _, lo := range tuneMins {
			for _, hi := range tuneMaxes {
				if lo >= hi {
					continue
				}
				for _, n0 := range tuneShrinks {
					c := combo{def: def, lo: lo, hi: hi, n0: n0}
					m := sigmaModel(c)
					c.s = scoreOf(preds, m)
					// Every point is scored twice, and the ranking uses the tilted
					// number because the tilt is what ships. The untilted score
					// stays in the table: it answers a different question ("are
					// these sigmas wrong on their own") and the two disagree, which
					// is the single most useful thing this grid says.
					tf, _ := tilted(preds, m)
					c.tiltBrier = scoreOf(preds, tf).brier
					out = append(out, c)
				}
			}
		}
	}
	// Ties are the norm here, not the exception — an axis that never binds scores
	// identically at every value — so break them deterministically or two runs of
	// the same command print different "winners".
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.tiltBrier != b.tiltBrier:
			return a.tiltBrier < b.tiltBrier
		case a.s.brier != b.s.brier:
			return a.s.brier < b.s.brier
		case a.s.logLoss != b.s.logLoss:
			return a.s.logLoss < b.s.logLoss
		case a.def != b.def:
			return a.def < b.def
		case a.lo != b.lo:
			return a.lo < b.lo
		case a.hi != b.hi:
			return a.hi < b.hi
		default:
			return a.n0 < b.n0
		}
	})

	fmt.Printf("\ntune — %d sigma/shrink combinations, ranked by tilted brier\n", len(out))
	// Ranked by the TILTED score, because that is the model that ships. The
	// untilted columns stay because they answer a different question — are these
	// sigmas wrong on their own — and the two answers disagree, which is the most
	// useful thing this grid has to say: the tilt renormalizes a whole vantage at
	// once and absorbs a sigma error, so an untilted optimum is not a mandate to
	// move any constant.
	fmt.Println("  brier/log-loss/predicted are untilted — 'are these sigmas wrong on their own'")
	fmt.Println("  tilted is the same combo with the exactly-n correction on top, which ships")
	fmt.Printf("  %s\n", sigmaCoverage(preds, pool))
	fmt.Printf("  %-8s %-8s %-8s %-8s %8s %9s %10s %10s\n",
		"default", "min", "max", "shrink", "brier", "log-loss", "predicted", "tilted")
	show := func(c combo) {
		tag := ""
		if c.def == engine.SigmaDefault && c.lo == engine.SigmaMin &&
			c.hi == engine.SigmaMax && c.n0 == adp.ShrinkPseudoDrafts {
			tag = "   <- shipped"
		}
		fmt.Printf("  %-8.1f %-8.1f %-8.1f %-8.0f %8.4f %9.4f %10.4f %10.4f%s\n",
			c.def, c.lo, c.hi, c.n0, c.s.brier, c.s.logLoss, c.s.mean, c.tiltBrier, tag)
	}
	// One row per distinct PRINTED score, not per combo. Six copies of the winner
	// differing only on an axis that never binds says nothing; six distinct
	// scores show the shape of the curve around the optimum, which is the only
	// way to tell a real minimum from a flat region — and here that is the whole
	// question, since the shipped combo scores 0.0670 against a 0.0667 winner.
	//
	// Rounded to the four decimals the column prints at, because the comparison
	// has to be the one the READER makes. At full float64 precision this only
	// ever suppressed bit-identical scores, i.e. nothing: (4.5, 8.0, 10.0, 200),
	// (4.5, 8.0, 10.0, 100), (4.5, 4.0, 15.0, 50) and (4.5, 4.0, 10.0, 50) all
	// print 0.0667 while differing in the eighth decimal, so four of the six rows
	// were the same number four times and the curve was invisible.
	//
	// Keyed on the tilted score alone, because that is what the table is ranked
	// by and what "the shape of the curve" refers to. The untilted columns answer
	// a different question and are along for the ride; letting them split a row
	// would put the duplicates straight back.
	//
	// A running last rather than a set: the sort is by exact tilted brier and
	// rounding is monotone, so rows sharing a printed value are contiguous.
	shown, last := 0, math.NaN() // NaN compares false, so the first row always prints
	for _, c := range out {
		r := math.Round(c.tiltBrier*1e4) / 1e4
		if r == last {
			continue
		}
		show(c)
		last = r
		if shown++; shown >= 6 {
			break
		}
	}
	for _, c := range out {
		if c.def == engine.SigmaDefault && c.lo == engine.SigmaMin &&
			c.hi == engine.SigmaMax && c.n0 == adp.ShrinkPseudoDrafts {
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
	best := sorted[0].tiltBrier
	var axes []string
	// lowIsReal marks an axis whose bottom value is a fact rather than a search
	// limit. The shrink axis starts at 0 pseudo-drafts, which is "no shrink at
	// all" — a winner sitting there is a verdict, not a grid that ran out of road,
	// and warning about it would send the reader looking for negative evidence.
	interior := func(axis []float64, lowIsReal bool, pick func(combo) float64) bool {
		for _, c := range sorted {
			if c.tiltBrier != best {
				break // sorted by tilted brier, so the tied block is a prefix
			}
			v := pick(c)
			if v == axis[0] && lowIsReal {
				return true
			}
			if v != axis[0] && v != axis[len(axis)-1] {
				return true
			}
		}
		return false
	}
	if len(tuneDefaults) > 2 && !interior(tuneDefaults, false, func(c combo) float64 { return c.def }) {
		axes = append(axes, "default")
	}
	if len(tuneMins) > 2 && !interior(tuneMins, false, func(c combo) float64 { return c.lo }) {
		axes = append(axes, "min")
	}
	if len(tuneMaxes) > 2 && !interior(tuneMaxes, false, func(c combo) float64 { return c.hi }) {
		axes = append(axes, "max")
	}
	if len(tuneShrinks) > 2 && !interior(tuneShrinks, true, func(c combo) float64 { return c.n0 }) {
		axes = append(axes, "shrink")
	}
	if len(axes) == 0 {
		return ""
	}
	return "the winner sits on the edge of the " + strings.Join(axes, "/") +
		" axis — widen the grid before believing it"
}
