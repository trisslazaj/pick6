package adp

import (
	"os"
	"path/filepath"
	"testing"
)

// The export's quirks, each one pinned by a row that has it.
//
// Every row below is COPIED verbatim out of the real 2025 export, values and
// all, because the point of the fixture is that the parse survives the shapes
// the file actually contains: a packed "Player (Bye)" field with runs of spaces,
// a defense whose team code is the literal string "DST", a team code sleeper
// spells differently, a platform column written as an em dash, and a deep row
// with no team and no bye at all. Inventing a row would test a file nobody has.
func TestLoadFantasyProsHandlesTheExportsShapes(t *testing.T) {
	path := writeFP(t, `Rank,Player (Bye),POS,Yahoo,Sleeper,RTSports,AVG,Real-Time
1,Ja'Marr Chase   CIN (10),WR1,1,1,1,1.0,1
10,Ashton Jeanty   LV (8),RB6,10,9,11,10.0,10
125,Denver Broncos DST   (12),DST1,94,147,142,127.7,107
253,Chris Rodriguez Jr.   JAC (8),RB73,—,—,184,184.0,200
387,John Ursua,WR136,—,371,—,371.0,—
`)

	f, err := LoadFantasyPros(path)
	if err != nil {
		t.Fatalf("loadfantasypros: %v", err)
	}
	if f.Rows != 5 {
		t.Fatalf("parsed %d rows, want 5", f.Rows)
	}
	// One row (john ursua) has no team code and the defense's is blanked on
	// purpose, so only the skill row counts as missing one.
	if f.NoTeam != 1 {
		t.Errorf("rows with no team = %d, want 1", f.NoTeam)
	}
	// Exactly one row leaves sleeper's column blank, and avg stands in for it.
	// Avg is never a fallback for itself.
	if got := f.Fallbacks[FPSleeper]; got != 1 {
		t.Errorf("sleeper fallbacks = %d, want 1", got)
	}
	if got := f.Fallbacks[FPAvg]; got != 0 {
		t.Errorf("avg fallbacks = %d, want 0 — avg cannot fall back to itself", got)
	}

	avg := f.Boards[FPAvg].Entries
	sleeper := f.Boards[FPSleeper].Entries
	if len(avg) != 5 || len(sleeper) != 5 {
		t.Fatalf("boards hold %d and %d entries, want 5 each — the two columns must "+
			"price the same players in the same order or their ranks are not comparable",
			len(avg), len(sleeper))
	}

	cases := []struct {
		name     string
		i        int
		wantName string
		wantPos  string
		wantTeam string
		wantBye  int
		wantAvg  float64
		wantSlp  float64
	}{
		{"the ordinary packed row", 0, "Ja'Marr Chase", "WR", "CIN", 10, 1.0, 1},
		{"multiple spaces before the team", 1, "Ashton Jeanty", "RB", "LV", 8, 10.0, 9},
		// The defense trap: the team name is the player name, "DST" is where the
		// code goes, and the pos suffix is stripped to DST then mapped to DEF.
		// Team is deliberately blank so the join goes by name.
		{"a defense", 2, "Denver Broncos", "DEF", "", 12, 127.7, 147},
		// Two things at once, as the real row has them: sleeper's column is an
		// em dash so avg stands in, and JAC is the one code fantasypros spells
		// differently from sleeper.
		{"a blank sleeper column, and jac becomes jax", 3,
			"Chris Rodriguez Jr.", "RB", "JAX", 8, 184.0, 184.0},
		// The deep tail: no team, no bye, and the split has to fail softly
		// rather than eating the name.
		{"no team and no bye", 4, "John Ursua", "WR", "", 0, 371.0, 371},
	}
	for _, c := range cases {
		got := avg[c.i]
		if got.Name != c.wantName || got.Pos != c.wantPos || got.Team != c.wantTeam || got.Bye != c.wantBye {
			t.Errorf("%s: row %d = %q/%q/%q bye %d, want %q/%q/%q bye %d",
				c.name, c.i, got.Name, got.Pos, got.Team, got.Bye,
				c.wantName, c.wantPos, c.wantTeam, c.wantBye)
		}
		if got.ADP != c.wantAvg {
			t.Errorf("%s: avg adp = %.2f, want %.2f", c.name, got.ADP, c.wantAvg)
		}
		if s := sleeper[c.i]; s.ADP != c.wantSlp || s.Name != c.wantName {
			t.Errorf("%s: sleeper column = %q at %.2f, want %q at %.2f",
				c.name, s.Name, s.ADP, c.wantName, c.wantSlp)
		}
		// The engine reads a missing stdev as "not reported" and falls back to
		// SigmaDefault. Anything non-zero here would be a number this file does
		// not contain.
		if got.Stdev != 0 || got.TimesDrafted != 0 || got.High != 0 || got.Low != 0 {
			t.Errorf("%s: row carries sample support the export does not have: "+
				"stdev %.2f, drafts %d, high %d, low %d",
				c.name, got.Stdev, got.TimesDrafted, got.High, got.Low)
		}
	}
}

// A file with no avg column cannot price anything, and a header that changed
// shape is worth an error rather than 389 silently empty rows.
func TestLoadFantasyProsRejectsAFileItCannotPrice(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no avg column", "Rank,Player (Bye),POS,Yahoo\n1,Ja'Marr Chase   CIN (10),WR1,1\n"},
		{"no pos column", "Rank,Player (Bye),AVG\n1,Ja'Marr Chase   CIN (10),1.0\n"},
		{"no player column", "Rank,POS,AVG\n1,WR1,1.0\n"},
	}
	for _, c := range cases {
		if _, err := LoadFantasyPros(writeFP(t, c.body)); err == nil {
			t.Errorf("%s: parsed without error, want a refusal", c.name)
		}
	}
}

// A row avg cannot price is dropped from BOTH boards, never from one. Two boards
// holding different players would have different within-position ranks, and the
// room warp is indexed by exactly that.
func TestLoadFantasyProsDropsUnpricedRowsFromEveryColumn(t *testing.T) {
	f, err := LoadFantasyPros(writeFP(t, `Rank,Player (Bye),POS,Sleeper,AVG
1,Ja'Marr Chase   CIN (10),WR1,1,1.0
2,Nobody At All   FA (5),WR2,7,—
`))
	if err != nil {
		t.Fatalf("loadfantasypros: %v", err)
	}
	if f.Rows != 1 || f.NoADP != 1 {
		t.Fatalf("parsed %d rows with %d unpriced, want 1 and 1", f.Rows, f.NoADP)
	}
	for _, c := range FPColumns {
		if n := len(f.Boards[c].Entries); n != 1 {
			t.Errorf("%s board holds %d entries, want 1", c, n)
		}
	}
}

func writeFP(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fp.csv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
