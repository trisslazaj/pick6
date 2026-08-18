package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/sleeper"
	"github.com/trisslazaj/pick6/internal/ui"
)

func runLive(args []string) error {
	fs := flagSet("live")
	sportName := sportFlag(fs)
	user := fs.String("user", "", "your username (sleeper) or manager name (fpl), to find your draft slot")
	slot := fs.Int("slot", 0, "your draft slot, if you'd rather say it directly")
	poll := fs.Int("poll", 3, "seconds between polls (sleeper asks for 2-3+)")
	replay := fs.Bool("replay", false, "load a finished draft once and print one frame (no tui)")
	data := fs.Bool("data", false, "render the data tab instead of the board in replay mode")
	tab, notes := tabFlag(fs), notesFlag(fs)
	view, ranks := viewFlag(fs), rankingsFlag(fs)
	// Matching board's, and here for the same reason -data landed here: the
	// overlay's taken rows carry the pick that took a man, and a board with no
	// picks in it cannot show one. A replayed draft is the only headless frame
	// that can.
	search := fs.String("search", "", "with -replay: open the search overlay on this query")
	selected := fs.String("selected", "", "with -replay: select this query's best match, prompt closed")
	room := roomFlag(fs)
	survival, scorer := survivalFlag(fs), scorerFlag(fs)
	width := fs.Int("width", 92, "board width for replay mode")
	height := fs.Int("height", 40, "board height for replay mode")
	// Go's flag package stops parsing at the first positional argument, so a
	// draft id anywhere but the very front silently swallows every flag after
	// it. Pulling a LEADING id out covered `live <id> -user x` and nothing else:
	// `live -sport fpl 4250 -slot 3` parsed -sport, stopped dead at 4250, and
	// ran with -slot at its default while looking like it had worked.
	//
	// So: alternate. Take a positional, parse what follows, take the next
	// positional, until nothing is left. Every order works and no flag can be
	// eaten by an argument's position.
	var draftID string
	for rest := args; ; {
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			if draftID == "" {
				draftID = rest[0]
			}
			rest = rest[1:]
			continue
		}
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			break
		}
		rest = fs.Args()
	}
	if draftID == "" {
		return fmt.Errorf("usage: pick6 live <draft_id> [-user <name> | -slot N]\n" +
			"       pick6 live -sport fpl <league_id> -slot N")
	}

	sp, err := resolveSport(*sportName)
	if err != nil {
		return err
	}
	setup, err := liveSetup(sp, draftID, *user, *slot)
	if err != nil {
		return err
	}

	// The draft id doubles as the room curve's hold-out: -replay over one of this
	// league's own completed drafts must not price it off itself.
	players, err := loadBoard(sp, *room, draftID)
	if err != nil {
		return err
	}

	s := engine.New(players, setup.teams, setup.rounds, setup.mySlot)
	if sp.demand {
		s.Demand = leagueDemand() // replacement level, from this room's own drafts
	}
	// Prefer the league's real lineup over our assumed one — this league runs two
	// flex slots, which the default shape does not.
	s.SetRoster(setup.roster)
	// The draft id is the hold-out for the escape rates exactly as it is for the
	// room curve, and it seeds the rollouts so a re-render never jiggles.
	if err := applyBrain(s, sp, *survival, *scorer, draftID, simSeedOf(draftID)); err != nil {
		return err
	}

	fmt.Println(setup.header)
	// Lowercase like everything else the tool prints; team codes are the only
	// uppercase text anywhere, and a slot name is not one.
	fmt.Printf("your slot: %d   lineup: %s\n", setup.mySlot,
		strings.ToLower(strings.Join(s.Roster.Slots, " ")))

	feed := setup.feed
	draft := setup.draft
	interval := *poll
	if interval < 2 {
		interval = 2 // both apis ask callers not to poll harder than this
	}

	// Replay loads a finished draft once and prints a single frame. No TUI, so it
	// works headless — which is how the whole live path gets exercised against a
	// real draft without waiting for one to be running.
	if *replay {
		snap, err := feed.Poll()
		if err != nil {
			return err
		}
		// The same loop the live tui runs, not a copy of it — see ui.ApplySnapshot.
		applied, err := ui.ApplySnapshot(s, snap, ownerSlotFunc(draft))
		if err != nil {
			return fmt.Errorf("%w\n(this means our draft-order model disagrees with the feed; "+
				"the board would be wrong, so it is not shown)", err)
		}
		fmt.Printf("replayed %d picks\n\n", applied)
		// ModeLive, because a replay is the live board with the poll already
		// finished: the frame you eyeball has to advertise the keys the live
		// board binds, not the mock's.
		b := ui.Board{State: s, Width: *width, Height: *height, Synced: time.Now(),
			Mode: ui.ModeLive, Fresh: loadFreshness(sp), Notes: ui.Notes{Dir: notesDir(sp, *notes)},
			Views: ui.Views{Dir: rankingsDir(sp, *ranks), Fetched: fetchedRankings(sp)}}
		pickTab(&b, *tab, *data, *view)
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
		ui.NewLiveModel(s, feed, interval, false).
			WithDraft(draft).
			WithFreshness(loadFreshness(sp)).
			WithNotes(notesDir(sp, *notes)).
			WithRankings(rankingsDir(sp, *ranks), fetchedRankings(sp)),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}

// resolveSlot works out which seat is ours, by username or explicit flag.
func resolveSlot(d *sleeper.Draft, user string, slot int) (int, error) {
	if slot > 0 {
		if slot > d.Settings.Teams {
			return 0, fmt.Errorf("slot %d is outside a %d-team draft", slot, d.Settings.Teams)
		}
		return slot, nil
	}
	if user == "" {
		return 0, fmt.Errorf("need -user <sleeper username> or -slot N to know which seat is yours")
	}
	u, err := sleeper.GetUser(user)
	if err != nil {
		return 0, err
	}
	got, ok := d.SlotOf(u.UserID)
	if !ok {
		// Before a draft is seeded the order is empty; that's not a user error.
		if len(d.DraftOrder) == 0 {
			return 0, fmt.Errorf("this draft hasn't assigned slots yet — pass -slot N once it has")
		}
		return 0, fmt.Errorf("user %q (%s) isn't in this draft", user, u.UserID)
	}
	return got, nil
}
