// Package rankings handles name normalization and matching source players onto
// Sleeper player ids.
//
// Measured on 2026-07-28: FFC -> Sleeper matches 233/233 with normalization plus
// the defense special case. Levenshtein is kept for the messier sources but does
// not fire on FFC at all.
package rankings

import (
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
	for _, r := range strings.ToLower(s) {
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
	return r
}

// Key is what we match on: normalized name plus position.
type Key struct {
	Name string
	Pos  string
}

// Index is a lookup built from the Sleeper pool.
type Index struct {
	byKey  map[Key]string    // normalized name + pos -> player_id
	byTeam map[string]string // team abbrev -> DEF player_id
	pool   map[string]sleeper.Player
}

// NewIndex builds the lookup. Defenses are indexed by team abbreviation only —
// in the Sleeper dump all 32 have full_name: null, so name matching cannot reach them.
func NewIndex(pool map[string]sleeper.Player) *Index {
	ix := &Index{
		byKey:  make(map[Key]string, len(pool)),
		byTeam: make(map[string]string, 32),
		pool:   pool,
	}
	for id, p := range pool {
		if p.Position == "DEF" {
			ix.byTeam[strings.ToUpper(p.Team)] = id
			continue
		}
		k := Key{Normalize(p.Name()), p.Position}
		// First writer wins; the pool is already filtered to active players so
		// genuine duplicate name+position collisions are vanishingly rare.
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
func (ix *Index) LookupExact(name, pos, team string) (string, bool) {
	pos = NormalizePos(pos)
	if pos == "DEF" {
		id, ok := ix.byTeam[strings.ToUpper(team)]
		return id, ok
	}
	id, ok := ix.byKey[Key{Normalize(name), pos}]
	return id, ok
}

// fuzzy is the Levenshtein <= 2 fallback, scoped to the same position.
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
		if d < bestDist {
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
