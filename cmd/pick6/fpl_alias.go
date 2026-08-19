package main

import (
	"sort"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/fpl"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// aliasIndex resolves the names a person writes down to the players fpl ships.
//
// It exists because fpl's web_name is a DISPLAY name and frequently is not the
// surname: Pau Torres is "Pau", Rúben Dias is "Rúben", Yeremy Pino is "Yeremy",
// Georginio Rutter is "Georginio". A hand-written board that says Torres, Dias,
// Pino and Rutter is reading the same players off a different column, and a
// join that only knows web_name reports every one of them unmatched.
//
// Every alias here is an EXACT string after normalising. There is no edit
// distance and there never should be: this board is one-word surnames and three
// different men are called Wilson. What makes the extra keys safe instead of
// reckless is that any key two players could claim is deleted outright, so an
// ambiguous name is reported rather than guessed at.
type aliasIndex struct {
	// exact is keyed by "name|POS" and by "name|POS|TEAM"; alias is the looser
	// keyspace (surname, full name, squashed initials) and is consulted only
	// after exact has missed.
	exact map[string]*adp.Player
	alias map[string]*adp.Player
	// Ambiguity is tracked PER MAP. Sharing one set let a weak alias collision
	// delete a strong exact match: two defenders answer to "james" once first
	// names are aliased (Reece James and James Tarkowski), and Reece — whose
	// printed name simply IS James — went with it.
	dupExact map[string]bool
	dupAlias map[string]bool
	// candidates by surname+position, with the first name fpl has for each, so a
	// sheet that writes "I Jesus" can be told apart from G.Jesus by the initial
	// it put there for exactly that reason.
	bySurname map[string][]surnameCand
	// positions the board holds, for the wrong-position report.
	positions []string
	// everyone fpl ships, filtered or not, so a miss can tell "this man is not
	// in the game" from "I could not resolve the name". The first is a row to
	// delete; the second is a row to fix.
	inGame map[string]bool
	// every board name by position, for suggesting a correction on a miss.
	namesByPos map[string][]string
}

func newAliasIndex(players map[string]*adp.Player, pool []fpl.Element, bs *fpl.Bootstrap) *aliasIndex {
	ix := &aliasIndex{
		exact:      map[string]*adp.Player{},
		alias:      map[string]*adp.Player{},
		dupExact:   map[string]bool{},
		dupAlias:   map[string]bool{},
		bySurname:  map[string][]surnameCand{},
		inGame:     map[string]bool{},
		namesByPos: map[string][]string{},
	}
	if bs != nil {
		for _, e := range bs.Elements {
			for _, k := range []string{e.WebName, e.SecondName, e.FirstName} {
				if n := rankings.Normalize(k); n != "" {
					ix.inGame[n] = true
					if fs := strings.Fields(n); len(fs) > 1 {
						ix.inGame[fs[0]] = true
						ix.inGame[fs[len(fs)-1]] = true
					}
				}
			}
		}
	}
	// Deterministic order, so a key two players claim is always resolved the
	// same way before being deleted for being ambiguous.
	ids := make([]string, 0, len(players))
	for id := range players {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	byID := make(map[string]fpl.Element, len(pool))
	for _, e := range pool {
		byID[itoa(e.ID)] = e
	}

	put := func(m map[string]*adp.Player, dup map[string]bool, key string, p *adp.Player) {
		if prev, seen := m[key]; seen && prev != p {
			dup[key] = true
		}
		m[key] = p
	}
	for _, id := range ids {
		p := players[id]
		n := rankings.Normalize(p.Name)
		if n == "" {
			continue
		}
		e := byID[id]
		put(ix.exact, ix.dupExact, n+"|"+p.Pos, p)
		put(ix.exact, ix.dupExact, n+"|"+p.Pos+"|"+strings.ToUpper(p.Team), p)

		first := rankings.Normalize(e.FirstName)
		for _, a := range aliasesFor(n, e, bs) {
			if a == "" || a == n {
				continue
			}
			put(ix.alias, ix.dupAlias, a+"|"+p.Pos, p)
			k := a + "|" + p.Pos
			ix.bySurname[k] = append(ix.bySurname[k], surnameCand{p: p, first: first})
		}
		k := n + "|" + p.Pos
		ix.bySurname[k] = append(ix.bySurname[k], surnameCand{p: p, first: first})
		ix.namesByPos[p.Pos] = append(ix.namesByPos[p.Pos], n)
	}
	seenPos := map[string]bool{}
	for _, p := range players {
		if !seenPos[p.Pos] {
			seenPos[p.Pos] = true
			ix.positions = append(ix.positions, p.Pos)
		}
	}
	sort.Strings(ix.positions)
	for k := range ix.dupExact {
		delete(ix.exact, k)
	}
	for k := range ix.dupAlias {
		delete(ix.alias, k)
	}
	return ix
}

// surnameCand is one player a surname could mean, with the first name fpl gives
// him so an initial can choose between them.
type surnameCand struct {
	p     *adp.Player
	first string
}

// byInitial picks the one candidate whose first name starts with the letter the
// sheet supplied. Returns nil unless exactly one does.
func (ix *aliasIndex) byInitial(surname, pos, initial string) *adp.Player {
	var hit *adp.Player
	for _, c := range ix.bySurname[surname+"|"+pos] {
		if strings.HasPrefix(c.first, initial) {
			if hit != nil && hit != c.p {
				return nil
			}
			hit = c.p
		}
	}
	return hit
}

// aliasesFor is every other string a person might write for this player.
func aliasesFor(webName string, e fpl.Element, bs *fpl.Bootstrap) []string {
	var out []string
	// The surname behind an initial: "J.Timber" is findable as "Timber". The dot
	// is already gone by the time Normalize is done, so split the raw name.
	if raw := e.WebName; strings.Contains(raw, ".") {
		if i := strings.LastIndex(raw, "."); i >= 0 && i+1 < len(raw) {
			out = append(out, rankings.Normalize(raw[i+1:]))
		}
	}
	// The last word of a printed name: fpl shows "João Pedro" and "Igor Jesus"
	// and "Pedro Porro" in full, and a sheet writes Pedro, Jesus and Porro.
	if fs := strings.Fields(rankings.Normalize(webName)); len(fs) > 1 {
		out = append(out, fs[len(fs)-1], fs[0])
	}
	// The name fpl does not print: "Pau" is Pau Torres and "Yeremy" is Yeremy
	// Pino, so the surname lives in second_name and nowhere on screen. Both ends
	// of it, because it is as often "Pino Santos" as "Porro Sauceda" and the
	// sheet writes whichever half a commentator says.
	if e.SecondName != "" {
		second := rankings.Normalize(e.SecondName)
		out = append(out, second)
		if fs := strings.Fields(second); len(fs) > 1 {
			out = append(out, fs[0], fs[len(fs)-1])
		}
	}
	// The first name on its own: Alisson Becker is printed "A.Becker", and
	// everybody calls him Alisson.
	if e.FirstName != "" {
		first := rankings.Normalize(e.FirstName)
		out = append(out, first)
		if fs := strings.Fields(first); len(fs) > 1 {
			out = append(out, fs[len(fs)-1])
		}
	}
	// The nickname, which fpl hides in single quotes inside first_name:
	// "Rodrigo 'Rodri'". Normalize strips the quotes, so pull it out first.
	if a, b := strings.Index(e.FirstName, "'"), strings.LastIndex(e.FirstName, "'"); a >= 0 && b > a+1 {
		out = append(out, rankings.Normalize(e.FirstName[a+1:b]))
	}
	if e.FirstName != "" && e.SecondName != "" {
		out = append(out, rankings.Normalize(e.FirstName+" "+e.SecondName))
	}
	// "A Silva" written with a space where fpl writes "A.Silva": normalising
	// keeps the space and drops the dot, so the two differ by exactly that.
	out = append(out, strings.ReplaceAll(webName, " ", ""))
	return out
}

// find resolves one csv row. The returned string is a note for the unmatched
// report, empty when the miss has nothing to add.
func (ix *aliasIndex) find(name, pos, team string) (*adp.Player, string) {
	n := rankings.Normalize(name)
	if n == "" {
		return nil, ""
	}
	if team != "" {
		if p, ok := ix.exact[n+"|"+pos+"|"+strings.ToUpper(team)]; ok {
			return p, ""
		}
	}
	if p, ok := ix.exact[n+"|"+pos]; ok {
		return p, ""
	}
	if p, ok := ix.alias[n+"|"+pos]; ok {
		return p, ""
	}
	// Squashed, for the initial written with a space.
	if sq := strings.ReplaceAll(n, " ", ""); sq != n {
		if p, ok := ix.exact[sq+"|"+pos]; ok {
			return p, ""
		}
		if p, ok := ix.alias[sq+"|"+pos]; ok {
			return p, ""
		}
	}
	// A leading single letter is a disambiguator the sheet added, not part of
	// the name fpl prints: a hand board writes "H Wilson" because three men are
	// called Wilson, while fpl writes plain "Wilson" and lets the position tell
	// them apart. Dropping it is safe precisely because the position still has
	// to match and an ambiguous key was already deleted.
	if bare := dropInitial(n); bare != "" {
		if p, ok := ix.exact[bare+"|"+pos]; ok {
			return p, ""
		}
		if p, ok := ix.alias[bare+"|"+pos]; ok {
			return p, ""
		}
	}
	// The initial the sheet supplied is there to disambiguate; use it as such.
	// "I Jesus" is Igor and not G.Jesus, "M Sangaré" is Mamadou and not Ibrahim.
	if fs := strings.Fields(n); len(fs) > 1 && len(fs[0]) == 1 {
		if p := ix.byInitial(strings.Join(fs[1:], " "), pos, fs[0]); p != nil {
			return p, ""
		}
	}
	for _, key := range []string{n, dropInitial(n)} {
		if key != "" && (ix.dupExact[key+"|"+pos] || ix.dupAlias[key+"|"+pos]) {
			return nil, " — more than one, add the team column"
		}
	}
	// Found, but filed under a different position. Worth saying out loud: it is
	// almost always the sheet and fpl disagreeing about whether a wing-back is a
	// defender, and the reader can settle it in one edit.
	for _, key := range []string{n, dropInitial(n)} {
		if key == "" {
			continue
		}
		for _, other := range ix.positions {
			if other == pos {
				continue
			}
			if p, ok := ix.exact[key+"|"+other]; ok {
				return nil, " — fpl has " + strings.ToLower(p.Name) + " as " + strings.ToLower(other)
			}
			if p, ok := ix.alias[key+"|"+other]; ok {
				return nil, " — fpl has " + strings.ToLower(p.Name) + " as " + strings.ToLower(other)
			}
		}
	}
	// A near-miss is almost always a typo, and saying so beats saying "not in
	// the game" about a man who is very much in it: "M Fernanades" is Matheus
	// Fernandes, one letter away and playing for spurs. The distance is used to
	// SUGGEST and never to resolve — a human reads it and edits the row.
	if near := ix.nearest(n, pos); near != "" {
		return nil, " — no match, did you mean " + near + "?"
	}
	for _, key := range []string{n, dropInitial(n)} {
		if key != "" && ix.inGame[key] {
			return nil, "" // known to fpl, just not resolvable here
		}
	}
	return nil, " — not in the fpl game, drop the row"
}

// nearest is the closest board name at this position within a couple of edits,
// and "" when nothing is close or two things are equally close. Suggestion only.
func (ix *aliasIndex) nearest(n, pos string) string {
	q := n
	if b := dropInitial(n); b != "" {
		q = b
	}
	// Starts above any distance the per-candidate cap admits, or the widest
	// match it allows can never become the best one.
	best, bestD, ties := "", 99, 0
	for _, cand := range ix.namesByPos[pos] {
		// A short name is one edit from far too much ("neto" to "nero"), and a
		// long one absorbs a mangled cluster without becoming a different man:
		// "Strujic" for Struijk is three edits and obviously the same person.
		max := 2
		switch {
		case len(cand) < 6:
			max = 1
		case len(cand) >= 7:
			max = 3
		}
		d := rankings.Distance(q, cand)
		if d > max {
			continue
		}
		switch {
		case d < bestD:
			best, bestD, ties = cand, d, 1
		case d == bestD:
			ties++
		}
	}
	if ties != 1 {
		return ""
	}
	return best
}

// dropInitial strips a leading one-letter token, returning "" when there is not
// one to strip.
func dropInitial(n string) string {
	fs := strings.Fields(n)
	if len(fs) < 2 || len(fs[0]) != 1 {
		return ""
	}
	return strings.Join(fs[1:], " ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
