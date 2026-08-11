package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
)

// Phase 3a's gate, and the evidence behind a default. The room warp shifts a
// player's price toward where these leagues' own drafts took the k-th man at his
// position; this section is what decided that the survival model believes it at
// every depth the curve reaches. It used to believe it only for the top five —
// see retractedCutoff, and adp.RoomGapTopK for what is left of that idea.
//
// The cross-validation is not optional and it has two halves, both in splitPool.
// A warp built from the draft it is scored on would report a skill it does not
// have, so the scored draft is held out. A warp built from drafts that had not
// happened yet reports a skill nobody can use, so those are held out too. All
// three drafts are scored — the 2025 folds price off a hand-exported fantasypros
// board — but only two of them have any prior draft to build a curve from, and
// the 2024 fold, whose whole pool is a year in its future, has none.

// modelRoom is the plain conditional logistic priced against the full-depth
// room-warped adp, recorded during the walk through engine.Player.ADPEff — so
// this grades the field the board reads rather than a second copy of the blend.
func modelRoom(p pred) float64 { return p.qRoom }

// modelRoomShrunk is the full-depth warp on top of the shrunk sigma: what the
// board ships. It is paired with the shrunk sigma rather than the raw one
// because swapping the price AND the sigma at once would leave two changes
// tangled in one brier delta.
func modelRoomShrunk(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoom, p.sigmaShrunk)
}

// retractedCutoff is the cap the board used to price, kept only so the sweep and
// the comparison row can still show what it scored. It is not a tuning knob and
// nothing reads it at the clock; adp.RoomGapTopK holds the reason the number 5
// still means something for the fetch-time READ of the curve.
const retractedCutoff = 5

// modelRoomTopShrunk is that retracted variant — the warp restricted to the top
// retractedCutoff players at each position. It stays on the table because "the
// warp is wrong" and "the warp is wrong past the fifth man at a position" are
// different findings, and this row is what distinguishes them.
func modelRoomTopShrunk(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoomTop, p.sigmaShrunk)
}

// noteRoomSources prints what the pool holds, per draft. Every draft is loaded,
// whatever season it is: the curve reads pick order and position only, so no
// historical adp is involved and a season with no era board still builds a
// perfectly good curve. Which drafts each FOLD may see is a different question,
// answered per fold by splitPool and printed on each fold's header.
func noteRoomSources(sources []adp.RoomSource, lookahead bool) {
	var ok []string
	picks := 0
	for _, s := range sources {
		if s.Err != nil {
			note("room", "skipped", fmt.Sprintf("%s · %s", s.ID, strings.ToLower(s.Err.Error())))
			continue
		}
		ok = append(ok, s.Season)
		picks += s.Picks
	}
	if len(ok) == 0 {
		note("room", "off", "no cached drafts — the 3a rows will tie the raw model")
		return
	}
	note("room", "cached", fmt.Sprintf(
		"%d drafts, %d picks · seasons %s · pick order only, so era is not the constraint here",
		len(ok), picks, strings.Join(ok, ", ")))
	if lookahead {
		// Loud, and on the fourth line of the output. Every warp number below it
		// is from a regime the live tool cannot reach.
		note("room", "lookahead", "each fold's curve may use drafts that started after it — retracted regime, not a result")
	}
}

// roomWarpTag / roomWarpDetail describe the curve one scored draft actually got,
// and why the rest of the pool is not in it. Printing the held-out ids is the
// point: it is the difference between a backtest and a memorization check, and
// between a backtest and a look-ahead, neither of which is visible in a score.
func roomWarpTag(f *fold) string {
	switch {
	case f.curve.Empty():
		return "off"
	case f.lookahead:
		return "look-ahead"
	default:
		return "causal"
	}
}

func roomWarpDetail(f *fold, byID map[string]*fold) string {
	held := heldOut(f, byID)
	if f.curve.Empty() {
		return "no draft precedes this one, so there is no curve it could have had at the clock · " +
			held + " · 3a rows tie the raw model"
	}
	var parts []string
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		if d := f.curve.Depth(pos); d > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ToLower(pos), d))
		}
	}
	// A ceiling, not the weight anyone was warped by: Warp weighs each k by the
	// drafts that actually reached it, so only players inside every draft's depth
	// are priced at this number and the rest are pulled less. Printed bare it read
	// as one weight applied to the whole board, which is true of no player on it.
	return fmt.Sprintf("%s · %s · depth %s · w up to %.2f, thinner past each depth",
		poolLabel(f.allowed, byID), held, strings.Join(parts, "/"),
		float64(f.curve.Drafts)/(float64(f.curve.Drafts)+adp.RoomWarpPseudo))
}

// heldOut names what the fold's curve was not allowed to see, and why. The
// scored draft is always in the list; the rest are there because they had not
// happened yet, which is the exclusion nothing used to make.
func heldOut(f *fold, byID map[string]*fold) string {
	parts := []string{poolKey(f.id, byID) + " scored"}
	if len(f.posterior) > 0 {
		why := " started later"
		if f.lookahead {
			why = " started later, put back by -lookahead"
		}
		parts = append(parts, poolNames(f.posterior, byID)+why)
	}
	if len(f.undated) > 0 {
		parts = append(parts, poolNames(f.undated, byID)+" undated")
	}
	return "held out: " + strings.Join(parts, ", ")
}

// roomGate is 3a's verdict, printed with the evidence that produced it.
//
// The comparison is the pre-warp model — shrunk sigma under the tilt, what the
// board priced before 3a — against that same model with the room's prices, at
// full depth and restricted to the top of each position, overall and per
// position. Per position because the warp's whole claim is positional; both
// depths because only one of them survives.
//
// THE QUARTERBACK CONFLICT, RESOLVED BY MEASUREMENT. Going in there were two true
// and apparently opposed facts: this league takes quarterback SLOTS early (first
// qb at 23.0 against 27.3 national), while this same backtest finds named
// quarterbacks surviving LONGER than adp implies (predicted 0.841, observed
// 0.917). Both hold, because the room drafts quarterbacks early as a group and
// takes DIFFERENT quarterbacks than the national board ranks first, so any given
// named qb sits. The expectation was therefore that the warp, mapping
// position-adp-RANK to room pick, would push named quarterbacks earlier and make
// qb worse.
//
// It does not, and neither does the opposite. Measured, the warp moves qb LATER
// on average — +11.0 picks — even though it moves qb1 through qb5 earlier by 1.7
// to 3.7. The mean is dominated by deep quarterbacks, where mapping rank to room
// pick is structurally wrong (see retractedCutoff), and there are far more of
// them on the board than there are early ones. Pushing them later happens to
// point the same way as the measured error, so qb BRIER improves (0.0970 ->
// 0.0923) while qb log-loss gets worse (0.2966 -> 0.3200): the tail is being
// helped for the wrong reason and the top is being hurt for the right one. That
// is not a signal, it is two errors partially cancelling, and it is exactly why
// the gate reads both metrics and every position instead of one average.
//
// The top-of-position variant settles it, and is what the board now prices.
// Warping only the first five at each position — where the room's early-qb
// behaviour actually lives — improves qb on BOTH metrics (brier 0.0970 ->
// 0.0952, log-loss 0.2966 -> 0.2921), and no position regresses at the precision
// these tables print. Five of six improve on both; wr improves on brier and its
// log-loss moves +0.000032, which prints as 0.2450 -> 0.2450. So the two facts
// were never in conflict: this room takes the first few quarterbacks early, AND
// the twenty-third quarterback on a national board is not somebody this room has
// an opinion about at all.
//
// Those per-position words are not typed here and trusted — roomPosGate computes
// them on every run, and the verdict below branches on what it found. It used to
// be an assertion printed from a condition that only read the two overall
// numbers, which meant the one failure the gate exists to catch, a warp that wins
// on average while wrecking a position, could not have changed a character of the
// output.
func roomGate(preds []pred, prior, full, top namedModel) {
	base, room, tk := scoreOf(preds, prior.f), scoreOf(preds, full.f), scoreOf(preds, top.f)

	var covered, n int
	var shift, absShift float64
	for _, p := range preds {
		n++
		if p.adpRoom == p.adp {
			continue // the curve never reached his rank; he kept the market price
		}
		covered++
		shift += p.adpRoom - p.adp
		absShift += math.Abs(p.adpRoom - p.adp)
	}

	fmt.Printf("\n3a gate — room-warped adp, cross-validated (curve built without the scored draft or its future)\n")
	if covered == 0 {
		fmt.Println("  the warp moved nothing: no curve, or it reached no player on the era board.")
		fmt.Println("  the fold header says which — a fold with no earlier draft has no curve it")
		fmt.Println("  could have had at the clock, and grades the unwarped model twice over.")
		return
	}
	note("reach", "repriced", fmt.Sprintf(
		"%d of %d predictions moved · mean shift %+.2f picks · mean size %.2f",
		covered, n, shift/float64(covered), absShift/float64(covered)))
	// The per-position shift is what makes the table below readable. Without it a
	// reader cannot tell whether a position got better because the warp moved it
	// the right way or because it barely moved at all — and the sign is the whole
	// mechanism: an earlier price means a player is further past it at any vantage,
	// which the conditional ratio reads as SAFER, not likelier to be gone.
	note("shift", "by pos", strings.Join(shiftByPos(preds), " · "))
	note("brier", verdict(base.brier, room.brier), fmt.Sprintf(
		"%.4f -> %.4f (%+.4f)", base.brier, room.brier, room.brier-base.brier))
	note("log-loss", verdict(base.logLoss, room.logLoss), fmt.Sprintf(
		"%.4f -> %.4f (%+.4f)", base.logLoss, room.logLoss, room.logLoss-base.logLoss))
	// The variant that survives the gate, scored right next to the one that
	// doesn't, because "the warp is wrong" and "the warp is wrong past the fifth
	// player at a position" are different findings and only the second is true.
	//
	// The tag reads BOTH metrics. Taken off brier alone it printed "better" on a
	// row whose log-loss column, three characters further along the same line, had
	// got worse — which is the exact reading error this whole section exists to
	// prevent.
	note(fmt.Sprintf("top %d only", retractedCutoff),
		verdictBoth(base.brier, tk.brier, base.logLoss, tk.logLoss), fmt.Sprintf(
			// "the retracted cutoff" rather than "this one ships": it is true
			// at whatever retractedCutoff is set to, including a value that loses
			// this gate, and the verdict below is the thing entitled to say ships.
			"brier %.4f -> %.4f (%+.4f) · log-loss %.4f -> %.4f (%+.4f) · the retracted cutoff",
			base.brier, tk.brier, tk.brier-base.brier,
			base.logLoss, tk.logLoss, tk.logLoss-base.logLoss))
	pg := roomPosGate(preds, prior, top)
	note("", "by pos", pg.line())

	segmentTable("3a by position", preds, positionSegs(), prior, full)
	// And by depth, because the warp's error turns out to BE a depth story: the
	// room's curve only reaches as deep into a position as a finite draft goes, so
	// past that point mapping rank to room pick prices players later than the
	// market does. Splitting by depth is what separates "the room-is-qb-early
	// signal is wrong" from "the signal is fine and the tail is dragging it down".
	segmentTable("3a by adp depth", preds, depthSegs(preds), prior, full)
	// The same positional split for the variant that ships, so the claim "the top
	// of the position is the good part" is a table rather than an assertion.
	segmentTable("3a by position, top-of-position warp only", preds, positionSegs(), prior, top)
	fmt.Println("  the gate is brier and log-loss, both directions, on every segment. a warp")
	fmt.Println("  that helps overall while wrecking one position has still moved the number")
	fmt.Println("  the board sorts by, so it does not ship into survival on an average.")
	if room.brier > base.brier {
		fmt.Println("  verdict, full depth: worse, and it prices nothing. a ranked list runs deeper")
		fmt.Println("  than any finite draft, so past the room's appetite rank->room-pick puts")
		fmt.Println("  players later than the market by construction and the tail swamps the head.")
	} else {
		fmt.Println("  verdict, full depth: not worse on this fold, and this is what the board")
		fmt.Println("  prices. it was once capped at the top five of each position; that cap lost")
		fmt.Println("  to full depth on both causal folds, on both metrics and on this per-position")
		fmt.Println("  gate, so it was retracted. the row below it is what it used to score.")
	}
	if betterPrinted(tk.brier, base.brier) && betterPrinted(tk.logLoss, base.logLoss) && pg.passes() {
		fmt.Printf("  verdict, top %d (retracted): it would clear the gate on this fold too. it is\n",
			retractedCutoff)
		fmt.Println("  still not what ships, because full depth clears it by more on every fold")
		fmt.Println("  that has a causal curve. the row is kept to show what the cap scored.")
	} else {
		fmt.Printf("  verdict, top %d (retracted): does not clear the full gate on this fold. the\n",
			retractedCutoff)
		fmt.Println("  cap was the default until both causal folds beat it — on both metrics and")
		fmt.Println("  on this per-position gate, at native depth and at `-depth 178`. this row is")
		fmt.Println("  the record of that, not a live candidate.")
	}
	fmt.Println("  caveat: this verdict is one fold and one fold only. the drafts before it build")
	fmt.Println("  the curve, this one scores it, and a single draft night cannot tell a real edge")
	fmt.Println("  from an unlucky one. the folds do not have to agree; the cutoff sweep below is")
	fmt.Println("  where they are made to answer together.")
	fmt.Println("  the warp is indexed by rank within a position, so it is also sensitive to how")
	fmt.Println("  deep the board is: the k-th receiver is a different player on a 177-name board")
	fmt.Println("  than on a 387-name one. the mean shift's sign above is where that shows up.")
}

// roomCutoffSweep is retractedCutoff's evidence, per fold, rather than the
// single-fold sweep its doc comment used to cite.
//
// The cutoff is the one fitted number in the room warp: the curve, the blend and
// the pseudo-count are all measured, but "warp the first five and nobody deeper"
// was chosen by sweeping this table on 2024 and reading off the minimum. A
// constant fitted on one fold and validated on the same fold is not measured, so
// the sweep runs on every fold and the folds are allowed to disagree in print.
//
// A FOLD WITH NO CURVE IS NOT A FOLD THAT AGREES. 2024 has no draft before it in
// the cache, so under splitPool's time order it has nothing to warp with and
// contributes no row here at all. That matters more than it sounds: 2024 was the
// only fold that ever preferred a capped warp, so the entire upper bound of the
// old "never worse for k in [1,8]" band came from a curve built out of that
// draft's own future. `-lookahead` reproduces it; nothing in the shipped regime
// can.
//
// `worse` is the gate's own per-position criterion — how many of the six
// positions regress against the unwarped model at the four decimals these tables
// print. The ship condition is the conjunction of a win overall and a zero in
// that column, so a row with the best brier and a nonzero `worse` has still
// failed.
func roomCutoffSweep(folds []*fold) {
	// Endpoints and the shipped value, plus enough interior points to see whether
	// there is a plateau or a slope. 0 is uncapped, printed last because it is the
	// limit of the column above it rather than a separate idea.
	cutoffs := []int{1, 2, 3, 4, 5, 6, 8, 12, 20, 0}

	// Per cutoff: on how many folds does it beat the unwarped model overall, and
	// on how many does no position regress? Tallied rather than eyeballed off the
	// rows, because "which k is safe everywhere" is a column-wise question and the
	// table is printed fold-wise.
	wins := map[int]int{}
	clean := map[int]int{}
	notWorse := map[int]int{}
	scored := 0

	fmt.Println("\nroom warp cutoff — roomwarptopk swept per fold, against the unwarped model")
	fmt.Printf("  %-8s %-12s %8s %9s %7s %s\n",
		"fold", "cutoff", "brier", "log-loss", "worse", "positions regressing")
	var noCurve []string
	for _, f := range folds {
		if f.curve.Empty() {
			noCurve = append(noCurve, f.label)
			continue
		}
		scored++
		unwarped := namedModel{"unwarped", mustTilted(f.preds, modelShrunkSigma)}
		s := scoreOf(f.preds, unwarped.f)
		fmt.Printf("  %-8s %-12s %8.4f %9.4f %7s %s\n", f.label, "none", s.brier, s.logLoss, "-", "-")
		for _, k := range cutoffs {
			eff := f.curve.EffectiveADPTopK(f.board.rows, k)
			m := namedModel{"warped", mustTilted(f.preds, func(p pred) float64 {
				a := p.adp
				if v, ok := eff[p.id]; ok {
					a = v
				}
				return psurvive(p.from, p.to, a, p.sigmaShrunk)
			})}
			ks, g := cutoffLabel(k), roomPosGate(f.preds, unwarped, m)
			w := scoreOf(f.preds, m.f)
			if betterPrinted(w.brier, s.brier) && betterPrinted(w.logLoss, s.logLoss) {
				wins[k]++
			}
			// Never-worse is the weaker and more useful bar: at these margins a tie
			// at four decimals is the common case, and a cutoff that is never worse
			// on any fold is a defensible default even where it is best on none.
			if !betterPrinted(s.brier, w.brier) && !betterPrinted(s.logLoss, w.logLoss) {
				notWorse[k]++
			}
			if g.passes() {
				clean[k]++
			}
			names := "-"
			if len(g.worse) > 0 {
				names = strings.Join(g.worse, " · ")
			}
			fmt.Printf("  %-8s %-12s %8.4f %9.4f %7d %s\n", f.label, ks, w.brier, w.logLoss, len(g.worse), names)
		}
	}
	if len(noCurve) > 0 {
		// Named rather than silently absent: a reader counting rows would otherwise
		// read a two-fold tally as a three-fold one, which is exactly the error
		// this whole section exists to stop.
		fmt.Printf("  %s has no row: every cached draft started after it, so at its own clock\n",
			strings.Join(noCurve, "/"))
		fmt.Println("  there was no curve to build. `-lookahead` scores it against a curve made of")
		fmt.Println("  its own future, which is where the retracted cutoff came from and is the only")
		fmt.Println("  regime any of that evidence exists in.")
	}
	if scored == 0 {
		fmt.Println("  no fold has a curve, so the cutoff has no evidence at all in this regime.")
		return
	}
	fmt.Printf("  the board is uncapped. %d is the retracted cutoff, shown for comparison only.\n", retractedCutoff)
	fmt.Println("  metrics and carries a zero in the worse column — the gate is a conjunction.")
	if scored < 2 {
		fmt.Println("  one fold cannot identify a constant. read the row above as a single draft")
		fmt.Println("  night's opinion, not as a sweep.")
		return
	}

	// The whole reason the sweep runs on every fold: a cutoff chosen on one fold
	// and validated on the same fold is fitted, not measured. This block says out
	// loud whether ANY cutoff survives all of them, and what the shipped one is
	// actually entitled to claim.
	fmt.Printf("\n  across all %d folds — the cutoff is a constant, so it has to hold on all of them\n", scored)
	fmt.Printf("  %-12s %10s %10s %10s\n", "cutoff", "wins", "not worse", "clean")
	var survives []string
	var safe []string
	for _, k := range cutoffs {
		fmt.Printf("  %-12s %10s %10s %10s\n", cutoffLabel(k),
			fmt.Sprintf("%d/%d", wins[k], scored),
			fmt.Sprintf("%d/%d", notWorse[k], scored),
			fmt.Sprintf("%d/%d", clean[k], scored))
		if wins[k] == scored && clean[k] == scored {
			survives = append(survives, cutoffLabel(k))
		}
		if notWorse[k] == scored {
			safe = append(safe, cutoffLabel(k))
		}
	}
	if len(survives) > 0 {
		fmt.Printf("  clears the full gate on every fold: %s\n", strings.Join(survives, ", "))
	} else {
		fmt.Println("  no cutoff clears the full gate on every fold. the per-position half of it is")
		fmt.Println("  a single-fold result, and the constant is not identified by this data.")
	}
	if len(safe) > 0 {
		fmt.Printf("  never worse than unwarped on any fold: %s\n", strings.Join(safe, ", "))
		fmt.Printf("  that band, not one number in it, is what the evidence supports. %d is inside\n", retractedCutoff)
		fmt.Println("  it, which is why the default stays where it is rather than moving to whichever")
		fmt.Println("  value wins on whichever fold is in front of you.")
		// The band only argues for a CAP while it excludes the uncapped warp. Once
		// uncapped is inside it, "5 is in the band" has stopped being a reason to
		// prefer 5 over no cap at all, and the honest remaining reason is
		// structural rather than measured. Read off the band rather than typed,
		// so the paragraph cannot outlive the condition it describes.
		if !betterEverywhere(wins, scored) && contains(safe, cutoffLabel(0)) {
			fmt.Println("  note what the band does not do: it no longer excludes the uncapped warp,")
			fmt.Println("  which every fold here prefers outright. the cap's whole case came from a")
			fmt.Println("  fold with no causal curve. what keeps it is the structural argument in")
			fmt.Println("  adp.roomwarptopk — a ranked list runs deeper than any finite draft — plus")
			fmt.Println("  never-worse, which is a weaker claim than the one the docs used to make.")
		}
	} else {
		fmt.Println("  no cutoff is even never-worse on every fold — the warp does not belong in")
		fmt.Println("  survival at any depth, and `-room=false` should be the default.")
	}
}

// betterEverywhere reports whether the shipped cutoff beat the unwarped model on
// every scored fold. When it did not, the "never worse" band is doing all the
// work and the paragraph above says so instead of leaving the reader to notice.
func betterEverywhere(wins map[int]int, scored int) bool {
	return wins[retractedCutoff] == scored
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func cutoffLabel(k int) string {
	if k <= 0 {
		return "uncapped"
	}
	return fmt.Sprintf("k <= %d", k)
}

// mustTilted is `tilted` with the per-vantage summary dropped, for the many rows
// that only want the scoring function.
func mustTilted(preds []pred, m func(pred) float64) func(pred) float64 {
	f, _ := tilted(preds, m)
	return f
}

// purityReport asks whether the room curve should be built from THIS user's own
// room or from every casual home league the cache can reach.
//
// The three cached drafts are three different leagues (see calibrateDrafts), and
// the room warp pools all of them. That is only correct if a stranger league's
// pick order predicts this room's — which is a testable claim, not a design
// decision, and this section tests it by rebuilding the curve from each source
// pool in turn and scoring the same fold with each.
//
// THE CONTROLLED COMPARISON IS THE TWO SINGLE-DRAFT ROWS, and the pooled row is
// not a third point on the same line. The blend weight is w = n/(n+2), so a
// two-draft pool warps at 0.50 where a one-draft pool warps at 0.33: pooling
// moves purity and weight at once, and a pooled win could be either. Two
// single-draft rows share a weight exactly, so the only thing that differs
// between them is whose room it was. Read that pair.
//
// Every row draws from the fold's OWN allowed pool — splitPool's, so the scored
// draft and anything that started after it are already gone. A purity split that
// resampled the whole cache would put the look-ahead back one level down, where
// nobody would look for it.
func purityReport(folds []*fold, drafts map[string]adp.RoomDraft, byID map[string]*fold, managers map[string]map[string]bool) {
	if len(drafts) < 2 {
		return // one draft in the pool: every fold's curve is empty or the whole pool
	}

	printed := 0
	var thin []string
	for _, f := range folds {
		// By fold label where there is one, so the rows read 2024 before 2025-a
		// rather than in sleeper-id order, which is chronologically backwards.
		others := append([]string(nil), f.pool()...)
		sort.Slice(others, func(i, j int) bool {
			return poolKey(others[i], byID) < poolKey(others[j], byID)
		})
		if len(others) < 2 {
			// Not silence: a fold with one prior draft has no purity question, and
			// a reader counting rows would otherwise take a one-fold table for a
			// three-fold one.
			thin = append(thin, fmt.Sprintf("%s (%d prior)", f.label, len(others)))
			continue // nothing to split, so nothing to compare
		}
		if printed == 0 {
			fmt.Println("\npurity — which rooms build the curve, scored per fold")
			fmt.Printf("  %-8s %-32s %8s %19s %19s\n",
				"fold", "curve built from", "overlap", "top-5 warp", "full-depth warp")
		}
		printed++

		// The no-warp row first, so the two warps below are read against the
		// price the board carried before 3a rather than against each other only.
		// modelShrunkSigma, not a warped model with an empty curve: it is the
		// pre-warp default itself, and it is what the 3a gate compares against.
		base := scoreOf(f.preds, mustTilted(f.preds, modelShrunkSigma))
		fmt.Printf("  %-8s %-32s %8s %19s %19s\n", f.label, "no warp (market adp)", "-",
			one(base.brier), one(base.logLoss))

		pools := make([][]string, 0, len(others)+1)
		for _, id := range others {
			pools = append(pools, []string{id})
		}
		pools = append(pools, others)
		for _, pool := range pools {
			top, full := scoreCurve(f, pool, drafts)
			fmt.Printf("  %-8s %-32s %8s %19s %19s\n", f.label,
				poolLabel(pool, byID), poolOverlap(pool, f, byID, managers),
				pair(top.brier, full.brier), pair(top.logLoss, full.logLoss))
		}
	}
	if printed == 0 {
		return
	}
	fmt.Println("  each warp cell reads top-5 -> full-depth, over the same predictions. the")
	fmt.Println("  overlap column is managers shared between the source room and the scored one.")
	fmt.Println("  the two single-draft rows are the purity test: same blend weight (w 0.33),")
	fmt.Println("  different room. the pooled row moves purity and weight together (w 0.50), so a")
	fmt.Println("  win there is not evidence about purity by itself.")
	if len(thin) > 0 {
		fmt.Printf("  no rows for %s. a fold can only be split over the\n", strings.Join(thin, ", "))
		fmt.Println("  drafts that preceded it, and those have fewer than two — so this table is")
		fmt.Println("  thinner than the fold count, and thinner than it was when folds were allowed")
		fmt.Println("  to build curves out of their own future.")
	}
}

// scoreCurve re-walks a fold against a curve built from one source pool. A walk
// rather than a rescore, because the warped price is computed inside it and rides
// on every prediction — swapping the curve changes the predictions themselves,
// not the way they are graded.
func scoreCurve(f *fold, pool []string, drafts map[string]adp.RoomDraft) (top, full score) {
	sub := map[string]adp.RoomDraft{}
	for _, id := range pool {
		sub[id] = drafts[id]
	}
	preds, _ := walk(f.draft, f.feed, f.board, adp.RoomCurveOf(sub), nil, false)
	for i := range preds {
		preds[i].idx = i // the tilt hands answers back through this index
	}
	topFn, _ := tilted(preds, modelRoomTopShrunk)
	fullFn, _ := tilted(preds, modelRoomShrunk)
	return scoreOf(preds, topFn), scoreOf(preds, fullFn)
}

// one formats a single metric in the same width the paired cells use, so the
// no-warp row lines up under the warped ones instead of drifting left.
func one(v float64) string { return fmt.Sprintf("%.4f", v) }

// poolKey is a draft's fold label, or its id when it never became a fold —
// which happens to any draft that builds the curve but has no era board to be
// scored against.
func poolKey(id string, byID map[string]*fold) string {
	if f, ok := byID[id]; ok {
		return f.label
	}
	return id
}

// poolNames joins a set of draft ids into their fold labels. Sorted by label,
// not by id: sleeper ids ascend with creation time within a season but not
// across them, so id order prints 2025-b before 2025-a.
func poolNames(pool []string, byID map[string]*fold) string {
	if len(pool) == 0 {
		return "nothing"
	}
	names := make([]string, 0, len(pool))
	for _, id := range pool {
		names = append(names, poolKey(id, byID))
	}
	sort.Strings(names)
	return strings.Join(names, " + ")
}

func poolLabel(pool []string, byID map[string]*fold) string {
	label := poolNames(pool, byID)
	if len(pool) == 1 {
		return label + " only"
	}
	return label + " (the gate's pool)"
}

// poolOverlap is the manager overlap between the source room and the scored one,
// and it is only meaningful for a single-draft pool — two rooms pooled have two
// overlaps and averaging them would invent a number.
func poolOverlap(pool []string, f *fold, byID map[string]*fold, managers map[string]map[string]bool) string {
	if len(pool) != 1 {
		return "mixed"
	}
	src, ok := byID[pool[0]]
	if !ok {
		return "-"
	}
	scored := managers[f.label]
	n, ok := sharedManagers(managers[src.label], scored)
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%d/%d", n, len(scored))
}

// shiftByPos is the mean (room - market) price move per position, in picks, over
// the graded predictions. Negative means the warp priced that position earlier.
func shiftByPos(preds []pred) []string {
	sum := map[string]float64{}
	n := map[string]int{}
	for _, p := range preds {
		sum[p.pos] += p.adpRoom - p.adp
		n[p.pos]++
	}
	var out []string
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		if n[pos] == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s %+.1f", strings.ToLower(pos), sum[pos]/float64(n[pos])))
	}
	return out
}

// posGate is the per-position half of 3a's gate, evaluated rather than claimed.
//
// The overall numbers cannot answer the question the section above asks. A warp
// that helps on average while wrecking one position has still moved the number
// the board sorts by for every player at that position, so the ship condition is
// the conjunction: better overall on both metrics AND no position regressing.
// Only the first half used to be computed.
//
// The verdict prints in three buckets because the honest answer has three, and
// collapsing them was the original error. `both` is the clean win. `flat` is a
// metric that moved the wrong way by less than the tables can show — reported
// with its true delta so the reader can see how small "flat" is rather than take
// the word for it. `worse` is the tripwire; one entry drops the ship verdict.
type posGate struct {
	both  []string // better on brier and log-loss, at the printed precision
	flat  []string // better on one, tied on the other, with the real delta named
	worse []string // regressed on a metric the reader can see — blocks the ship
}

func (g posGate) passes() bool { return len(g.worse) == 0 }

// line is the one-line summary that sits under the overall numbers.
func (g posGate) line() string {
	var parts []string
	if len(g.both) > 0 {
		parts = append(parts, strings.Join(g.both, "/")+" better on both")
	}
	parts = append(parts, g.flat...)
	parts = append(parts, g.worse...)
	if len(parts) == 0 {
		return "no position had enough rows to score"
	}
	return strings.Join(parts, " · ")
}

// roomPosGate scores the shipped warp against the pre-warp model one position at
// a time, over the same segments and in the same order the tables above print,
// so the summary and the table can never tell different stories.
func roomPosGate(preds []pred, base, top namedModel) posGate {
	var g posGate
	for _, sg := range positionSegs() {
		var sub []pred
		for _, p := range preds {
			if sg.keep(p) {
				sub = append(sub, p)
			}
		}
		if len(sub) == 0 {
			continue // a position nobody had an adp for isn't a pass, it's absent
		}
		sa, sb := scoreOf(sub, base.f), scoreOf(sub, top.f)
		clean := true
		for _, m := range []struct {
			name     string
			from, to float64
		}{
			{"brier", sa.brier, sb.brier},
			{"log-loss", sa.logLoss, sb.logLoss},
		} {
			switch {
			case betterPrinted(m.to, m.from):
				// An improvement the reader can see in the table above.
			case betterPrinted(m.from, m.to):
				clean = false
				g.worse = append(g.worse,
					fmt.Sprintf("%s %s worse %+.4f", sg.label, m.name, m.to-m.from))
			default:
				// Tied to four decimals. The real delta is named anyway, because
				// "flat" quietly hiding a large move would be the same asserted
				// claim in a smaller font.
				clean = false
				g.flat = append(g.flat,
					fmt.Sprintf("%s %s flat %+f", sg.label, m.name, m.to-m.from))
			}
		}
		if clean {
			g.both = append(g.both, sg.label)
		}
	}
	return g
}

// verdictBoth labels a change that moves two metrics at once. Read off one of
// them it printed "better" next to a row whose other column, a few characters
// along the same line, had got worse.
func verdictBoth(baseA, otherA, baseB, otherB float64) string {
	a, b := verdict(baseA, otherA), verdict(baseB, otherB)
	if a == b {
		return a
	}
	return "mixed"
}

// verdict labels a metric where lower is better, for the aligned tag column.
func verdict(base, other float64) string {
	switch {
	case other < base:
		return "better"
	case other > base:
		return "worse"
	default:
		return "tied"
	}
}
