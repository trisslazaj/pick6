// Package rankings handles name normalization and matching source players onto
// Sleeper player ids.
//
// Measured on 2026-07-28: FFC -> Sleeper matches 233/233 with normalization plus
// the defense special case. Levenshtein is kept for the messier sources but does
// not fire on FFC at all.
package rankings

import (
	"sort"
	"strings"
	"unicode"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

var suffixes = map[string]bool{
	"jr": true, "sr": true, "ii": true, "iii": true, "iv": true, "v": true,
}

// Normalize lowercases, strips accents, punctuation and generational suffixes,
// and collapses whitespace. "Marvin Harrison Jr." -> "marvin harrison".
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(ligatures.Replace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(fold(r))
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
		// everything else (. ' ` - ,) is dropped
	}
	fields := strings.Fields(b.String())
	kept := fields[:0]
	for _, f := range fields {
		if !suffixes[f] {
			kept = append(kept, f)
		}
	}
	// Guard against a name that is nothing but a suffix.
	if len(kept) == 0 {
		return strings.Join(fields, " ")
	}
	return strings.Join(kept, " ")
}

// accents covers the latin letters that actually show up in NFL rosters.
// Not a general unicode folder; doesn't need to be.
var accents = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
	'ñ': 'n', 'ý': 'y', 'ÿ': 'y', 'ç': 'c', 'š': 's',
}

func fold(r rune) rune {
	if a, ok := accents[r]; ok {
		return a
	}
	if a, ok := letters[r]; ok {
		return a
	}
	return r
}

// letters are the characters that are not an accented latin base and so survive
// the accents table untouched: a slashed o is its own letter, not an o with a
// mark on it, and neither is a dotless i or a turkish g-breve. They reached the
// board through fpl, where Ødegaard, Kadıoğlu and Groß are all inside the top
// 400 and a hand-typed sheet spells every one of them without the diacritic.
var letters = map[rune]rune{
	'ø': 'o', 'ı': 'i', 'ğ': 'g', 'ş': 's', 'đ': 'd', 'ð': 'd', 'ł': 'l', 'ħ': 'h',
}

// ligatures are the folds that are one character in and two out, which a rune
// map cannot express.
var ligatures = strings.NewReplacer(
	"ß", "ss", "Ø", "O", "Æ", "AE", "æ", "ae", "Œ", "OE", "œ", "oe", "þ", "th", "Þ", "TH",
)

// Key is what we match on: normalized name plus position.
type Key struct {
	Name string
	Pos  string
}

// Index is a lookup built from the Sleeper pool.
type Index struct {
	byKey  map[Key]string    // normalized name + pos -> player_id
	byTeam map[string]string // team abbrev -> DEF player_id
	// byDefName is the same 32 defenses keyed on their normalized TEAM NAME,
	// for sources that give one instead of a code. See LookupExact.
	byDefName map[string]string
	pool      map[string]sleeper.Player
}

// NewIndex builds the lookup. Defenses are indexed by team abbreviation and by
// team name — in the Sleeper dump all 32 have full_name: null and carry the city
// and nickname in first_name/last_name ("Denver"/"Broncos"), so a name-based
// matcher aimed at Name() reaches them and one aimed at FullName never does.
//
// Ids are SORTED before the loop, and that is not tidiness. "First writer wins"
// over a Go map range means "whichever entry this process happened to visit
// first", so a colliding (name, position) key resolved differently run to run.
// Measured on the current 3,223-player active pool there are 8 such keys —
// "nick williams" WR (1604 / 11530), "chase cota" WR (11347 / 11554), "frank
// gore" RB, and five more — and rebuilding the index 20 times in one process
// flipped them 62 times between the two ids. The 2024 ffc board hits none of
// them; the 389-name 2025 fantasypros board hits two, and a 389-deep board
// against a 192-pick draft makes deep names draftable. If the wrong twin is
// looked up, his id never appears in the picks, he is labeled "survived forever"
// at every vantage of all twelve seats, and the fold's brier moves between runs
// with the join count unchanged at 387/389. Sorted, the lowest id wins, always.
func NewIndex(pool map[string]sleeper.Player) *Index {
	ix := &Index{
		byKey:     make(map[Key]string, len(pool)),
		byTeam:    make(map[string]string, 32),
		byDefName: make(map[string]string, 32),
		pool:      pool,
	}
	ids := make([]string, 0, len(pool))
	for id := range pool {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := pool[id]
		if p.Position == "DEF" {
			ix.byTeam[strings.ToUpper(p.Team)] = id
			ix.byDefName[Normalize(p.Name())] = id
			continue
		}
		k := Key{Normalize(p.Name()), p.Position}
		// First writer wins, and after the sort the first writer is a fact about
		// the pool rather than about this run.
		if _, seen := ix.byKey[k]; !seen {
			ix.byKey[k] = id
		}
	}
	return ix
}

// Lookup resolves a source player to a Sleeper player_id, falling back to
// Levenshtein when the name doesn't match exactly.
// Pass team for defenses; it is ignored for everyone else.
func (ix *Index) Lookup(name, pos, team string) (string, bool) {
	if id, ok := ix.LookupExact(name, pos, team); ok {
		return id, true
	}
	// Defenses never reach the fuzzy stage in practice — byKey holds none of
	// them — so this is a plain miss for DEF and the fallback for everyone else.
	return ix.fuzzy(Normalize(name), NormalizePos(pos))
}

// LookupExact is Lookup minus the fuzzy fallback: same position normalization,
// same defense-by-team join, same exact key, then a miss.
//
// It exists for the backtester, which joins a 2024 source against the *current*
// active Sleeper pool. Anyone retired or cut since is simply absent from that
// pool, and Levenshtein would hand back a similarly-named active player whose
// id never appears in the 2024 picks — so he'd be labeled "survived forever"
// and quietly bias every calibration number. A name with no exact match is one
// to print and drop, not one to guess at.
//
// Defenses get two exact paths, tried in that order, because sources disagree
// about what identifies one. FFC gives a team code and a name nobody can match
// ("Seattle Defense"), so the code is the join and always has been. FantasyPros'
// adp export gives the reverse: the team NAME in the name field ("Denver
// Broncos") and the literal string "DST" where a code belongs, which resolves to
// nothing at all. Falling through to the normalized team name catches those 30
// rows and took that join from 91.8% to 99.5%. It cannot change an existing
// answer — the code path still wins whenever it hits — and it is still exact.
func (ix *Index) LookupExact(name, pos, team string) (string, bool) {
	pos = NormalizePos(pos)
	if pos == "DEF" {
		if id, ok := ix.byTeam[strings.ToUpper(team)]; ok {
			return id, true
		}
		id, ok := ix.byDefName[Normalize(name)]
		return id, ok
	}
	id, ok := ix.byKey[Key{Normalize(name), pos}]
	return id, ok
}

// fuzzy is the Levenshtein <= 2 fallback, scoped to the same position.
//
// Ties break on the lowest id for the same reason NewIndex sorts: this ranges a
// map, so "the first one at distance 2" is whichever the runtime visited first,
// and two equally-close names would resolve differently run to run. This is on
// the shipped `fetch` path, where the answer is written into mapping.json and
// then trusted forever.
func (ix *Index) fuzzy(norm, pos string) (string, bool) {
	best, bestDist := "", 3
	for k, id := range ix.byKey {
		if k.Pos != pos {
			continue
		}
		// Cheap length gate before the expensive part.
		if abs(len(k.Name)-len(norm)) > 2 {
			continue
		}
		d := levenshtein(k.Name, norm)
		if d < bestDist || (d == bestDist && id < best) {
			best, bestDist = id, d
		}
	}
	return best, best != ""
}

// NormalizePos maps source position codes onto Sleeper's.
// FFC uses PK for kickers; MFL uses Def/PK variants.
func NormalizePos(p string) string {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "PK", "K":
		return "K"
	case "DEF", "DST", "D/ST", "TEAM DEFENSE":
		return "DEF"
	default:
		return strings.ToUpper(strings.TrimSpace(p))
	}
}

// Player returns the Sleeper record for an id.
func (ix *Index) Player(id string) (sleeper.Player, bool) {
	p, ok := ix.pool[id]
	return p, ok
}

// Authoritative returns Sleeper's version of a player's identity. Sources
// disagree on team codes (MFL says LVR where Sleeper says LV) and name defenses
// however they like, so Sleeper wins once we've resolved an id.
func (ix *Index) Authoritative(id string) (name, pos, team string, bye int, ok bool) {
	p, ok := ix.pool[id]
	if !ok {
		return "", "", "", 0, false
	}
	return p.Name(), p.Position, p.Team, p.ByeWeek, true
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
