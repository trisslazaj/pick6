package main

import (
	"flag"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/ui"
)

// `pick6 board` — the same war room with nothing driving it. Sleeper is not
// polled, no draft id is needed, and picks arrive the way they do in a room
// with a whiteboard: somebody says a name and you type it.
//
// It exists for the drafts sleeper cannot see — an in-person league, a platform
// with no api, a draft whose id you do not have — and it is the entry point
// phase 2's fpl mode will reuse, since a manual mark-taken feed is the only one
// that draft has.
//
// Everything downstream is identical. The engine never asks where a pick came
// from, so survival, urgency, tiers, runs and cliffs all read exactly as they
// do live; the only thing missing is the feed's cross-check on the snake math,
// which has nothing to disagree with when the picks are yours.
func runBoard(args []string) error {
	fs := flagSet("board")
	teams := fs.Int("teams", 12, "league size")
	rounds := fs.Int("rounds", 15, "rounds; defaults to lineup + bench when -lineup is given")
	slot := fs.Int("slot", 1, "my draft slot, 1-indexed")
	lineup := fs.String("lineup", "", "starting slots, e.g. \"qb rb rb wr wr te flex flex k def\"")
	bench := fs.Int("bench", 0, "bench spots; only read with -lineup")
	room := roomFlag(fs)
	survival, scorer := survivalFlag(fs), scorerFlag(fs)
	snapshot := fs.Bool("snapshot", false, "print one frame and exit (no tui)")
	search := fs.String("search", "", "with -snapshot: open the search overlay on this query")
	selected := fs.String("selected", "", "with -snapshot: select this query's best match, prompt closed")
	data := fs.Bool("data", false, "with -snapshot: render the data tab instead of the board")
	width := fs.Int("width", 100, "terminal width for snapshot mode")
	height := fs.Int("height", 40, "terminal height for snapshot mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slot < 1 || *slot > *teams {
		return fmt.Errorf("slot %d is outside a %d-team league", *slot, *teams)
	}

	// Live mode reads the real lineup out of sleeper's draft settings; there is
	// no draft here to read, so it has to be typed. It matters more than it
	// looks: need() drives urgency, the roster pane, MustFillStarters and the
	// endgame guard, and this user's 2025 league ran TWO flex slots against the
	// one in the default shape.
	roster := engine.DefaultRoster
	if *lineup != "" {
		slots, err := parseLineup(*lineup)
		if err != nil {
			return err
		}
		roster = engine.Roster{Slots: slots, Bench: *bench}
	} else if *bench != 0 {
		return fmt.Errorf("-bench only means something with -lineup")
	}
	// A lineup the draft is too short to fill is a board that spends the whole
	// endgame guard telling you about slots you were never going to reach, so
	// rounds follow the roster unless they were asked for explicitly.
	if !flagSet0(fs, "rounds") {
		*rounds = len(roster.Slots) + roster.Bench
	}

	players, err := loadBoard(*room, "")
	if err != nil {
		return err
	}
	s := engine.New(players, *teams, *rounds, *slot)
	s.SetRoster(roster)
	s.Demand = leagueDemand()
	if err := applyBrain(s, *survival, *scorer, "", 0); err != nil {
		return err
	}

	// Snapshot renders one frame to stdout, which is how the readme's pictures
	// get taken and how a layout change gets eyeballed without driving the tui
	// by hand. -search is here rather than in mock because the overlay is this
	// command's whole point and it is the one pane no scripted draft can reach.
	if *snapshot {
		b := ui.Board{State: s, Width: *width, Height: *height, Synced: time.Now(),
			Mode: ui.ModeManual, Fresh: loadFreshness()}
		if *data {
			b.Tab = 1
		}
		if *search != "" {
			b.Search = ui.Search{Open: true, Query: *search}
		}
		if *selected != "" {
			b.SelectBest(*selected)
		}
		fmt.Println(b.View())
		return nil
	}

	p := tea.NewProgram(
		ui.NewManualModel(s).WithFreshness(loadFreshness()),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}

// parseLineup turns "qb rb rb wr wr te flex flex k def" into engine slots, and
// refuses anything the engine cannot fill. A typo'd slot is worse than an error:
// nothing is eligible for it, so it stays unfilled forever, MustFillStarters
// fires for the rest of the draft and every need weight downstream is wrong.
func parseLineup(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Fields(strings.ToUpper(s)) {
		if !knownSlot(f) {
			return nil, fmt.Errorf("unknown lineup slot %q — use qb rb wr te k def flex superflex",
				strings.ToLower(f))
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-lineup is empty")
	}
	return out, nil
}

func knownSlot(s string) bool {
	switch s {
	case "QB", "RB", "WR", "TE", "K", "DEF", "FLEX", "SUPERFLEX":
		return true
	}
	return false
}

// flagSet0 reports whether a flag was given on the command line, as opposed to
// sitting at its default. The flag package only exposes this by walking the set.
func flagSet0(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}
