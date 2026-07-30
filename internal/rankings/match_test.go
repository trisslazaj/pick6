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

// The other half of the defense join, and the reason it exists: fantasypros'
// adp export puts the TEAM NAME where a player name goes and the literal string
// "DST" where a team code goes, so the abbreviation path resolves to nothing and
// all 30 defenses drop. Matching the normalized team name instead took that join
// from 91.8% to 99.5%.
//
// The order matters as much as the fallback. A source that gives a real code
// must still be answered by the code, or a future export whose names disagree
// with sleeper's would start missing defenses the abbreviation already found.
func TestLookupDefenseByTeamName(t *testing.T) {
	pool := map[string]sleeper.Player{
		"DEN": {PlayerID: "DEN", FirstName: "Denver", LastName: "Broncos", Position: "DEF", Team: "DEN", Active: true},
		"SF":  {PlayerID: "SF", FirstName: "San Francisco", LastName: "49ers", Position: "DEF", Team: "SF", Active: true},
		"SEA": {PlayerID: "SEA", FirstName: "Seattle", LastName: "Seahawks", Position: "DEF", Team: "SEA", Active: true},
	}
	ix := NewIndex(pool)

	cases := []struct {
		name       string
		srcName    string
		srcPos     string
		srcTeam    string
		want       string
		wantMatch  bool
		whyItFails string
	}{
		// The fantasypros shape: name carries the team, team carries nothing
		// usable. The pos column said DST, which NormalizePos already turned
		// into DEF before this call.
		{"team name with no code", "Denver Broncos", "DEF", "", "DEN", true, ""},
		// Digits survive normalization, or the niners never match.
		{"a nickname that is a number", "San Francisco 49ers", "DEF", "", "SF", true, ""},
		// The abbreviation still wins where the source has one — ffc's path.
		{"a real code beats the name", "Seattle Defense", "DEF", "SEA", "SEA", true, ""},
		// And a code that resolves is never second-guessed by a name that
		// would resolve elsewhere.
		{"the code wins even when the name points somewhere else",
			"Denver Broncos", "DEF", "SEA", "SEA", true, ""},
		// Still exact: a team nobody indexed is a miss, never a guess.
		{"an unknown team is a miss", "Chicago Bears", "DEF", "", "", false, ""},
		{"a partial name is a miss", "Broncos", "DEF", "", "", false, ""},
	}
	for _, c := range cases {
		id, ok := ix.LookupExact(c.srcName, c.srcPos, c.srcTeam)
		if ok != c.wantMatch || (c.wantMatch && id != c.want) {
			t.Errorf("%s: lookupexact(%q, %q, %q) = %q, %v; want %q, %v",
				c.name, c.srcName, c.srcPos, c.srcTeam, id, ok, c.want, c.wantMatch)
		}
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

// The pool is a map, so "first writer wins" over a range means "whichever entry
// the runtime visited first" — and colliding (name, position) keys are real:
// eight of them on the current 3,223-player active pool, two of which the 389-name
// 2025 backtest board reaches. Rebuilding the index twenty times in one process
// flipped those two 62 times before the sort went in. A flipped id is looked up,
// never appears in the picks, and is labeled "survived forever" at every vantage —
// a bias that moves the backtest between runs with the join count unchanged.
func TestIndexResolvesCollisionsTheSameWayEveryBuild(t *testing.T) {
	pool := map[string]sleeper.Player{
		// Same normalized name and position, three different ids. Only one can win
		// and it must be the same one on every build.
		"77": {PlayerID: "77", FullName: "Nick Williams", Position: "WR", Team: "DEN", Active: true},
		"11": {PlayerID: "11", FullName: "Nick Williams", Position: "WR", Team: "CHI", Active: true},
		"44": {PlayerID: "44", FullName: "Nick Williams", Position: "WR", Team: "FA", Active: true},
		// A suffix collision, since Normalize strips them: both are "chase cota".
		"22": {PlayerID: "22", FullName: "Chase Cota", Position: "WR", Team: "SEA", Active: true},
		"33": {PlayerID: "33", FullName: "Chase Cota Jr.", Position: "WR", Team: "LAR", Active: true},
	}
	cases := []struct {
		name, srcName, want string
	}{
		// Lowest id wins, which is a fact about the pool rather than about the run.
		{"three-way name collision", "Nick Williams", "11"},
		{"suffix-stripped collision", "Chase Cota", "22"},
	}
	for _, c := range cases {
		for build := 0; build < 25; build++ {
			ix := NewIndex(pool)
			id, ok := ix.LookupExact(c.srcName, "WR", "")
			if !ok || id != c.want {
				t.Fatalf("%s: build %d resolved to %q, %v; want %q, true",
					c.name, build, id, ok, c.want)
			}
		}
	}
}

// The same map-order tie-break lives in the fuzzy fallback, and that one is on
// the shipped fetch path: its answer is written into mapping.json and trusted
// forever after. Two candidates at the same edit distance must not depend on
// which one the runtime reached first.
func TestFuzzyBreaksEqualDistanceTiesTheSameWayEveryBuild(t *testing.T) {
	pool := map[string]sleeper.Player{
		// Both exactly one edit from "Chris Johnsan".
		"90": {PlayerID: "90", FullName: "Chris Johnson", Position: "RB", Team: "TEN", Active: true},
		"30": {PlayerID: "30", FullName: "Chris Johnsen", Position: "RB", Team: "NYJ", Active: true},
	}
	for build := 0; build < 25; build++ {
		ix := NewIndex(pool)
		id, ok := ix.Lookup("Chris Johnsan", "RB", "TEN")
		if !ok || id != "30" {
			t.Fatalf("build %d fuzzed to %q, %v; want 30, true", build, id, ok)
		}
	}
}
