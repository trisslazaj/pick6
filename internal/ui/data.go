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
	n := b.Height - 10 // header, banner, urgency strip, title, column head, footer
	if n < 8 {
		return 8
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

	sb.WriteString(Dim.Render(fmt.Sprintf("  %-3s  %-20s  %-3s  %3s  %4s  %5s  %5s  %4s  %4s  %4s",
		"pos", "player", "tm", "bye", "tier", "value", "adp", "sd", "surv", "sprd")) + "\n")
	for _, p := range rows[off:end] {
		sb.WriteString(b.dataRow(p) + "\n")
	}
	return sb.String()
}

// dataRow is one player, every column. Dashes mean "no source had a number",
// which is itself information — a dash-heavy row is a player the market has
// no opinion on.
func (b Board) dataRow(p engine.Player) string {
	s := b.State
	style := Pos(p.Pos, false)

	// Amber numbers mark a faller, same as the board tab.
	numeric := Dim
	if s.Falling(p) {
		numeric = Run
	}

	surv := s.PSurvive(p)
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
	sd := "   —"
	if p.Stdev > 0 {
		sd = fmt.Sprintf("%4.1f", p.Stdev)
	}
	sprd := "   —"
	if p.FormatSpread > 0 {
		sprd = fmt.Sprintf("%4.1f", p.FormatSpread)
	}
	bye := "  —"
	if p.Bye > 0 {
		bye = fmt.Sprintf("%3d", p.Bye)
	}

	return fmt.Sprintf("  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s",
		style.Render(fmt.Sprintf("%-3s", strings.ToLower(p.Pos))),
		style.Render(fmt.Sprintf("%-20s", trunc(strings.ToLower(p.Name), 20))),
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
// urgency number and the bestNow→bestLater pair that produces it, or a green
// "safe" when waiting costs nothing. Sorted most urgent first; entries that
// don't fit collapse into a "+n" rather than wrapping.
//
// "safe" obeys the same guards as the board tab's safe-to-wait tag — not on
// my own pick (urgency is 0 for everyone then, and waiting isn't on offer),
// not for untiered positions (k/def have no math to be safe about), and not
// while the position's tier is cliffing. Two tabs contradicting each other
// about the same state is worse than either being wrong alone.
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
		level, _, _ := s.Cliff(pos)
		switch {
		case u > 0:
			later, _ := s.BestLater(pos)
			entries = append(entries, entry{fmt.Sprintf("%s %s %s", tag,
				FG.Render(fmt.Sprintf("%.0f", u)),
				Dim.Render(lastName(now.Name)+"→"+lastName(later.Name))), u})
		case now.Tier != 0 && level == engine.CliffNone && s.PicksUntilMine() > 0:
			entries = append(entries, entry{fmt.Sprintf("%s %s", tag, Wait.Render("safe")), 0})
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
