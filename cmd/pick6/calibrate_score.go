package main

import (
	"fmt"
	"math"
	"sort"
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

	// The tilt is not a per-player transform — it is one exponent solved over a
	// whole vantage's board at once — so a flat list of predictions is not enough
	// to compute it. vantage says which (draft, seat, round) this row came from;
	// idx is its position in the global slice, which is how a solved exponent
	// finds its way back to the rows it belongs to through any segment filter.
	vantage int
	idx     int
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

// ---- the exactly-n tilt, scored the same way as everything else ----

// vantageTilt is one solved exponent and the three numbers it was solved
// between. The gap between `model` and `actual` is the bias the tilt exists to
// remove; the gap between `n` and `actual` is the price of aiming at it with a
// number that does not leak the labels.
type vantageTilt struct {
	c      float64
	n      int     // picks in the window — what the tilt targets
	model  float64 // sum(1-q): removals the untilted model expected
	actual float64 // sum(1-y): board players the window really took
}

// tilted corrects any model with the exactly-n tilt, one exponent per vantage.
//
// n is every pick in [from, to). At a backtest vantage that count includes my
// own pick at `from` — I am one of the drafters removing a player, and the
// labels agree (a player I take at `from` has d < to, so y = 0). Live the engine
// counts only opponent picks, because there its window starts at somebody else's
// pick and my own is the horizon, never inside it. Same rule, one window, every
// pick in it removes exactly one player.
//
// Keyed by pred.idx rather than recomputed per subset on purpose: a segment is a
// grading filter, not a different draft. The exponent for "13+ picks" rows must
// be the one solved over the whole board at that vantage, exactly as the engine
// would have solved it — re-solving over the filtered subset would invent a
// draft in which only long-horizon players exist. That keying is why runCalibrate
// numbers pred.idx once, over the final slice, before anything reads it.
func tilted(preds []pred, q func(pred) float64) (func(pred) float64, []vantageTilt) {
	groups := map[int][]int{}
	var order []int // map iteration is random and the returned summary must not be
	for _, p := range preds {
		if _, seen := groups[p.vantage]; !seen {
			order = append(order, p.vantage)
		}
		groups[p.vantage] = append(groups[p.vantage], p.idx)
	}

	out := make([]float64, len(preds))
	vts := make([]vantageTilt, 0, len(order))
	for _, v := range order {
		idx := groups[v]
		ps := make([]float64, len(idx))
		vt := vantageTilt{n: preds[idx[0]].horizon()}
		for i, j := range idx {
			ps[i] = q(preds[j])
			vt.model += 1 - ps[i]
			vt.actual += 1 - preds[j].y
		}
		vt.c = solveTilt(ps, vt.n)
		for i, j := range idx {
			out[j] = math.Pow(ps[i], vt.c)
		}
		vts = append(vts, vt)
	}
	return func(p pred) float64 { return out[p.idx] }, vts
}

// solveTilt finds the exponent c with sum(1 - p^c) = n: the correction that
// makes the model expect exactly as many removals as the window really makes.
// A power is the gentlest available fix, because ln p is the player's cumulative
// hazard over those picks, so p^c scales every hazard by one factor and leaves
// the fixed points alone — p = 1 stays 1, p = 0 stays 0, ordering is preserved.
//
// This is a SECOND implementation of engine.survivalTilt, which is unexported
// and reads its horizon off NextPick(). A backtest vantage stands at my own pick
// and asks about my next one, so NextPick() is the vantage itself and the
// engine's own solve degenerates to c = 1 — there is no way to drive it to this
// question from outside the package. The duplication is therefore checked rather
// than assumed: tiltSelfCheck runs both over one shared board on every run and
// prints how far apart they landed, and calibrate_test.go fails the build if
// they ever disagree past 1e-12.
func solveTilt(ps []float64, n int) float64 {
	if n <= 0 {
		return 1
	}
	// Each log-survival once, then bisect over the array — the alternative is
	// ~26x the transcendental calls for a bit-identical answer.
	lnp := make([]float64, len(ps))
	for i, p := range ps {
		lnp[i] = math.Log(p)
	}
	f := func(c float64) float64 {
		total := 0.0
		for _, l := range lnp {
			total += 1 - math.Exp(c*l)
		}
		return total
	}
	// f is continuous and strictly increasing in c, so plain bisection is enough.
	// Both clamps are real board states: f(hi) < n means the pool cannot supply n
	// removals however hungry the room gets, f(lo) > n means it is stacked with
	// players the model has already written off while too few picks remain to
	// take them all.
	lo, hi := 1/engine.TiltCMax, engine.TiltCMax
	if f(hi) < float64(n) {
		return hi
	}
	if f(lo) > float64(n) {
		return lo
	}
	for hi-lo > engine.TiltTol {
		mid := (lo + hi) / 2
		if f(mid) < float64(n) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// tiltSelfCheck grades the local solve against the engine's own on a board both
// can see, so the two never drift apart unnoticed. It builds a state whose next
// pick is 18 away — the engine's tilt then fires for real — and compares
// PSurviveTilted against pow(PSurviveAt, solveTilt) player by player.
func tiltSelfCheck() (worst, c float64, players int) {
	board := map[string]engine.Player{}
	for i := 1; i <= 60; i++ {
		id := fmt.Sprintf("chk%02d", i)
		board[id] = engine.Player{
			ID: id, Name: id, Pos: "RB", Team: "FA",
			ADP: float64(i), Sigma: engine.SigmaDefault, Value: 100 - i, Tier: 1,
		}
	}
	s := engine.New(board, 12, 15, 3)
	s.PickNo = 4 // slot 3 picks at 3 and again at 22: 18 picks intervene

	at := s.NextPick()
	ps := make([]float64, 0, len(board))
	for _, p := range board {
		ps = append(ps, s.PSurviveAt(p, at))
	}
	c = solveTilt(ps, s.PicksUntilMine())
	for _, p := range board {
		d := math.Abs(s.PSurviveTilted(p) - math.Pow(s.PSurviveAt(p, at), c))
		if d > worst {
			worst = d
		}
	}
	return worst, c, len(board)
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

	tiltFn, vts := tilted(preds, modelEngine)
	flatTiltFn, _ := tilted(preds, modelFlatSigma)
	shipped := namedModel{"engine", modelEngine}
	tilt := namedModel{"tilted", tiltFn}

	fmt.Printf("\n%-34s %8s %9s %10s %10s\n", "model", "brier", "log-loss", "predicted", "d brier")
	row := func(label string, s score) {
		// A baseline that wins says so on its own line. The whole point of
		// running this is that the fancy math doesn't get to grade itself — and
		// that applies hardest to the tilt row, which is here to be gated.
		beat, delta := "", "-"
		if s.brier != base.brier {
			delta = fmt.Sprintf("%+.4f", s.brier-base.brier)
		}
		if s.brier < base.brier {
			beat = "   <- beats the engine"
		}
		fmt.Printf("  %-32s %8.4f %9.4f %10.4f %10s%s\n", label, s.brier, s.logLoss, s.mean, delta, beat)
	}
	row("engine (per-player sigma)", base)
	row("engine + exactly-n tilt", scoreOf(preds, tiltFn))
	row(fmt.Sprintf("baseline: constant %.3f", base.obs), scoreOf(preds, modelConstant(base.obs)))
	row(fmt.Sprintf("baseline: sigma %.1f flat", engine.SigmaDefault), scoreOf(preds, modelFlatSigma))
	row(fmt.Sprintf("baseline: sigma %.1f flat + tilt", engine.SigmaDefault), scoreOf(preds, flatTiltFn))
	row("baseline: unconditional", scoreOf(preds, modelUnconditional))

	tiltReport(vts)
	// Both models share one set of bins — the engine's — so the tails line up
	// row for row and the question "did the tilt move the bad bins toward the
	// observed rate" is answered by reading across, not by matching two tables
	// whose rows hold different players.
	reliability("bins by the engine's prediction, so both models share the rows",
		preds, modelEngine, []namedModel{shipped, tilt})
	// And then the tilt graded on its own bins, because the table above cannot
	// see whether the tilt invented a new region of overconfidence.
	reliability("bins by the tilt's own prediction — its calibration, not a comparison",
		preds, tiltFn, []namedModel{tilt})
	segments(preds, shipped, tilt)
}

// tiltReport shows what the correction actually had to work with. The exponent
// is the whole model — one number per vantage — so it gets printed rather than
// trusted, and the two removal counts either side of it say whether the target
// was reachable at all.
func tiltReport(vts []vantageTilt) {
	if len(vts) == 0 {
		return
	}
	worst, c, players := tiltSelfCheck()

	cs := make([]float64, len(vts))
	var clamped int
	var sumN, sumModel, sumActual float64
	for i, v := range vts {
		cs[i] = v.c
		if v.c >= engine.TiltCMax || v.c <= 1/engine.TiltCMax {
			clamped++
		}
		sumN += float64(v.n)
		sumModel += v.model
		sumActual += v.actual
	}
	sort.Float64s(cs)
	f := float64(len(vts))

	fmt.Printf("\ntilt — one exponent per vantage, solved so expected removals equal the window\n")
	note("solve", "checked", fmt.Sprintf(
		"local solve matches engine.PSurviveTilted to %.1e over %d players (c %.6f)", worst, players, c))
	note("exponent c", "solved", fmt.Sprintf(
		"%.4f / %.4f / %.4f (min / median / max over %d vantages) · %d clamped",
		cs[0], cs[len(cs)/2], cs[len(cs)-1], len(vts), clamped))
	note("removals", "mean", fmt.Sprintf(
		"target %.1f picks · model expected %.1f · the board really lost %.1f",
		sumN/f, sumModel/f, sumActual/f))
	// The gap between the target and what the board really lost is not an error
	// in the tilt, it is the tilt being honest: a 15-round 12-team draft makes
	// 180 picks against a 178-name era board, so a good share of every window is
	// spent on players no adp source ranked. Live the engine has exactly the same
	// leak — EnsurePlayer registers those picks with no adp, they survive with
	// probability 1, and they sit in the normalization pool contributing nothing.
	// Aiming at the observed count instead would score the labels against
	// themselves.
	fmt.Printf("  %-12s %-8s %s\n", "", "",
		"the target counts every pick; the board only holds the players adp ranked,")
	fmt.Printf("  %-12s %-8s %s\n", "", "",
		"so the rest went to unranked names — the same leak the live engine has.")
}

// namedModel is a scoring function with the label it prints under.
type namedModel struct {
	label string
	f     func(pred) float64
}

// reliability is the brier score made visible: of all the times the model said
// ~70%, did ~70% survive? A model's column matching the observed column is the
// whole test. Binning is a parameter because comparing two models needs them on
// shared rows, while grading one model needs it on its own.
func reliability(title string, preds []pred, binBy func(pred) float64, models []namedModel) {
	const bins = 10
	sums := make([][bins]float64, len(models))
	var obs [bins]float64
	var n [bins]int
	for _, p := range preds {
		b := int(binBy(p) * bins)
		if b >= bins {
			b = bins - 1 // q == 1 exactly, which happens on undrafted-radar players
		}
		if b < 0 {
			b = 0
		}
		for i, m := range models {
			sums[i][b] += m.f(p)
		}
		obs[b] += p.y
		n[b]++
	}

	fmt.Printf("\nreliability — %s\n", title)
	fmt.Printf("  %-10s", "bin")
	for _, m := range models {
		fmt.Printf(" %10s", m.label)
	}
	fmt.Printf(" %10s %8s\n", "observed", "n")
	for b := 0; b < bins; b++ {
		label := fmt.Sprintf("%.1f-%.1f", float64(b)/bins, float64(b+1)/bins)
		if n[b] == 0 {
			// A dash, never 0.00 — an empty bin has no opinion and printing one
			// would read as "the model said 0% and was right".
			fmt.Printf("  %-10s", label)
			for range models {
				fmt.Printf(" %10s", "-")
			}
			fmt.Printf(" %10s %8d\n", "-", 0)
			continue
		}
		f := float64(n[b])
		fmt.Printf("  %-10s", label)
		for i := range models {
			fmt.Printf(" %10.4f", sums[i][b]/f)
		}
		fmt.Printf(" %10.4f %8d\n", obs[b]/f, n[b])
	}
}

// segments splits the same predictions three ways. The tails are where the tool
// earns its keep: a long horizon is where waiting actually costs you, and the
// deep board is where per-player sigma is doing the most work.
func segments(preds []pred, a, b namedModel) {
	// Horizon first and always: the tilt's entire claim is about how many picks
	// intervene, so a tilt that helps overall while losing the long horizons has
	// not done the thing it was built to do.
	segmentTable("by horizon (picks between my two vantages)", preds, []seg{
		{"<= 6 picks", func(p pred) bool { return p.horizon() <= 6 }},
		{"7-12 picks", func(p pred) bool { return p.horizon() >= 7 && p.horizon() <= 12 }},
		{"13+ picks", func(p pred) bool { return p.horizon() >= 13 }},
	}, a, b)

	var byPos []seg
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		want := pos
		byPos = append(byPos, seg{strings.ToLower(pos), func(p pred) bool { return p.pos == want }})
	}
	segmentTable("by position", preds, byPos, a, b)

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
	}, a, b)
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

func segmentTable(title string, preds []pred, segs []seg, a, b namedModel) {
	fmt.Printf("\n%s — %s -> %s\n", title, a.label, b.label)
	fmt.Printf("  %-24s %7s %9s %18s %18s %18s\n",
		"segment", "n", "observed", "brier", "log-loss", "predicted")
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
		sa, sb := scoreOf(sub, a.f), scoreOf(sub, b.f)
		fmt.Printf("  %-24s %7d %9.4f %18s %18s %18s\n", sg.label, sa.n, sa.obs,
			pair(sa.brier, sb.brier), pair(sa.logLoss, sb.logLoss), pair(sa.mean, sb.mean))
	}
}

// pair prints one metric for both models in one cell. Two full tables would put
// the numbers being compared a screen apart; side by side, the sign of the
// change is readable without arithmetic.
func pair(a, b float64) string { return fmt.Sprintf("%.4f -> %.4f", a, b) }
