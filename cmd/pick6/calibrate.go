package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// calibrateDrafts are this user's own completed drafts (ids from CLAUDE.md).
// Season is read from each draft's metadata rather than hardcoded alongside the
// id, so the era check below is the draft's own answer and not this list's.
var calibrateDrafts = []string{
	"1133489617308684288",
	"1261824503076360192",
	"1253161474382102529",
}

// runCalibrate scores the survival model against drafts that already happened.
//
// Every constant in tuning.go started as a guess. This replays a real draft from
// all twelve seats, asks the engine the same question it answers at the clock —
// "is he still there at my next pick?" — and grades every answer against what the
// room actually did. Nothing here writes anything: it prints numbers a human then
// decides to believe or not.
func runCalibrate(args []string) error {
	fs := flagSet("calibrate")
	format := fs.String("format", "half-ppr", "scoring format to score against; must match the drafts")
	verbose := fs.Bool("v", false, "list the era names with no exact sleeper match")
	tune := fs.Bool("tune", false, "grid-search the sigma constants and print the winner")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("pick6 calibrate — scoring survival against drafts that already happened")
	fmt.Println()

	pool, hit, err := sleeper.Players()
	if err != nil {
		return fmt.Errorf("sleeper players: %w", err)
	}
	say("sleeper", hit, fmt.Sprintf("%d active fantasy players", len(pool)))
	ix := rankings.NewIndex(pool)

	boards := map[int]*eraBoard{} // one ffc pull per season, shared by drafts in it
	var preds []pred
	var dropped []string
	var eraPool []engine.Player // every era board that loaded, for -tune's coverage line
	drafts, skipped, noEra, vantages := 0, 0, 0, 0

	for _, id := range calibrateDrafts {
		d, err := sleeper.CachedDraft(id)
		if err != nil {
			note("draft", "skipped", id+" · "+strings.ToLower(err.Error()))
			skipped++
			continue
		}
		picks, err := sleeper.CachedPicks(id)
		if err == nil {
			err = d.Validate()
		}
		if err != nil {
			note("draft", "skipped", id+" · "+d.Season+" · "+strings.ToLower(err.Error()))
			skipped++
			continue
		}

		year, _ := strconv.Atoi(d.Season)
		b, seen := boards[year]
		if !seen {
			b = loadEraBoard(ix, *format, year)
			boards[year] = b
			if b.err == nil {
				detail := fmt.Sprintf("%d/%d names matched, %d dropped", len(b.players),
					len(b.players)+len(b.dropped), len(b.dropped))
				// A count with no way to reach the names is a dead end: a broken
				// join would read "148/178, 30 dropped" and stop there.
				if len(b.dropped) > 0 && !*verbose {
					detail += " · run with -v to list them"
				}
				note(fmt.Sprintf("join %d", year), "exact", detail)
				dropped = append(dropped, b.dropped...)
				eraPool = append(eraPool, b.players...)
			}
		}
		if b.err != nil {
			// No adp from that season. Scoring these picks against a different
			// year's prices would move every number below without saying so, and
			// a silently wrong backtest is worse than a missing one.
			note("draft", "skipped", fmt.Sprintf("%s · %s · no era adp (%s)",
				id, d.Season, strings.ToLower(b.err.Error())))
			skipped++
			noEra++
			continue
		}

		p, v := walk(d, picks, b.players)
		preds = append(preds, p...)
		vantages += v
		drafts++
		note("draft", "scored", fmt.Sprintf("%s · %s · %d teams, %d rounds, %d picks",
			id, d.Season, d.Settings.Teams, d.Settings.Rounds, len(picks)))
		note("era check", eraTag(d, b.res), eraDetail(d, b.res))
	}

	if *verbose && len(dropped) > 0 {
		fmt.Println("\ndropped — no exact sleeper match, never fuzzed:")
		sort.Strings(dropped)
		for _, n := range dropped {
			fmt.Printf("  %s\n", n)
		}
	}
	if len(preds) == 0 {
		return fmt.Errorf("nothing scorable — no draft had era adp")
	}

	report(preds, drafts, vantages)
	if *tune {
		tuneSigma(preds, eraPool)
	}
	printCaveats(skipped, noEra)
	return nil
}

// eraBoard is one season's adp joined onto sleeper ids.
//
// Exact matches only. Index.Lookup would fall through to Levenshtein, and a 2024
// name that is missing from the *current* active pool — retired, cut, out of the
// league — would map to a similarly-named active player whose id never appears
// in the 2024 picks. He would then be labeled "survived forever" at every single
// vantage, which is a bias, not a gap.
type eraBoard struct {
	players []engine.Player
	dropped []string
	res     adp.FFCResult
	err     error
}

func loadEraBoard(ix *rankings.Index, format string, year int) *eraBoard {
	// teams is cosmetic to ffc (identical adp for 8/10/12/14) but it is part of
	// the url, so keep it at the league size the cache filename already implies.
	res, fetched, err := adp.FetchFFC(format, 12, year)
	if err != nil {
		return &eraBoard{err: err}
	}
	say(fmt.Sprintf("ffc %d", year), fetched, fmt.Sprintf("%d players, %d drafts (%s)",
		len(res.Entries), res.TotalDrafts, res.Window))

	b := &eraBoard{res: res}
	for _, e := range res.Entries {
		id, ok := ix.LookupExact(e.Name, e.Pos, e.Team)
		if !ok {
			// Lowercased here, not at print time: the team code is the one thing
			// that stays uppercase, and a blanket ToLower on the way out would
			// eat it along with the name.
			b.dropped = append(b.dropped, fmt.Sprintf("%s (%s/%s)",
				strings.ToLower(e.Name), strings.ToLower(e.Pos), e.Team))
			continue
		}
		b.players = append(b.players, engine.Player{
			ID:    id,
			Name:  e.Name,
			Pos:   rankings.NormalizePos(e.Pos),
			Team:  e.Team,
			ADP:   e.ADP,
			Sigma: adp.Sigma(e.Stdev), // the same conversion fetch writes
			Stdev: e.Stdev,
		})
	}
	return b
}

// walk scores one draft from every seat.
//
// Twelve seats over the same 180 picks is twelve times the labeled data for
// free — the snake math is seat-agnostic, so each seat is a different schedule
// of vantages over the same reality. It is not twelve times the *evidence*
// (same draft, correlated), which the caveats say out loud.
func walk(d *sleeper.Draft, picks []sleeper.DraftPick, board []engine.Player) (out []pred, vantages int) {
	drafted := make(map[string]int, len(picks))
	for _, p := range picks {
		drafted[p.PlayerID] = p.PickNo
	}
	teams, rounds := d.Settings.Teams, d.Settings.Rounds

	for seat := 1; seat <= teams; seat++ {
		// The state is here for its snake math and its PickNo; PSurviveAt reads
		// nothing else, so the player map stays empty and the board is handed in
		// row by row with era adp and era sigma already on it.
		s := engine.New(nil, teams, rounds, seat)
		for r := 1; r < rounds; r++ {
			from, to := s.MyPick(r), s.MyPick(r+1)
			s.PickNo = from
			vantages++
			for _, pl := range board {
				at := drafted[pl.ID]
				if at != 0 && at < from {
					continue // already off the board at this vantage
				}
				y := 0.0
				if at == 0 || at >= to {
					y = 1 // he really was still sitting there
				}
				out = append(out, pred{
					pos: pl.Pos, adp: pl.ADP, stdev: pl.Stdev,
					from: from, to: to, teams: teams,
					q: s.PSurviveAt(pl, to), y: y,
				})
			}
		}
	}
	return out, vantages
}

// eraTag / eraDetail measure the gap the spec assumed instead of asserting it.
// ffc serves the trailing snapshot of a season's draft week, and this user's
// 2024 draft ran inside that window — so for this backtest the prices really are
// the ones the room had, not a couple of weeks of drift.
func eraTag(d *sleeper.Draft, res adp.FFCResult) string {
	if d.StartTime == 0 {
		return "unknown"
	}
	day := time.UnixMilli(d.StartTime).Format("2006-01-02")
	start, end, ok := windowDays(res.Window)
	if !ok {
		return "unknown"
	}
	if day >= start && day <= end {
		return "match"
	}
	return "drift"
}

func eraDetail(d *sleeper.Draft, res adp.FFCResult) string {
	if d.StartTime == 0 {
		return "draft carries no start time; assume the adp snapshot postdates it"
	}
	day := time.UnixMilli(d.StartTime).Format("2006-01-02")
	start, end, ok := windowDays(res.Window)
	if !ok {
		return "draft ran " + day + "; adp window unparseable"
	}
	if day >= start && day <= end {
		return "draft ran " + day + ", inside the adp window — same market"
	}
	return fmt.Sprintf("draft ran %s, adp window is %s..%s — the market moved between them", day, start, end)
}

func windowDays(window string) (start, end string, ok bool) {
	parts := strings.Split(window, "..")
	if len(parts) != 2 || len(parts[0]) != 10 || len(parts[1]) != 10 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// note is the one aligned status line every command prints. say wraps it for the
// cached/fetched case so the columns can't drift apart between subcommands.
func note(label, tag, detail string) {
	fmt.Printf("  %-12s %-8s %s\n", label, tag, detail)
}

// printCaveats splits the skip count by cause. Three paths above increment it —
// no era adp, a draft that would not load, a draft Validate refused — and
// blaming all three on ffc's archive would send the reader off to recheck 2025
// when the real answer was the wifi or a non-snake draft.
func printCaveats(skipped, noEra int) {
	fmt.Println("\ncaveats — a number without its caveat is a lie")
	fmt.Println("  one draft. the twelve seats multiply vantages, not evidence: every seat scores")
	fmt.Println("  the same real picks, so they are correlated and the effective sample is far")
	fmt.Println("  smaller than the prediction count.")
	if noEra > 0 {
		fmt.Printf("  %d draft(s) contributed nothing: ffc's archive has no 2025 adp. they were\n", noEra)
		fmt.Println("  skipped rather than scored against another season's prices — an era mismatch")
		fmt.Println("  would move every number above without saying so. rechecking is one request:")
		fmt.Println("  do it once near draft day, in case 2025 has landed since.")
	}
	if other := skipped - noEra; other > 0 {
		fmt.Printf("  %d draft(s) never loaded or were refused — the skipped lines above say which\n", other)
		fmt.Println("  and why. that is a gap in the data, not a verdict on the model.")
	}
	fmt.Println("  the adp snapshot is the trailing window of that season's draft week, so it")
	fmt.Println("  already knows what the room knew — late injuries, camp news. read the numbers")
	fmt.Println("  as the optimistic case: the model running with final prices.")
}
