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
	user := fs.String("user", "", "your sleeper username, to find your draft slot")
	slot := fs.Int("slot", 0, "your draft slot, if you'd rather say it directly")
	poll := fs.Int("poll", 3, "seconds between polls (sleeper asks for 2-3+)")
	replay := fs.Bool("replay", false, "load a finished draft once and print one frame (no tui)")
	room := roomFlag(fs)
	width := fs.Int("width", 92, "board width for replay mode")
	height := fs.Int("height", 40, "board height for replay mode")
	// Go's flag package stops parsing at the first positional argument, so
	// `live <id> -user x` would silently drop every flag. Pull a leading draft id
	// out first; both orders then work.
	var draftID string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		draftID, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if draftID == "" && fs.NArg() > 0 {
		draftID = fs.Arg(0)
	}
	if draftID == "" {
		return fmt.Errorf("usage: pick6 live <draft_id> [-user <name> | -slot N]")
	}

	draft, err := sleeper.GetDraft(draftID)
	if err != nil {
		return err
	}
	if err := draft.Validate(); err != nil {
		return err
	}

	mySlot, err := resolveSlot(draft, *user, *slot)
	if err != nil {
		return err
	}

	// The draft id doubles as the room curve's hold-out: -replay over one of this
	// league's own completed drafts must not price it off itself.
	players, err := loadBoard(*room, draftID)
	if err != nil {
		return err
	}

	s := engine.New(players, draft.Settings.Teams, draft.Settings.Rounds, mySlot)
	s.Demand = leagueDemand() // replacement level, from this room's own drafts
	// Prefer the league's real lineup over our assumed one — this league runs two
	// flex slots, which the default shape does not.
	if slots := draft.RosterSlots(); len(slots) > 0 {
		s.SetRoster(engine.Roster{Slots: slots, Bench: draft.Settings.SlotsBench})
	}

	fmt.Printf("draft %s — %d teams, %d rounds, %s\n",
		draftID, draft.Settings.Teams, draft.Settings.Rounds, draft.Status)
	// Lowercase like everything else the tool prints; team codes are the only
	// uppercase text anywhere, and a slot name is not one.
	fmt.Printf("your slot: %d   lineup: %s\n", mySlot,
		strings.ToLower(strings.Join(s.Roster.Slots, " ")))

	feed := sleeper.NewFeed(draftID)
	interval := *poll
	if interval < 2 {
		interval = 2 // Sleeper asks callers not to poll harder than this
	}

	// Replay loads a finished draft once and prints a single frame. No TUI, so it
	// works headless — which is how the whole live path gets exercised against a
	// real draft without waiting for one to be running.
	if *replay {
		snap, err := feed.Poll()
		if err != nil {
			return err
		}
		for _, p := range snap.Picks {
			if p.PlayerID == "" {
				continue
			}
			s.EnsurePlayer(engine.Player{
				ID:   p.PlayerID,
				Name: strings.TrimSpace(p.Metadata.FirstName + " " + p.Metadata.LastName),
				Pos:  p.Metadata.Position,
				Team: p.Metadata.Team,
			})
			if err := s.ApplyRemote(engine.RemotePick{
				PickNo: p.PickNo, Round: p.Round, Slot: p.DraftSlot,
				OwnerSlot: draft.OwnerSlot(p), PlayerID: p.PlayerID,
			}); err != nil {
				return fmt.Errorf("%w\n(this means our draft-order model disagrees with sleeper; "+
					"the board would be wrong, so it is not shown)", err)
			}
		}
		fmt.Printf("replayed %d picks\n\n", len(snap.Picks))
		b := ui.Board{State: s, Width: *width, Height: *height, Synced: time.Now(),
			Fresh: loadFreshness()}
		fmt.Println(b.View())
		return nil
	}

	p := tea.NewProgram(
		ui.NewLiveModel(s, feed, interval, false).
			WithDraft(draft).
			WithFreshness(loadFreshness()),
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
