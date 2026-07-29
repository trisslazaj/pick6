package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/trisslazaj/pick6/internal/engine"
)

func TestTabTogglesDataView(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "sorted by value") {
		t.Error("tab should switch to the data view")
	}
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "best available") {
		t.Error("tab again should switch back to the board")
	}
}

// Every column the sources feed us must be on screen and labeled — the data
// tab's whole contract. Dashes for numbers no source provided are part of it.
func TestDataViewShowsEveryColumn(t *testing.T) {
	players := map[string]engine.Player{
		"r1": {ID: "r1", Name: "fake starman", Pos: "RB", Team: "AAA", Bye: 9,
			ADP: 12.5, Sigma: 1.8, Stdev: 3.3, Value: 5000, Tier: 2,
			TierSrc: "rankings", FormatSpread: 6.5},
		"r2": {ID: "r2", Name: "fake gapman", Pos: "RB", Team: "BBB", Bye: 5,
			ADP: 40, Sigma: 4, Stdev: 7.2, Value: 900, Tier: 7, TierSrc: "derived"},
		"r3": {ID: "r3", Name: "fake nobody", Pos: "RB", Team: "CCC"},
	}
	s := engine.New(players, 12, 15, 3)
	b := Board{State: s, Width: 92, Height: 40, Tab: 1}
	view := ansi.ReplaceAllString(b.View(), "")

	for _, label := range []string{"pos", "player", "tm", "bye", "tier", "value", "adp", "sd", "surv", "sprd"} {
		if !strings.Contains(view, label) {
			t.Errorf("data view is missing the %q column label", label)
		}
	}
	for _, want := range []string{"fake starman", "12.5", "3.3", "5000", "6.5"} {
		if !strings.Contains(view, want) {
			t.Errorf("data view should show %q for the fully-known player", want)
		}
	}
	// Derived tiers carry their marker; rankings tiers don't.
	if !strings.Contains(view, "7d") {
		t.Error("derived tier should render with the d marker")
	}
	if strings.Contains(view, "2d") {
		t.Error("rankings tier must not carry the derived marker")
	}
	// The player no source knows renders dashes, not zeros.
	if !strings.Contains(view, "fake nobody") || strings.Contains(view, "  0.0") {
		t.Errorf("unknown numbers should be dashes, got:\n%s", view)
	}
}

func TestDataViewFilterCycles(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, key("tab"))
	m = send(m, key("p")) // all -> rb
	view := ansi.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "rb only") {
		t.Errorf("first p should filter to rb, got header %q", view[:120])
	}
	if strings.Contains(view, "fake wr") {
		t.Error("rb filter should hide receivers")
	}
	for i := 0; i < len(dataFilters)-1; i++ {
		m = send(m, key("p"))
	}
	if !strings.Contains(ansi.ReplaceAllString(m.View(), ""), "all players") {
		t.Error("cycling through every filter should come back to all")
	}
}

// Scrolling must clamp at both ends: k at the top stays put, j past the
// bottom pins to the last full page rather than running into blank space.
func TestDataViewScrollClamps(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m = send(m, tea.WindowSizeMsg{Width: 92, Height: 24})
	m = send(m, key("tab"))
	m = send(m, key("k"))
	if got := m.board.DataScroll; got != 0 {
		t.Errorf("k at the top scrolled to %d, want 0", got)
	}
	for i := 0; i < 10000; i++ {
		m = send(m, key("j"))
	}
	max := len(m.board.dataRows()) - m.board.visibleDataRows()
	if got := m.board.DataScroll; got != max {
		t.Errorf("scroll bottomed out at %d, want %d", got, max)
	}
}

// The urgency strip is the engine's summary on one line: the number, the
// bestNow→bestLater pair producing it, and a green safe for positions where
// waiting is free.
func TestDataViewUrgencyStrip(t *testing.T) {
	s := runState()
	for _, id := range []string{"wr1", "wr2", "qb1", "wr3", "rb2", "rb3", "wr4", "rb4", "rb5"} {
		s.Draft(id) // the frame-B state: rb urgency 45, wr safe
	}
	b := Board{State: s, Width: 92, Height: 40, Tab: 1}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "rb 45 rb1→rb6") {
		t.Errorf("strip should show rb 45 with its bestNow→bestLater pair, got:\n%s", view)
	}
	if !strings.Contains(view, "wr safe") {
		t.Errorf("strip should mark wr safe, got:\n%s", view)
	}
}

// The strip's "safe" obeys the board tab's guards. On my own pick every
// urgency is zero by construction, and an all-green strip at the moment of
// choosing would be nonsense — it says so instead. A cliff-last position
// never reads safe even at zero urgency.
func TestUrgencyStripGuards(t *testing.T) {
	s := runState()
	s.Draft("wr1")
	s.Draft("wr2") // pick 3 is mine: everyone survives, urgency 0 across the board
	b := Board{State: s, Width: 92, Height: 40, Tab: 1}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "your pick — urgency resets") {
		t.Errorf("on my pick the strip should say so, got:\n%s", view)
	}
	if strings.Contains(view, "safe") {
		t.Errorf("no position is 'safe' on my own pick, got:\n%s", view)
	}

	// The safe-cliff fixture: rb's last tier-1 man survives (urgency 0) but
	// the tier is cliffing — the strip must not call that safe while the
	// banner calls it a cliff.
	players := map[string]engine.Player{}
	add := func(id, pos string, value int, adp, sigma float64, tier int) {
		players[id] = engine.Player{ID: id, Name: "fake " + id, Pos: pos, Team: "AAA",
			Value: value, ADP: adp, Sigma: sigma, Tier: tier, Bye: 7}
	}
	add("ra", "RB", 100, 2, 2, 1)
	add("rb2", "RB", 90, 60, 8, 1)
	add("wa", "WR", 100, 2, 2, 1)
	add("wb2", "WR", 98, 1, 0.5, 1)
	add("wc", "WR", 40, 60, 8, 2)
	add("td1", "TE", 30, 80, 9, 1)
	s2 := engine.New(players, 12, 15, 3)
	s2.Draft("ra")
	s2.Draft("wa")
	s2.Draft("td1")
	b2 := Board{State: s2, Width: 92, Height: 40, Tab: 1}
	view2 := ansi.ReplaceAllString(b2.View(), "")
	if strings.Contains(view2, "rb safe") {
		t.Errorf("a cliffing position must not read safe, got:\n%s", view2)
	}
	if !strings.Contains(view2, "wr 58") {
		t.Errorf("the urgent position should still show its number, got:\n%s", view2)
	}
}

// A banner must never wrap: a two-line banner makes the frame one taller than
// the terminal and bubbletea clips the header — during a run, at 80 columns,
// which is exactly when someone is looking.
func TestDataViewFitsTerminalHeight(t *testing.T) {
	for _, w := range []int{80, 92} {
		s := runState()
		for _, id := range []string{"wr1", "wr2", "qb1", "wr3", "rb2", "rb3", "wr4", "rb4", "rb5"} {
			s.Draft(id) // run banner active
		}
		b := Board{State: s, Width: w, Height: 24, Tab: 1}
		view := ansi.ReplaceAllString(b.View(), "")
		if !strings.Contains(view, "rb run") {
			t.Fatalf("width %d: fixture should have the run banner up", w)
		}
		if lines := strings.Count(view, "\n") + 1; lines > 24 {
			t.Errorf("width %d: frame is %d lines for a 24-line terminal", w, lines)
		}
	}
}

// The data tab obeys the same law as the board: no line wider than the
// terminal, at any size.
func TestDataViewNoLineExceedsTerminalWidth(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{80, 24}, {92, 30}, {100, 40}, {140, 50}, {200, 60},
	} {
		m := NewModel(testState(), firstAvailable, false)
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for i := 0; i < 40; i++ {
			m.step()
		}
		m = send(m, key("tab"))
		for i, line := range strings.Split(m.View(), "\n") {
			if n := len([]rune(ansi.ReplaceAllString(line, ""))); n > size.w {
				t.Errorf("%dx%d: data line %d is %d runes, exceeds width", size.w, size.h, i, n)
			}
		}
	}
}
