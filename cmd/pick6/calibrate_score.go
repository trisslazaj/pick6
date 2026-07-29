package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
)

// pred is one labeled prediction: standing at pick `from`, the engine said the
// player survives to `to` with probability q, and the draft said y.
type pred struct {
	pos   string
	adp   float64
	stdev float64 // 0 when ffc reported none, i.e. the sigma fallback fired
	from  int
	to    int
	teams int
	q     float64 // the engine's own conditional survival, per-player sigma
	y     float64 // 1 if he really was still available at `to`
}

func (p pred) horizon() int { return p.to - p.from }

// score is one model graded over one set of predictions.
type score struct {
	brier   float64
	logLoss float64
	mean    float64 // mean predicted survival
	obs     float64 // observed survival rate — the two match when calibrated
	n       int
}

func scoreOf(preds []pred, q func(pred) float64) score {
	var s score
	for _, p := range preds {
		v := q(p)
		d := v - p.y
		c := clampP(v)
		s.brier += d * d
		s.logLoss -= p.y*math.Log(c) + (1-p.y)*math.Log(1-c)
		s.mean += v
		s.obs += p.y
		s.n++
	}
	if s.n == 0 {
		return s
	}
	n := float64(s.n)
	s.brier /= n
	s.logLoss /= n
	s.mean /= n
	s.obs /= n
	return s
}

// clampP keeps log-loss finite. ln(0) is -inf, so a single overconfident miss
// would otherwise be the entire score.
func clampP(v float64) float64 { return math.Max(1e-6, math.Min(1-1e-6, v)) }

// ---- the models being compared ----

// modelEngine is what ships today, recorded during the walk so this is literally
// the engine's own arithmetic and not a reimplementation of it.
func modelEngine(p pred) float64 { return p.q }

// modelFlatSigma throws away the per-player stdev. If this ties the engine, the
// stdev plumbing is decoration.
func modelFlatSigma(p pred) float64 {
	return psurvive(p.from, p.to, p.adp, engine.SigmaDefault)
}

// modelUnconditional drops the S(to)/S(from) ratio and asks the raw curve. If
// this ties the engine, conditioning on "he is demonstrably still here" is
// decoration. Written out rather than borrowed because the engine deliberately
// never computes an unconditional survival on its own.
func modelUnconditional(p pred) float64 {
	sigma := sigmaOf(p.stdev, engine.SigmaDefault, engine.SigmaMin, engine.SigmaMax)
	return 1 / (1 + math.Exp((float64(p.to)-p.adp)/sigma))
}

// modelConstant predicts the base rate for everyone — the "no model at all"
// floor. Anything that cannot beat it is theater.
func modelConstant(rate float64) func(pred) float64 {
	return func(pred) float64 { return rate }
}

// psurvive runs the engine's survival with sigma supplied rather than read off a
// fetched player. SigmaDefault and the clamps are compile-time consts inside
// adp.Sigma and PSurviveAt's fallback, so pre-filling Player.Sigma is the only
// way to sweep them — and it guarantees the engine's own fallback never fires.
func psurvive(from, to int, adpVal, sigma float64) float64 {
	s := engine.State{PickNo: from} // PSurviveAt reads PickNo and nothing else
	return s.PSurviveAt(engine.Player{ADP: adpVal, Sigma: sigma}, to)
}

// sigmaOf is adp.Sigma with its three constants as parameters. Same rule: no
// observed stdev means the default, and the default itself is not clamped.
func sigmaOf(stdev, def, lo, hi float64) float64 {
	if stdev <= 0 {
		return def
	}
	return math.Max(lo, math.Min(hi, stdev/adp.SigmaFromStdev))
}

// ---- the report ----

func report(preds []pred, drafts, vantages int) {
	base := scoreOf(preds, modelEngine)
	fmt.Printf("\nscored %d predictions · %d draft(s) · %d vantages\n", base.n, drafts, vantages)
	fmt.Printf("observed survival rate %.4f — a model that beats nothing scores brier %.4f\n",
		base.obs, base.obs*(1-base.obs))

	fmt.Printf("\n%-30s %8s %9s %10s\n", "model", "brier", "log-loss", "predicted")
	row := func(label string, s score) {
		// A baseline that wins says so on its own line. The whole point of
		// running this is that the fancy math doesn't get to grade itself.
		beat := ""
		if s.brier < base.brier {
			beat = "   <- beats the engine"
		}
		fmt.Printf("  %-28s %8.4f %9.4f %10.4f%s\n", label, s.brier, s.logLoss, s.mean, beat)
	}
	row("engine (per-player sigma)", base)
	row(fmt.Sprintf("baseline: constant %.3f", base.obs), scoreOf(preds, modelConstant(base.obs)))
	row(fmt.Sprintf("baseline: sigma %.1f flat", engine.SigmaDefault), scoreOf(preds, modelFlatSigma))
	row("baseline: unconditional", scoreOf(preds, modelUnconditional))

	reliability(preds)
	segments(preds)
}

// reliability is the brier score made visible: of all the times the model said
// ~70%, did ~70% survive? The two middle columns matching is the whole test.
func reliability(preds []pred) {
	const bins = 10
	var sum, obs [bins]float64
	var n [bins]int
	for _, p := range preds {
		b := int(p.q * bins)
		if b >= bins {
			b = bins - 1 // q == 1 exactly, which happens on undrafted-radar players
		}
		sum[b] += p.q
		obs[b] += p.y
		n[b]++
	}

	fmt.Printf("\nreliability — engine, %d bins by predicted survival\n", bins)
	fmt.Printf("  %-10s %10s %10s %8s\n", "bin", "predicted", "observed", "n")
	for b := 0; b < bins; b++ {
		label := fmt.Sprintf("%.1f-%.1f", float64(b)/bins, float64(b+1)/bins)
		if n[b] == 0 {
			// A dash, never 0.00 — an empty bin has no opinion and printing one
			// would read as "the model said 0% and was right".
			fmt.Printf("  %-10s %10s %10s %8d\n", label, "-", "-", 0)
			continue
		}
		f := float64(n[b])
		fmt.Printf("  %-10s %10.4f %10.4f %8d\n", label, sum[b]/f, obs[b]/f, n[b])
	}
}

// segments splits the same predictions three ways. The tails are where the tool
// earns its keep: a long horizon is where waiting actually costs you, and the
// deep board is where per-player sigma is doing the most work.
func segments(preds []pred) {
	segmentTable("by horizon (picks between my two vantages)", preds, []seg{
		{"<= 6 picks", func(p pred) bool { return p.horizon() <= 6 }},
		{"7-12 picks", func(p pred) bool { return p.horizon() >= 7 && p.horizon() <= 12 }},
		{"13+ picks", func(p pred) bool { return p.horizon() >= 13 }},
	})

	var byPos []seg
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		want := pos
		byPos = append(byPos, seg{strings.ToLower(pos), func(p pred) bool { return p.pos == want }})
	}
	segmentTable("by position", preds, byPos)

	// Where a round ends is each draft's own answer: pick 30 in a 10-team league,
	// 36 in a 12-team one. Reading the cut off preds[0] and applying it to
	// everyone would file a second league's adp-31 players under "rounds 1-3"
	// when they are round-4 picks there — the wrong board, silently.
	early := func(p pred) float64 { return float64(3 * p.teams) }
	mid := func(p pred) float64 { return float64(8 * p.teams) }
	segmentTable("by adp depth", preds, []seg{
		{"rounds 1-3" + depthCut(preds, 3), func(p pred) bool { return p.adp <= early(p) }},
		{"rounds 4-8" + depthCut(preds, 8), func(p pred) bool { return p.adp > early(p) && p.adp <= mid(p) }},
		{"rounds 9+", func(p pred) bool { return p.adp > mid(p) }},
	})
}

// depthCut names the adp boundary in the label, but only while every prediction
// came from one league size — over mixed sizes there is no single number to
// print and naming one would be the same lie the per-pred cut just fixed.
func depthCut(preds []pred, rounds int) string {
	if len(preds) == 0 {
		return ""
	}
	teams := preds[0].teams
	for _, p := range preds {
		if p.teams != teams {
			return ""
		}
	}
	return fmt.Sprintf(" (adp <= %d)", rounds*teams)
}

type seg struct {
	label string
	keep  func(pred) bool
}

func segmentTable(title string, preds []pred, segs []seg) {
	fmt.Printf("\n%s\n", title)
	fmt.Printf("  %-26s %8s %8s %9s %10s %10s\n", "segment", "n", "brier", "log-loss", "predicted", "observed")
	for _, sg := range segs {
		var sub []pred
		for _, p := range preds {
			if sg.keep(p) {
				sub = append(sub, p)
			}
		}
		if len(sub) == 0 {
			continue // a position nobody had an adp for isn't a zero, it's absent
		}
		s := scoreOf(sub, modelEngine)
		fmt.Printf("  %-26s %8d %8.4f %9.4f %10.4f %10.4f\n",
			sg.label, s.n, s.brier, s.logLoss, s.mean, s.obs)
	}
}
