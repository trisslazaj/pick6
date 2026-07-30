package adp

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// FantasyPros' overall adp table, exported by hand as a csv, read here as a
// BACKTEST board and nothing else.
//
// WHY IT EXISTS. Every gate in this project has rested on one fold: the user's
// 2024 draft, scored against era-correct ffc 2024 prices. Ffc's archive has no
// 2025 — all four formats answer `{"status":"Error","errors":"No ADP data
// found."}`, rechecked 2026-07-30, while 2023 and 2024 both work — and the draft
// that matters most is a 2025 one: `1261824503076360192`, the same twelve
// managers this user drafts against in 2026. A hand-exported 2025 board is the
// only way to score it, and a second fold is the only way to tell a real edge
// from a lucky one.
//
// WHAT IT IS NOT: a price source for the live board. There is no `stdev`, no
// `times_drafted`, no `high`/`low`, so a fold priced off it runs every player on
// SigmaDefault and cannot referee SigmaMin, SigmaMax, the shrink or the support
// floor. It CAN referee the tilt, EBest, the conditioning, the room warp and the
// need weights, which is most of the engine. `pick6 calibrate` is the only
// caller and says which regime it is in on every run.
//
// WHERE THE FILE LIVES: not in this repo. It is FantasyPros' data and this is a
// public repository, so calibrate reads it out of the cache dir — see
// FantasyProsPath — and simply skips the fold when it is absent.
//
// THE SHAPE, measured on the 2025 export (389 data rows, 20 KB):
//
//	Rank, "Player (Bye)", POS, Yahoo, Sleeper, RTSports, AVG, Real-Time
//
//   - "Player (Bye)" packs three things: `Ja'Marr Chase   CIN (10)` is name, team
//     and bye week, with runs of spaces between them. 71 of the 389 rows carry no
//     team and no bye at all (`John Ursua`), so the split has to fail softly.
//   - POS carries a rank suffix — WR1, RB6, TE54, DST3 — and `DST` is this file's
//     spelling of DEF.
//   - Yahoo / Sleeper / RTSports / AVG are adp POSITIONS, not scores. The three
//     platform columns have blanks written as an em dash (Sleeper: 358 of 389
//     populated); AVG is populated on all 389 and carries one decimal.
//   - Position counts: WR 137, RB 103, TE 54, QB 42, K 23, DST 30.
//   - Era-verified before trusting any of it: Ashton Jeanty rank 10, Cam Ward
//     160, Travis Hunter 71 (2025 rookies), Marvin Harrison Jr. 39 in his second
//     year, Ja'Marr Chase 1. Unambiguously the 2025 preseason board.
//   - The user confirms the export is 0.5 ppr, which is the league's own scoring,
//     so there is no format mismatch to correct for.
//
// THE DEFENSE TRAP, and it is worth the paragraph: defenses here carry the TEAM
// NAME in the name field ("Denver Broncos") and the literal string "DST" where a
// team code would go. The project's usual join-defenses-on-team-abbreviation path
// therefore cannot fire at all, and taking the file at its word would hand
// `rankings` a team code of "DST" for all 30 of them. Matching them on the
// normalized team NAME instead — sleeper's def entries have full_name null and
// carry city/nickname in first_name/last_name — takes the join from 91.8% to
// 99.5%.

// FPColumn names one of the export's adp columns.
//
// Both are exposed because scoring them separately answers a real question:
// does sleeper's own adp predict a SLEEPER draft better than the cross-platform
// consensus does? The two prices sit on the same rows, so carrying both costs
// one extra float per player and one extra model column.
type FPColumn string

const (
	// FPAvg is the cross-platform consensus and the default. It is populated on
	// every row, which is the property that makes board membership identical
	// whichever column prices it.
	FPAvg FPColumn = "avg"
	// FPSleeper is sleeper's own adp — the platform the scored drafts were
	// actually run on, and blank on 31 of 389 rows.
	FPSleeper FPColumn = "sleeper"
)

// FPColumns are the columns every parse produces a board for, in report order.
var FPColumns = []FPColumn{FPAvg, FPSleeper}

// ParseFPColumn resolves a flag value. Unknown columns are an error rather than
// a silent fall back to avg: a typo that quietly prices the fold off a different
// column would move every number in the report without saying so.
func ParseFPColumn(s string) (FPColumn, error) {
	for _, c := range FPColumns {
		if strings.EqualFold(s, string(c)) {
			return c, nil
		}
	}
	return "", fmt.Errorf("unknown fantasypros adp column %q — pick avg or sleeper", s)
}

// FantasyProsFile is one parsed export: the same players, priced once per column.
//
// Boards share their entry ORDER as well as their membership, because the room
// warp ranks players by adp within a position and two boards holding different
// players would not be comparable at all.
type FantasyProsFile struct {
	Path   string
	Rows   int // data rows that produced a board entry
	NoTeam int // rows whose name field carried no team code (free agents, deep names)
	NoADP  int // rows dropped entirely: not even avg had a price for them

	Boards    map[FPColumn]FFCResult
	Fallbacks map[FPColumn]int // rows where that column was blank and avg stood in
}

// LoadFantasyPros parses an export into one board per column.
//
// It returns FFCResult because that is this package's name for "one era's worth
// of adp, in pick units" and the backtest's era-board loader already consumes
// exactly that shape. A second near-identical struct would buy nothing but a
// conversion at every call site. TotalDrafts and Window stay zero and empty:
// the export carries neither, and inventing a snapshot window would let the
// era check claim a match it cannot know about.
func LoadFantasyPros(path string) (*FantasyProsFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // exported files are ragged often enough
	r.TrimLeadingSpace = true

	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header of %s: %w", path, err)
	}
	col := fpHeaders(head)
	for _, want := range []string{"player", "pos", string(FPAvg)} {
		if _, ok := col[want]; !ok {
			return nil, fmt.Errorf("%s has no %s column (saw: %s)",
				path, want, strings.Join(head, ", "))
		}
	}

	out := &FantasyProsFile{
		Path:      path,
		Boards:    map[FPColumn]FFCResult{},
		Fallbacks: map[FPColumn]int{},
	}
	entries := map[FPColumn][]Entry{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name, team, bye := splitFPPlayer(fpField(rec, col, "player"))
		pos := rankings.NormalizePos(stripFPRank(fpField(rec, col, "pos")))
		if name == "" || pos == "" {
			continue
		}
		// Avg gates the whole row, for both columns, and it gates it before
		// anything else is counted. A row it cannot price is a row nothing can
		// price, and dropping it here rather than per column is what keeps the
		// two boards holding the same players in the same order.
		avg := fpNumber(fpField(rec, col, string(FPAvg)))
		if avg <= 0 {
			out.NoADP++
			continue
		}

		if pos == "DEF" {
			// The team column that isn't one. This file writes the literal "DST"
			// where a code belongs, so passing it through would look like a real
			// abbreviation and miss all 30. Blank it and let the name carry the
			// join — see the defense trap above.
			team = ""
		} else {
			team = fixFPTeam(team)
			if team == "" {
				out.NoTeam++
			}
		}
		out.Rows++
		for _, c := range FPColumns {
			adp := fpNumber(fpField(rec, col, string(c)))
			if adp <= 0 {
				// The platform never ranked him. Falling back to the consensus
				// keeps him on the board at a defensible price; counting it is
				// what stops "sleeper's adp" quietly meaning "avg" for a chunk
				// of the deep board.
				adp = avg
				if c != FPAvg {
					out.Fallbacks[c]++
				}
			}
			entries[c] = append(entries[c], Entry{
				Name: name,
				Pos:  pos,
				Team: team,
				ADP:  adp,
				Bye:  bye,
				// Stdev, TimesDrafted, High and Low stay zero: the export has no
				// column for any of them. Zero here is read downstream as "not
				// reported", which is exactly what it is.
			})
		}
	}

	for _, c := range FPColumns {
		out.Boards[c] = FFCResult{
			Format:  "fantasypros-" + string(c),
			Entries: entries[c],
		}
	}
	return out, nil
}

// FantasyProsPath is where a season's export is expected to sit.
//
// The cache dir, deliberately, and not the repo: the file is FantasyPros' data
// and this repository is public. A missing file is not an error condition — it
// means that season's fold is skipped, which is the state every machine except
// this one is in.
func FantasyProsPath(year int) (string, error) {
	dir, err := cache.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("fantasypros_adp_%d.csv", year)), nil
}

// FantasyProsMissing is the one message a reader gets when the file is absent.
// It names the file it wanted and where to get it, because "no era adp" on its
// own sends the reader off to recheck ffc's archive, which is not the answer.
func FantasyProsMissing(year int, path string) string {
	return fmt.Sprintf("no %d board · ffc's archive has none and %s is not there. "+
		"export the overall adp table from fantasypros.com/nfl/adp/overall.php, "+
		"save it at that path, and the %d fold scores", year, path, year)
}

// fpPlayerRe splits `Ja'Marr Chase   CIN (10)` into name, team and bye. Rows
// with no team and no bye simply do not match, which is the soft failure the
// deep end of the file needs — 71 of 389 rows are that shape.
var fpPlayerRe = regexp.MustCompile(`^(.*?)\s+([A-Z]{2,3})\s*\((\d+)\)\s*$`)

func splitFPPlayer(s string) (name, team string, bye int) {
	s = strings.TrimSpace(s)
	m := fpPlayerRe.FindStringSubmatch(s)
	if m == nil {
		return s, "", 0
	}
	bye, _ = strconv.Atoi(m[3])
	return strings.TrimSpace(m[1]), m[2], bye
}

// fpTeamFixes are the codes fantasypros spells differently from sleeper. Only
// JAC fires on the 2025 export (8 rows); LA and WSH are here because they are
// the other two codes that differ between the two vocabularies and a future
// export using them would otherwise drop every player on those teams.
var fpTeamFixes = map[string]string{
	"JAC": "JAX",
	"LA":  "LAR",
	"WSH": "WAS",
}

func fixFPTeam(team string) string {
	team = strings.ToUpper(strings.TrimSpace(team))
	if fixed, ok := fpTeamFixes[team]; ok {
		return fixed
	}
	return team
}

// stripFPRank turns "WR12" into "WR" and "DST3" into "DST".
func stripFPRank(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "0123456789")
}

// fpNumber parses one adp cell. A blank or an em dash — how the export writes
// "this platform did not rank him" — comes back as 0, which the caller reads as
// "no price from this column".
func fpNumber(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// fpHeaders maps the export's headers onto the fields we read. The player column
// is matched by prefix because its header is literally "Player (Bye)"; the rest
// are exact. First writer wins, so a second column with the same name is ignored
// rather than silently replacing the first.
func fpHeaders(head []string) map[string]int {
	col := map[string]int{}
	set := func(field string, i int) {
		if _, taken := col[field]; !taken {
			col[field] = i
		}
	}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.HasPrefix(h, "player"):
			set("player", i)
		case h == "pos" || h == "position":
			set("pos", i)
		case h == "avg" || h == "avg." || h == "average":
			set(string(FPAvg), i)
		case h == "sleeper":
			set(string(FPSleeper), i)
		}
	}
	return col
}

func fpField(rec []string, col map[string]int, field string) string {
	i, ok := col[field]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}
