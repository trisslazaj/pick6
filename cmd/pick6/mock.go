package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
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
	width := fs.Int("width", 100, "terminal width for snapshot mode")
	height := fs.Int("height", 40, "terminal height for snapshot mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slot < 1 || *slot > *teams {
		return fmt.Errorf("slot %d is outside a %d-team league", *slot, *teams)
	}

	players, err := loadBoard()
	if err != nil {
		return err
	}
	s := engine.New(players, *teams, *rounds, *slot)
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
		b := ui.Board{State: s, Width: *width, Height: *height, Synced: time.Now()}
		if *data {
			b.Tab = 1
		}
		fmt.Println(b.View())
		return nil
	}

	p := tea.NewProgram(
		ui.NewModel(s, pick, *auto),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}

// loadBoard reads the cached board written by `pick6 fetch`.
func loadBoard() (map[string]engine.Player, error) {
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
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("board is empty — run `pick6 fetch` first")
	}
	return out, nil
}

// scriptedPicker drafts the way a room of humans roughly does: near the top of
// the remaining ADP order, with enough noise to produce real positional runs.
//
// The players are real (that's the point — this demos the actual board); only
// the pick *sequence* is synthetic. Seeded, so a given seed always replays the
// same draft, which is what makes it useful for demos and for milestone 4's
// "scripted RB run flips the banner" test.
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
