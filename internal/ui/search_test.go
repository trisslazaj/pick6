package ui

import (
	"strings"
	"testing"

	"github.com/trisslazaj/pick6/internal/engine"
)

// searchBoard is the run fixture with three real-shaped name collisions bolted
// on, because collisions are the only thing the ranking has to get right — a
// board where every query matches one player would pass any ordering at all.
func searchBoard() *engine.State {
	s := runState()
	s.Players["nx1"] = engine.Player{ID: "nx1", Name: "Fake Brown", Pos: "WR", Team: "BBB",
		Value: 60, ADP: 20, Sigma: 4, Tier: 3, Bye: 7}
	s.Players["nx2"] = engine.Player{ID: "nx2", Name: "Brown Fakeman", Pos: "RB", Team: "CCC",
		Value: 55, ADP: 25, Sigma: 4, Tier: 4, Bye: 7}
	s.Players["nx3"] = engine.Player{ID: "nx3", Name: "Fakerson Hambrownlee", Pos: "TE", Team: "DDD",
		Value: 70, ADP: 22, Sigma: 4, Tier: 3, Bye: 7}
	return s
}

func query(s *engine.State, q string) Board {
	return Board{State: s, Width: 100, Height: 40, Search: Search{Open: true, Query: q}}
}

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// A token that starts a word beats one buried inside it, whatever the value
// column says. "brownlee" is the most valuable of the three and still sorts
// last, because nobody typing "brown" means him.
func TestSearchRanksWordStartsFirst(t *testing.T) {
	rows := query(searchBoard(), "brown").searchResults()
	if len(rows) != 3 {
		t.Fatalf("want 3 matches, got %d", len(rows))
	}
	if rows[2].ID != "nx3" {
		t.Errorf("mid-word match should sort last, got order %s %s %s",
			rows[0].ID, rows[1].ID, rows[2].ID)
	}
	// Among equal-quality matches, value decides.
	if rows[0].ID != "nx1" {
		t.Errorf("want the more valuable word-start match first, got %s", rows[0].ID)
	}
}

// Every token has to land, so a second word narrows instead of widening. This
// is the whole reason the query is split on spaces rather than matched whole:
// "brown fake" and "fake brown" are the same request.
func TestSearchRequiresEveryToken(t *testing.T) {
	for _, q := range []string{"brown fakeman", "fakeman brown"} {
		rows := query(searchBoard(), q).searchResults()
		if len(rows) != 1 || rows[0].ID != "nx2" {
			t.Fatalf("%q: want just the one man, got %d matches", q, len(rows))
		}
	}
	if rows := query(searchBoard(), "brown zzz").searchResults(); len(rows) != 0 {
		t.Errorf("an unmatchable token should empty the list, got %d rows", len(rows))
	}
}

// The team code is part of the haystack, which is the only thing that reaches a
// defense: sleeper carries the city and the nickname, and everyone types the
// abbreviation.
func TestSearchMatchesOnTeam(t *testing.T) {
	s := searchBoard()
	s.Players["dx1"] = engine.Player{ID: "dx1", Name: "Fakeville Defense", Pos: "DEF", Team: "FKV"}
	rows := query(s, "fkv").searchResults()
	if len(rows) != 1 || rows[0].ID != "dx1" {
		t.Fatalf("want the defense by team code, got %d rows", len(rows))
	}
}

// Taken players stay in the list and say when they went. Answering "is he gone"
// with an empty result is answering a different question — "no such player" —
// and it is the answer a drafter is least able to catch.
func TestSearchKeepsTakenPlayersAndSaysWhen(t *testing.T) {
	s := searchBoard()
	s.Draft("nx1")
	b := query(s, "brown")
	rows := b.searchResults()
	if len(rows) != 3 {
		t.Fatalf("want 3 matches, got %d", len(rows))
	}
	if !rows[2].taken || rows[2].ID != "nx1" {
		t.Errorf("the taken man should sort last, got %s taken=%v", rows[2].ID, rows[2].taken)
	}
	view := plain(b.View())
	if !strings.Contains(view, "taken 1.01") {
		t.Errorf("overlay should name the pick that took him:\n%s", view)
	}
}

// Typing goes into the query, not into the keybinds underneath it. The p and
// the j in "jaxon" cycled the data tab's position filter and scrolled it before
// the prompt consumed keys first.
func TestSearchSwallowsTheKeysUnderneathIt(t *testing.T) {
	b := &Board{State: searchBoard(), Width: 100, Height: 40, Tab: 1}
	b.HandleKey("/")
	for _, k := range []string{"p", "j", " ", "k"} {
		if !b.HandleKey(k) {
			t.Fatalf("key %q should be consumed while the prompt is open", k)
		}
	}
	if b.Search.Query != "pj k" {
		t.Errorf("query should collect the typed runes, got %q", b.Search.Query)
	}
	if b.DataFilter != "" || b.DataScroll != 0 {
		t.Errorf("data tab state moved while typing: filter %q scroll %d",
			b.DataFilter, b.DataScroll)
	}
	// ...and ctrl+c is never something you should have to escape out of first.
	if b.HandleKey("ctrl+c") {
		t.Error("ctrl+c must fall through to the model's quit binding")
	}
}

// enter leaves a selection behind and closes the prompt; esc leaves neither.
func TestSearchEnterSelectsAndEscCancels(t *testing.T) {
	b := &Board{State: searchBoard(), Width: 100, Height: 40}
	b.HandleKey("/")
	for _, r := range "brown" {
		b.HandleKey(string(r))
	}
	b.HandleKey("down")
	b.HandleKey("enter")
	if b.Search.Open {
		t.Error("enter should close the prompt")
	}
	if b.Selected != "nx2" {
		t.Errorf("enter should select the row under the cursor, got %q", b.Selected)
	}

	b.HandleKey("/")
	b.HandleKey("z")
	b.HandleKey("esc")
	if b.Search.Open || b.Search.Query != "" {
		t.Error("esc should close the prompt and forget the query")
	}
	if b.Selected != "nx2" {
		t.Error("esc out of the prompt should leave the standing selection alone")
	}
	// A second esc, with no prompt up, is what clears it.
	if !b.HandleKey("esc") || b.Selected != "" {
		t.Errorf("esc should clear the selection, got %q", b.Selected)
	}
}

// The cursor cannot leave the list, however long you hold the key down. It is
// an index into a slice that shrinks as you type.
func TestSearchCursorStaysInsideTheList(t *testing.T) {
	b := &Board{State: searchBoard(), Width: 100, Height: 40}
	b.HandleKey("/")
	for _, r := range "brown" {
		b.HandleKey(string(r))
	}
	for i := 0; i < 10; i++ {
		b.HandleKey("down")
	}
	if b.Search.Cursor != 2 {
		t.Errorf("cursor should stop at the last row, got %d", b.Search.Cursor)
	}
	// Narrowing the query past the cursor's row has to bring it back.
	b.HandleKey("l")
	b.HandleKey("e")
	if n := len(b.searchResults()); b.Search.Cursor >= n {
		t.Errorf("cursor %d is off the end of a %d-row list", b.Search.Cursor, n)
	}
	for i := 0; i < 10; i++ {
		b.HandleKey("up")
	}
	if b.Search.Cursor != 0 {
		t.Errorf("cursor should stop at the first row, got %d", b.Search.Cursor)
	}
}

// The overlay is charged against the same height budget as everything else. It
// is drawn between the body and the footer precisely so the clamp takes its
// rows off the bottom of the body — an overlay appended after the frame would
// scroll the header off the top, which is the failure Reserve exists to stop.
func TestOverlayNeverExceedsTerminalHeight(t *testing.T) {
	for _, h := range []int{18, 22, 24, 30, 40} {
		for _, reserve := range []int{0, 2} {
			b := Board{State: searchBoard(), Width: 100, Height: h, Reserve: reserve,
				Search: Search{Open: true, Query: "brown"}}
			if got := rowCount(b.View()) + reserve; got > h {
				t.Errorf("height %d reserve %d: frame is %d rows", h, reserve, got)
			}
		}
	}
}

// A cursor handed in from outside — the snapshot path sets Search wholesale —
// cannot walk the render off the end of the list.
func TestSearchPaneSurvivesAnOutOfRangeCursor(t *testing.T) {
	for _, cursor := range []int{-5, 2, 99} {
		for _, q := range []string{"brown", "nobody-by-this-name"} {
			b := Board{State: searchBoard(), Width: 100, Height: 40,
				Search: Search{Open: true, Query: q, Cursor: cursor}}
			b.searchPane(100) // must not panic
		}
	}
}

// Nothing selected and nothing typed means no overlay at all — no reserved
// blank row, the same rule the alert banner follows.
func TestOverlayIsAbsentWhenNothingIsSelected(t *testing.T) {
	b := Board{State: searchBoard(), Width: 100, Height: 40}
	if got := b.overlay(100); got != "" {
		t.Errorf("want no overlay, got %q", got)
	}
}

// The standing selection survives a tab flip, because it is a fact about the
// session and not about the pane you happen to be looking at.
func TestSelectionSurvivesTabFlip(t *testing.T) {
	b := &Board{State: searchBoard(), Width: 100, Height: 40, Mode: ModeManual,
		Selected: "nx1"}
	first := plain(b.View())
	if !strings.Contains(first, "selected") || !strings.Contains(first, "fake brown") {
		t.Fatalf("board tab should name the selection:\n%s", first)
	}
	if !strings.Contains(lastLine(first), "x taken") {
		t.Errorf("manual mode should say what x would do:\n%s", lastLine(first))
	}
	b.HandleKey("tab")
	if second := plain(b.View()); !strings.Contains(second, "fake brown") {
		t.Errorf("data tab lost the selection:\n%s", second)
	}
}

// Live mode's marks come from the poll. A hand-typed one there would desync the
// board it is supposed to be checking, so the line does not offer the key.
func TestLiveModeDoesNotOfferToMark(t *testing.T) {
	b := Board{State: searchBoard(), Width: 100, Height: 40, Mode: ModeLive,
		Selected: "nx1"}
	view := plain(b.View())
	if strings.Contains(view, "space step") || strings.Contains(view, "x taken") {
		t.Errorf("live footer should only name keys live mode binds:\n%s", lastLine(view))
	}
}

// Both overlay panes drop clauses rather than running over their column, at
// every width the board renders at. The selection line jammed itself against
// its own hint at 80 before the ladder went in.
func TestOverlayFitsEveryWidth(t *testing.T) {
	for _, w := range []int{MinWidth, 88, 92, 100, MaxWidth} {
		b := Board{State: searchBoard(), Width: w, Height: 40, Mode: ModeManual,
			Selected: "nx3"}
		for _, line := range strings.Split(plain(b.selectedLine(w)), "\n") {
			if got := len([]rune(line)); got > w {
				t.Errorf("width %d: selection line is %d cells: %q", w, got, line)
			}
		}
		b.Search = Search{Open: true, Query: "brown"}
		for _, line := range strings.Split(plain(b.searchPane(w)), "\n") {
			if got := len([]rune(line)); got > w {
				t.Errorf("width %d: search row is %d cells: %q", w, got, line)
			}
		}
	}
}

// x spends a pick on the selection, and only in manual mode. This is the one
// place in the tool where a human types a pick, so it has to advance the draft
// exactly the way a fed one does — and undo has to put it back.
func TestManualMarkSpendsAPickAndUndoTakesItBack(t *testing.T) {
	m := NewManualModel(testState())
	m.board.Selected = "RB00"

	m = send(m, key(" ")) // no autopicker: the room advances the draft, not a key
	if got := m.board.State.PickNo; got != 1 {
		t.Errorf("space stepped a manual board to pick %d", got)
	}

	m = send(m, key("x"))
	if !m.board.State.Taken["RB00"] {
		t.Fatal("x should mark the selection taken")
	}
	if got := m.board.State.PickNo; got != 2 {
		t.Errorf("after marking, pick = %d, want 2", got)
	}
	if m.board.Selected != "" {
		t.Error("marking should clear the selection, not leave it to be marked twice")
	}
	if !strings.Contains(m.board.Status, "fake rb00") {
		t.Errorf("status should name who it just spent the pick on, got %q", m.board.Status)
	}

	m = send(m, key("u"))
	if m.board.State.Taken["RB00"] || m.board.State.PickNo != 1 {
		t.Errorf("undo left taken=%v at pick %d",
			m.board.State.Taken["RB00"], m.board.State.PickNo)
	}

	// x with nothing selected says so rather than silently spending a pick.
	m = send(m, key("x"))
	if m.board.State.PickNo != 1 {
		t.Error("x with no selection must not advance the draft")
	}
	if !strings.Contains(m.board.Status, "nobody selected") {
		t.Errorf("status should say why nothing happened, got %q", m.board.Status)
	}
}

// ...and in mock mode x is not a binding at all: the scripted picker owns the
// draft, and a hand-typed mark on top of it would double-spend a pick.
func TestMockModeIgnoresTheMarkKey(t *testing.T) {
	m := NewModel(testState(), firstAvailable, false)
	m.board.Selected = "RB00"
	m = send(m, key("x"))
	if m.board.State.Taken["RB00"] || m.board.State.PickNo != 1 {
		t.Error("x marked a player on a board with a picker driving it")
	}
}

// The footer spends its own keys before it truncates how old the board is: the
// glossary is static and learnable, the age is a fact about the data that
// nothing else on screen can tell you. q drops first, on a screen where esc and
// ctrl+c also quit.
func TestFooterSpendsKeysBeforeTheAgeClause(t *testing.T) {
	keys := []string{"space step", "/ search", "tab data", "u undo", "q quit", "a auto"}
	got, note := fitFooter(104, keys, []string{"adp 26h old · 1,110 drafts", "adp 26h old"}, 13)
	if note != "adp 26h old · 1,110 drafts" {
		t.Errorf("104 columns should still seat the long note, got %q", note)
	}
	if len(got) >= len(keys) {
		t.Error("...and it should have paid for it with a key")
	}
	if len(got) < keyFloor {
		t.Errorf("the row kept only %d keys, below the floor of %d", len(got), keyFloor)
	}
	// With nothing to say about the board's age, every key stays.
	if got, note := fitFooter(104, keys, []string{"", ""}, 13); note != "" || len(got) != len(keys) {
		t.Errorf("no meta.json should cost no keys, got %d keys and note %q", len(got), note)
	}
}
