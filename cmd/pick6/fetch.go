package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

func runFetch(args []string) error {
	fs := flagSet("fetch")
	format := fs.String("format", "half-ppr", "primary scoring format: half-ppr, ppr, standard, 2qb")
	teams := fs.Int("teams", 12, "league size")
	year := fs.Int("year", 2026, "season")
	rankFile := fs.String("rankings", "", "path to a rankings csv; its tiers and points win over fetched data")
	verbose := fs.Bool("v", false, "list every unmatched name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 1. sleeper player pool -------------------------------------------------
	pool, hit, err := sleeper.Players()
	if err != nil {
		return fmt.Errorf("sleeper players: %w", err)
	}
	say("sleeper", hit, fmt.Sprintf("%d active fantasy players", len(pool)))
	ix := rankings.NewIndex(pool)

	// 2. primary adp ---------------------------------------------------------
	primary, hit, err := adp.FetchFFC(*format, *teams, *year)
	if err != nil {
		return err
	}
	say("ffc "+*format, hit, fmt.Sprintf("%d players, %d drafts (%s)",
		len(primary.Entries), primary.TotalDrafts, primary.Window))

	// 3. cross-check formats -------------------------------------------------
	// Same drafts, scored differently. Used only to measure how scoring-sensitive
	// a player's draft cost is; never blended into the primary ADP.
	var crosschecks []adp.FFCResult
	for _, f := range adp.FFCFormats {
		if f == *format || !adp.Comparable(f, *format) {
			continue
		}
		r, hit, err := adp.FetchFFC(f, *teams, *year)
		if err != nil {
			fmt.Printf("  %-12s %-8s %v\n", "ffc "+f, "skipped", strings.ToLower(err.Error()))
			continue
		}
		say("ffc "+f, hit, fmt.Sprintf("%d players, %d drafts", len(r.Entries), r.TotalDrafts))
		crosschecks = append(crosschecks, r)
	}

	// 4. value + id crosswalk ------------------------------------------------
	cw, hit, err := adp.FetchCrosswalk(1, *teams, 1)
	if err != nil {
		fmt.Printf("  %-12s %-8s %v\n", "fantasycalc", "skipped", strings.ToLower(err.Error()))
		cw = &adp.Crosswalk{Tier: map[string]int{}, Value: map[string]int{}, MflToSleeper: map[string]string{}}
	} else {
		say("fantasycalc", hit, fmt.Sprintf("%d players, %d with a value", cw.Count, len(cw.Value)))
	}

	// 5. merge ---------------------------------------------------------------
	players, unmatched := adp.Merge(ix, primary, crosschecks, cw)

	// 6. user rankings override ----------------------------------------------
	tierOrigin := "value gaps (no rankings file)"
	if *rankFile != "" {
		rf, err := rankings.LoadCSV(*rankFile)
		if err != nil {
			return fmt.Errorf("rankings: %w", err)
		}
		res, rankUnmatched := adp.ApplyRankings(players, ix, rf)

		what := []string{}
		if rf.HasPoints {
			what = append(what, "points")
		}
		if rf.HasTier {
			what = append(what, "tiers")
		}
		if len(what) == 0 {
			what = append(what, "names only")
		}
		detail := fmt.Sprintf("%d rows, %d applied, has %s",
			len(rf.Rows), res.Applied, strings.Join(what, " + "))
		if res.OffBoard > 0 {
			detail += fmt.Sprintf(" (%d too deep to be drafted)", res.OffBoard)
		}
		fmt.Printf("  %-12s %-8s %s\n", "rankings", "loaded", detail)

		if rf.TiersWereGlobal {
			fmt.Println("  note        the file numbered tiers across all positions; renumbered per position")
		}
		switch {
		case res.TiersUsed:
			tierOrigin = "your rankings file"
		case rf.HasTier:
			tierOrigin = "value gaps (file's tiers were mostly singletons, unusable for cliffs)"
		}
		unmatched = append(unmatched, rankUnmatched...)
	}

	valued, tiered := 0, map[string]int{}
	for _, p := range players {
		if p.Value > 0 {
			valued++
		}
		if p.Tier > tiered[p.Pos] {
			tiered[p.Pos] = p.Tier
		}
	}

	fmt.Println()
	matched := len(primary.Entries) - len(unmatched)
	fmt.Printf("merged %d players — %d/%d matched to sleeper (%.1f%%), %d with a value\n",
		len(players), matched, len(primary.Entries),
		100*float64(matched)/float64(len(primary.Entries)), valued)
	fmt.Printf("tiers from %s: %s\n", tierOrigin, tierSummary(tiered))

	if len(unmatched) > 0 {
		fmt.Printf("%d unmatched\n", len(unmatched))
		if *verbose {
			sort.Strings(unmatched)
			for _, n := range unmatched {
				fmt.Printf("  %s\n", strings.ToLower(n))
			}
		} else {
			fmt.Println("  run with -v to list them")
		}
	}

	// 6. write ---------------------------------------------------------------
	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	outPath := filepath.Join(dir, "players.json")
	if err := writePlayers(outPath, players); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s\n", strings.ToLower(outPath))

	if err := writeMappingStub(dir, unmatched); err != nil {
		return err
	}
	printPreview(players)
	return nil
}

func say(source string, fetched bool, detail string) {
	where := "cached"
	if fetched {
		where = "fetched"
	}
	fmt.Printf("  %-12s %-8s %s\n", source, where, detail)
}

func tierSummary(byPos map[string]int) string {
	order := []string{"QB", "RB", "WR", "TE", "K", "DEF"}
	var parts []string
	for _, p := range order {
		if n, ok := byPos[p]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ToLower(p), n))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func writePlayers(path string, players map[string]*adp.Player) error {
	list := sortedByADP(players)
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// writeMappingStub drops a mapping.json so unmatched names can be fixed by hand.
// Manual entries always win, so never overwrite an existing file.
func writeMappingStub(dir string, unmatched []string) error {
	if len(unmatched) == 0 {
		return nil
	}
	path := filepath.Join(dir, "mapping.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	stub := map[string]string{}
	for _, n := range unmatched {
		stub[n] = ""
	}
	b, err := json.MarshalIndent(stub, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s — fill in sleeper ids for anything you care about\n", strings.ToLower(path))
	return os.WriteFile(path, b, 0o644)
}

func sortedByADP(players map[string]*adp.Player) []*adp.Player {
	list := make([]*adp.Player, 0, len(players))
	for _, p := range players {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ADP < list[j].ADP })
	return list
}

func printPreview(players map[string]*adp.Player) {
	list := sortedByADP(players)
	fmt.Printf("\ntop 14 by adp:\n")
	fmt.Printf("  %-3s %-22s %-4s %-4s %6s %6s %6s %5s %6s\n",
		"#", "name", "pos", "tm", "adp", "sigma", "value", "tier", "spread")
	for i, p := range list {
		if i >= 14 {
			break
		}
		fmt.Printf("  %-3d %-22s %-4s %-4s %6.1f %6.1f %6d %5s %6.1f\n",
			i+1, trunc(strings.ToLower(p.Name), 22), strings.ToLower(p.Pos), p.Team,
			p.ADP, p.Sigma, p.Value, dash(p.Tier), p.FormatSpread)
	}

	// The most scoring-sensitive players are genuinely useful to eyeball before
	// a half-ppr draft: they are where format assumptions bite hardest.
	bySpread := append([]*adp.Player(nil), list...)
	sort.Slice(bySpread, func(i, j int) bool { return bySpread[i].FormatSpread > bySpread[j].FormatSpread })
	fmt.Printf("\nmost scoring-sensitive (adp swing across formats):\n")
	for i, p := range bySpread {
		if i >= 5 || p.FormatSpread == 0 {
			break
		}
		fmt.Printf("  %-22s %-4s %6.1f adp, swings %.0f picks\n",
			trunc(strings.ToLower(p.Name), 22), strings.ToLower(p.Pos), p.ADP, p.FormatSpread)
	}
}

func dash(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
