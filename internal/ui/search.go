package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// The `/` overlay: type a name, get a player. Every mode gets one out of it,
// because "where does he stand" is a question you ask in all three — but what
// the selection is FOR differs. Manual mode marks him taken with x; live mode
// only reads him, since the marks come from the feed and a hand-typed one would
// desync the board it is supposed to be checking.
//
// Selection and marking are two keys on purpose. `/name` + enter + x is one
// keystroke more than enter alone, and that keystroke is the whole guard
// against a fuzzy match drafting the wrong man 190 times a night. Undo exists,
// but undo after the next pick has already landed is a mess.

// searchRows is how many matches the overlay shows at once, before the height
// budget takes some back. Six is two more than the number of people who share
// a last name on a real board, which is the case the list has to disambiguate.
const searchRows = 6

// searchPool caps the match list. Nobody scrolls past forty candidates; they
// type another letter instead, which is faster and what the ranking assumes.
const searchPool = 40

// Search is the overlay's whole state: whether it is up, what has been typed,
// and which match the cursor is on.
type Search struct {
	Open   bool
	Query  string
	Cursor int
}

// searchMatch is one row of the overlay. Taken players are deliberately IN the
// list — "is he gone?" is the same keystrokes as "where is he?", and answering
// it with an empty result reads as "no such player", which is a different and
// wrong answer. They sort under the available ones and say when they went.
type searchMatch struct {
	engine.Player
	taken bool
	pick  int // overall pick that took him, 0 when he is still on the board
	score int // 0 = matched at a word start, higher = matched mid-word
}

// searchKey handles every key while the prompt is up, and swallows the ones it
// has no use for: a stray `p` mid-name must not cycle the data tab's filter.
// ctrl+c is the exception — quitting is never something you have to escape out
// of first.
func (b *Board) searchKey(key string) bool {
	switch key {
	case "ctrl+c":
		return false
	case "esc":
		b.Search = Search{}
	case "enter":
		rows := b.searchResults()
		if len(rows) > 0 {
			i := b.Search.Cursor
			if i >= len(rows) {
				i = len(rows) - 1
			}
			b.Selected = rows[i].ID
		}
		b.Search = Search{}
	case "backspace":
		if q := []rune(b.Search.Query); len(q) > 0 {
			b.Search.Query = string(q[:len(q)-1])
			b.Search.Cursor = 0
		}
	case "ctrl+u":
		b.Search.Query = ""
		b.Search.Cursor = 0
	case "down", "ctrl+n":
		b.Search.Cursor++
	case "up", "ctrl+p":
		b.Search.Cursor--
	default:
		// Printable single runes are text, including " " — bubbletea reports the
		// space bar as a one-cell key and names are two words.
		if r := []rune(key); len(r) == 1 && unicode.IsPrint(r[0]) {
			b.Search.Query += key
			b.Search.Cursor = 0
		}
	}
	if b.Search.Open {
		n := len(b.searchResults())
		if b.Search.Cursor >= n {
			b.Search.Cursor = n - 1
		}
		if b.Search.Cursor < 0 {
			b.Search.Cursor = 0
		}
	}
	return true
}

// SelectBest selects a query's top match without going through the prompt, for
// the snapshot path: the selection line is a pane like any other and has to be
// eyeballable on real names and real widths, which no scripted draft can reach.
func (b *Board) SelectBest(query string) {
	prev := b.Search
	b.Search = Search{Query: query}
	if rows := b.searchResults(); len(rows) > 0 {
		b.Selected = rows[0].ID
	}
	b.Search = prev
}

// searchResults ranks the board against the query. Tokens all have to match, so
// "ja ch" finds ja'marr chase and "sea def" finds the seahawks; a token that
// lands at the start of a word beats one that lands inside it, so "brown" puts
// marquise brown above hollywood brown's teammates.
//
// The haystack is the normalized name plus the team code, which is what makes
// "sea" reach a defense: in the sleeper dump defenses carry the city and the
// nickname, not the abbreviation everybody actually types.
func (b Board) searchResults() []searchMatch {
	tokens := strings.Fields(rankings.Normalize(b.Search.Query))
	if len(tokens) == 0 {
		return nil
	}
	takenAt := b.takenAt()

	var out []searchMatch
	for id, p := range b.State.Players {
		hay := rankings.Normalize(p.Name) + " " + strings.ToLower(p.Team)
		score, ok := matchScore(hay, tokens)
		if !ok {
			continue
		}
		out = append(out, searchMatch{Player: p, taken: b.State.Taken[id],
			pick: takenAt[id], score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.taken != c.taken {
			return c.taken // still available first
		}
		if a.score != c.score {
			return a.score < c.score
		}
		if a.Value != c.Value {
			return a.Value > c.Value
		}
		return a.ID < c.ID
	})
	if len(out) > searchPool {
		out = out[:searchPool]
	}
	return out
}

// matchScore requires every token and rewards word starts. Returns the number
// of tokens that landed mid-word, so 0 is the best possible match.
func matchScore(hay string, tokens []string) (int, bool) {
	score := 0
	for _, t := range tokens {
		i := strings.Index(hay, t)
		if i < 0 {
			return 0, false
		}
		if i > 0 && hay[i-1] != ' ' {
			score++
		}
	}
	return score, true
}

// takenAt maps player id to the pick that took him, for the "taken 1.03" note.
// Rebuilt per frame off State.Picks, which is a few hundred entries at most —
// the engine recomputes everything on every pick and this is cheaper than any
// of it.
func (b Board) takenAt() map[string]int {
	at := make(map[string]int, len(b.State.Picks))
	for _, p := range b.State.Picks {
		at[p.PlayerID] = p.PickNo
	}
	return at
}

// searchVisible is how many result rows fit. The overlay is charged against the
// same height budget as everything else — while it is up the body gives ground,
// and on a short terminal it gives less than six rows' worth.
func (b Board) searchVisible() int {
	if b.Height <= 0 {
		return searchRows
	}
	n := b.Height - 16 - b.Reserve
	if n < 2 {
		return 2
	}
	if n > searchRows {
		return searchRows
	}
	return n
}

// overlay is the block drawn between the body and the footer: the prompt and
// its matches while searching, the standing selection afterwards, nothing when
// neither exists. It returns "" in that last case so the frame does not reserve
// a row for something that is not there — same rule the alert banner follows.
func (b Board) overlay(w int) string {
	switch {
	case b.Search.Open:
		return b.searchPane(w)
	case b.Selected != "":
		return b.selectedLine(w)
	}
	return ""
}

func (b Board) searchPane(w int) string {
	rows := b.searchResults()
	vis := b.searchVisible()
	// searchKey clamps the cursor on every keystroke, but the snapshot path and
	// the tests set Search wholesale, so the window is clamped here too rather
	// than trusting a caller not to hand this a cursor past the end.
	off := 0
	if b.Search.Cursor >= vis {
		off = b.Search.Cursor - vis + 1
	}
	if off > len(rows) {
		off = len(rows)
	}
	end := off + vis
	if end > len(rows) {
		end = len(rows)
	}

	// Only the count, never the keys: the footer is already carrying those while
	// the prompt is up, and printing them twice on adjacent rows spends the width
	// the query itself needs.
	count := fmt.Sprintf("%d matches", len(rows))
	switch {
	case b.Search.Query == "":
		count = "type a name"
	case len(rows) == 1:
		count = "1 match"
	case len(rows) == 0:
		count = "no match"
	}
	left := "  " + Dim.Render("search  ") + FG.Render(strings.ToLower(b.Search.Query)) +
		Accent.Render("▏")
	right := Dim.Render(count)
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}

	var sb strings.Builder
	sb.WriteString(Dim.Render("  "+strings.Repeat("─", w-4)) + "\n")
	sb.WriteString(left + strings.Repeat(" ", pad) + right + "\n")
	for i, m := range rows[off:end] {
		sb.WriteString(b.searchRow(m, off+i == b.Search.Cursor, w) + "\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// searchNameW scales the name column with the terminal, the same way the data
// tab's does: everything else on the row is fixed, so the name takes the slack.
func searchNameW(w int) int {
	n := w - 46
	if n < 14 {
		return 14
	}
	if n > 26 {
		return 26
	}
	return n
}

func (b Board) searchRow(m searchMatch, cursor bool, w int) string {
	s := b.State
	style := Pos(m.Pos, s.Suppressed(m.Pos))
	name := trunc(strings.ToLower(m.Name), searchNameW(w))

	mark := "  "
	if cursor {
		mark = Accent.Render("▸ ")
	}

	// The facts are the ones the overlay is being asked for: a taken player owes
	// you the pick that took him and nothing else, an available one owes you his
	// price and his odds — the same tilted survival the rest of the board quotes,
	// off the same styler, so a player looked up here and read off the data tab
	// cannot carry two different numbers.
	var facts string
	if m.taken {
		gone := "taken"
		if m.pick > 0 {
			gone = "taken " + b.pickLabel(m.pick)
		}
		facts = Dim.Render(gone)
		style = style.Faint(true)
	} else {
		var parts []string
		if m.Tier > 0 {
			parts = append(parts, fmt.Sprintf("tier %d", m.Tier))
		}
		if m.ADP > 0 {
			parts = append(parts, fmt.Sprintf("adp %.1f", m.ADP))
		}
		facts = Dim.Render(strings.Join(parts, " · "))
		if len(parts) > 0 {
			facts += Dim.Render(" · ")
		}
		facts += b.survStyle(m.Player).Render(pct(s.PSurviveTilted(m.Player)))
	}

	return fmt.Sprintf("  %s%s  %s  %s  %s", mark,
		style.Render(fmt.Sprintf("%-3s", strings.ToLower(m.Pos))),
		style.Render(fmt.Sprintf("%-*s", searchNameW(w), name)),
		Dim.Render(fmt.Sprintf("%-3s", m.Team)),
		facts)
}

// selectedLine is what stands after the prompt closes: one row naming the man
// the next x would take, with his numbers. It survives tab flips, because the
// selection is a fact about the session and not about the pane you are looking
// at.
// What x would do is not repeated here: the footer already names it in manual
// mode, and a hint printed twice on adjacent rows is width the facts need. The
// right-hand clause is only the way out, which nothing else advertises.
func (b Board) selectedLine(w int) string {
	p, ok := b.State.Players[b.Selected]
	if !ok {
		return ""
	}
	style := Pos(p.Pos, b.State.Suppressed(p.Pos))
	id := "  " + Dim.Render("selected  ") +
		style.Render(strings.ToLower(p.Pos)+" "+strings.ToLower(p.Name)) + " " +
		Dim.Render(p.Team)

	// Longest first, dropping whole clauses from the least useful end: the price
	// goes before the odds, the odds go before his name, and his name never goes.
	// At 80 columns the untrimmed line ran a cell over and jammed itself against
	// the hint, which is the rendering fault every other pane already avoids.
	var lefts []string
	if b.State.Taken[b.Selected] {
		// The same clause the row he was selected FROM carried. It said "taken
		// 1.01" there and "already taken" here, about one man in one frame.
		gone := " · already taken"
		if at := b.takenAt()[b.Selected]; at > 0 {
			gone = " · taken " + b.pickLabel(at)
		}
		lefts = []string{id + Dim.Render(gone), id}
	} else {
		surv := Dim.Render(" · ") + b.survStyle(p).Render(pct(b.State.PSurviveTilted(p)))
		tier, adp := "", ""
		if p.Tier > 0 {
			tier = Dim.Render(fmt.Sprintf(" · tier %d", p.Tier))
		}
		if p.ADP > 0 {
			adp = Dim.Render(fmt.Sprintf(" · adp %.1f", p.ADP))
		}
		lefts = []string{id + tier + adp + surv, id + adp + surv, id + surv, id}
	}

	left, right := lefts[len(lefts)-1], ""
fit:
	for _, cand := range lefts {
		for _, hint := range []string{"esc clear", ""} {
			if lipgloss.Width(cand)+lipgloss.Width(hint)+4 <= w {
				left = cand
				if hint != "" {
					right = Dim.Render(hint)
				}
				break fit
			}
		}
	}

	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return Dim.Render("  "+strings.Repeat("─", w-4)) + "\n" +
		left + strings.Repeat(" ", pad) + right
}
