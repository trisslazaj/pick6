package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A rankings folder with one obviously fake file: two men on the board (one
// of whom gets taken), one the board has never heard of, and a defense named
// the way real files name them — by team, not by player.
func fakeRankingsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	csv := "name,position,team,tier,sentiment\n" +
		"Fake Wr00,WR,,1,target\n" +
		"Fake Wr01,WR,,1,\n" +
		"Nobody Atall,WR,,2,avoid\n" +
		"Fake Rb00,RB,,1,\n" +
		"Some Defense,DEF,DEF00,,\n"
	if err := os.WriteFile(filepath.Join(dir, "guru-2026.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestViewsCycleAndResetScroll(t *testing.T) {
	b := Board{State: testState(), Width: 100, Height: 40, Tab: 1, Views: Views{Dir: fakeRankingsDir(t)}}
	want := []string{"value", "adp", "tiers", "guru-2026", "value"}
	for i, label := range want {
		if got := b.currentView().label; got != label {
			t.Fatalf("step %d: view %q, want %q", i, got, label)
		}
		if i == len(want)-1 {
			break
		}
		b.DataScroll = 5
		b.HandleKey("right")
	}
	if b.DataScroll != 0 {
		t.Error("switching views should reset the scroll")
	}
	b.HandleKey("left")
	if got := b.currentView().label; got != "guru-2026" {
		t.Errorf("left from value should wrap to the last view, got %q", got)
	}
	// The strip names every view and marks the open one.
	view := ansi.ReplaceAllString(b.View(), "")
	for _, label := range []string{"value", "adp", "tiers", "▏guru-2026"} {
		if !strings.Contains(view, label) {
			t.Errorf("strip should show %q:\n%s", label, view)
		}
	}
}

// A file view is the file's word: its order, its tiers, its opinions. A man
// the board knows carries his odds; one who is gone says which pick took him;
// one the board never heard of prints with a ? and is not dropped.
func TestFileViewIsTheFileVerbatim(t *testing.T) {
	s := testState()
	s.Draft("WR01") // 1.01 takes him
	b := Board{State: s, Width: 100, Height: 40, Tab: 1, Views: Views{Dir: fakeRankingsDir(t)}}
	b.SelectView("guru-2026")
	view := ansi.ReplaceAllString(b.View(), "")

	for _, want := range []string{
		"guru-2026 — as the file ranks them",
		"wr · tier 1 · 1 of 2 left", // the file's tier, with wr01 gone
		"wr · tier 2\n",             // nobody atall: neither gone nor left, so no count
		"nobody atall",
		"?: not on the board",
		"taken 1.01",
		"fake wr00 target",
		"def",
		"some defense",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("file view should contain %q:\n%s", want, view)
		}
	}
	// The file's order, not the board's: rb comes after wr because the file
	// wrote it that way, and the ? row sits in its place.
	wr := strings.Index(view, "fake wr00")
	nobody := strings.Index(view, "nobody atall")
	rb := strings.Index(view, "fake rb00")
	if !(wr < nobody && nobody < rb) {
		t.Errorf("file order should be wr00, nobody, rb00; got offsets %d %d %d", wr, nobody, rb)
	}
	// Nothing of ours: no value column, no urgency strip.
	if strings.Contains(view, "fmt gap") || strings.Contains(view, "urgency") {
		t.Errorf("a file view carries no engine columns:\n%s", view)
	}
	// The unknown row wears a ? where his odds would go.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "nobody atall") && !strings.HasSuffix(strings.TrimRight(line, " "), "?") {
			t.Errorf("unknown row should end in ?: %q", line)
		}
	}
}

// The tiers view is the board's own ladder: every man, position by position,
// tier by tier, the taken still listed and marked.
func TestTiersViewGroupsByTierAndKeepsTheTaken(t *testing.T) {
	s := testState()
	s.Draft("RB00")
	b := Board{State: s, Width: 100, Height: 60, Tab: 1}
	b.SelectView("tiers")
	view := ansi.ReplaceAllString(b.View(), "")
	for _, want := range []string{
		"every player — by position and tier, taken struck",
		"rb · tier 1 · 3 of 4 left",
		"fake rb00",
		"taken 1.01",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("tiers view should contain %q:\n%s", want, view)
		}
	}
	// The p filter narrows the ladder like it narrows the table.
	b.HandleKey("p") // RB
	view = ansi.ReplaceAllString(b.View(), "")
	if !strings.Contains(view, "rb only — by position") || strings.Contains(view, "wr · tier") {
		t.Errorf("filtered ladder should show rb only:\n%s", view)
	}
}

// Every view obeys the board's two laws: no line wider than the terminal, no
// frame taller than it.
func TestEveryViewFitsTheTerminal(t *testing.T) {
	dir := fakeRankingsDir(t)
	for _, size := range []struct{ w, h int }{
		{80, 24}, {92, 30}, {100, 40}, {140, 50},
	} {
		m := NewModel(testState(), firstAvailable, false).WithRankings(dir, "")
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
		for i := 0; i < 40; i++ {
			m.step()
		}
		m = send(m, key("tab"))
		for _, label := range []string{"value", "adp", "tiers", "guru-2026"} {
			if got := m.board.currentView().label; got != label {
				t.Fatalf("expected view %q, on %q", label, got)
			}
			view := m.View()
			for i, line := range strings.Split(view, "\n") {
				if n := len([]rune(ansi.ReplaceAllString(line, ""))); n > size.w {
					t.Errorf("%dx%d %s: line %d is %d runes, exceeds width", size.w, size.h, label, i, n)
				}
			}
			if lines := strings.Count(view, "\n") + 1; lines > size.h {
				t.Errorf("%dx%d %s: frame is %d lines", size.w, size.h, label, lines)
			}
			m = send(m, tea.KeyMsg{Type: tea.KeyRight})
		}
	}
}

// The fetched file is a view wherever it lives, and a folder without it does
// not lose it; the same file in both places is one view, not two.
func TestFetchedFileIsAlwaysAView(t *testing.T) {
	dir := fakeRankingsDir(t)
	fetched := filepath.Join(dir, "guru-2026.csv")
	b := Board{State: testState(), Views: Views{Dir: dir, Fetched: fetched}}
	if n := len(b.viewList()); n != 4 {
		t.Errorf("same file in folder and fetched should be one view, got %d views", n)
	}
	b = Board{State: testState(), Views: Views{Dir: t.TempDir(), Fetched: fetched}}
	if got := b.viewList()[3].label; got != "guru-2026" {
		t.Errorf("fetched file should be a view on its own, got %q", got)
	}
	b = Board{State: testState(), Views: Views{Fetched: filepath.Join(dir, "missing.csv")}}
	if n := len(b.viewList()); n != 3 {
		t.Errorf("a missing fetched file is skipped, got %d views", n)
	}
}
