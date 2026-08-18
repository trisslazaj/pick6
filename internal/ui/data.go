package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
)

// The data tab: every available player and every number the engine holds on
// him, one flat table, no abstraction. Between picks there is time to actually
// read, and the person reading built the tool.

// dataFilters cycles with the p key, empty string meaning every position. Off
// the same derivation as everything else, or the key walks six filters that each
// match nothing and the table reads "rows 1-0 of 0" six times running.
func (b Board) dataFilters() []string {
	return append([]string{""}, b.positions()...)
}

// survGoneBand is presentation, not tuning: below this the survival cell
// renders in the cliff red, because "he is gone" is the same family of alarm.
const survGoneBand = 0.15

// HandleKey consumes tab-switching and data-tab keys shared by both models.
// Returns false when the key isn't ours, so the caller's own bindings run.
func (b *Board) HandleKey(key string) bool {
	// The prompt eats everything while it is up, so a name with a p or a j in
	// it types instead of cycling the filter. See searchKey.
	if b.Search.Open {
		return b.searchKey(key)
	}
	if key == "/" {
		b.Search = Search{Open: true}
		return true
	}
	// esc clears the selection before it means quit. Both models still bind q
	// and ctrl+c unconditionally, so there is always a way out that does not
	// depend on what is on screen.
	if key == "esc" && b.Selected != "" {
		b.Selected = ""
		return true
	}
	if key == "tab" {
		b.Tab = (b.Tab + 1) % 3
		return true
	}
	if b.Tab == 2 {
		return b.notesKey(key)
	}
	if b.Tab != 1 {
		return false
	}
	switch key {
	case "j", "down":
		b.DataScroll++
	case "k", "up":
		b.DataScroll--
	case "p":
		b.DataFilter = b.nextFilter(b.DataFilter)
		b.DataScroll = 0
	case "right", "l", "]":
		b.cycleView(1)
	case "left", "h", "[":
		b.cycleView(-1)
	default:
		return false
	}
	b.clampScroll()
	return true
}

func (b Board) nextFilter(cur string) string {
	filters := b.dataFilters()
	for i, f := range filters {
		if f == cur {
			return filters[(i+1)%len(filters)]
		}
	}
	return ""
}

// dataRows is every available player under the current filter, best value
// first — the same comparator Available uses, so the two tabs agree, except
// that here an undrafted player (adp <= 0) is priced at UndraftedADP before
// comparing rather than left at raw adp, which is what sinks him to the bottom
// instead of the top. The adp view sorts the same rows by price instead,
// cheapest first, value breaking ties; the undrafted sink to the bottom
// either way.
func (b Board) dataRows() []engine.Player {
	s := b.State
	var out []engine.Player
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		if b.DataFilter != "" && p.Pos != b.DataFilter {
			continue
		}
		out = append(out, p)
	}
	byADP := b.currentView().kind == viewADP
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].ADP, out[j].ADP
		if ai <= 0 {
			ai = engine.UndraftedADP
		}
		if aj <= 0 {
			aj = engine.UndraftedADP
		}
		if byADP && ai != aj {
			return ai < aj
		}
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		if ai != aj {
			return ai < aj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (b Board) visibleDataRows() int {
	if b.Height <= 0 {
		return 20
	}
	// header, banner, view strip, urgency strip, title, column head, legend,
	// footer — plus whatever the caller draws under the whole board (see
	// Board.Reserve). A file view has no urgency strip: it is the file's word,
	// not the engine's, so those two rows go to the file.
	n := b.Height - 12 - b.Reserve
	if b.currentView().kind == viewFile {
		n += 2
	}
	if n < 8 {
		return 8
	}
	return n
}

// dataCount is how many lines the open view has to page through.
func (b Board) dataCount() int {
	switch v := b.currentView(); v.kind {
	case viewTiers, viewFile:
		lines, _ := b.sheetLines(v, b.Width)
		return len(lines)
	}
	return len(b.dataRows())
}

// dataNameW scales the player column with the terminal: every other column is
// fixed-width, so the name takes the slack, floored at 18 and capped where
// even hyphenated receivers fit.
func (b Board) dataNameW(w int) int {
	fixed := 62
	if !b.hasMarketColumns() {
		fixed -= 20 // bye, spread and fmt gap, plus their separators
	}
	n := w - fixed
	if n < 18 {
		return 18
	}
	if n > 26 {
		return 26
	}
	return n
}

func (b *Board) clampScroll() {
	max := b.dataCount() - b.visibleDataRows()
	if max < 0 {
		max = 0
	}
	if b.DataScroll > max {
		b.DataScroll = max
	}
	if b.DataScroll < 0 {
		b.DataScroll = 0
	}
}

func (b Board) dataPane(w int) string {
	v := b.currentView()
	if v.kind == viewTiers || v.kind == viewFile {
		return b.sheetPane(v, w)
	}
	rows := b.dataRows()
	vis := b.visibleDataRows()
	off, end := b.page(len(rows), vis)

	var sb strings.Builder
	sb.WriteString(b.viewStrip(w) + "\n")
	sb.WriteString(b.urgencyStrip(w))

	by := "value"
	if v.kind == viewADP {
		by = b.PriceNoun()
	}
	sb.WriteString(b.dataTitle("all players — sorted by "+by, off, end, len(rows), w))

	nameW := b.dataNameW(w)
	head := []string{fmt.Sprintf("%-3s", "pos"), fmt.Sprintf("%-*s", nameW, "player"), fmt.Sprintf("%-3s", "tm")}
	if b.hasMarketColumns() {
		head = append(head, fmt.Sprintf("%3s", "bye"))
	}
	head = append(head, fmt.Sprintf("%4s", "tier"), fmt.Sprintf("%5s", "value"),
		fmt.Sprintf("%5s", b.PriceNoun()))
	if b.hasMarketColumns() {
		head = append(head, fmt.Sprintf("%6s", "spread"))
	}
	head = append(head, fmt.Sprintf("%4s", "surv"))
	if b.hasMarketColumns() {
		head = append(head, fmt.Sprintf("%7s", "fmt gap"))
	}
	sb.WriteString(Dim.Render("  "+strings.Join(head, "  ")) + "\n")
	// Whether the chip RENDERED, not whether the player tripped it: at 80-82
	// columns the name column cannot afford " past low", and a legend for a
	// marker nobody can see spends the row explaining nothing while pushing the
	// column glossary off it. See tripwireShown.
	tripped := false
	for _, p := range rows[off:end] {
		tripped = tripped || b.tripwireShown(p, nameW)
		sb.WriteString(b.dataRow(p, nameW) + "\n")
	}
	sb.WriteString(b.legend(tripped, w) + "\n")
	return sb.String()
}

// sheetPane is the tiers view or a file view: the strip, the title, the
// column head, a page of the ladder, and a legend only when a ? is on it.
func (b Board) sheetPane(v view, w int) string {
	lines, unmatched := b.sheetLines(v, w)
	vis := b.visibleDataRows()
	if !unmatched {
		vis++ // no legend row this frame: the table gets it
	}
	off, end := b.page(len(lines), vis)

	var sb strings.Builder
	sb.WriteString(b.viewStrip(w) + "\n")
	title := "every player — by position and tier, taken struck"
	if v.kind == viewFile {
		title = v.label + " — as the file ranks them"
	}
	if v.kind == viewTiers {
		sb.WriteString(b.urgencyStrip(w))
	}
	sb.WriteString(b.dataTitle(title, off, end, len(lines), w))
	sb.WriteString(sheetColumns(w) + "\n")
	for _, l := range lines[off:end] {
		sb.WriteString(l + "\n")
	}
	if unmatched {
		sb.WriteString("  " + Dim.Render("?: not on the board") + "\n")
	}
	return sb.String()
}

// page clamps the scroll to the last page and returns the visible window.
func (b Board) page(n, vis int) (off, end int) {
	off = b.DataScroll
	if max := n - vis; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	end = off + vis
	if end > n {
		end = n
	}
	return off, end
}

// dataTitle is the row over a table: what it is on the left, the page counter
// on the right. The position filter rewrites the left half.
func (b Board) dataTitle(label string, off, end, n, w int) string {
	if i := strings.Index(label, " —"); b.DataFilter != "" && i >= 0 {
		label = strings.ToLower(b.DataFilter) + " only" + label[i:]
	}
	counter := fmt.Sprintf("rows %d–%d of %d", off+1, end, n)
	if n == 0 {
		counter = "no players"
	}
	label = trunc(label, w-lipgloss.Width(counter)-5)
	left := Dim.Render(label)
	right := Dim.Render(counter)
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if pad < 1 {
		pad = 1
	}
	return "  " + left + strings.Repeat(" ", pad) + right + "\n"
}

// legend is the one row under the table that says what the columns mean, and —
// only when one is actually on the page — what the "past low" chip means.
//
// 4b's copy is 36 cells and fits no column on either tab, so it lives here. It
// goes FIRST and in the chip's own amber, because it is the contextual half:
// the column glossary is static and learnable, while a chip that appeared this
// frame is the thing being read right now. A legend for something you cannot
// see is width spent on nothing, so it is absent the rest of the time.
//
// Clauses drop from the tail rather than the line truncating, which is what it
// used to do — at 92 columns the old single string ran two cells over and
// ended "d: derived ti…", a rendering fault sitting under a table whose whole
// job is being readable.
func (b Board) legend(tripped bool, w int) string {
	cols := []string{"d: derived tier"}
	if b.hasMarketColumns() {
		cols = []string{
			"spread: how widely drafts vary on him",
			"fmt gap: adp shift by format",
			"d: derived tier",
		}
	}
	out, used := "", 0
	if tripped {
		out = Run.Render("past worst observed pick — check news")
		used = lipgloss.Width("past worst observed pick — check news")
	}
	for _, c := range cols {
		sep := ""
		if used > 0 {
			sep = " · "
		}
		if used+lipgloss.Width(sep)+lipgloss.Width(c) > w-4 {
			break
		}
		out += Dim.Render(sep + c)
		used += lipgloss.Width(sep) + lipgloss.Width(c)
	}
	return "  " + out
}

// dataRow is one player, every column. Dashes mean "no source had a number",
// which is itself information — a dash-heavy row is a player the market has
// no opinion on.
func (b Board) dataRow(p engine.Player, nameW int) string {
	s := b.State
	// Suppressed k/def render faint here too, not just on the board tab. The old
	// palette let this slide — a pastel mauve kicker was quiet at full strength
	// anyway — but sleeper's kicker is a vivid violet, and undimmed it pulls more
	// eye in round 9 than the receivers above it. Same player, two tabs, opposite
	// urgency is the one thing this table cannot do.
	style := b.pos(p.Pos, s.Suppressed(p.Pos))

	// Amber numbers mark a faller, same as the board tab.
	numeric := Dim
	if s.Falling(p) {
		numeric = Run
	}

	// The tilted survival, same as the board tab's column: two panes quoting
	// different odds on the same player is the one failure this table cannot
	// afford, since reading the numbers is its entire job. The BANDS come from
	// the same place too — they used to be written out here and dim-unless-
	// falling over on the board tab, which is how the board tab spent its whole
	// life rendering the answer to its own question in grey.
	surv := s.PSurviveTilted(p)
	survStyle := b.survStyle(p)

	tier := "  — "
	if p.Tier > 0 {
		mark := " "
		if p.TierSrc == "derived" {
			mark = "d"
		}
		tier = fmt.Sprintf("%3d%s", p.Tier, mark)
	}
	value := "    —"
	if p.Value > 0 {
		value = fmt.Sprintf("%5d", p.Value)
	}
	adp := "    —"
	if p.ADP > 0 {
		adp = fmt.Sprintf("%5s", fmt.Sprintf(b.PriceFmt(), p.ADP))
	}
	sd := "     —"
	if p.Stdev > 0 {
		sd = fmt.Sprintf("%6.1f", p.Stdev)
	}
	sprd := "      —"
	if p.FormatSpread > 0 {
		sprd = fmt.Sprintf("%7.1f", p.FormatSpread)
	}
	bye := "  —"
	if p.Bye > 0 {
		bye = fmt.Sprintf("%3d", p.Bye)
	}

	cells := []string{
		style.Render(fmt.Sprintf("%-3s", strings.ToLower(p.Pos))),
		b.nameCell(p, style, nameW),
		Dim.Render(fmt.Sprintf("%-3s", p.Team)),
	}
	if b.hasMarketColumns() {
		cells = append(cells, Dim.Render(bye))
	}
	cells = append(cells,
		Dim.Render(tier),
		FG.Render(value),
		numeric.Render(adp),
	)
	if b.hasMarketColumns() {
		cells = append(cells, numeric.Render(sd))
	}
	cells = append(cells, survStyle.Render(fmt.Sprintf("%3.0f%%", 100*surv)))
	if b.hasMarketColumns() {
		cells = append(cells, Dim.Render(sprd))
	}
	return "  " + strings.Join(cells, "  ")
}

// hasMarketColumns reports whether this board's source reports the things a
// thousand-draft adp sample reports and a published ranking does not: a bye
// week, a draft-position spread, and the same player priced under a second
// scoring format.
//
// Three of the table's ten columns come from there, and on an fpl board all
// three are em dashes on all 560 rows — thirty cells of nothing per screen,
// plus two legend clauses spending the glossary row explaining columns that
// have no numbers in them. Dropping them also frees the width the name column
// spends its life fighting for.
func (b Board) hasMarketColumns() bool { return b.PriceNoun() == "adp" }

// urgencyStrip is the engine's summary math on one line: per position, the
// urgency number and the bestNow→bestLater pair that produces it, plus a green
// "safe" where waiting keeps your own guy. Sorted most urgent first; entries
// that don't fit collapse into a "+n" rather than wrapping.
//
// "safe" is decided by the same engine call as the board tab's safe-to-wait
// tag, State.SafeToWait, rather than re-derived here. Two tabs contradicting
// each other about the same state is worse than either being wrong alone, and
// re-deriving is exactly how they drift apart.
//
// Which is also why a safe position still shows its number and still sorts on
// it. Under milestone 4 "safe" meant urgency was exactly 0, so filing those
// entries under a sort key of 0 was faithful; urgency is continuous now and the
// board's top group — the one wearing the accent border — is routinely a
// position whose own best man will keep. Pinning it to zero here ranked it last
// while the board tab ranked it first, about the same state, in the same frame.
//
// The u > 0 arm is what stays silent on my own pick: urgency is continuous now
// and only ever EXACTLY zero when no picks intervene, which is the one frame
// where the strip has nothing worth saying and says so below.
func (b Board) urgencyStrip(w int) string {
	s := b.State
	type entry struct {
		text string
		u    float64
	}
	var entries []entry
	for _, pos := range b.positions() {
		if s.Need(pos) == 0 {
			continue
		}
		now, ok := s.BestNow(pos)
		if !ok {
			continue
		}
		tag := b.pos(pos, false).Bold(true).Render(strings.ToLower(pos))
		u := s.Urgency(pos)
		switch {
		case s.SafeToWait(pos):
			entries = append(entries, entry{fmt.Sprintf("%s %s %s", tag,
				FG.Render(fmt.Sprintf("%.0f", u)), Wait.Render("safe")), u})
		case u > 0:
			// bestLater is the modal best survivor now — the man you are most
			// likely to be choosing from instead, which is the question the
			// arrow was always asking. He can be bestNow himself: the top man
			// only has to outlast the intervening picks, while everyone under
			// him also needs the men above him to go first. Rendering
			// "love→love" for that reads as a rendering fault, so the arrow
			// says what it means instead.
			later, _ := s.BestLater(pos)
			pair := lastName(now.Name) + "→" + lastName(later.Name)
			if later.ID == now.ID {
				pair = lastName(now.Name) + "→same"
			}
			entries = append(entries, entry{fmt.Sprintf("%s %s %s", tag,
				FG.Render(fmt.Sprintf("%.0f", u)), Dim.Render(pair)), u})
		}
	}
	rule := "\n" + Dim.Render("  "+strings.Repeat("─", w-4)) + "\n"
	if len(entries) == 0 {
		note := Dim.Render("—")
		if !s.Done() && s.PicksUntilMine() == 0 {
			// It said "take best value" until the board's zero-urgency tie-break
			// became need-weighted VOR, at which point the two tabs were
			// describing different rules on the one frame where the rule decides
			// everything. The wording is this terse for a width reason and not a
			// stylistic one: the plan rides on the end of this row, and the budget
			// leaves 49 cells for the note — "take best value over replacement"
			// fits the row and pushes the plan off it.
			note = Dim.Render("your pick — urgency resets, best over replacement")
		}
		// The plan still rides along here, and this is the frame it is most worth
		// reading on: standing at my own pick, every urgency is exactly zero and
		// the strip has nothing else to say about which position to take.
		line := "  " + Dim.Render("urgency  ") + note
		return line + b.planTail(lipgloss.Width(line), false, w) + rule
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].u > entries[j].u })

	line := "  " + Dim.Render("urgency  ")
	used := lipgloss.Width(line)
	shown, overflowed := 0, false
	for _, e := range entries {
		sep := ""
		if shown > 0 {
			sep = Dim.Render(" · ")
		}
		if used+lipgloss.Width(sep)+lipgloss.Width(e.text) > w-8 {
			line += Dim.Render(fmt.Sprintf(" +%d", len(entries)-shown))
			overflowed = true
			break
		}
		line += sep + e.text
		used += lipgloss.Width(sep) + lipgloss.Width(e.text)
		shown++
	}
	return line + b.planTail(used, overflowed, w) + rule
}

// planTail appends the two-pick plan to the strip when the row has width to
// spare, and omits it silently otherwise. The board tab carries the plan in
// full, so here it is a convenience — and a strip that wrapped to two lines
// would cost a row of the very table this tab exists to show.
//
// The gap before it is spaces rather than the entries' "·": the plan is not
// another position entry, it is one claim about the whole board, and joining
// their list would read as though it were.
func (b Board) planTail(used int, overflowed bool, w int) string {
	if overflowed {
		return "" // the entries already spent the row and said so with a "+n"
	}
	plan := b.planCopy()
	if plan == "" {
		return "" // last round: no second pick to plan for
	}
	gap := "   "
	if used+lipgloss.Width(gap)+lipgloss.Width(plan) > w-8 {
		return ""
	}
	return gap + Dim.Render(plan)
}

// lastName compresses "jeremiyah love" to "love" for the urgency strip; a
// full name there costs width the pairs need.
func lastName(full string) string {
	fields := strings.Fields(strings.ToLower(full))
	if len(fields) == 0 {
		return "?"
	}
	return trunc(fields[len(fields)-1], 10)
}
