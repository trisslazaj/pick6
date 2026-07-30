package ui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/trisslazaj/pick6/internal/engine"
)

// pinnedNow is the clock every chip test reads. "three hours ago" is not
// assertable against a wall clock.
var pinnedNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// msAgo is a news_updated stamp d in the past, in the units sleeper actually
// uses. Spelled out so no test has to open-code the factor of 1000 the chip is
// here to get right.
func msAgo(d time.Duration) int64 { return pinnedNow.Add(-d).UnixMilli() }

// mark writes the source fields the chips read onto a fixture player.
// engine.Player is a value in the map, so this is a whole-entry replacement.
func mark(s *engine.State, id string, p engine.Player) {
	cur := s.Players[id]
	cur.InjuryStatus, cur.Status, cur.NewsUpdated, cur.Low = p.InjuryStatus, p.Status, p.NewsUpdated, p.Low
	s.Players[id] = cur
}

// The alarm set is the point of the chip and the exclusions are the point of
// the set.
//
// Measured on the cached dump (3,223 active fantasy players): injury_status is
// empty for 3,090, and the non-empty values across the full dump are
// Questionable, PUP, NA, IR, Out, Sus, COV and DNR. Questionable sits on 93 of
// them in july — about one board player in three — so a chip on it would be
// wallpaper, and a chip the reader learns to skip costs name column on every row
// it decorates while saying nothing.
//
// The status arm is the other half, and the one that can fabricate: 32 players
// in the active pool carry an EMPTY status, where empty means the dump did not
// say. Calling those men injured would be inventing a fact about a real person.
func TestInjuryChip(t *testing.T) {
	cases := []struct {
		injury, status, want string
	}{
		{"PUP", "Active", "pup"},
		{"IR", "Active", "ir"},
		{"Out", "Active", "out"},
		{"Sus", "Active", "sus"},
		{"NA", "Active", "na"},
		{"DNR", "Active", "dnr"},
		{"COV", "Active", "cov"},
		{"Doubtful", "Active", "dbt"}, // in the spec, absent from a july dump
		{"Questionable", "Active", ""},
		{"", "Active", ""}, // the normal case: 3,090 of 3,223
		{"", "", ""},       // 32 players: unknown, and never an alarm
		{"", "Inactive", "inact"},
		{"", "Injured Reserve", "ir"},
		{"Questionable", "Inactive", "inact"}, // status speaks when injury won't
		{"pup", "active", "pup"},              // casing is the source's business
		{"  PUP  ", "Active", "pup"},
	}
	for _, c := range cases {
		got := injuryChip(engine.Player{InjuryStatus: c.injury, Status: c.status})
		if got != c.want {
			t.Errorf("injury %q status %q: chip = %q, want %q",
				c.injury, c.status, got, c.want)
		}
	}
}

// news_updated is epoch MILLISECONDS (verified in players.json: 1785334208692
// for a july 2026 player). time.Unix takes seconds, so a missing factor of 1000
// dates everybody to january 1970 and the chip fires on nobody; the same slip
// the other way puts everybody in the year 58555, and with a clamp on negative
// ages it would fire on everybody. Either failure keeps rendering while carrying
// no information at all, which is the kind that survives a screenshot — hence
// the last case, which is the real timestamp read as seconds.
func TestNewsChipReadsMilliseconds(t *testing.T) {
	cases := []struct {
		name string
		ms   int64
		want string
	}{
		{"just now", msAgo(20 * time.Minute), "news 0h"},
		{"three hours", msAgo(3 * time.Hour), "news 3h"},
		{"inside the window", msAgo(47*time.Hour + 30*time.Minute), "news 47h"},
		{"past the window", msAgo(engine.NewsFreshHours*time.Hour + time.Minute), ""},
		{"weeks old", msAgo(400 * time.Hour), ""},
		{"null in the dump", 0, ""},
		// The same instant read as seconds instead of milliseconds: the year
		// 58555, which is not news, and must render as nothing rather than "0h".
		{"seconds mistaken for millis", msAgo(3*time.Hour) * 1000, ""},
		// And read as milliseconds when it was seconds: january 1970.
		{"millis mistaken for seconds", msAgo(3*time.Hour) / 1000, ""},
	}
	for _, c := range cases {
		got := newsChip(engine.Player{NewsUpdated: c.ms}, pinnedNow)
		if got != c.want {
			t.Errorf("%s: chip = %q, want %q", c.name, got, c.want)
		}
	}
}

// Low is the LATEST pick any observed draft took him at; High is the earliest.
// That pair is the easy one to get backwards, and it is verified the right way
// round in players.json (alec pierce: adp 61.8, high 40, low 84). Reading High
// here would fire the chip 20-odd picks early, on players who are merely at
// their price rather than past every draft in the sample.
func TestTripwireChipFiresPastObservedLow(t *testing.T) {
	cases := []struct {
		name         string
		pickNo, low  int
		pos          string
		wantTripwire bool
	}{
		{"before his worst price", 60, 84, "RB", false},
		{"exactly at it", 84, 84, "RB", false},
		{"inside the slack", 86, 84, "RB", false},
		{"one past the slack", 87, 84, "RB", true},
		{"long past", 140, 84, "RB", true},
		{"no sample at all", 200, 0, "RB", false},
		// k and def are past their national low from about round 9 onward because
		// the engine, the mock's picker and this league all hold them to the end.
		// The tool is the reason they are still there, so the alarm would be the
		// board complaining about its own design on six rows every frame.
		{"suppressed def", 140, 95, "DEF", false},
		{"suppressed k", 140, 95, "K", false},
	}
	for _, c := range cases {
		s := waitState()
		s.PickNo = c.pickNo
		id := "trip"
		s.Players[id] = engine.Player{ID: id, Name: "fake tripper", Pos: c.pos,
			Team: "AAA", Value: 1, ADP: 60, Sigma: 6, Low: c.low}
		b := Board{State: s, Width: 92, Height: 40, Now: pinnedNow}
		if got := b.tripwire(s.Players[id]); got != c.wantTripwire {
			t.Errorf("%s (pick %d, low %d, %s): tripwire = %v, want %v",
				c.name, c.pickNo, c.low, c.pos, got, c.wantTripwire)
		}
	}
}

// chipState is the rendering fixture: one flagged back, one merely questionable
// back, one back past his worst observed pick, and one with fresh news. Built on
// waitState so the tilt still solves inside its bracket (see
// TestFixturesAreNotTiltClamped) — chips are a display layer and must not need
// their own draft geometry.
func chipState() *engine.State {
	s := waitState()
	mark(s, "r1", engine.Player{InjuryStatus: "PUP", Status: "Active"})
	mark(s, "r2", engine.Player{InjuryStatus: "Questionable", Status: "Active"})
	mark(s, "r3", engine.Player{NewsUpdated: msAgo(3 * time.Hour)})
	mark(s, "rx1", engine.Player{Low: 4}) // pick 1 + slack is not past 4; see below
	return s
}

// chipsOn is whatever a rendered row carries between the player's name and the
// start of the next column, which is exactly the chip run. Asserting on this
// rather than on Contains is the difference between "the chip is there" and
// "nothing else is" — and the questionable case below is entirely about the
// second.
func chipsOn(row, name string) string {
	i := strings.Index(row, name)
	if i < 0 {
		return ""
	}
	rest := row[i+len(name):]
	if j := strings.Index(rest, "AAA"); j >= 0 { // every fixture's team column
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// A flagged player wears his chip on both tabs; a questionable one wears
// nothing at all. Both halves matter: the chip is only worth the name column it
// costs because it is rare, and it is only rare because Questionable — 93 active
// players in july — is excluded.
func TestFlaggedPlayerRendersChipAndQuestionableDoesNot(t *testing.T) {
	cases := []struct {
		id, want string
	}{
		{"fake r1", "pup"},
		{"fake r2", ""}, // questionable
		{"fake r3", "news 3h"},
	}
	for _, tab := range []int{0, 1} {
		view := ansi.ReplaceAllString(
			Board{State: chipState(), Width: 104, Height: 40, Tab: tab, Now: pinnedNow}.View(), "")
		for _, c := range cases {
			row := playerRow(view, c.id)
			if row == "" {
				t.Fatalf("tab %d: %s is not on screen", tab, c.id)
			}
			if got := chipsOn(row, c.id); got != c.want {
				t.Errorf("tab %d: %s carries chips %q, want %q", tab, c.id, got, c.want)
			}
		}
	}
}

// The tripwire copy is 36 cells and fits no column, so it rides on the data
// tab's legend — and only when a chip that needs explaining is on the page. A
// legend for something you cannot see is width spent on nothing, and this row
// has none to spare.
func TestTripwireChipAndItsLegend(t *testing.T) {
	s := chipState()
	s.PickNo = 20 // past rx1's low of 4, and past nobody else's
	view := ansi.ReplaceAllString(
		Board{State: s, Width: 104, Height: 40, Tab: 1, Now: pinnedNow}.View(), "")

	if row := playerRow(view, "fake rx1"); !strings.Contains(row, tripwireChip) {
		t.Errorf("a player past his worst observed pick should chip, got %q", row)
	}
	if !strings.Contains(view, "past worst observed pick — check news") {
		t.Errorf("the legend should explain the chip while one is on screen:\n%s", view)
	}

	// Roll the vantage back behind every observed low: no chip, and the legend
	// goes back to spending its width on the columns.
	s.PickNo = 3
	view = ansi.ReplaceAllString(
		Board{State: s, Width: 104, Height: 40, Tab: 1, Now: pinnedNow}.View(), "")
	if strings.Contains(view, tripwireChip) {
		t.Errorf("nobody is past his worst pick at 1.03:\n%s", view)
	}
	if strings.Contains(view, "past worst observed pick") {
		t.Error("the legend should not explain a chip that isn't on the page")
	}
	if !strings.Contains(view, "d: derived tier") {
		t.Error("with the chip gone the column legend should have its width back")
	}
}

// The legend has to key off the chip REACHING THE SCREEN, not off the predicate
// that would draw it. Chips are paid for out of the name column, so the two
// disagree at tight widths: dataNameW(80) is 18, the chip budget is
// 18 - chipMinName = 6 cells, and " past low" costs 9. At 80-82 columns the
// frame carried "past worst observed pick — check news" with no chip anywhere in
// it, and those 37 cells spent the whole w-4 budget, so all three column
// clauses dropped too — the row explained an absent marker instead of the "9d"
// tier markers that were on screen. 80 is MinWidth, i.e. the canonical terminal.
func TestTripwireLegendFollowsTheChipNotThePredicate(t *testing.T) {
	cases := []struct {
		width    int
		wantChip bool
	}{
		{80, false}, // MinWidth: budget 6 against a 9-cell chip
		{82, false}, // last width that cannot pay
		{83, true},  // dataNameW hits 21, budget 9, the chip fits exactly
		{104, true}, // MaxWidth
	}
	for _, c := range cases {
		s := chipState()
		s.PickNo = 20 // past rx1's low of 4, and past nobody else's
		view := ansi.ReplaceAllString(
			Board{State: s, Width: c.width, Height: 40, Tab: 1, Now: pinnedNow}.View(), "")

		gotChip := strings.Contains(view, " "+tripwireChip)
		gotLegend := strings.Contains(view, "past worst observed pick")
		if gotChip != c.wantChip {
			t.Errorf("width %d: chip on screen = %v, want %v", c.width, gotChip, c.wantChip)
		}
		if gotLegend != gotChip {
			t.Errorf("width %d: legend = %v but chip = %v; the legend must follow the chip",
				c.width, gotLegend, gotChip)
		}
		// The width the legend gives up goes back to the columns, which is the
		// point: an 80-column frame should explain what "d" means.
		if !gotChip && !strings.Contains(view, "spread: how widely drafts vary on him") {
			t.Errorf("width %d: with no chip the column legend should have the row:\n%s",
				c.width, view)
		}
	}
}

// The footer says how old the picture is, because adp and every injury flag
// froze at fetch time and nothing else on screen can say when that was.
//
// The absent case is the one that matters: boards fetched before meta.json
// existed are on disk right now, and "adp 0h old" about one of them is a lie the
// reader has no way to catch. Silence is the only honest rendering.
func TestFooterShowsBoardAge(t *testing.T) {
	cases := []struct {
		name     string
		fresh    Freshness
		width    int
		want     string
		notWants []string
	}{
		{"long form when the row can pay for it",
			Freshness{FetchedAt: time.Now().Add(-26 * time.Hour), Drafts: 1110},
			104, "adp 26h old · 1,110 drafts", nil},
		{"short form at 80 columns, where the keybinds already spent the row",
			Freshness{FetchedAt: time.Now().Add(-26 * time.Hour), Drafts: 1110},
			80, "adp 26h old", []string{"1,110 drafts"}},
		{"days once hours stop being readable",
			Freshness{FetchedAt: time.Now().Add(-100 * time.Hour), Drafts: 1187},
			104, "adp 4d old · 1,187 drafts", nil},
		{"no meta.json: nothing at all",
			Freshness{}, 104, "", []string{"adp 0h old", "adp ", "drafts"}},
		{"meta without a draft count drops the count, not the age",
			Freshness{FetchedAt: time.Now().Add(-2 * time.Hour)},
			104, "adp 2h old", []string{"drafts"}},
	}
	for _, c := range cases {
		view := ansi.ReplaceAllString(
			Board{State: waitState(), Width: c.width, Height: 40, Fresh: c.fresh}.View(), "")
		foot := lastLine(view)
		if c.want != "" && !strings.Contains(foot, c.want) {
			t.Errorf("%s: footer %q should contain %q", c.name, foot, c.want)
		}
		for _, no := range c.notWants {
			if strings.Contains(foot, no) {
				t.Errorf("%s: footer %q must not contain %q", c.name, foot, no)
			}
		}
	}
}

// The live board warns about its own age and never blocks on it: a stale board
// still beats no board at a draft party, the same principle the poll loop
// already follows when the wifi drops. And with no meta.json there is nothing
// to warn about — an absent file is not a stale one.
func TestStaleWarning(t *testing.T) {
	cases := []struct {
		name  string
		fresh Freshness
		want  string
	}{
		{"fresh", Freshness{FetchedAt: time.Now().Add(-2 * time.Hour)}, ""},
		{"past StaleADPHours",
			Freshness{FetchedAt: time.Now().Add(-31 * time.Hour)},
			"adp and injury flags are 31h old"},
		{"fetched minutes ago off a stale cache",
			Freshness{FetchedAt: time.Now().Add(-time.Minute),
				WindowEnd: time.Now().Add(-5 * 24 * time.Hour)},
			"ffc's adp window ended 5d ago"},
		{"window still inside the tolerance",
			Freshness{FetchedAt: time.Now().Add(-time.Minute),
				WindowEnd: time.Now().Add(-25 * time.Hour)}, ""},
		{"no meta.json", Freshness{}, ""},
	}
	for _, c := range cases {
		got, ok := c.fresh.Stale()
		if (c.want == "") == ok {
			t.Errorf("%s: stale = %v with message %q", c.name, ok, got)
			continue
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: message %q should contain %q", c.name, got, c.want)
		}
	}

	// And it reaches the screen, under the board rather than in place of it.
	m := liveModel(waitState(), &fakeFeed{}).WithFreshness(
		Freshness{FetchedAt: time.Now().Add(-31 * time.Hour)})
	view := ansi.ReplaceAllString(m.View(), "")
	if !strings.Contains(view, "stale — adp and injury flags are 31h old") {
		t.Errorf("the live view should carry the stale line:\n%s", view)
	}
	if !strings.Contains(view, "best available") {
		t.Error("staleness warns, never blocks — the board must still be there")
	}
}

var pctRe = regexp.MustCompile(`\d+%`)

// Chips are a truth layer and nothing more: they must not move value, survival,
// urgency, tier-hold or group order by a single cell. The risk is real — they
// are computed inside the render, from fields sitting on engine.Player right
// next to the ones survival reads — and a chip that quietly reordered the board
// would be indistinguishable from the board being right.
//
// Two frames of the same draft, one with every injury, news and support field
// set and one with none: the position order and every probability on screen have
// to match exactly.
func TestChipsDoNotChangeTheBoard(t *testing.T) {
	for _, tab := range []int{0, 1} {
		plain := Board{State: waitState(), Width: 104, Height: 40, Tab: tab, Now: pinnedNow}
		chipped := Board{State: chipState(), Width: 104, Height: 40, Tab: tab, Now: pinnedNow}
		chipped.State.Players["r1"] = func() engine.Player {
			p := chipped.State.Players["r1"]
			p.High, p.TimesDrafted = 3, 900 // the support fields too, while we're here
			return p
		}()

		a := ansi.ReplaceAllString(plain.View(), "")
		b := ansi.ReplaceAllString(chipped.View(), "")

		if tab == 0 {
			if x, y := groupOrder(a), groupOrder(b); !reflect.DeepEqual(x, y) {
				t.Errorf("chips reordered the board: %v became %v", x, y)
			}
		}
		if x, y := pctRe.FindAllString(a, -1), pctRe.FindAllString(b, -1); !reflect.DeepEqual(x, y) {
			t.Errorf("tab %d: chips moved a probability: %v became %v", tab, x, y)
		}
	}
}

// Chips are new horizontal content in the two tightest columns on either tab, so
// the layout law gets re-checked with them on: no rendered line wider than the
// terminal, at any width. They are paid for out of the name column precisely so
// this can never fail — the board row already spends 36 of its 37 cells at 80
// columns and there is nothing to append to.
//
// The name is also asserted to survive: an alarm may truncate it down to
// chipMinName and no further, and a row whose name had been eaten entirely would
// pass a width check while being useless.
func TestChipsFitEveryWidth(t *testing.T) {
	for _, w := range []int{80, 92, 104, 140} {
		for _, tab := range []int{0, 1} {
			s := chipState()
			s.PickNo = 20 // far enough along that the tripwire is up too
			view := Board{State: s, Width: w, Height: 40, Tab: tab, Now: pinnedNow}.View()
			for i, line := range strings.Split(view, "\n") {
				if n := len([]rune(ansi.ReplaceAllString(line, ""))); n > w {
					t.Errorf("width %d tab %d: line %d is %d runes:\n%s", w, tab, i, n, line)
				}
			}
			plain := ansi.ReplaceAllString(view, "")
			if row := playerRow(plain, "fake r1"); !strings.Contains(row, "pup") {
				t.Errorf("width %d tab %d: the alarm chip dropped, got %q", w, tab, row)
			}
			if row := playerRow(plain, "fake r1"); !strings.Contains(row, "fake") {
				t.Errorf("width %d tab %d: the chip ate the name, got %q", w, tab, row)
			}
		}
	}
}

// lastLine is the footer: the last non-empty rendered row.
func lastLine(view string) string {
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// The tripwire's suppression gate asks the k/def rule, not Need.
//
// It used to ask Need == 0 on the premise that only k and def ever read zero.
// The endgame guard broke that premise: once my remaining picks equal my open
// starting slots, Need drops to zero for every bench-weight skill position too.
// The board tab hides those groups, but the data tab still lists their players —
// so the chip, and the legend line that follows it, vanished from rows that were
// still on screen and still past every observed draft's worst price.
func TestTripwireSurvivesTheEndgameGuard(t *testing.T) {
	// Round 15 of 15, slot 3: pick 171 is my last, so R is 1. Every slot but K is
	// filled, so U is 1 too — the R == U regime, where a bench pick is worth
	// nothing and needFrom's bench weight gets multiplied to zero. Both receiver
	// slots and the flex are taken, so wr is squarely a bench position here.
	s := endgameBoard(171, []string{"QB", "RB", "RB", "WR", "WR", "TE", "RB", "DEF"})
	id := "trip"
	s.Players[id] = engine.Player{ID: id, Name: "fake tripper", Pos: "WR",
		Team: "AAA", Value: 1, ADP: 60, Sigma: 6, Low: 90}
	b := Board{State: s, Width: 92, Height: 40, Now: pinnedNow}

	if s.Need("WR") != 0 {
		t.Fatalf("fixture no longer exercises the guard: need(wr) = %v", s.Need("WR"))
	}
	if !b.tripwire(s.Players[id]) {
		t.Error("a wr 81 picks past his worst observed price must still trip")
	}
}
