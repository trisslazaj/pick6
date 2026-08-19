package main

import (
	"testing"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/fpl"
)

// The alias index exists because fpl's web_name is a DISPLAY name and a written
// ranking is not reading off that column. Every case here is a real shape from
// the live pool, with invented players standing in for them.
func TestAliasIndexResolvesWrittenNames(t *testing.T) {
	// id -> (webName, first, second, pos)
	type row struct{ id, web, first, second, pos string }
	rows := []row{
		{"1", "Zed", "Zedediah", "Torrino", "DEF"},        // printed name is the FIRST name
		{"2", "J.Alpha", "Jonas", "Alpha", "DEF"},         // initial-dot surname
		{"3", "Alpha", "Rowan", "Alpha", "MID"},           // same surname, other position
		{"4", "Bram Delta", "Bram", "Ecks Wye", "FWD"},    // two-word printed name
		{"5", "Quill", "Quillon 'Quib'", "Marrow", "MID"}, // nickname in quotes
		{"6", "Vex", "Vexley", "Pomeroy Grange", "GKP"},   // compound surname
		{"7", "Alpha", "Sable", "Alpha", "MID"},           // AMBIGUOUS with 3
	}
	players := map[string]*adp.Player{}
	var pool []fpl.Element
	for i, r := range rows {
		players[r.id] = &adp.Player{SleeperID: r.id, Name: r.web, Pos: r.pos, Team: "AAA"}
		pool = append(pool, fpl.Element{ID: i + 1, WebName: r.web, FirstName: r.first, SecondName: r.second})
	}
	ix := newAliasIndex(players, pool, nil)

	for _, c := range []struct{ name, pos, want string }{
		{"Zed", "DEF", "1"},     // as printed
		{"Torrino", "DEF", "1"}, // the surname fpl never shows
		{"Alpha", "DEF", "2"},   // bare surname, board has the initial
		{"J Alpha", "DEF", "2"}, // initial written with a space
		{"Delta", "FWD", "4"},   // last word of a printed two-word name
		{"Quib", "MID", "5"},    // the nickname in quotes
		{"Pomeroy", "GKP", "6"}, // first word of a compound surname
		{"Grange", "GKP", "6"},  // ...and its last
		{"R Alpha", "MID", "3"}, // initial picks Rowan out of two Alphas
		{"S Alpha", "MID", "7"}, // ...and Sable out of the same two
	} {
		p, why := ix.find(c.name, c.pos, "")
		if p == nil {
			t.Errorf("%q (%s) did not resolve%s", c.name, c.pos, why)
			continue
		}
		if p.SleeperID != c.want {
			t.Errorf("%q (%s) resolved to %s (%s), want id %s", c.name, c.pos, p.Name, p.SleeperID, c.want)
		}
	}

	// Ambiguous with no initial to settle it: reported, never guessed.
	if p, why := ix.find("Alpha", "MID", ""); p != nil {
		t.Errorf("two midfielders called Alpha resolved to %s anyway", p.SleeperID)
	} else if why == "" {
		t.Error("an ambiguous name gave no reason")
	}
	// A wrong position is worth saying out loud rather than reporting as absent.
	if _, why := ix.find("Zed", "MID", ""); why == "" {
		t.Error("a position mismatch was reported as a plain miss")
	}
}

// A weak alias must never delete a strong one. Two defenders answer to "james"
// once first names are aliased (Reece James, and James Tarkowski whose FIRST
// name it is), and Reece — whose printed name simply IS James — went with it
// while the two shared one ambiguity set.
func TestAStrongNameSurvivesAWeakCollision(t *testing.T) {
	players := map[string]*adp.Player{
		"1": {SleeperID: "1", Name: "Jamesy", Pos: "DEF", Team: "AAA"},
		"2": {SleeperID: "2", Name: "Tarkomere", Pos: "DEF", Team: "BBB"},
	}
	pool := []fpl.Element{
		{ID: 1, WebName: "Jamesy", FirstName: "Reece", SecondName: "Jamesy"},
		{ID: 2, WebName: "Tarkomere", FirstName: "Jamesy", SecondName: "Tarkomere"},
	}
	ix := newAliasIndex(players, pool, nil)
	p, why := ix.find("Jamesy", "DEF", "")
	if p == nil || p.SleeperID != "1" {
		t.Fatalf("the man actually called Jamesy did not win his own name (%v)%s", p, why)
	}
}
