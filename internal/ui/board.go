package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
)

// Positions in display order when nothing else separates them.
var positions = []string{"RB", "WR", "TE", "QB", "K", "DEF"}

// Board renders the whole screen. Everything it emits is lowercase; only team
// abbreviations stay uppercase.
type Board struct {
	State  *engine.State
	Width  int
	Height int
	Synced time.Time
	Status string // transient message shown in the footer
}

// Layout bounds. The board has a natural width — a player row is about 46
// columns and a roster row about 34 — so stretching to fill a wide terminal just
// manufactures a gulf between the panes. Cap it and centre instead; extra height
// is spent showing more players, which is the thing you actually want more of.
const (
	MinWidth   = 80
	MaxWidth   = 92
	SidebarW   = 34
	MinDepth   = 3 // players shown per position group
	MaxDepth   = 8
	MinTickerN = 4
	MaxTickerN = 10
)

func (b Board) View() string {
	content := b.Width
	if content > MaxWidth {
		content = MaxWidth
	}
	if content < MinWidth {
		content = MinWidth
	}
	leftW := content - SidebarW - 3

	left := b.bestAvailable(leftW)
	right := b.sidebar(SidebarW)

	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(left),
		b.divider(maxLines(left, right)),
		lipgloss.NewStyle().Width(SidebarW).PaddingLeft(2).Render(right),
	)

	block := strings.Join([]string{
		b.header(content),
		b.banner(content), // "" when nothing is active; filtered below
		body,
		b.footer(content),
	}, "\n")
	block = strings.ReplaceAll(block, "\n\n\n", "\n\n")

	// Centre the board when the terminal is wider than it needs to be, rather
	// than letting the panes drift to opposite edges.
	if pad := (b.Width - content) / 2; pad > 0 {
		block = lipgloss.NewStyle().MarginLeft(pad).Render(block)
	}
	return block
}

// divider is the vertical rule between the panes.
func (b Board) divider(h int) string {
	if h < 1 {
		h = 1
	}
	rows := make([]string, h)
	for i := range rows {
		rows[i] = Dim.Render("│")
	}
	return lipgloss.NewStyle().Width(1).PaddingLeft(1).Render(strings.Join(rows, "\n"))
}

func maxLines(a, b string) int {
	la, lb := strings.Count(a, "\n")+1, strings.Count(b, "\n")+1
	if la > lb {
		return la
	}
	return lb
}

// depth decides how many players to show per position group, from the height we
// actually have. Each group costs a header plus its rows plus a blank line.
func (b Board) depth(groups int) int {
	if b.Height <= 0 || groups == 0 {
		return 4
	}
	avail := b.Height - 8 // header, banner, footer, breathing room
	perGroup := avail/groups - 2
	if perGroup < MinDepth {
		return MinDepth
	}
	if perGroup > MaxDepth {
		return MaxDepth
	}
	return perGroup
}

func (b Board) tickerRows() int {
	if b.Height <= 0 {
		return MinTickerN + 2
	}
	// The sidebar's fixed chrome is roughly 20 rows before the ticker starts.
	n := b.Height - 22
	if n < MinTickerN {
		return MinTickerN
	}
	if n > MaxTickerN {
		return MaxTickerN
	}
	return n
}

// ---- header ----

func (b Board) header(w int) string {
	s := b.State
	if s.Done() {
		return Bold.Foreground(lipgloss.Color(ColAccent)).Render("  draft complete") + "\n"
	}
	onClock := s.OnTheClock()
	until := s.PicksUntilMine()

	who := fmt.Sprintf("team %d", onClock)
	whoStyled := Dim.Render(who)
	if onClock == s.MySlot {
		whoStyled = Wait.Bold(true).Render("you")
	}

	left := fmt.Sprintf("  %s  %s  %s",
		Bold.Foreground(lipgloss.Color(ColAccent)).Render(
			fmt.Sprintf("round %d", s.Round(s.PickNo))),
		Dim.Render(fmt.Sprintf("pick %d.%02d", s.Round(s.PickNo), s.IndexInRound(s.PickNo))),
		Dim.Render(fmt.Sprintf("overall %d", s.PickNo)),
	)

	right := fmt.Sprintf("on the clock %s   %s", whoStyled, untilLabel(until))
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right + "\n" +
		Dim.Render("  "+strings.Repeat("─", w-4))
}

func untilLabel(n int) string {
	switch {
	case n == 0:
		return Wait.Bold(true).Render("your pick")
	case n == 1:
		return Run.Render("1 pick until yours")
	case n <= 3:
		return Run.Render(fmt.Sprintf("%d picks until yours", n))
	default:
		return Dim.Render(fmt.Sprintf("%d picks until yours", n))
	}
}

// ---- alert banner ----

// banner renders the run/cliff line, or "" when nothing is active. It never
// reserves an empty row — an always-present bar reads as broken.
func (b Board) banner(w int) string {
	s := b.State
	// A finished draft has no runs and no cliffs to act on. Anything urgent-
	// sounding here is stale by definition.
	if s.Done() {
		return ""
	}
	if run, ok := s.DetectRun(); ok {
		return b.runBanner(run, w)
	}
	// No run: surface the most urgent cliff, if any. Two last-men-standing at
	// once is rare but real, and the one whose position bleeds more value wins.
	type c struct {
		pos, msg string
	}
	var worst *c
	var worstU float64
	for _, pos := range positions {
		if s.Need(pos) == 0 {
			continue
		}
		level, tier, _ := s.Cliff(pos)
		if level != CliffLastLevel {
			continue
		}
		if u := s.Urgency(pos); worst == nil || u > worstU {
			worst = &c{pos, fmt.Sprintf("%s tier %d — last one. take him or lose the tier.",
				strings.ToLower(pos), tier)}
			worstU = u
		}
	}
	if worst == nil {
		return ""
	}
	return bar(w, ColCliff, "cliff", worst.msg)
}

// CliffLastLevel aliases the engine constant so callers here read cleanly.
const CliffLastLevel = engine.CliffLast

func (b Board) runBanner(run engine.Run, w int) string {
	pos := strings.ToLower(run.Pos)
	if run.TierBroke {
		alt := b.bestOtherPosition(run.Pos)
		msg := fmt.Sprintf("%s run — tier broke, no value left.", pos)
		if alt != "" {
			msg += fmt.Sprintf(" best value now: %s.", alt)
		}
		return bar(w, ColCliff, "run", msg)
	}
	return bar(w, ColRun, "run", fmt.Sprintf(
		"%s run in progress — %d of the last %d picks. %d left in tier %d. act now or lose it.",
		pos, run.Count, engine.RunWindow, run.TierLeft, run.Tier))
}

// bestOtherPosition names the highest-urgency position besides the one on a
// run. Returns "" when everything else is safe to wait on — the banner just
// drops the clause rather than naming a position with nothing at stake.
func (b Board) bestOtherPosition(exclude string) string {
	best, bestScore := "", 0.0
	for _, pos := range positions {
		if pos == exclude {
			continue
		}
		if score := b.State.Urgency(pos); score > bestScore {
			best, bestScore = strings.ToLower(pos), score
		}
	}
	return best
}

func bar(w int, color, tag, msg string) string {
	body := fmt.Sprintf(" %s  %s ", strings.ToUpper(tag), msg)
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#1A1B26")).
		Background(lipgloss.Color(color)).
		Bold(true).
		Width(w - 4).
		MarginLeft(2).
		Render(strings.ToLower(body))
}

// ---- left pane: best available ----

func (b Board) bestAvailable(w int) string {
	s := b.State
	type group struct {
		pos     string
		players []engine.Player
		score   float64
		value   float64
	}
	var groups []group
	for _, pos := range positions {
		// A suppressed position (K and DEF before the last rounds) is hidden
		// outright, not just sorted last. The tool must never imply you should be
		// thinking about kickers in round 3, and eight rows of them is exactly
		// that implication.
		if s.Need(pos) == 0 {
			continue
		}
		avail := s.Available(pos)
		if len(avail) == 0 {
			continue
		}
		// Groups sort by urgency: the need-weighted value lost by waiting until
		// my next pick. Near my own pick urgencies collapse to zero — nobody can
		// be taken in zero picks — so ties fall to need-weighted best value,
		// which keeps the board pointing at the pick instead of going limp
		// exactly when I'm on the clock.
		groups = append(groups, group{pos, avail, s.Urgency(pos),
			float64(avail[0].Value) * s.Need(pos)})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].score != groups[j].score {
			return groups[i].score > groups[j].score
		}
		return groups[i].value > groups[j].value
	})

	depth := b.depth(len(groups))
	var sb strings.Builder
	sb.WriteString(sectionHead("best available", w-2) + "\n")
	for gi, g := range groups {
		sb.WriteString(b.groupBlock(g.pos, g.players, gi == 0, w, depth))
	}
	return sb.String()
}

func (b Board) groupBlock(pos string, avail []engine.Player, top bool, w, depth int) string {
	s := b.State
	suppressed := s.Need(pos) == 0
	style := Pos(pos, suppressed)

	level, tier, remaining := s.Cliff(pos)
	count := Dim.Render(fmt.Sprintf("%d left in tier %d", remaining, tier))
	switch level {
	case engine.CliffLast:
		count = Cliff.Bold(true).Render("last one in tier " + fmt.Sprint(tier))
	case engine.CliffWarning:
		count = Run.Render(fmt.Sprintf("%d left in tier %d — ending", remaining, tier))
	}
	if tier == 0 {
		count = Dim.Render("untiered")
	}

	head := fmt.Sprintf("%s  %s", style.Bold(true).Render(strings.ToLower(pos)), count)

	// Green only when there is truly nothing to do: cliff copy always wins,
	// untiered groups (k/def, value 0, urgency identically 0) never earn the
	// tag — "safe to wait" about a kicker in the last round would be a lie —
	// and neither does anything on my own pick, when waiting isn't on offer.
	if tier != 0 && level == engine.CliffNone && s.Urgency(pos) == 0 && s.PicksUntilMine() > 0 {
		head += "  " + Wait.Render("safe to wait")
	}

	// One accent, not five: only the top group gets a left border.
	edge := "  "
	if top {
		edge = style.Render("▏") + " "
	}

	var sb strings.Builder
	sb.WriteString("\n" + edge + head + "\n")
	for i, p := range avail {
		if i >= depth {
			break
		}
		sb.WriteString(edge + "  " + b.playerLine(p, style, w-6) + "\n")
	}
	return sb.String()
}

// playerLine drops columns rather than wrapping when the pane is tight. A row
// that wraps costs two lines and reads as broken; a row missing the bye week
// still tells you who and when.
//
// Width budget: the row is name + " " + meta(14) + " " + surv(4), plus "  " +
// bye(6) when it fits. Both thresholds are shifted by the survival column's
// five columns; lowering either wraps rows at content widths 81-82 or 89-91.
func (b Board) playerLine(p engine.Player, style lipgloss.Style, w int) string {
	nameW := 22
	if w < 40 {
		nameW = 16
	}
	name := style.Render(fmt.Sprintf("%-*s", nameW, trunc(strings.ToLower(p.Name), nameW)))
	// A falling player is a discount — the draft has moved past his price and
	// he's still here. Amber on the numbers, not the name: it's a state, and
	// the name keeps its position colour like everyone else's.
	numStyle := Dim
	if b.State.Falling(p) {
		numStyle = Run
	}
	meta := numStyle.Render(fmt.Sprintf("%-4s adp %5.1f", p.Team, p.ADP))
	// Chance he's still there at my next pick. The number the whole board runs
	// on, so show it rather than asking anyone to trust the ordering blind.
	surv := numStyle.Render(fmt.Sprintf("%3.0f%%", 100*b.State.PSurvive(p)))

	if w < nameW+26 { // no room for the bye column
		return fmt.Sprintf("%s %s %s", name, meta, surv)
	}
	bye := Dim.Render(fmt.Sprintf("bye %2d", p.Bye))
	if p.Bye == 0 {
		bye = Dim.Render("      ")
	}
	return fmt.Sprintf("%s %s %s  %s", name, meta, surv, bye)
}

// ---- right pane: roster + ticker ----

func (b Board) sidebar(w int) string {
	var sb strings.Builder
	sb.WriteString(sectionHead("your roster", w-2) + "\n")
	sb.WriteString(b.roster())
	sb.WriteString(b.insight())
	sb.WriteString("\n" + sectionHead("recent picks", w-2) + "\n")
	sb.WriteString(b.ticker())
	return sb.String()
}

// insight is the "so what" under the roster: what's still open, which bye week
// is quietly stacking up, and when you're up again. Everything here is derivable
// from the roster, but nobody derives it mid-draft with a clock running.
func (b Board) insight() string {
	s := b.State
	var sb strings.Builder

	if need := s.UnfilledStarters(s.MySlot); len(need) > 0 {
		parts := make([]string, len(need))
		for i, n := range need {
			// Colour each slot by position, and keep K/DEF faint while the engine
			// is suppressing them. Otherwise "need k def" in round 9 reads as an
			// instruction, contradicting the board that just hid every kicker.
			if n == "FLEX" {
				parts[i] = FG.Render("flex")
				continue
			}
			parts[i] = Pos(n, s.Need(n) == 0).Render(strings.ToLower(n))
		}
		sb.WriteString(fmt.Sprintf("\n  %s %s\n",
			Dim.Render("need "), strings.Join(parts, " ")))
	} else {
		sb.WriteString("\n  " + Dim.Render("need ") + " " +
			Wait.Render("lineup complete") + "\n")
	}

	// Only the worst bye week is worth the line; listing every collision turns
	// a warning into wallpaper.
	if conflicts := s.ByeConflicts(s.MySlot); len(conflicts) > 0 {
		c := conflicts[0]
		// The sidebar is narrow; a longer phrasing wraps onto its own line and
		// reads as a rendering fault rather than a warning.
		msg := fmt.Sprintf("wk %d — %d out", c.Week, len(c.Players))
		if len(conflicts) > 1 {
			msg += fmt.Sprintf(", +%d wk", len(conflicts)-1)
		}
		sb.WriteString(fmt.Sprintf("  %s %s\n", Dim.Render("byes "), Run.Render(msg)))
	}

	if !s.Done() {
		if picks := s.MyUpcomingPicks(2); len(picks) > 0 {
			parts := make([]string, len(picks))
			for i, p := range picks {
				parts[i] = fmt.Sprintf("%d.%02d", s.Round(p), s.IndexInRound(p))
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n",
				Dim.Render("up   "),
				Accent.Render(strings.Join(parts, ", then "))))
		}
	}
	return sb.String()
}

func (b Board) roster() string {
	s := b.State
	filled, bench := s.FilledSlots(s.MySlot)

	var sb strings.Builder
	for i, slot := range s.Roster.Slots {
		label := Dim.Render(fmt.Sprintf("%-5s", strings.ToLower(slot)))
		if filled[i] == "" {
			sb.WriteString(fmt.Sprintf("  %s %s\n", label, Dim.Render("—")))
			continue
		}
		p := s.Players[filled[i]]
		byeNote := ""
		if p.Bye > 0 {
			byeNote = Dim.Render(fmt.Sprintf("  bye %d", p.Bye))
		}
		sb.WriteString(fmt.Sprintf("  %s %s%s\n", label,
			Pos(p.Pos, false).Render(trunc(strings.ToLower(p.Name), 18)), byeNote))
	}
	for i, id := range bench {
		p := s.Players[id]
		sb.WriteString(fmt.Sprintf("  %s %s\n",
			Dim.Render(fmt.Sprintf("bn%-3d", i+1)),
			Pos(p.Pos, false).Render(trunc(strings.ToLower(p.Name), 18))))
	}
	return sb.String()
}

func (b Board) ticker() string {
	s := b.State
	picks := s.Picks
	if n := b.tickerRows(); len(picks) > n {
		picks = picks[len(picks)-n:]
	}
	if len(picks) == 0 {
		return Dim.Render("  nothing yet") + "\n"
	}
	var sb strings.Builder
	for i := len(picks) - 1; i >= 0; i-- {
		pk := picks[i]
		p := s.Players[pk.PlayerID]
		label := fmt.Sprintf("%d.%02d", pk.Round, ((pk.PickNo-1)%s.Teams)+1)
		pos := fmt.Sprintf("%-3s", strings.ToLower(p.Pos))
		name := trunc(strings.ToLower(p.Name), 17)

		// Your own picks are the thing you scan for, so they get a green bar and
		// their own label rather than a dot you have to hunt for.
		if pk.Slot == s.MySlot {
			sb.WriteString(fmt.Sprintf("%s %s %s %s\n",
				Wait.Bold(true).Render("▌you"),
				Dim.Render(label),
				Pos(p.Pos, false).Render(pos),
				Bold.Foreground(lipgloss.Color(ColFG)).Render(name)))
			continue
		}
		sb.WriteString(fmt.Sprintf("     %s %s %s\n",
			Dim.Render(label),
			Pos(p.Pos, false).Render(pos),
			Dim.Render(name)))
	}
	return sb.String()
}

// ---- footer ----

func (b Board) footer(w int) string {
	keys := []string{"space step", "a auto", "u undo", "q quit"}
	left := "  " + Dim.Render(strings.Join(keys, "   "))

	right := Dim.Render(fmt.Sprintf("synced %s ago", since(b.Synced)))
	if b.Status != "" {
		right = Accent.Render(strings.ToLower(b.Status))
	}
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return Dim.Render("  "+strings.Repeat("─", w-4)) + "\n" +
		left + strings.Repeat(" ", pad) + right
}

func since(t time.Time) string {
	if t.IsZero() {
		return "0s"
	}
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func trunc(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// sectionHead is a dim label with a rule under it, so each pane reads as a block
// rather than rows floating in space.
func sectionHead(label string, w int) string {
	if w < len(label)+2 {
		w = len(label) + 2
	}
	return Dim.Render(label) + " " + Dim.Render(strings.Repeat("─", w-len(label)-1))
}
