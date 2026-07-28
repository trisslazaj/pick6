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
type Entry struct {
	Name   string
	Pos    string
	Team   string
	ADP    float64
	Stdev  float64 // 0 when the source doesn't report it
	Bye    int
	Sample int
}

// Player is the merged, matched result: one row per Sleeper player id.
type Player struct {
	SleeperID string
	Name      string
	Pos       string
	Team      string
	Bye       int

	ADP     float64 // from the primary format, in pick units
	Stdev   float64 // observed draft-position stdev, 0 if unknown
	Sigma   float64 // logistic scale the survival model wants
	Value   int     // relative season value; 0 when unknown
	Tier    int     // per-position tier; 0 when unknown
	TierSrc TierSource

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
			Sigma:     Sigma(e.Stdev),
			Formats:   1,
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
	AssignTiers(out)
	return out, unmatched
}

// AssignTiers groups players into value tiers within each position.
//
// Tiers come from value, never from ADP. A fixed ADP gap cannot work: early ADP
// is dense (players go every pick or two) and late ADP is sparse, so any single
// threshold either merges the whole first round or shatters the last one.
// Players without a value get tier 0 and are excluded from cliff logic.
func AssignTiers(players map[string]*Player) {
	byPos := map[string][]*Player{}
	for _, p := range players {
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
	}

	// Re-derive for anyone the file didn't cover, and for everyone if its tiers
	// were unusable. Players the file did set are skipped inside AssignTiers.
	AssignTiers(players)
	return r, unmatched
}

// RankingsResult reports what a rankings file actually did.
type RankingsResult struct {
	Applied   int  // rows that landed on a player in the ADP board
	OffBoard  int  // rows that resolved to a real player who is too deep to be drafted
	TiersUsed bool // whether the file's tiers were granular enough to use
}
