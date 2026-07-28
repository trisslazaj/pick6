package rankings

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Row is one line of a user-supplied rankings file.
type Row struct {
	Name   string
	Pos    string
	Team   string
	Tier   int
	Points float64
	ADP    float64
	Rank   int
}

// File is a loaded rankings file plus what we learned about it.
type File struct {
	Rows []Row

	HasTier   bool
	HasPoints bool
	HasADP    bool

	// TiersWereGlobal is set when the source numbered tiers across all positions
	// at once (an "overall" ranking export) and we renumbered them per position.
	TiersWereGlobal bool
}

// header aliases, lowercased. Covers the generic format plus FantasyPros exports,
// which vary between their overall and positional files.
var headerAliases = map[string][]string{
	"name":   {"name", "player", "player name", "playername", "full name"},
	"pos":    {"pos", "position"},
	"team":   {"team", "tm"},
	"tier":   {"tier", "tiers"},
	"points": {"points", "pts", "fpts", "proj", "proj pts", "proj. pts", "projection", "projected points", "fantasy points"},
	"adp":    {"adp", "avg pick", "average draft position", "avg. pick"},
	"rank":   {"rank", "rk", "#", "overall"},
}

// LoadCSV reads a rankings file. Column order doesn't matter and unknown columns
// are ignored, so FantasyPros exports and hand-rolled files both work.
func LoadCSV(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // ragged rows are common in exported files
	r.TrimLeadingSpace = true

	head, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	col := mapHeaders(head)
	if _, ok := col["name"]; !ok {
		return nil, fmt.Errorf("no player-name column found in %s (saw: %s)",
			path, strings.Join(head, ", "))
	}

	out := &File{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := Row{
			Name: get(rec, col, "name"),
			Pos:  NormalizePos(stripDigits(get(rec, col, "pos"))),
			Team: strings.ToUpper(get(rec, col, "team")),
		}
		if row.Name == "" {
			continue
		}
		// FantasyPros sometimes packs the team into the name: "Ja'Marr Chase CIN".
		if row.Team == "" {
			if n, tm := splitTrailingTeam(row.Name); tm != "" {
				row.Name, row.Team = n, tm
			}
		}
		row.Tier = atoi(get(rec, col, "tier"))
		row.Points = atof(get(rec, col, "points"))
		row.ADP = atof(get(rec, col, "adp"))
		row.Rank = atoi(get(rec, col, "rank"))

		out.HasTier = out.HasTier || row.Tier > 0
		out.HasPoints = out.HasPoints || row.Points > 0
		out.HasADP = out.HasADP || row.ADP > 0
		out.Rows = append(out.Rows, row)
	}

	if out.HasTier {
		out.TiersWereGlobal = normalizeTiers(out.Rows)
	}
	return out, nil
}

// normalizeTiers converts cross-position ("overall") tier numbering into
// per-position tiers, and reports whether it had to.
//
// This matters because cliff detection is a per-position question. An overall
// ranking file numbers tiers across every position at once, so "tier 3" might
// hold one running back and two receivers — counting "how many RBs are left in
// this tier" against that number is meaningless.
func normalizeTiers(rows []Row) bool {
	// Detect by looking for gaps, not by checking whether a tier number spans
	// positions — it always does. Per-position numbering restarts at 1 for every
	// position and runs 1..k with no holes. Global numbering leaves holes in each
	// position, because the missing numbers belong to other positions: an overall
	// file gives running backs tiers 1, 2, 5, 7, 8, 12 and so on.
	tiersOf := map[string]map[int]bool{}
	for _, r := range rows {
		if r.Tier == 0 || r.Pos == "" {
			continue
		}
		if tiersOf[r.Pos] == nil {
			tiersOf[r.Pos] = map[int]bool{}
		}
		tiersOf[r.Pos][r.Tier] = true
	}
	global := false
	for _, set := range tiersOf {
		max := 0
		for t := range set {
			if t > max {
				max = t
			}
		}
		if max > len(set) {
			global = true
			break
		}
	}
	if !global {
		return false
	}

	// Renumber: within each position, walk in source order and start a new local
	// tier every time the source's tier number changes. This preserves the
	// source's grouping while making the counts per-position.
	idx := map[string][]int{} // pos -> row indexes, in source order
	for i, r := range rows {
		if r.Pos != "" {
			idx[r.Pos] = append(idx[r.Pos], i)
		}
	}
	for _, is := range idx {
		sort.SliceStable(is, func(a, b int) bool {
			ra, rb := rows[is[a]], rows[is[b]]
			if ra.Tier != rb.Tier {
				return ra.Tier < rb.Tier
			}
			return ra.Rank < rb.Rank
		})
		local, prev := 0, -1
		for _, i := range is {
			if rows[i].Tier != prev {
				local++
				prev = rows[i].Tier
			}
			rows[i].Tier = local
		}
	}
	return true
}

// TierSizes reports players per tier per position, for sanity-checking a file.
func (f *File) TierSizes() map[string][]int {
	counts := map[string]map[int]int{}
	for _, r := range f.Rows {
		if r.Tier == 0 || r.Pos == "" {
			continue
		}
		if counts[r.Pos] == nil {
			counts[r.Pos] = map[int]int{}
		}
		counts[r.Pos][r.Tier]++
	}
	out := map[string][]int{}
	for pos, m := range counts {
		sizes := make([]int, 0, len(m))
		for _, n := range m {
			sizes = append(sizes, n)
		}
		sort.Ints(sizes)
		out[pos] = sizes
	}
	return out
}

// UsableTiers reports whether the file's tiers are granular enough to drive
// cliff detection. Tiers that are mostly singletons would leave the board stuck
// in permanent cliff state, so we'd rather fall back to deriving them.
func (f *File) UsableTiers() bool {
	if !f.HasTier {
		return false
	}
	var all []int
	for _, sizes := range f.TierSizes() {
		all = append(all, sizes...)
	}
	if len(all) == 0 {
		return false
	}
	singles := 0
	for _, n := range all {
		if n == 1 {
			singles++
		}
	}
	return float64(singles)/float64(len(all)) < 0.5
}

func mapHeaders(head []string) map[string]int {
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(h))
		for field, aliases := range headerAliases {
			if _, taken := col[field]; taken {
				continue
			}
			for _, a := range aliases {
				if h == a {
					col[field] = i
				}
			}
		}
	}
	return col
}

func get(rec []string, col map[string]int, field string) string {
	i, ok := col[field]
	if !ok || i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

// stripDigits turns "RB12" into "RB" — FantasyPros positional files number them.
func stripDigits(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "0123456789")
}

// splitTrailingTeam pulls "CIN" off the end of "Ja'Marr Chase CIN".
func splitTrailingTeam(name string) (string, string) {
	fields := strings.Fields(name)
	if len(fields) < 2 {
		return name, ""
	}
	last := fields[len(fields)-1]
	if len(last) < 2 || len(last) > 3 || last != strings.ToUpper(last) {
		return name, ""
	}
	for _, r := range last {
		if r < 'A' || r > 'Z' {
			return name, ""
		}
	}
	return strings.Join(fields[:len(fields)-1], " "), last
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atof(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
