package adp

import (
	"math"
	"sort"

	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The price curve of the rooms this user drafts in, built from completed drafts.
//
// National ADP is the average of thousands of strangers. A dozen leaguemates are
// not the average of anything — measured over three completed drafts, they take
// the first quarterback at pick 23.0 and the first tight end at 23.3, against
// 26.1 and 39.5 on the 2026 half-ppr board. Over the first five at each position
// they run 17.4 picks early on quarterbacks and 13.4 early on tight ends, while
// running backs (+0.4) and receivers (-0.8) track the market to within a pick.
// CLAUDE.md's per-round table says the same thing less precisely; `pick6 fetch`
// prints this one.
//
// THREE DRAFTS, THREE LEAGUES. Measured manager overlap is 9/12 between the 2024
// draft and the 2025 one this user will draft against again, 5/12 and 6/12 for
// the third, and all three carry previous_league_id null — so this is a portrait
// of casual home leagues, not of one room. The three curves agree closely (qb1 at
// picks 21/29/19, te1 at 24/21/25), which is why pooling them is defensible; it
// is also why "this room drafts weird" is not a claim this curve can support.
//
// The curve reads ONLY pick order and position. No historical national adp is
// involved, so all 552 picks build a curve even for a season no era board exists
// for: "the fourth quarterback went at pick 35" is as true in 2024 as in 2026,
// because it is a statement about behaviour and not about any player. That is a
// statement about ERA, and it is not a licence to ignore ORDER — a curve is only
// usable at the clock if every draft in it already happened, which is why
// RoomSource carries Start and calibrate holds each fold's future out.
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

// THERE IS NO LONGER A CUTOFF. The board warps every player it has a curve for.
// `RoomWarpTopK = 5` used to cap it at the top five of each position; it was
// removed once a second and third fold could test it, and this comment is the
// record of why, because the number moved meaning three times.
//
// WHAT THE CAUSAL FOLDS SAY. On both 2025 folds — the only two with a curve
// built entirely from drafts that started before them — uncapped beats k<=5 on
// both metrics at native depth (a: 0.0197/0.0698 -> 0.0175/0.0616; b:
// 0.0209/0.0790 -> 0.0200/0.0738) and at a common board size (`-depth 178`, a:
// 0.0559/0.2103 -> 0.0502/0.1929; b: 0.0579/0.1913 -> 0.0559/0.1815). It also
// regresses FEWER positions than the cap did — 1 and 2 against the cap's 5 and
// 5 — so the cap lost on the per-position half of its own gate, not just the
// aggregate. The sweep is monotone toward no cap on both.
//
// THE STRUCTURAL ARGUMENT, which is the case FOR a cap and which the measurement
// did not vindicate. It is kept because it is still the reason to expect the
// tail to be junk, and because it is what a future fold would have to overturn:
// National adp ranks far more players at a position than any finite draft takes:
// the 2024 board priced its 12th quarterback at adp 89.6, while that room's 12th
// quarterback went at pick 126.5, because 12 teams over 15 rounds only ever take
// ~19 of them. So past the top of a position, rank->room-pick prices players
// LATER than the market by construction, and the warp's mean shift flips sign —
// qb +11.0 picks overall against -1.7 to -3.7 for qb1 through qb5. The
// room-is-qb-early signal is real and lives entirely at the top; the tail is an
// artifact of comparing a ranked list to a finite draft. A cap is the obvious
// response to that, and 5 is a guess at where the top ends.
//
// WHAT WAS ONCE CLAIMED, AND IS NOW RETRACTED TWICE OVER. The cutoff was swept on
// the 2024 fold, where k<=5 wins (brier 0.0670 -> 0.0660, log-loss 0.2250 ->
// 0.2222) while the uncapped warp loses (0.0671 / 0.2327), and no position
// regressed at four decimals. Two things happened to that:
//
//   - It did not replicate. Two more folds arrived (the 2025 drafts, priced off
//     a hand-exported fantasypros board). `k<=5` fails the per-position half of
//     the gate on both, the uncapped warp wins outright on both, and no k clears
//     the full gate on all three. Board depth was the obvious explanation and is
//     not it: `calibrate -depth 178` puts every fold on a common board size and
//     the verdict does not move. Season and vendor stay collinear — 2024 is
//     ffc's sample of 906 real drafts, 2025 a three-platform ranking consensus.
//   - The 2024 result was never causal. Measured start times: that draft ran
//     2024-09-01 and BOTH cached drafts that built its curve ran a year later
//     (2025-09-02, 2025-09-04). Leave-one-out held out the scored draft and
//     nothing else, so the only fold that ever preferred a cap was priced off
//     its own future — a regime the live tool cannot reach at the clock.
//     `calibrate` now holds out each fold's future as well, which leaves 2024
//     with no curve at all and the sweep with two folds, both 2025 and both
//     monotone toward no cap. `calibrate -lookahead` reprints the old regime.
//
// SO WHY REMOVE IT, WHEN THE STRUCTURAL ARGUMENT STILL READS WELL? Because the
// argument predicted a specific measurable thing — that the tail prices players
// worse — and on every fold that can test it causally, it does not. Keeping a
// cap that loses on both metrics AND on the per-position gate, on both folds,
// against an argument about what ought to happen, is preferring the reasoning to
// the measurement. This project's rule is the other way round.
//
// WHAT WOULD PUT IT BACK: a causal fold on which uncapped loses. The obvious
// candidate is an ffc-priced fold that is not 2024 and has a draft before it in
// the cache — ffc's archive still answers "no adp data found" for year=2025, so
// recheck once near draft day. Season and vendor are still collinear here (2024
// is ffc's 906 real drafts, both 2025 folds a three-platform ranking consensus),
// so a fold that breaks that tie is worth more than a fourth of the same kind.
// `EffectiveADPTopK` is retained for the sweep and needs no code change to
// re-cap; `-room=false` remains the way to leave the room out entirely.
//
// RoomGapTopK is display only. `pick6 fetch` prints the room's curve against the
// market, and averages the gap over the first five at each position because
// averaging over ALL k inverts its sign for exactly the structural reason above:
// a ranked list runs deeper than any finite draft, so down there the room is
// "late" about everybody. That makes 5 a reasonable place to read the signal. It
// no longer prices anything.
const RoomGapTopK = 5

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
// `pick6 calibrate` is its only caller and the only one it should ever have: at
// full depth this is the variant the 2024 gate rejected, so it survives as a
// comparison row in the model table and nothing else. The board prices
// EffectiveADPTopK. Note that it is also the variant that WINS on both causal
// folds — keeping the rejected model scored next to the shipped one is exactly
// what made that visible, and RoomWarpTopK carries what is left of the case for
// capping anyway.
//
// Rank is by adp WITHIN the position, over the board rows handed in, which is the
// only rank that lines up with adp_room's own definition: adp_room(WR, 5) is where
// the fifth receiver went, so it must be compared against the fifth receiver on
// the board. Rows with no adp are skipped rather than ranked last — an unranked
// player has no market price, and giving him one would be inventing data.
//
// The priced sequence comes out non-decreasing in k across the WHOLE position,
// warped rows and the raw ones behind them together, which the blend does not
// give for free — see the running max and the ceiling below.
func (c RoomCurve) EffectiveADP(rows []RoomRow) map[string]float64 {
	return c.effectiveADP(rows, 0)
}

// EffectiveADPTopK is that warp restricted to the first maxK players at each
// position (0 means no limit). At maxK = RoomWarpTopK this is the warp the board
// ships with: `loadBoard` calls it on every mock and live startup unless
// `-room=false`, and `pick6 calibrate` grades the same call as the row that wins
// the 3a gate. One function, so the graded variant and the priced one cannot
// drift apart.
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
		// Where the warp stops at this position: the caller's cutoff, or the depth
		// the room's curve reaches, whichever comes first. Everyone past it keeps
		// the raw market price, so this index is also the boundary the ceiling
		// below has to hold across.
		limit := len(group)
		if maxK > 0 && maxK < limit {
			limit = maxK
		}
		if d := c.Depth(pos); d < limit {
			limit = d
		}
		// The ceiling: a warped player may not be priced later than the market
		// prices the next man at his position, because that man is NOT warped and
		// the two halves of the sequence have to join up.
		//
		// Without it the top-k cutoff reintroduces the inversion the running maxes
		// exist to prevent, one step further down the pipeline. Measured on the
		// shipped 2026 board at RoomWarpTopK = 5: the warp priced the 5th defense
		// at 144.16, while the 6th kept his raw adp of 129.90 — so the board read
		// the 6th defense as coming off 14.3 picks BEFORE the 5th, and before the
		// 7th, 8th and 9th too. Only def does it today, because def is the one
		// position this room drafts later than the market at the top (fetch's
		// room-curve table prints gap5 +30.0 for it), but any future curve that
		// prices some other position late gets the same discontinuity for free.
		ceiling := math.Inf(1)
		if limit < len(group) {
			ceiling = group[limit].ADP
		}
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
		for i := 0; i < limit; i++ {
			r := group[i]
			eff, ok := c.Warp(pos, i+1, r.ADP)
			if !ok {
				continue
			}
			if eff < run {
				eff = run
			}
			if eff > ceiling {
				eff = ceiling
			}
			// Capped after the running max, never before: min(runningMax, ceiling)
			// is still non-decreasing, so clamping one row cannot let the next one
			// duck back under it.
			run = eff
			out[r.ID] = eff
		}
	}
	return out
}

// RoomSource is one draft the curve was built from, or the reason it wasn't.
//
// Start is the draft's own start time (ms since epoch, 0 when sleeper carries
// none), and it is here because a curve is only usable at the clock if every
// draft in it already happened. `pick6 calibrate` orders the pool by it and
// refuses to price a fold off its own future; nothing else reads it.
type RoomSource struct {
	ID     string
	Season string
	Picks  int
	Start  int64
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
// `pick6 mock` and `pick6 live` reach this on startup — for survival prices as
// well as replacement level since the warp became the default — and a board is
// not allowed to block on http: cache.Get refetches an aged-out file with a 120s
// timeout and only falls back to the stale copy if the request errors, so six
// month-old draft files behind a blackholing proxy is twelve minutes of blank
// screen. Nothing here is load-bearing: no curve means raw national adp and a
// lineup-shape replacement level, which is a degraded board rather than no
// board, and it draws instantly.
func CachedRoomDrafts(ids []string) (map[string]RoomDraft, []RoomSource) {
	return loadRoomDrafts(ids, true)
}

// loadRoomDrafts is both readers. Which pair of functions to call is chosen
// once, before the loop — never a read followed by an override.
//
// It was an override, and `offline` therefore meant nothing:
// `d, err := sleeper.CachedDraft(id)` ran the network-capable read FIRST and the
// disk read only replaced its result. Caught by pointing a board at an empty
// HOME and finding six draft files in the fresh cache dir afterwards, which only
// a download can put there. On this path that is the twelve-minute blank screen
// DiskDraft's comment describes: cache.Get gives a missing or month-old file a
// 120s client timeout, and falls back to stale bytes only when the request
// ERRORS rather than hangs. TestCachedRoomDraftsNeverFetches pins it.
func loadRoomDrafts(ids []string, offline bool) (map[string]RoomDraft, []RoomSource) {
	out := map[string]RoomDraft{}
	var sources []RoomSource
	draft, picksOf := sleeper.CachedDraft, sleeper.CachedPicks
	if offline {
		draft, picksOf = sleeper.DiskDraft, sleeper.DiskPicks
	}
	for _, id := range ids {
		src := RoomSource{ID: id}
		d, err := draft(id)
		if err != nil {
			src.Err = err
			sources = append(sources, src)
			continue
		}
		src.Season, src.Start = d.Season, d.StartTime
		picks, err := picksOf(id)
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
