package adp

import (
	"sort"

	"github.com/trisslazaj/pick6/internal/rankings"
)

// OverlayADP replaces the price on the board with one from another feed, keeping
// everything else the primary source gave us.
//
// WHY THIS EXISTS, measured. FFC is the only feed here that publishes a spread,
// so it has to stay the primary: stdev is what per-player sigma, the shrinkage
// prior and the whole survival curve's width are built from. But FFC's ADP comes
// from FFC's own mock drafter, and this league drafts on Sleeper. Scored against
// the two real 2025 drafts, Sleeper's own adp column beat the cross-platform
// consensus by margins an order of magnitude larger than anything the room warp
// ever moved — log-loss 0.0698 -> 0.0495 on one and 0.0787 -> 0.0617 on the
// other, over an identical prediction set both times. That is a data change, not
// a model change, and it is the largest single improvement measured in this
// project.
//
// So: ORDER from the market that actually prices this room, UNITS and spread
// from the only market that reports them. A player the overlay cannot reach
// keeps his primary price rather than being dropped — a thinner board would cost
// more than a slightly stale price on one player.
//
// WHY ONLY THE ORDER, and this is the part that is easy to get wrong. The two
// feeds are not the same quantity. FFC's adp is the mean pick in a real 12-team
// draft, so it is bounded by draft length: measured on the 2026 board, median
// 98.5 and max 194.7 over 204 players. The FantasyPros column is a RANK over 337
// names and is bounded by nothing: median 123, max 245. Overlaying it raw moved
// every price by a mean of 20.6 picks, almost all of which was that scale gap
// rather than any disagreement about players — and it would have pushed the deep
// board past the end of a draft that can only make 192 picks.
//
// What the 2025 backtest actually measured was Sleeper's column against
// FantasyPros' own consensus column, both in the same rank units. So the signal
// is ORDERING, and nothing else. This maps that ordering onto the primary's own
// distribution by rank: the player the overlay ranks k-th cheapest gets the adp
// the primary assigns to its k-th cheapest. Monotone by construction, exactly
// preserves the thing that was measured to help, and lands in true pick units,
// which is what the survival curve requires. Do not "improve" this by using the
// raw numbers — that is the same class of error that got MFL's feed rejected.
//
// The caller must re-run ShrinkSigma afterwards. Sigma is derived from the raw
// Stdev against an adp-fitted prior, and this moves the adp out from under that
// fit; Stdev itself is untouched, so the re-run is a clean recompute rather than
// a second shrink of an already-shrunk number.
func OverlayADP(players map[string]*Player, board FFCResult, ix *rankings.Index) OverlayResult {
	var r OverlayResult

	priced := make(map[string]float64, len(board.Entries))
	for _, e := range board.Entries {
		id, ok := ix.LookupExact(e.Name, e.Pos, e.Team)
		if !ok {
			r.Unmatched = append(r.Unmatched, e.Name+" ("+e.Pos+"/"+e.Team+")")
			continue
		}
		// First writer wins, matching the rest of the package. The export is
		// already one row per player, so a second hit would be a duplicate.
		if _, seen := priced[id]; !seen {
			priced[id] = e.ADP
		}
	}

	// Sorted so two runs over the same inputs report the same numbers, the same
	// discipline ShrinkSigma and RoomCurveOf state.
	ids := make([]string, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// The rank map is built over exactly the players the overlay reaches, so the
	// two distributions being matched cover the same set. Anyone it misses keeps
	// his primary price and is left out of both sides.
	var reached []string
	for _, id := range ids {
		if _, ok := priced[id]; ok {
			reached = append(reached, id)
		} else {
			r.Kept++
		}
	}
	units := make([]float64, 0, len(reached))
	for _, id := range reached {
		units = append(units, players[id].ADP)
	}
	sort.Float64s(units) // the primary's own pick-unit distribution, cheapest first

	order := append([]string(nil), reached...)
	sort.Slice(order, func(i, j int) bool {
		if a, b := priced[order[i]], priced[order[j]]; a != b {
			return a < b
		}
		return order[i] < order[j] // ties are impossible in one export, but stay deterministic
	})

	for k, id := range order {
		p := players[id]
		if p.Stdev <= 0 {
			r.NoSpread++
		}
		mapped := units[k]
		r.Moved += absf(mapped - p.ADP)
		p.ADP = mapped
		r.Repriced++
	}
	if r.Repriced > 0 {
		r.MeanMove = r.Moved / float64(r.Repriced)
	}
	sort.Strings(r.Unmatched)
	return r
}

// OverlayResult reports what the overlay did, so fetch can say it out loud
// rather than silently changing every price on the board.
type OverlayResult struct {
	Repriced  int     // board players whose adp came from the overlay
	Kept      int     // board players the overlay could not reach, still on the primary price
	NoSpread  int     // repriced players with no stdev, so sigma falls back to the default
	MeanMove  float64 // mean |new - old| over the repriced, in picks
	Moved     float64
	Unmatched []string // overlay rows that resolved to nobody on the board
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
