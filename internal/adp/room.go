package adp

import (
	"sort"

	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The room's own price curve, built from this user's completed drafts.
//
// National ADP is the average of thousands of strangers. A fixed set of twelve
// leaguemates is not the average of anything — measured over three of their
// completed drafts, this room takes its first quarterback at pick 23.0 and its
// first tight end at 23.3, against 26.1 and 39.5 on the 2026 half-ppr board. Over
// the first five at each position the room runs 17.4 picks early on quarterbacks
// and 13.4 early on tight ends, while running backs (+0.4) and receivers (-0.8)
// track the market to within a pick. CLAUDE.md's per-round table says the same
// thing less precisely; `pick6 fetch` prints this one.
//
// The curve reads ONLY pick order and position. No historical national adp is
// involved, so all 552 picks are usable and the era caveat that makes the two
// 2025 drafts unscorable for `pick6 calibrate` does not apply here: "the fourth
// quarterback went at pick 35" is as true in 2024 as in 2026, because it is a
// statement about the room's behaviour and not about any player.
//
//	adp_room(P, k) = mean over drafts of the overall pick at which the k-th
//	                 player of position P was taken, monotonized over k.
//
// Location shift only. Sigma is never touched: the room disagreeing about a
// player's price is not the same claim as the room being more predictable, and
// only one of those two is measurable from three drafts.

// RoomWarpPseudo is the prior weight, in drafts, behind the curve. The blend is
// w = n/(n + RoomWarpPseudo), so three drafts buy 60% of the warp, one buys 33%,
// and a k no draft ever reached buys none — the "w -> 0 outside the observed k
// range" rule falls out of the same formula instead of being a special case.
//
// Two is deliberately small-sample-shy: three drafts of one room is thin
// evidence, and the whole point of a pseudo-count is that the answer degrades
// toward the national market rather than toward whatever three nights produced.
//
// It lives here rather than in engine/tuning.go for the same reason SigmaDefault
// is duplicated in this package: the curve is built at fetch/load time and this
// package deliberately does not import the engine. The engine only ever reads
// the finished number off Player.ADPEff.
const RoomWarpPseudo = 2.0

// RoomWarpTopK is where the warp stops being right, MEASURED, and it is not
// shipped — `pick6 calibrate` grades it as an extra row and nothing prices it.
//
// The full-depth warp fails its gate (brier 0.0670 -> 0.0671, log-loss 0.2250 ->
// 0.2326 on the cross-validated 2024 backtest). Restricted to the first five
// players at each position it PASSES, and comfortably: brier 0.0660, log-loss
// 0.2222. Sweeping the cutoff on the same backtest, the flip is sharp and in one
// place — k<=3 brier 0.0662, k<=5 0.0660, k<=8 0.0662, k<=12 0.0670 (worse),
// uncapped 0.0671 (worse). The cutoff rows are the same to four decimals whether
// or not the deep-k corrections below are in, which is the point: every one of
// them lands past k=5.
//
// Restricted that way it improves every position on both metrics, quarterbacks
// included (brier 0.0970 -> 0.0952, log-loss 0.2966 -> 0.2921) — which is the
// answer to the apparent contradiction in CLAUDE.md, where the room takes qb
// slots early while named quarterbacks survive longer than adp implies. Both are
// true at different depths of the position.
//
// The reason is structural, not a tuning accident. National adp ranks far more
// players at a position than any finite draft takes: the 2024 board priced its
// 12th quarterback at adp 89.6, while this room's 12th quarterback went at pick
// 126.5, because 12 teams over 15 rounds only ever take ~19 of them. So past the
// top of a position, rank->room-pick prices players LATER than the market and the
// warp's mean shift flips sign — qb +11.5 picks overall against -1.7 to -3.7 for
// qb1 through qb5. The room-is-qb-early signal is real and lives entirely at the
// top; the tail is an artifact of comparing a ranked list to a finite draft.
//
// Left unshipped on purpose: one fold (2024 is the only season with era adp),
// two drafts building the curve, and a -0.0009 brier win is not a mandate. This
// is the note that stops the next person re-deriving it — and if a second
// scorable season ever lands in ffc's archive, this is the variant to gate again.
const RoomWarpTopK = 5

// RoomDraft is one completed draft reduced to what the curve reads: the position
// taken at each pick, in pick order. Index 0 is pick 1; "" is a pick whose
// metadata carried no position.
type RoomDraft []string

// RoomCurve is adp_room: for each position, where the k-th player at it comes
// off the board in this league.
type RoomCurve struct {
	Drafts int // how many drafts went in, for the printout

	pick map[string][]float64 // pick[pos][k-1], monotonized
	seen map[string][]int     // seen[pos][k-1]: drafts that reached that k
}

// BuildRoomCurve averages the drafts into one curve.
//
// The running max is not cosmetic. Within a single draft the k-th player at a
// position cannot go before the (k-1)-th, but the MEANS can invert, because a
// deep k is supported by fewer drafts than a shallow one: if two of three drafts
// stopped at 19 quarterbacks, adp_room(QB, 20) is one draft's number and can
// easily sit earlier than the three-draft average before it. An inverted price
// curve would tell the board the 20th quarterback goes sooner than the 19th.
func BuildRoomCurve(drafts []RoomDraft) RoomCurve {
	sum := map[string][]float64{}
	seen := map[string][]int{}
	for _, d := range drafts {
		count := map[string]int{}
		for i, pos := range d {
			if pos == "" {
				continue // a pick whose metadata carried no position
			}
			count[pos]++
			k := count[pos]
			for len(sum[pos]) < k {
				sum[pos] = append(sum[pos], 0)
				seen[pos] = append(seen[pos], 0)
			}
			sum[pos][k-1] += float64(i + 1) // index 0 is pick 1
			seen[pos][k-1]++
		}
	}

	c := RoomCurve{Drafts: len(drafts), pick: map[string][]float64{}, seen: seen}
	for pos, s := range sum {
		means := make([]float64, len(s))
		run := 0.0
		for k := range s {
			m := s[k] / float64(seen[pos][k])
			if m < run {
				m = run
			}
			run = m
			means[k] = m
		}
		c.pick[pos] = means
	}
	return c
}

// Empty reports a curve with nothing in it — no draft loaded, or none of them
// carried positions. Callers fall back to raw adp.
func (c RoomCurve) Empty() bool { return len(c.pick) == 0 }

// Depth is the deepest k the room ever reached at a position. Past it the curve
// has no opinion and the warp weight is zero.
func (c RoomCurve) Depth(pos string) int { return len(c.pick[pos]) }

// At is adp_room(pos, k) plus the number of drafts that reached that k. ok is
// false past Depth.
func (c RoomCurve) At(pos string, k int) (pick float64, drafts int, ok bool) {
	ks := c.pick[pos]
	if k < 1 || k > len(ks) {
		return 0, 0, false
	}
	return ks[k-1], c.seen[pos][k-1], true
}

// Warp blends the room's price for the k-th player at a position with the price
// the national market charges him:
//
//	adp_eff = w*adp_room(P, k) + (1-w)*adp,   w = n/(n + RoomWarpPseudo)
//
// n is the drafts that reached this k, so the weight thins out exactly where the
// evidence does. ok is false when the room never got that deep or the player has
// no adp to blend — both mean "use the raw number", and neither is an error.
func (c RoomCurve) Warp(pos string, k int, adp float64) (float64, bool) {
	room, n, ok := c.At(pos, k)
	if !ok || n <= 0 || adp <= 0 {
		return adp, false
	}
	w := float64(n) / (float64(n) + RoomWarpPseudo)
	return w*room + (1-w)*adp, true
}

// RoomRow is the minimum a board row needs to be warped: an id to key the answer
// on, a position, and the market price that fixes his rank within it.
type RoomRow struct {
	ID  string
	Pos string
	ADP float64
}

// EffectiveADP warps a whole board at once, keyed by id, and includes only the
// rows the curve actually covers — a caller can then leave everyone else on raw
// adp without deciding what "uncovered" means.
//
// Rank is by adp WITHIN the position, over the board rows handed in, which is the
// only rank that lines up with adp_room's own definition: adp_room(WR, 5) is where
// the fifth receiver went, so it must be compared against the fifth receiver on
// the board. Rows with no adp are skipped rather than ranked last — an unranked
// player has no market price, and giving him one would be inventing data.
//
// The warped prices come out non-decreasing in k, which the blend does not give
// for free — see the running max below.
func (c RoomCurve) EffectiveADP(rows []RoomRow) map[string]float64 {
	return c.effectiveADP(rows, 0)
}

// EffectiveADPTopK is that warp restricted to the first maxK players at each
// position (0 means no limit). Only `pick6 calibrate` calls it, to grade the
// variant RoomWarpTopK documents; nothing on the board prices it.
func (c RoomCurve) EffectiveADPTopK(rows []RoomRow, maxK int) map[string]float64 {
	return c.effectiveADP(rows, maxK)
}

func (c RoomCurve) effectiveADP(rows []RoomRow, maxK int) map[string]float64 {
	byPos := map[string][]RoomRow{}
	for _, r := range rows {
		if r.Pos == "" || r.ADP <= 0 {
			continue
		}
		byPos[r.Pos] = append(byPos[r.Pos], r)
	}
	out := map[string]float64{}
	for pos, group := range byPos {
		sort.Slice(group, func(i, j int) bool {
			if group[i].ADP != group[j].ADP {
				return group[i].ADP < group[j].ADP
			}
			return group[i].ID < group[j].ID // map order is not an order
		})
		// A second running max, over the BLEND this time, and it is not redundant
		// with the one BuildRoomCurve carries. Both inputs are non-decreasing in k
		// — adp_room by that first running max, adp by the sort above — but the
		// weight between them is not constant: Warp weighs by the drafts that
		// reached this k, and that count falls off with depth, so a thinly
		// supported k stays nearer its market price than the well-supported k in
		// front of it. Measured on the shipped 2026 board that inverted four rows:
		// the 13th defense (one draft deep, w 1/3) priced 5.2 picks EARLIER than
		// the 12th (three drafts deep, w 0.6). That is the same "the 20th
		// quarterback goes sooner than the 19th" the curve's own running max
		// exists to prevent, arriving one step later in the pipeline.
		run := 0.0
		for i, r := range group {
			if maxK > 0 && i+1 > maxK {
				break // the group is adp-sorted, so everyone after him is deeper too
			}
			eff, ok := c.Warp(pos, i+1, r.ADP)
			if !ok {
				continue
			}
			if eff < run {
				eff = run
			}
			run = eff
			out[r.ID] = eff
		}
	}
	return out
}

// RoomSource is one draft the curve was built from, or the reason it wasn't.
type RoomSource struct {
	ID     string
	Season string
	Picks  int
	Err    error
}

// LoadRoomDrafts reads completed drafts through the sleeper disk cache, pulling
// any that are missing or a month stale, and reduces each to positions in pick
// order. This is the fetch-time path: one network round per draft ever, since a
// finished draft's picks never change again.
//
// A draft that will not load is reported, not fatal: the curve simply carries
// fewer drafts, which the pseudo-count already knows how to price.
func LoadRoomDrafts(ids []string) (map[string]RoomDraft, []RoomSource) {
	return loadRoomDrafts(ids, false)
}

// CachedRoomDrafts is the same read with the network taken away, for the paths
// that end in a rendered frame.
//
// `pick6 mock` and `pick6 live` reach this on startup, and a board is not
// allowed to block on http: cache.Get refetches an aged-out file with a 120s
// timeout and only falls back to the stale copy if the request errors, so six
// month-old draft files behind a blackholing proxy is twelve minutes of blank
// screen. Nothing here is load-bearing — no curve means raw adp and a
// lineup-shape replacement level, which is the default board.
func CachedRoomDrafts(ids []string) (map[string]RoomDraft, []RoomSource) {
	return loadRoomDrafts(ids, true)
}

func loadRoomDrafts(ids []string, offline bool) (map[string]RoomDraft, []RoomSource) {
	out := map[string]RoomDraft{}
	var sources []RoomSource
	for _, id := range ids {
		src := RoomSource{ID: id}
		d, err := sleeper.CachedDraft(id)
		if offline {
			d, err = sleeper.DiskDraft(id)
		}
		if err != nil {
			src.Err = err
			sources = append(sources, src)
			continue
		}
		src.Season = d.Season
		picks, err := sleeper.CachedPicks(id)
		if offline {
			picks, err = sleeper.DiskPicks(id)
		}
		if err != nil {
			src.Err = err
			sources = append(sources, src)
			continue
		}
		src.Picks = len(picks)
		out[id] = RoomDraftOf(picks)
		sources = append(sources, src)
	}
	return out, sources
}

// RoomDraftOf reduces a sleeper pick list to positions in pick order.
//
// Indexed by pick_no, not by position in the slice: a missing pick must leave a
// hole rather than shift every later pick one earlier. The curve is made of pick
// NUMBERS, so an off-by-one there moves the whole room's price and nothing would
// look wrong.
func RoomDraftOf(picks []sleeper.DraftPick) RoomDraft {
	high := 0
	for _, p := range picks {
		if p.PickNo > high {
			high = p.PickNo
		}
	}
	d := make(RoomDraft, high)
	for _, p := range picks {
		if p.PickNo < 1 {
			continue
		}
		d[p.PickNo-1] = rankings.NormalizePos(p.Metadata.Position)
	}
	return d
}

// RoomCurveOf builds a curve from loaded drafts, skipping any id in except.
//
// The exclusion is what keeps the backtest honest: a warp built from the draft it
// is being scored on has memorized the answer, and would report a skill it does
// not have. Ids are sorted before summing so the floats add in a fixed order and
// two runs print the same digits.
func RoomCurveOf(drafts map[string]RoomDraft, except ...string) RoomCurve {
	skip := map[string]bool{}
	for _, id := range except {
		skip[id] = true
	}
	ids := make([]string, 0, len(drafts))
	for id := range drafts {
		if !skip[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	list := make([]RoomDraft, 0, len(ids))
	for _, id := range ids {
		list = append(list, drafts[id])
	}
	return BuildRoomCurve(list)
}
