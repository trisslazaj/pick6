package adp

import "testing"

func TestInjuryAlarm(t *testing.T) {
	cases := []struct {
		name   string
		injury string
		status string
		want   bool
	}{
		{"healthy", "", "Active", false},
		{"pup", "PUP", "Active", true},
		{"ir field", "IR", "Active", true},
		{"ir status", "", "Injured Reserve", true},
		{"inactive", "", "Inactive", true},
		// Both of these are in the dump and were not in the original spec's list.
		{"not active", "NA", "Active", true},
		{"did not report", "DNR", "Active", true},
		// 93 active players carry Questionable in july. Flagging them all would
		// make the chip wallpaper and the report useless, which is the whole
		// reason the alarm set is a list and not "injury_status != null".
		{"questionable is not an alarm", "Questionable", "Active", false},
		// 32 active players have no status at all. Treating empty as non-Active
		// would invent an injury for every one of them.
		{"empty status is unknown, not hurt", "", "", false},
		{"case and whitespace", " out ", "Active", true},
	}
	for _, c := range cases {
		if got := InjuryAlarm(c.injury, c.status); got != c.want {
			t.Errorf("%s: InjuryAlarm(%q, %q) = %v, want %v", c.name, c.injury, c.status, got, c.want)
		}
	}
}

func TestInjuryNote(t *testing.T) {
	cases := []struct {
		injury string
		status string
		want   string
	}{
		{"", "Active", ""},
		{"PUP", "Active", "pup"},
		{"", "Inactive", "inactive"},
		{"IR", "Injured Reserve", "ir · injured reserve"},
		{"Questionable", "Active", ""}, // not an alarm, so not a chip
	}
	for _, c := range cases {
		if got := InjuryNote(c.injury, c.status); got != c.want {
			t.Errorf("InjuryNote(%q, %q) = %q, want %q", c.injury, c.status, got, c.want)
		}
	}
}

// TestTierADPGaps pins two skips that would otherwise be invisible: an untiered
// player (K/DEF, or anyone no source valued) and a player with no adp are both
// dropped from BOTH rankings, not just the one they lack. Leaving either in
// would shift every rank below him and manufacture disagreements for players the
// file and the market actually agree about.
func TestTierADPGaps(t *testing.T) {
	mk := func(id, name, pos string, tier, value int, adp float64) *Player {
		return &Player{SleeperID: id, Name: name, Pos: pos, Tier: tier, Value: value, ADP: adp}
	}
	players := map[string]*Player{
		// wr: by tier a,b,c,d — by adp b,c,a,d. only a moves (1 -> 3).
		"a": mk("a", "fake receiver one", "WR", 1, 100, 30),
		"b": mk("b", "fake receiver two", "WR", 2, 90, 10),
		"c": mk("c", "fake receiver three", "WR", 3, 80, 20),
		"d": mk("d", "fake receiver four", "WR", 4, 70, 40),
		// Skipped: no tier, and no adp. Either one counted would make a's delta -3.
		"e": mk("e", "fake kicker", "WR", 0, 60, 5),
		"f": mk("f", "fake deep receiver", "WR", 2, 85, 0),
		// te: by tier t1..t4 — by adp t2,t3,t4,t1. t1 moves 1 -> 4.
		"t1": mk("t1", "fake tight end one", "TE", 1, 100, 50),
		"t2": mk("t2", "fake tight end two", "TE", 2, 90, 10),
		"t3": mk("t3", "fake tight end three", "TE", 3, 80, 20),
		"t4": mk("t4", "fake tight end four", "TE", 4, 70, 30),
		// qb: both orderings agree, and their adps interleave with the others —
		// ranks are per position, so neither shows up.
		"q1": mk("q1", "fake quarterback one", "QB", 1, 50, 3),
		"q2": mk("q2", "fake quarterback two", "QB", 2, 40, 60),
	}

	got := TierADPGaps(players, 2)
	if len(got) != 2 {
		t.Fatalf("got %d gaps, want 2 (%v)", len(got), got)
	}
	// Biggest disagreement first.
	if got[0].Player.SleeperID != "t1" || got[0].Delta != -3 || got[0].TierRank != 1 || got[0].ADPRank != 4 {
		t.Errorf("first gap = %s tier rank %d adp rank %d delta %d, want t1 1 4 -3",
			got[0].Player.SleeperID, got[0].TierRank, got[0].ADPRank, got[0].Delta)
	}
	if got[1].Player.SleeperID != "a" || got[1].Delta != -2 {
		t.Errorf("second gap = %s delta %d, want a -2", got[1].Player.SleeperID, got[1].Delta)
	}

	// The threshold is a filter, not a sort: raising it past both deltas empties
	// the report rather than returning the least-bad rows.
	if n := len(TierADPGaps(players, 4)); n != 0 {
		t.Errorf("minGap 4 returned %d gaps, want 0", n)
	}
}
