package adp

import (
	"sort"
	"strings"
)

// TierAdpGapFlag is how far a player's tier rank and his market rank must
// disagree before the disagreement is worth printing. 8 places is roughly a
// round in a 12-team draft — smaller than that and the two orderings are saying
// the same thing with rounding noise.
//
// Fetch-side, so it lives here rather than in engine/tuning.go: the engine never
// asks this question. This is the same deliberate split as the tier constants
// above, which the engine mirrors but fetch actually runs.
const TierAdpGapFlag = 8

// injuryAlarms are the sleeper injury_status values that mean "this man may not
// play", as opposed to "he has a knock". Counts are from the cached dump on
// 2026-07-29, over all 12,202 players:
//
//	PUP 98 · NA 84 · IR 21 · Out 4 · Sus 4 · COV 2 · DNR 2
//
// NA (not active) and DNR (did not report) are not in the original spec's list
// and are in the dump; they are the same class of answer, so they are here. COV
// has zero carriers in the active pool today and is kept for the same reason.
//
// Questionable is deliberately ABSENT. 326 players carry it dump-wide and 93 in
// the active fantasy pool — in july that is the league's ordinary bumps and
// bruises, and a chip on ninety-three names is wallpaper, not a warning.
var injuryAlarms = map[string]bool{
	"OUT": true, "IR": true, "PUP": true, "DOUBTFUL": true,
	"SUS": true, "NA": true, "DNR": true, "COV": true,
}

// InjuryAlarm reports whether a player's sleeper status is worth flagging on the
// board. Two independent signals, either one is enough: an injury_status in the
// alarm set, or a roster status that isn't Active.
//
// An EMPTY status is unknown, not hurt. 32 active players carry no status at
// all, and on the 2026 board those 32 include all 19 defenses — treating empty
// as "not Active" would put an injury chip on every defense in the league and
// invent a fact about everyone else it caught.
func InjuryAlarm(injuryStatus, status string) bool {
	if injuryAlarms[strings.ToUpper(strings.TrimSpace(injuryStatus))] {
		return true
	}
	s := strings.TrimSpace(status)
	return s != "" && !strings.EqualFold(s, "Active")
}

// InjuryNote renders a player's flags as one lowercase chip string, e.g.
// "pup · inactive". Empty when nothing is flagged.
func InjuryNote(injuryStatus, status string) string {
	var parts []string
	if injuryAlarms[strings.ToUpper(strings.TrimSpace(injuryStatus))] {
		parts = append(parts, strings.ToLower(strings.TrimSpace(injuryStatus)))
	}
	if s := strings.TrimSpace(status); s != "" && !strings.EqualFold(s, "Active") {
		parts = append(parts, strings.ToLower(s))
	}
	return strings.Join(parts, " · ")
}

// TierGap is one player the tiers and the market rank differently.
type TierGap struct {
	Player   *Player
	TierRank int // 1-indexed rank within his position by (tier, value)
	ADPRank  int // 1-indexed rank within his position by adp
	Delta    int // TierRank - ADPRank; negative means the tiers like him more
}

// TierADPGaps ranks every tiered player twice within his own position — once the
// way the board will order him (tier, then value) and once the way the room
// prices him (adp) — and returns everyone whose two ranks differ by at least
// minGap, biggest disagreement first.
//
// This exists for one human chore: re-transcribing the tier graphic before draft
// day. A hand-typed tier is one keystroke from wrong, and a wrong tier is
// invisible on the board — it just quietly sorts somebody into the wrong group.
// Where the market flatly disagrees with the file is where a slip would show up
// first, so those are the rows worth re-reading against the source image.
//
// Untiered players (K/DEF and anyone no source valued) and players with no ADP
// are skipped: one of the two rankings doesn't exist for them, so there is no
// disagreement to measure.
func TierADPGaps(players map[string]*Player, minGap int) []TierGap {
	byPos := map[string][]*Player{}
	for _, p := range players {
		if p.Tier > 0 && p.ADP > 0 {
			byPos[p.Pos] = append(byPos[p.Pos], p)
		}
	}

	var out []TierGap
	for _, group := range byPos {
		byTier := append([]*Player(nil), group...)
		sort.Slice(byTier, func(i, j int) bool {
			if byTier[i].Tier != byTier[j].Tier {
				return byTier[i].Tier < byTier[j].Tier
			}
			if byTier[i].Value != byTier[j].Value {
				return byTier[i].Value > byTier[j].Value
			}
			return byTier[i].SleeperID < byTier[j].SleeperID // map order is not an order
		})
		tierRank := map[string]int{}
		for i, p := range byTier {
			tierRank[p.SleeperID] = i + 1
		}

		byADP := append([]*Player(nil), group...)
		sort.Slice(byADP, func(i, j int) bool {
			if byADP[i].ADP != byADP[j].ADP {
				return byADP[i].ADP < byADP[j].ADP
			}
			return byADP[i].SleeperID < byADP[j].SleeperID
		})
		for i, p := range byADP {
			d := tierRank[p.SleeperID] - (i + 1)
			if d >= minGap || -d >= minGap {
				out = append(out, TierGap{Player: p, TierRank: tierRank[p.SleeperID], ADPRank: i + 1, Delta: d})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if abs(out[i].Delta) != abs(out[j].Delta) {
			return abs(out[i].Delta) > abs(out[j].Delta)
		}
		if out[i].Player.Pos != out[j].Player.Pos {
			return out[i].Player.Pos < out[j].Player.Pos
		}
		return out[i].Player.SleeperID < out[j].Player.SleeperID
	})
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
