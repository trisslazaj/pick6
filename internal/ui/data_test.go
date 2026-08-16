package ui

import (
	"reflect"
	"regexp"
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
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "your roster") {
		t.Error("tab twice more (through notes) should return to the board")
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

	for _, label := range []string{"pos", "player", "tm", "bye", "tier", "value", "adp", "spread", "surv", "fmt gap"} {
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
// bestNow→bestLater pair producing it, and a green safe for positions whose own
// best man will keep. bestLater is the modal best survivor now — rx3 is the man
// most likely to be the best back left, which is the question the arrow asks.
func TestDataViewUrgencyStrip(t *testing.T) {
	s := runState()
	for _, id := range append(append([]string{}, openingPicks...), runPicks...) {
		s.Draft(id) // the frame-B state: rb urgency 49, wr safe
	}
	b := Board{State: s, Width: 92, Height: 40, Tab: 1}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "rb 49 rb1→rx3") {
		t.Errorf("strip should show rb 49 with its bestNow→bestLater pair, got:\n%s", view)
	}
	// A safe position keeps its number: the tag says his own odds are fine, the
	// number says what the position still costs, and hiding it is what let the
	// two tabs rank positions differently.
	if !strings.Contains(view, "wr 0 safe") {
		t.Errorf("strip should mark wr safe and still show its number, got:\n%s", view)
	}
}

// The plan rides on the end of the urgency strip only when the row can pay for
// it, and dropping it has to be silent: this tab's whole job is the table below,
// so a strip that wrapped would cost a row of it, and the board tab is carrying
// the plan in full either way.
//
// The fixture's three entries are 45 cells and the plan is another 32 with its
// gap, against a budget of w-8 — so 80 columns cannot fit it and 92 can, which
// is the boundary this pins.
func TestDataStripDropsThePlanWhenItDoesNotFit(t *testing.T) {
	cases := []struct {
		w    int
		want bool
	}{
		{80, false},
		{92, true},
	}
	for _, c := range cases {
		b := Board{State: lookaheadState(), Width: c.w, Height: 40, Tab: 1}
		view := ansi.ReplaceAllString(b.View(), "")
		if got := strings.Contains(view, "plan  wr 1.02 → rb 2.03"); got != c.want {
			t.Errorf("width %d: plan on the strip = %v, want %v\n%s", c.w, got, c.want, view)
		}
	}
}

// stripOrder reads the positions off the urgency strip in the order it lists
// them, which is meant to be the order the board tab stacks its groups.
func stripOrder(view string) []string {
	for _, line := range strings.Split(view, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "urgency  ") {
			continue
		}
		l = strings.TrimPrefix(l, "urgency  ")
		// The plan rides on the end of the strip when it fits, separated by
		// spaces rather than the entries' "·" precisely because it is not one of
		// them. Cut it off, or a fixture wide enough to show it would quietly add
		// a phantom "position" to every order assertion below.
		if i := strings.Index(l, "   plan  "); i >= 0 {
			l = l[:i]
		}
		var out []string
		for _, e := range strings.Split(l, "·") {
			if f := strings.Fields(e); len(f) > 0 {
				out = append(out, f[0])
			}
		}
		return out
	}
	return nil
}

// The two tabs rank positions by DIFFERENT keys now, on purpose: the board by
// the primary key (worth to my roster, engine.PickChoices), the data tab's
// strip by urgency (value evaporating before my pick). They answer different
// questions and no longer promise the same order — what each promises
// individually is pinned instead. The board must render PickChoices' order
// exactly, market-dissent rows aside; the strip must stay urgency-ranked.
func TestBoardRendersThePrimaryKeysOrder(t *testing.T) {
	s := runState()
	for _, id := range openingPicks {
		s.Draft(id)
	}
	board := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40}.View(), "")
	data := ansi.ReplaceAllString(Board{State: s, Width: 92, Height: 40, Tab: 1}.View(), "")

	var want []string
	for _, c := range s.PickChoices() {
		want = append(want, strings.ToLower(c.Pos))
	}
	// A market-dissent row repeats its position's tag directly under the choice
	// row, so collapse consecutive repeats before comparing.
	var got []string
	for _, pos := range choiceOrder(board) {
		if len(got) == 0 || got[len(got)-1] != pos {
			got = append(got, pos)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("board tab order = %v, want the primary key's %v", got, want)
	}
	// wr 28.3, te 9.6 (safe), qb 3.6 (safe), rb 3.0 on urgency — unchanged.
	if got := stripOrder(data); !reflect.DeepEqual(got, []string{"wr", "te", "qb", "rb"}) {
		t.Errorf("data tab strip = %v, want the urgency order", got)
	}
}

// The strip reads "safe" from the same engine call the board tab does, so the
// two tabs cannot disagree. On my own pick every urgency is exactly zero by
// construction and nothing is safe (waiting isn't on offer), so the strip has
// nothing to list and says so instead of going all-green at the moment of
// choosing. A position whose tier is about to vanish reads its number, never
// the green tag.
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

	// The two-cliffs board: rb's last tier-1 man is going, so rb is cliffing
	// and must not read safe while the banner calls wr a cliff.
	b2 := Board{State: cliffState(), Width: 92, Height: 40, Tab: 1}
	view2 := ansi.ReplaceAllString(b2.View(), "")
	if strings.Contains(view2, "rb safe") {
		t.Errorf("a cliffing position must not read safe, got:\n%s", view2)
	}
	if !strings.Contains(view2, "wr 40") {
		t.Errorf("the urgent position should still show its number, got:\n%s", view2)
	}
}

// A banner must never wrap: a two-line banner makes the frame one taller than
// the terminal and bubbletea clips the header — during a run, at 80 columns,
// which is exactly when someone is looking.
func TestDataViewFitsTerminalHeight(t *testing.T) {
	for _, w := range []int{80, 92} {
		s := runState()
		for _, id := range append(append([]string{}, openingPicks...), runPicks...) {
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

// A roster row must keep the bye note on the player's own line at every
// width — an 18-char name once pushed "bye 11" onto its own row, which reads
// as a rendering fault in the pane you look at most.
func TestRosterByeStaysOnOneLine(t *testing.T) {
	players := map[string]engine.Player{
		"w1": {ID: "w1", Name: "fake longnamed-receiver", Pos: "WR", Team: "AAA",
			Value: 100, ADP: 3, Sigma: 2, Tier: 1, Bye: 11},
		"r1": {ID: "r1", Name: "fake back", Pos: "RB", Team: "AAA",
			Value: 90, ADP: 10, Sigma: 3, Tier: 1, Bye: 7},
	}
	digitsOnly := regexp.MustCompile(`^[0-9]+$`)
	for _, w := range []int{80, 92, 104, 140} {
		s := engine.New(players, 12, 15, 3)
		s.PickNo = 3 // my pick
		s.Draft("w1")
		b := Board{State: s, Width: w, Height: 40}
		for i, line := range strings.Split(ansi.ReplaceAllString(b.View(), ""), "\n") {
			// A wrapped sidebar row leaves an orphan fragment — a line that is
			// nothing but the spilled number ("11" from "bye 11").
			frag := strings.TrimSpace(strings.ReplaceAll(line, "│", " "))
			if frag != "" && digitsOnly.MatchString(frag) {
				t.Errorf("width %d: line %d is an orphaned wrap fragment %q", w, i, frag)
			}
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
