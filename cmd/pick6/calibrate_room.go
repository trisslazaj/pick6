package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
)

// Phase 3a's gate. The room warp shifts every player's price toward where this
// league's own drafts took the k-th man at his position; this is the part that
// decides whether the survival model gets to believe it.
//
// The cross-validation is not optional. A warp built from the 2024 draft and then
// scored on the 2024 draft would report a skill it does not have, so the curve
// each vantage sees is built from the OTHER drafts only (adp.RoomCurveOf with the
// scored id excluded). With one scorable season that resolves to the two 2025
// drafts, exactly as the spec asks.

// modelRoom is the plain conditional logistic priced against the room-warped adp,
// recorded during the walk through engine.Player.ADPEff — so this grades the
// shipped reader rather than a second copy of the blend.
func modelRoom(p pred) float64 { return p.qRoom }

// modelRoomShrunk is the warp on top of what actually ships: the shrunk sigma.
// This is the only fair comparison for the gate — swapping the price AND the
// sigma at once would leave two changes tangled in one brier delta.
func modelRoomShrunk(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoom, p.sigmaShrunk)
}

// modelRoomTopShrunk is the warp restricted to the top adp.RoomWarpTopK players at
// each position. It is here because it wins and is still not shipped: see
// adp.RoomWarpTopK for the sweep, the structural reason the tail is wrong, and why
// a -0.0009 brier win on a single fold does not move the default.
func modelRoomTopShrunk(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoomTop, p.sigmaShrunk)
}

// noteRoomSources prints what the curve was built from, per draft. Every draft
// counts here, including the two the survival backtest has to skip: the curve
// reads pick order and position only, so no historical adp is involved and the
// era mismatch that invalidates those two for scoring does not apply.
func noteRoomSources(sources []adp.RoomSource) {
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
		"%d drafts, %d picks · seasons %s · pick order only, so no era caveat",
		len(ok), picks, strings.Join(ok, ", ")))
}

// roomWarpTag / roomWarpDetail describe the leave-one-out curve one scored draft
// actually got. Printing the excluded id is the point: it is the difference
// between a backtest and a memorization check, and it should be visible in the
// output rather than trusted to a comment.
func roomWarpTag(c adp.RoomCurve) string {
	if c.Empty() {
		return "off"
	}
	return "held out"
}

func roomWarpDetail(c adp.RoomCurve, scored string) string {
	if c.Empty() {
		return "no other draft to build a curve from · 3a rows will tie the raw model"
	}
	var parts []string
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		if d := c.Depth(pos); d > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ToLower(pos), d))
		}
	}
	// A ceiling, not the weight anyone was warped by: Warp weighs each k by the
	// drafts that actually reached it, so only players inside every draft's depth
	// are priced at this number and the rest are pulled less. Printed bare it read
	// as one weight applied to the whole board, which is true of no player on it.
	return fmt.Sprintf("%d drafts, %s excluded · depth %s · w up to %.2f, thinner past each depth",
		c.Drafts, scored, strings.Join(parts, "/"),
		float64(c.Drafts)/(float64(c.Drafts)+adp.RoomWarpPseudo))
}

// roomGate is 3a's verdict, printed with the evidence that produced it.
//
// The comparison is the shipped model against the shipped model with the room's
// prices, overall and per position — per position because the warp's whole claim
// is positional.
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
// on average — +11.6 picks — even though it moves qb1 through qb5 earlier by 1.7
// to 3.7. The mean is dominated by deep quarterbacks, where mapping rank to room
// pick is structurally wrong (see adp.RoomWarpTopK), and there are far more of
// them on the board than there are early ones. Pushing them later happens to
// point the same way as the measured error, so qb BRIER improves (0.0970 ->
// 0.0917) while qb log-loss gets worse (0.2966 -> 0.3184): the tail is being
// helped for the wrong reason and the top is being hurt for the right one. That
// is not a signal, it is two errors partially cancelling, and it is exactly why
// the gate reads both metrics and every position instead of one average.
//
// The top-of-position variant settles it. Warping only the first five at each
// position — where the room's early-qb behaviour actually lives — improves qb on
// BOTH metrics (brier 0.0970 -> 0.0952, log-loss 0.2966 -> 0.2921) and improves
// every other position too. So the two facts were never in conflict: this room
// takes the first few quarterbacks early, AND the twenty-third quarterback on a
// national board is not somebody this room has an opinion about at all.
func roomGate(preds []pred, shipped, warped, top namedModel) {
	base, room, tk := scoreOf(preds, shipped.f), scoreOf(preds, warped.f), scoreOf(preds, top.f)

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

	fmt.Printf("\n3a gate — room-warped adp, cross-validated (curve built without the scored draft)\n")
	if covered == 0 {
		fmt.Println("  the warp moved nothing: no curve, or it reached no player on the era board.")
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
	note(fmt.Sprintf("top %d only", adp.RoomWarpTopK), verdict(base.brier, tk.brier), fmt.Sprintf(
		"brier %.4f -> %.4f (%+.4f) · log-loss %.4f -> %.4f (%+.4f) · graded, not shipped",
		base.brier, tk.brier, tk.brier-base.brier,
		base.logLoss, tk.logLoss, tk.logLoss-base.logLoss))

	segmentTable("3a by position", preds, positionSegs(), shipped, warped)
	// And by depth, because the warp's error turns out to BE a depth story: the
	// room's curve only reaches as deep into a position as a finite draft goes, so
	// past that point mapping rank to room pick prices players later than the
	// market does. Splitting by depth is what separates "the room-is-qb-early
	// signal is wrong" from "the signal is fine and the tail is dragging it down".
	segmentTable("3a by adp depth", preds, depthSegs(preds), shipped, warped)
	// The same positional split for the variant that passes, so the claim "the
	// top of the position is the good part" is a table rather than an assertion.
	segmentTable("3a by position, top-of-position warp only", preds, positionSegs(), shipped, top)
	fmt.Println("  the gate is brier and log-loss, both directions, on every segment. a warp")
	fmt.Println("  that helps overall while wrecking one position has still moved the number")
	fmt.Println("  the board sorts by, so it does not ship into survival on an average.")
	if room.brier > base.brier {
		fmt.Println("  verdict: worse. the warp is not wired into survival — the default board")
		fmt.Println("  prices raw adp. `pick6 fetch` prints the room curve as a read for the")
		fmt.Println("  human, and `-room` on mock/live turns the pricing on for anyone who")
		fmt.Println("  wants to argue with this table.")
		fmt.Printf("  the top-%d row above is the interesting part: the same warp restricted to\n",
			adp.RoomWarpTopK)
		fmt.Println("  the top of each position passes on both metrics. the room's curve is right")
		fmt.Println("  where the room's appetite is, and structurally later than adp past it — a")
		fmt.Println("  ranked list is longer than any finite draft. still not shipped on one fold.")
	} else {
		fmt.Println("  verdict: not worse. the warp is still opt-in behind -room: three drafts of")
		fmt.Println("  one room against one scorable season is a small sample winning by a hair,")
		fmt.Println("  and wiring it in by default needs more seasons than ffc's archive has.")
	}
	fmt.Println("  caveat: two drafts build the curve and one scores it, so this is a single")
	fmt.Println("  fold. it is the only fold this data can cut — 2024 is the only season with")
	fmt.Println("  era adp — and a single fold cannot tell a bad idea from an unlucky one.")
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
