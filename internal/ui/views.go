package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// The data tab is a strip of views, ← → to switch, the same way the notes tab
// flips files. value and adp are the engine's table sorted two ways. tiers is
// the ladder: every man at every position, grouped by tier, the taken struck.
// And every rankings csv in the rankings folder gets a view of its own,
// rendered as the file wrote it — its order, its tiers, its opinions — with
// nothing of ours added but survival and the strike. The tiers view is what
// the engine believes; a file view is what the person who wrote the file
// believes; the two are allowed to disagree and the tab shows both.
//
// Nothing here reaches the engine. Same rule as the notes tab.

// Views is the tab's state: where the csvs live, the file fetch loaded (a
// view whether or not it sits in the folder), and which view is open. Sel is a
// label rather than an index so the folder can change under it; "" is value.
type Views struct {
	Dir     string
	Fetched string
	Sel     string
}

type viewKind int

const (
	viewValue viewKind = iota
	viewADP
	viewTiers
	viewFile
)

type view struct {
	label string
	kind  viewKind
	path  string // csv path, file views only
}

// viewList is the strip: the three board views, then every csv in the folder
// by name, then the fetched file if it isn't already there. Files that don't
// exist are skipped silently — a stale meta.json is not the tab's problem.
func (b Board) viewList() []view {
	// The price view's LABEL follows the sport while its kind does not: the kind
	// names a sort key, and -view adp keeps working on either board so the shot
	// scripts and anyone's muscle memory survive the rename.
	out := []view{{label: "value", kind: viewValue}, {label: b.PriceNoun(), kind: viewADP}, {label: "tiers", kind: viewTiers}}
	seen := map[string]bool{}
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if seen[abs] {
			return
		}
		if fi, err := os.Stat(path); err != nil || fi.IsDir() {
			return
		}
		seen[abs] = true
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		for _, v := range out {
			if v.label == stem {
				return // same name elsewhere: the strip is by label, so it would be unreachable
			}
		}
		out = append(out, view{label: stem, kind: viewFile, path: path})
	}
	if b.Views.Dir != "" {
		ents, _ := os.ReadDir(b.Views.Dir)
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
				add(filepath.Join(b.Views.Dir, e.Name()))
			}
		}
	}
	if b.Views.Fetched != "" {
		add(b.Views.Fetched)
	}
	return out
}

func (b Board) currentView() view {
	vs := b.viewList()
	for _, v := range vs {
		if v.label == b.Views.Sel {
			return v
		}
	}
	return vs[0]
}

func (b *Board) cycleView(d int) {
	vs := b.viewList()
	cur := b.currentView()
	i := 0
	for j, v := range vs {
		if v.label == cur.label {
			i = j
		}
	}
	b.Views.Sel = vs[(i+d+len(vs))%len(vs)].label
	b.DataScroll = 0
}

// SelectView opens a view by name — the -view flag for headless frames. An
// unknown name leaves the default open rather than erroring: the frame still
// says which views exist, which is the answer to the question being asked.
func (b *Board) SelectView(name string) {
	want := strings.ToLower(name)
	for _, v := range b.viewList() {
		if v.label == want {
			b.Views.Sel = v.label
			return
		}
	}
	// "adp" is the price view under either name. The label follows the sport —
	// an fpl board's price is a rank and calling it adp on screen would claim a
	// measurement nobody made — but the label is not an api, and -view adp is in
	// the shot scripts and in anyone's fingers. Without this it matched nothing,
	// left the selection unset, and silently rendered the VALUE view while the
	// strip said so and nobody read it.
	if want == "adp" || want == "rank" {
		for _, v := range b.viewList() {
			if v.kind == viewADP {
				b.Views.Sel = v.label
				return
			}
		}
	}
}

// viewStrip is the row of view names, the open one in accent, the rest dim.
// Overflow collapses to "+n" rather than wrapping, same as the notes strip.
func (b Board) viewStrip(w int) string {
	cur := b.currentView()
	var parts []string
	used, overflow := 0, 0
	for _, v := range b.viewList() {
		var cell string
		if v.label == cur.label {
			cell = Accent.Bold(true).Render("▏" + v.label)
		} else {
			cell = Dim.Render(" " + v.label)
		}
		cw := lipgloss.Width(cell) + 3
		if used+cw > w-4 && len(parts) > 0 {
			overflow++
			continue
		}
		parts = append(parts, cell)
		used += cw
	}
	row := strings.Join(parts, "   ")
	if overflow > 0 {
		row += Dim.Render(fmt.Sprintf("   +%d", overflow))
	}
	return "  " + row
}

// ---- sheets: the tiers view and the file views ----

// sheetRow is one line of a ladder: a group head, or a man. A file row the
// board doesn't know is a man with ok false — he still prints, dim, with a ?
// where his odds would go, because dropping him would misrepresent the file
// while claiming to show it verbatim.
type sheetRow struct {
	head string // non-empty: a group header, nothing else set

	pos       string
	rank      int // his rank at his position, in the sheet's own order
	name      string
	team      string
	sentiment string
	p         engine.Player
	ok        bool
}

// sheetEnt is a sheet row before it is laid out: the man plus the tier the
// sheet files him under.
type sheetEnt struct {
	row  sheetRow
	tier int
	src  string
}

// sheetRows builds the ladder for a view. Positions come in board order for
// the tiers view and in the file's own first-seen order for a file view;
// within a position the tiers view sorts tier-then-value and a file view keeps
// the file's order. A group head opens every (position, tier) block.
func (b Board) sheetRows(v view) ([]sheetRow, error) {
	groups := map[string][]sheetEnt{}
	var order []string

	switch v.kind {
	case viewTiers:
		byPos := map[string][]engine.Player{}
		for _, p := range b.State.Players {
			byPos[p.Pos] = append(byPos[p.Pos], p)
		}
		for _, pos := range b.positions() {
			ps := byPos[pos]
			if len(ps) == 0 {
				continue
			}
			sort.Slice(ps, func(i, j int) bool {
				ti, tj := ps[i].Tier, ps[j].Tier
				if ti != tj {
					if ti == 0 || tj == 0 {
						return tj == 0
					}
					return ti < tj
				}
				if ps[i].Value != ps[j].Value {
					return ps[i].Value > ps[j].Value
				}
				ai, aj := ps[i].ADP, ps[j].ADP
				if ai <= 0 {
					ai = engine.UndraftedADP
				}
				if aj <= 0 {
					aj = engine.UndraftedADP
				}
				if ai != aj {
					return ai < aj
				}
				return ps[i].ID < ps[j].ID
			})
			order = append(order, pos)
			for i, p := range ps {
				groups[pos] = append(groups[pos], sheetEnt{row: sheetRow{pos: pos, rank: i + 1, name: p.Name,
					team: p.Team, sentiment: p.Sentiment, p: p, ok: true}, tier: p.Tier, src: p.TierSrc})
			}
		}
	case viewFile:
		f, err := rankings.LoadCSV(v.path)
		if err != nil {
			return nil, err
		}
		ix := b.sheetIndex()
		for _, r := range f.Rows {
			pos := r.Pos
			p, ok := ix.find(r)
			if pos == "" && ok {
				pos = p.Pos
			}
			if _, seen := groups[pos]; !seen {
				order = append(order, pos)
			}
			team := r.Team
			if team == "" && ok {
				team = p.Team
			}
			groups[pos] = append(groups[pos], sheetEnt{row: sheetRow{pos: pos, rank: len(groups[pos]) + 1,
				name: r.Name, team: team, sentiment: r.Sentiment, p: p, ok: ok}, tier: r.Tier})
		}
	}

	var out []sheetRow
	for _, pos := range order {
		if b.DataFilter != "" && pos != b.DataFilter {
			continue
		}
		es := groups[pos]
		lastTier := -1
		for i, e := range es {
			if i == 0 || e.tier != lastTier {
				out = append(out, sheetRow{head: b.sheetHead(pos, e.tier, e.src, es)})
				lastTier = e.tier
			}
			out = append(out, e.row)
		}
	}
	return out, nil
}

// sheetHead is a group's header — "rb · tier 2 · 3 of 7 left". Untiered blocks
// carry only the position; a derived tier says so, since it is our guess and
// not the file's line.
func (b Board) sheetHead(pos string, tier int, src string, es []sheetEnt) string {
	label := strings.ToLower(pos)
	if label == "" {
		label = "—"
	}
	if tier == 0 {
		return label
	}
	// Only men the board knows are counted: a ? row is neither gone nor left,
	// and counting him in the total would read as drafted.
	total, left := 0, 0
	for _, e := range es {
		if e.tier != tier || !e.row.ok {
			continue
		}
		total++
		if !b.State.Taken[e.row.p.ID] {
			left++
		}
	}
	s := fmt.Sprintf("%s · tier %d", label, tier)
	if src == "derived" {
		s += " (derived)"
	}
	if total == 0 {
		return s
	}
	return fmt.Sprintf("%s · %d of %d left", s, left, total)
}

// sheetIndex matches a file's rows to the board. Name and position first, then
// name alone when it is unique on the board, and defenses by team code — the
// same doors fetch's own join uses, minus the fuzzy fallback: a wrong man
// struck through on a sheet is worse than a ? next to the right one.
type sheetIndex struct {
	byKey  map[string]engine.Player // "normalized name|POS"
	byName map[string]engine.Player // normalized name, when unique
	byID   map[string]engine.Player
}

func (b Board) sheetIndex() sheetIndex {
	ix := sheetIndex{byKey: map[string]engine.Player{}, byName: map[string]engine.Player{}, byID: b.State.Players}
	dup := map[string]bool{}
	for _, p := range b.State.Players {
		n := rankings.Normalize(p.Name)
		if n == "" {
			continue
		}
		if _, seen := ix.byKey[n+"|"+p.Pos]; seen {
			dup[n+"|"+p.Pos] = true
		}
		ix.byKey[n+"|"+p.Pos] = p
		if _, seen := ix.byName[n]; seen {
			dup[n] = true
		}
		ix.byName[n] = p
	}
	// byKey gets the same duplicate guard byName always had. Name+position is
	// unique on an nfl board and is NOT on an fpl one — two midfielders called
	// Sangaré — so a rankings sheet's row for one of them resolved to whichever
	// go's map iteration reached last, and the index is rebuilt every frame, so
	// the row's odds and strike-through flickered between renders. Dropped
	// rather than guessed: an unresolvable row already renders dim with `?` where
	// its odds go, which is the honest answer.
	for n := range dup {
		delete(ix.byName, n)
		delete(ix.byKey, n)
	}
	return ix
}

func (ix sheetIndex) find(r rankings.Row) (engine.Player, bool) {
	n := rankings.Normalize(r.Name)
	if r.Pos != "" {
		if p, ok := ix.byKey[n+"|"+r.Pos]; ok {
			return p, true
		}
	}
	// Name alone, when it names exactly one man: the door for a file that
	// files taysom hill under te while sleeper says qb.
	if p, ok := ix.byName[n]; ok {
		return p, true
	}
	if r.Pos == "DEF" && r.Team != "" {
		if p, ok := ix.byID[r.Team]; ok {
			return p, true
		}
	}
	return engine.Player{}, false
}

// sheetNameW is the name column on a ladder. Fewer columns than the numbers
// table, so the name gets more of the width.
func sheetNameW(w int) int {
	n := w - 34
	if n < 18 {
		return 18
	}
	if n > 30 {
		return 30
	}
	return n
}

// sheetLines renders a ladder. Every line, unpaged; the caller pages.
func (b Board) sheetLines(v view, w int) ([]string, bool) {
	rows, err := b.sheetRows(v)
	if err != nil {
		msg := strings.ToLower("could not read " + filepath.Base(v.path) + ": " + err.Error())
		return []string{"  " + Dim.Render(trunc(msg, w-2))}, false
	}
	nameW := sheetNameW(w)
	takenAt := b.takenAt()
	unmatched := false
	var out []string
	for _, r := range rows {
		if r.head != "" {
			out = append(out, "  "+Dim.Render(r.head))
			continue
		}
		unmatched = unmatched || !r.ok
		out = append(out, b.sheetLine(r, nameW, takenAt))
	}
	if len(out) == 0 {
		out = append(out, "  "+Dim.Render("nothing here"))
	}
	return out, unmatched
}

// sheetLine is one man: position, rank, name, team, and either his odds of
// reaching my pick, the pick that took him, or a ? when the file named someone
// the board does not know.
func (b Board) sheetLine(r sheetRow, nameW int, takenAt map[string]int) string {
	s := b.State
	style := b.pos(r.pos, s.Suppressed(r.pos))
	name := strings.ToLower(r.name)
	var nameCell, odds string
	oddsStyle := Dim
	switch {
	case !r.ok:
		style = Dim
		nameCell = Dim.Render(trunc(name, nameW))
		odds = "?"
	case s.Taken[r.p.ID]:
		style = Dim
		nameCell = Dim.Strikethrough(true).Render(trunc(name, nameW))
		odds = "taken"
		if at := takenAt[r.p.ID]; at > 0 {
			odds = "taken " + b.pickLabel(at)
		}
	default:
		nameCell = style.Render(trunc(name, nameW))
		odds = fmt.Sprintf("%.0f%%", 100*s.PSurviveTilted(r.p))
		oddsStyle = b.survStyle(r.p)
	}
	// The file's opinion rides after the name, out of the name's slack, in the
	// board's own chip styles: avoid reversed, target and pass dim.
	used := lipgloss.Width(trunc(name, nameW))
	if r.sentiment != "" && r.ok && !s.Taken[r.p.ID] {
		st := Dim
		if r.sentiment == "avoid" {
			st = ChipNote
		}
		if used+1+len(r.sentiment) <= nameW {
			nameCell += " " + st.Render(r.sentiment)
			used += 1 + len(r.sentiment)
		}
	}
	nameCell += strings.Repeat(" ", nameW-used)

	tag := strings.ToLower(r.pos)
	if tag == "" {
		tag = "?"
	}
	return fmt.Sprintf("  %s  %s  %s  %s  %s",
		style.Render(fmt.Sprintf("%-3s", tag)),
		Dim.Render(fmt.Sprintf("%3d", r.rank)),
		nameCell,
		Dim.Render(fmt.Sprintf("%-3s", trunc(r.team, 3))),
		strings.Repeat(" ", 11-lipgloss.Width(odds))+oddsStyle.Render(odds),
	)
}

// sheetColumns is the head row over a ladder.
func sheetColumns(w int) string {
	return Dim.Render(fmt.Sprintf("  %-3s  %3s  %-*s  %-3s  %11s", "pos", "#", sheetNameW(w), "player", "tm", "surv"))
}
