package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
)

// testState builds an obviously-fake board deep enough to finish a 12x15 draft
// (180 picks) without exhausting any position.
func testState() *engine.State {
	players := map[string]engine.Player{}
	for _, pos := range []string{"RB", "WR", "QB", "TE", "K", "DEF"} {
		for i := 0; i < 60; i++ {
			id := fmt.Sprintf("%s%02d", pos, i)
			players[id] = engine.Player{
				ID: id, Name: "fake " + strings.ToLower(id), Pos: pos, Team: "AAA",
				Tier: i/4 + 1, Value: 1000 - i*10, ADP: float64(i + 1), Bye: 7,
			}
		}
	}
	return engine.New(players, 12, 15, 3)
}

// picks the first available player, so the model tests don't depend on rng.
func firstAvailable(s *engine.State) (string, bool) {
	for _, pos := range []string{"RB", "WR", "QB", "TE", "K", "DEF"} {
		if a := s.Available(pos); len(a) > 0 {
			return a[0].ID, true
		}
	}
	return "", false
}

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func key(s string) tea.KeyMsg {
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestStepAdvancesTheDraft(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	if got := m.board.State.PickNo; got != 1 {
		t.Fatalf("starting pick = %d, want 1", got)
	}
	m = send(m, key(" "))
	if got := m.board.State.PickNo; got != 2 {
		t.Errorf("after step, pick = %d, want 2", got)
	}
	if len(m.board.State.Picks) != 1 {
		t.Errorf("expected 1 recorded pick, got %d", len(m.board.State.Picks))
	}
}

func TestUndoRestoresPreviousPick(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, key(" "))
	m = send(m, key(" "))
	m = send(m, key("u"))
	if got := m.board.State.PickNo; got != 2 {
		t.Errorf("after undo, pick = %d, want 2", got)
	}
	if !strings.Contains(m.board.Status, "undid") {
		t.Errorf("status = %q, want an undo note", m.board.Status)
	}
}

func TestAutoToggle(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, key("a"))
	if !m.auto || m.board.Status != "auto on" {
		t.Errorf("auto = %v status = %q, want on", m.auto, m.board.Status)
	}
	m = send(m, key("a"))
	if m.auto || m.board.Status != "auto off" {
		t.Errorf("auto = %v status = %q, want off", m.auto, m.board.Status)
	}
}

func TestQuitSetsFlag(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, key("q"))
	if !m.quit {
		t.Error("q should set the quit flag")
	}
	if m.View() != "" {
		t.Error("view should be empty once quitting, to avoid a stray frame")
	}
}

// A tick must not advance the draft while auto is off, or stepping manually
// would fight a stale timer.
func TestTickIsInertWhenAutoOff(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	before := m.board.State.PickNo
	m = send(m, tickMsg{})
	if m.board.State.PickNo != before {
		t.Errorf("tick advanced the draft with auto off: %d -> %d", before, m.board.State.PickNo)
	}
}

func TestWindowResizeIsApplied(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	if m.board.Width != 140 || m.board.Height != 40 {
		t.Errorf("size = %dx%d, want 140x40", m.board.Width, m.board.Height)
	}
}

// The view has to survive an empty draft and a completed one without panicking,
// since both are reachable states.
func TestViewRendersAtBothEnds(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	if out := m.View(); !strings.Contains(out, "best available") {
		t.Error("opening frame should show the board")
	}
	for i := 0; i < 12*15+5; i++ {
		m.step()
	}
	out := m.View()
	if !strings.Contains(out, "draft complete") {
		t.Errorf("final frame should say the draft is complete, got:\n%s", out)
	}
}

// Everything the tool prints is lowercase, team codes excepted. This is a
// deliberate look and easy to regress, so pin it.
func TestChromeIsLowercase(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	for i := 0; i < 8; i++ {
		m.step()
	}
	for _, want := range []string{"best available", "your roster", "recent picks", "round "} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("expected lowercase chrome %q in the view", want)
		}
	}
	for _, notWant := range []string{"Best Available", "Your Roster", "Recent Picks"} {
		if strings.Contains(m.View(), notWant) {
			t.Errorf("found capitalised chrome %q", notWant)
		}
	}
}
