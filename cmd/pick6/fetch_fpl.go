package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/fpl"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// fplMaxAge is how long a cached pool is good for. Same daily rhythm as the
// sleeper dump: fpl's draft_rank moves when fpl decides it does, and the injury
// layer under it is the half that actually goes stale — which is why the ritual
// is to refetch on draft morning rather than to poll a static file.
const fplMaxAge = 12 * time.Hour

// runFetchFPL builds the fpl board.
//
// It is a different pipeline from the nfl fetch, not a flag inside it, and the
// list of things it does NOT do is most of what makes it short: no id crosswalk
// (one source, one id), no second format to cross-check (there is one ranking),
// no room curve and no escape rates (fpl draft leagues are per-season with
// fresh ids, so this room has no history yet), no k/def value anchor (every fpl
// player is ranked, so nobody needs a borrowed value), and no meta.json (an fpl
// fetch eight days before the nfl draft must not be able to touch that board).
func runFetchFPL(sp sport, rankFile string, verbose bool) error {
	bs, hit, err := fpl.GetBootstrap(fplMaxAge)
	if err != nil {
		return err
	}
	pool := bs.Draftable()
	say("fpl bootstrap", hit, fmt.Sprintf("%d players, %d draftable", len(bs.Elements), len(pool)))
	if dropped := len(bs.Elements) - len(pool); dropped > 0 {
		note("filtered", "left", fmt.Sprintf("%d players have left the league — an undraftable player is not a player", dropped))
	}

	players := map[string]*adp.Player{}
	byPos := map[string]int{}
	injured := map[string]int{}
	for _, e := range pool {
		pos := bs.Pos(e)
		if pos == "" {
			continue
		}
		p := &adp.Player{
			SleeperID: strconv.Itoa(e.ID),
			Name:      e.Name(),
			Pos:       pos,
			Team:      bs.TeamCode(e),
			// draft_rank IS the price, in rank units. The sim wants an ordering
			// and a need, and this is the ordering — fpl's own, which prices an
			// injury-shortened season back up where the market has it rather
			// than burying it a hundred ranks deep the way a points sort does.
			ADP: float64(e.DraftRank),
			// No source anywhere reports an fpl draft-position spread, so every
			// player runs on the default width. Written explicitly rather than
			// left at zero: a zero sigma divides by zero in the v1 logistic, and
			// -survival=adp is refused for this sport partly because of it.
			Sigma: adp.Sigma(0),
			Value: fpl.RankValue(e.DraftRank, engine.ValueDecay),

			InjuryStatus: e.InjuryChip(),
			NewsUpdated:  e.NewsMillis(),
		}
		players[p.SleeperID] = p
		byPos[pos]++
		if p.InjuryStatus != "" {
			injured[pos]++
		}
	}
	note("pool", "ranked", posSummary(sp, byPos))
	if len(injured) > 0 {
		note("flags", "injury", posSummary(sp, injured)+" — display only, no value is ever marked down")
	}

	// Tiers: the file's if there is one, derived from the value curve for
	// everyone it missed. AssignTiersAll rather than AssignTiers, or all 184
	// defenders arrive untiered and a third of the board loses its cliffs.
	tierOrigin := "value gaps"
	if rankFile != "" {
		applied, unmatched, err := applyFPLRankings(players, rankFile)
		if err != nil {
			return err
		}
		tierOrigin = filepath.Base(rankFile)
		note("rankings", "applied", fmt.Sprintf("%d rows matched, %d unmatched", applied, len(unmatched)))
		printUnmatched(unmatched, verbose)
	}
	adp.AssignTiersAll(players)

	tiered := map[string]int{}
	for _, p := range players {
		if p.Tier > 0 {
			tiered[p.Pos]++
		}
	}
	fmt.Printf("tiers from %s: %s\n", tierOrigin, posSummary(sp, tiered))

	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, sp.board)
	if err := writePlayers(path, players); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s (%d players)\n", path, len(players))

	printFPLTop(sp, players)
	printFPLReplacement(sp, players)
	return nil
}

// applyFPLRankings joins a rankings csv onto the pool by name and position, and
// it does NOT go through rankings.Index.
//
// The Index routes every DEF row through a team-keyed map — right for the one
// nfl team defense per club, and silently destructive here, where twenty club
// codes would absorb a hundred and eighty-four defenders, last writer winning,
// every lookup reporting success. Its fuzzy fallback is the second reason: it
// was measured at zero false positives on two-word nfl full names, and fpl's
// board is one-word surnames where an edit distance of two joins Wilson to
// Wilson, three of whom are different men at three different clubs.
//
// So: exact, on normalized name plus position, and anything that misses is
// printed and dropped. A name with no exact match is one to fix in the csv.
func applyFPLRankings(players map[string]*adp.Player, path string) (int, []string, error) {
	f, err := rankings.LoadCSV(path)
	if err != nil {
		return 0, nil, err
	}
	byKey := map[string]*adp.Player{}
	for _, p := range players {
		byKey[rankings.Normalize(p.Name)+"|"+p.Pos] = p
	}
	applied := 0
	var unmatched []string
	for _, r := range f.Rows {
		pos := rankings.NormalizePos(r.Pos)
		p, ok := byKey[rankings.Normalize(r.Name)+"|"+pos]
		if !ok {
			unmatched = append(unmatched, r.Name+" ("+strings.ToLower(pos)+")")
			continue
		}
		if r.Tier > 0 {
			p.Tier, p.TierSrc = r.Tier, adp.TierFromRankings
		}
		if r.Sentiment != "" {
			p.Sentiment = r.Sentiment
		}
		applied++
	}
	return applied, unmatched, nil
}

// printFPLTop is the top of the rank board, so a fetch that ran can be eyeballed
// against the app in five seconds.
func printFPLTop(sp sport, players map[string]*adp.Player) {
	list := sortedByADP(players)
	fmt.Println("\ntop of the board — fpl's own draft_rank")
	for i, p := range list {
		if i >= 15 {
			break
		}
		tier := "—"
		if p.Tier > 0 {
			tier = fmt.Sprintf("t%d", p.Tier)
		}
		fmt.Printf("  %3.0f  %-18s %-4s %-4s %5d  %s\n",
			p.ADP, strings.ToLower(p.Name), strings.ToLower(p.Pos), p.Team, p.Value, tier)
	}
}

// printFPLReplacement is the vor baseline: under a hard quota with no flex, the
// replacement index is teams x the quota at that position, which is exactly the
// last man the room rosters. The nfl board's two-index rule arrives at the same
// answer here without being told — no position can reach a flex slot, so every
// one of them indexes at startable slots.
func printFPLReplacement(sp sport, players map[string]*adp.Player) {
	byPos := map[string][]*adp.Player{}
	for _, p := range players {
		byPos[p.Pos] = append(byPos[p.Pos], p)
	}
	fmt.Println("\nreplacement level — the last man a 10-manager room rosters at each position")
	quota := map[string]int{}
	for _, slot := range sp.roster.Slots {
		quota[slot]++
	}
	for _, pos := range engine.RosterPositions(sp.roster) {
		list := byPos[pos]
		sort.Slice(list, func(i, j int) bool { return list[i].ADP < list[j].ADP })
		d := quota[pos] * sp.teams
		v, name := 0, "—"
		if d > 0 && d <= len(list) {
			v, name = list[d-1].Value, strings.ToLower(list[d-1].Name)
		}
		fmt.Printf("  %-4s %3d st  %6d  %s\n", strings.ToLower(pos), d, v, name)
	}
}
