package fpl

import (
	"encoding/json"
	"testing"

	"github.com/trisslazaj/pick6/internal/sleeper"
)

// The bootstrap shapes that actually bit, in one fixture. Obviously fake names
// throughout — no real player, ranking or club appears here.
const bootstrapJSON = `{
  "elements": [
    {"id": 11, "web_name": "Alpha",  "team": 1, "element_type": 4, "draft_rank": 1,  "status": "a"},
    {"id": 12, "web_name": "Bravo",  "team": 2, "element_type": 3, "draft_rank": 2,  "status": "d",
     "news": "knock", "news_added": "2026-08-01T12:00:00Z"},
    {"id": 13, "web_name": "Cha",    "team": 1, "element_type": 2, "draft_rank": 40, "status": "i"},
    {"id": 14, "web_name": "Delta",  "team": 2, "element_type": 1, "draft_rank": 90, "status": "s"},
    {"id": 15, "web_name": "Echo",   "team": 1, "element_type": 3, "draft_rank": 12, "status": "u"}
  ],
  "teams": [{"id": 1, "short_name": "AAA"}, {"id": 2, "short_name": "BBB"}],
  "element_types": [
    {"id": 1, "singular_name_short": "GKP"}, {"id": 2, "singular_name_short": "DEF"},
    {"id": 3, "singular_name_short": "MID"}, {"id": 4, "singular_name_short": "FWD"}
  ],
  "settings": {"squad": {"size": 15, "select_GKP": 2, "select_DEF": 5, "select_MID": 5, "select_FWD": 3}}
}`

func testBootstrap(t *testing.T) *Bootstrap {
	t.Helper()
	var bs Bootstrap
	if err := json.Unmarshal([]byte(bootstrapJSON), &bs); err != nil {
		t.Fatal(err)
	}
	return &bs
}

// A player who has left the league is not a player. He keeps a draft_rank like
// everyone else, so nothing downstream would notice him — he would simply sit on
// the board being recommended.
func TestDraftableDropsPlayersWhoLeft(t *testing.T) {
	bs := testBootstrap(t)
	got := bs.Draftable()
	if len(got) != 4 {
		t.Fatalf("draftable = %d players, want 4 (one has status u)", len(got))
	}
	for _, e := range got {
		if e.WebName == "Echo" {
			t.Error("a player who left the league is on the board")
		}
	}
	// ...and the board comes out in rank order, which is the order everything
	// downstream assumes.
	for i := 1; i < len(got); i++ {
		if got[i-1].DraftRank > got[i].DraftRank {
			t.Errorf("draftable is not rank-ordered at %d", i)
		}
	}
}

// The status letters map onto the chip vocabulary the ui already speaks, and
// news_added is RFC3339 — NOT the epoch milliseconds the sleeper dump uses. Read
// as millis it would date every injury to 1970 and the news chip would never
// fire; read as RFC3339 and stamped to millis it lands where the chip looks.
func TestStatusAndNewsMapping(t *testing.T) {
	bs := testBootstrap(t)
	byName := map[string]Element{}
	for _, e := range bs.Elements {
		byName[e.WebName] = e
	}
	for _, c := range []struct{ name, want string }{
		{"Alpha", ""}, {"Bravo", "Questionable"}, {"Cha", "Out"}, {"Delta", "Sus"},
	} {
		if got := byName[c.name].InjuryChip(); got != c.want {
			t.Errorf("%s: chip = %q, want %q", c.name, got, c.want)
		}
	}
	if ms := byName["Bravo"].NewsMillis(); ms != 1785585600000 {
		t.Errorf("news millis = %d, want the 2026-08-01T12:00:00Z stamp", ms)
	}
	if ms := byName["Alpha"].NewsMillis(); ms != 0 {
		t.Errorf("a player with no news carries %d, want 0", ms)
	}
}

// Value is only ever compared by differences, so the scale is arbitrary — but it
// is an INT, and the engine's own base of 250 rounds a 560-deep board's tail to
// zero, which is the sentinel for "nobody ranked him". Every fpl player IS
// ranked and none may wear it.
func TestRankValueNeverUnderflowsToTheUnrankedSentinel(t *testing.T) {
	prev := RankValue(1, 40)
	if prev < 1000 {
		t.Fatalf("rank 1 = %d, want a curve with real resolution at the top", prev)
	}
	for rank := 2; rank <= 600; rank++ {
		v := RankValue(rank, 40)
		if v < 1 {
			t.Fatalf("rank %d = %d, which is the unranked sentinel", rank, v)
		}
		if v > prev {
			t.Fatalf("rank %d = %d rose above rank %d = %d", rank, v, rank-1, prev)
		}
		prev = v
	}
	if RankValue(0, 40) != 0 {
		t.Error("an absent rank should stay at zero, which is what unranked means")
	}
}

// Seat n is whoever picks nth in round one, which is the only place fpl ever
// states the draft order. The map has to build itself as round one happens and
// be right the moment it ends.
func TestSeatsComeFromRoundOne(t *testing.T) {
	choices := []Choice{
		{Index: 1, Round: 1, Pick: 1, Entry: 700},
		{Index: 2, Round: 1, Pick: 2, Entry: 900},
		{Index: 3, Round: 1, Pick: 3, Entry: 800},
		{Index: 4, Round: 2, Pick: 1, Entry: 800},
		{Index: 5, Round: 2, Pick: 2, Entry: 900},
		{Index: 6, Round: 2, Pick: 3, Entry: 700},
	}
	want := map[int]int{700: 1, 900: 2, 800: 3}
	for entry, seat := range want {
		if got, ok := SeatOfEntry(choices, entry); !ok || got != seat {
			t.Errorf("entry %d seat = %d (%v), want %d", entry, got, ok, seat)
		}
	}
	if _, ok := SeatOfEntry(choices, 123); ok {
		t.Error("an entry that never picked was given a seat")
	}
}

// The slot a pick carries must come from the entry's round-one seat and never
// from the snake arithmetic, because ApplyRemote's whole job is comparing the
// two. Derived from the arithmetic it would agree with itself forever.
func TestPickSlotsComeFromTheSeatMapNotTheSnake(t *testing.T) {
	f := &feed{leagueID: "x", seats: map[int]int{}, who: map[int]Named{
		42: {Name: "Alpha", Pos: "FWD", Team: "AAA"},
	}}
	picks, err := f.toPicks([]Choice{
		{Index: 1, Round: 1, Pick: 1, Entry: 700, Element: 42},
		{Index: 2, Round: 1, Pick: 2, Entry: 900, Element: 43},
		// Round two reverses: the seat-3 manager picks first.
		{Index: 3, Round: 1, Pick: 3, Entry: 800, Element: 44},
		{Index: 4, Round: 2, Pick: 1, Entry: 800, Element: 45},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(picks) != 4 {
		t.Fatalf("got %d picks, want 4", len(picks))
	}
	if picks[3].DraftSlot != 3 {
		t.Errorf("the round-two opener sits at slot %d, want 3 — the snake's own answer, "+
			"reached independently", picks[3].DraftSlot)
	}
	if picks[0].PlayerID != "42" || picks[0].Metadata.FirstName != "Alpha" {
		t.Errorf("pick 1 = %+v, want the element id stringified and the player named", picks[0])
	}
	// A pick from a manager round one never seated is a wrong roster all night.
	if _, err := f.toPicks([]Choice{{Index: 9, Round: 2, Pick: 1, Entry: 999}}); err == nil {
		t.Error("a pick from an unseated entry was accepted")
	}
}

// Not max_entries: the fixture league advertises eight and drafted with seven,
// carrying an empty row where the eighth would be. Every pick number in the
// snake math derives from this count.
func TestTeamsCountsSeatsThatExist(t *testing.T) {
	l := &League{Entries: []Entry{
		{EntryID: 1, FirstName: "One"}, {EntryID: 2, FirstName: "Two"}, {EntryID: 0},
	}}
	if got := l.Teams(); got != 2 {
		t.Errorf("Teams() = %d, want 2 — the empty row is not a manager", got)
	}
}

var _ sleeper.Feed = (*feed)(nil)

// The status a poll reports decides whether the board keeps polling, and draft
// night walks all three states in one sitting: empty feed before 02:00, picks
// arriving for ninety minutes, then done.
//
// The trap it guards is the middle one. FPL's own draft_status stays "pre" until
// the draft ENDS — it never says "drafting" — and we only re-read it every tenth
// poll, which at 3s is half a minute. Without the between-fetches promotion the
// header would say nobody has picked while picks scrolled past in the ticker.
func TestDraftStatusWalksTheNight(t *testing.T) {
	pre := &League{}
	pre.Info.DraftStatus = "pre"
	post := &League{}
	post.Info.DraftStatus = "post"

	for _, c := range []struct {
		name  string
		l     *League
		picks int
		want  string
	}{
		{"before it starts", pre, 0, "pre_draft"},
		{"picks are arriving, and fpl still says pre", pre, 7, "drafting"},
		{"over", post, 150, "complete"},
	} {
		if got := draftStatus(c.l, c.picks); got != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, got, c.want)
		}
	}

	// "complete" is the literal the ui compares against; anything else and the
	// board polls a finished draft forever.
	var snap sleeper.Snapshot
	snap.Status = draftStatus(post, 150)
	if !snap.Complete() {
		t.Error("the finished draft did not read as complete to the model")
	}
}

// The seat map persists across polls and is not rebuilt from scratch each time,
// because a mid-draft reconnect starts with an empty one and fills it from the
// feed's own round one — which is still in the feed, since we re-read the whole
// list every poll.
func TestSeatsSurviveRepeatedPolls(t *testing.T) {
	f := &feed{leagueID: "x", seats: map[int]int{}, who: map[int]Named{}}
	round1 := []Choice{
		{Index: 1, Round: 1, Pick: 1, Entry: 700, Element: 1},
		{Index: 2, Round: 1, Pick: 2, Entry: 800, Element: 2},
	}
	if _, err := f.toPicks(round1); err != nil {
		t.Fatal(err)
	}
	// A later poll carrying the same round one plus more.
	all := append(append([]Choice{}, round1...),
		Choice{Index: 3, Round: 2, Pick: 1, Entry: 800, Element: 3},
		Choice{Index: 4, Round: 2, Pick: 2, Entry: 700, Element: 4})
	picks, err := f.toPicks(all)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{1, 2, 2, 1} {
		if picks[i].DraftSlot != want {
			t.Errorf("pick %d landed at seat %d, want %d", i+1, picks[i].DraftSlot, want)
		}
	}
}
