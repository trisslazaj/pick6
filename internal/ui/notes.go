package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
)

// The notes tab: your own words, next to the board, so draft night is two
// screens (this and sleeper) and not three. A folder of markdown files, a strip
// to pick one, the file rendered with player names in their position colours —
// struck through once they are gone — and a map of the draft so far beside it.
//
// Nothing here reaches the engine. A note is the human's side of the argument
// the board is making, shown next to it; the board does not read it and the
// math does not move. That is deliberate and it is the whole design.
//
// Files are re-read every frame. They are a few kilobytes and the frame is
// drawn twice a second at most, so there is no cache to go stale — write in
// your own editor (or press e), save, tab over, it is there.

// Notes is the tab's state: where the files live, which one is open, and how
// far it is scrolled. Sel is a filename rather than an index so the folder can
// change under it; "" means "not chosen yet", which resolves to the file named
// for my draft slot when one exists and the first file otherwise.
type Notes struct {
	Dir    string
	Sel    string
	Scroll int
}

// noteFile is one entry in the folder. Pinned files (global.md, always.md,
// anything starting with an underscore) render above whichever file is
// selected, every time — the place for "never take a qb before round 5"
// no matter which seat you drew.
type noteFile struct {
	name   string // filename with extension
	label  string // stem, what the strip prints
	pinned bool
	slot   int // the draft slot the filename names, 0 when it doesn't
}

// slotFilePattern reads a seat out of a filename: slot-12, slot12, seat_3,
// pick-1, or a bare 12. Anything else is a strategy file, not a seat file.
var slotFilePattern = regexp.MustCompile(`^(?:slot|seat|pick)?[-_ ]?(\d{1,2})$`)

var pinnedStems = map[string]bool{"global": true, "always": true, "all": true, "general": true}

// notesKey handles the notes tab's own keys. Left/right (or h/l) switch files;
// j/k scroll; everything else falls through to the model.
func (b *Board) notesKey(key string) bool {
	switch key {
	case "j", "down":
		b.Notes.Scroll++
	case "k", "up":
		b.Notes.Scroll--
	case "right", "l", "]":
		b.cycleNote(1)
	case "left", "h", "[":
		b.cycleNote(-1)
	default:
		return false
	}
	b.clampNoteScroll()
	return true
}

func (b *Board) cycleNote(d int) {
	files := b.noteFiles()
	var open []noteFile
	for _, f := range files {
		if !f.pinned {
			open = append(open, f)
		}
	}
	if len(open) == 0 {
		return
	}
	cur := b.selectedNote(files)
	i := 0
	for j, f := range open {
		if f.name == cur {
			i = j
		}
	}
	i = (i + d + len(open)) % len(open)
	b.Notes.Sel = open[i].name
	b.Notes.Scroll = 0
}

// NotePath is the file e opens: the selected file, or global.md in an empty
// folder so the first press of e creates the place to write.
func (b Board) NotePath() string {
	files := b.noteFiles()
	if sel := b.selectedNote(files); sel != "" {
		return filepath.Join(b.Notes.Dir, sel)
	}
	for _, f := range files {
		if f.pinned {
			return filepath.Join(b.Notes.Dir, f.name)
		}
	}
	return filepath.Join(b.Notes.Dir, "global.md")
}

// noteFiles lists the folder: pinned first, then alphabetical. A missing folder
// is an empty list, not an error — the tab renders where to put the first file.
func (b Board) noteFiles() []noteFile {
	ents, err := os.ReadDir(b.Notes.Dir)
	if err != nil {
		return nil
	}
	var out []noteFile
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".md" && ext != ".txt" {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		f := noteFile{name: e.Name(), label: strings.ToLower(stem)}
		f.pinned = pinnedStems[f.label] || strings.HasPrefix(stem, "_")
		if m := slotFilePattern.FindStringSubmatch(f.label); m != nil {
			f.slot, _ = strconv.Atoi(m[1])
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].pinned != out[j].pinned {
			return out[i].pinned
		}
		// Seat files in seat order, then the rest by name: slot-2 before
		// slot-10, which a plain string sort gets backwards.
		if out[i].slot != out[j].slot {
			if out[i].slot == 0 || out[j].slot == 0 {
				return out[i].slot != 0
			}
			return out[i].slot < out[j].slot
		}
		return out[i].label < out[j].label
	})
	return out
}

// selectedNote resolves Sel: an explicit choice that still exists, else the
// file named for my seat, else the first unpinned file. Never a pinned file —
// those are always on screen anyway.
func (b Board) selectedNote(files []noteFile) string {
	if b.Notes.Sel != "" {
		for _, f := range files {
			if f.name == b.Notes.Sel && !f.pinned {
				return f.name
			}
		}
	}
	for _, f := range files {
		if !f.pinned && f.slot == b.State.MySlot {
			return f.name
		}
	}
	for _, f := range files {
		if !f.pinned {
			return f.name
		}
	}
	return ""
}

// ---- layout ----

// notesPane is the whole tab: the notes on the left, the draft map on the right.
func (b Board) notesPane(w int) string {
	mapW := b.mapWidth() + 2 // the pane's own left padding
	leftW := w - mapW - 3
	left := b.notesText(leftW)
	right := b.draftMap(mapW - 2)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(left),
		b.divider(maxLines(left, right)),
		lipgloss.NewStyle().Width(mapW).PaddingLeft(2).Render(right),
	)
}

// visibleNoteRows is how many lines of notes fit under the strip. Same shape as
// visibleDataRows: budget for the chrome, floor so it can't vanish. On a short
// terminal the map beside it is the taller pane and View's clamp takes the
// overshoot off the bottom of BOTH panes — which is why the map's tally lines
// sit above its grid and the rows that go are the kicker rounds.
func (b Board) visibleNoteRows() int {
	if b.Height <= 0 {
		return 30
	}
	n := b.Height - 8 - b.Reserve
	if n < 6 {
		return 6
	}
	return n
}

// clampNoteScroll pins Scroll to the last page, the same way the data tab
// does: a value receiver cannot write the clamp back at render time, and an
// unclamped scroll runs past the end and makes k look dead for as many
// presses as j overshot.
func (b *Board) clampNoteScroll() {
	max := len(b.noteLines(b.notesWidth())) - b.visibleNoteRows()
	if max < 0 {
		max = 0
	}
	if b.Notes.Scroll > max {
		b.Notes.Scroll = max
	}
	if b.Notes.Scroll < 0 {
		b.Notes.Scroll = 0
	}
}

// notesWidth is the notes pane's width at the board's current size — the
// same arithmetic notesPane uses, needed by the clamp before a frame exists.
func (b Board) notesWidth() int {
	content := b.Width
	if content > MaxWidth {
		content = MaxWidth
	}
	if content < MinWidth {
		content = MinWidth
	}
	return content - (b.mapWidth() + 2) - 3
}

// noteLines is the rendered body: every pinned file, then the selected one,
// each under its own dim head when there is more than one thing on the page.
func (b Board) noteLines(w int) []string {
	files := b.noteFiles()
	sel := b.selectedNote(files)
	var lines []string
	sections := 0
	for _, f := range files {
		if f.pinned {
			sections++
		}
	}
	if sel != "" {
		sections++
	}
	for _, f := range files {
		if !f.pinned {
			continue
		}
		if sections > 1 {
			lines = append(lines, "  "+Dim.Render("▸ "+f.label))
		}
		lines = append(lines, b.renderNote(filepath.Join(b.Notes.Dir, f.name), w)...)
		lines = append(lines, "")
	}
	if sel != "" {
		if sections > 1 {
			lines = append(lines, "  "+Dim.Render("▸ "+strings.TrimSuffix(strings.ToLower(sel), filepath.Ext(sel))))
		}
		lines = append(lines, b.renderNote(filepath.Join(b.Notes.Dir, sel), w)...)
	}
	// Trim the trailing blank a pinned-only page leaves.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// notesText renders the strip and the file(s) under it, scrolled.
func (b Board) notesText(w int) string {
	files := b.noteFiles()
	sel := b.selectedNote(files)

	var sb strings.Builder
	sb.WriteString(b.noteStrip(files, sel, w) + "\n")

	if len(files) == 0 {
		sb.WriteString("\n")
		for _, l := range wrapText("no notes yet. drop markdown files in "+b.Notes.Dir+
			" — global.md renders on top of every other file, slot-N.md opens itself when you draw seat N, "+
			"anything else you flip between with ← →. press e to start one.", w-4) {
			sb.WriteString("  " + Dim.Render(l) + "\n")
		}
		return sb.String()
	}

	lines := b.noteLines(w)
	vis := b.visibleNoteRows()
	off := b.Notes.Scroll
	if max := len(lines) - vis; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	end := off + vis
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[off:end] {
		sb.WriteString(l + "\n")
	}
	if end < len(lines) {
		sb.WriteString("  " + Dim.Render(fmt.Sprintf("↓ %d more", len(lines)-end)) + "\n")
	}
	return sb.String()
}

// noteStrip is the row of filenames: pinned ones marked, the open one in
// accent, the rest dim. Overflow collapses to "+n" rather than wrapping.
func (b Board) noteStrip(files []noteFile, sel string, w int) string {
	var parts []string
	used := 0
	budget := w - 4
	overflow := 0
	for _, f := range files {
		var cell string
		switch {
		case f.pinned:
			cell = Dim.Render("▪ " + f.label)
		case f.name == sel:
			cell = Accent.Bold(true).Render("▏" + f.label)
		default:
			cell = Dim.Render(" " + f.label)
		}
		cw := lipgloss.Width(cell) + 3
		if used+cw > budget && len(parts) > 0 {
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

// ---- rendering a file ----

// mdLine is one source line, classified. The markdown this understands is the
// markdown people write in a notes file: headers, bullets, checkboxes, bold.
// Anything else is a paragraph.
func (b Board) renderNote(path string, w int) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{"  " + Dim.Render("could not read "+filepath.Base(path))}
	}
	ix := b.nameIndex()
	var out []string
	for _, src := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		out = append(out, b.renderLine(src, w, ix)...)
	}
	return out
}

func (b Board) renderLine(src string, w int, ix nameIndex) []string {
	line := strings.ToLower(strings.TrimRight(src, " \t"))
	indent := "  "
	body := line
	style := FG
	prefix := ""

	trimmed := strings.TrimLeft(line, " \t")
	lead := len(line) - len(trimmed)
	switch {
	case strings.HasPrefix(trimmed, "#"):
		body = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		style = Accent.Bold(true)
	case strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "* [x]"):
		body = strings.TrimSpace(trimmed[5:])
		prefix = Wait.Render("● ")
		style = Dim
	case strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]"):
		body = strings.TrimSpace(trimmed[5:])
		prefix = Dim.Render("○ ")
	case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
		body = strings.TrimSpace(trimmed[2:])
		prefix = Dim.Render("· ")
	case strings.HasPrefix(trimmed, "> "):
		body = strings.TrimSpace(trimmed[2:])
		prefix = Dim.Render("▍ ")
		style = Dim
	default:
		body = trimmed
	}
	if lead >= 2 {
		indent += strings.Repeat(" ", (lead/2)*2)
	}
	if body == "" {
		return []string{""}
	}
	avail := w - lipgloss.Width(indent) - lipgloss.Width(prefix) - 2
	if avail < 12 {
		avail = 12
	}
	var out []string
	cont := strings.Repeat(" ", lipgloss.Width(prefix))
	for i, seg := range wrapText(glueNames(body, ix), avail) {
		p := prefix
		if i > 0 {
			p = cont
		}
		seg = strings.ReplaceAll(seg, nameGlue, " ")
		out = append(out, indent+p+b.styleTokens(seg, style, ix))
	}
	return out
}

// nameGlue holds a two-word name together through the word wrap: a name that
// breaks across lines is two halves that no longer match anything, so the
// first half lost its colour. A word joiner is not a space, so Fields keeps
// the pair whole; it is swapped back before the tokens are styled.
const nameGlue = "\u2060"

func glueNames(body string, ix nameIndex) string {
	locs := wordLocs(body)
	if len(locs) < 2 {
		return body
	}
	var sb strings.Builder
	pos := 0
	for i := 0; i+1 < len(locs); i++ {
		a, z := locs[i][0], locs[i][1]
		a2, z2 := locs[i+1][0], locs[i+1][1]
		if body[z:a2] != " " {
			continue
		}
		key := rankings.Normalize(body[a:z]) + " " + rankings.Normalize(body[a2:z2])
		if _, ok := ix.full[key]; !ok {
			continue
		}
		sb.WriteString(body[pos:z])
		sb.WriteString(nameGlue)
		pos = a2
		i++ // the second word is spoken for
	}
	sb.WriteString(body[pos:])
	return sb.String()
}

// wrapText word-wraps plain text to width, never mid-word unless a word is
// longer than the whole line.
func wrapText(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	var cur strings.Builder
	curW := 0
	for _, word := range strings.Fields(s) {
		ww := lipgloss.Width(strings.ReplaceAll(word, nameGlue, " "))
		if curW > 0 && curW+1+ww > w {
			out = append(out, cur.String())
			cur.Reset()
			curW = 0
		}
		for ww > w {
			// A word wider than the line is cut by cells, not runes: an emoji
			// is one rune and two cells, and slicing runes by a cell count
			// walks off the end of the slice.
			r := []rune(word)
			n, used := 0, 0
			for n < len(r) {
				cw := lipgloss.Width(string(r[n]))
				if used+cw > w && n > 0 {
					break
				}
				used += cw
				n++
			}
			out = append(out, string(r[:n]))
			word = string(r[n:])
			ww = lipgloss.Width(word)
		}
		if curW > 0 {
			cur.WriteByte(' ')
			curW++
		}
		cur.WriteString(word)
		curW += ww
	}
	if curW > 0 || len(out) == 0 {
		out = append(out, cur.String())
	}
	return out
}

// nameIndex is how a line of prose finds the players in it: full normalized
// names, and last names that belong to exactly one man on the board. Built per
// render from State.Players; two hundred entries, nothing to cache.
type nameIndex struct {
	full map[string]engine.Player // "malik nabers"
	last map[string]engine.Player // "nabers" — unique last names only
	// has is the positions THIS board holds, so a position word only counts as
	// one when the board has that position. posWords carries both sports'
	// vocabularies and the two barely overlap, so without this an nfl note
	// saying "mid" or "forwards" or "keeper" — ordinary english — gets
	// classified as a position and rendered in the fallback foreground, breaking
	// out of the line's own style for no reason a reader can see.
	has map[string]bool
}

// commonLastNames are surnames that are also ordinary words: a note that says
// "i love this pick" must not colour love mint because jeremiyah love exists.
// Two-word forms still match — write the first name and he lights up.
var commonLastNames = map[string]bool{
	"love": true, "hall": true, "hill": true, "ward": true, "hunt": true, "cook": true,
	"bell": true, "young": true, "brown": true, "white": true, "green": true, "king": true,
	"wright": true, "walker": true, "price": true, "long": true, "short": true, "best": true,
	"good": true, "still": true, "will": true, "early": true, "late": true, "round": true,
	"pick": true, "team": true, "gone": true, "left": true, "right": true, "over": true,
	"under": true, "high": true, "low": true, "fair": true, "grant": true,
}

func (b Board) nameIndex() nameIndex {
	ix := nameIndex{full: map[string]engine.Player{}, last: map[string]engine.Player{},
		has: map[string]bool{}}
	for _, pos := range b.State.Positions() {
		ix.has[pos] = true
	}
	seen := map[string]int{}
	for _, p := range b.State.Players {
		n := rankings.Normalize(p.Name)
		if n == "" {
			continue
		}
		ix.full[n] = p
		toks := strings.Fields(n)
		l := toks[len(toks)-1]
		if len(l) < 4 || commonLastNames[l] || !hasSurname(p) {
			continue
		}
		seen[l]++
		ix.last[l] = p
	}
	for l, n := range seen {
		if n > 1 {
			delete(ix.last, l)
		}
	}
	return ix
}

var wordPattern = regexp.MustCompile(`[a-z0-9'’.]+`)

// wordLocs is the word pattern with sentence punctuation handed back: a dot
// inside a word is a name ("a.j. brown", "t.j."), a dot ending a plain word is
// the end of a sentence and stays prose.
func wordLocs(s string) [][]int {
	locs := wordPattern.FindAllStringIndex(s, -1)
	for _, l := range locs {
		w := s[l[0]:l[1]]
		if strings.HasSuffix(w, ".") && strings.Count(w, ".") == 1 {
			l[1]--
		}
	}
	return locs
}

// Both sports, because a note is written the night before in whatever words the
// writer has. "grab a gkp late" and "two mids before a fwd" got no colour at all
// while "def" landed on the nfl reading of the same string.
//
// One map rather than a per-sport pair: the two vocabularies collide on exactly
// "def", which means the same thing to the eye either way, and a note that says
// "rb" on an fpl board is a note about a sport this board is not showing — which
// is the writer's business, not a bug to guard against.
var posWords = map[string]string{
	"qb": "QB", "qbs": "QB", "rb": "RB", "rbs": "RB", "wr": "WR", "wrs": "WR",
	"te": "TE", "tes": "TE", "k": "K", "def": "DEF", "dst": "DEF",

	"gkp": "GKP", "gkps": "GKP", "gk": "GKP", "keeper": "GKP", "keepers": "GKP",
	"defs": "DEF", "defender": "DEF", "defenders": "DEF",
	"mid": "MID", "mids": "MID", "midfielder": "MID", "midfielders": "MID",
	"fwd": "FWD", "fwds": "FWD", "forward": "FWD", "forwards": "FWD",
	"striker": "FWD", "strikers": "FWD",
}

// noteTok is one classified run of a note line: prose, a player, or a
// position word. Classification is separate from rendering so it can be
// tested without a colour profile.
type noteTok struct {
	text   string
	player *engine.Player // set when the run names a man on the board
	pos    string         // set when the run is a position word
	bold   bool
}

// noteTokens splits an already-lowercase segment into runs: two-word names
// first ("chase brown" is one man, not two), then unique last names, then
// position words, everything else prose. **bold** markers are consumed.
func noteTokens(seg string, ix nameIndex) []noteTok {
	bold := map[int]bool{}
	if strings.Contains(seg, "**") {
		var sb strings.Builder
		on := false
		i := 0
		for i < len(seg) {
			if strings.HasPrefix(seg[i:], "**") {
				on = !on
				i += 2
				continue
			}
			if on {
				bold[sb.Len()] = true
			}
			sb.WriteByte(seg[i])
			i++
		}
		seg = sb.String()
	}
	var out []noteTok
	add := func(from, to int, t noteTok) {
		if from >= to {
			return
		}
		t.text = seg[from:to]
		t.bold = bold[from]
		out = append(out, t)
	}
	locs := wordLocs(seg)
	pos := 0
	for i := 0; i < len(locs); i++ {
		a, z := locs[i][0], locs[i][1]
		add(pos, a, noteTok{})
		word := seg[a:z]
		norm := rankings.Normalize(word)
		if i+1 < len(locs) {
			a2, z2 := locs[i+1][0], locs[i+1][1]
			if p, ok := ix.full[norm+" "+rankings.Normalize(seg[a2:z2])]; ok {
				add(a, z2, noteTok{player: &p})
				pos = z2
				i++
				continue
			}
		}
		if p, ok := ix.last[norm]; ok {
			add(a, z, noteTok{player: &p})
		} else if p, ok := ix.full[norm]; ok {
			add(a, z, noteTok{player: &p})
		} else if ps, ok := posWords[strings.Trim(word, ".'’")]; ok && ix.has[ps] {
			add(a, z, noteTok{pos: ps})
		} else {
			add(a, z, noteTok{})
		}
		pos = z
	}
	add(pos, len(seg), noteTok{})
	return out
}

// styleTokens renders the runs: player names in their position hue (struck
// through and dimmed once taken), position words in theirs, everything else in
// the line's base style.
func (b Board) styleTokens(seg string, base lipgloss.Style, ix nameIndex) string {
	var sb strings.Builder
	for _, t := range noteTokens(seg, ix) {
		st := base
		switch {
		case t.player != nil:
			st = b.noteName(*t.player)
		case t.pos != "":
			st = b.pos(t.pos, false)
		}
		if t.bold {
			st = st.Bold(true)
		}
		sb.WriteString(st.Render(t.text))
	}
	return sb.String()
}

// noteName is a player's style in prose: his colour while he is on the board,
// struck through and dim once he is gone. The strike is the tab's one piece of
// live information and the reason the names are matched at all.
func (b Board) noteName(p engine.Player) lipgloss.Style {
	if b.State.Taken[p.ID] {
		return Dim.Strikethrough(true)
	}
	return b.pos(p.Pos, false)
}

// ---- the draft map ----

// mapCell is the width of one seat's column: a two-letter tag plus a gap when
// the terminal can afford it, the tag alone when it can't.
func (b Board) mapCellW() int {
	if b.State.Teams > 12 {
		return 2
	}
	return 3
}

func (b Board) mapWidth() int {
	return 4 + b.State.Teams*b.mapCellW()
}

// draftMap is the snake as a grid — seats across, rounds down — each made pick
// a position tag in its colour, mine in reverse video, the pick on the clock in
// accent. The shape of the room at a glance: the receiver wall in round four,
// the kicker cluster, where my next picks sit and how far apart they are.
func (b Board) draftMap(w int) string {
	s := b.State
	cw := b.mapCellW()
	var sb strings.Builder
	sb.WriteString(sectionHead("draft map", w) + "\n")
	// The tally and my pick numbers go ABOVE the grid: on a 24-row terminal
	// the map is taller than the body budget and the clamp cuts from the
	// bottom, so what goes is round fifteen and not the two lines that say
	// what the room has done and where I pick next.
	sb.WriteString(b.mapTally(w) + "\n" + b.mapMine(w) + "\n\n")

	// Header row: seat numbers, in round-one order.
	sb.WriteString("    ")
	for _, slot := range s.Order {
		lab := fmt.Sprintf("%*d", cw, slot)
		if cw == 3 {
			lab = fmt.Sprintf("%2d ", slot)
		}
		if slot == s.MySlot {
			sb.WriteString(Accent.Bold(true).Render(lab))
		} else {
			sb.WriteString(Dim.Render(lab))
		}
	}
	sb.WriteString("\n")

	at := map[int]engine.Pick{}
	for _, p := range s.Picks {
		at[p.PickNo] = p
	}
	for r := 1; r <= s.Rounds; r++ {
		sb.WriteString(Dim.Render(fmt.Sprintf("%3s ", fmt.Sprintf("r%d", r))))
		for i := range s.Order {
			pickNo := mapPickNo(s, r, i)
			slot := s.Order[i]
			cell := b.mapCell(pickNo, slot, at, cw)
			sb.WriteString(cell)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// mapTally is how many of each position the room has taken so far, in the
// board's position order — the count behind "is this room going qb early".
func (b Board) mapTally(w int) string {
	s := b.State
	n := map[string]int{}
	for _, p := range s.Picks {
		n[s.Players[p.PlayerID].Pos]++
	}
	var parts []string
	for _, pos := range s.Positions() {
		if n[pos] == 0 {
			continue
		}
		parts = append(parts, b.pos(pos, false).Render(strings.ToLower(pos))+Dim.Render(fmt.Sprintf(" %d", n[pos])))
	}
	if len(parts) == 0 {
		return Dim.Render("gone  nobody yet")
	}
	return trunc0(Dim.Render("gone  ")+strings.Join(parts, Dim.Render(" · ")), w)
}

// mapMine names every one of my picks in the draft: the ones spent dim, the
// next in accent, the rest plain. The wheel's 24/25/48 and the middle's
// evenly spaced 3/22/27 are the whole difference between two seat files.
// Wraps onto a second line rather than truncating — the late picks are the
// ones the endgame notes are about.
func (b Board) mapMine(w int) string {
	s := b.State
	next := s.NextPick()
	lead := Dim.Render("yours ")
	cont := strings.Repeat(" ", 6)
	var lines []string
	cur, curW := lead, 6
	for r := 1; r <= s.Rounds; r++ {
		p := s.MyPick(r)
		lab := fmt.Sprintf("%d", p)
		var cell string
		switch {
		case p == next && !s.Done():
			cell = Accent.Bold(true).Render(lab)
		case p < s.PickNo:
			cell = Dim.Render(lab)
		default:
			cell = FG.Render(lab)
		}
		if curW+1+len(lab) > w && curW > 6 {
			lines = append(lines, cur)
			cur, curW = cont, 6
		}
		if curW > 6 {
			cur += " "
			curW++
		}
		cur += cell
		curW += len(lab)
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n")
}

// trunc0 clips a styled line to w cells with an ellipsis, ansi-aware.
func trunc0(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w-1).Render(s) + Dim.Render("…")
}

// mapPickNo is the overall pick made in round r by the seat at round-one index
// i (0-based): the same snake arithmetic as State.MyPick, for any seat.
func mapPickNo(s *engine.State, r, i int) int {
	if r%2 == 1 {
		return (r-1)*s.Teams + i + 1
	}
	return (r-1)*s.Teams + (s.Teams - i)
}

func (b Board) mapCell(pickNo, slot int, at map[int]engine.Pick, cw int) string {
	s := b.State
	mine := slot == s.MySlot
	pad := ""
	if cw == 3 {
		pad = " "
	}
	if p, ok := at[pickNo]; ok {
		pl := s.Players[p.PlayerID]
		tag := strings.ToLower(pl.Pos)
		if len(tag) > 2 {
			tag = tag[:2] // def -> de; k -> " k"
		}
		if len(tag) == 1 {
			tag = " " + tag
		}
		if tag == "" {
			tag = "??"
		}
		st := b.pos(pl.Pos, false)
		if p.OwnerSlot != 0 && p.OwnerSlot != p.Slot {
			// A traded pick: the seat picked, another roster got the player.
			// Underline marks it — the map is about seats, the roster pane is
			// about who owns whom.
			st = st.Underline(true)
		}
		if mine {
			c, ok := posColor[pl.Pos]
			if !ok {
				c = ColFG
			}
			st = lipgloss.NewStyle().Foreground(lipgloss.Color(ColInk)).Background(lipgloss.Color(c)).Bold(true)
		}
		return st.Render(tag) + pad
	}
	switch {
	case pickNo == s.PickNo && !s.Done():
		return Accent.Bold(true).Render("▸▸") + pad
	case mine:
		return Accent.Render("··") + pad
	default:
		return Dim.Render("··") + pad
	}
}

// ---- editing ----

// editDoneMsg is what comes back when the editor exits.
type editDoneMsg struct{ err error }

// EditNote suspends the tui and opens the current note in $EDITOR (vi when it
// is unset). Only on the notes tab — anywhere else e is nobody's key. The file
// is re-read on the next frame like every frame, so there is nothing to reload.
func (b *Board) EditNote() tea.Cmd {
	if b.Tab != 2 {
		return nil
	}
	path := b.NotePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vi"
	}
	// Through the shell so an EDITOR with arguments ("code -w") works.
	c := exec.Command("sh", "-c", ed+" '"+strings.ReplaceAll(path, "'", `'\''`)+"'")
	return tea.ExecProcess(c, func(err error) tea.Msg { return editDoneMsg{err} })
}

// editStatus is the footer note after the editor closes.
func editStatus(msg editDoneMsg) string {
	if msg.err != nil {
		return "editor: " + msg.err.Error()
	}
	return "notes reloaded"
}

// hasSurname reports whether the last word of a player's name is a surname to
// match note prose against.
//
// An nfl team defense has none: sleeper carries its city and nickname
// ("Seattle Seahawks"), so the "last name" is a nickname a note is likely to use
// about anything, and "the seahawks are due" would colour a defense. The check
// used to be `p.Pos == "DEF"`, which is that fact spelled as the string — right
// for one team unit per club and wrong for fpl's hundred and eighty-four
// defenders, who have real surnames and want the same last-name matching
// everyone else gets. A team defense is one whose name is the team's, which is
// what the id carries: sleeper keys them by the team abbreviation.
func hasSurname(p engine.Player) bool {
	return !(p.Pos == "DEF" && p.ID == p.Team && p.Team != "")
}
