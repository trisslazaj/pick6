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
	// id is the sleeper player id, carried so a model can be repriced off a
	// different warp without re-walking the draft — which is what the cutoff
	// sweep does, eleven times per fold.
	id    string
	pos   string
	adp   float64
	stdev float64 // 0 when ffc reported none, i.e. the sigma fallback fired
	from  int
	to    int
	teams int
	q     float64 // the engine's own conditional survival, per-player sigma
	y     float64 // 1 if he really was still available at `to`

	// 4b's inputs. sigmaShrunk is the variance-shrunk sigma fetch writes now;
	// prior is the pool's expected stdev at this adp, kept separately so -tune
	// can re-blend at any pseudo-count. high/drafts are ffc's own support:
	// the earliest pick anyone took him at, and how many drafts that is over.
	sigmaShrunk float64
	prior       float64
	high        int
	drafts      int

	// 3a's inputs. adpRoom is the full-depth room-warped effective adp for this
	// player, built from the OTHER drafts only; qRoom is the engine's survival off
	// it, recorded during the walk through engine.Player.ADPEff so the graded
	// number comes out of the shipped reader. adpRoomTop is the same warp applied
	// only to the top retractedCutoff players at each position — the variant the
	// board actually prices, and the one the shipped row is scored on.
	adpRoom    float64
	adpRoomTop float64
	qRoom      float64

	// The source's second adp column, when it has one, warped the same way. A
	// fantasypros export prices every player twice — sleeper's own adp and the
	// three-platform consensus — and the drafts being scored were run on sleeper,
	// so which one predicts them better is a real question. Zero on any board with
	// a single price, and columnGate checks that before printing anything.
	adpAlt    float64
	adpAltTop float64

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

// betterPrinted compares two scores at the four decimals every table in this
// command prints. A win too small to show up there is a win nobody can check,
// and "beats what ships" next to two columns of identical digits reads as a bug
// in the table — which is exactly what the support-floor row would do, since the
// floor bites on no predictions at all against the shrunk curve.
//
// The gate reads it in the other direction too, and that is the load-bearing
// use: 3a's per-position check calls a position "worse" only when the table the
// reader is looking at can show it. wr's log-loss under the shipped warp moves
// +0.000032, which prints as 0.2450 -> 0.2450 — a claim of regression nobody
// could verify from the output, so it is scored as a tie and printed as one.
func betterPrinted(a, b float64) bool { return math.Round(a*1e4) < math.Round(b*1e4) }

// ---- the models being compared ----

// modelEngine is the plain conditional logistic over the source's raw stdev,
// recorded during the walk so this is literally the engine's own arithmetic and
// not a reimplementation of it.
//
// It is the BASE every other row is measured against, not what ships: fetch
// shrinks sigma, the tilt corrects the whole vantage, and 3a warps the top of
// each position's price, so the shipped model is the "top-5 warp + shrunk +
// tilt" row. Keeping the base uncorrected is the point — a baseline that quietly
// adopts each improvement can never show one.
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

// modelRoomTopFlat is the shipped model with sigma forced to SigmaDefault for
// every player.
//
// It exists because it is the only regime EVERY fold can run in. A fantasypros
// export carries no stdev, so a fold priced off one has no per-player spread to
// shrink or clamp and runs flat whether anyone asks it to or not. Comparing that
// fold's brier against an ffc fold's per-player-sigma brier would be comparing
// two different models on two different drafts and calling the difference a
// finding, so crossFold forces every fold onto this one and reports it beside
// each fold's own best available.
func modelRoomTopFlat(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoom, engine.SigmaDefault)
}

// modelAltTop is the shipped model priced off the source's OTHER adp column.
// Same sigma, same warp, same tilt when it is tilted — the only thing that
// changes is the price, which is what makes the comparison a comparison.
func modelAltTop(p pred) float64 {
	return psurvive(p.from, p.to, p.adpAltTop, p.sigmaShrunk)
}

// ---- 4b: the two support-driven corrections, each scorable on its own ----

// modelShrunkSigma is the engine's curve with the variance-shrunk sigma instead
// of the raw one. The shrink is fetch-side arithmetic (adp.StdevPrior.Shrink),
// precomputed per row during the walk, so this grades what fetch now writes and
// not a reimplementation of it.
func modelShrunkSigma(p pred) float64 {
	return psurvive(p.from, p.to, p.adp, p.sigmaShrunk)
}

// modelSupportFloor raises the shipped curve to the rule-of-three bound wherever
// the horizon lands inside the window no observed draft ever took him in. It
// calls engine.SupportFloor rather than restating the formula — the tilt's
// duplicated solve in this file is the standing reminder of what two copies of
// one model costs.
func modelSupportFloor(p pred) float64 {
	return engine.SupportFloor(p.q, p.to, p.high, p.drafts)
}

// modelShrunkFloor is both, in the order the engine applies them: sigma is
// chosen at fetch, the floor is applied to the curve it produces.
func modelShrunkFloor(p pred) float64 {
	return engine.SupportFloor(modelShrunkSigma(p), p.to, p.high, p.drafts)
}

// floorReach counts what the floor is even allowed to touch. A correction that
// fires on twelve rows out of sixteen thousand is not an improvement however
// good its brier looks, so the count is printed next to the score rather than
// left for the reader to assume.
//
// applies is "the horizon is at or before his earliest observed pick"; binds is
// the subset where the bound was actually above the model's own answer. lift is
// the mean size of the move over the rows it bound on.
func floorReach(preds []pred, base func(pred) float64) (applies, binds int, lift float64) {
	for _, p := range preds {
		if p.high <= 0 || p.drafts <= 0 || p.to > p.high {
			continue
		}
		applies++
		b := base(p)
		if f := engine.SupportFloor(b, p.to, p.high, p.drafts); f > b {
			binds++
			lift += f - b
		}
	}
	if binds > 0 {
		lift /= float64(binds)
	}
	return applies, binds, lift
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

// foldHeader says which draft the tables below grade and what they grade it
// against. With more than one fold in the output, every table is ambiguous
// without it — and the room-warp exclusion line is the one piece of provenance
// that, if it were wrong, would make all of them meaningless.
func foldHeader(f *fold, byID map[string]*fold) {
	name := f.id
	if f.league != "" {
		name += " · " + strings.ToLower(f.league)
	}
	fmt.Printf("\nfold %s — %s\n", f.label, name)
	note("draft", "scored", fmt.Sprintf("%s · %d teams, %d rounds, %d picks · %s",
		f.season, f.draft.Settings.Teams, f.draft.Settings.Rounds, f.picks,
		strings.ReplaceAll(f.draft.Metadata.ScoringType, "_", "-")))

	b := f.board
	switch b.source {
	case "fantasypros":
		note("board", "csv", fmt.Sprintf("fantasypros · %d joined players · %s column · %d rows fell back to avg",
			len(b.players), b.col, b.fallbacks))
	default:
		note("board", b.source, fmt.Sprintf("%d joined players, %d drafts (%s)",
			len(b.players), b.drafts, b.res.Window))
	}
	if b.truncatedFrom > 0 {
		// Loud and on the fold's own header, not only in the flag's help text. A
		// truncated board is a controlled experiment, and every number under this
		// line answers "what if the boards were the same size" rather than "how
		// did the model do" — which is the reading error the whole line prevents.
		note("depth", "truncated", fmt.Sprintf(
			"board cut from %d to %d cheapest · diagnostic only, not this fold's result",
			b.truncatedFrom, len(b.players)+len(b.dropped)))
	}
	note("era check", eraTag(f.draft, b), eraDetail(f.draft, b))
	note("room warp", roomWarpTag(f), roomWarpDetail(f, byID))
}

func report(f *fold, byID map[string]*fold) {
	preds := f.preds
	foldHeader(f, byID)

	base := scoreOf(preds, modelEngine)
	fmt.Printf("\nscored %d predictions · %d vantages\n", base.n, f.vantages)
	fmt.Printf("observed survival rate %.4f — a model that beats nothing scores brier %.4f\n",
		base.obs, base.obs*(1-base.obs))

	tiltFn, vts := tilted(preds, modelEngine)
	flatTiltFn, _ := tilted(preds, modelFlatSigma)
	shrunkTiltFn, _ := tilted(preds, modelShrunkSigma)
	floorTiltFn, _ := tilted(preds, modelSupportFloor)
	bothTiltFn, _ := tilted(preds, modelShrunkFloor)
	roomTiltFn, _ := tilted(preds, modelRoom)
	roomShrunkTiltFn, _ := tilted(preds, modelRoomShrunk)
	roomTopTiltFn, _ := tilted(preds, modelRoomTopShrunk)
	roomTopFlatTiltFn, _ := tilted(preds, modelRoomTopFlat)
	plain := namedModel{"engine", modelEngine}
	tilt := namedModel{"tilted", tiltFn}
	// The previous default, kept as a column rather than dropped: 3a's warp is a
	// price change laid on top of it, so it is the thing the warp has to beat, and
	// dropping the middle model would leave the shrink's contribution
	// indistinguishable from the tilt's.
	current := namedModel{"shrunk+tilt", shrunkTiltFn}
	// What ships: that model with the room warp on its price, at every depth the
	// curve reaches. The label carries a plus because the reliability columns read
	// as a progression — each one is the column before it plus one correction —
	// and a bare "room warp" would collide with the model table's untilted 3a row.
	shipped := namedModel{"+room warp", roomShrunkTiltFn}

	// What the marker is measured against. It used to be the first row — the
	// pre-4x engine — which after this milestone is not what ships: eleven of
	// fifteen rows beat it, the marker stopped separating anything, and the one
	// baseline that beat the then-shipped model on log-loss (flat sigma 6.0 under
	// the tilt, 0.2233 against 0.2250) read like all the others. The whole point
	// of running this is that the fancy math doesn't get to grade itself, so it is
	// graded against the fancy math that ships — the full-depth warp row, since
	// the top-5 cap was retracted on the two causal folds.
	ship := scoreOf(preds, roomShrunkTiltFn)
	better := betterPrinted
	fmt.Printf("\n%-34s %8s %9s %10s %10s\n", "model", "brier", "log-loss", "predicted", "d brier")
	line := func(label string, s score, tag string) {
		delta := "-"
		if s.brier != base.brier {
			delta = fmt.Sprintf("%+.4f", s.brier-base.brier)
		}
		// The marker is APPENDED to a row's own label, never replaced by it. A
		// named row can still beat what ships — the flat-sigma warp row does, on
		// log-loss, on the 2024 fold — and swallowing the marker because the row
		// already had something to say would hide exactly the comparison this
		// column exists to make. Rows compared against themselves never trigger
		// it, since betterPrinted is strict.
		beat := ""
		switch {
		case better(s.brier, ship.brier) && better(s.logLoss, ship.logLoss):
			beat = "   <- beats what ships"
		case better(s.brier, ship.brier):
			beat = "   <- beats what ships on brier"
		case better(s.logLoss, ship.logLoss):
			beat = "   <- beats what ships on log-loss"
		}
		fmt.Printf("  %-32s %8.4f %9.4f %10.4f %10s%s%s\n", label, s.brier, s.logLoss, s.mean, delta, tag, beat)
	}
	row := func(label string, s score) { line(label, s, "") }
	row("engine (per-player sigma)", base)
	row("engine + exactly-n tilt", scoreOf(preds, tiltFn))
	row("4b: shrunk sigma", scoreOf(preds, modelShrunkSigma))
	line("4b: shrunk sigma + tilt", scoreOf(preds, shrunkTiltFn), "   <- the default before 3a")
	row("4b: support floor", scoreOf(preds, modelSupportFloor))
	row("4b: support floor + tilt", scoreOf(preds, floorTiltFn))
	row("4b: shrunk + floor + tilt", scoreOf(preds, bothTiltFn))
	row("3a: room-warped adp", scoreOf(preds, modelRoom))
	row("3a: room warp + tilt", scoreOf(preds, roomTiltFn))
	line("3a: room warp + shrunk + tilt", ship, "   <- ships")
	// The retracted cap, kept on the table because "the warp is wrong" and "the
	// warp is wrong past the fifth man at a position" are different findings, and
	// this row is what tells them apart.
	row(fmt.Sprintf("3a: top-%d warp + shrunk + tilt (retracted)", retractedCutoff),
		scoreOf(preds, roomTopTiltFn))
	// The same model with every sigma forced flat. Not a baseline and not a
	// candidate: it is the one regime a board with no stdev can run in, so it is
	// what crossFold compares folds on. On a flat-sigma fold it IS the shipped
	// row, printed twice on purpose — the tie is the proof the fold is really flat.
	line(fmt.Sprintf("3a: room warp + flat %.1f + tilt", engine.SigmaDefault),
		scoreOf(preds, roomTopFlatTiltFn), "   <- the cross-fold regime")
	row(fmt.Sprintf("baseline: constant %.3f", base.obs), scoreOf(preds, modelConstant(base.obs)))
	row(fmt.Sprintf("baseline: sigma %.1f flat", engine.SigmaDefault), scoreOf(preds, modelFlatSigma))
	row(fmt.Sprintf("baseline: sigma %.1f flat + tilt", engine.SigmaDefault), scoreOf(preds, flatTiltFn))
	row("baseline: unconditional", scoreOf(preds, modelUnconditional))
	fmt.Println("  the tilt ships, so the rows to compare 4b against are the tilted ones.")
	if f.board.flatSigma {
		// Said here rather than left for the reader to spot: on this board the
		// per-player, shrunk, floored and flat rows are literally one model, so
		// the ties above are structural and not a finding about sigma.
		fmt.Println("  this board reports no stdev, so every sigma above is sigmadefault — the")
		fmt.Println("  per-player, shrunk, floored and flat rows are one model printed five times.")
		fmt.Println("  read the tilt, the warp and the conditioning here; read nothing about sigma.")
	}

	columnGate(f)
	supportReport(preds)
	tiltReport(vts)
	// Every model shares one set of bins — the engine's — so the tails line up
	// row for row and the question "did each correction move the bad bins toward
	// the observed rate" is answered by reading across, not by matching tables
	// whose rows hold different players.
	reliability("bins by the engine's prediction, so all four models share the rows",
		preds, modelEngine, []namedModel{plain, tilt, current, shipped})
	// And then the shipped model graded on its own bins, because the table above
	// cannot see whether it invented a new region of overconfidence.
	reliability("bins by the shipped model's own prediction — its calibration, not a comparison",
		preds, roomTopTiltFn, []namedModel{shipped})
	// The segments grade the shrink: tilt alone against tilt plus the shrink. The
	// tails are where a change earns or loses its place — a long horizon is where
	// waiting actually costs you, and the deep board is where per-player sigma,
	// and therefore the shrink, is doing all of its work. The warp's own segments
	// are in the gate section below, against the same tables.
	segments(preds, tilt, current)
	// 3a gets its own section because it is a gate, not a column: it is the
	// evidence the shipped model prices the room's numbers at the top of a
	// position and the market's everywhere else.
	roomGate(preds, current,
		namedModel{"room warp", roomShrunkTiltFn},
		namedModel{fmt.Sprintf("top-%d warp", retractedCutoff), roomTopTiltFn})
}

// columnGate answers the question having two adp columns exists to answer: does
// sleeper's own adp predict a SLEEPER draft better than the cross-platform
// consensus does?
//
// It is a fair fight by construction. Both columns price the same players in the
// same order, both go through the same top-of-position warp, both carry the same
// sigma, both get their own exponent solved over their own board at every
// vantage. The only thing that differs is the price, so the delta is the columns
// and nothing else.
//
// The fallback count matters as much as the score: where a platform never ranked
// a player his price IS the consensus, so on the deep board the two columns are
// partly the same number and the comparison is diluted exactly there.
func columnGate(f *fold) {
	b := f.board
	if b.altCol == "" {
		return
	}
	var priced int
	for _, p := range f.preds {
		if p.adpAlt > 0 {
			priced++
		}
	}
	if priced == 0 {
		return
	}
	primary, _ := tilted(f.preds, modelRoomTopShrunk)
	alt, _ := tilted(f.preds, modelAltTop)
	sp, sa := scoreOf(f.preds, primary), scoreOf(f.preds, alt)

	fmt.Printf("\nadp column — the same board priced twice, %s (scored) against %s\n", b.col, b.altCol)
	// Both fallback counts, because they are the dilution: where a column was
	// blank the player's price IS the consensus, so on those rows the two models
	// are literally the same number and the comparison is weakest exactly there.
	note("reach", "priced", fmt.Sprintf(
		"%d of %d predictions carry both prices · blank source rows filled from avg: %s %d, %s %d",
		priced, len(f.preds), b.col, b.fallbacks, b.altCol, b.altFallbacks))
	note("brier", verdict(sp.brier, sa.brier), fmt.Sprintf("%s %.4f -> %s %.4f (%+.4f)",
		b.col, sp.brier, b.altCol, sa.brier, sa.brier-sp.brier))
	note("log-loss", verdict(sp.logLoss, sa.logLoss), fmt.Sprintf("%s %.4f -> %s %.4f (%+.4f)",
		b.col, sp.logLoss, b.altCol, sa.logLoss, sa.logLoss-sp.logLoss))
	segmentTable("adp column by position", f.preds, positionSegs(),
		namedModel{b.col, primary}, namedModel{b.altCol, alt})
	fmt.Println("  a platform's own adp is not automatically the better price for a draft run on")
	fmt.Println("  that platform: its column is a ranking, the consensus is an average of three,")
	fmt.Println("  and averages are usually better calibrated. the table decides, not the intuition.")
}

// crossFold is the whole point of a second fold.
//
// Every gate in this project rested on one draft, and a single fold cannot tell
// a real edge from a lucky one. Two can start to — but only if they are compared
// in a regime they can both run, and a board with no stdev can only run flat. So
// this table forces every fold onto sigma 6.0 and prints each fold's own best
// available beside it. Where those two columns are identical, the fold really
// was flat all along, which is the visible proof rather than a claim.
//
// It does NOT pool the folds. Different rooms, different round counts, different
// boards: pooling would let one fold's vantages be tilted against another fold's
// prices, and the average of two folds is not a third measurement.
func crossFold(folds []*fold) {
	if len(folds) < 2 {
		fmt.Println("\ncross-fold — one fold, so there is nothing to cross. every number above")
		fmt.Println("  rests on a single draft night and cannot tell a real edge from a lucky one.")
		return
	}
	fmt.Printf("\ncross-fold — %d folds in the one regime they share: sigma flat at %.1f\n",
		len(folds), engine.SigmaDefault)
	fmt.Printf("  %-8s %-16s %6s %6s %8s %9s %19s %19s\n",
		"fold", "board", "depth", "picks", "n", "observed", "brier", "log-loss")
	for _, f := range folds {
		flatFn, _ := tilted(f.preds, modelRoomTopFlat)
		ownFn, _ := tilted(f.preds, modelRoomTopShrunk)
		flat, own := scoreOf(f.preds, flatFn), scoreOf(f.preds, ownFn)
		board := f.board.source
		if f.board.col != "" {
			board += " " + f.board.col
		}
		fmt.Printf("  %-8s %-16s %6d %6d %8d %9.4f %19s %19s\n",
			f.label, board, len(f.board.players), f.picks, flat.n, flat.obs,
			pair(flat.brier, own.brier), pair(flat.logLoss, own.logLoss))
	}
	fmt.Println("  each metric cell reads flat -> own. 'flat' is the shipped model with every")
	fmt.Println("  sigma forced to sigmadefault; 'own' is that fold's best available price for")
	fmt.Println("  sigma. they are equal exactly when the board carries no stdev at all, which")
	fmt.Println("  is why the equality is left visible rather than collapsed into one column.")
	// The depth column is not decoration and this paragraph is the reason it is
	// there. Ffc publishes ~178 names and fantasypros 389, so a fantasypros fold
	// grades hundreds of players nobody was ever going to take. They survive
	// trivially, which lifts the observed rate and shrinks every brier on that
	// row for free. Two folds' briers are therefore NOT comparable as numbers,
	// only as distances from their own floors — which is why the floors print.
	fmt.Println("  depth is the joined board size against a draft of `picks` picks. a board far")
	fmt.Println("  deeper than the draft is mostly players nobody was ever taking: they survive")
	fmt.Println("  trivially, the observed rate rises and every brier on that row falls for free.")
	fmt.Println("  so do not compare briers across rows. compare each against its own floor:")
	for _, f := range folds {
		s := scoreOf(f.preds, modelEngine)
		flatFn, _ := tilted(f.preds, modelRoomTopFlat)
		flat := scoreOf(f.preds, flatFn)
		note(f.label, "floor", fmt.Sprintf(
			"obs %.4f · constant predictor %.4f · flat model %.4f · %.0f%% of the floor",
			s.obs, s.obs*(1-s.obs), flat.brier, 100*flat.brier/(s.obs*(1-s.obs))))
	}
	fmt.Println("  the last column is the only cross-fold comparison this data supports: what")
	fmt.Println("  fraction of a no-model baseline the model still pays. lower is better.")
}

// supportReport says what 4b's two corrections were allowed to touch, counted
// in predictions — the same unit the scores above are in. A brier delta with no
// reach next to it is unreadable: a correction that fires on twelve rows can
// post any number at all and still mean nothing.
//
// Per prediction, not per player, and deliberately so. The scores are per
// prediction, so "how much of the graded set moved" is the question. The pool
// view of sigma is a different question and -tune's coverage line answers it.
func supportReport(preds []pred) {
	applies, binds, lift := floorReach(preds, modelEngine)

	raws := make([]float64, 0, len(preds))
	shrunks := make([]float64, 0, len(preds))
	var moved int
	for _, p := range preds {
		raw := adp.Sigma(p.stdev)
		raws = append(raws, raw)
		shrunks = append(shrunks, p.sigmaShrunk)
		if math.Abs(p.sigmaShrunk-raw) > 0.05*raw {
			moved++
		}
	}
	sort.Float64s(raws)
	sort.Float64s(shrunks)

	_, sBinds, sLift := floorReach(preds, modelShrunkSigma)

	fmt.Printf("\n4b — what the two corrections reach, in predictions\n")
	if len(raws) > 0 {
		note("shrink", "sigma", fmt.Sprintf(
			"median %.2f -> %.2f · %d of %d rows moved more than 5%%",
			raws[len(raws)/2], shrunks[len(shrunks)/2], moved, len(preds)))
	}
	note("floor", "reach", fmt.Sprintf(
		"horizon inside the unobserved window on %d of %d rows · the bound bites on %d · mean lift %+.4f",
		applies, len(preds), binds, lift))
	// Graded against the shrunk curve too, because the shrink widens sigma and a
	// wider curve is exactly what the floor is supposed to catch. If it stays at
	// zero here, the floor is not being starved by a sigma that was too tight.
	note("", "shrunk", fmt.Sprintf(
		"against the shrunk curve the same bound bites on %d · mean lift %+.4f", sBinds, sLift))
	// The structural reason, stated once so nobody re-derives it: `high` is the
	// MINIMUM observed pick and adp is the mean, so high < adp always, and the
	// floor's window is therefore always before the player's own price — where
	// the logistic already reads near 1. The bound is dominated by the curve
	// almost everywhere by construction, not by accident of this season.
	fmt.Printf("  %-12s %-8s %s\n", "", "",
		"high is the earliest observed pick and adp the mean, so the floor's window")
	fmt.Printf("  %-12s %-8s %s\n", "", "",
		"always sits before a player's own price — where the curve is already near 1.")
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
		"local solve matches the engine's tilted survival to %.1e over %d players (c %.6f)",
		worst, players, c))
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

	segmentTable("by position", preds, positionSegs(), a, b)

	segmentTable("by adp depth", preds, depthSegs(preds), a, b)
}

// depthSegs splits by where on the board a player is priced.
//
// Where a round ends is each draft's own answer: pick 30 in a 10-team league, 36
// in a 12-team one. Reading the cut off preds[0] and applying it to everyone would
// file a second league's adp-31 players under "rounds 1-3" when they are round-4
// picks there — the wrong board, silently.
func depthSegs(preds []pred) []seg {
	early := func(p pred) float64 { return float64(3 * p.teams) }
	mid := func(p pred) float64 { return float64(8 * p.teams) }
	return []seg{
		{"rounds 1-3" + depthCut(preds, 3), func(p pred) bool { return p.adp <= early(p) }},
		{"rounds 4-8" + depthCut(preds, 8), func(p pred) bool { return p.adp > early(p) && p.adp <= mid(p) }},
		{"rounds 9+", func(p pred) bool { return p.adp > mid(p) }},
	}
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

// positionSegs is the by-position split, shared by the standard segment tables
// and 3a's gate — the room warp is a per-position claim, so it is graded on the
// same rows and in the same order the rest of the report uses.
func positionSegs() []seg {
	var out []seg
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		want := pos
		out = append(out, seg{strings.ToLower(pos), func(p pred) bool { return p.pos == want }})
	}
	return out
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
