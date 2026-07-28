package rankings

import (
	"testing"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Marvin Harrison Jr.", "marvin harrison"},
		{"Ja'Marr Chase", "jamarr chase"},
		{"Amon-Ra St. Brown", "amonra st brown"},
		{"James Cook III", "james cook"},
		{"Michael Pittman Jr", "michael pittman"},
		{"  D'Andre   Swift  ", "dandre swift"},
		{"Aaron Jones Sr.", "aaron jones"},
		{"Kenneth Walker III", "kenneth walker"},
		{"Travis Etienne Jr.", "travis etienne"},
		// A name that is only a suffix must not normalize to empty.
		{"Jr.", "jr"},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizePos(t *testing.T) {
	cases := []struct{ in, want string }{
		{"PK", "K"}, {"K", "K"},
		{"DEF", "DEF"}, {"DST", "DEF"}, {"D/ST", "DEF"},
		{"rb", "RB"}, {" WR ", "WR"},
	}
	for _, c := range cases {
		if got := NormalizePos(c.in); got != c.want {
			t.Errorf("NormalizePos(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Defenses are the one case name matching cannot reach: every DEF in the Sleeper
// dump has full_name: null and is keyed by team abbreviation.
func TestLookupDefenseByTeam(t *testing.T) {
	pool := map[string]sleeper.Player{
		"HOU": {PlayerID: "HOU", FirstName: "Houston", LastName: "Texans", Position: "DEF", Team: "HOU", Active: true},
		"SEA": {PlayerID: "SEA", FirstName: "Seattle", LastName: "Seahawks", Position: "DEF", Team: "SEA", Active: true},
		"1":   {PlayerID: "1", FullName: "Jahmyr Gibbs", Position: "RB", Team: "DET", Active: true},
	}
	ix := NewIndex(pool)

	// FFC calls these "Seattle Defense" — the name will never match, the team must.
	if id, ok := ix.Lookup("Seattle Defense", "DEF", "SEA"); !ok || id != "SEA" {
		t.Errorf("defense lookup by team = %q, %v; want SEA, true", id, ok)
	}
	if id, ok := ix.Lookup("LA Rams Defense", "DEF", "LAR"); ok {
		t.Errorf("unknown defense matched %q, want miss", id)
	}
	if id, ok := ix.Lookup("Jahmyr Gibbs", "RB", "DET"); !ok || id != "1" {
		t.Errorf("skill lookup = %q, %v; want 1, true", id, ok)
	}
}

func TestLookupFuzzyStaysInPosition(t *testing.T) {
	pool := map[string]sleeper.Player{
		"1": {PlayerID: "1", FullName: "Chris Johnson", Position: "RB", Team: "TEN", Active: true},
		"2": {PlayerID: "2", FullName: "Chris Johnsen", Position: "WR", Team: "NYJ", Active: true},
	}
	ix := NewIndex(pool)

	// One edit away, but a different position — must not cross over.
	if id, ok := ix.Lookup("Chris Johnsen", "RB", "TEN"); !ok || id != "1" {
		t.Errorf("fuzzy within position = %q, %v; want 1, true", id, ok)
	}
	if id, ok := ix.Lookup("Chris Johnson", "TE", "TEN"); ok {
		t.Errorf("fuzzy crossed position boundary, matched %q", id)
	}
}
