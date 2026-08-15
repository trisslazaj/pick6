package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The off-board escape's disk plumbing. The sim needs the measured probability
// that a pick leaves the ranked pool (engine.State.OffBoard), and measuring it
// takes each cached draft's picks against that draft's own ERA board — which
// means network (ffc's archive). So `fetch` measures and writes escape.json,
// and the draft-time commands load it disk-only, the same division of labour
// the rest of the board follows: a blocking http call at the clock is a blank
// screen at a draft party.

const escapeName = "escape.json"

// escapeRecord is one cached draft's counts, indexed by full rounds remaining
// after a pick's own round (0 = the final round). Counts rather than rates,
// so a loader can sum any subset — replay holds out the replayed draft and its
// future, exactly as the room curve does.
type escapeRecord struct {
	ID    string `json:"id"`
	Start int64  `json:"start"` // draft start, ms; 0 when sleeper carries none
	Tot   []int  `json:"tot"`
	Off   []int  `json:"off"`
}

// writeEscape measures every cached league draft against its own season's era
// board and writes the per-draft counts. Failures are notes, never errors: a
// board without escape rates still draws, the sim just runs escape-less — and
// says so at load time.
func writeEscape(ix *rankings.Index, format string) {
	dir, err := cache.Dir()
	if err != nil {
		return
	}
	boards := map[int]*eraBoard{}
	var recs []escapeRecord
	for _, id := range leagueDrafts {
		d, err := sleeper.CachedDraft(id)
		if err != nil {
			note("escape", "skipped", id+" · "+strings.ToLower(err.Error()))
			continue
		}
		picks, err := sleeper.CachedPicks(id)
		if err != nil {
			note("escape", "skipped", id+" · "+strings.ToLower(err.Error()))
			continue
		}
		year, _ := strconv.Atoi(d.Season)
		b, seen := boards[year]
		if !seen {
			b = loadEraBoard(ix, format, year, fpFor(0, "", year), adp.FPAvg, 0)
			boards[year] = b
		}
		if b.err != nil {
			note("escape", "skipped", fmt.Sprintf("%s · %s · no era board (%s)",
				id, d.Season, strings.ToLower(b.err.Error())))
			continue
		}
		member := make(map[string]bool, len(b.players))
		for _, pl := range b.players {
			member[pl.ID] = true
		}
		rec := escapeRecord{ID: id, Start: d.StartTime}
		for _, p := range picks {
			rem := d.Settings.Rounds - p.Round
			if rem < 0 {
				continue
			}
			for len(rec.Tot) <= rem {
				rec.Tot, rec.Off = append(rec.Tot, 0), append(rec.Off, 0)
			}
			rec.Tot[rem]++
			if !member[p.PlayerID] {
				rec.Off[rem]++
			}
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		note("escape", "off", "no cached draft could be measured — the sim will run escape-less")
		return
	}
	sortEscapeRecs(recs)
	buf, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(dir, escapeName)
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		note("escape", "error", strings.ToLower(err.Error()))
		return
	}
	rates := sumEscape(recs)
	note("escape", "measured", fmt.Sprintf("%d drafts · %s · wrote %s",
		len(recs), escapeSummary(rates), strings.ToLower(path)))
}

// loadEscape reads the measured rates for a live or mock board. replaying is
// the draft id being replayed, or "": a replayed draft is held out of its own
// rates along with everything that started after it — the same two rules the
// room curve applies, for the same reason, so the frame you eyeball agrees
// with the paper.
//
// nil on any failure or when nothing survives the hold-out; the engine reads
// nil as no escape, and the caller says which out loud.
func loadEscape(replaying string) []float64 {
	dir, err := cache.Dir()
	if err != nil {
		return nil
	}
	buf, err := os.ReadFile(filepath.Join(dir, escapeName))
	if err != nil {
		return nil
	}
	var recs []escapeRecord
	if err := json.Unmarshal(buf, &recs); err != nil {
		return nil
	}
	if replaying != "" {
		var cutoff int64
		found := false
		for _, r := range recs {
			if r.ID == replaying {
				cutoff, found = r.Start, true
			}
		}
		if found {
			kept := recs[:0]
			for _, r := range recs {
				// The replayed draft never prices itself; an undated record
				// (start 0) is unordered against it and held out too.
				if r.ID == replaying || r.Start == 0 || r.Start >= cutoff {
					continue
				}
				kept = append(kept, r)
			}
			recs = kept
		}
	}
	return sumEscape(recs)
}

// sumEscape pools per-draft counts into rates. nil when there is nothing.
func sumEscape(recs []escapeRecord) []float64 {
	var tot, off []int
	for _, r := range recs {
		for i := range r.Tot {
			for len(tot) <= i {
				tot, off = append(tot, 0), append(off, 0)
			}
			tot[i] += r.Tot[i]
			off[i] += r.Off[i]
		}
	}
	if len(tot) == 0 {
		return nil
	}
	rates := make([]float64, len(tot))
	for i, n := range tot {
		if n > 0 {
			rates[i] = float64(off[i]) / float64(n)
		}
	}
	return rates
}

// escapeSummary is the human line: the endgame rates, deepest first, where
// they actually live.
func escapeSummary(rates []float64) string {
	var parts []string
	for rem := 0; rem < len(rates) && rem < 8; rem++ {
		parts = append(parts, fmt.Sprintf("%d:%.0f%%", rem, rates[rem]*100))
	}
	return "p(pick leaves the ranked pool) by rounds remaining — " + strings.Join(parts, " ")
}

// applySim is the one place a draft-time command turns the sim on, so the
// default cannot drift between mock, board and live: survival mode from the
// shared -survival flag, the escape rates from disk, and the seed. Prints what
// it did — the sim is the board's default brain, and which brain is running
// with which escape is a fact the terminal should state once.
func applySim(s *engine.State, survival, replaying string, seed int64) error {
	return applyBrain(s, survival, engine.ScorerPair, replaying, seed)
}

// applyBrain is applySim plus the decision score, which milestone 8 made a
// second switch. The default is milestone 7's two-leg pair score; `-scorer
// roster` opts in to the finished-roster objective, which was built, graded
// against `pick6 regret` and did not clear its gate — and which the drafter
// then looked at and rejected outright. It stays in the tree as a documented
// negative result and as the smaller step back than turning the whole sim off,
// and it stays OFF.
func applyBrain(s *engine.State, survival, scorer, replaying string, seed int64) error {
	switch scorer {
	case "pair", "":
		s.Scorer = engine.ScorerPair
	case "roster":
		s.Scorer = engine.ScorerRoster
	default:
		return fmt.Errorf("-scorer must be pair or roster, not %q", scorer)
	}
	switch survival {
	case "adp":
		s.Survival = engine.SurvivalADP
		note("survival", "adp", "v1 logistic + tilt — the sim is off by flag")
		return nil
	case "sim":
	default:
		return fmt.Errorf("-survival must be sim or adp, not %q", survival)
	}
	s.Survival = engine.SurvivalSim
	s.SimSeed = seed
	s.OffBoard = loadEscape(replaying)
	if s.Scorer == engine.ScorerRoster {
		note("scorer", "roster", "milestone 8's finished-team objective — built, graded, and off by "+
			"default: it did not beat the pair score on either causal fold of `pick6 regret`")
	}
	if s.OffBoard == nil {
		// Two states share this nil and the advice differs: no file means fetch
		// hasn't measured, while a replay can hold every measured draft out —
		// which is correct, not fixable, and the same answer calibrate's fold
		// gets for that draft.
		why := "run `pick6 fetch` to measure off-board rates"
		if replaying != "" {
			why = "every measured draft is this one or came after it (or no escape.json yet)"
		}
		note("survival", "sim", "opponent rollouts, escape-less — "+why)
		return nil
	}
	note("survival", "sim", "opponent rollouts · escape "+escapeSummary(s.OffBoard))
	return nil
}

// survivalFlag declares -survival for mock, board and live in one place, same
// rule as roomFlag: the default cannot be flipped in one command and not the
// others.
func survivalFlag(fs *flag.FlagSet) *string {
	return fs.String("survival", "sim",
		"survival model: sim (opponent-aware rollouts, the default) or adp (the v1 logistic)")
}

// scorerFlag declares -scorer alongside -survival, same one-declaration rule.
func scorerFlag(fs *flag.FlagSet) *string {
	return fs.String("scorer", "pair",
		"decision score under -survival=sim: pair (milestone 7's two-leg score, the default) or roster (milestone 8's finished-team objective, which did not clear its gate)")
}

// sortEscapeRecs keeps escape.json diffable run to run.
func sortEscapeRecs(recs []escapeRecord) {
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
}
