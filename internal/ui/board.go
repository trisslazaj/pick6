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

	// Reserve is rows the caller will draw BELOW this board — live mode's sticky
	// stale warning, its desync line, the poll error, "draft complete". The panes
	// are sized to fill Height exactly, so a caller appending a line without
	// charging for it makes the frame one row taller than the terminal, which
	// scrolls the header off the top and defeats the alt-screen repaint. Sticky
	// lines make that permanent: a board older than StaleADPHours sat one row
	// over for the whole draft, in exactly the state the warning exists for.
	//
	// It buys back a row only where the board has one to give. depth() is a
	// quantised knob — a row per GROUP, not a row total — so at 40 lines charging
	// one row costs four and the frame comes in three under rather than one over,
	// which is the right direction. Below MinDepth there is nothing left to give
	// and the frame stays put: at exactly 28 lines it is still one over, which is
	// the pre-existing floor (a 24-line terminal already renders 28 rows), not
	// something this field can fix.
	Reserve int

	// Fresh is how old the data underneath all of this is, handed in by the cmd
	// layer. The zero value means "no meta.json" and renders as nothing at all,
	// which is the honest answer for a board fetched before that file existed.
	Fresh Freshness
	// Now is the clock the news chip reads; zero means the wall clock. A field
	// only so tests can pin it.
	Now time.Time

	// Tab 0 is the board; tab 1 is the data table — every player, every
	// number, nothing abstracted away. State for the latter lives here so
	// both the mock and live models share it.
	Tab        int
	DataScroll int
	DataFilter string // "" = all positions
}

// Layout bounds. Rows scale with width — wider terminals buy longer names and
// a roomier sidebar — up to the point where columns can't usefully consume more
// and stretching would only manufacture a gulf between the panes. Cap there and
// centre; extra height is spent showing more players, which is the thing you
// actually want more of.
const (
	MinWidth    = 80
	MaxWidth    = 104
	SidebarW    = 34 // minimum; grows with the terminal up to SidebarMaxW
	SidebarMaxW = 38
	MinDepth    = 3 // players shown per position group
	MaxDepth    = 8
	MinTickerN  = 4
	MaxTickerN  = 10
)

// sidebarWidth gives the sidebar a share of any width beyond the old 92-column
// board, so wide terminals buy unclipped roster names instead of a wider gulf.
func sidebarWidth(content int) int {
	sw := SidebarW + (content-92)/3
	if sw < SidebarW {
		return SidebarW
	}
	if sw > SidebarMaxW {
		return SidebarMaxW
	}
	return sw
}

func (b Board) View() string {
	content := b.Width
	if content > MaxWidth {
		content = MaxWidth
	}
	if content < MinWidth {
		content = MinWidth
	}
	var body string
	if b.Tab == 1 {
		body = b.dataPane(content)
	} else {
		sw := sidebarWidth(content)
		leftW := content - sw - 3
		left := b.bestAvailable(leftW)
		right := b.sidebar(sw)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(leftW).Render(left),
			b.divider(maxLines(left, right)),
			lipgloss.NewStyle().Width(sw).PaddingLeft(2).Render(right),
		)
	}

	head, banner, foot := b.header(content), b.banner(content), b.footer(content)
	assemble := func(body string) string {
		block := strings.Join([]string{head, banner, body, foot}, "\n")
		// An absent banner still contributes its separator, and a body that opens
		// on a blank line then collapses it again — so the chrome is 4 rows or 5
		// depending on state, which is why the clamp below measures instead of
		// subtracting a constant.
		return strings.ReplaceAll(block, "\n\n\n", "\n\n")
	}
	block := assemble(body)

	// Never render taller than the terminal. bubbletea clips from the TOP, so an
	// overshoot costs the header and the alert banner — during a run, which is
	// the exact moment someone is looking. depth() and tickerRows() both budget
	// by arithmetic that overshoots (a full nine-slot lineup, six bench and a
	// ticker genuinely do not fit 24 rows late in a draft), so this measures the
	// assembled frame and takes the difference off the bottom of the body, where
	// it costs the oldest ticker rows and the least urgent group.
	//
	// The data tab is clamped too. visibleDataRows budgets for the chrome but
	// floors at 8 rows, so it cannot shrink past that: at height 20 with live
	// mode drawing a two-line trailer the pane came out one row over, and at 18
	// three over, which costs the header the same way.
	if b.Height > 0 {
		if over := rowCount(block) + b.Reserve - b.Height; over > 0 {
			block = assemble(clampRows(body, rowCount(body)-over))
		}
	}

	// Centre the board when the terminal is wider than it needs to be, rather
	// than letting the panes drift to opposite edges.
	if pad := (b.Width - content) / 2; pad > 0 {
		block = lipgloss.NewStyle().MarginLeft(pad).Render(block)
	}
	return block
}

// bodyRows is the sidebar's row budget: the terminal less the chrome it cannot
// have. Approximate on purpose — it decides how much bench to show, and the
// clamp in View is what actually guarantees the frame fits.
func (b Board) bodyRows() int {
	if b.Height <= 0 {
		return 1 << 30 // unset height means "don't budget", as in snapshot tests
	}
	if n := b.Height - 5 - b.Reserve; n > 1 {
		return n
	}
	return 1
}

// rowCount is visible rows: a trailing newline is a terminator, not a row.
func rowCount(s string) int {
	return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n"))
}

// clampRows drops trailing rows past n. It never pads: a short frame should
// stay short rather than pushing the footer to the bottom of the terminal.
func clampRows(s string, n int) string {
	if n < 1 {
		n = 1
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n")
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
	// The plan line is one more row the left pane spends and this does not charge
	// for it, deliberately. Taking it off avail before the division costs a row
	// per GROUP, not a row total — at 40 lines with four groups the frame shrank
	// from 39 rows to 36, trading two player rows to reclaim one. The budget is
	// already approximate (it overshoots by three at 24 lines, which is its own
	// tracked bug); one predictable row beats a lumpy four.
	avail := b.Height - 8 - b.Reserve // header, banner, footer, breathing room
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
	// It charges Reserve too: late in a draft one or two groups survive and the
	// sidebar becomes the taller pane, so it is the one setting frame height.
	n := b.Height - 22 - b.Reserve
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

	who := fmt.Sprintf("team %d", onClock)
	whoStyled := Dim.Render(who)
	if onClock == s.MySlot {
		whoStyled = Wait.Bold(true).Render("you")
	}

	left := fmt.Sprintf("  %s  %s  %s",
		Bold.Foreground(lipgloss.Color(ColAccent)).Render(
			fmt.Sprintf("round %d", s.Round(s.PickNo))),
		Dim.Render("pick "+b.pickLabel(s.PickNo)),
		Dim.Render(fmt.Sprintf("overall %d", s.PickNo)),
	)

	right := fmt.Sprintf("on the clock %s   %s", whoStyled, b.untilNote())
	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right + "\n" +
		Dim.Render("  "+strings.Repeat("─", w-4))
}

// pickLabel renders an overall pick as round.pick — "2.10" — which is how
// everyone at a draft says it out loud. Shared because the header, the "up" line
// and the plan all quote pick numbers, and three copies of one format string are
// three chances for one of them to drift.
func (b Board) pickLabel(p int) string {
	return fmt.Sprintf("%d.%02d", b.State.Round(p), b.State.IndexInRound(p))
}

// untilNote is the header's right-hand clause about my own turn, and it asks
// whether I still have one before quoting a distance to it.
//
// PicksUntilMine cannot answer that alone: past my last pick of the draft
// NextPick has no answer and falls back to the final pick, which is behind us,
// so the count reads 0 — the same 0 that means "you're on the clock". The header
// then claimed every remaining pick was mine, for the whole tail of the draft.
// MyUpcomingPicks is empty exactly when I am done, which is the question.
func (b Board) untilNote() string {
	if len(b.State.MyUpcomingPicks(1)) == 0 {
		return Dim.Render("no picks left")
	}
	return untilLabel(b.State.PicksUntilMine())
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
	// No run: surface the most urgent cliff, if any. Two tiers about to vanish
	// at once is rare but real, and the one whose position bleeds more value
	// wins.
	type c struct {
		pos, msg string
	}
	var worst *c
	var worstU float64
	for _, pos := range positions {
		if s.Need(pos) == 0 {
			continue
		}
		level, tier, remaining := s.Cliff(pos)
		if level != CliffLastLevel {
			continue
		}
		hold, _ := s.TierHold(pos)
		if u := s.Urgency(pos); worst == nil || u > worstU {
			worst = &c{pos, cliffMsg(strings.ToLower(pos), tier, remaining, b.holdNote(hold), w)}
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

// cliffMsg is the banner's copy for a tier that probably won't reach me.
//
// "last one" is a claim about the COUNT, and the count is no longer what fires
// the alarm: cliff level comes from the probability the tier holds, so red now
// fires with three men left when all three are contested. Calling three players
// "last one" would be flatly untrue, so that wording is kept for the case that
// earns it and the probabilistic case says what it actually knows.
//
// The imperative is dropped rather than truncated when the row can't hold it —
// the long-form-then-short rule the footer, the group header and the endgame
// line all follow. Naming the horizon costs eight cells and at 80 columns the
// banner had about two to spare, so it is the tail that gives.
func cliffMsg(pos string, tier, remaining int, hold string, w int) string {
	head := fmt.Sprintf("%s tier %d unlikely to hold — %s.", pos, tier, hold)
	tail := " take one or lose the tier."
	if remaining == 1 {
		head = fmt.Sprintf("%s tier %d — last one.", pos, tier)
		tail = " take him or lose the tier."
	}
	if barFits("cliff", head+tail, w) {
		return head + tail
	}
	return head
}

// barFits reports whether a banner body survives bar's truncation at this width.
func barFits(tag, msg string, w int) bool {
	return len([]rune(fmt.Sprintf(" %s  %s ", tag, msg))) <= w-4
}

// pct renders a probability the way every other number on the board is
// rendered: whole percent, no decimals, nothing to read past.
func pct(p float64) string { return fmt.Sprintf("%.0f%%", 100*p) }

// holdNote is a tier's hold probability, with the pick it is measured to named
// only when that pick is not the one the survival column beside it uses.
//
// Off the clock the two horizons are the same and the extra words are noise. On
// the clock they are not: survival is priced to my next pick, which IS this one,
// so every cell reads 100%, while the tier's hold is priced to the pick after —
// the one passing actually costs me. Both numbers are right for their own
// horizon and neither said which, which is how "holds 3%" ended up printed
// directly above three men each reading 100%.
func (b Board) holdNote(hold float64) string {
	at := b.State.TierHoldPick()
	if at == b.State.NextPick() {
		return "holds " + pct(hold)
	}
	return fmt.Sprintf("holds %s to %s", pct(hold), b.pickLabel(at))
}

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
	// K and DEF carry no value from any source, so they carry no tier, so a run
	// on them has no tier state to report — no count, no hold probability, and
	// nothing that could break. The run itself is the whole message, and amber
	// rather than red: the room is moving, it is not costing you a tier.
	if run.Tier == 0 {
		return bar(w, ColRun, "run", fmt.Sprintf("%s run — %d of the last %d picks.",
			pos, run.Count, engine.RunWindow))
	}
	// The count is the run's evidence. Whether it costs you anything is the
	// tier's hold probability, and "act now or lose it" is a claim about that
	// rather than about the count: with cliff levels priced by probability, a
	// run banner sat above a group header reading "safe to wait" on 21 of the 41
	// run frames of the scripted mock — the same frame telling the reader to act
	// and to wait about the same position. So the imperative is earned by the
	// same threshold that turns that header amber, and a run whose tier will
	// keep reports the room moving instead of manufacturing an alarm.
	msg := fmt.Sprintf("%s run in progress — %d of the last %d picks. %d left in tier %d",
		pos, run.Count, engine.RunWindow, run.TierLeft, run.Tier)
	if hold, ok := b.State.TierHold(run.Pos); ok && hold >= engine.TierHoldWarn {
		// The number goes on the calm wording only, and it is a width budget as
		// much as a copy choice: the alarm wording plus a percentage overruns
		// the banner's single row and truncates. When the alarm fires the group
		// header below is carrying the same number anyway.
		msg += ", " + b.holdNote(hold) + "."
	} else {
		msg += ". act now or lose it."
	}
	return bar(w, ColRun, "run", msg)
}

// bestOtherPosition names the highest-urgency position besides the one on a
// run, skipping any the board is simultaneously tagging "safe to wait".
// Returns "" when everything else is safe — the banner drops the clause rather
// than pointing at a position whose own group header, three lines below, says
// there is no hurry.
//
// The skip used to be implicit: a safe position scored exactly 0 under
// milestone-4 urgency and lost the argmax by itself. Continuous urgency gives
// it a real number, and it started winning.
func (b Board) bestOtherPosition(exclude string) string {
	best, bestScore := "", 0.0
	for _, pos := range positions {
		if pos == exclude || b.State.SafeToWait(pos) {
			continue
		}
		if score := b.State.Urgency(pos); score > bestScore {
			best, bestScore = strings.ToLower(pos), score
		}
	}
	return best
}

func bar(w int, color, tag, msg string) string {
	body := strings.ToLower(fmt.Sprintf(" %s  %s ", strings.ToUpper(tag), msg))
	// Truncate, never wrap: a two-line banner makes the frame one taller than
	// the terminal and bubbletea clips the header — during a run, the exact
	// moment someone is looking. The head of the copy carries the information.
	body = trunc(body, w-4)
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColInk)).
		Background(lipgloss.Color(color)).
		Bold(true).
		Width(w - 4).
		MarginLeft(2).
		Render(body)
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
		// my next pick. On my own pick every urgency is exactly zero — nobody can
		// be taken in zero picks — so the tie-break is what keeps the board
		// pointing at the pick instead of going limp exactly when I'm choosing.
		//
		// It is need-weighted VOR, not need-weighted value. Raw value ranks a
		// position by its best man; vor ranks it by what that man buys over the
		// one this room would have ended up with anyway (engine/vor.go). On
		// today's board that is a 314-point discount on quarterbacks and 182 on
		// tight ends against none at all on running backs, which is the depth of
		// those positions expressed as a number.
		//
		// Since phase 1 made urgency continuous, exact ties are essentially
		// unreachable ANYWHERE ELSE — which makes this tie-break more important
		// than it sounds, not less: it now fires almost exclusively on the frame
		// where I am on the clock and about to act on it.
		groups = append(groups, group{pos, avail, s.Urgency(pos),
			s.VOR(avail[0]) * s.Need(pos)})
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
	sb.WriteString(b.planLine(w))
	sb.WriteString(b.endgameLine(w))
	for gi, g := range groups {
		sb.WriteString(b.groupBlock(g.pos, g.players, gi == 0, w, depth))
	}
	return sb.String()
}

// planCopy is the two-pick lookahead in words: which position to take with my
// next pick, and which to expect with the one after it. Both legs are named by
// their pick numbers and never by "now" — the plan is computed as-if standing at
// my next pick but drawn on every frame, and on eleven frames out of twelve that
// pick belongs to somebody else.
//
// The pair's score is deliberately NOT rendered. It ranks pairs; it does not
// forecast what you will get, because its first leg is priced at the value of
// today's best available even when my next pick is eighteen picks away and he
// will plainly be gone by then. Printed as "(e 12318)" it read as an expectation
// while sitting three rows above the same man's survival column reading 0% — one
// line of the board contradicting the evidence directly under it, on 27 of 166
// plan frames of the scripted mock. The pair is the product; the number was
// noise wearing a label it could not honour.
//
// "" when there is no second pick to plan for.
func (b Board) planCopy() string {
	plan, ok := b.State.BestPlan()
	if !ok {
		return ""
	}
	return fmt.Sprintf("plan  %s at %s → %s at %s",
		strings.ToLower(plan.First), b.pickLabel(plan.FirstPick),
		strings.ToLower(plan.Second), b.pickLabel(plan.SecondPick))
}

// planLine is that copy as the left pane's first row, or "" in the last round.
// When there is no second pick the line disappears outright rather than
// reserving an empty one — the rule the alert banner already follows, and the
// reason both read as intentional rather than broken.
//
// Dim, because it is a suggestion sitting on top of the board that holds the
// evidence for it. The group order, the tiers and the survivals below are what
// you actually read; this is the one-line summary you're free to ignore.
//
// The line has no short form to fall back to because it never needs one: its
// widest possible copy is 33 cells ("plan  def at 15.12 → def at 15.12") and the
// tightest this pane ever gets is 43, at 80 columns with the widest sidebar.
func (b Board) planLine(w int) string {
	line := b.planCopy()
	if line == "" {
		return ""
	}
	return "  " + Dim.Render(line) + "\n"
}

// endgameLine says out loud what the engine has already done to the board: at
// the point where my remaining picks exactly equal my open starting slots, every
// pick left is spoken for, and needFrom has zeroed the need on everything that
// cannot fill one — so whole position groups vanish from the pane above.
//
// Without the line that disappearance looks like a bug. With it, it reads as the
// tool doing arithmetic the drafter has stopped doing at round 13.
//
// Dim and one row, like the plan line, and it costs the layout nothing it can't
// afford: it can only appear once the suppression has already removed groups, so
// the pane it adds a row to is the shortest it ever gets. "" the rest of the
// time — no reserved empty row, the rule the banner and the plan both follow.
//
// The line has to name any open slot the pane is NOT showing, or it asserts a
// constraint over positions that are not on screen. K and DEF are hidden by the
// unrelated KDefLastRounds suppression, which outlives the endgame arithmetic by
// several rounds: at pick 11.04 of the scripted mock (seed 1) the roster needs
// rb, te, k and def with exactly four picks left, so the line fires — and the
// pane shows two of the four positions it is talking about. Naming them is the
// smaller fix; un-suppressing them would tell somebody to draft a kicker in
// round 11, which is the one thing the suppression exists to prevent.
//
// Long and short forms for the same reason the tier header has them: at 80
// columns the left pane is 43 cells and the long form does not fit.
func (b Board) endgameLine(w int) string {
	if !b.State.MustFillStarters() {
		return ""
	}
	line := "every remaining pick must fill a starter"
	if hidden := b.hiddenStarters(); len(hidden) > 0 {
		names := strings.Join(hidden, ", ")
		line = line + " · " + names + " included"
		if 2+lipgloss.Width(line) > w {
			line = "every pick must fill a starter · " + names
		}
	}
	return "  " + Dim.Render(line) + "\n"
}

// hiddenStarters lists the open starting slots whose group the pane above is not
// drawing, lowercase and in lineup order.
//
// Only a suppressed position can land here: needFrom returns NeedStarter for
// every open dedicated slot, so the only way an unfilled starter reads zero need
// is the k/def suppression. FLEX is skipped because it is a slot name and not a
// position — bestAvailable never draws a group called "flex", so it cannot be
// missing from one.
func (b Board) hiddenStarters() []string {
	s := b.State
	var out []string
	for _, slot := range s.UnfilledStarters(s.MySlot) {
		for _, pos := range positions {
			if slot == pos && s.Need(pos) == 0 {
				out = append(out, strings.ToLower(pos))
			}
		}
	}
	return out
}

// tierLabel is the group header's tier clause, in a long form and a short one.
//
// It carries both the remaining count and the probability at least one of those
// players is still there the next time I can act (on the clock, that means after
// this pick), because the two answer different questions and the second is the
// one the cliff levels now read. A count cannot
// tell three players the room is about to eat from one player nobody wants, and
// those are opposite situations; showing the count alone would leave the reader
// unable to see why an eight-man tier went red.
func (b Board) tierLabel(pos string) (long, short string, style lipgloss.Style) {
	s := b.State
	level, tier, remaining := s.Cliff(pos)
	if tier == 0 {
		// K and DEF carry no value from any source, so they carry no tier and
		// there is no hold probability to quote about them.
		return "untiered", "untiered", Dim
	}
	hold, _ := s.TierHold(pos) // cannot fail once Cliff found a tiered player
	note := b.holdNote(hold)
	switch {
	case level == engine.CliffLast && remaining == 1:
		// A count claim, and still a true one — keep the wording that says it.
		short = fmt.Sprintf("last one in tier %d", tier)
		return short, short, Cliff.Bold(true)
	case level == engine.CliffLast:
		short = fmt.Sprintf("tier %d unlikely to hold", tier)
		return short + " — " + note, short, Cliff.Bold(true)
	case level == engine.CliffWarning:
		short = fmt.Sprintf("%d left in tier %d — ending", remaining, tier)
		return short + " · " + note, short, Run
	default:
		short = fmt.Sprintf("%d left in tier %d", remaining, tier)
		return short + " · " + note, short, Dim
	}
}

func (b Board) groupBlock(pos string, avail []engine.Player, top bool, w, depth int) string {
	s := b.State
	suppressed := s.Need(pos) == 0
	style := Pos(pos, suppressed)

	// The green tag comes straight from the engine, which is also what the data
	// tab's strip asks. It used to be inferred from urgency == 0, and urgency is
	// continuous now — an exact zero happens only on my own pick, so the old
	// condition would have quietly stopped firing anywhere else. Every guard it
	// carried (cliff copy wins, untiered k/def never claim safety, nothing is
	// "safe to wait" when the wait is zero picks long) lives in SafeToWait now.
	tag := strings.ToLower(pos)
	wait := ""
	if s.SafeToWait(pos) {
		wait = "  safe to wait"
	}

	// The header drops its hold clause rather than wrapping. At 80 columns the
	// left pane is 43 wide and the long form plus a safe-to-wait tag overruns
	// it; a wrapped group header reads as a rendering fault, the same reason
	// playerLine drops the bye column instead of spilling.
	long, short, countStyle := b.tierLabel(pos)
	count := long
	// Measured in display cells, not bytes: the separators are a middle dot and
	// an em dash, and counting their utf-8 length would drop the hold clause a
	// few columns before it actually stopped fitting.
	if 2+lipgloss.Width(tag)+2+lipgloss.Width(count)+lipgloss.Width(wait) > w-2 {
		count = short
	}

	head := fmt.Sprintf("%s  %s", style.Bold(true).Render(tag), countStyle.Render(count))
	if wait != "" {
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
// bye(6) when it fits. The name takes whatever the pane can spare (row =
// nameW+28 with bye, so nameW = w-26 fills the budget exactly); below w=42
// even the floor name doesn't leave room for the bye column.
//
// Chips ride inside the name column and are paid for out of it, so this budget
// is untouched by them — at 80 columns the row already spends 36 of its 37
// cells and there is nothing to append to.
func (b Board) playerLine(p engine.Player, style lipgloss.Style, w int) string {
	nameW := w - 26
	if nameW < 16 {
		nameW = 16
	}
	if nameW > 26 {
		nameW = 26
	}
	name := b.nameCell(p, style, nameW)
	// A falling player is a discount — the draft has moved past his price and
	// he's still here. Amber on the numbers, not the name: it's a state, and
	// the name keeps its position colour like everyone else's.
	numStyle := Dim
	if b.State.Falling(p) {
		numStyle = Run
	}
	meta := numStyle.Render(fmt.Sprintf("%-4s adp %5.1f", p.Team, p.ADP))
	// Chance he's still there at my next pick. The number the whole board runs
	// on, so show it rather than asking anyone to trust the ordering blind —
	// and specifically the TILTED one, the same probability urgency, tier-hold
	// and the safe tag consume. One truth: a raw survival on screen next to an
	// ordering computed from the corrected one leaves nobody able to tell which
	// number the board actually believed.
	surv := numStyle.Render(fmt.Sprintf("%3.0f%%", 100*b.State.PSurviveTilted(p)))

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
	// Spend the rows in priority order. The lineup is the sidebar's reason to
	// exist and the insight lines are the "so what" under it, so neither is ever
	// cut; the bench collapses to a count first, and the ticker takes whatever
	// the body clamp still has to remove. Late in a draft a full nine-slot
	// lineup plus six bench plus a ticker genuinely does not fit 24 rows, and
	// bench depth is the least useful thing on screen at that moment — you know
	// who you drafted.
	insight := b.insight()
	fixed := 1 + len(b.State.Roster.Slots) + strings.Count(insight, "\n") + 2
	benchCap := b.bodyRows() - fixed - MinTickerN

	var sb strings.Builder
	sb.WriteString(sectionHead("your roster", w-2) + "\n")
	sb.WriteString(b.roster(w, benchCap))
	sb.WriteString(insight)
	sb.WriteString("\n" + sectionHead("recent picks", w-2) + "\n")
	sb.WriteString(b.ticker(w))
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
				parts[i] = b.pickLabel(p)
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n",
				Dim.Render("up   "),
				Accent.Render(strings.Join(parts, ", then "))))
		}
	}
	return sb.String()
}

func (b Board) roster(w, benchCap int) string {
	s := b.State
	filled, bench := s.FilledSlots(s.MySlot)

	// Row budget: 2 indent + 2 pane padding + label(6) + name + "  bye 11"(8).
	// A name past this wraps the bye onto its own line, which reads as broken.
	nameW := w - 18
	if nameW < 16 {
		nameW = 16
	}
	if nameW > 24 {
		nameW = 24
	}

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
			Pos(p.Pos, false).Render(trunc(strings.ToLower(p.Name), nameW)), byeNote))
	}
	// A negative cap means the sidebar has no room for the bench at all, but the
	// count line still earns its row: "six on the bench" is the difference
	// between a full roster and a rendering fault.
	shown := len(bench)
	if benchCap < shown {
		shown = benchCap
	}
	if shown < 0 {
		shown = 0
	}
	for i, id := range bench[:shown] {
		p := s.Players[id]
		sb.WriteString(fmt.Sprintf("  %s %s\n",
			Dim.Render(fmt.Sprintf("bn%-3d", i+1)),
			Pos(p.Pos, false).Render(trunc(strings.ToLower(p.Name), nameW))))
	}
	if hidden := len(bench) - shown; hidden > 0 {
		sb.WriteString(fmt.Sprintf("  %s\n",
			Dim.Render(fmt.Sprintf("+%d more on the bench", hidden))))
	}
	return sb.String()
}

func (b Board) ticker(w int) string {
	s := b.State
	picks := s.Picks
	if n := b.tickerRows(); len(picks) > n {
		picks = picks[len(picks)-n:]
	}
	if len(picks) == 0 {
		return Dim.Render("  nothing yet") + "\n"
	}
	nameW := w - 17 // "▌you 1.02 rb  " chrome plus pane padding
	if nameW < 17 {
		nameW = 17
	}
	if nameW > 24 {
		nameW = 24
	}
	var sb strings.Builder
	for i := len(picks) - 1; i >= 0; i-- {
		pk := picks[i]
		p := s.Players[pk.PlayerID]
		label := fmt.Sprintf("%d.%02d", pk.Round, ((pk.PickNo-1)%s.Teams)+1)
		pos := fmt.Sprintf("%-3s", strings.ToLower(p.Pos))
		name := trunc(strings.ToLower(p.Name), nameW)

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
	keys := []string{"space step", "a auto", "u undo", "tab data", "q quit"}
	if b.Tab == 1 {
		keys = []string{"tab board", "j/k scroll", "p filter", "q quit"}
	}
	left := "  " + Dim.Render(strings.Join(keys, "   "))

	right := Dim.Render(fmt.Sprintf("synced %s ago", since(b.Synced)))
	if b.Status != "" {
		right = Accent.Render(strings.ToLower(b.Status))
	}

	// How old the picture is, sitting next to how long ago we last synced —
	// two clocks that get mistaken for each other constantly. "synced" is the
	// poll; "adp" is the data the entire board is computed from, frozen at fetch
	// time along with every injury flag, and only meta.json knows when that was.
	//
	// Long form, then short, then nothing: at 80 columns the keybinds and the
	// sync note have already spent the row and about 15 cells are left, which is
	// the short form and not the long one. Dropping beats wrapping, the same rule
	// playerLine and the banner follow.
	if long, short := b.Fresh.note(); long != "" {
		avail := w - 3 - lipgloss.Width(left) - lipgloss.Width(right)
		for _, cand := range []string{long, short} {
			if lipgloss.Width(cand)+3 <= avail {
				right = Dim.Render(cand) + "   " + right
				break
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
