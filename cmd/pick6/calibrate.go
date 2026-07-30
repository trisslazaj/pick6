package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
//
// THREE DRAFTS, THREE LEAGUES — not one league across three seasons, which is
// what the docs used to imply and what the phrase "our league" still invites.
// Measured from the cached league user lists and reprinted by `leagueReport` on
// every run: the first 2025 draft shares 9 of 12 managers with 2024 and is this
// user's own room; the second shares 5 and belongs to somebody else. All three
// carry previous_league_id null, so nothing links them programmatically and the
// manager overlap is the only evidence there is.
//
// It matters here for two reasons. The folds are separate because they are
// separate rooms, not because they are separate years. And the room warp is one
// curve built from all three, so "this room drafts weird" is not the claim the
// data supports — "casual home leagues take quarterbacks and tight ends earlier
// than national adp" is.
var calibrateDrafts = []string{
	"1133489617308684288",
	"1261824503076360192",
	"1253161474382102529",
}

// fold is one scored draft: everything the report needs to grade it and to say
// what it was graded against.
//
// One fold per DRAFT, never per season, and never pooled. Two drafts share a
// board only if they share a season, and even then they are different rooms with
// different lineup shapes and different round counts — pooling their predictions
// would let one fold's tilt exponents be solved over another fold's board.
type fold struct {
	label    string // 2024, 2025-a, 2025-b — season plus order of appearance
	id       string
	season   string
	league   string // the draft's league name, lowercased at print time
	leagueID string
	draft    *sleeper.Draft
	picks    int
	start    int64               // draft start, ms since epoch; 0 when sleeper carries none
	feed     []sleeper.DraftPick // kept so a second curve can be re-walked, not re-fetched
	board    *eraBoard
	preds    []pred
	vantages int

	// The room curve's inputs, split by why each draft is in or out. Two rules
	// produce this split and only the first used to exist.
	//
	// Leave-one-out: the fold's own draft is never in its own curve. A warp built
	// from the draft it is scored on has memorized the answer.
	//
	// Time order: neither is any draft that started AFTER it. The live tool
	// standing at the clock can only have drafts that already happened, so a
	// curve built out of a fold's future measures a tool nobody can run — and
	// this is not symmetric noise, because the 2024 fold's entire pool is
	// posterior to it (measured starts: 2024-09-01, 2025-09-02, 2025-09-04), and
	// that fold is the only one that ever preferred a capped warp.
	allowed   []string // what the curve is actually built from, sorted
	posterior []string // held out for starting after this draft
	undated   []string // held out for carrying no start time, so order is unknowable
	lookahead bool     // -lookahead: posterior drafts put back in, for the retracted regime
	curve     adp.RoomCurve
}

// pool is every draft id the fold could see under its own rules, which is what
// the purity split resamples over. Never includes the fold itself.
func (f *fold) pool() []string { return f.allowed }

// runCalibrate scores the survival model against drafts that already happened.
//
// Every constant in tuning.go started as a guess. This replays real drafts from
// all twelve seats, asks the engine the same question it answers at the clock —
// "is he still there at my next pick?" — and grades every answer against what the
// room actually did. Nothing here writes anything: it prints numbers a human then
// decides to believe or not.
func runCalibrate(args []string) error {
	fs := flagSet("calibrate")
	format := fs.String("format", "half-ppr", "scoring format to score against; must match the drafts")
	verbose := fs.Bool("v", false, "list the era names with no exact sleeper match")
	tune := fs.Bool("tune", false, "grid-search the sigma constants and print the winner")
	fpSpec := fs.String("fp", "", "fantasypros overall adp csv for ONE season, as `year=path` or a path whose filename carries the year; default is the cache dir's fantasypros_adp_<year>.csv")
	fpCol := fs.String("fp-adp", string(adp.FPAvg), "which fantasypros column prices a fold it loads: avg (cross-platform consensus) or sleeper")
	depth := fs.Int("depth", 0, "truncate every era board to its n cheapest players before joining; 0 keeps the source's own depth")
	lookahead := fs.Bool("lookahead", false, "let each fold's room curve use drafts that started after it — the retracted regime, kept only to reproduce the numbers that justified the cutoff")
	if err := fs.Parse(args); err != nil {
		return err
	}
	col, err := adp.ParseFPColumn(*fpCol)
	if err != nil {
		return err
	}
	fpYear, fpPath, err := parseFPSpec(*fpSpec)
	if err != nil {
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

	boards := map[int]*eraBoard{} // one board per season, shared by drafts in it
	var folds []*fold
	var dropped []string
	skipped, noEra := 0, 0

	// 3a's inputs, read once. Every draft is loaded — including any with no era
	// adp to score against, which are useless to the survival backtest and
	// perfectly good here, since the room curve reads pick order only.
	roomDrafts, roomSources := adp.LoadRoomDrafts(calibrateDrafts)
	noteRoomSources(roomSources, *lookahead)
	// When each pool draft happened, which is what decides whether a fold is
	// allowed to see it. Read off the drafts themselves, never off the season
	// string: two of these drafts share a season and are two days apart.
	started := map[string]int64{}
	for _, s := range roomSources {
		if s.Err == nil {
			started[s.ID] = s.Start
		}
	}
	if fpPath != "" {
		note(fmt.Sprintf("csv %d", fpYear), "bound", fmt.Sprintf(
			"%s · used for the %d fold only, whatever other seasons need a board", fpPath, fpYear))
	}

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
			// The hand-exported board prices the ONE season it is bound to. It used
			// to price every season ffc could not serve, whatever era the file was:
			// a poisoned or missing 2024 cache entry plus the natural
			// `-fp ~/Downloads/FantasyPros_2025_...csv` scored the 2024 draft on
			// 2025 prices and still printed "era matched by season only".
			b = loadEraBoard(ix, *format, year, fpFor(fpYear, fpPath, year), col, *depth)
			boards[year] = b
			if b.err == nil {
				noteEraBoard(b, year, *verbose)
				for _, n := range b.dropped {
					// Tagged with the season, because two boards drop into one
					// list and "hollywood brown" appears on both of them.
					dropped = append(dropped, fmt.Sprintf("%d  %s", year, n))
				}
			}
		}
		if b.err != nil {
			// No era prices for that season. Scoring these picks against a
			// different year's board would move every number below without
			// saying so, and a silently wrong backtest is worse than a missing
			// one.
			note("draft", "skipped", fmt.Sprintf("%s · %s · %s",
				id, d.Season, strings.ToLower(b.err.Error())))
			skipped++
			noEra++
			continue
		}

		f := &fold{
			id:        id,
			season:    d.Season,
			league:    d.Metadata.Name,
			leagueID:  d.LeagueID,
			draft:     d,
			picks:     len(picks),
			start:     d.StartTime,
			feed:      picks,
			board:     b,
			lookahead: *lookahead,
		}
		// Leave-one-out AND time order, computed rather than hardcoded so both
		// stay correct however many drafts the list grows to, and asserted below
		// rather than assumed.
		splitPool(f, roomDrafts, started)
		f.curve = curveOf(roomDrafts, f.allowed)
		if err := checkCurve(roomDrafts, f); err != nil {
			return err
		}
		f.preds, f.vantages = walk(d, picks, b, f.curve)
		// idx is assigned once, per fold, over that fold's own slice: the tilt
		// hands its per-vantage answers back through this index, so pred.idx must
		// be the row's position in the slice being graded and nothing else.
		for i := range f.preds {
			f.preds[i].idx = i
		}
		folds = append(folds, f)
	}

	if *verbose && len(dropped) > 0 {
		fmt.Println("\ndropped — no exact sleeper match, never fuzzed:")
		sort.Strings(dropped)
		for _, n := range dropped {
			fmt.Printf("  %s\n", n)
		}
	}
	if len(folds) == 0 {
		return fmt.Errorf("nothing scorable — no draft had era adp")
	}
	labelFolds(folds)

	byID := map[string]*fold{}
	for _, f := range folds {
		byID[f.id] = f
	}

	managers := leagueReport(folds)
	for _, f := range folds {
		report(f, byID)
		if *tune {
			tuneFold(f)
		}
	}
	roomCutoffSweep(folds)
	purityReport(folds, roomDrafts, byID, managers)
	crossFold(folds)
	printCaveats(folds, skipped, noEra)
	return nil
}

// parseFPSpec binds a hand-exported board to the ONE season it prices.
//
// The flag used to be a bare path applied to every season ffc could not serve,
// and the file itself carries no year — so passing a 2025 export while ffc was
// missing 2024 scored the 2024 draft against 2025 prices, printed "era matched
// by season only", and gave a full model table off it. That is the era crossover
// the skipped-season path exists to prevent, arriving through the flag.
//
// `2025=/path/file.csv` is explicit. A bare path has to name its year in the
// filename — both the cache's `fantasypros_adp_2025.csv` and the export's own
// `FantasyPros_2025_Overall_ADP_Rankings.csv` do — and anything else is refused
// rather than guessed at.
func parseFPSpec(v string) (int, string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, "", nil
	}
	if year, path, ok := strings.Cut(v, "="); ok {
		y, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || y < 2000 || y > 2100 {
			return 0, "", fmt.Errorf("-fp %q: %q is not a season", v, year)
		}
		if strings.TrimSpace(path) == "" {
			return 0, "", fmt.Errorf("-fp %q: no path after the season", v)
		}
		return y, strings.TrimSpace(path), nil
	}
	seen := map[int]bool{}
	for _, run := range fpDigitsRe.FindAllString(filepath.Base(v), -1) {
		// Exactly four digits, in range, and a run rather than a substring: a
		// sleeper id is nineteen digits and must not offer up its first four.
		if len(run) != 4 {
			continue
		}
		if y, err := strconv.Atoi(run); err == nil && y >= 2000 && y <= 2100 {
			seen[y] = true
		}
	}
	if len(seen) != 1 {
		return 0, "", fmt.Errorf(
			"-fp %q: cannot tell which season this board prices from its filename — pass it as `year=path`, e.g. -fp 2025=%s", v, v)
	}
	for y := range seen {
		return y, v, nil
	}
	return 0, "", nil // unreachable: len(seen) == 1
}

// fpDigitsRe finds whole runs of digits. Word boundaries are the wrong tool here
// — `_` is a word character, so `\b\d{4}\b` never matches inside the cache's own
// `fantasypros_adp_2025.csv`.
var fpDigitsRe = regexp.MustCompile(`[0-9]+`)

// fpFor hands the flag's path to the season it was bound to and to no other.
func fpFor(boundYear int, path string, year int) string {
	if path == "" || boundYear != year {
		return ""
	}
	return path
}

// splitPool decides which drafts a fold's room curve is allowed to see.
//
// Two exclusions, and the second is the one that was missing. The fold's own
// draft is out because a curve built from the draft it is scored on has
// memorized the answer. Every draft that started AFTER it is out because the
// live tool standing at the clock cannot have it — a fold priced off its own
// future is measuring a tool nobody can run.
//
// That second rule is not a formality here. Measured start times: the 2024 draft
// ran 2024-09-01 and BOTH other drafts ran a year later, so under leave-one-out
// alone its curve was 100% posterior — and it is the only fold that ever
// preferred a capped warp, i.e. the sole source of RoomWarpTopK's upper bound.
// Under this rule it has no curve at all, which is the honest answer: no cached
// draft precedes it, so its room-warp verdict cannot be produced in the regime
// the tool actually runs in.
//
// A draft with no start time is held out too. Unknown order is not prior order,
// and guessing from the season string would put two drafts two days apart into
// the same instant.
func splitPool(f *fold, drafts map[string]adp.RoomDraft, started map[string]int64) {
	// The fold's own start comes off its draft metadata rather than off the room
	// loader's map: a draft can fail the room read, which needs its picks, and
	// still be perfectly scorable.
	f.allowed, f.posterior, f.undated = splitByStart(drafts, started, f.id, f.start)
	if f.lookahead {
		f.allowed = append(f.allowed, f.posterior...)
		sort.Strings(f.allowed)
	}
}

// splitByStart is the rule itself, shared with `live -replay` so the frame a
// human eyeballs and the numbers in the paper cannot come from different pools.
//
// prior is what a curve for `scored` may be built from: drafts that started
// before it. posterior is its future. undated is anything neither can be
// established for — unknown order is not prior order, and guessing would be the
// same look-ahead with a coin flip in front of it.
func splitByStart(drafts map[string]adp.RoomDraft, started map[string]int64, scored string, scoredStart int64) (prior, posterior, undated []string) {
	for id := range drafts {
		if id == scored {
			continue
		}
		switch {
		case started[id] == 0 || scoredStart == 0:
			undated = append(undated, id)
		case started[id] >= scoredStart:
			posterior = append(posterior, id)
		default:
			prior = append(prior, id)
		}
	}
	sort.Strings(prior)
	sort.Strings(posterior)
	sort.Strings(undated)
	return prior, posterior, undated
}

// curveOf builds a room curve from an explicit list of drafts, which is the
// same thing every caller here wants: the exclusions are decided once, by
// splitPool, and everything downstream reads the surviving list.
func curveOf(drafts map[string]adp.RoomDraft, ids []string) adp.RoomCurve {
	sub := make(map[string]adp.RoomDraft, len(ids))
	for _, id := range ids {
		if d, ok := drafts[id]; ok {
			sub[id] = d
		}
	}
	return adp.RoomCurveOf(sub)
}

// labelFolds names each fold by season, suffixed a/b/... when a season holds
// more than one draft — which 2025 does, and they are different leagues, so the
// season alone would name two folds the same thing. The suffix is order of
// appearance in calibrateDrafts, which is fixed, so two runs print the same
// labels and the paper can cite one.
func labelFolds(folds []*fold) {
	total := map[string]int{}
	for _, f := range folds {
		total[f.season]++
	}
	nth := map[string]int{}
	for _, f := range folds {
		nth[f.season]++
		if total[f.season] == 1 {
			f.label = f.season
			continue
		}
		f.label = fmt.Sprintf("%s-%c", f.season, 'a'+rune(nth[f.season]-1))
	}
}

// checkCurve is the exclusion assertion, in code rather than in a comment.
//
// Both failures it catches are invisible in the output — a curve that saw more
// than it should just makes the numbers better — so neither can be left to a
// comment. The scored draft must not be in its own curve. And unless -lookahead
// asks for the retracted regime, nothing that started after it may be either.
//
// The count is checked against splitPool's own split rather than against the
// pool size, so a draft that never loaded, or one the fold could not order
// itself against, cannot quietly pass for a held-out one.
func checkCurve(drafts map[string]adp.RoomDraft, f *fold) error {
	for _, id := range f.allowed {
		if id == f.id {
			return fmt.Errorf("room warp: %s is in its own curve", f.id)
		}
		if _, in := drafts[id]; !in {
			return fmt.Errorf("room warp: curve for %s names %s, which never loaded", f.id, id)
		}
	}
	if !f.lookahead {
		for _, id := range f.posterior {
			for _, in := range f.allowed {
				if in == id {
					return fmt.Errorf("room warp: curve for %s contains %s, which started after it", f.id, id)
				}
			}
		}
	}
	if f.curve.Drafts != len(f.allowed) {
		return fmt.Errorf("room warp: curve for %s carries %d drafts, want the %d it was allowed",
			f.id, f.curve.Drafts, len(f.allowed))
	}
	return nil
}

// eraBoard is one season's adp joined onto sleeper ids.
//
// Exact matches only. Index.Lookup would fall through to Levenshtein, and a name
// missing from the *current* active pool — retired, cut, out of the league —
// would map to a similarly-named active player whose id never appears in that
// season's picks. He would then be labeled "survived forever" at every single
// vantage, which is a bias, not a gap.
type eraBoard struct {
	players []engine.Player
	dropped []string
	// rows is the room warp's ranking input, and it covers EVERY entry on the era
	// board — including the ones the exact-only join dropped. Rank is by adp
	// within a position, so a missing name shifts every deeper player at that
	// position up by one and warps him with adp_room(P, k) when he is the k+1-th.
	// One name went missing on the 2024 board (hollywood brown, wr37), which
	// mispriced the 28 receivers behind him by 1.6 picks on average against a mean
	// warp of 4.4 — an uncontrolled error in the measurement the whole phase rests
	// on, and one the shipped roomWarp does not have, since it ranks over the
	// complete players.json.
	rows []adp.RoomRow
	res  adp.FFCResult
	// prior is that season's own stdev-against-adp line, fitted over the era
	// pool. Fitting it on the 2026 pool and scoring 2024 with it would grade the
	// shrink against a market it never saw — the same era mismatch a skipped
	// season exists to avoid. On a source with no stdev it fits over nothing and
	// the shrink degenerates to a no-op, which flatSigma below says out loud.
	prior adp.StdevPrior

	// Provenance, printed rather than inferred from the season number.
	source string // "ffc" or "fantasypros"
	path   string // where a hand-exported board was read from
	drafts int    // sample behind the prices, 0 when the source doesn't say
	// truncatedFrom is the board's original size when `-depth` cut it, 0 when it
	// is the source's own depth. Printed on every fold header, because a
	// truncated fold is a diagnostic and its numbers must never be read as the
	// fold's result.
	truncatedFrom int

	// flatSigma is set when NO entry on the board carried a stdev, so every
	// player falls back to SigmaDefault. It is not a silent fallback: it decides
	// what this fold is allowed to referee, and every report keyed off it says so.
	flatSigma bool

	// The second adp column, when the source has one. alt prices the same players
	// in the same order, so it can be ranked and warped independently and scored
	// as a model beside the primary. Empty for ffc, which has one adp per format.
	altCol       string
	alt          map[string]float64
	altRows      []adp.RoomRow
	altFallbacks int // rows where the ALT column was blank and avg stood in

	// col names the column the primary prices came from, for fantasypros boards.
	col       string
	fallbacks int // rows where col was blank and avg stood in

	err error
}

// loadEraBoard resolves one season's prices.
//
// FFC first, always: it carries stdev, times_drafted, high and low, so a season
// it covers can referee the whole model rather than the half of it that does not
// need sample support. A hand-exported fantasypros table is the fallback and
// exists for exactly one reason — ffc's archive has no 2025, and the 2025 draft
// is the most relevant one in the repo. If ffc ever grows that season, it wins
// here without anyone editing this function.
func loadEraBoard(ix *rankings.Index, format string, year int, fpPath string, col adp.FPColumn, depth int) *eraBoard {
	// teams is cosmetic to ffc (identical adp for 8/10/12/14) but it is part of
	// the url, so keep it at the league size the cache filename already implies.
	res, fetched, ffcErr := adp.FetchFFC(format, 12, year)
	if ffcErr == nil {
		say(fmt.Sprintf("ffc %d", year), fetched, fmt.Sprintf("%d players, %d drafts (%s)",
			len(res.Entries), res.TotalDrafts, res.Window))
		full := len(res.Entries)
		keep := keepCheapest(res.Entries, depth)
		res.Entries = pickEntries(res.Entries, keep)
		b := buildEraBoard(ix, res)
		b.source, b.drafts = "ffc", res.TotalDrafts
		b.truncatedFrom = truncatedFrom(full, len(res.Entries))
		return b
	}

	path := fpPath
	if path == "" {
		p, err := adp.FantasyProsPath(year)
		if err != nil {
			return &eraBoard{err: ffcErr}
		}
		path = p
	}
	file, err := adp.LoadFantasyPros(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &eraBoard{err: errors.New(adp.FantasyProsMissing(year, path))}
		}
		return &eraBoard{err: err}
	}
	note(fmt.Sprintf("csv %d", year), "loaded", fmt.Sprintf(
		"fantasypros · %d rows · pricing off the %s column · %d blank rows fell back to avg",
		file.Rows, col, file.Fallbacks[col]))
	note("", "path", path)

	// One keep-set, computed off the priced column and applied to every column.
	// Truncating each column by its own adp would leave the two boards holding
	// different players, and the loader's whole invariant is that they hold the
	// same ones in the same order — the room warp is indexed by rank within a
	// position, so a membership difference would silently rerank both.
	primary := file.Boards[col]
	full := len(primary.Entries)
	keep := keepCheapest(primary.Entries, depth)
	primary.Entries = pickEntries(primary.Entries, keep)

	b := buildEraBoard(ix, primary)
	b.source, b.path, b.col, b.fallbacks = "fantasypros", path, string(col), file.Fallbacks[col]
	b.truncatedFrom = truncatedFrom(full, len(primary.Entries))

	// The other column, carried alongside rather than instead: it prices the same
	// players in the same order, so scoring both answers whether sleeper's own adp
	// predicts a sleeper draft better than the consensus does.
	for _, other := range adp.FPColumns {
		if other == col {
			continue
		}
		res := file.Boards[other]
		res.Entries = pickEntries(res.Entries, keep)
		alt := buildEraBoard(ix, res)
		b.altCol, b.altRows, b.altFallbacks = string(other), alt.rows, file.Fallbacks[other]
		b.alt = make(map[string]float64, len(alt.players))
		for _, p := range alt.players {
			b.alt[p.ID] = p.ADP
		}
		break
	}
	return b
}

// keepCheapest picks the n entries with the smallest adp, returned as a sorted
// index list so the caller can apply the SAME set to a second column.
//
// It exists for one measurement. The room warp is indexed by rank within a
// position, and the two folds are priced off boards of very different depths —
// ffc publishes 178 names for 2024, the fantasypros export 389 for 2025 — so
// "the k-th receiver" is a different player on each. The warp's ordering
// disagrees across those folds, and depth is the leading suspect: `-depth 178`
// puts both folds on comparable boards and asks whether the disagreement
// survives. n <= 0, or n past the board, keeps everything, which is the default
// and the only regime any headline number is reported from.
func keepCheapest(entries []adp.Entry, n int) []int {
	idx := make([]int, len(entries))
	for i := range entries {
		idx[i] = i
	}
	if n <= 0 || n >= len(entries) {
		return idx
	}
	// Priced rows first, cheapest first; ties by original position so two runs
	// keep the same players. A row with no adp is not "expensive", it is
	// unpriced — it sorts last and is the first thing a cut removes.
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := entries[idx[a]].ADP, entries[idx[b]].ADP
		if (x > 0) != (y > 0) {
			return x > 0
		}
		return x < y
	})
	idx = idx[:n]
	sort.Ints(idx)
	return idx
}

func pickEntries(entries []adp.Entry, keep []int) []adp.Entry {
	if len(keep) == len(entries) {
		return entries
	}
	out := make([]adp.Entry, 0, len(keep))
	for _, i := range keep {
		out = append(out, entries[i])
	}
	return out
}

func truncatedFrom(full, kept int) int {
	if kept < full {
		return full
	}
	return 0
}

// buildEraBoard joins one board's entries onto sleeper ids and fits that pool's
// own stdev prior. Shared by both sources so the join, the drop list and the
// ranking rows cannot differ between them.
func buildEraBoard(ix *rankings.Index, res adp.FFCResult) *eraBoard {
	b := &eraBoard{res: res, flatSigma: true}
	adps := make([]float64, 0, len(res.Entries))
	stdevs := make([]float64, 0, len(res.Entries))
	for _, e := range res.Entries {
		if e.Stdev > 0 && e.ADP > 0 {
			adps = append(adps, e.ADP)
			stdevs = append(stdevs, e.Stdev)
			b.flatSigma = false
		}
	}
	b.prior = adp.FitStdevPrior(adps, stdevs)

	for _, e := range res.Entries {
		pos := rankings.NormalizePos(e.Pos)
		id, ok := ix.LookupExact(e.Name, e.Pos, e.Team)
		if !ok {
			// Lowercased here, not at print time: the team code is the one thing
			// that stays uppercase, and a blanket ToLower on the way out would
			// eat it along with the name.
			b.dropped = append(b.dropped, fmt.Sprintf("%s (%s/%s)",
				strings.ToLower(e.Name), strings.ToLower(e.Pos), e.Team))
			// He still occupied a rank on the board the room drafted from, so he
			// keeps one here. The id is deliberately not a sleeper id: nothing can
			// look him up, he only holds his place in the queue.
			b.rows = append(b.rows, adp.RoomRow{ID: "unmatched:" + e.Name, Pos: pos, ADP: e.ADP})
			continue
		}
		b.rows = append(b.rows, adp.RoomRow{ID: id, Pos: pos, ADP: e.ADP})
		b.players = append(b.players, engine.Player{
			ID:   id,
			Name: e.Name,
			Pos:  pos,
			Team: e.Team,
			ADP:  e.ADP,
			// The UNSHRUNK conversion, deliberately: this feeds the base row.
			// walk carries the shrunk sigma fetch actually writes alongside it,
			// so the two can be compared instead of one silently replacing the
			// other. With no stdev this is SigmaDefault for everyone, which is
			// what flatSigma above records.
			Sigma: adp.Sigma(e.Stdev),
			Stdev: e.Stdev,

			// The sample support behind that adp. It rides on the board rather
			// than being looked up again per row, and walk decides which models
			// get to see it.
			TimesDrafted: e.TimesDrafted,
			High:         e.High,
			Low:          e.Low,
		})
	}
	return b
}

// noteEraBoard prints what one season's board is and what it can referee.
func noteEraBoard(b *eraBoard, year int, verbose bool) {
	matched, total := len(b.players), len(b.players)+len(b.dropped)
	detail := fmt.Sprintf("%d/%d names matched (%.1f%%), %d dropped", matched, total,
		100*float64(matched)/float64(total), len(b.dropped))
	// A count with no way to reach the names is a dead end: a broken join would
	// read "148/178, 30 dropped" and stop there.
	if len(b.dropped) > 0 && !verbose {
		detail += " · run with -v to list them"
	}
	note(fmt.Sprintf("join %d", year), "exact", detail)

	if b.flatSigma {
		// Loud, because it decides what the fold is allowed to say. A silent
		// fallback here would let a reader take this fold's numbers as evidence
		// about constants it cannot see at all.
		note(fmt.Sprintf("sigma %d", year), "flat", fmt.Sprintf(
			"the source reports no stdev — every player runs on sigmadefault %.1f", engine.SigmaDefault))
		note("", "referees", "the tilt, ebest, the conditioning, the room warp, the need weights")
		note("", "cannot", "sigmamin, sigmamax, the shrink, the support floor — no per-player spread exists")
		return
	}
	// The shrink's prior, printed rather than trusted: it is a line fitted to
	// that season's own pool, and a slope near zero would mean the shrink is
	// pulling everyone toward one number.
	note(fmt.Sprintf("prior %d", year), "fitted", fmt.Sprintf(
		"stdev ~ %.2f + %.4f*adp (median %.1f, %.0f pseudo-drafts)",
		b.prior.Intercept, b.prior.Slope, b.prior.Median, b.prior.Pseudo))
}

// leagueReport is the fact the docs used to get wrong, printed from the data.
//
// The three cached drafts are three DIFFERENT leagues, not one league across
// three seasons: nothing links them programmatically (previous_league_id is null
// on all three), and the only evidence of who shares a room with whom is the
// manager overlap. It is printed because the room warp is built from all three
// at once, and a reader who believes they are one league will read that curve as
// a portrait of one room when it is a portrait of casual home leagues in general.
//
// Best effort: the user lists come off the same disk cache everything else here
// reads, and a league that never got fetched is reported as unknown rather than
// dropping the section.
// It returns the manager sets it read, keyed by fold label, because the purity
// question below is asked in exactly these units: "does a curve built from a
// room that shares nine managers with the scored one beat a curve built from a
// room that shares six".
func leagueReport(folds []*fold) map[string]map[string]bool {
	if len(folds) == 1 {
		fmt.Println("\nleague — the one fold that scored")
	} else {
		fmt.Printf("\nleagues — %d folds, and they are not one league across seasons\n", len(folds))
	}
	managers := map[string]map[string]bool{}
	for _, f := range folds {
		name := strings.ToLower(f.league)
		if name == "" {
			name = f.leagueID
		}
		users, err := sleeper.LeagueUsers(f.leagueID)
		if err != nil || len(users) == 0 {
			note(f.label, "no users", name+" · manager list not cached, so it is missing from the overlap")
			continue
		}
		set := map[string]bool{}
		for _, u := range users {
			set[u.UserID] = true
		}
		managers[f.label] = set
		note(f.label, "league", fmt.Sprintf("%s · %d managers", name, len(set)))
	}
	for i := 0; i < len(folds); i++ {
		for j := i + 1; j < len(folds); j++ {
			a, b := managers[folds[i].label], managers[folds[j].label]
			if a == nil || b == nil {
				continue
			}
			shared := 0
			for id := range a {
				if b[id] {
					shared++
				}
			}
			note("overlap", fmt.Sprintf("%d/%d", shared, len(a)),
				fmt.Sprintf("%s and %s share %d managers", folds[i].label, folds[j].label, shared))
		}
	}
	return managers
}

// sharedManagers counts the user ids two leagues have in common, and says so
// only when both lists were actually cached — an uncached league is unknown,
// never zero.
func sharedManagers(a, b map[string]bool) (int, bool) {
	if a == nil || b == nil {
		return 0, false
	}
	n := 0
	for id := range a {
		if b[id] {
			n++
		}
	}
	return n, true
}

// walk scores one draft from every seat.
//
// Twelve seats over the same picks is twelve times the labeled data for free —
// the snake math is seat-agnostic, so each seat is a different schedule of
// vantages over the same reality. It is not twelve times the *evidence* (same
// draft, correlated), which the caveats say out loud.
//
// Rounds come off the draft's own settings, never a constant: the 2024 draft is
// 15 rounds and the 2025 one is 16, and a hardcoded 15 would silently drop a
// whole round of vantages from the fold that matters most.
//
// curve is 3a's room warp, already built without this draft in it. It produces a
// second survival per row rather than replacing the first: the gate is a
// comparison, so both prices have to be on every prediction.
func walk(d *sleeper.Draft, picks []sleeper.DraftPick, b *eraBoard, curve adp.RoomCurve) (out []pred, vantages int) {
	board := b.players
	drafted := make(map[string]int, len(picks))
	for _, p := range picks {
		drafted[p.PlayerID] = p.PickNo
	}
	teams, rounds := d.Settings.Teams, d.Settings.Rounds

	// The warped price per player, over the era board's own position-adp ranks —
	// the 2024 board's fifth receiver, not 2026's. Uncovered players keep raw adp,
	// which is the same fallback engine.Player.price() applies.
	//
	// b.rows, not the joined players: the rank is a position on the board the room
	// drafted from, so a name the join dropped still has to occupy his. See
	// eraBoard.rows.
	rows := b.rows
	// The full-depth warp, graded and priced nowhere: it lost this gate, and the
	// only reason it is still computed is that the loser has to stay on the table
	// next to the winner. adp.EffectiveADP's doc names this as its one caller.
	eff := curve.EffectiveADP(rows)
	// The same warp restricted to the top of each position — the variant the board
	// ships, through the same adp.EffectiveADPTopK call `loadBoard` makes, so the
	// graded model and the priced one cannot drift apart. retractedCutoff carries
	// the cutoff sweep and how thin one fold of evidence is.
	effTop := curve.EffectiveADPTopK(rows, retractedCutoff)
	// And the same again over the alternate adp column, when the source has one.
	// Ranked on its own prices, because rank within a position is what adp_room
	// is indexed by and the two columns do not agree about it.
	effTopAlt := curve.EffectiveADP(b.altRows)

	for seat := 1; seat <= teams; seat++ {
		// The state is here for its snake math and its PickNo; PSurviveAt reads
		// nothing else, so the player map stays empty and the board is handed in
		// row by row with era adp and era sigma already on it.
		s := engine.New(nil, teams, rounds, seat)
		for r := 1; r < rounds; r++ {
			from, to := s.MyPick(r), s.MyPick(r+1)
			s.PickNo = from
			v := vantages
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
				// The warped row goes through the engine's own ADPEff reader rather
				// than through a local formula, so this grades the field that ships
				// and not a second implementation of it. adpRoom repeats price()'s
				// fallback for the shrunk-sigma variant, which needs the number
				// itself rather than a player carrying it.
				warped := pl
				warped.ADPEff = eff[pl.ID]
				adpRoom := pl.ADP
				if v, ok := eff[pl.ID]; ok {
					adpRoom = v
				}
				adpRoomTop := pl.ADP
				if v, ok := effTop[pl.ID]; ok {
					adpRoomTop = v
				}
				alt := b.alt[pl.ID]
				adpAltTop := alt
				if v, ok := effTopAlt[pl.ID]; ok {
					adpAltTop = v
				}
				out = append(out, pred{
					id: pl.ID, pos: pl.Pos, adp: pl.ADP, stdev: pl.Stdev,
					from: from, to: to, teams: teams,
					q: s.PSurviveAt(pl, to), y: y, vantage: v,

					// 4b's two inputs, per row: the shrunk sigma fetch now writes,
					// and the support the floor reads. The base q above is the
					// plain conditional logistic either way — the floor is not
					// wired into PSurviveAt, so the models that want it apply
					// engine.SupportFloor themselves and the comparison stays a
					// comparison. On a board with no stdev the shrink is a no-op
					// and all three sigmas are SigmaDefault, which is the point.
					sigmaShrunk: adp.Sigma(b.prior.Shrink(pl.Stdev, pl.ADP, pl.TimesDrafted)),
					prior:       b.prior.At(pl.ADP),
					high:        pl.High,
					drafts:      pl.TimesDrafted,

					// 3a: the room-warped price and the survival it produces.
					adpRoom:    adpRoom,
					adpRoomTop: adpRoomTop,
					qRoom:      s.PSurviveAt(warped, to),

					// The alternate adp column, warped the same way. Zero when the
					// source has only one column, which is what the column gate
					// checks before printing anything.
					adpAlt:    alt,
					adpAltTop: adpAltTop,
				})
			}
		}
	}
	return out, vantages
}

// eraTag / eraDetail measure the gap the spec assumed instead of asserting it.
// ffc serves the trailing snapshot of a season's draft week, and this user's
// 2024 draft ran inside that window — so for that backtest the prices really are
// the ones the room had, not a couple of weeks of drift. A hand-exported board
// carries no dates at all, and says so rather than guessing.
func eraTag(d *sleeper.Draft, b *eraBoard) string {
	if b.res.Window == "" {
		return "undated"
	}
	if d.StartTime == 0 {
		return "unknown"
	}
	day := time.UnixMilli(d.StartTime).Format("2006-01-02")
	start, end, ok := windowDays(b.res.Window)
	if !ok {
		return "unknown"
	}
	if day >= start && day <= end {
		return "match"
	}
	return "drift"
}

func eraDetail(d *sleeper.Draft, b *eraBoard) string {
	day := ""
	if d.StartTime != 0 {
		day = time.UnixMilli(d.StartTime).Format("2006-01-02")
	}
	if b.res.Window == "" {
		// Not a parse failure — the export has no date column at all. The prices
		// are that season's preseason board and the draft is that season's draft,
		// so the era is right; how many days apart they are is unknowable from
		// the file, and pretending otherwise would be the era check lying.
		if day == "" {
			return "the export carries no snapshot date and the draft no start time — era matched by season only"
		}
		return "draft ran " + day + "; the export carries no snapshot date — era matched by season only"
	}
	if day == "" {
		return "draft carries no start time; assume the adp snapshot postdates it"
	}
	start, end, ok := windowDays(b.res.Window)
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

// tuneFold runs the sigma grid on one fold, or says why it can't.
func tuneFold(f *fold) {
	if f.board.flatSigma {
		fmt.Printf("\ntune — skipped for %s: its board reports no stdev, so every grid point\n", f.label)
		fmt.Println("  scores identically and the sweep would print a winner it did not earn.")
		return
	}
	tuneSigma(f.preds, f.board.players)
}

// printCaveats says what the numbers above are and are not.
//
// The skip count is split by cause: three paths increment it — no era board, a
// draft that would not load, a draft Validate refused — and blaming all three on
// a missing archive would send the reader off to recheck ffc when the real answer
// was the wifi or a non-snake draft.
func printCaveats(folds []*fold, skipped, noEra int) {
	fmt.Println("\ncaveats — a number without its caveat is a lie")
	fmt.Printf("  %d fold(s). the twelve seats multiply vantages, not evidence: every seat scores\n", len(folds))
	fmt.Println("  the same real picks, so they are correlated and the effective sample per fold is")
	fmt.Printf("  far smaller than its prediction count. %d folds is %d draft nights, not %d\n",
		len(folds), len(folds), len(folds))
	fmt.Println("  independent seasons of the market.")

	var flat []string
	var fp []string
	for _, f := range folds {
		if f.board.flatSigma {
			flat = append(flat, f.label)
		}
		if f.board.source == "fantasypros" {
			fp = append(fp, f.label)
		}
	}
	if len(flat) > 0 {
		fmt.Printf("  %s carry no stdev, so every player there runs on sigmadefault %.1f. no\n",
			strings.Join(flat, "/"), engine.SigmaDefault)
		fmt.Println("  per-player spread exists to shrink or clamp. they can referee the tilt, ebest,")
		fmt.Println("  the conditioning, the room warp and the need weights; they cannot referee")
		fmt.Println("  sigmamin, sigmamax, the shrink or the support floor. the cross-fold table is")
		fmt.Println("  the only place folds are compared, and it forces every fold flat to do it.")
	}
	if len(fp) > 0 {
		fmt.Printf("  %s price off a hand-exported fantasypros board, not off ffc's. it is a\n",
			strings.Join(fp, "/"))
		fmt.Println("  consensus of three platforms rather than a sample of real drafts, it carries no")
		fmt.Println("  snapshot date, and it is a file a human downloaded — refetching it is a chore,")
		fmt.Println("  not a request. a missing file simply skips that fold.")
	}
	// The room warp's own sample, which is not the fold count and is smaller than
	// it looks. Printed because "three folds" reads as three votes on the cutoff,
	// and under the time-order rule it is at most two.
	var noCurve []string
	for _, f := range folds {
		if f.curve.Empty() {
			noCurve = append(noCurve, f.label)
		}
	}
	if len(noCurve) > 0 {
		fmt.Printf("  %s scored with no room curve at all: every cached draft started after it,\n",
			strings.Join(noCurve, "/"))
		fmt.Println("  so at its own clock there was nothing to build one from. it grades the tilt,")
		fmt.Println("  the shrink, sigma and the conditioning and says nothing about the warp. the")
		fmt.Println("  cutoff that ships was chosen on exactly that fold, from a curve made of its")
		fmt.Println("  own future — `-lookahead` reprints that regime and marks every line of it.")
	}
	if noEra > 0 {
		fmt.Printf("  %d draft(s) contributed nothing: no era board for their season. they were\n", noEra)
		fmt.Println("  skipped rather than scored against another season's prices — an era mismatch")
		fmt.Println("  would move every number above without saying so.")
	}
	if other := skipped - noEra; other > 0 {
		fmt.Printf("  %d draft(s) never loaded or were refused — the skipped lines above say which\n", other)
		fmt.Println("  and why. that is a gap in the data, not a verdict on the model.")
	}
	fmt.Println("  an ffc snapshot is the trailing window of that season's draft week, so it")
	fmt.Println("  already knows what the room knew — late injuries, camp news. read those numbers")
	fmt.Println("  as the optimistic case: the model running with final prices.")
	fmt.Println("  the tilt is scored at a vantage that stands on my own pick and looks to my")
	fmt.Println("  next, so its n counts my pick too. live the window opens on somebody else's")
	fmt.Println("  pick and my next one closes it, so n is picks-until-mine. same rule — every")
	fmt.Println("  pick inside the window — but the live horizon is one pick shorter than any")
	fmt.Println("  row here, which is the easy direction: shorter windows tilt less.")
}
