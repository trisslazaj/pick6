package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	adpSrc := fs.String("adp", "sleeper", "which market prices the board: sleeper (fantasypros export, measured better) or ffc")
	fpPath := fs.String("fp", "", "fantasypros adp export; defaults to the cached fantasypros_adp_<year>.csv")
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

	// 5b. reprice off the market that actually drafts this league --------------
	// FFC stays the primary because it is the only feed that publishes a spread,
	// and stdev is what sigma, the shrinkage prior and the curve's whole width
	// are built from. But the price itself comes from sleeper where we have it:
	// scored against both real 2025 drafts, sleeper's own column beat the
	// cross-platform consensus by an order of magnitude more than any model
	// change in this repo. `-adp ffc` puts the board back on ffc's own price.
	if err := repriceFromSleeper(players, ix, *adpSrc, *fpPath, *year, *verbose); err != nil {
		return err
	}

	// Injury truth rides along from the dump. It is copied here rather than
	// inside Merge because Merge takes a rankings.Index, which deliberately
	// exposes only identity (name/pos/team/bye) — the ADP sources have no
	// opinion about anyone's health, and this is the one place that holds both
	// the merged board and the raw sleeper pool.
	flagged := 0
	for id, p := range players {
		sp, ok := pool[id]
		if !ok {
			continue
		}
		p.InjuryStatus, p.Status, p.NewsUpdated = sp.InjuryStatus, sp.Status, sp.NewsUpdated
		if adp.InjuryAlarm(p.InjuryStatus, p.Status) {
			flagged++
		}
	}

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
		if res.Opinions > 0 {
			detail += fmt.Sprintf(", %d opinions", res.Opinions)
		}
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
	fmt.Printf("tiers from %s: %s\n", tierOrigin, posSummary(tiered))

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

	// 7. write ---------------------------------------------------------------
	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	outPath := filepath.Join(dir, "players.json")
	if err := writePlayers(outPath, players); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s\n", strings.ToLower(outPath))

	// The freshness record, next to the board and never inside it. Everything
	// downstream is a photograph taken right here: adp, tiers, and injury status
	// all stop updating the moment this file is written, and the only defence is
	// knowing when that was.
	_, windowEnd, _ := windowDays(primary.Window)
	meta := adp.Meta{
		FetchedAt:    time.Now(),
		Format:       *format,
		Season:       *year,
		Players:      len(players),
		TotalDrafts:  primary.TotalDrafts,
		ADPWindowEnd: windowEnd,
		TiersFile:    *rankFile,
		TiersMod:     adp.ModTime(*rankFile),
		SleeperMod:   adp.ModTime(filepath.Join(dir, sleeper.PlayersCache)),
	}
	if err := adp.WriteMeta(dir, meta); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", strings.ToLower(filepath.Join(dir, adp.MetaName)))

	if err := writeMappingStub(dir, unmatched); err != nil {
		return err
	}
	printPreview(players)
	printKDefValues(players)
	printReplacement(players)
	printInjuryFlags(players, flagged)
	printTierDisagreements(players)
	printRoomCurve(players)
	return nil
}

func say(source string, fetched bool, detail string) {
	where := "cached"
	if fetched {
		where = "fetched"
	}
	note(source, where, detail)
}

// posSummary renders any per-position count in lineup order: "qb 8, rb 12".
// Zero counts are omitted, so a position nothing landed on stays quiet.
func posSummary(byPos map[string]int) string {
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

// sortedByADP is the order players.json is written in, so the tie-break is not
// cosmetic: the list comes out of a map, sort.Slice is not stable, and six
// players share an adp with somebody on the 2026 board. Without the id the same
// cache wrote a different file on every fetch and two runs could not be diffed.
// Same rule Available uses — adp asc, then id.
func sortedByADP(players map[string]*adp.Player) []*adp.Player {
	list := make([]*adp.Player, 0, len(players))
	for _, p := range players {
		list = append(list, p)
	}
	sortByADP(list)
	return list
}

func sortByADP(list []*adp.Player) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].ADP != list[j].ADP {
			return list[i].ADP < list[j].ADP
		}
		return list[i].SleeperID < list[j].SleeperID
	})
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

// injuryReportDepth is how deep the injury report looks, as an adp cutoff (105
// players today, since adp is a price and not a rank). Pick 100 ends round eight
// of a 12-team draft: the range where believing a stale adp costs you a starter
// rather than a bench flier.
const injuryReportDepth = 100

// printInjuryFlags lists board players carrying an injury or roster-status alarm
// who are still priced inside the top 100. Those are the traps, and the single
// worst failure this whole tool can have is recommending the man who tore his
// acl last night — adp is a trailing weekly average and has not heard the news.
//
// Expect this list to be SHORT — two names on 2026-07-29. That is the correct
// answer, not a broken query: six players inside the cutoff carry any injury
// note at all, and four of those are Questionable, which InjuryAlarm excludes on
// purpose. A busy-looking report would mean the alarm set had been widened until
// it stopped meaning anything.
//
// Every number here is frozen at fetch time, which is exactly why meta.json
// exists and why refetching on draft morning is the ritual.
func printInjuryFlags(players map[string]*adp.Player, flagged int) {
	var list, deeper []*adp.Player
	for _, p := range players {
		if p.ADP <= 0 || !adp.InjuryAlarm(p.InjuryStatus, p.Status) {
			continue
		}
		if p.ADP <= injuryReportDepth {
			list = append(list, p)
		} else {
			deeper = append(deeper, p)
		}
	}
	sortByADP(list)
	sortByADP(deeper)

	fmt.Printf("\ninjury flags — top %d adp (%d flagged across the whole board):\n",
		injuryReportDepth, flagged)
	if len(list) == 0 {
		fmt.Println("  none. nobody the room still drafts early is listed out, ir, pup, na or inactive.")
	}
	for _, p := range list {
		fmt.Printf("  %6.1f %-22s %-4s %-4s %-18s news %s\n",
			p.ADP, trunc(strings.ToLower(p.Name), 22), strings.ToLower(p.Pos), p.Team,
			adp.InjuryNote(p.InjuryStatus, p.Status), newsAge(p.NewsUpdated))
	}
	// Name the rest rather than leaving the count above unexplained. They are
	// outside the trap zone by price, not by importance — a round-10 tight end
	// on pup is still someone you want to have read about first.
	if len(deeper) > 0 {
		var names []string
		for i, p := range deeper {
			if i >= 6 {
				names = append(names, fmt.Sprintf("+%d more", len(deeper)-i))
				break
			}
			names = append(names, fmt.Sprintf("%s (%s, %.0f)",
				strings.ToLower(p.Name), strings.ToLower(p.Pos), p.ADP))
		}
		fmt.Printf("  also flagged, priced deeper than %d: %s\n",
			injuryReportDepth, strings.Join(names, ", "))
	}
	fmt.Println("  (questionable is not an alarm — 93 active players carry it in july)")
}

// newsAge renders how long ago sleeper last had something to say about a player.
func newsAge(ms int64) string {
	if ms <= 0 {
		return "never"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// printTierDisagreements shows where the tier board and the market rank a player
// most differently, within his own position.
//
// The chore this serves: re-transcribing the tier graphic in the week before the
// draft. A tier typed one row off is invisible on the board — it doesn't look
// wrong, it just silently sorts a player into the wrong group and quietly moves
// urgency with him. Disagreement with adp doesn't prove a typo, but a typo will
// almost always be in here, and so will the genuinely contrarian calls that are
// the reason you trusted the file in the first place.
func printTierDisagreements(players map[string]*adp.Player) {
	gaps := adp.TierADPGaps(players, adp.TierAdpGapFlag)
	fmt.Printf("\ntiers vs market — biggest rank disagreements within a position (>= %d places):\n",
		adp.TierAdpGapFlag)
	if len(gaps) == 0 {
		fmt.Println("  none. your tiers and the room agree everywhere, to within a round.")
		return
	}
	fmt.Printf("  %-4s %-22s %5s %8s %7s %7s  %s\n",
		"pos", "name", "tier", "by tier", "by adp", "delta", "tiers")
	for i, g := range gaps {
		if i >= 10 {
			break
		}
		p := g.Player
		fmt.Printf("  %-4s %-22s %5d %8d %7d %+7d  %s\n",
			strings.ToLower(p.Pos), trunc(strings.ToLower(p.Name), 22),
			p.Tier, g.TierRank, g.ADPRank, g.Delta, string(p.TierSrc))
	}
	// The top 10 skew to whichever position is deepest, because rank gaps have
	// more room to open up there — today that is receiver, twelve of fifteen.
	// The tail line stops a shallow position's one real disagreement from being
	// invisible just because it was crowded out.
	if len(gaps) > 10 {
		byPos := map[string]int{}
		for _, g := range gaps[10:] {
			byPos[g.Player.Pos]++
		}
		fmt.Printf("  %d more at or over %d places: %s\n",
			len(gaps)-10, adp.TierAdpGapFlag, posSummary(byPos))
	}
	fmt.Println("  negative delta = your tiers rank him ahead of the market; positive = the market does.")
	fmt.Println("  rows marked derived have no hand-typed tier to check — that gap is value vs adp.")
}

// roomCurveKs are the ranks the room table prints: the top of a position, where
// the fight actually is, plus one deep enough to show the curve flattening.
var roomCurveKs = []int{1, 2, 4, 8}

// printRoomCurve shows where this league takes the k-th player at a position
// against where the national market prices him, and caches the drafts it read.
//
// This is the human-readable face of the warp the board now prices. The signal
// is real and measured — over the first five at each position this room takes
// quarterbacks 17.4 picks and tight ends 13.4 picks earlier than the market
// prices them, and defenses 30.0 picks LATER, while backs and receivers track it
// to within a pick — and restricted to that same top-of-position range it wins
// the 2024 backtest's cross-validated gate, so mock and live blend it into
// survival unless you pass `-room=false` (see `pick6 calibrate`, the 3a gate).
// Deeper k are printed here and priced nowhere: past the room's appetite the
// curve measured worse than the market.
//
// A read like this is worth more than it looks: knowing the room takes tight ends
// early is a reason to move one up your own board, and that is a judgement the
// tool should hand over rather than make.
func printRoomCurve(players map[string]*adp.Player) {
	drafts, sources := adp.LoadRoomDrafts(leagueDrafts)
	var seasons []string
	picks := 0
	for _, s := range sources {
		if s.Err != nil {
			note("room", "skipped", fmt.Sprintf("%s · %s", s.ID, strings.ToLower(s.Err.Error())))
			continue
		}
		seasons = append(seasons, s.Season)
		picks += s.Picks
	}

	fmt.Printf("\nroom curve — where the k-th player at a position really goes in your league:\n")
	curve := adp.RoomCurveOf(drafts)
	if curve.Empty() {
		fmt.Println("  no cached drafts loaded. the ids live in cmd/pick6/calibrate.go.")
		return
	}
	note("drafts", "cached", fmt.Sprintf("%d drafts, %d picks · seasons %s",
		curve.Drafts, picks, strings.Join(seasons, ", ")))

	// The market side of every comparison: the adp of the k-th best player at the
	// position on the board fetch just built. Same rank, both sides — adp_room(WR, 5)
	// is where the fifth receiver went, so it can only be compared against the
	// fifth receiver's price.
	market := map[string][]float64{}
	for _, p := range players {
		if p.ADP > 0 && p.Pos != "" {
			market[p.Pos] = append(market[p.Pos], p.ADP)
		}
	}
	for _, list := range market {
		sort.Float64s(list)
	}

	fmt.Printf("  %-5s %5s %6s", "pos", "depth", fmt.Sprintf("gap%d", adp.RoomGapTopK))
	for _, k := range roomCurveKs {
		fmt.Printf(" %-14s", fmt.Sprintf("k%d", k))
	}
	fmt.Println()
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		depth := curve.Depth(pos)
		if depth == 0 {
			continue
		}
		// The gap is the mean of (room - market) over the TOP of the position only,
		// and the cutoff is load-bearing rather than tidy. Averaged over every k the
		// number inverts: national adp ranks more players at a position than a
		// 15-round draft ever takes, so past the room's appetite the curve is later
		// than the market by construction and the tail swamps the head. Over all k
		// quarterback reads +9.8 here; over the top five it reads -17.4, and the
		// second number is the one that matches what the room actually does — it is
		// the figure room.go's header quotes, and not to be confused with the 3a
		// gate's qb price SHIFT of +11.5, which is a different quantity with the
		// opposite sign. adp.RoomGapTopK carries the measurement behind the cutoff.
		var gap float64
		var n int
		for k := 1; k <= depth && k <= len(market[pos]) && k <= adp.RoomGapTopK; k++ {
			if room, _, ok := curve.At(pos, k); ok {
				gap += room - market[pos][k-1]
				n++
			}
		}
		if n > 0 {
			gap /= float64(n)
		}
		fmt.Printf("  %-5s %5d %+6.1f", strings.ToLower(pos), depth, gap)
		for _, k := range roomCurveKs {
			room, _, ok := curve.At(pos, k)
			if !ok || k > len(market[pos]) {
				fmt.Printf(" %-14s", "-")
				continue
			}
			fmt.Printf(" %-14s", fmt.Sprintf("%.1f / %.1f", room, market[pos][k-1]))
		}
		fmt.Println()
	}
	fmt.Println("  cells are room pick / market adp for the k-th player at the position.")
	fmt.Printf("  gap%d is the mean of (room - market) over the first %d: negative means this room\n",
		adp.RoomGapTopK, adp.RoomGapTopK)
	fmt.Println("  takes the position earlier than the national market prices it. deeper k are")
	fmt.Println("  shown but not averaged — a ranked list runs deeper than any finite draft, so")
	fmt.Println("  down there the room is later than adp about everyone and the mean flips sign.")
	fmt.Println("  depth is the deepest k the room ever reached; past it the curve says nothing.")
	fmt.Println("  survival on mock and live blends toward these numbers by default, at every depth")
	fmt.Println("  the curve reaches; -room=false puts the whole board back on raw national adp.")
	fmt.Printf("  it used to stop after the first %d of each position and that cap is gone — it lost\n",
		adp.RoomGapTopK)
	fmt.Println("  to full depth on both folds whose curve predates the draft it scores, and the one")
	fmt.Println("  fold that ever preferred it turned out to have no curve at all. `pick6 calibrate`")
	fmt.Println("  prints that gate; read it as thin either way.")
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

// repriceFromSleeper overlays sleeper's own adp onto the merged board.
//
// The export is the same FantasyPros file `pick6 calibrate` scores against, and
// it lives in the cache dir rather than the repo because it is FantasyPros'
// data, not ours. A missing file is not an error: the board simply keeps ffc's
// price and says so, which is exactly what `-adp ffc` asks for on purpose.
func repriceFromSleeper(players map[string]*adp.Player, ix *rankings.Index, src, path string, year int, verbose bool) error {
	col, err := adp.ParseFPColumn(src)
	if err != nil {
		if src == "ffc" {
			note("adp", "ffc", "board priced on ffc's own adp — sleeper's column not used")
			return nil
		}
		return fmt.Errorf("-adp: %w (use sleeper or ffc)", err)
	}
	if path == "" {
		dir, err := cache.Dir()
		if err != nil {
			return err
		}
		path = filepath.Join(dir, fmt.Sprintf("fantasypros_adp_%d.csv", year))
	}
	f, err := adp.LoadFantasyPros(path)
	if err != nil {
		note("adp", "ffc", fmt.Sprintf("no %d fantasypros export at %s — board stays on ffc's price",
			year, strings.ToLower(path)))
		return nil
	}
	board, ok := f.Boards[col]
	if !ok {
		note("adp", "ffc", fmt.Sprintf("export has no %s column — board stays on ffc's price", col))
		return nil
	}

	r := adp.OverlayADP(players, board, ix)
	// Sigma is derived from the raw stdev against a prior fitted on adp, and the
	// adp just moved out from under that fit. Stdev is untouched, so this is a
	// recompute rather than a second shrink.
	adp.ShrinkSigma(players)

	detail := fmt.Sprintf("%d of %d repriced from sleeper · moved %.1f picks each · %d kept ffc's price",
		r.Repriced, r.Repriced+r.Kept, r.MeanMove, r.Kept)
	if r.NoSpread > 0 {
		detail += fmt.Sprintf(" · %d with no stdev run on the default sigma", r.NoSpread)
	}
	note("adp", "sleeper", detail)
	if n := len(r.Unmatched); n > 0 {
		if verbose {
			for _, u := range r.Unmatched {
				fmt.Printf("  %-12s %-8s %s\n", "", "off board", strings.ToLower(u))
			}
		} else {
			note("", "", fmt.Sprintf("%d export rows are too deep for this board", n))
		}
	}
	return nil
}
