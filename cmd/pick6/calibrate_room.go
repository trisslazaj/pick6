package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
)

// Phase 3a's gate, and the evidence behind a default. The room warp shifts a
// player's price toward where this league's own drafts took the k-th man at his
// position; this section is what decided that the survival model believes it for
// the top adp.RoomWarpTopK at each position and nowhere deeper.
//
// The cross-validation is not optional. A warp built from the 2024 draft and then
// scored on the 2024 draft would report a skill it does not have, so the curve
// each vantage sees is built from the OTHER drafts only (adp.RoomCurveOf with the
// scored id excluded). With one scorable season that resolves to the two 2025
// drafts, exactly as the spec asks — and one fold is also the whole caveat.

// modelRoom is the plain conditional logistic priced against the full-depth
// room-warped adp, recorded during the walk through engine.Player.ADPEff — so
// this grades the field the board reads rather than a second copy of the blend.
func modelRoom(p pred) float64 { return p.qRoom }

// modelRoomShrunk is the full-depth warp on top of the shrunk sigma. It is the
// fair comparison for the gate — swapping the price AND the sigma at once would
// leave two changes tangled in one brier delta — and it is the row that loses.
func modelRoomShrunk(p pred) float64 {
	return psurvive(p.from, p.to, p.adpRoom, p.sigmaShrunk)
}

// modelRoomTopShrunk is the warp restricted to the top adp.RoomWarpTopK players
// at each position: what the board ships. See adp.RoomWarpTopK for the cutoff
// sweep, the structural reason the tail is wrong, and how thin one fold is.
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
// pick is structurally wrong (see adp.RoomWarpTopK), and there are far more of
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
	//
	// The tag reads BOTH metrics. Taken off brier alone it printed "better" on a
	// row whose log-loss column, three characters further along the same line, had
	// got worse — which is the exact reading error this whole section exists to
	// prevent.
	note(fmt.Sprintf("top %d only", adp.RoomWarpTopK),
		verdictBoth(base.brier, tk.brier, base.logLoss, tk.logLoss), fmt.Sprintf(
			// "the one the board prices" rather than "this one ships": it is true
			// at whatever adp.RoomWarpTopK is set to, including a value that loses
			// this gate, and the verdict below is the thing entitled to say ships.
			"brier %.4f -> %.4f (%+.4f) · log-loss %.4f -> %.4f (%+.4f) · the one the board prices",
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
		fmt.Println("  verdict, full depth: not worse on this fold — but it lost the last time and")
		fmt.Println("  the top-of-position cutoff still beats it, so the deep warp stays unpriced.")
	}
	if betterPrinted(tk.brier, base.brier) && betterPrinted(tk.logLoss, base.logLoss) && pg.passes() {
		fmt.Printf("  verdict, top %d: better on both metrics overall, and no position regresses at\n",
			adp.RoomWarpTopK)
		fmt.Println("  the precision these tables print, so it ships — mock and live blend the")
		fmt.Println("  room's price into survival for the top of each position by default, and")
		fmt.Println("  everyone deeper keeps raw national adp. `-room=false` puts the whole board")
		fmt.Println("  back on the market's numbers.")
	} else {
		fmt.Printf("  verdict, top %d: it no longer clears the pre-warp model on this fold. that is\n",
			adp.RoomWarpTopK)
		fmt.Println("  the result the default was gated on — check adp.RoomWarpTopK against these")
		fmt.Println("  numbers and consider moving the -room default back to off.")
	}
	fmt.Println("  caveat, and it is the whole caveat: two drafts build the curve and one scores")
	fmt.Println("  it, so this is a single fold. it is the only fold this data can cut — 2024 is")
	fmt.Println("  the only season with era adp — and a single fold cannot tell a bad idea from")
	fmt.Println("  an unlucky one. a shipped default resting on it is worth re-gating the day")
	fmt.Println("  ffc's archive grows a second scorable season.")
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
