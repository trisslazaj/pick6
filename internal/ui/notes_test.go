package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
)

// notesDir writes a fixture folder and returns it.
func notesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTabCyclesThroughNotes(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false).WithNotes(notesDir(t, map[string]string{
		"global.md": "# always\n- never a qb early\n",
	}))
	m = send(m, key("tab"))
	m = send(m, key("tab"))
	view := ansi.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "draft map") || !strings.Contains(view, "never a qb early") {
		t.Errorf("second tab should land on the notes tab, got:\n%s", view)
	}
	m = send(m, key("tab"))
	if !strings.Contains(m.View(), "your roster") {
		t.Error("third tab should return to the board")
	}
}

// Pinned files render every time; the seat file opens itself; the rest are one
// keypress away in seat order.
func TestNotesPinsGlobalAndOpensTheSeatFile(t *testing.T) {
	dir := notesDir(t, map[string]string{
		"global.md":  "# rules\n- rule one\n",
		"slot-3.md":  "# from the three\n- the three plan\n",
		"slot-12.md": "# the wheel\n- the wheel plan\n",
		"queens.md":  "# queens\n- queens plan\n",
		"notes.txt":  "scratch\n",
		"README":     "not a note\n",
	})
	s := testState() // slot 3
	b := Board{State: s, Width: 100, Height: 40, Tab: 2, Notes: Notes{Dir: dir}}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "rule one") {
		t.Error("the pinned file should render without being selected")
	}
	if !strings.Contains(view, "the three plan") {
		t.Error("slot-3.md should open itself for the seat-3 drafter")
	}
	if strings.Contains(view, "the wheel plan") || strings.Contains(view, "queens plan") {
		t.Error("only the selected file renders under the pinned one")
	}
	if strings.Contains(view, "readme") {
		t.Error("files that aren't .md/.txt stay out of the strip")
	}
	// Right arrow moves to the next file in seat-then-name order: slot-12.
	b.HandleKey("right")
	view = ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "the wheel plan") || strings.Contains(view, "the three plan") {
		t.Errorf("→ should open slot-12.md next, got:\n%s", view)
	}
	if !strings.Contains(view, "rule one") {
		t.Error("the pinned file stays on screen after switching")
	}
	b.HandleKey("left")
	if !strings.Contains(ansi.ReplaceAllString(b.View(), ""), "the three plan") {
		t.Error("← should come back to slot-3.md")
	}
}

func TestNotesEmptyFolderSaysWhere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	b := Board{State: testState(), Width: 92, Height: 30, Tab: 2, Notes: Notes{Dir: dir}}
	view := ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "no notes yet") || !strings.Contains(view, "nope") {
		t.Errorf("an empty folder should say where to put the first file, got:\n%s", view)
	}
	if got := b.NotePath(); got != filepath.Join(dir, "global.md") {
		t.Errorf("e in an empty folder should start global.md, got %s", got)
	}
}

// Player names in prose are found — two-word names whole, unique last names
// alone, common-word surnames ("love") only with a first name — and a two-word
// name survives the word wrap in one piece.
func TestNotesFindsPlayersInProse(t *testing.T) {
	players := map[string]engine.Player{
		"w1": {ID: "w1", Name: "Fake Nabers", Pos: "WR", Value: 100, ADP: 1},
		"r1": {ID: "r1", Name: "Fake Love", Pos: "RB", Value: 90, ADP: 2},
		"r2": {ID: "r2", Name: "Other Back", Pos: "RB", Value: 80, ADP: 3},
		"r3": {ID: "r3", Name: "Third Back", Pos: "RB", Value: 70, ADP: 4},
	}
	s := engine.New(players, 12, 15, 3)
	b := Board{State: s}
	ix := b.nameIndex()

	got := map[string]string{}
	for _, tok := range noteTokens("i love this pick: fake love. nabers if he lasts, plus other back and a wr", ix) {
		switch {
		case tok.player != nil:
			got[tok.text] = tok.player.ID
		case tok.pos != "":
			got[tok.text] = "pos:" + tok.pos
		}
	}
	want := map[string]string{
		"fake love":  "r1", // full name, even with a common-word surname
		"nabers":     "w1", // unique last name alone
		"other back": "r2", // full name wins over an ambiguous last name
		"wr":         "pos:WR",
	}
	for text, id := range want {
		if got[text] != id {
			t.Errorf("%q → %q, want %q (all: %v)", text, got[text], id, got)
		}
	}
	if _, bad := got["love"]; bad {
		t.Error("\"love\" as a word must not be jeremiyah love")
	}
	if _, bad := got["back"]; bad {
		t.Error("\"back\" is two men on this board and must stay prose")
	}

	// The strike is decided by Taken, and it is the tab's one live signal.
	s.Draft("w1")
	if b.noteName(players["w1"]).GetStrikethrough() != true {
		t.Error("a taken player renders struck through")
	}
	if b.noteName(players["r1"]).GetStrikethrough() {
		t.Error("an available player does not")
	}

	// Wrap: a narrow pane splits the line, and a two-word name must not split.
	dir := notesDir(t, map[string]string{
		"global.md": "- some words to push the name toward the edge fake nabers and then more\n",
	})
	for w := 60; w <= 100; w++ {
		b := Board{State: s, Width: w, Height: 40, Tab: 2, Notes: Notes{Dir: dir}}
		plain := ansi.ReplaceAllString(b.View(), "")
		for _, line := range strings.Split(plain, "\n") {
			left := strings.TrimRight(strings.SplitN(line, "│", 2)[0], " ")
			if strings.HasSuffix(left, " fake") {
				t.Fatalf("width %d: a two-word name broke across a wrap:\n%s", w, plain)
			}
		}
	}
}

// The draft map's cell arithmetic is the snake: for my own seat it must name
// the same picks State.MyPick does, in every round.
func TestDraftMapPickNumbersAreTheSnake(t *testing.T) {
	s := testState()
	idx := 0
	for i, slot := range s.Order {
		if slot == s.MySlot {
			idx = i
		}
	}
	for r := 1; r <= s.Rounds; r++ {
		if got, want := mapPickNo(s, r, idx), s.MyPick(r); got != want {
			t.Errorf("round %d: map pick %d, MyPick %d", r, got, want)
		}
	}
	// And every pick number appears exactly once across the grid.
	seen := map[int]bool{}
	for r := 1; r <= s.Rounds; r++ {
		for i := range s.Order {
			p := mapPickNo(s, r, i)
			if seen[p] {
				t.Errorf("pick %d appears twice", p)
			}
			seen[p] = true
		}
	}
	if len(seen) != s.Teams*s.Rounds {
		t.Errorf("grid covers %d picks, want %d", len(seen), s.Teams*s.Rounds)
	}
}

func TestDraftMapShowsPicksAndTheClock(t *testing.T) {
	s := testState()
	for i := 0; i < 5; i++ {
		id, _ := firstAvailable(s)
		s.Draft(id)
	}
	b := Board{State: s, Width: 100, Height: 40, Tab: 2, Notes: Notes{Dir: t.TempDir()}}
	view := ansi.ReplaceAllString(b.View(), "")
	r1 := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, " r1 ") {
			r1 = line
		}
	}
	if r1 == "" {
		t.Fatalf("no r1 row in the map:\n%s", view)
	}
	if strings.Count(r1, "rb") != 5 || !strings.Contains(r1, "▸▸") {
		t.Errorf("round one should show five backs then the clock, got %q", r1)
	}
	if !strings.Contains(view, "gone  rb 5") {
		t.Errorf("tally should read the five backs, got:\n%s", view)
	}
	if !strings.Contains(view, "yours 3 22 27") {
		t.Errorf("my picks should be listed for seat 3, got:\n%s", view)
	}
}

// Same law as the other tabs: no line wider than the terminal, and the frame
// fits the terminal's height, at every size.
func TestNotesTabFitsTheTerminal(t *testing.T) {
	dir := notesDir(t, map[string]string{
		"global.md": strings.Repeat("- a fairly long note line about a receiver who might fall to the third round\n", 30),
		"slot-3.md": "# three\n" + strings.Repeat("- more\n", 20),
	})
	for _, size := range []struct{ w, h int }{
		{80, 24}, {92, 30}, {100, 40}, {140, 50}, {200, 60},
	} {
		m := NewModel(testState(), firstAvailable, false).WithNotes(dir)
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for i := 0; i < 40; i++ {
			m.step()
		}
		m = send(m, key("tab"))
		m = send(m, key("tab"))
		view := m.View()
		if lines := strings.Count(view, "\n") + 1; lines > size.h {
			t.Errorf("%dx%d: notes frame is %d lines", size.w, size.h, lines)
		}
		for i, line := range strings.Split(view, "\n") {
			if n := len([]rune(ansi.ReplaceAllString(line, ""))); n > size.w {
				t.Errorf("%dx%d: notes line %d is %d runes, exceeds width", size.w, size.h, i, n)
			}
		}
	}
}

// A word wider than the line is cut by cells: an emoji is one rune and two
// cells, and cutting by rune count walked off the slice.
func TestNotesWrapSurvivesWideRunes(t *testing.T) {
	for _, w := range []int{1, 3, 12, 40} {
		for _, in := range []string{"🔥🔥🔥🔥🔥🔥🔥🔥🔥🔥", "abc🔥🔥🔥🔥🔥🔥🔥def", "supercalifragilisticexpialidocious"} {
			for _, line := range wrapText(in, w) {
				if got := lipgloss.Width(line); got > w && w > 1 {
					t.Errorf("width %d: %q is %d cells", w, line, got)
				}
			}
		}
	}
	dir := notesDir(t, map[string]string{"global.md": "- " + strings.Repeat("🔥", 60) + "\n"})
	b := Board{State: testState(), Width: 80, Height: 30, Tab: 2, Notes: Notes{Dir: dir}}
	_ = b.View() // must not panic
}

func TestNotesScrollAndEditKey(t *testing.T) {
	dir := notesDir(t, map[string]string{"global.md": strings.Repeat("- line\n", 60)})
	b := Board{State: testState(), Width: 92, Height: 24, Tab: 2, Notes: Notes{Dir: dir}}
	if !strings.Contains(ansi.ReplaceAllString(b.View(), ""), "more") {
		t.Fatal("fixture should overflow the pane")
	}
	b.HandleKey("j")
	b.HandleKey("j")
	if b.Notes.Scroll != 2 {
		t.Errorf("j twice should scroll 2, got %d", b.Notes.Scroll)
	}
	b.HandleKey("k")
	b.HandleKey("k")
	b.HandleKey("k")
	if b.Notes.Scroll != 0 {
		t.Errorf("scroll must not go negative, got %d", b.Notes.Scroll)
	}
	// Overshooting the end must not run away — k has to bite on the very
	// next press, which it cannot if j piled up past the last page.
	for i := 0; i < 200; i++ {
		b.HandleKey("j")
	}
	max := b.Notes.Scroll
	if max <= 0 || max >= 60 {
		t.Fatalf("scroll should clamp to the last page, got %d", max)
	}
	b.HandleKey("k")
	if b.Notes.Scroll != max-1 {
		t.Errorf("k after overshoot should scroll back one, got %d (was %d)", b.Notes.Scroll, max)
	}
	if b.EditNote() == nil {
		t.Error("e on the notes tab should open the editor")
	}
	b.Tab = 0
	if b.EditNote() != nil {
		t.Error("e off the notes tab is nobody's key")
	}
}
