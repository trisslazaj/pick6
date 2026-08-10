// Package adp fetches ADP from several sources and merges them onto one scale.
package adp

import (
	"math"
	"sort"

	"github.com/trisslazaj/pick6/internal/rankings"
)

// Tuning. Mirrors internal/engine/tuning.go; kept here so fetch can precompute
// sigma and tiers without importing the engine.
const (
	SigmaDefault   = 6.0
	SigmaFromStdev = 1.8138 // pi/sqrt(3): stdev of a logistic = scale * this
	SigmaMin       = 0.5
	SigmaMax       = 25.0

	// ShrinkPseudoDrafts is how many drafts of prior belief the variance shrink
	// carries. FFC reports times_drafted per player, and it is far smaller than
	// the pool's headline draft count: the 2024 half-ppr pool says 906 drafts,
	// but the median player was taken in 54 of them and the thinnest in 11. A
	// spread measured over 11 drafts is mostly noise; one over 190 is signal.
	// At 25, a median player's own number outvotes the prior 2:1 and an 11-draft
	// player lands most of the way onto it.
	ShrinkPseudoDrafts = 25.0

	// Tier derivation, from value gaps. A tier breaks when value drops by more
	// than TierDropPct relative to the previous player AND the drop clears an
	// absolute floor scaled to the position's best player. The relative test
	// finds cliffs anywhere on the curve; the floor stops the flat tail from
	// shattering into singleton tiers.
	TierDropPct  = 0.10
	TierFloorPct = 0.015

	// TierMaxSize caps how many players a single tier may hold. A rankings file's
	// boundaries are always kept, but a tier bigger than this gets subdivided at
	// its largest internal value drops. Published tier graphics sometimes lump 20+
	// players together, and a tier that large can never trigger a cliff — the
	// board would simply never warn you about that position.
	TierMaxSize = 8

	// TierMinSize is the floor for any tier *we* create. A tier of one is a
	// permanent "cliff — last one", so splitting a block must never manufacture
	// one and derived runts get merged forward. Two is deliberately allowed: a
	// genuine two-man elite tier is real information and shows as amber "tier
	// ending", which is correct. A singleton the rankings file drew itself is left
	// alone — Ja'Marr Chase alone in tier 1 is a statement about the position.
	TierMinSize = 2
)

// Entry is one source's opinion about one player, before matching.
//
// TimesDrafted/High/Low are the support behind ADP: how many drafts the average
// summarises and the extremes it hides. The field was called Sample; it is named
// for FFC's own key now so the three sample-support numbers read as a set.
type Entry struct {
	Name         string
	Pos          string
	Team         string
	ADP          float64
	Stdev        float64 // 0 when the source doesn't report it
	Bye          int
	TimesDrafted int // drafts this average is computed over
	High         int // earliest pick he was taken at in any of them
	Low          int // latest
}

// Player is the merged, matched result: one row per Sleeper player id.
type Player struct {
	SleeperID string
	Name      string
	Pos       string
	Team      string
	Bye       int

	ADP   float64 // from the primary format, in pick units
	Stdev float64 // observed draft-position stdev as the source reported it, 0 if unknown
	// Sigma is the logistic scale the survival model wants. It is NOT
	// Stdev/SigmaFromStdev: ShrinkSigma blends the observed spread toward the
	// pool's prior first, weighted by TimesDrafted, so a five-draft player's
	// suspiciously tight number is pulled back to what players at his price
	// usually do. The gap between the two columns is that shrink.
	Sigma   float64
	Value   int // relative season value; 0 when unknown
	Tier    int // per-position tier; 0 when unknown
	TierSrc TierSource

	// Sentiment is the rankings file's opinion of him — "target", "pass" or
	// "avoid", "" when the file said nothing. Display only, like the injury
	// fields: value, survival and ordering must never read it.
	Sentiment string

	// Sample support behind ADP, from the primary source only. TimesDrafted is
	// live: it is the weight ShrinkSigma gives a player's own stdev against the
	// pool prior. High and Low are carried for the board — High is what
	// engine.SupportFloor would read if it were ever wired in (it isn't; see
	// that function for the measurement), and Low drives the ui's past-worst-pick
	// tripwire.
	TimesDrafted int
	High         int
	Low          int

	// Injury truth, copied from the sleeper dump at fetch time. Display only —
	// see sleeper.Player for why nothing may ever price them into value. Frozen
	// at fetch: meta.json records when that was, and refetching on draft morning
	// is the ritual that keeps them worth reading.
	InjuryStatus string
	Status       string
	NewsUpdated  int64 // epoch milliseconds, 0 when the dump had no news for him

	// FormatSpread is the largest gap in picks between the primary format's ADP
	// and any cross-checked format. High spread means the player's draft cost is
	// sensitive to scoring rules — a receiving back, or a superflex quarterback.
	FormatSpread float64
	Formats      int
}

// Sigma converts an observed draft-position stdev into the logistic scale
// parameter the survival curve needs, falling back to the default.
//
// Measured across the 2026 FFC pool: stdev runs 0.7 to 45.6, median 11.1, which
// implies a median sigma of ~6.1 — the old hardcoded 6.0 was right for the median
// player and wrong at both tails, which is exactly where it matters.
func Sigma(stdev float64) float64 {
	if stdev <= 0 {
		return SigmaDefault
	}
	return math.Max(SigmaMin, math.Min(SigmaMax, stdev/SigmaFromStdev))
}

// StdevPrior is the pool's own answer to "how much does the market usually
// disagree about a player priced here": a least-squares line of stdev against
// adp, plus the pool median as the fallback when the fit degenerates or the
// line goes non-positive at some adp.
//
// The line is not decoration — spread grows with depth, measurably. On the 2024
// half-ppr pool the fit is stdev = 0.76 + 0.0876*adp, so it expects 1.6 picks of
// disagreement about the adp-10 player and 16.5 about the adp-180 one. Shrinking
// a thin sample toward one pooled median instead would tell a round-15 flier that
// the market agrees about him as tightly as it does about a round-5 back.
type StdevPrior struct {
	Intercept float64
	Slope     float64
	Median    float64
	Pseudo    float64 // prior weight in drafts; ShrinkPseudoDrafts unless a sweep says otherwise
}

// FitStdevPrior computes that line over the pool. adps and stdevs are parallel;
// rows with a missing stdev must be left out by the caller, since a zero there
// means "not reported", not "the market agreed perfectly".
func FitStdevPrior(adps, stdevs []float64) StdevPrior {
	p := StdevPrior{Pseudo: ShrinkPseudoDrafts}
	if len(adps) == 0 {
		return p
	}
	sorted := append([]float64(nil), stdevs...)
	sort.Float64s(sorted)
	p.Median = sorted[len(sorted)/2]

	var mx, my float64
	for i := range adps {
		mx += adps[i]
		my += stdevs[i]
	}
	n := float64(len(adps))
	mx, my = mx/n, my/n
	var sxy, sxx float64
	for i := range adps {
		sxy += (adps[i] - mx) * (stdevs[i] - my)
		sxx += (adps[i] - mx) * (adps[i] - mx)
	}
	if sxx == 0 {
		return p // every player at one adp: no line to draw, the median stands
	}
	p.Slope = sxy / sxx
	p.Intercept = my - p.Slope*mx
	return p
}

// At is the prior spread for a player priced at adp, with the median standing in
// wherever the line would predict a non-positive spread (an extrapolation past
// the ends of the pool it was fitted on).
func (p StdevPrior) At(adp float64) float64 {
	v := p.Intercept + p.Slope*adp
	if v <= 0 {
		return p.Median
	}
	return v
}

// Shrink blends a player's observed stdev toward the prior, weighted by how many
// drafts he was actually taken in. Empirical Bayes in one line.
//
// The blend is on VARIANCE, not on stdev: stdev is a square root, and averaging
// square roots is not the same thing as averaging the quantity they summarise.
// Variance is what adds.
func (p StdevPrior) Shrink(stdev, adp float64, drafts int) float64 {
	return Blend(stdev, p.At(adp), drafts, p.Pseudo)
}

// Blend is that arithmetic with the prior and its weight supplied, so the
// backtest can sweep the pseudo-count without a second copy of the formula.
// A player with no support number reported is left alone: n = 0 would hand the
// whole answer to the prior and throw away the one measurement we have.
func Blend(stdev, prior float64, drafts int, pseudo float64) float64 {
	if stdev <= 0 || drafts <= 0 || prior <= 0 || pseudo <= 0 {
		return stdev
	}
	n := float64(drafts)
	return math.Sqrt((n*stdev*stdev + pseudo*prior*prior) / (n + pseudo))
}

// ShrinkSigma is the pool-level pass that writes every Player.Sigma. It has to
// see the whole pool — the prior is fitted across it — which is why it cannot
// live inside the per-player Sigma().
//
// Call order: it runs once the whole pool is assembled, and it is the last word
// on Sigma — anything set before it is overwritten. Player.Stdev keeps the
// source's raw number so the data tab still reports what FFC said; sigma is
// therefore no longer exactly stdev/SigmaFromStdev, and that gap is the shrink.
func ShrinkSigma(players map[string]*Player) StdevPrior {
	// Ids are sorted before the fit so the sums accumulate in a fixed order and
	// two runs over the same cache write the same digits — the discipline
	// RoomCurveOf and PositionDemand already state. Ranging the map directly made
	// every Sigma in players.json differ in the last ulps run to run: no
	// behavioural effect at 1e-15, and no way to diff two fetches either.
	ids := make([]string, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	adps := make([]float64, 0, len(players))
	stdevs := make([]float64, 0, len(players))
	for _, id := range ids {
		p := players[id]
		if p.Stdev > 0 && p.ADP > 0 {
			adps = append(adps, p.ADP)
			stdevs = append(stdevs, p.Stdev)
		}
	}
	prior := FitStdevPrior(adps, stdevs)
	for _, p := range players {
		p.Sigma = Sigma(prior.Shrink(p.Stdev, p.ADP, p.TimesDrafted))
	}
	return prior
}

// TierSource records where a player's tier came from, so the UI and the logs can
// be honest about it.
type TierSource string

const (
	TierFromRankings TierSource = "rankings" // user-supplied file, always preferred
	TierFromValue    TierSource = "derived"  // computed from value gaps
	TierNone         TierSource = ""
)

// Merge matches the primary ADP source onto Sleeper ids, cross-checks it against
// other scoring formats of the same source, and attaches value and tiers.
//
// Only the primary defines the player set. Cross-check formats are the same pool
// of drafts scored differently, so their ADP is directly comparable — no rescaling
// needed, and any player they add would be one the primary deliberately excluded.
func Merge(ix *rankings.Index, primary FFCResult, crosschecks []FFCResult, cw *Crosswalk) (map[string]*Player, []string) {
	var unmatched []string
	out := map[string]*Player{}

	for _, e := range primary.Entries {
		id, ok := ix.Lookup(e.Name, e.Pos, e.Team)
		if !ok {
			unmatched = append(unmatched, e.Name+" ("+e.Pos+"/"+e.Team+")")
			continue
		}
		p := &Player{
			SleeperID: id,
			Name:      e.Name,
			Pos:       rankings.NormalizePos(e.Pos),
			Team:      e.Team,
			Bye:       e.Bye,
			ADP:       e.ADP,
			Stdev:     e.Stdev,
			// The unshrunk conversion, overwritten by ShrinkSigma below once the
			// whole pool is in. Set here anyway so no half-built map ever carries
			// a zero sigma into the engine's silent SigmaDefault fallback.
			Sigma:   Sigma(e.Stdev),
			Formats: 1,

			// Only the primary's support travels. A cross-check format is a
			// different scoring of the same drafts, so its high/low describe a
			// different price series and averaging them would blur the one number
			// the support floor depends on being exact.
			TimesDrafted: e.TimesDrafted,
			High:         e.High,
			Low:          e.Low,
		}
		// Sleeper is the authority on identity — source team codes disagree
		// (MFL says LVR where Sleeper says LV) and defense names vary wildly.
		if name, pos, team, bye, ok := ix.Authoritative(id); ok {
			p.Name, p.Pos, p.Team = name, pos, team
			if bye != 0 {
				p.Bye = bye
			}
		}
		out[id] = p
	}

	// Every sigma is written here, over the finished pool. Nothing below this
	// line adds a player or touches a stdev, so this is as early as the prior can
	// be fitted and as late as it matters.
	ShrinkSigma(out)

	// Cross-check: same drafts, different scoring. Records disagreement only.
	for _, cc := range crosschecks {
		for _, e := range cc.Entries {
			id, ok := ix.Lookup(e.Name, e.Pos, e.Team)
			if !ok {
				continue
			}
			p, exists := out[id]
			if !exists {
				continue
			}
			if d := math.Abs(p.ADP - e.ADP); d > p.FormatSpread {
				p.FormatSpread = d
			}
			p.Formats++
		}
	}

	if cw != nil {
		for id, p := range out {
			p.Value = cw.Value[id]
		}
	}
	AnchorKDefValues(out)
	AssignTiers(out)
	return out, unmatched
}

// AnchorKDefValues gives kickers and defenses a value on the same curve as
// everybody else, so the endgame machinery has something to compare them with.
// FantasyCalc rates QB/RB/WR/TE only, and a position stuck at value 0 can never
// produce urgency however badly you need a kicker in round 15.
//
// THE RULE IS BY RANK, NOT BY NEIGHBOURING ADP. The spec said "the value of the
// skill player with the nearest overall adp, interpolating between neighbours",
// and implemented literally that produces nonsense, measured on the real board:
// value is not monotone in adp (Tyler Allgeier adp 173.4 carries 538 while
// Keaton Mitchell at adp 172.9 carries 194), so interpolating between whoever
// happens to sit either side gave Denver DEF at adp 103.3 a value of 1698.6,
// Houston DEF at 106.4 a value of 989.9 and the LA Rams at 108.3 a value of
// 251.1 — three defenses ordered nonsensically against their own prices, which
// then corrupts Available(), since that sorts by value.
//
// Instead: let k be the number of valued skill players priced ahead of him and
// take the k-th largest skill value. Monotone by construction (k only grows with
// adp, the value list only shrinks), it lands exactly ON the imported curve
// rather than between two points of it, and it needs no interpolation at all.
// Measured across all 34 k/def on the shipped board: zero monotonicity
// violations, seattle DEF at adp 94.8 -> 1027, philadelphia DEF 132.9 -> 394,
// pittsburgh DEF 148.9 -> 171, evan mcpherson 158.2 -> 112, chris boswell
// 171.5 -> 32.
//
// The rejected alternative is worse than it sounds: ValueBase*exp(-rank/decay)
// gives about 6 at rank 150, on a board where the rank-150 skill player carries
// ~190. That 30x mismatch is what CLAUDE.md's "never mix modes in one draft"
// rule exists to prevent, and it would make kicker urgency invisible forever.
//
// Idempotent, and called again after a rankings file has moved skill values, so
// the anchor always references the curve the board is actually using.
func AnchorKDefValues(players map[string]*Player) {
	// One population, two sorted views of it: a skill player counts toward the
	// rank only if he has both a price and a value, or k would be counted over a
	// different set than it indexes into and the k-th largest value would belong
	// to nobody in particular.
	var values []int     // descending
	var prices []float64 // ascending
	for _, p := range players {
		if isKDef(p.Pos) || p.Value <= 0 || p.ADP <= 0 {
			continue
		}
		values = append(values, p.Value)
		prices = append(prices, p.ADP)
	}
	if len(values) == 0 {
		return // no curve to anchor to; k/def stay at 0 as they were before
	}
	sort.Sort(sort.Reverse(sort.IntSlice(values)))
	sort.Float64s(prices)

	for _, p := range players {
		if !isKDef(p.Pos) {
			continue
		}
		if p.ADP <= 0 {
			// No price, no anchor. A kicker a live feed registered off-board has
			// nothing to be ranked against, and inventing a value for him would be
			// inventing data.
			p.Value = 0
			continue
		}
		k := sort.SearchFloat64s(prices, p.ADP) // skill players strictly ahead of him
		if k < 1 {
			k = 1 // priced ahead of the whole board: the top of the curve
		}
		if k > len(values) {
			k = len(values) // past the end: the floor, not a negative or a wrap
		}
		p.Value = values[k-1]
	}
}

// isKDef is the one place the two positions no source prices are named, since
// three separate rules key off them: an anchored value, no tiers ever, and
// suppression until the last rounds.
func isKDef(pos string) bool { return pos == "K" || pos == "DEF" }

// AssignTiers groups players into value tiers within each position.
//
// Tiers come from value, never from ADP. A fixed ADP gap cannot work: early ADP
// is dense (players go every pick or two) and late ADP is sparse, so any single
// threshold either merges the whole first round or shatters the last one.
// Players without a value get tier 0 and are excluded from cliff logic.
//
// K and DEF are excluded EXPLICITLY, and the exclusion is now load-bearing:
// AnchorKDefValues gives them a value, and the old rule ("anyone with a value
// gets a tier") would have started tiering them the moment it landed. They stay
// at tier 0 on purpose. Their value is borrowed from the skill player at the
// same price, so a "gap" between two kickers is a gap between two receivers
// somewhere else on the board — it cannot draw a real tier boundary. Tier 0 is
// also what keeps cliff logic skipping them, and a red "last one in tier 3"
// about kickers is exactly the kind of alarm this tool must never raise.
func AssignTiers(players map[string]*Player) {
	byPos := map[string][]*Player{}
	for _, p := range players {
		if isKDef(p.Pos) {
			p.Tier, p.TierSrc = 0, TierNone
			continue
		}
		if p.Value > 0 || p.TierSrc == TierFromRankings {
			byPos[p.Pos] = append(byPos[p.Pos], p)
		}
	}
	for _, group := range byPos {
		assignPositionTiers(group)
	}
}

// assignPositionTiers builds the final tier blocks for one position.
//
// Three rules, in order:
//  1. A rankings file's boundaries are hard. Two players it separated stay separated.
//  2. A block bigger than TierMaxSize is subdivided at its largest value drops.
//     This is what stops a 21-player "tier 2" from a published graphic making the
//     position permanently cliff-proof, without second-guessing where the humans
//     actually drew their lines.
//  3. Players the file never covered are tiered from value gaps, and land after
//     everyone it did cover.
func assignPositionTiers(group []*Player) {
	sort.Slice(group, func(i, j int) bool {
		if group[i].Value != group[j].Value {
			return group[i].Value > group[j].Value
		}
		return group[i].ADP < group[j].ADP // deterministic when values tie
	})

	var blocks [][]*Player

	// Rule 1: one block per source tier, in the file's own order.
	ranked := map[int][]*Player{}
	var rankedTiers []int
	var rest []*Player
	for _, p := range group {
		if p.TierSrc == TierFromRankings && p.Tier > 0 {
			if _, seen := ranked[p.Tier]; !seen {
				rankedTiers = append(rankedTiers, p.Tier)
			}
			ranked[p.Tier] = append(ranked[p.Tier], p)
		} else {
			rest = append(rest, p)
		}
	}
	sort.Ints(rankedTiers)
	for _, t := range rankedTiers {
		blocks = append(blocks, subdivide(ranked[t])...) // rule 2
	}

	// Rule 3: value-gap tiers for whatever the file didn't cover.
	blocks = append(blocks, deriveBlocks(rest)...)

	for i, b := range blocks {
		for _, p := range b {
			p.Tier = i + 1
		}
	}
}

// subdivide splits an oversized block at its largest relative value drops until
// every piece fits within TierMaxSize. Returns the block untouched if it already
// fits or if no player in it carries a value to split on.
func subdivide(block []*Player) [][]*Player {
	if len(block) <= TierMaxSize {
		return [][]*Player{block}
	}
	pieces := [][]*Player{block}
	for {
		worst, worstLen := -1, TierMaxSize
		for i, p := range pieces {
			if len(p) > worstLen {
				worst, worstLen = i, len(p)
			}
		}
		if worst < 0 {
			return pieces
		}
		at := biggestDrop(pieces[worst], TierMinSize)
		if at <= 0 {
			return pieces // flat values, nothing meaningful to split on
		}
		p := pieces[worst]
		split := [][]*Player{p[:at], p[at:]}
		pieces = append(pieces[:worst], append(split, pieces[worst+1:]...)...)
	}
}

// biggestDrop returns the index of the largest relative value drop, considering
// only split points that leave at least minSize players on both sides. Returns 0
// when no legal split exists.
func biggestDrop(block []*Player, minSize int) int {
	best, bestDrop := 0, 0.0
	for i := minSize; i <= len(block)-minSize; i++ {
		prev := float64(block[i-1].Value)
		if prev <= 0 {
			continue
		}
		if d := (prev - float64(block[i].Value)) / prev; d > bestDrop {
			best, bestDrop = i, d
		}
	}
	return best
}

// deriveBlocks groups players by value gaps, the fallback when no file covers them.
func deriveBlocks(rest []*Player) [][]*Player {
	if len(rest) == 0 {
		return nil
	}
	floor := float64(rest[0].Value) * TierFloorPct
	cur := []*Player{rest[0]}
	rest[0].TierSrc = TierFromValue
	var out [][]*Player
	for i := 1; i < len(rest); i++ {
		prev := float64(rest[i-1].Value)
		drop := prev - float64(rest[i].Value)
		if prev > 0 && drop/prev > TierDropPct && drop >= floor {
			out = append(out, cur)
			cur = nil
		}
		rest[i].TierSrc = TierFromValue
		cur = append(cur, rest[i])
	}
	return mergeRuntTiers(append(out, cur))
}

// mergeRuntTiers folds any derived block below TierMinSize into its neighbour, so
// the value-gap rule can't leave a trail of one-player tiers down the flat tail.
func mergeRuntTiers(blocks [][]*Player) [][]*Player {
	var out [][]*Player
	for _, b := range blocks {
		if len(out) > 0 && len(b) < TierMinSize {
			out[len(out)-1] = append(out[len(out)-1], b...)
			continue
		}
		out = append(out, b)
	}
	// A short leading block has no previous neighbour; fold it forward instead.
	if len(out) > 1 && len(out[0]) < TierMinSize {
		out[1] = append(out[0], out[1]...)
		out = out[1:]
	}
	return out
}

// Deliberately absent: cross-source ADP blending. It was built, measured, and
// removed. See FetchMFL for why — MFL's feed ranks a different population, and
// projecting it onto FFC's scale put incoming rookies inside the first two rounds.

// ApplyRankings overlays a user-supplied rankings file. It is the highest-priority
// source for both tiers and value: the whole point of the file is that the user
// trusts it more than anything we fetched.
//
// Tiers are only taken when the file's own tiers are granular enough to be worth
// using (see rankings.File.UsableTiers) — a file whose tiers are mostly singletons
// would pin the board in permanent cliff state, so we keep the derived ones.
func ApplyRankings(players map[string]*Player, ix *rankings.Index, f *rankings.File) (r RankingsResult, unmatched []string) {
	r.TiersUsed = f.UsableTiers()

	for _, row := range f.Rows {
		id, ok := ix.Lookup(row.Name, row.Pos, row.Team)
		if !ok {
			unmatched = append(unmatched, row.Name+" ("+row.Pos+"/"+row.Team+")")
			continue
		}
		p, exists := players[id]
		if !exists {
			// Resolved to a real Sleeper player, but too deep to appear in the
			// ADP board. Not a matching failure — just outside the draftable set.
			r.OffBoard++
			continue
		}
		r.Applied++
		if row.Points > 0 {
			p.Value = int(row.Points * 100) // keep a little precision; scale is arbitrary
		}
		if r.TiersUsed && row.Tier > 0 {
			p.Tier = row.Tier
			p.TierSrc = TierFromRankings
		}
		if row.Sentiment != "" {
			p.Sentiment = row.Sentiment
			r.Opinions++
		}
	}

	// A points column rewrites skill values, and k/def values are read off that
	// curve, so re-anchor before re-tiering or the kickers stay pinned to the
	// values of a board that no longer exists.
	AnchorKDefValues(players)

	// Re-derive for anyone the file didn't cover, and for everyone if its tiers
	// were unusable. Players the file did set are skipped inside AssignTiers.
	AssignTiers(players)
	return r, unmatched
}

// RankingsResult reports what a rankings file actually did.
type RankingsResult struct {
	Applied   int  // rows that landed on a player in the ADP board
	OffBoard  int  // rows that resolved to a real player who is too deep to be drafted
	Opinions  int  // applied rows that carried a target/pass/avoid opinion
	TiersUsed bool // whether the file's tiers were granular enough to use
}
