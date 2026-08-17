package ui

import (
	"fmt"
	"math"
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
	// The sidebar's ticker budget charges it, and the final clamp in View takes
	// whatever is still over off the bottom of the body.
	Reserve int

	// Fresh is how old the data underneath all of this is, handed in by the cmd
	// layer. The zero value means "no meta.json" and renders as nothing at all,
	// which is the honest answer for a board fetched before that file existed.
	Fresh Freshness
	// Now is the clock the news chip reads; zero means the wall clock. A field
	// only so tests can pin it.
	Now time.Time

	// Tab 0 is the board; tab 1 is the data table — every player, every
	// number, nothing abstracted away; tab 2 is the notes. State for them
	// lives here so both the mock and live models share it.
	Tab        int
	DataScroll int
	DataFilter string // "" = all positions

	// Views is which of the data tab's views is open and where the rankings
	// csvs it shows live. See views.go. Nothing on it reaches the engine.
	Views Views

	// Notes is tab 2: your own markdown, rendered beside a map of the draft.
	// See notes.go. Nothing on it reaches the engine.
	Notes Notes

	// Search is the `/` overlay and Selected is the player it produced, held
	// here rather than in the models for the same reason the data tab's scroll
	// is: all three entry points get the same keys, and a selection that only
	// existed in one of them would vanish on a tab flip. See search.go.
	Search   Search
	Selected string // player id, "" when nothing is selected

	// Mode is which entry point is driving, and it decides two things: which
	// keys the footer advertises, and whether x may mark a player taken.
	Mode Mode
}

// Mode distinguishes the three drivers. The board renders identically in all
// three — the numbers do not care where the picks came from — but the keys
// differ, and a footer offering a key that does nothing is worse than a footer
// with one fewer key on it.
type Mode int

const (
	ModeMock   Mode = iota // scripted draft; space steps it, a autoplays it
	ModeLive               // sleeper feed; r refreshes it, marks are not ours to make
	ModeManual             // no feed at all; x marks, u takes it back
)

// Manual reports whether hand-marking is allowed. Live mode's marks come from
// the poll, and a hand-typed one there would desync the board it is meant to
// be checking.
func (b Board) Manual() bool { return b.Mode == ModeManual }

// Layout bounds. Rows scale with width — wider terminals buy longer names and
// a roomier sidebar — up to the point where columns can't usefully consume more
// and stretching would only manufacture a gulf between the panes. Cap there and
// centre; extra height is spent showing more players, which is the thing you
// actually want more of.
const (
	MinWidth    = 80
	MaxWidth    = 128
	SidebarW    = 34 // minimum; grows with the terminal up to SidebarMaxW
	SidebarMaxW = 38
	MinTickerN  = 4
	MaxTickerN  = 10

	// marketGapPicks is how far the market must price a player ahead of the
	// position's value-best before the board surfaces the disagreement as its
	// own row. Display judgement, not engine math: the value column and the
	// adp column disagree constantly by a pick or two, and a row for every
	// wobble buries the one that matters — rashee rice priced 10 picks ahead
	// of the receiver the value curve prefers.
	marketGapPicks = engine.MarketGapPicks

	// planLegsMax is how many legs of the plan the line will ever name. The
	// rollouts play every pick I have left — sixteen of them in round one — and
	// a sixteen-leg line is a printout, not a plan. Four is where a human stops
	// treating it as advice: the next two picks are a decision, the two after
	// that are shape, and everything past them is the draft telling you what it
	// intends to do to you.
	planLegsMax = 4

	// outlookRows caps the "your team from here" block. Round one opens with
	// nine or ten unfilled starting slots and a ten-row block is the roster
	// pane again, badly, on the wrong side of the divider. Five is the shape of
	// the decision: the slots you are actually choosing between over the next
	// few picks.
	outlookRows = 5

	// planNameShare is how often a man has to land in a slot before the board
	// will say his name. Below it the row speaks in bands instead, because a
	// name is a much stronger claim than a tier and it has to be earned.
	planNameShare = 0.35

	// outlookFilled is the share of futures a slot has to end filled in before
	// it gets a filler at all. Under it the honest row is that the slot is
	// still open when the draft ends, which is a fact about the plan and not a
	// missing value.
	outlookFilled = 0.5
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
	// The primary key, computed once per frame: the plan line, the banner and
	// the pane ordering all read the same ranking, which is the point of it.
	choices := b.State.PickChoices()

	var body string
	switch b.Tab {
	case 1:
		body = b.dataPane(content)
	case 2:
		body = b.notesPane(content)
	default:
		sw := sidebarWidth(content)
		leftW := content - sw - 3
		left := b.bestAvailable(leftW, choices)
		right := b.sidebar(sw)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(leftW).Render(left),
			b.divider(maxLines(left, right)),
			lipgloss.NewStyle().Width(sw).PaddingLeft(2).Render(right),
		)
	}

	head, banner, foot := b.header(content), b.banner(content, choices), b.footer(content)
	// The search prompt and the standing selection sit between the body and the
	// footer, and they go INSIDE the assembled block rather than being appended
	// after it — the clamp below measures the block, so rows spent here come off
	// the bottom of the body instead of off the top of the header.
	over := b.overlay(content)
	assemble := func(body string) string {
		parts := []string{head, banner, body}
		if over != "" {
			parts = append(parts, over)
		}
		block := strings.Join(append(parts, foot), "\n")
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
//
// Every path through it is now gated on the RECOMMENDATION, not on raw board
// state. A banner is the one element that interrupts, so it fires only when
// what it reports should change what you do: a cliff on a position you were
// never taking is a fact, not an alert, and at 3.01 the old banner shouted
// about a wr cliff while the list under it ranked qb first — two urgencies on
// one frame with no way to tell which one the board believed.
func (b Board) banner(w int, choices []engine.PickChoice) string {
	s := b.State
	// A finished draft has no runs and no cliffs to act on. Anything urgent-
	// sounding here is stale by definition.
	if s.Done() {
		return ""
	}
	if run, ok := s.DetectRun(); ok {
		if msg := b.runBanner(run, w, choices); msg != "" {
			return msg
		}
	}
	// A cliff banners only when it concerns a position in the top two of the
	// ranking — the pick or its runner-up — because that is when it argues for
	// acting differently. Walking choices in order keeps the higher-ranked
	// cliff when both qualify.
	for i, c := range choices {
		if i >= 2 {
			break
		}
		level, tier, remaining := s.Cliff(c.Pos)
		if level != CliffLastLevel {
			continue
		}
		hold, _ := s.TierHold(c.Pos)
		return bar(w, ColCliff, "cliff",
			cliffMsg(strings.ToLower(c.Pos), tier, remaining, b.holdNote(hold), w))
	}
	return ""
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
// only when that pick is not the one being made right now.
//
// Off the clock "my next pick" is unambiguous and the extra words are noise. On
// the clock it is not — the frame is about pick 3.03 while every probability on
// it is priced to 4.10 — and the reader is one bad reading away from taking the
// hold for a verdict on the pick in front of him. The hold and the survival
// cells under it share a horizon (see engine.ActPick), so this labels the
// frame's numbers rather than marking a discrepancy between them.
func (b Board) holdNote(hold float64) string {
	at := b.State.ActPick()
	if at == b.State.NextPick() {
		return "holds " + pct(hold)
	}
	return fmt.Sprintf("holds %s to %s", pct(hold), b.pickLabel(at))
}

// runBanner is the run's banner copy, or "" when the run does not clear the
// recommendation gate. DetectRun has already established the count is a
// genuine surprise against the market's own forecast; what remains is whether
// it should interrupt YOU.
func (b Board) runBanner(run engine.Run, w int, choices []engine.PickChoice) string {
	pos := strings.ToLower(run.Pos)
	if run.TierBroke {
		alt := b.bestOtherPosition(run.Pos, choices)
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
	// A tiered run interrupts only when it is COSTING you something: the tier
	// it is eating is at risk, and the position is one of the top two things
	// the board would have you take. The calm variant ("run in progress, holds
	// 95%") is gone — a run that changes nothing is ticker material, and the
	// board's own rows already carry the hold.
	if hold, ok := b.State.TierHold(run.Pos); ok && hold >= engine.TierHoldWarn {
		return ""
	}
	top2 := false
	for i, c := range choices {
		if i >= 2 {
			break
		}
		if c.Pos == run.Pos {
			top2 = true
		}
	}
	if !top2 {
		return ""
	}
	return bar(w, ColRun, "run", fmt.Sprintf(
		"%s run in progress — %d of the last %d picks. %d left in tier %d. act now or lose it.",
		pos, run.Count, engine.RunWindow, run.TierLeft, run.Tier))
}

// bestOtherPosition names the best-ranked position besides the one on a run,
// skipping any the board is simultaneously tagging "safe to wait". Returns ""
// when everything else is safe — the banner drops the clause rather than
// pointing at a position whose own row, three lines below, says there is no
// hurry.
func (b Board) bestOtherPosition(exclude string, choices []engine.PickChoice) string {
	for _, c := range choices {
		if c.Pos == exclude || b.State.SafeToWait(c.Pos) {
			continue
		}
		return strings.ToLower(c.Pos)
	}
	return ""
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

// ---- left pane: the pick, and the frames before it ----

// bestAvailable is the left pane: the recommendation and the ranking behind
// it, in one of two tenses. On the clock it leads with a verdict — one name,
// the reasons, and the field beneath it. Off the clock the same ranking reads
// as a forecast of that decision: who will likely be there when I pick, and
// what is slipping away in the meantime. Both are ordered by the one primary
// key (engine.PickChoices), so no two elements of a frame can disagree about
// what matters most.
//
// The old layout — four position groups, five players each — is gone from both
// frames. It was a data table wearing a recommendation's accent border, and
// everything it showed is one keypress away on the data tab.
func (b Board) bestAvailable(w int, choices []engine.PickChoice) string {
	s := b.State
	var sb strings.Builder

	imDone := len(s.MyUpcomingPicks(1)) == 0
	onClock := !s.Done() && !imDone && s.PicksUntilMine() == 0

	head := "best available"
	switch {
	case onClock:
		head = "the pick — " + b.pickLabel(s.PickNo)
	case !s.Done() && !imDone:
		n := s.PicksUntilMine()
		noun := "picks"
		if n == 1 {
			noun = "pick"
		}
		head = fmt.Sprintf("before you pick at %s — %d %s", b.pickLabel(s.NextPick()), n, noun)
	}
	sb.WriteString(sectionHead(head, w-2) + "\n")

	if len(choices) == 0 {
		sb.WriteString("\n  " + Dim.Render("nothing left to recommend") + "\n")
		return sb.String()
	}

	if onClock {
		sb.WriteString("\n" + b.verdictBlock(w, choices[0]))
		sb.WriteString("\n")
		sb.WriteString(b.planLine(w))
		sb.WriteString(b.endgameLine(w))
		if rows := b.choiceRows(w, choices, true); rows != "" {
			sb.WriteString("\n  " + Dim.Render(fitCaption(w-4,
				"if not him — ranked by "+b.rankedBy("what the pick is worth"), "if not him")) + "\n")
			sb.WriteString(rows)
		}
		sb.WriteString(b.teamAhead(w, choices[0]))
		sb.WriteString(b.fieldBlocks(w, rowCount(sb.String())))
		return sb.String()
	}

	sb.WriteString(b.planLine(w))
	sb.WriteString(b.endgameLine(w))
	caption := "what's left, best three by value — ranked by " + b.rankedBy("what taking each is worth")
	if imDone || s.Done() {
		caption = "still on the board"
	}
	sb.WriteString("\n  " + Dim.Render(fitCaption(w-4, caption, "what's left")) + "\n")
	sb.WriteString(b.choiceRows(w, choices, false))
	sb.WriteString(b.teamAhead(w, choices[0]))
	sb.WriteString(b.fieldBlocks(w, rowCount(sb.String())))
	return sb.String()
}

// rankedBy names the key the rows are ordered on, because milestone 8 made that
// two different claims and a caption that named the wrong one would be the
// frame lying about its own arithmetic. Under the shipped pair score the rows
// are ranked by what the PICK is worth; under -scorer roster they are ranked by
// the finished TEAM each one leads to, which is a different sentence and a
// different promise.
//
// It asks the SCORER and not the horizon, unlike the two blocks below it: at the
// endgame a two-pick window happens to be the whole horizon, so the futures
// finish a roster and the blocks reading them are honest — while the ranking
// they sit under is still the pair score.
func (b Board) rankedBy(pair string) string {
	if b.State.ScoresTheTeam() {
		return "the team each leads to"
	}
	return pair
}

// fitCaption is the long-form-then-short rule every caption follows: dropping
// beats truncating, and both beat wrapping.
func fitCaption(w int, long, short string) string {
	if lipgloss.Width(long) <= w {
		return long
	}
	return short
}

// verdictBlock is the recommendation itself, on the one frame that spends a
// pick: the top choice's man, and the two or three facts that justify him. It
// exists because the old on-clock frame offered a four-row table of costs in
// board-value units — a number nobody feels — and left the aggregation to the
// reader, at the exact moment the reader has none to spare.
//
// The clauses speak in the units a drafter actually feels: picks against his
// market price, the odds he is gone before I act again, the tier he is the
// last of, the slot he fills. The value units justify the ORDER; the words
// justify the pick.
func (b Board) verdictBlock(w int, top engine.PickChoice) string {
	p := top.Best
	style := Pos(p.Pos, false)
	edge := style.Render("▏") + " "

	var sb strings.Builder
	sb.WriteString(edge + style.Bold(true).Render(trunc(strings.ToLower(p.Name), w-12)) +
		"  " + style.Render(strings.ToLower(p.Pos)) + " " + Dim.Render(p.Team) + "\n")

	// Line two: his price, and whether he is still there when I next act. Both
	// clauses matter, so the PRICE gives ground first — its long form restates
	// the adp, which the short form drops — and only then does the line fall
	// back to dropping clauses. The room note rides on the price when the warp
	// moved him meaningfully: "at his price" is a different claim in a room
	// that takes him five earlier.
	long, priceStyle := b.priceClause(p, true)
	short, _ := b.priceClause(p, false)
	if p.ADPEff > 0 && math.Abs(p.ADPEff-p.ADP) >= 3 {
		long += fmt.Sprintf(" · room ~%.0f", p.ADPEff)
		short += fmt.Sprintf(" · room ~%.0f", p.ADPEff)
	}
	gone, goneStyle := b.goneClause(p)
	line2 := []styledClause{{long, priceStyle}}
	if gone != "" {
		line2 = append(line2, styledClause{gone, goneStyle})
		if lipgloss.Width(long)+3+lipgloss.Width(gone) > w-4 {
			line2[0].text = short
		}
	}
	sb.WriteString(edge + joinClauses(line2, w-4) + "\n")

	// Line three: what the position looks like behind him. Dropped whole when
	// there is nothing to say.
	var l3 []styledClause
	if long, short, st := b.stateNote(p.Pos); long != "untiered" {
		l3 = append(l3, styledClause{long, st}, styledClause{short, st})
		l3 = l3[:1] // long form; joinClauses handles the drop, not a short swap
	}
	// The file's opinion of the verdict's own man, when it has one. "avoid" is
	// the dissent case — the value curve crowning a man your rankings warned you
	// off is exactly the market-dissent situation with a different second voice,
	// so it wears the same accent. Milder opinions confirm rather than dissent
	// and stay dim. It sits directly after the tier state because clauses drop
	// from the right: on a narrow pane the file's dissent outlives the slot
	// note, which the roster pane is already showing.
	if p.Sentiment != "" {
		st := Dim
		if p.Sentiment == "avoid" {
			st = Accent
		}
		l3 = append(l3, styledClause{"file says " + p.Sentiment, st})
	}
	if cl := b.slotClause(p.Pos); cl != "" {
		l3 = append(l3, styledClause{cl, Dim})
	}
	// The other opinion, when the two curves disagree about who the best man at
	// this position is. Under sim the futures have already RANKED the two, so
	// the clause names whichever one lost — and when the market's man won the
	// verdict, saying so is the more useful half of the disagreement.
	if top.Alt.ID != "" {
		who := "market prefers "
		if !top.AltIsMarket {
			who = "value curve prefers "
		}
		l3 = append(l3, styledClause{who + lastName(strings.ToLower(top.Alt.Name)), Accent})
	}
	if len(l3) > 0 {
		sb.WriteString(edge + joinClauses(l3, w-4) + "\n")
	}
	if cq := b.consequence(); cq != "" {
		sb.WriteString(edge + Dim.Render(trunc(cq, w-4)) + "\n")
	}
	return sb.String()
}

// consequence is what the runner-up costs, named in a player: "taking rb
// instead costs you mcbride".
//
// It is the one clause on the frame that only exists because of common random
// numbers. The top choice and the runner-up were scored on the SAME futures, so
// their ending rosters differ by what the choice caused and nothing else —
// diff them and the difference has a name in it. Under independent sampling the
// same diff would be mostly noise, and the sentence would be a lie with a name
// in it, which is worse than no sentence.
//
// Three ways it says nothing, and all three drop the clause whole rather than
// hedging: the plan was priced by the v1 formula (no futures to diff), nothing
// recurs often enough to name, and the man who does recur is too rarely the
// difference to be worth a row. A consequence line that says nothing must not
// print — it is the widest single clause on the block and it earns its row by
// naming somebody.
func (b Board) consequence() string {
	if choices := b.State.PickChoices(); len(choices) == 0 || !b.fullHorizon(choices[0]) {
		return ""
	}
	cq, ok := b.State.Consequence()
	if !ok || cq.Share < planNameShare {
		return ""
	}
	p := b.State.Players[cq.PlayerID]
	if p.Name == "" {
		return ""
	}
	return fmt.Sprintf("taking %s instead costs you %s",
		strings.ToLower(cq.Runner), lastName(strings.ToLower(p.Name)))
}

// styledClause is one clause of a sentence assembled to fit a width.
type styledClause struct {
	text string
	st   lipgloss.Style
}

// joinClauses renders clauses separated by dim middle dots, dropping whole
// clauses from the RIGHT until the line fits — the same rule the old group
// header followed, for the same reason: a wrapped clause reads as a rendering
// fault, and the head of the sentence carries the information. The last clause
// standing truncates rather than wraps, for the same reason again.
func joinClauses(clauses []styledClause, w int) string {
	for len(clauses) > 1 {
		total := 0
		for i, c := range clauses {
			total += lipgloss.Width(c.text)
			if i > 0 {
				total += 3
			}
		}
		if total <= w {
			break
		}
		clauses = clauses[:len(clauses)-1]
	}
	if len(clauses) == 1 && lipgloss.Width(clauses[0].text) > w && w > 1 {
		clauses[0].text = trunc(clauses[0].text, w)
	}
	parts := make([]string, len(clauses))
	for i, c := range clauses {
		parts[i] = c.st.Render(c.text)
	}
	return strings.Join(parts, Dim.Render(" · "))
}

// priceClause is a player's cost in the one currency every drafter already
// thinks in: picks against his price. long=false gives the compact chip the
// rows use.
//
//	"fell 7"    the draft ran past his price and he is still here — a discount,
//	            amber, the same state the Falling flag marks
//	"11 early"  taking him now pays 11 picks over his price
//	"at price"  he would go about here
//
// The price is the room-warped effective one when a curve is loaded, because
// it has to be: Falling reads the warped price, and a chip doing its
// arithmetic on raw adp printed "fell -1" beside an amber flag — the flag and
// the number disagreeing about the same fact. The long form still restates the
// raw market adp, which is the number the reader can check anywhere else.
func (b Board) priceClause(p engine.Player, long bool) (string, lipgloss.Style) {
	if p.ADP <= 0 {
		return "unpriced", Dim
	}
	price := p.ADP
	if p.ADPEff > 0 {
		price = p.ADPEff
	}
	diff := float64(b.State.PickNo) - price
	switch {
	case b.State.Falling(p):
		if long {
			return fmt.Sprintf("fell %.0f past his price — adp %.1f", diff, p.ADP), Run
		}
		return fmt.Sprintf("fell %.0f", diff), Run
	case diff <= -3:
		if long {
			return fmt.Sprintf("%.0f before his price — adp %.1f", -diff, p.ADP), Dim
		}
		return fmt.Sprintf("%.0f early", -diff), Dim
	default:
		if long {
			return fmt.Sprintf("at his price — adp %.1f", p.ADP), Dim
		}
		return "at price", Dim
	}
}

// goneClause is the verdict's survival clause: pass on him now, is he there
// when I come back. "" on my last pick, where there is no coming back and the
// question has no answer.
func (b Board) goneClause(p engine.Player) (string, lipgloss.Style) {
	s := b.State
	at := s.ActPick()
	if at <= s.PickNo {
		return "", Dim
	}
	sv := s.PSurviveTilted(p)
	if sv < engine.SurviveThreshold {
		return fmt.Sprintf("gone by %s — %s", b.pickLabel(at), pct(sv)), b.survStyle(p)
	}
	return fmt.Sprintf("keeps to %s — %s", b.pickLabel(at), pct(sv)), b.survStyle(p)
}

// slotClause names what taking the position buys my lineup, because "need" as
// a bare weight never reached the screen and the difference between filling a
// second starter slot and burning a flex is a real argument.
func (b Board) slotClause(pos string) string {
	open := 0
	for _, slot := range b.State.UnfilledStarters(b.State.MySlot) {
		if slot == pos {
			open++
		}
	}
	switch {
	case open >= 2:
		return fmt.Sprintf("both %s slots open", strings.ToLower(pos))
	case open == 1:
		return fmt.Sprintf("fills your %s slot", strings.ToLower(pos))
	case b.State.Need(pos) >= engine.NeedFlex:
		return "flex only"
	default:
		return "bench depth"
	}
}

// choiceRows renders the ranking itself, one row per position: the man, his
// survival to the pick I next act at, and the clauses that say what taking or
// leaving him means. onClock also skips the top choice, which the verdict
// block has already spent three lines on.
//
// Where the market disagrees with the value curve about a position's best man
// (marketPick), the market's choice gets a row of his own directly under the
// position's — the disagreement rendered as two rows the reader can compare,
// rather than resolved silently in either direction.
func (b Board) choiceRows(w int, choices []engine.PickChoice, onClock bool) string {
	var sb strings.Builder
	for i, c := range choices {
		if onClock && i == 0 {
			// The verdict's own position can still hide a market disagreement,
			// and the one row worth keeping from it is exactly that.
			if row := b.marketRow(w, c); row != "" {
				sb.WriteString(row)
			}
			continue
		}
		if onClock {
			sb.WriteString(b.choiceRow(w, c, false, true))
		} else {
			sb.WriteString(b.depthRow(w, c, i == 0))
		}
		if row := b.marketRow(w, c); row != "" {
			sb.WriteString(row)
		}
	}
	return sb.String()
}

// depthN is how many men an off-clock row names per position. Three is the
// question being asked between picks — "if the first is gone, who is the
// settle-for, and is he safe" — and the fourth is the data tab's job.
const depthN = 3

// depthRow is one row of the off-clock ranking: the position, then its best
// three by value with each man's odds of reaching my pick, then the tier's
// state. It replaced a row that named only the position's current best man,
// which off the clock is usually a man who will NOT be there — a 9% caleb
// williams under the caption "who'll likely be there" was the frame lying
// about its own label. The ranking still orders the rows (the accent still
// marks the top choice); the content is the depth, because the depth is what
// you can act on when your turn comes.
//
// Priority under width pressure, dropping from the right: the long tier note
// goes first, then the third man, then the second, then the safe tag — the
// first man and the short note survive everything.
func (b Board) depthRow(w int, c engine.PickChoice, accent bool) string {
	s := b.State
	style := Pos(c.Pos, false)
	edge := "  "
	if accent {
		edge = style.Render("▏") + " "
	}
	tag := style.Bold(true).Render(fmt.Sprintf("%-3s", strings.ToLower(c.Pos)))
	prefix := edge + tag + " "

	var men []string
	for i, p := range s.Available(c.Pos) {
		if i >= depthN {
			break
		}
		cs := b.alarmChips(p)
		if p.Sentiment == "avoid" {
			cs = append(cs, chip{"avoid", ChipNote})
		}
		chips, _, _ := renderChips(cs, 20)
		men = append(men, style.Render(strings.ToLower(p.Name))+chips+" "+
			b.survStyle(p).Render(pct(s.PSurviveTilted(p))))
	}
	if len(men) == 0 {
		return ""
	}

	long, short, noteStyle := b.stateNote(c.Pos)
	var extras []styledClause
	if s.SafeToWait(c.Pos) {
		extras = append(extras, styledClause{"safe", Wait})
	}
	if best := s.Available(c.Pos)[0]; s.Falling(best) {
		price, priceStyle := b.priceClause(best, false)
		extras = append(extras, styledClause{price, priceStyle})
	}
	// Priority under width, in order: the first man; the short note when it is
	// an alarm ("last one in tier 3" outranks everybody's second man); the
	// second man; the short note with the safe/falling tags; the third man;
	// the long note. Nothing is ever truncated — a clause that does not fit
	// whole is dropped whole, same law as every other row.
	sepS := Dim.Render(" · ")
	sepW := lipgloss.Width(sepS)
	alarm := false
	if level, _, _ := s.Cliff(c.Pos); level != engine.CliffNone {
		alarm = true
	}
	tailNeed := lipgloss.Width(short) + 3*len(extras) + sumWidth(extras)
	line := prefix + men[0]
	fits := func(m string, keep int) bool {
		return lipgloss.Width(line)+sepW+lipgloss.Width(m)+keep <= w
	}
	reserve := 0
	if alarm {
		reserve = 3 + lipgloss.Width(short)
	}
	if len(men) > 1 && fits(men[1], reserve) {
		line += sepS + men[1]
	}
	if len(men) > 2 {
		rem := w - lipgloss.Width(line) - 3
		switch {
		case fits(men[2], 3+tailNeed):
			line += sepS + men[2] // the tail still fits after him
		case rem < tailNeed && !alarm && fits(men[2], 0):
			line += sepS + men[2] // the tail was not fitting anyway
		}
	}
	rem := w - lipgloss.Width(line) - 3
	var clauses []styledClause
	switch {
	case rem >= lipgloss.Width(long)+3*len(extras)+sumWidth(extras):
		clauses = append(clauses, styledClause{long, noteStyle})
	case rem >= lipgloss.Width(short):
		clauses = append(clauses, styledClause{short, noteStyle})
	}
	clauses = append(clauses, extras...)
	// Drop whole clauses from the right until the tail fits; never truncate.
	for len(clauses) > 0 && sumWidth(clauses)+3*(len(clauses)-1) > rem {
		clauses = clauses[:len(clauses)-1]
	}
	if len(clauses) > 0 {
		var parts []string
		for _, c := range clauses {
			parts = append(parts, c.st.Render(c.text))
		}
		line += "   " + strings.Join(parts, Dim.Render(" · "))
	}
	return line + "\n"
}

func sumWidth(cs []styledClause) int {
	n := 0
	for _, c := range cs {
		n += lipgloss.Width(c.text)
	}
	return n
}

// choiceRow is one row of the ranking. Column budget: edge(2) tag(4)
// name(nameW) surv(5), then clauses that drop from the right — which makes the
// ORDER a priority list, and the two tenses hold different priorities.
//
// On the clock the price chip leads: the question is whether to buy, and picks
// against market price is the currency of that argument. Off the clock there
// is no buying, and "9 early" would even be measured from a pick that isn't
// mine — so the tier's state leads, the safe tag rides next (the claim a
// narrow row must not lose), and a falling chip marks the discount forming.
func (b Board) choiceRow(w int, c engine.PickChoice, accent, onClock bool) string {
	s := b.State
	p := c.Best
	style := Pos(c.Pos, false)
	edge := "  "
	if accent {
		// One accent, not five: only the top choice wears its position colour.
		edge = style.Render("▏") + " "
	}
	nameW := rowNameWidth(w)
	tag := style.Bold(true).Render(fmt.Sprintf("%-3s", strings.ToLower(c.Pos)))
	name := b.nameCell(p, style, nameW)
	surv := b.survStyle(p).Render(fmt.Sprintf("%3.0f%%", 100*s.PSurviveTilted(p)))

	tail := w - (2 + 4 + nameW + 5 + 1)
	price, priceStyle := b.priceClause(p, false)
	long, short, noteStyle := b.stateNote(c.Pos)

	// Assemble with the SHORT tier note first, so the claims to its right — the
	// safe tag, a falling chip — get their space before the note's hold number
	// does; then upgrade the note to its long form only if the line still fits.
	// The other order starved "safe" out of every narrow row.
	var clauses []styledClause
	noteAt := -1
	if onClock {
		clauses = append(clauses, styledClause{price, priceStyle})
		// v2's spite chip: the seat after me needs this exact man and he is the
		// last of his band. A chip, never the verdict — engine.Deny cannot fire
		// on a player I need at starter weight, and the verdict's own man never
		// carries it (his row isn't in this list on the clock; if the value pick
		// and the deny coincide, "take him" is already the advice). It rides
		// BEFORE the tier note: clauses drop from the right, the note is on
		// every row, and a once-a-draft chip must not be the first thing a
		// narrow pane sheds — especially as the note it displaces ("1 in tier
		// 2") is half-implied by the chip anyway.
		if dp, them, ok := s.Deny(); ok && dp.ID == p.ID {
			clauses = append(clauses, styledClause{fmt.Sprintf("deny team %d", them), ChipNote})
		}
		noteAt = len(clauses)
		clauses = append(clauses, styledClause{short, noteStyle})
	} else {
		noteAt = 0
		clauses = append(clauses, styledClause{short, noteStyle})
		if s.SafeToWait(c.Pos) {
			clauses = append(clauses, styledClause{"safe", Wait})
		}
		if s.Falling(p) {
			clauses = append(clauses, styledClause{price, priceStyle})
		}
	}
	if later, ok := s.BestLater(c.Pos); ok && later.ID != p.ID {
		clauses = append(clauses, styledClause{"→ " + lastName(strings.ToLower(later.Name)), Dim})
	}
	total := 0
	for i, c := range clauses {
		total += lipgloss.Width(c.text)
		if i > 0 {
			total += 3
		}
	}
	if total+lipgloss.Width(long)-lipgloss.Width(short) <= tail {
		clauses[noteAt].text = long
	}
	return fmt.Sprintf("%s%s %s %s %s\n", edge, tag, name, surv,
		joinClauses(clauses, tail))
}

// marketRow is the losing half of a value-versus-market disagreement, as a row
// of its own directly under the position's, or "".
//
// Since milestone 8 both men are scored candidates under sim and the winner
// takes the position's row, so this can be either of them — the market's pick
// when the value curve won, the value curve's when the market's man did. The
// label says which, because "market's pick" under a row the market also picked
// would be the frame agreeing with itself in two voices.
func (b Board) marketRow(w int, c engine.PickChoice) string {
	s := b.State
	mb := c.Alt
	if mb.ID == "" {
		return ""
	}
	label := "market's pick"
	if !c.AltIsMarket {
		label = "value's pick"
	}
	style := Pos(c.Pos, false)
	nameW := rowNameWidth(w)
	tag := Dim.Render(fmt.Sprintf("%-3s", strings.ToLower(c.Pos)))
	name := b.nameCell(mb, style, nameW)
	surv := b.survStyle(mb).Render(fmt.Sprintf("%3.0f%%", 100*s.PSurviveTilted(mb)))
	// "market's pick", not "market's wr pick" — the row's own tag names the
	// position, and the shorter label is what lets the price chip survive at
	// narrow widths, where a falling market pick is the whole story.
	price, priceStyle := b.priceClause(mb, false)
	clauses := []styledClause{
		{label, Accent},
		{price, priceStyle},
	}
	return fmt.Sprintf("  %s %s %s %s\n", tag, name, surv,
		joinClauses(clauses, w-(2+4+nameW+5+1)))
}

// rowNameWidth is the shared name-column budget for ranking rows, so the
// verdict's field and a market dissent line up as one table. Capped low enough
// that the clause tail keeps a price chip AND a tier note at 92 columns —
// names give ground before claims do.
func rowNameWidth(w int) int {
	nameW := w - 36
	if nameW < 16 {
		nameW = 16 // nameCell's chip budget needs the old playerLine floor
	}
	if nameW > 20 {
		nameW = 20
	}
	return nameW
}

// planCopy is the lookahead in words: which position to take with my next pick,
// and which to expect at each pick after it. Every leg is named by its pick
// number and never by "now" — the plan is computed as-if standing at my next
// pick but drawn on every frame, and on eleven frames out of twelve that pick
// belongs to somebody else.
//
// Milestone 8 grew it from a pair into a SKELETON, because the score behind it
// grew from a pair into a finished roster: the rollouts now play every pick I
// have left, so the plan they summarise really does run to the end. What is
// rendered is a prefix of it — a plan longer than four legs is a printout, and
// the legs past the second are context rather than a claim.
//
// The pair's score is deliberately NOT rendered, and milestone 8 did not change
// that even though the number is now a whole team's value. It ranks choices; it
// does not forecast what you will get, because its first leg is priced at the
// value of today's best available even when my next pick is eighteen picks away
// and he will plainly be gone by then. Printed as "(e 12318)" it read as an
// expectation while sitting three rows above the same man's survival column
// reading 0% — one line of the board contradicting the evidence directly under
// it, on 27 of 166 plan frames of the scripted mock.
//
// "" when there is no second pick to plan for.
//
// Two legs and no more: its one caller is the data tab's urgency strip, which is
// width-starved by design and budgets for the string it is handed. A skeleton
// there would be silently dropped WHOLE by planTail rather than shortened, which
// is what happened before this cap existed — the strip simply stopped carrying a
// plan under the roster scorer. Found by review.
func (b Board) planCopy() string { return b.planSkeleton(1<<30, 2) }

// planSkeleton is that copy inside a width budget: leg one and leg two always,
// then as many later legs as the budget has room for.
//
// The drop order deviates from the milestone's own sketch, deliberately and in
// one direction. The sketch had every leg outrank the odds clause; at a
// full-horizon skeleton that rule means the legs always fill the line and the
// odds never render at any terminal width, which would silently delete a claim
// milestone 7 shipped. So the rule is: leg two outranks the odds, and the odds
// outrank every leg after it. Leg two is the actionable claim and the odds are
// about leg two — a third position four picks out is the least load-bearing
// thing on the row.
func (b Board) planSkeleton(budget, maxLegs int) string {
	plan, ok := b.State.BestPlan()
	if !ok {
		return ""
	}
	first := fmt.Sprintf("plan  %s %s", strings.ToLower(plan.First), b.pickLabel(plan.FirstPick))
	if plan.Second == "" {
		// The rollouts found no legal second leg at all: the board past my next
		// pick holds nothing this lineup is allowed to want. Rare — it needs an
		// endgame where the only open slots are k and def while our own
		// suppression still forbids them — and naming a blank position is worse
		// than naming none.
		return first
	}
	line := first + fmt.Sprintf(" → %s %s", strings.ToLower(plan.Second), b.pickLabel(plan.SecondPick))
	// Legs past the second exist only under the conditioned rollouts; the v1
	// formula plans a pair and must not be dressed up as planning more.
	for i := 2; i < len(plan.Legs) && i < maxLegs; i++ {
		leg := plan.Legs[i]
		if leg.Pos == "" {
			break
		}
		next := line + fmt.Sprintf(" → %s %s", strings.ToLower(leg.Pos), b.pickLabel(leg.Pick))
		if lipgloss.Width(next) > budget {
			break
		}
		line = next
	}
	return line
}

// planOdds is the conditioned lookahead's outcome claim (milestone 7): how
// often leg two actually lands the band it is planning on, across the futures
// the rollouts played. "lands tier-2 rb 78%."
//
// It is an OUTCOME claim, not a loss forecast — which is why it earns a place
// on the frame where CostOfPassing did not. "you stand to lose 990 of te value"
// is a number in board units about a thing that has not happened; "the tier-2
// backs are there when you come back, 78% of the time" is the plan's own
// premise, stated as odds a drafter can disagree with.
//
// The claim is "that tier OR BETTER", because landing a better one is strictly
// good news and a clause that excluded it would understate the plan.
//
// "" under the v1 formula, which has no futures to count and must not be made
// to look like it does — and "" on an untiered second leg where the honest
// short form is the position alone.
func planOdds(plan engine.Plan) string {
	if plan.SecondOdds <= 0 || plan.Second == "" {
		return ""
	}
	pos := strings.ToLower(plan.Second)
	if plan.SecondTier == 0 {
		return fmt.Sprintf("lands a %s %.0f%%", pos, 100*plan.SecondOdds)
	}
	return fmt.Sprintf("lands tier-%d %s %.0f%%", plan.SecondTier, pos, 100*plan.SecondOdds)
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
// The plan clause itself has no short form to fall back to because it never
// needs one: its widest possible copy is 33 cells ("plan  def at 15.12 → def at
// 15.12") and the tightest this pane ever gets is 43, at 80 columns with the
// widest sidebar. The odds clause riding behind it does not fit there, and
// drops whole — joinClauses' rule, and the right priority: the plan is the
// recommendation, the odds are how much to trust it.
func (b Board) planLine(w int) string {
	plan, ok := b.State.BestPlan()
	if !ok {
		return ""
	}
	odds := planOdds(plan)
	// The skeleton's own budget is what is left after the odds clause has been
	// paid for, which is what makes the later legs give ground to it rather
	// than the other way round. joinClauses still gets the true width, so when
	// even leg two plus the odds will not fit, the odds drop as they always did.
	budget := w - 2
	if odds != "" {
		budget -= 3 + lipgloss.Width(odds)
	}
	line := b.planSkeleton(budget, planLegsMax)
	if line == "" {
		return ""
	}
	clauses := []styledClause{{line, Dim}}
	if odds != "" {
		clauses = append(clauses, styledClause{odds, Dim})
	}
	return "  " + joinClauses(clauses, w-2) + "\n"
}

// teamAhead is the "your team from here" block: one row per starting slot that
// is open today, read off the TOP CHOICE'S OWN FUTURES.
//
// It exists because milestone 8's score changed units. The board used to rank on
// vor × need — a number in board units about a pick — and the only honest thing
// to show was the ranking. It now ranks on the mean value of a FINISHED ROSTER,
// which means the engine is holding five hundred versions of my team and has
// been throwing all of them away except the mean. This block is the display in
// the score's own native units, not decoration: what the recommendation actually
// expects your team to become.
//
// One brain, deliberately: every row comes out of choices[0]'s rollouts, the
// same futures Score is the mean of, so the block cannot contradict the verdict
// standing above it. A version of this built off a second sample would be two
// models on one frame, which is the bug survivalAt exists to prevent.
//
// It sits at the BOTTOM of the pane and that is its height policy: the frame's
// clamp drops rows from the bottom of the body, so under height pressure this
// block gives ground row by row and then disappears entirely before a single
// field row does. Nothing extra had to be written for that.
func (b Board) teamAhead(w int, top engine.PickChoice) string {
	if len(top.Outlook) == 0 || !b.fullHorizon(top) {
		return ""
	}
	nameW := rowNameWidth(w) - 1
	var rows []string
	for _, o := range top.Outlook {
		if len(rows) >= outlookRows {
			break
		}
		// A slot whose modal filler is a man I ALREADY have is not news: it
		// closed by reassignment, not by a pick. Which of two same-position
		// slots is "the open one" is arbitrary anyway — adding a second back
		// puts the better of the two in the first rb slot and the other in the
		// second — so the row would name a player the roster pane is already
		// showing, two columns to the right, and say nothing about the future.
		// The man who actually arrives gets his own row on whichever slot he
		// lands in.
		if o.Pick == 0 && o.PlayerID != "" {
			continue
		}
		rows = append(rows, b.outlookRow(w, nameW, o))
	}
	if len(rows) == 0 {
		return ""
	}
	return "\n  " + Dim.Render(fitCaption(w-4,
		"your team from here — where the top choice's futures end up", "your team from here")) +
		"\n" + strings.Join(rows, "")
}

// outlookRow is one open starting slot as the futures fill it.
//
// The naming rule is the milestone's: name the man when he recurs in at least
// planNameShare of futures, and speak in tiers below that. A name is a much
// stronger claim than a band and it has to be earned — "warren at te" when he
// lands there one future in six is a sentence the board cannot support, while
// "a tier-3 te" is true of the same distribution.
//
// The percentage always matches the claim beside it: a named man carries HIS
// share, a band carries the inclusive tier odds. Printing the tier odds next to
// a name would be quoting a number about a group as though it were about him.
func (b Board) outlookRow(w, nameW int, o engine.SlotOutlook) string {
	s := b.State
	slot := Dim.Render(fmt.Sprintf("%-4s", strings.ToLower(o.Slot)))
	tail := w - (2 + 5 + nameW + 1 + 4 + 1)

	if o.PlayerID == "" || o.Filled < outlookFilled {
		// The slot ends empty in most futures, which is the one thing this block
		// says that the roster pane opposite cannot: that pane shows a hole, this
		// one says the hole is still there at the end of the draft.
		return fmt.Sprintf("  %s %s\n", slot,
			Cliff.Render(trunc("still open at the end", nameW+11)))
	}

	p := s.Players[o.PlayerID]
	style := Pos(p.Pos, s.Suppressed(p.Pos))
	var cell string
	pct := o.Odds
	named := o.Share >= planNameShare
	if named {
		cell = b.nameCell(p, style, nameW)
		pct = o.Share
	} else {
		label := "a " + strings.ToLower(p.Pos)
		if o.Tier > 0 {
			label = fmt.Sprintf("a tier-%d %s", o.Tier, strings.ToLower(p.Pos))
		}
		cell = style.Render(fmt.Sprintf("%-*s", nameW, trunc(label, nameW)))
	}

	var clauses []styledClause
	if o.Pick > 0 {
		clauses = append(clauses, styledClause{"at " + b.pickLabel(o.Pick), Dim})
	}
	// The second mode, on named rows only. Under a band the runner-up is
	// already inside the claim — "a tier-3 te or better" covers him — so naming
	// him there would be arguing with the row above it.
	if named && o.ElseID != "" {
		clauses = append(clauses,
			styledClause{"else " + lastName(strings.ToLower(s.Players[o.ElseID].Name)), Dim})
	}
	return fmt.Sprintf("  %s %s %s %s\n", slot, cell,
		Dim.Render(fmt.Sprintf("%3.0f%%", 100*pct)), joinClauses(clauses, tail))
}

// fullHorizon reports whether the plan behind a choice was played to my LAST
// pick, which is the milestone-8 objective's window and nobody else's.
//
// Two blocks depend on it and neither may render without it. "Your team from
// here" reads a finished lineup out of the futures, and a two-pick window
// finishes nothing — every slot past the second leg would read "still open at
// the end", which is a fact about the window and not about the draft. The
// consequence clause is the same story: diffing two rosters that are two picks
// long names whoever leg two happened to differ on, which is a coin toss with a
// player's name attached.
//
// So under the shipped pair score both blocks are simply absent, and the frame
// is the milestone-7 frame. Late in a draft, where my remaining picks ARE two,
// the pair window is the full horizon and both blocks come back — correctly,
// because there the two questions have the same answer.
func (b Board) fullHorizon(top engine.PickChoice) bool {
	return len(top.Legs) >= b.State.MyPicksLeft()
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

// stateNote is a position's tier clause, in a long form and a short one.
//
// It carries both the remaining count and the probability at least one of those
// players is still there the next time I can act (on the clock, that means after
// this pick), because the two answer different questions and the second is the
// one the cliff levels now read. A count cannot
// tell three players the room is about to eat from one player nobody wants, and
// those are opposite situations; showing the count alone would leave the reader
// unable to see why an eight-man tier went red.
func (b Board) stateNote(pos string) (long, short string, style lipgloss.Style) {
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
		short = fmt.Sprintf("%d in tier %d — ending", remaining, tier)
		return short + " · " + note, short, Run
	default:
		short = fmt.Sprintf("%d in tier %d", remaining, tier)
		return short + " · " + note, short, Dim
	}
}

// survStyle bands a survival probability, and BOTH TABS read it so they cannot
// drift apart about the same player.
//
// The board tab used to render this column dim unless the man was falling. That
// is backwards for the frame it spends most of its life on: off the clock,
// twenty minutes to read, "will he still be here when I pick" is the entire
// question, and the answer to it was the one number on the row with no colour.
// You had to parse eleven of them where you should be able to glance at one.
func (b Board) survStyle(p engine.Player) lipgloss.Style {
	surv := b.State.PSurviveTilted(p)
	switch {
	case b.State.Falling(p):
		return Run
	case surv >= engine.SurviveThreshold:
		return Wait
	case surv < survGoneBand:
		return Cliff
	default:
		return Dim
	}
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
	insight := b.insight(w)
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
func (b Board) insight(w int) string {
	s := b.State
	var sb strings.Builder

	if need := s.UnfilledStarters(s.MySlot); len(need) > 0 {
		// A two-flex lineup's full list — "qb wr wr te flex flex k def" — is a
		// cell wider than the sidebar, and a wrapped need line puts a lone "def"
		// on its own row looking like a rendering fault. "fx" is the fallback,
		// not the default: flex reads better and usually fits.
		flexLabel := "flex"
		width := func(label string) int {
			n := 8 // "  need  " gutter
			for i, slot := range need {
				if i > 0 {
					n++
				}
				if isFlex := slot == "FLEX" || slot == "SUPERFLEX"; isFlex {
					n += len(label)
				} else {
					n += len(slot)
				}
			}
			return n
		}
		if width(flexLabel) > w-2 {
			flexLabel = "fx"
		}
		parts := make([]string, len(need))
		labels := make([]string, len(need))
		for i, n := range need {
			// Colour each slot by position, and keep K/DEF faint while the engine
			// is suppressing them. Otherwise "need k def" in round 9 reads as an
			// instruction, contradicting the board that just hid every kicker.
			if n == "FLEX" || n == "SUPERFLEX" {
				labels[i] = flexLabel
				parts[i] = FG.Render(flexLabel)
				continue
			}
			labels[i] = strings.ToLower(n)
			parts[i] = Pos(n, s.Need(n) == 0).Render(labels[i])
		}
		// "fx" is not always enough: ten unfilled slots overrun a 34-cell sidebar
		// even abbreviated, and the line wrapped, which put a lone "def" on its
		// own row looking exactly like a rendering fault. Show what fits and
		// count the rest — the list shrinks with every pick you make, so this is
		// a first-few-picks state and never an endgame one, which is also why the
		// slots that drop are the ones furthest down the lineup.
		avail, used, shown := w-10, 0, 0
		for i := range parts {
			step := len(labels[i])
			if shown > 0 {
				step++
			}
			tail := 0
			if i < len(parts)-1 {
				tail = len(fmt.Sprintf(" +%d", len(parts)-i))
			}
			if shown > 0 && used+step+tail > avail {
				break
			}
			used += step
			shown++
		}
		line := strings.Join(parts[:shown], " ")
		if shown < len(parts) {
			line += Dim.Render(fmt.Sprintf(" +%d", len(parts)-shown))
		}
		sb.WriteString(fmt.Sprintf("\n  %s %s\n", Dim.Render("need "), line))
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
		picks := s.MyUpcomingPicks(3)
		head := picks
		if len(head) > 2 {
			head = head[:2]
		}
		if len(head) > 0 {
			parts := make([]string, len(head))
			for i, p := range head {
				parts[i] = b.pickLabel(p)
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n",
				Dim.Render("up   "),
				Accent.Render(strings.Join(parts, ", then "))))
		}
		// The wait AFTER those two, which is the number that decides how much of
		// the board you can afford to leave on it. On the wheel it is the whole
		// story and the two-pick line actively hides it: slot 1 picks at 24 and
		// 25 — back to back, nothing intervening — and then not again until 48.
		// "up 2.12, then 3.01" reads as comfort while the real fact is that
		// everything you defer past 3.01 is gone for twenty-two picks. Mid-round
		// seats have an even rhythm and this line just confirms it, which is
		// cheap; the seats where it matters are the ones where it is severe.
		if len(picks) > 2 {
			gap := picks[2] - picks[1] - 1
			note := fmt.Sprintf("%d picks to %s", gap, b.pickLabel(picks[2]))
			if gap == 0 {
				// The turn again, one pick further out. "0 picks to 3.01" is
				// arithmetically fine and reads like a bug.
				note = b.pickLabel(picks[2]) + " back to back"
			}
			sb.WriteString(fmt.Sprintf("  %s %s\n", Dim.Render("then "), Dim.Render(note)))
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

// footerKeys is the keybind row, and it names the keys that do something in the
// mode you are actually in. A footer advertising "space step" on a live board
// is telling you to press a key that has been a no-op all draft, and one
// advertising "x taken" in live mode invites a hand-typed mark onto a board
// whose marks come from the feed.
//
// Ordered most-useful-first, because the tail is what drops when the row runs
// out of width. See keyFloor.
func (b Board) footerKeys() []string {
	if b.Search.Open {
		return []string{"↑/↓ pick", "enter select", "esc cancel"}
	}
	switch b.Tab {
	case 1:
		return []string{"tab notes", "←→ view", "j/k scroll", "p filter", "/ search", "q quit"}
	case 2:
		return []string{"tab board", "←→ file", "j/k scroll", "e edit", "/ search", "q quit"}
	}
	switch b.Mode {
	case ModeManual:
		return []string{"/ search", "x taken", "u undo", "tab data", "q quit"}
	case ModeLive:
		return []string{"r refresh", "/ search", "tab data", "q quit"}
	}
	return []string{"space step", "/ search", "tab data", "u undo", "q quit", "a auto"}
}

// keyFloor is how many keys the row keeps before the data-age clause starts
// giving ground instead of them. Four is where the glossary stops being a
// convenience and starts being a hole — the tab flip and the search key are not
// guessable, and a reader who never finds them does not know half the tool is
// there. Past the floor the age note steps down to its short form.
//
// What drops first is the most guessable key in the tool: q, on a screen where
// esc and ctrl+c also quit. That is the trade this ladder makes on purpose —
// how old the board is cannot be inferred from anything else on it.
const keyFloor = 4

func (b Board) footer(w int) string {
	right := Dim.Render(fmt.Sprintf("synced %s ago", since(b.Synced)))
	if b.Status != "" {
		right = Accent.Render(strings.ToLower(b.Status))
	}

	// How old the picture is, sitting next to how long ago we last synced —
	// two clocks that get mistaken for each other constantly. "synced" is the
	// poll; "adp" is the data the entire board is computed from, frozen at fetch
	// time along with every injury flag, and only meta.json knows when that was.
	//
	// Long form, then fewer keys, then the short form, then nothing. Dropping
	// beats wrapping, the same rule playerLine, the banner and the data tab's
	// legend all follow.
	keys := b.footerKeys()
	long, short := b.Fresh.note()
	keys, note := fitFooter(w, keys, []string{long, short}, lipgloss.Width(right))

	left := "  " + Dim.Render(strings.Join(keys, "   "))
	if note != "" {
		right = Dim.Render(note) + "   " + right
	}

	pad := w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return Dim.Render("  "+strings.Repeat("─", w-4)) + "\n" +
		left + strings.Repeat(" ", pad) + right
}

// fitFooter seats the longest note it can, spending trailing keys down to
// keyFloor to do it, and returns the pair that fit. No note fitting at all
// leaves every key on the row, which is the honest rendering for a board with
// no meta.json: there is nothing to say about its age.
func fitFooter(w int, keys, notes []string, rightW int) ([]string, string) {
	floor := keyFloor
	if len(keys) < floor {
		// A row already at or under the floor has nothing to spend, but it can
		// still seat a note — the search prompt's three keys are the case, and
		// without this the board went quiet about its own age while searching.
		floor = len(keys)
	}
	for _, note := range notes {
		if note == "" {
			continue
		}
		for n := len(keys); n >= floor; n-- {
			// The arithmetic the row itself uses: two cells of left margin, three
			// between the note and the sync clause, and one gap in the middle.
			left := 2 + lipgloss.Width(strings.Join(keys[:n], "   "))
			if left+lipgloss.Width(note)+rightW+6 <= w {
				return keys[:n], note
			}
		}
	}
	return keys, ""
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
