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

	// 3a's inputs, read once. Every draft is loaded — including the ones with no
	// era adp to score against, which are useless to the survival backtest and
	// perfectly good here, since the room curve reads pick order only.
	roomDrafts, roomSources := adp.LoadRoomDrafts(calibrateDrafts)
	noteRoomSources(roomSources)

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
				// The shrink's prior, printed rather than trusted: it is a line
				// fitted to that season's own pool, and a slope near zero would
				// mean the shrink is pulling everyone toward one number.
				note(fmt.Sprintf("prior %d", year), "fitted", fmt.Sprintf(
					"stdev ~ %.2f + %.4f*adp (median %.1f, %.0f pseudo-drafts)",
					b.prior.Intercept, b.prior.Slope, b.prior.Median, b.prior.Pseudo))
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

		// Leave-one-out, and it is the whole gate: a warp built from the draft it
		// is scored on has memorized the answer. Here that resolves to "the two
		// 2025 drafts", because 2024 is the only season with era adp — but the rule
		// is written as exclusion rather than as a hardcoded pair, so it stays
		// correct the day ffc's archive grows a 2025 board.
		curve := adp.RoomCurveOf(roomDrafts, id)
		note("room warp", roomWarpTag(curve), roomWarpDetail(curve, id))

		p, v := walk(d, picks, b, curve, vantages)
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
	// idx is assigned once, here, after every draft has appended: the tilt hands
	// its per-vantage answers back through this index, so pred.idx must be the
	// row's position in THIS slice and nothing else.
	for i := range preds {
		preds[i].idx = i
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
	// shrink against a market it never saw — the same era mismatch the skipped
	// 2025 drafts exist to avoid.
	prior adp.StdevPrior
	err   error
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
	adps := make([]float64, 0, len(res.Entries))
	stdevs := make([]float64, 0, len(res.Entries))
	for _, e := range res.Entries {
		if e.Stdev > 0 && e.ADP > 0 {
			adps = append(adps, e.ADP)
			stdevs = append(stdevs, e.Stdev)
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
			// other.
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

// walk scores one draft from every seat.
//
// Twelve seats over the same 180 picks is twelve times the labeled data for
// free — the snake math is seat-agnostic, so each seat is a different schedule
// of vantages over the same reality. It is not twelve times the *evidence*
// (same draft, correlated), which the caveats say out loud.
// vbase is where this draft's vantage numbering starts, so ids stay unique once
// a second draft has era adp to score against. The tilt is solved per vantage
// and two drafts sharing an id would pool two different boards into one solve.
//
// curve is 3a's room warp, already built without this draft in it. It produces a
// second survival per row rather than replacing the first: the gate is a
// comparison, so both prices have to be on every prediction.
func walk(d *sleeper.Draft, picks []sleeper.DraftPick, b *eraBoard, curve adp.RoomCurve, vbase int) (out []pred, vantages int) {
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
	// graded model and the priced one cannot drift apart. adp.RoomWarpTopK carries
	// the cutoff sweep and how thin one fold of evidence is.
	effTop := curve.EffectiveADPTopK(rows, adp.RoomWarpTopK)

	for seat := 1; seat <= teams; seat++ {
		// The state is here for its snake math and its PickNo; PSurviveAt reads
		// nothing else, so the player map stays empty and the board is handed in
		// row by row with era adp and era sigma already on it.
		s := engine.New(nil, teams, rounds, seat)
		for r := 1; r < rounds; r++ {
			from, to := s.MyPick(r), s.MyPick(r+1)
			s.PickNo = from
			v := vbase + vantages
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
				out = append(out, pred{
					pos: pl.Pos, adp: pl.ADP, stdev: pl.Stdev,
					from: from, to: to, teams: teams,
					q: s.PSurviveAt(pl, to), y: y, vantage: v,

					// 4b's two inputs, per row: the shrunk sigma fetch now writes,
					// and the support the floor reads. The base q above is the
					// plain conditional logistic either way — the floor is not
					// wired into PSurviveAt, so the models that want it apply
					// engine.SupportFloor themselves and the comparison stays a
					// comparison.
					sigmaShrunk: adp.Sigma(b.prior.Shrink(pl.Stdev, pl.ADP, pl.TimesDrafted)),
					prior:       b.prior.At(pl.ADP),
					high:        pl.High,
					drafts:      pl.TimesDrafted,

					// 3a: the room-warped price and the survival it produces.
					adpRoom:    adpRoom,
					adpRoomTop: adpRoomTop,
					qRoom:      s.PSurviveAt(warped, to),
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
	fmt.Println("  the tilt is scored at a vantage that stands on my own pick and looks to my")
	fmt.Println("  next, so its n counts my pick too. live the window opens on somebody else's")
	fmt.Println("  pick and my next one closes it, so n is picks-until-mine. same rule — every")
	fmt.Println("  pick inside the window — but the live horizon is one pick shorter than any")
	fmt.Println("  row here, which is the easy direction: shorter windows tilt less.")
}
