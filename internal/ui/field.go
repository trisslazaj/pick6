package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
)

// The blocks under the recommendation. The choice rows are the decision; these
// are the board behind it — what the room is about to do, how deep each tier
// still runs — and they exist because the left
// pane used to stop after five rows and leave two-thirds of the terminal blank
// at every size.
//
// They are added in priority order and ONLY while they fit the height budget,
// so a short terminal keeps the recommendation and the ticker, and a tall one
// fills up. Every block reads numbers the engine already holds; none of them
// runs anything new.

// fieldBlocks renders whatever fits under `used` rows of the left pane.
func (b Board) fieldBlocks(w, used int) string {
	budget := b.bodyRows()
	var sb strings.Builder
	for _, block := range []func(int) string{b.roomBlock, b.tierLadder} {
		out := block(w)
		if out == "" {
			continue
		}
		rows := rowCount(out) + 1 // the blank line above it
		if used+rows > budget {
			break
		}
		sb.WriteString("\n" + out)
		used += rows
	}
	return sb.String()
}

// ---- the room ----

// roomBlock is the rollouts read as picks: for every seat between now and my
// next chance to act, the position it took most often and how often, and the
// sum of it all as "expect 7 wr · 6 rb". Every survival percentage on the rows
// above is a claim about exactly these picks; this is the same claim in the
// units a drafter argues in. Sim only — the adp logistic has no rollouts to
// sum, so under -survival=adp the block is absent rather than empty.
func (b Board) roomBlock(w int) string {
	s := b.State
	fc := s.RoomForecast()
	if len(fc) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(sectionHead(fmt.Sprintf("the room before %s", b.pickLabel(s.ActPick())), w-2) + "\n")

	// The sum first: it is the actionable line.
	var parts []string
	for _, r := range s.ExpectedRemovals() {
		n := int(r.Expect + 0.5)
		if n == 0 {
			continue
		}
		if r.Pos == "" {
			parts = append(parts, Dim.Render(fmt.Sprintf("%d unranked", n)))
			continue
		}
		parts = append(parts, FG.Render(fmt.Sprintf("%d ", n))+b.pos(r.Pos, false).Render(strings.ToLower(r.Pos)))
	}
	if len(parts) > 0 {
		sb.WriteString(fitStyled("  "+Dim.Render("expect  ")+strings.Join(parts, Dim.Render(" · ")), w) + "\n")
	}

	// Then the men: everyone the window is likelier than not to take, lowest
	// survival first, capped at the window's own length. A per-pick strip was
	// tried here and read as twenty-one coin flips that all said "wr 45" —
	// the names are what the picks mean.
	var gone []engine.Player
	for _, p := range s.Players {
		if s.Taken[p.ID] || s.Suppressed(p.Pos) {
			continue
		}
		gone = append(gone, p)
	}
	surv := map[string]float64{}
	for _, p := range gone {
		surv[p.ID] = s.PSurviveTilted(p)
	}
	sort.Slice(gone, func(i, j int) bool {
		if surv[gone[i].ID] != surv[gone[j].ID] {
			return surv[gone[i].ID] < surv[gone[j].ID]
		}
		return gone[i].Value > gone[j].Value
	})
	var cells []string
	for _, p := range gone {
		if len(cells) >= len(fc) || surv[p.ID] >= 0.5 {
			break
		}
		cells = append(cells, b.pos(p.Pos, false).Render(strings.ToLower(p.Name)))
	}
	if len(cells) > 0 {
		lines := packCells(cells, Dim.Render(" · "), w-15)
		// Three lines of names is a read; ten is a table. The rest is a count.
		if len(lines) > goneLines {
			shown := 0
			for _, line := range lines[:goneLines] {
				shown += strings.Count(line, " · ") + 1
			}
			lines = lines[:goneLines]
			lines[goneLines-1] += Dim.Render(fmt.Sprintf(" +%d", len(cells)-shown))
		}
		for i, line := range lines {
			lead := Dim.Render("likely gone  ")
			if i > 0 {
				lead = strings.Repeat(" ", 13)
			}
			sb.WriteString("  " + lead + line + "\n")
		}
	}
	return sb.String()
}

// goneLines caps the likely-gone list. Past three lines the names stop being
// a glance and the count says the same thing.
const goneLines = 3

// packCells greedily fills lines with styled cells joined by sep, wrapping at w
// cells. Never splits a cell.
func packCells(cells []string, sep string, w int) []string {
	var lines []string
	cur, curW := "", 0
	sepW := lipgloss.Width(sep)
	for _, c := range cells {
		cw := lipgloss.Width(c)
		if curW > 0 && curW+sepW+cw > w {
			lines = append(lines, cur)
			cur, curW = "", 0
		}
		if curW > 0 {
			cur += sep
			curW += sepW
		}
		cur += c
		curW += cw
	}
	if curW > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// fitStyled clips a styled line to w cells, ansi-aware, with an ellipsis.
func fitStyled(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w-1).Render(s) + Dim.Render("…")
}

// ---- tiers ----

// tierLadder is cliff detection as a picture: one row per tiered position,
// each tier a run of dots, filled where the man is gone and hollow where he is
// still on the board. Tiers already emptied collapse to "t1–3 gone" so the row
// starts at the band that matters, and that band's hollow dots take the alarm
// colour when the engine says it will not hold — the same Cliff the rows and
// the banner read, so the ladder cannot disagree with them.
func (b Board) tierLadder(w int) string {
	var rows []string
	// Every position, not the four tiered nfl ones. ladderRow already returns ""
	// for a position with no tiers, so k and def drop out on their own and the
	// nfl ladder is unchanged — while gkp, def, mid and fwd, all four of which
	// ARE tiered, stop being invisible.
	for _, pos := range b.State.Positions() {
		if row := b.ladderRow(pos, w-4); row != "" {
			rows = append(rows, "  "+row)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return sectionHead("tiers — filled is gone", w-2) + "\n" + strings.Join(rows, "\n") + "\n"
}

func (b Board) ladderRow(pos string, w int) string {
	s := b.State
	// Every tiered player at the position, by tier.
	size := map[int]int{}
	left := map[int]int{}
	maxTier := 0
	for id, p := range s.Players {
		if p.Pos != pos || p.Tier == 0 {
			continue
		}
		size[p.Tier]++
		if !s.Taken[id] {
			left[p.Tier]++
		}
		if p.Tier > maxTier {
			maxTier = p.Tier
		}
	}
	if maxTier == 0 {
		return ""
	}
	level, best, _ := s.Cliff(pos)
	alarm := b.pos(pos, false)
	switch level {
	case engine.CliffLast:
		alarm = Cliff
	case engine.CliffWarning:
		alarm = Run
	}

	tag := b.pos(pos, s.Suppressed(pos)).Render(fmt.Sprintf("%-3s", strings.ToLower(pos)))
	used := 4
	var cells []string

	// Leading tiers with nobody left collapse to one clause.
	gone := 0
	for t := 1; t <= maxTier && size[t] > 0 && left[t] == 0; t++ {
		gone = t
	}
	if gone > 0 {
		lab := "t1 gone"
		if gone > 1 {
			lab = fmt.Sprintf("t1–%d gone", gone)
		}
		cells = append(cells, Dim.Render(lab))
		used += lipgloss.Width(lab)
	}
	rest := 0 // available players in tiers that did not fit
	for t := gone + 1; t <= maxTier; t++ {
		if size[t] == 0 {
			continue
		}
		dots := strings.Repeat("●", size[t]-left[t])
		hollow := strings.Repeat("○", left[t])
		lab := fmt.Sprintf("t%d ", t)
		cw := len(lab) + size[t]
		sep := 3
		if len(cells) == 0 {
			sep = 0
		}
		if rest > 0 || used+sep+cw > w-4 { // keep room for a "+n"; once one tier is out, the rest follow
			rest += left[t]
			continue
		}
		st := b.pos(pos, false)
		if t == best {
			st = alarm
		}
		cells = append(cells, Dim.Render(lab)+Dim.Render(dots)+st.Render(hollow))
		used += sep + cw
	}
	row := tag + " " + strings.Join(cells, Dim.Render(" · "))
	if rest > 0 {
		row += Dim.Render(fmt.Sprintf(" +%d", rest))
	}
	return row
}
