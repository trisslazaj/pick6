package rankings

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.csv")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCSVGeneric(t *testing.T) {
	f, err := LoadCSV(write(t, `name,position,team,tier,points,adp
Fake Alpha,RB,AAA,1,310.5,1.2
Fake Bravo,WR,BBB,1,298.0,3.4
Fake Charlie,QB,CCC,2,401.2,55.0
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(f.Rows))
	}
	if !f.HasTier || !f.HasPoints || !f.HasADP {
		t.Errorf("column detection wrong: tier=%v points=%v adp=%v", f.HasTier, f.HasPoints, f.HasADP)
	}
	if f.Rows[0].Name != "Fake Alpha" || f.Rows[0].Pos != "RB" || f.Rows[0].Points != 310.5 {
		t.Errorf("bad first row: %+v", f.Rows[0])
	}
}

// FantasyPros-style export: different headers, position carries a rank suffix,
// and the team is packed into the player name.
func TestLoadCSVFantasyProsShape(t *testing.T) {
	f, err := LoadCSV(write(t, `"RK","TIERS","PLAYER NAME","POS","BYE WEEK"
1,1,Fake Alpha AAA,RB1,7
2,1,Fake Bravo BBB,WR1,9
3,2,Fake Charlie CCC,RB2,5
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Rows[0].Name != "Fake Alpha" || f.Rows[0].Team != "AAA" {
		t.Errorf("trailing team not split: %+v", f.Rows[0])
	}
	if f.Rows[0].Pos != "RB" {
		t.Errorf("position rank suffix not stripped: %q", f.Rows[0].Pos)
	}
	if f.Rows[0].Rank != 1 {
		t.Errorf("rank column not read: %+v", f.Rows[0])
	}
}

// An "overall" ranking file numbers tiers across every position at once. Cliff
// detection is per-position, so those numbers have to be renumbered.
func TestGlobalTiersAreRenumberedPerPosition(t *testing.T) {
	// Realistic shape: RB occupies tiers 1, 3, 4 and WR occupies 2, 5 — the holes
	// in each position are what gives the global numbering away.
	f, err := LoadCSV(write(t, `rank,name,pos,tier
1,Fake Alpha,RB,1
2,Fake Bravo,RB,1
3,Fake Charlie,WR,2
4,Fake Delta,RB,3
5,Fake Echo,RB,4
6,Fake Foxtrot,RB,4
7,Fake Golf,WR,5
`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.TiersWereGlobal {
		t.Fatal("should have detected cross-position tier numbering")
	}
	got := map[string]int{}
	for _, r := range f.Rows {
		got[r.Name] = r.Tier
	}
	// RB: global 1,3,4 -> local 1,2,3, preserving the grouping.
	if got["Fake Alpha"] != 1 || got["Fake Bravo"] != 1 {
		t.Errorf("rb tier 1 wrong: %v", got)
	}
	if got["Fake Delta"] != 2 {
		t.Errorf("rb tier 2 wrong: %v", got)
	}
	if got["Fake Echo"] != 3 || got["Fake Foxtrot"] != 3 {
		t.Errorf("rb tier 3 wrong: %v", got)
	}
	// WR renumbers independently and also starts at 1.
	if got["Fake Charlie"] != 1 || got["Fake Golf"] != 2 {
		t.Errorf("wr tiers renumbered wrong: %v", got)
	}
	if sizes := f.TierSizes(); len(sizes["RB"]) != 3 {
		t.Errorf("expected 3 rb tiers, got %v", sizes["RB"])
	}
}

// The ambiguous case: with no gaps, global and per-position numbering produce
// identical groupings, so treating it as per-position is a safe no-op.
func TestAmbiguousTierNumberingLeftAlone(t *testing.T) {
	f, err := LoadCSV(write(t, `rank,name,pos,tier
1,Fake Alpha,RB,1
2,Fake Bravo,WR,1
3,Fake Charlie,RB,2
4,Fake Delta,WR,2
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.TiersWereGlobal {
		t.Error("no gaps means no evidence of global numbering; should be left alone")
	}
}

func TestPerPositionTiersLeftAlone(t *testing.T) {
	f, err := LoadCSV(write(t, `name,pos,tier
Fake Alpha,RB,1
Fake Bravo,RB,1
Fake Charlie,WR,1
Fake Delta,WR,1
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.TiersWereGlobal {
		t.Error("tiers are already per-position; should not have been flagged global")
	}
}

func TestUsableTiers(t *testing.T) {
	// Mostly singletons — this is the FantasyCalc failure mode. Unusable.
	bad, err := LoadCSV(write(t, `name,pos,tier
Fake Alpha,RB,1
Fake Bravo,RB,2
Fake Charlie,RB,3
Fake Delta,RB,4
`))
	if err != nil {
		t.Fatal(err)
	}
	if bad.UsableTiers() {
		t.Error("all-singleton tiers must be rejected: they pin the board in cliff state")
	}

	good, err := LoadCSV(write(t, `name,pos,tier
Fake Alpha,RB,1
Fake Bravo,RB,1
Fake Charlie,RB,1
Fake Delta,RB,2
Fake Echo,RB,2
`))
	if err != nil {
		t.Fatal(err)
	}
	if !good.UsableTiers() {
		t.Error("well-sized tiers should be accepted")
	}
}

func TestLoadCSVNoNameColumn(t *testing.T) {
	if _, err := LoadCSV(write(t, "a,b,c\n1,2,3\n")); err == nil {
		t.Error("expected an error when no player-name column exists")
	}
}

// A file may carry rows with a name, a position and an opinion and no tier: the
// kicker and defense calls work that way, and so does a "still deciding" column
// on a hand-built board. Global renumbering must leave those rows alone.
//
// Left in the sort they land FIRST, because tier 0 is the smallest number, so
// they collected local tier 1 and pushed the file's real tier 1 down to 2. On a
// real fpl sheet that made eight parked defenders the top tier while the man
// written in the first column dropped behind them, and the cliff alarms went
// with them.
func TestGlobalRenumberingLeavesUntieredRowsAlone(t *testing.T) {
	rows := []Row{
		// Global numbering: gaps within each position, which is what triggers it.
		{Name: "alpha", Pos: "RB", Tier: 1, Rank: 1},
		{Name: "bravo", Pos: "WR", Tier: 2, Rank: 2},
		{Name: "charlie", Pos: "RB", Tier: 5, Rank: 3},
		{Name: "delta", Pos: "WR", Tier: 7, Rank: 4},
		// ...and opinions with no tier at all.
		{Name: "parked", Pos: "RB", Rank: 5},
		{Name: "hurt", Pos: "WR", Rank: 6},
	}
	if !normalizeTiers(rows) {
		t.Fatal("gapped per-position numbering was not detected as global")
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Name] = r.Tier
	}
	for name, want := range map[string]int{
		"alpha": 1, "charlie": 2, // rb 1,5 -> 1,2
		"bravo": 1, "delta": 2, // wr 2,7 -> 1,2
		"parked": 0, "hurt": 0, // no tier in, no tier out
	} {
		if got[name] != want {
			t.Errorf("%s renumbered to tier %d, want %d", name, got[name], want)
		}
	}
}
