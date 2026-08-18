package engine

import (
	"reflect"
	"testing"
)

// fplSlots is FPLRoster's lineup, so a change to the real constant moves these
// tests with it.
var fplSlots = FPLRoster.Slots

// The whole f1 invariant in one table: the derivation has to reproduce the three
// hardcoded six-string literals it replaced — plan.go's planPositions, sim.go's
// simPositions and the ui's own — EXACTLY, order included, or every tie-break,
// need weight and rng draw on the nfl board moves.
func TestPositionsAreTheOldLiterals(t *testing.T) {
	cases := []struct {
		name           string
		roster         Roster
		want, wantDisp []string
	}{{
		name:     "the default nfl lineup",
		roster:   DefaultRoster,
		want:     []string{"QB", "RB", "WR", "TE", "K", "DEF"}, // plan.go and sim.go carried this
		wantDisp: []string{"RB", "WR", "TE", "QB", "K", "DEF"}, // ui/board.go carried this
	}, {
		name:     "this user's real 2025 lineup, two flex slots",
		roster:   Roster{Slots: []string{"QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "FLEX", "K", "DEF"}, Bench: 6},
		want:     []string{"QB", "RB", "WR", "TE", "K", "DEF"},
		wantDisp: []string{"RB", "WR", "TE", "QB", "K", "DEF"},
	}, {
		name:     "superflex, where qb reaches the flex",
		roster:   Roster{Slots: []string{"QB", "RB", "RB", "WR", "WR", "TE", "SUPERFLEX", "K", "DEF"}, Bench: 6},
		want:     []string{"QB", "RB", "WR", "TE", "K", "DEF"},
		wantDisp: []string{"QB", "RB", "WR", "TE", "K", "DEF"},
	}, {
		name:     "the fpl squad",
		roster:   FPLRoster,
		want:     []string{"GKP", "DEF", "MID", "FWD"},
		wantDisp: []string{"GKP", "DEF", "MID", "FWD"},
	}}

	for _, c := range cases {
		s := &State{Roster: c.roster}
		if got := s.Positions(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Positions() = %v, want %v", c.name, got, c.want)
		}
		if got := s.DisplayPositions(); !reflect.DeepEqual(got, c.wantDisp) {
			t.Errorf("%s: DisplayPositions() = %v, want %v", c.name, got, c.wantDisp)
		}
	}
}

// A lineup whose flex slot is the ONLY route a position has into it used to
// derive its order out of a Go MAP, which randomises iteration — so
// `board -lineup "qb flex flex k def"` resolved planPositions' tie-break by coin
// flip, a different way on different renders of the same frame. It is legal
// today (knownSlot accepts it), it is not hypothetical, and it is why the
// derivation walks a fixed slice.
func TestPositionsAreDeterministicWhenOnlyAFlexNamesAPosition(t *testing.T) {
	s := &State{Roster: Roster{Slots: []string{"QB", "FLEX", "FLEX", "K", "DEF"}, Bench: 6}}
	want := []string{"QB", "K", "DEF", "RB", "WR", "TE"}
	for i := 0; i < 200; i++ {
		if got := s.Positions(); !reflect.DeepEqual(got, want) {
			t.Fatalf("derivation %d = %v, want %v", i, got, want)
		}
	}
}

// The quota rule, through all four doors need comes in by. A position filled to
// its quota is not cheap depth, it is a player the official app will not let you
// draft — and every consumer already knows how to hide a zero.
func TestQuotaFilledPositionNeedsNothing(t *testing.T) {
	s := New(quotaBoard(), 10, 15, 1)
	s.Roster = FPLRoster
	// Two keepers is the whole gkp quota; one forward of three is not.
	s.Rosters[1] = []string{"gkp1", "gkp2", "fwd1"}

	if n := s.Need("GKP"); n != 0 {
		t.Errorf("Need(GKP) at full quota = %v, want 0", n)
	}
	if n := s.Need("FWD"); n != NeedStarter {
		t.Errorf("Need(FWD) with two slots open = %v, want %v", n, NeedStarter)
	}
	if n := s.NeedAfter("FWD", "fwd2"); n != NeedStarter {
		t.Errorf("NeedAfter(FWD) with one slot still open = %v, want %v", n, NeedStarter)
	}
	if n := s.NeedAfter("GKP", "gkp3"); n != 0 {
		t.Errorf("NeedAfter(GKP) past the quota = %v, want 0", n)
	}
	filled, _ := s.FilledSlots(1)
	if n := s.opponentNeed("GKP", filled, 1); n != 0 {
		t.Errorf("opponentNeed(GKP) at full quota = %v, want 0", n)
	}
	// And the same roster under nfl's bench rule pays the bench weight instead,
	// which is what makes this a quota test rather than a lineup test.
	s.Roster.Quota = false
	if n := s.Need("GKP"); n != NeedBench {
		t.Errorf("Need(GKP) without the quota bit = %v, want %v (bench depth)", n, NeedBench)
	}
}

// A hold set that nobody set is the nfl one. Every production site that builds a
// Roster out of sleeper metadata writes Slots and Bench and nothing else, so the
// zero value has to keep suppressing kickers — and a sport that suppresses
// nothing has to say so.
func TestHoldDefaultsToKAndDef(t *testing.T) {
	s := New(quotaBoard(), 10, 15, 1)
	s.Roster = Roster{Slots: []string{"QB", "RB", "WR", "TE", "K", "DEF"}, Bench: 6}
	if !s.Suppressed("K") || !s.Suppressed("DEF") {
		t.Error("a roster with no Hold set stopped suppressing k/def")
	}
	if s.Suppressed("RB") {
		t.Error("a roster with no Hold set suppressed rb")
	}
	s.Roster.Hold = NoHold
	if s.Suppressed("K") || s.Suppressed("DEF") {
		t.Error("engine.NoHold still suppressed k/def")
	}
}

// quotaBoard is a squad-shaped board of obviously fake players, deeper at every
// position than what the tests actually draft.
func quotaBoard() map[string]Player {
	out := map[string]Player{}
	for _, c := range []struct {
		pos string
		n   int
	}{{"GKP", 4}, {"DEF", 8}, {"MID", 8}, {"FWD", 5}} {
		for i := 1; i <= c.n; i++ {
			id := lower(c.pos) + itoa(i)
			out[id] = Player{ID: id, Name: id, Pos: c.pos, ADP: float64(i), Sigma: 6, Value: 1000 - i}
		}
	}
	return out
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] += 'a' - 'A'
	}
	return string(b)
}

func itoa(n int) string { return string(rune('0' + n)) }

// The f1 DoD, and the only test that would have caught the whole pass being
// wrong: drive a quota squad through the machinery a frame actually calls and
// check the answers are about all four positions rather than about DEF alone.
//
// Every failure mode f1 fixes is silent — the old literals compile fine against
// an fpl board, and the only string they share with it is "DEF", which is the
// WRONG def. Left unfixed, PickChoices returns one choice, the sim refuses
// defenders for eight rounds, and the plan can only ever name a defender.
func TestTheEngineRunsOnAQuotaSquad(t *testing.T) {
	s := New(quotaBoard(), 10, 15, 1)
	s.Roster = FPLRoster
	s.Survival = SurvivalSim
	s.Rounds = 15

	seen := map[string]bool{}
	for _, c := range s.PickChoices() {
		seen[c.Pos] = true
		if c.Best.ID == "" {
			t.Errorf("choice %s named nobody", c.Pos)
		}
	}
	for _, pos := range []string{"GKP", "DEF", "MID", "FWD"} {
		if !seen[pos] {
			t.Errorf("PickChoices never considered %s — the position list is still nfl's", pos)
		}
	}

	// Round one, and nothing is held: a keeper is a legal (if unwise) pick, and
	// the tool must not be silently pretending he doesn't exist.
	for _, pos := range []string{"GKP", "DEF", "MID", "FWD"} {
		if s.Suppressed(pos) {
			t.Errorf("%s was suppressed in round 1 of an fpl draft", pos)
		}
	}

	// The sim's opponents draft from all four too. Without the derivation they
	// read need off an nfl index and every defender survives at ~1.
	if p, ok := s.BestNow("DEF"); ok {
		if surv := s.PSurviveTilted(p); surv <= 0 || surv > 1 {
			t.Errorf("survival for the best defender = %v, want a probability", surv)
		}
	}

	// And the plan runs to a real second leg rather than dead-ending on a board
	// whose only allowed position is one the nfl hold refuses.
	if plan, ok := s.BestPlan(); !ok || plan.First == "" {
		t.Errorf("BestPlan on a full fpl board = %+v, ok=%v", plan, ok)
	}
}

// The quota is a legality rule, not a preference, and the sim has to obey it:
// the official app will not let anybody draft a sixth defender, so a rollout
// that does is a rollout modelling a draft nobody is in. A zero WEIGHT is not
// enough on its own — the zero-weight guard exists to rescue a pick whose whole
// candidate pool priced at nothing, and it would rescue this one straight into
// the illegal man.
func TestTheSimNeverDraftsPastAQuota(t *testing.T) {
	s := New(quotaBoard(), 10, 15, 1)
	s.Roster = FPLRoster
	s.Survival = SurvivalSim

	// Seat 2 has its whole keeper quota and both forwards it wants; only mids
	// and defenders remain legal for them.
	core := s.newSimCore(7)
	core.reset()
	rosters := map[int][]string{2: {"gkp1", "gkp2"}}
	for i := 0; i < 200; i++ {
		idx, took, exhausted := core.oppPick(2, rosters)
		if exhausted {
			break
		}
		if !took {
			continue
		}
		if pos := core.pool[idx].pos; pos == "GKP" {
			t.Fatalf("the sim drafted a %s for a seat whose quota was already full", pos)
		}
		core.alive[idx] = false
	}
	if len(rosters[2]) == 2 {
		t.Fatal("the sim never picked at all, so the test proved nothing")
	}
}
