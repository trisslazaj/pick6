package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// LiveModel renders a real Sleeper draft, polling for picks.
type LiveModel struct {
	board    Board
	feed     sleeper.Feed
	draft    *sleeper.Draft // for resolving traded picks to their owning roster
	interval time.Duration
	once     bool // load one snapshot and stop (replay of a finished draft)

	applied  int    // picks already folded into state
	pollErr  string // last transport error, shown but not fatal
	desync   string // snake-math disagreement; sticky, because it invalidates the board
	complete bool
	quit     bool
}

type pollMsg struct {
	snap sleeper.Snapshot
	err  error
}

// NewLiveModel builds the live board.
func NewLiveModel(s *engine.State, feed sleeper.Feed, pollSeconds int, once bool) LiveModel {
	return LiveModel{
		board:    Board{State: s, Width: 92, Height: 32},
		feed:     feed,
		interval: time.Duration(pollSeconds) * time.Second,
		once:     once,
	}
}

func (m LiveModel) Init() tea.Cmd { return m.pollNow() }

func (m LiveModel) pollNow() tea.Cmd {
	feed := m.feed
	return func() tea.Msg {
		snap, err := feed.Poll()
		return pollMsg{snap: snap, err: err}
	}
}

func (m LiveModel) pollLater() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m LiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.board.Width, m.board.Height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.board.HandleKey(msg.String()) {
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case "r":
			m.board.Status = "refreshing"
			return m, m.pollNow()
		}
		return m, nil

	case tickMsg:
		return m, m.pollNow()

	case pollMsg:
		return m.handlePoll(msg)
	}
	return m, nil
}

func (m LiveModel) handlePoll(msg pollMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// A failed poll is normal on flaky wifi at a draft party. Keep the last
		// good board on screen, say so, and try again rather than dying.
		m.pollErr = msg.err.Error()
		m.board.Status = "poll failed, retrying"
		if m.once {
			return m, tea.Quit
		}
		return m, m.pollLater()
	}
	m.pollErr = ""

	applied := 0
	for _, p := range msg.snap.Picks {
		if p.PlayerID == "" {
			continue
		}
		m.board.State.EnsurePlayer(pickToPlayer(p))
		if err := m.board.State.ApplyRemote(engine.RemotePick{
			PickNo: p.PickNo, Round: p.Round, Slot: p.DraftSlot,
			OwnerSlot: m.ownerSlot(p), PlayerID: p.PlayerID,
		}); err != nil {
			// Our model of the draft order disagrees with Sleeper's. Every survival
			// number downstream would be wrong, so say so loudly and stop applying.
			m.desync = err.Error()
			break
		}
		applied++
	}

	if applied != m.applied {
		m.board.Synced = time.Now()
		m.applied = applied
	}
	m.board.Status = ""
	m.complete = msg.snap.Complete()

	if m.once || m.complete {
		if m.once {
			return m, tea.Quit
		}
		return m, nil // draft over: stop polling, leave the final board up
	}
	return m, m.pollLater()
}

func (m LiveModel) View() string {
	if m.quit {
		return ""
	}
	// Built before the board is drawn, because the board has to be charged for
	// it. Board.View() fills Height exactly; appending afterwards without paying
	// pushed the frame one row over the terminal and scrolled the header off the
	// top. That was permanent for the stale line, which is sticky by design —
	// a board past StaleADPHours warned about being old for the whole draft while
	// costing the reader the round and the pick number.
	trailer := m.trailer()
	m.board.Reserve = len(trailer) // m is a value; this is local to the frame

	out := m.board.View()
	for _, line := range trailer {
		out += "\n" + line
	}
	// No trailing newline, for the same reason Model.View has none: bubbletea
	// counts it as an extra line and clips the header to make room.
	return out
}

// trailer is every line drawn under the board, in priority order. One slice so
// the count and the content cannot disagree — a line added here that the height
// budget never saw is the bug this shape exists to prevent.
func (m LiveModel) trailer() []string {
	var out []string
	if m.desync != "" {
		out = append(out, Cliff.Bold(true).Render(
			"  desync — board is not trustworthy: "+strings.ToLower(m.desync)))
	}
	// Sticky, because staleness does not go away on its own the way a failed poll
	// does — it is a fact about the data until somebody refetches. Amber and not
	// red: the board is still the best information in the room, it is just older
	// than it should be, and blocking on it would be the one outcome worse than
	// showing it. Same principle as the poll loop keeping the last good frame up.
	if note, ok := m.board.Fresh.Stale(); ok {
		out = append(out, Run.Render("  "+trunc(note, m.board.Width-4)))
	}
	if m.pollErr != "" {
		out = append(out, Run.Render("  "+strings.ToLower(trunc(m.pollErr, 88))))
	}
	if m.complete {
		out = append(out, Wait.Render("  draft complete — polling stopped"))
	}
	return out
}

// Snapshot renders one frame without a TTY, for replaying a finished draft.
func (m LiveModel) Snapshot() string { return m.View() }

// pickToPlayer builds a minimal player from what the draft feed tells us, for
// anyone missing from our board.
func pickToPlayer(p sleeper.DraftPick) engine.Player {
	name := strings.TrimSpace(p.Metadata.FirstName + " " + p.Metadata.LastName)
	return engine.Player{
		ID:   p.PlayerID,
		Name: name,
		Pos:  p.Metadata.Position,
		Team: p.Metadata.Team,
	}
}

// WithDraft attaches draft metadata so traded picks resolve to the roster that
// actually receives the player.
func (m LiveModel) WithDraft(d *sleeper.Draft) LiveModel {
	m.draft = d
	return m
}

// WithFreshness attaches how old the cached board is, for the footer's age
// clause and the sticky stale warning. A zero value is the honest state of a
// board with no meta.json and renders as nothing.
func (m LiveModel) WithFreshness(f Freshness) LiveModel {
	m.board.Fresh = f
	return m
}

func (m LiveModel) ownerSlot(p sleeper.DraftPick) int {
	if m.draft == nil {
		return p.DraftSlot
	}
	return m.draft.OwnerSlot(p)
}
