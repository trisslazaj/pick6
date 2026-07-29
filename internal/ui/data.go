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

// dataFilters cycles with the p key. Empty string means every position.
var dataFilters = []string{"", "RB", "WR", "TE", "QB", "K", "DEF"}

// survGoneBand is presentation, not tuning: below this the survival cell
// renders in the cliff red, because "he is gone" is the same family of alarm.
const survGoneBand = 0.15

// HandleKey consumes tab-switching and data-tab keys shared by both models.
// Returns false when the key isn't ours, so the caller's own bindings run.
func (b *Board) HandleKey(key string) bool {
	if key == "tab" {
		b.Tab = 1 - b.Tab
		return true
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
		b.DataFilter = nextFilter(b.DataFilter)
		b.DataScroll = 0
	default:
		return false
	}
	b.clampScroll()
	return true
}

func nextFilter(cur string) string {
	for i, f := range dataFilters {
		if f == cur {
			return dataFilters[(i+1)%len(dataFilters)]
		}
	}
	return ""
}

// dataRows is every available player under the current filter, best value
// first — the same comparator Available uses, so the two tabs agree.
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		ai, aj := out[i].ADP, out[j].ADP
		if ai <= 0 {
			ai = engine.UndraftedADP
		}
		if aj <= 0 {
			aj = engine.UndraftedADP
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
	n := b.Height - 11 // header, banner, urgency strip, title, column head, legend, footer
	if n < 8 {
		return 8
	}
	return n
}

// dataNameW scales the player column with the terminal: every other column is
// fixed-width, so the name takes the slack, floored at 18 and capped where
// even hyphenated receivers fit.
func dataNameW(w int) int {
	n := w - 62
	if n < 18 {
		return 18
	}
	if n > 26 {
		return 26
	}
	return n
}

func (b *Board) clampScroll() {
	max := len(b.dataRows()) - b.visibleDataRows()
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
	rows := b.dataRows()
	vis := b.visibleDataRows()
	off := b.DataScroll
	if max := len(rows) - vis; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	end := off + vis
	if end > len(rows) {
		end = len(rows)
	}

	var sb strings.Builder
	sb.WriteString(b.urgencyStrip(w))

	label := "all players — sorted by value"
	if b.DataFilter != "" {
		label = strings.ToLower(b.DataFilter) + " only — sorted by value"
	}
	counter := fmt.Sprintf("rows %d–%d of %d", off+1, end, len(rows))
	if len(rows) == 0 {
		counter = "no players"
	}
	left := Dim.Render(label)
	right := Dim.Render(counter)
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if pad < 1 {
		pad = 1
	}
	sb.WriteString("  " + left + strings.Repeat(" ", pad) + right + "\n")

	nameW := dataNameW(w)
	sb.WriteString(Dim.Render(fmt.Sprintf("  %-3s  %-*s  %-3s  %3s  %4s  %5s  %5s  %6s  %4s  %7s",
		"pos", nameW, "player", "tm", "bye", "tier", "value", "adp", "spread", "surv", "fmt gap")) + "\n")
	for _, p := range rows[off:end] {
		sb.WriteString(b.dataRow(p, nameW) + "\n")
	}
	sb.WriteString(Dim.Render(trunc(
		"  spread: how widely real drafts vary on him · fmt gap: adp shift by scoring format · d: derived tier",
		w-2)) + "\n")
	return sb.String()
}

// dataRow is one player, every column. Dashes mean "no source had a number",
// which is itself information — a dash-heavy row is a player the market has
// no opinion on.
func (b Board) dataRow(p engine.Player, nameW int) string {
	s := b.State
	style := Pos(p.Pos, false)

	// Amber numbers mark a faller, same as the board tab.
	numeric := Dim
	if s.Falling(p) {
		numeric = Run
	}

	// The tilted survival, same as the board tab's column: two panes quoting
	// different odds on the same player is the one failure this table cannot
	// afford, since reading the numbers is its entire job.
	surv := s.PSurviveTilted(p)
	survStyle := Dim
	switch {
	case s.Falling(p):
		survStyle = Run
	case surv >= engine.SurviveThreshold:
		survStyle = Wait
	case surv < survGoneBand:
		survStyle = Cliff
	}

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
		adp = fmt.Sprintf("%5.1f", p.ADP)
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

	return fmt.Sprintf("  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s",
		style.Render(fmt.Sprintf("%-3s", strings.ToLower(p.Pos))),
		style.Render(fmt.Sprintf("%-*s", nameW, trunc(strings.ToLower(p.Name), nameW))),
		Dim.Render(fmt.Sprintf("%-3s", p.Team)),
		Dim.Render(bye),
		Dim.Render(tier),
		FG.Render(value),
		numeric.Render(adp),
		numeric.Render(sd),
		survStyle.Render(fmt.Sprintf("%3.0f%%", 100*surv)),
		Dim.Render(sprd),
	)
}

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
	for _, pos := range positions {
		if s.Need(pos) == 0 {
			continue
		}
		now, ok := s.BestNow(pos)
		if !ok {
			continue
		}
		tag := Pos(pos, false).Bold(true).Render(strings.ToLower(pos))
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
	if len(entries) == 0 {
		note := Dim.Render("—")
		if !s.Done() && s.PicksUntilMine() == 0 {
			note = Dim.Render("your pick — urgency resets, take best value")
		}
		return "  " + Dim.Render("urgency  ") + note + "\n" +
			Dim.Render("  "+strings.Repeat("─", w-4)) + "\n"
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].u > entries[j].u })

	line := "  " + Dim.Render("urgency  ")
	used := lipgloss.Width(line)
	shown := 0
	for _, e := range entries {
		sep := ""
		if shown > 0 {
			sep = Dim.Render(" · ")
		}
		if used+lipgloss.Width(sep)+lipgloss.Width(e.text) > w-8 {
			line += Dim.Render(fmt.Sprintf(" +%d", len(entries)-shown))
			break
		}
		line += sep + e.text
		used += lipgloss.Width(sep) + lipgloss.Width(e.text)
		shown++
	}
	return line + "\n" + Dim.Render("  "+strings.Repeat("─", w-4)) + "\n"
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
