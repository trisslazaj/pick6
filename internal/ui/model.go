package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
)

// Autopicker chooses the next player for a team that isn't us. Mock mode uses a
// scripted one; live mode will replace it with the Sleeper feed.
type Autopicker func(s *engine.State) (playerID string, ok bool)

// Model drives the board.
type Model struct {
	board Board
	pick  Autopicker

	auto     bool // auto-advance without keypresses
	interval time.Duration
	quit     bool
}

type tickMsg time.Time

// NewModel builds the Bubble Tea model.
func NewModel(s *engine.State, pick Autopicker, auto bool) Model {
	return Model{
		board:    Board{State: s, Width: 92, Height: 32, Synced: time.Now()},
		pick:     pick,
		auto:     auto,
		interval: 550 * time.Millisecond,
	}
}

// NewManualModel is `pick6 board`: the same board with nothing driving it. No
// feed, no autopicker — you find a player with `/` and mark him taken with `x`
// as the room calls names out, which is also the offline path for any draft
// sleeper cannot see.
//
// It reuses Model rather than getting its own because the difference is one
// field: a manual draft is a scripted draft whose script is a person. Every
// key that needs a picker checks for one.
func NewManualModel(s *engine.State) Model {
	m := NewModel(s, nil, false)
	m.board.Mode = ModeManual
	return m
}

// WithFreshness attaches how old the cached board is, for the footer's age
// clause. Mirrors LiveModel's, so both entry points wire it the same way.
func (m Model) WithFreshness(f Freshness) Model {
	m.board.Fresh = f
	return m
}

// WithNotes points the notes tab at a folder. Both models take it the same way.
func (m Model) WithNotes(dir string) Model {
	m.board.Notes.Dir = dir
	return m
}

// WithRankings points the data tab's file views at a folder of csvs, plus the
// file fetch loaded. Both models take it the same way.
func (m Model) WithRankings(dir, fetched string) Model {
	m.board.Views.Dir, m.board.Views.Fetched = dir, fetched
	return m
}

func (m Model) Init() tea.Cmd {
	if m.auto {
		return m.tick()
	}
	return nil
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case " ", "n", "right":
			if m.pick == nil {
				return m, nil // manual mode: the room advances the draft, not a key
			}
			m.step()
			return m, nil
		case "x":
			if m.board.Manual() {
				m.mark()
			}
			return m, nil
		case "a":
			if m.pick == nil {
				return m, nil
			}
			m.auto = !m.auto
			m.board.Status = "auto off"
			if m.auto {
				m.board.Status = "auto on"
				return m, m.tick()
			}
			return m, nil
		case "u":
			m.board.State.Undo()
			m.board.Status = "undid last pick"
			m.board.Synced = time.Now()
			return m, nil
		case "e":
			return m, m.board.EditNote()
		}
		return m, nil

	case editDoneMsg:
		m.board.Status = editStatus(msg)
		return m, nil

	case tickMsg:
		if !m.auto {
			return m, nil
		}
		m.step()
		if m.board.State.Done() {
			return m, nil
		}
		return m, m.tick()
	}
	return m, nil
}

// mark takes the selected player off the board on behalf of whoever is on the
// clock. Manual mode only — this is the one place in the tool where a human
// types a pick, and it says whose name it just spent so a mis-selected fuzzy
// match is visible immediately rather than three picks later.
func (m *Model) mark() {
	id := m.board.Selected
	if id == "" {
		m.board.Status = "nobody selected — / to find someone"
		return
	}
	p, ok := m.board.State.Players[id]
	if !ok {
		m.board.Status = "not on this board"
		return
	}
	if m.board.State.Taken[id] {
		m.board.Status = strings.ToLower(p.Name) + " is already gone"
		return
	}
	if m.board.State.Done() {
		m.board.Status = "draft complete"
		return
	}
	label := m.board.pickLabel(m.board.State.PickNo)
	m.board.State.Draft(id)
	m.board.Selected = ""
	m.board.Synced = time.Now()
	m.board.Status = label + " " + strings.ToLower(p.Name)
}

func (m *Model) step() {
	s := m.board.State
	if s.Done() {
		m.board.Status = "draft complete"
		return
	}
	id, ok := m.pick(s)
	if !ok {
		m.board.Status = "no players left"
		return
	}
	s.Draft(id)
	m.board.Synced = time.Now()
	m.board.Status = ""
}

// View returns the frame with NO trailing newline. bubbletea splits the view on
// "\n" and drops lines from the TOP when the result is taller than the terminal,
// and a trailing newline is an extra (empty) element — so a board that fills
// Height exactly, which is what the clamp in Board.View now guarantees, came out
// one element over and cost the header row. Measured: at height 24 the frame
// split into 25 and "round 4  pick 4.05 … 5 picks until yours" was never drawn.
func (m Model) View() string {
	if m.quit {
		return ""
	}
	return m.board.View()
}
