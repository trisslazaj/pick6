package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/ui"
)

func runMock(args []string) error {
	fs := flagSet("mock")
	teams := fs.Int("teams", 12, "league size")
	rounds := fs.Int("rounds", 15, "rounds")
	slot := fs.Int("slot", 3, "my draft slot, 1-indexed")
	seed := fs.Int64("seed", 6, "rng seed; same seed replays the same draft")
	auto := fs.Bool("auto", true, "auto-advance picks")
	snapshot := fs.Int("snapshot", -1, "advance N picks, print one frame, exit (no tui)")
	data := fs.Bool("data", false, "render the data tab instead of the board in snapshot mode")
	room := roomFlag(fs)
	width := fs.Int("width", 100, "terminal width for snapshot mode")
	height := fs.Int("height", 40, "terminal height for snapshot mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slot < 1 || *slot > *teams {
		return fmt.Errorf("slot %d is outside a %d-team league", *slot, *teams)
	}

	players, err := loadBoard(*room, "")
	if err != nil {
		return err
	}
	s := engine.New(players, *teams, *rounds, *slot)
	s.Demand = leagueDemand() // replacement level, from this room's own drafts
	pick := scriptedPicker(*seed)

	// Snapshot mode renders a single frame to stdout. Useful for eyeballing the
	// layout, for README screenshots, and for checking a run banner actually
	// fires at a given pick without driving the tui by hand.
	if *snapshot >= 0 {
		for i := 0; i < *snapshot && !s.Done(); i++ {
			id, ok := pick(s)
			if !ok {
				break
			}
			s.Draft(id)
		}
		b := ui.Board{State: s, Width: *width, Height: *height, Synced: time.Now(),
			Fresh: loadFreshness()}
		if *data {
			b.Tab = 1
		}
		fmt.Println(b.View())
		return nil
	}

	p := tea.NewProgram(
		ui.NewModel(s, pick, *auto).WithFreshness(loadFreshness()),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}

// roomFlag declares -room, which mock and live share and which is ON: survival
// prices the top adp.RoomWarpTopK players at each position against this league's
// own rank->pick curve. `-room=false` is the way back to raw national adp for
// the whole board — the only way, since Go's flag package reads a bare `-room
// false` as the flag followed by a positional argument, so the help text has to
// name the `=` form.
//
// One declaration for both commands so the default cannot be flipped in one
// place and not the other, and so there is a single thing for a test to assert.
func roomFlag(fs *flag.FlagSet) *bool {
	return fs.Bool("room", true,
		"price survival against this league's own draft history at the top of each position; -room=false for raw national adp")
}

// leagueDrafts is calibrateDrafts under the name the room warp cares about. Same
// three completed drafts, two different questions: the backtest scores them
// against era adp, the warp reads nothing but their pick order. One list on
// purpose — a second copy would drift the day a fourth draft happens.
var leagueDrafts = calibrateDrafts

// loadBoard reads the cached board written by `pick6 fetch`.
//
// room is on by default and turns on the room-warped effective adp: the survival
// model prices the top adp.RoomWarpTopK players at each position against a blend
// of national adp and where this league's own drafts actually took the k-th man
// there. `-room=false` puts the whole board back on raw national adp, which is
// also what happens on its own when no draft is cached. See roomWarp for the
// numbers behind the default and for how thin they are.
//
// replaying is the draft id being replayed, or "" live and in the mock. It is
// held out of the curve; see roomWarp.
func loadBoard(room bool, replaying string) (map[string]engine.Player, error) {
	dir, err := cache.Dir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "players.json"))
	if err != nil {
		return nil, fmt.Errorf("no board yet — run `pick6 fetch` first (%w)", err)
	}
	var list []*adp.Player
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := make(map[string]engine.Player, len(list))
	for _, p := range list {
		out[p.SleeperID] = engine.Player{
			ID:    p.SleeperID,
			Name:  p.Name,
			Pos:   p.Pos,
			Team:  p.Team,
			Bye:   p.Bye,
			ADP:   p.ADP,
			Sigma: p.Sigma,
			Value: p.Value,
			Tier:  p.Tier,

			Stdev:        p.Stdev,
			FormatSpread: p.FormatSpread,
			TierSrc:      string(p.TierSrc),

			TimesDrafted: p.TimesDrafted,
			High:         p.High,
			Low:          p.Low,

			// Injury state is as old as players.json — this is the only place it
			// enters the engine, and it entered players.json at fetch time. The
			// board can be honest about that because meta.json says when that was.
			InjuryStatus: p.InjuryStatus,
			Status:       p.Status,
			NewsUpdated:  p.NewsUpdated,
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("board is empty — run `pick6 fetch` first")
	}
	if room {
		roomWarp(out, list, replaying)
	}
	return out, nil
}

// roomWarp fills in engine.Player.ADPEff from this league's own draft history and
// says out loud what it did. Nothing else in the pipeline changes: ADPEff is a
// second field precisely so the display columns, Available's adp tie-break and
// the mock's picker keep reading the raw market price.
//
// ONLY THE TOP adp.RoomWarpTopK AT EACH POSITION, and that restriction is the
// whole reason this runs by default. Cross-validated on the 2024 draft with the
// curve built from the two 2025 drafts only, the top-of-position warp moves
// brier 0.0670 -> 0.0660 and log-loss 0.2250 -> 0.2222 against the previous
// default, with no position regressing at the four decimals calibrate prints:
// qb/rb/te/k/def improve on both metrics and wr improves on brier while its
// log-loss moves +0.000032. Warping the whole board instead LOSES (0.0671 /
// 0.2327) — a ranked list runs deeper than any finite draft, so past the room's
// appetite rank->room-pick prices players later than the market by construction.
// adp.RoomWarpTopK carries the cutoff sweep and the structural argument.
//
// One fold of two drafts is behind that, and it is thin: 2024 is the only season
// with era adp. It ships because no position and no nearby cutoff points the
// other way, not because the margin is large. `-room=false` is the way out and
// `pick6 calibrate` is the referee — its 3a gate recomputes that per-position
// claim on every run instead of trusting this paragraph.
//
// A failure to load is a note, not an error: a board on raw adp is still a board,
// so there is nothing to abort. The read is disk-only, and now that this is the
// default path that matters more than it did — a blocking http call here is a
// blank screen at a draft party. See adp.CachedRoomDrafts.
//
// replaying is held out of the curve. `live <id> -replay` over one of the three
// league drafts would otherwise price that draft's own survival numbers off a
// curve built partly from it, which is memorization wearing a backtest's clothes;
// `pick6 calibrate` excludes the scored draft for exactly this reason and the
// frame you eyeball afterwards has to agree with it. "" outside replay, where
// nothing to hold out is the normal case.
func roomWarp(out map[string]engine.Player, list []*adp.Player, replaying string) {
	drafts, sources := adp.CachedRoomDrafts(leagueDrafts)
	for _, s := range sources {
		if s.Err != nil {
			note("room", "skipped", fmt.Sprintf("%s · %s", s.ID, strings.ToLower(s.Err.Error())))
		}
	}
	var except []string
	if _, ours := drafts[replaying]; ours {
		except = append(except, replaying)
		note("room", "held out", replaying+" · replaying it, so it cannot price itself")
	}
	curve := adp.RoomCurveOf(drafts, except...)
	if curve.Empty() {
		note("room", "off", "no cached drafts loaded — board stays on raw adp")
		return
	}

	rows := make([]adp.RoomRow, 0, len(list))
	for _, p := range list {
		rows = append(rows, adp.RoomRow{ID: p.SleeperID, Pos: p.Pos, ADP: p.ADP})
	}
	eff := curve.EffectiveADPTopK(rows, adp.RoomWarpTopK)
	if len(eff) == 0 {
		note("room", "off", "the curve reaches nobody on this board")
		return
	}
	var moved float64
	for id, v := range eff {
		p, ok := out[id]
		if !ok {
			continue
		}
		moved += math.Abs(v - p.ADP)
		p.ADPEff = v
		out[id] = p
	}
	note("room", "warped", fmt.Sprintf(
		"top %d at each position repriced from %d drafts · %d of %d rows · moved %.1f picks each · -room=false for raw adp",
		adp.RoomWarpTopK, curve.Drafts, len(eff), len(list), moved/float64(len(eff))))
}

// loadFreshness reads meta.json for the footer's age clause and the live
// board's stale warning, flattening adp.Meta into the three fields the ui
// needs.
//
// A missing or unreadable file is deliberately not an error and not a log line:
// boards fetched before meta.json existed still draw fine, they just cannot say
// how old they are. The zero Freshness renders as nothing at all, which beats
// "adp 0h old" about a board from last week.
func loadFreshness() ui.Freshness {
	dir, err := cache.Dir()
	if err != nil {
		return ui.Freshness{}
	}
	m, err := adp.LoadMeta(dir)
	if err != nil {
		return ui.Freshness{}
	}
	f := ui.Freshness{FetchedAt: m.FetchedAt, Drafts: m.TotalDrafts}
	if end, ok := m.WindowEnd(); ok {
		f.WindowEnd = end
	}
	return f
}

// scriptedPicker drafts the way a room of humans roughly does: near the top of
// the remaining ADP order, with enough noise to produce real positional runs.
//
// The players are real (that's the point — this demos the actual board); only
// the pick *sequence* is synthetic. Seeded, so a given seed always replays the
// same draft, which is what makes it useful for demos and for milestone 4's
// "scripted RB run flips the banner" test.
//
// It drafts off RAW adp, never the room-warped ADPEff — which matters more now
// that the warp is on by default than it did when it was a flag. The warp is a
// claim about how a real room deviates from national adp; feeding it to the fake
// room would make the fake room deviate that way BY CONSTRUCTION, and every frame
// would then confirm the warp perfectly. A self-fulfilling prophecy is exactly
// the failure mode a mock is supposed to be immune to.
func scriptedPicker(seed int64) ui.Autopicker {
	rng := rand.New(rand.NewSource(seed))

	return func(s *engine.State) (string, bool) {
		type cand struct {
			id  string
			adp float64
		}
		var avail []cand
		for id, p := range s.Players {
			if s.Taken[id] {
				continue
			}
			// Nobody drafts a kicker in round 6, and if the mock does it the
			// board fills with noise. Matches the engine's own suppression.
			if (p.Pos == "K" || p.Pos == "DEF") && s.RoundsRemaining() > engine.KDefLastRounds {
				continue
			}
			a := p.ADP
			if a <= 0 {
				a = engine.UndraftedADP
			}
			avail = append(avail, cand{id, a})
		}
		if len(avail) == 0 {
			return "", false
		}
		sort.Slice(avail, func(i, j int) bool {
			if avail[i].adp != avail[j].adp {
				return avail[i].adp < avail[j].adp
			}
			return avail[i].id < avail[j].id // deterministic tie-break
		})

		// Exponential reach: usually the top of the board, occasionally a real
		// reach. This is what makes runs emerge instead of a tidy ADP walk.
		const pool = 12
		n := pool
		if len(avail) < n {
			n = len(avail)
		}
		idx := int(rng.ExpFloat64() * 2.2)
		if idx >= n {
			idx = n - 1
		}
		return avail[idx].id, true
	}
}
