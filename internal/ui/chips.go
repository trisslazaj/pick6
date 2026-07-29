package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/trisslazaj/pick6/internal/engine"
)

// Chips are the truth layer: short badges beside a player's name carrying a
// fact the numbers cannot. They are display only and must stay that way —
// value, survival, urgency, tier-hold and group order never read them. Our
// values are imported, so marking a man down for a pup designation would be a
// projection of our own, which this project does not make. The board states the
// fact and the human does the discounting.
//
// They live INSIDE the name column's width rather than after it, so a chip can
// never change a row's width. At 80 columns the board's player row spends 36 of
// its 37 cells and the data row spends 76 of 80; anything appended would wrap,
// and a wrapped row reads as a rendering fault. The name gives up cells to pay
// for a chip and keeps at least chipMinName of them — past that the chip drops
// instead, the same discipline playerLine already applies to the bye column.
//
// chipMinName is 12 because that is where a truncated name stops identifying
// anybody: "ja'marr cha…" is still a person, "ja'marr c…" at 10 is a guess.
const chipMinName = 12

// tripwireChip is 4b's copy, compressed. The full sentence — "past worst
// observed pick — check news" — is 36 cells and fits no column on either tab,
// so it rides on the data tab's legend line, which only prints it when a chip
// that needs it is actually on the page.
const tripwireChip = "past low"

// chip is one badge: already-lowercase text plus how to paint it.
type chip struct {
	text  string
	style lipgloss.Style
}

// injuryAlarm maps a sleeper injury_status to its badge.
//
// Measured on the cached dump (3,223 active fantasy players): injury_status is
// EMPTY for 3,090 of them, and the non-empty values across the full dump are
// Questionable, PUP, NA, IR, Out, Sus, COV and DNR. Every one of those except
// Questionable is in here. The spec names {Out, IR, PUP, Doubtful, Sus}; NA,
// DNR and COV are the same class of alarm and the dump actually carries them,
// so they are handled too. Doubtful is the reverse case — in the spec, absent
// from a july dump, because sleeper only uses it in season.
//
// Questionable is DELIBERATELY absent: 93 active players carry it in july, so a
// chip on it would sit on roughly one board player in three. That is wallpaper,
// and a chip the reader learns to skip is worse than no chip, because it costs
// name column on every row it decorates.
var injuryAlarm = map[string]string{
	"OUT":      "out",
	"IR":       "ir",
	"PUP":      "pup",
	"DOUBTFUL": "dbt",
	"SUS":      "sus",
	"NA":       "na",
	"DNR":      "dnr",
	"COV":      "cov",
}

// injuryChip is the red badge, or "" when the dump has nothing alarming to say.
//
// The status arm reads "present and not active", never "not active": 32 players
// in the active pool carry an EMPTY status, and empty means the dump did not
// say. Calling those men injured would be inventing a fact about a real person,
// which is the one thing a truth layer cannot do.
func injuryChip(p engine.Player) string {
	if c, ok := injuryAlarm[strings.ToUpper(strings.TrimSpace(p.InjuryStatus))]; ok {
		return c
	}
	switch strings.ToUpper(strings.TrimSpace(p.Status)) {
	case "", "ACTIVE":
		return ""
	case "INJURED RESERVE":
		return "ir"
	default:
		return "inact"
	}
}

// newsChip is the dim "news 3h" badge: sleeper's news_updated moved recently
// enough that something is probably being said about him.
//
// NewsUpdated is epoch MILLISECONDS (verified in players.json: 1785334208692
// for a july 2026 player). time.Unix takes seconds, so a missing factor of 1000
// dates everybody to january 1970 and the chip fires on nobody; the slip the
// other way puts everybody in the year 58555 and — with a clamp on negative
// ages — fires it on everybody. Either failure keeps rendering while carrying
// no information, which is the kind that survives a screenshot. Hence
// time.UnixMilli, and hence the future guard below being silence rather than a
// clamp: a timestamp ahead of our own clock is bad data, and a chip that claims
// to know when something happened should say nothing when it doesn't.
func newsChip(p engine.Player, now time.Time) string {
	if p.NewsUpdated <= 0 {
		return "" // null in the dump: no news, not old news
	}
	age := now.Sub(time.UnixMilli(p.NewsUpdated))
	if age < 0 || age > engine.NewsFreshHours*time.Hour {
		return ""
	}
	return fmt.Sprintf("news %dh", int(age.Hours()))
}

// tripwire reports whether the draft has run past the latest pick any observed
// draft took this player at.
//
// Low is the MAXIMUM observed pick and High the minimum — the easy pair to get
// backwards, and verified the right way round in players.json (alec pierce: adp
// 61.8, high 40, low 84). So being past Low means today's room is behind every
// draft in ffc's sample on him at once. That is either a bargain or news nobody
// told us about, and the tool genuinely cannot tell which, which is why the copy
// says check rather than offering a discount.
//
// TripwireSlack keeps a player who is one pick past his worst observed price
// from chipping on what is effectively a rounding difference; Low is a max over
// ~1,187 drafts and a room running a pick or two behind national adp is normal.
//
// In practice this fires only on players the engine already calls Falling — Low
// exceeds ADP by several sigma for anyone with a real sample — so the chip
// extends the amber treatment rather than introducing a new state. It is not
// derived from Falling, because a player with no sample at all has no worst
// observed pick and must not be accused of passing one.
func (b Board) tripwire(p engine.Player) bool {
	if p.Low <= 0 {
		return false // no sample: nothing to be past
	}
	// A suppressed position gets no tripwire. Need is zero only for k and def
	// before the last rounds, and every one of them is past his national low from
	// about round 9 onward — the engine holds them, the mock's picker holds them
	// and this league holds them (first def in round 11, measured). The tool is
	// the reason they are still on the board, so chipping them would be the board
	// raising an alarm about its own design, on six rows of the data tab, every
	// frame. Once it is kicker o'clock and need turns on they trip like anyone
	// else.
	if b.State.Need(p.Pos) == 0 {
		return false
	}
	return b.State.PickNo > p.Low+engine.TripwireSlack
}

// now is the clock the news chip reads. A field rather than time.Now() directly
// so tests can pin it: "three hours ago" is not assertable against a wall clock.
func (b Board) now() time.Time {
	if b.Now.IsZero() {
		return time.Now()
	}
	return b.Now
}

// alarmChips are the badges worth a truncated name: "ja'marr ch… pup" tells you
// more than "ja'marr chase" does. Injury first, tripwire second — that is the
// order in which they change what you do at the clock, and therefore the order
// they drop in when the column runs out.
//
// Two intensities for two severities. The injury chip is reverse video on the
// cliff red, the same treatment the cliff banner gets, so the two loudest things
// on the board read as one family. The tripwire is plain amber text because it
// IS the existing falling treatment extended rather than a new state. Nothing
// tints a whole row.
func (b Board) alarmChips(p engine.Player) []chip {
	var out []chip
	if c := injuryChip(p); c != "" {
		out = append(out, chip{c, ChipAlarm})
	}
	if b.tripwire(p) {
		out = append(out, chip{tripwireChip, Run})
	}
	return out
}

// renderChips lays badges out inside a cell budget, stopping at the first one
// that doesn't fit rather than skipping it for a smaller one behind it —
// showing a news flag while hiding an injury would be the exact inversion of
// the priority order above.
//
// Returns the rendered run (each chip carries its own leading space), the cells
// it costs so the caller can pad the column back to a fixed width, and how many
// chips actually made it — the data tab's legend needs to know whether the chip
// it is about to explain reached the screen, which the predicate cannot say.
func renderChips(cs []chip, budget int) (string, int, int) {
	var sb strings.Builder
	used, n := 0, 0
	for _, c := range cs {
		w := 1 + lipgloss.Width(c.text)
		if used+w > budget {
			break
		}
		sb.WriteString(" " + c.style.Render(c.text))
		used += w
		n++
	}
	return sb.String(), used, n
}

// chipBudget is how many cells of a name column of width w the chips may spend.
// One definition, because tripwireShown has to predict exactly what nameCell
// will do and two copies of this arithmetic would drift apart at precisely the
// widths where it matters.
func chipBudget(w int) int {
	if b := w - chipMinName; b > 0 {
		return b
	}
	return 0
}

// tripwireShown is tripwire() plus "and the chip actually fit". They are
// different questions, because chips are paid for out of the name column: at
// dataNameW(80) = 18 the budget is 18 - chipMinName = 6 cells and " past low"
// costs 9, so below 83 columns the chip is dropped for every player on the page.
//
// The data tab's legend keys off this rather than the predicate. Keyed off the
// predicate it printed "past worst observed pick — check news" at widths 80-82
// with no chip anywhere in the frame, and those 37 cells also crowded all three
// column-glossary clauses off the line — so the one row under the table
// explained an absent marker instead of the "9d" tier markers that were present.
func (b Board) tripwireShown(p engine.Player, nameW int) bool {
	if !b.tripwire(p) {
		return false
	}
	// alarmChips emits the tripwire last, so it reached the screen exactly when
	// every chip did.
	cs := b.alarmChips(p)
	_, _, n := renderChips(cs, chipBudget(nameW))
	return n == len(cs)
}

// nameCell is a player's name plus his chips, padded to exactly w cells. Both
// tabs go through here so a chip can never appear on one and not the other, and
// so neither tab's row arithmetic has to know chips exist.
//
// w is floored at 16 by playerLine and 18 by dataRow, both above chipMinName,
// which is what guarantees the truncation below always has cells to work with.
func (b Board) nameCell(p engine.Player, style lipgloss.Style, w int) string {
	name := strings.ToLower(p.Name)

	chips, cw, _ := renderChips(b.alarmChips(p), chipBudget(w))

	// The news flag is spent out of what the name did NOT want, never out of the
	// name itself. It is context rather than an instruction, and it is on 54 of
	// the shipped board's 203 players — measured — so charging a truncated name
	// for it would cost a quarter of the rows their last few letters to say
	// "somebody wrote something about him". Short names carry the chip, long ones
	// keep their letters, and the trade always favours the name.
	if c := newsChip(p, b.now()); c != "" {
		if slack := w - cw - lipgloss.Width(name); slack >= 1+lipgloss.Width(c) {
			chips += " " + Dim.Render(c)
			cw += 1 + lipgloss.Width(c)
		}
	}

	name = trunc(name, w-cw)
	return style.Render(name) + chips +
		strings.Repeat(" ", w-cw-lipgloss.Width(name))
}
