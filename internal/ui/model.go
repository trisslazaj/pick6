package ui

import (
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
		board:    Board{State: s, Width: 100, Synced: time.Now()},
		pick:     pick,
		auto:     auto,
		interval: 550 * time.Millisecond,
	}
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
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case " ", "n", "right":
			m.step()
			return m, nil
		case "a":
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
			return m, nil
		}
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

func (m Model) View() string {
	if m.quit {
		return ""
	}
	return m.board.View() + "\n"
}
