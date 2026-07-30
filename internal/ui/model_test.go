package ui

import (
	"fmt"
	"regexp"
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

// ansi strips escape sequences so we can measure real rendered width.
var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// The board must never emit a line wider than the terminal, at any size. A line
// that overflows wraps, and a wrapped row reads as a broken layout.
func TestNoLineExceedsTerminalWidth(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{80, 24}, {92, 30}, {100, 40}, {140, 50}, {200, 60},
	} {
		m := NewModel(testState(), firstAvailable, false)
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for i := 0; i < 40; i++ {
			m.step()
		}
		for i, line := range strings.Split(m.View(), "\n") {
			if n := len([]rune(ansi.ReplaceAllString(line, ""))); n > size.w {
				t.Errorf("%dx%d: line %d is %d runes, exceeds width", size.w, size.h, i, n)
			}
		}
	}
}

// Widening the terminal must not just add whitespace between the panes — that
// was the original complaint. Beyond MaxWidth the board caps and centres.
func TestWideTerminalCapsAndCentres(t *testing.T) {
	widths := []int{120, 160, 240}
	var gaps []int
	for _, w := range widths {
		m := NewModel(testState(), firstAvailable, false)
		m = send(m, tea.WindowSizeMsg{Width: w, Height: 40})
		for i := 0; i < 40; i++ {
			m.step()
		}
		// Measure the widest content line; it must stay bounded by MaxWidth
		// regardless of how wide the terminal gets.
		widest := 0
		for _, line := range strings.Split(m.View(), "\n") {
			plain := strings.TrimRight(ansi.ReplaceAllString(line, ""), " ")
			trimmed := strings.TrimLeft(plain, " ")
			if n := len([]rune(trimmed)); n > widest {
				widest = n
			}
		}
		if widest > MaxWidth {
			t.Errorf("width %d: content is %d runes, exceeds MaxWidth %d", w, widest, MaxWidth)
		}
		gaps = append(gaps, widest)
	}
	// Content width must be stable as the terminal grows, not proportional to it.
	if gaps[0] != gaps[len(gaps)-1] {
		t.Errorf("content width drifted with terminal size: %v", gaps)
	}
}

// More vertical space should buy more players, not more blank rows.
func TestTallerTerminalShowsMorePlayers(t *testing.T) {
	count := func(h int) int {
		m := NewModel(testState(), firstAvailable, false)
		m = send(m, tea.WindowSizeMsg{Width: 92, Height: h})
		for i := 0; i < 40; i++ {
			m.step()
		}
		return strings.Count(m.View(), "adp ")
	}
	short, tall := count(24), count(60)
	if tall <= short {
		t.Errorf("tall terminal showed %d players, short showed %d; expected more", tall, short)
	}
}

// What bubbletea actually receives, not what Board.View returns. The renderer
// splits the view on "\n" and, when the result has more elements than the
// terminal has rows, keeps only the LAST height of them — so a trailing newline
// is an extra element and it costs the first row: round, overall pick, who is on
// the clock, and how many picks until yours. Both models appended one, and the
// height clamp added in this milestone is exactly what made it bite, by filling
// Height to the row instead of coming in under it.
//
// The existing height tests all assert on Board{...}.View() directly, which is
// correct and cannot see this. This goes through the models.
func TestModelViewFitsTheTerminalExactly(t *testing.T) {
	for _, h := range []int{20, 24, 28, 30, 32, 40, 50} {
		m := NewModel(testState(), firstAvailable, false)
		m = send(m, tea.WindowSizeMsg{Width: 92, Height: h})
		for i := 0; i < 120; i++ {
			m.step()
		}
		if got := len(strings.Split(m.View(), "\n")); got > h {
			t.Errorf("mock model at height %d hands bubbletea %d lines; the header is clipped", h, got)
		}

		lm := liveModel(testState(), &fakeFeed{})
		lm.board.Width, lm.board.Height = 92, h
		lm.complete = true
		if got := len(strings.Split(lm.View(), "\n")); got > h {
			t.Errorf("live model at height %d hands bubbletea %d lines; the header is clipped", h, got)
		}
	}
}
