package main

import (
	"math"
	"testing"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
)

// Leave-one-out is the difference between a backtest and a memorization check,
// and its failure mode is invisible: a curve built from the draft it is scored
// on just makes the numbers better. That silently voids the gate that put the
// room warp on by default, so the exclusion is asserted rather than trusted to
// the comment above adp.RoomCurveOf.
//
// The three fixture drafts disagree about when the room takes its first back, so
// holding one out MOVES the curve. A fixture where they agreed would pass
// whether the exclusion worked or not.
func TestRoomCurveHoldsOutTheScoredDraft(t *testing.T) {
	drafts := map[string]adp.RoomDraft{
		"early": {"RB", "WR", "WR"}, // the first back goes at pick 1
		"mid":   {"WR", "RB", "WR"}, // ...at pick 2
		"late":  {"WR", "WR", "RB"}, // ...at pick 3
	}
	cases := []struct {
		name   string
		except string
		drafts int
		rb1    float64
	}{
		// Hand-computed: adp_room(RB, 1) is the mean pick over the drafts left in.
		{"nothing held out is the leak", "", 3, 2},     // (1+2+3)/3
		{"holding out the early one", "early", 2, 2.5}, // (2+3)/2
		{"holding out the late one", "late", 2, 1.5},   // (1+2)/2
	}
	for _, c := range cases {
		var curve adp.RoomCurve
		if c.except == "" {
			curve = adp.RoomCurveOf(drafts)
		} else {
			curve = adp.RoomCurveOf(drafts, c.except)
		}
		if curve.Drafts != c.drafts {
			t.Errorf("%s: curve carries %d drafts, want %d", c.name, curve.Drafts, c.drafts)
		}
		pick, n, ok := curve.At("RB", 1)
		if !ok || n != c.drafts || math.Abs(pick-c.rb1) > 1e-9 {
			t.Errorf("%s: adp_room(rb, 1) = %.4f over %d drafts (ok %v), want %.4f over %d",
				c.name, pick, n, ok, c.rb1, c.drafts)
		}
	}

	// And the guard that fires when a caller wires the curve up wrong.
	guards := []struct {
		name    string
		pool    map[string]adp.RoomDraft
		f       *fold
		wantErr bool
	}{
		{"a properly held-out fold passes", drafts,
			&fold{id: "early", allowed: []string{"mid", "late"},
				curve: adp.RoomCurveOf(drafts, "early")}, false},
		{"a curve built from everything is caught", drafts,
			&fold{id: "early", allowed: []string{"mid", "late"},
				curve: adp.RoomCurveOf(drafts)}, true},
		{"a fold in its own allowed list is caught", drafts,
			&fold{id: "early", allowed: []string{"early", "mid", "late"},
				curve: adp.RoomCurveOf(drafts)}, true},
		// The look-ahead half. A fold allowed to build from a draft it also
		// classified as posterior is the exact defect this check was added for.
		{"a posterior draft inside the curve is caught", drafts,
			&fold{id: "early", allowed: []string{"late", "mid"}, posterior: []string{"late"},
				curve: adp.RoomCurveOf(drafts, "early")}, true},
		{"...and is fine once -lookahead asks for it", drafts,
			&fold{id: "early", allowed: []string{"late", "mid"}, posterior: []string{"late"},
				lookahead: true, curve: adp.RoomCurveOf(drafts, "early")}, false},
		// Nothing precedes this fold, so the only correct curve is the empty one,
		// which roomWarpTag prints as "off".
		{"a fold with no prior draft has no curve", drafts,
			&fold{id: "early", posterior: []string{"mid", "late"},
				curve: adp.RoomCurveOf(drafts, "early", "mid", "late")}, false},
		{"...and building one anyway is caught",
			drafts,
			&fold{id: "early", posterior: []string{"mid", "late"},
				curve: adp.RoomCurveOf(drafts, "early")}, true},
		// A draft that never loaded cannot be in a curve, and naming one is a
		// wiring bug rather than a silent zero.
		{"an allowed draft that never loaded is caught", drafts,
			&fold{id: "early", allowed: []string{"mid", "gone"},
				curve: adp.RoomCurveOf(drafts, "early", "late")}, true},
	}
	for _, g := range guards {
		err := checkCurve(g.pool, g.f)
		if (err != nil) != g.wantErr {
			t.Errorf("%s: checkcurve returned %v, want error = %v", g.name, err, g.wantErr)
		}
	}
}

// splitPool is the fix for a look-ahead that no score could show: under
// leave-one-out alone the 2024 fold's curve was built entirely from drafts that
// ran a year later, and it is the only fold that ever preferred a capped warp.
// Ordering is by the draft's own start time, never by season — two of the real
// drafts share a season and are two days apart.
func TestSplitPoolHoldsOutTheFuture(t *testing.T) {
	const day = 24 * 60 * 60 * 1000
	drafts := map[string]adp.RoomDraft{"old": nil, "mid": nil, "new": nil, "nodate": nil}
	started := map[string]int64{"old": 100 * day, "mid": 200 * day, "new": 300 * day, "nodate": 0}

	cases := []struct {
		name      string
		id        string
		start     int64
		lookahead bool
		allowed   []string
		posterior []string
		undated   []string
	}{
		// The 2024 fold's shape: everything cached happened afterwards.
		{"the oldest draft has nothing to build from", "old", 100 * day, false,
			nil, []string{"mid", "new"}, []string{"nodate"}},
		{"the newest sees both of the others", "new", 300 * day, false,
			[]string{"mid", "old"}, nil, []string{"nodate"}},
		// Two days apart is still an order, which is why the split reads start
		// times and not seasons.
		{"the middle one sees only what preceded it", "mid", 200 * day, false,
			[]string{"old"}, []string{"new"}, []string{"nodate"}},
		// -lookahead puts the future back and nothing else changes: the fold is
		// still held out of its own curve, and an undated draft is still unusable.
		{"-lookahead restores the posterior drafts", "old", 100 * day, true,
			[]string{"mid", "new"}, []string{"mid", "new"}, []string{"nodate"}},
		// A draft the fold cannot order itself against is not prior. Guessing
		// would be the same look-ahead with a coin flip in front of it.
		{"a fold with no start time can trust nobody", "nodate", 0, false,
			nil, nil, []string{"mid", "new", "old"}},
	}
	for _, c := range cases {
		f := &fold{id: c.id, start: c.start, lookahead: c.lookahead}
		splitPool(f, drafts, started)
		if !equalIDs(f.allowed, c.allowed) {
			t.Errorf("%s: allowed %v, want %v", c.name, f.allowed, c.allowed)
		}
		if !equalIDs(f.posterior, c.posterior) {
			t.Errorf("%s: posterior %v, want %v", c.name, f.posterior, c.posterior)
		}
		if !equalIDs(f.undated, c.undated) {
			t.Errorf("%s: undated %v, want %v", c.name, f.undated, c.undated)
		}
		f.curve = curveOf(drafts, f.allowed)
		if err := checkCurve(drafts, f); err != nil {
			t.Errorf("%s: the split it produced does not pass its own check: %v", c.name, err)
		}
	}
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// -fp used to apply to every season ffc could not serve, and the csv carries no
// year of its own. So a poisoned or missing 2024 cache entry plus the natural
// `-fp ~/Downloads/FantasyPros_2025_...csv` scored the 2024 draft against 2025
// prices and printed "era matched by season only" over the top of it. The flag
// now names one season or refuses.
func TestParseFPSpecBindsABoardToOneSeason(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		year    int
		path    string
		wantErr bool
	}{
		{"nothing passed", "", 0, "", false},
		{"explicit season", "2025=/tmp/board.csv", 2025, "/tmp/board.csv", false},
		{"explicit season, spaced", " 2024 = /tmp/b.csv ", 2024, "/tmp/b.csv", false},
		// The two filenames that actually occur: the cache's own and the export's.
		{"the cache filename", "/c/pick6/fantasypros_adp_2025.csv", 2025, "/c/pick6/fantasypros_adp_2025.csv", false},
		{"the download filename", "/d/FantasyPros_2025_Overall_ADP_Rankings.csv", 2025,
			"/d/FantasyPros_2025_Overall_ADP_Rankings.csv", false},
		// A directory carrying a different year must not decide the season for a
		// file that names its own.
		{"only the basename is read", "/backups/2019/fantasypros_adp_2025.csv", 2025,
			"/backups/2019/fantasypros_adp_2025.csv", false},
		{"no year anywhere", "/d/adp.csv", 0, "", true},
		{"two years is ambiguous, not a guess", "/d/adp_2024_vs_2025.csv", 0, "", true},
		{"a season that is not one", "banana=/d/adp.csv", 0, "", true},
		{"a season with no path", "2025=", 0, "", true},
	}
	for _, c := range cases {
		year, path, err := parseFPSpec(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: parsefpspec(%q) error %v, want error = %v", c.name, c.in, err, c.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if year != c.year || path != c.path {
			t.Errorf("%s: parsefpspec(%q) = %d, %q; want %d, %q", c.name, c.in, year, path, c.year, c.path)
		}
	}

	// And the binding itself: the path reaches its own season and no other.
	if got := fpFor(2025, "/tmp/b.csv", 2024); got != "" {
		t.Errorf("a 2025 board priced the 2024 fold: fpfor returned %q, want the default path", got)
	}
	if got := fpFor(2025, "/tmp/b.csv", 2025); got != "/tmp/b.csv" {
		t.Errorf("fpfor returned %q for its own season, want /tmp/b.csv", got)
	}
}

// Two of the three scored drafts are 2025, and they are different leagues, so
// the season alone names two folds the same thing. The suffix is order of
// appearance, which is fixed, so the paper can cite a label.
func TestLabelFoldsSuffixesOnlyWhenASeasonRepeats(t *testing.T) {
	folds := []*fold{{season: "2024"}, {season: "2025"}, {season: "2025"}}
	labelFolds(folds)
	want := []string{"2024", "2025-a", "2025-b"}
	for i, f := range folds {
		if f.label != want[i] {
			t.Errorf("fold %d labeled %q, want %q", i, f.label, want[i])
		}
	}
}

// calibrate carries its own copy of the exactly-n tilt because the engine's is
// unexported and hardwired to the NextPick() horizon, which a backtest vantage
// never occupies. Two implementations of one model is the failure mode this file
// exists to prevent: if they drift, calibrate starts grading a tilt the board
// never renders and the gate it produces means nothing.
func TestLocalTiltMatchesTheEngine(t *testing.T) {
	worst, c, players := tiltSelfCheck()
	if players != 60 {
		t.Fatalf("self-check board is %d players, want 60", players)
	}
	// An exponent pinned to a clamp would make the two agree for the wrong
	// reason — both would return TiltCMax without bisecting at all.
	if c <= 1/engine.TiltCMax || c >= engine.TiltCMax {
		t.Fatalf("self-check clamped at c %g; it must exercise the bisection", c)
	}
	if worst > 1e-12 {
		t.Fatalf("local solve and engine.PSurviveTilted disagree by %g, want <= 1e-12", worst)
	}
}

// The tilt's whole definition is that expected removals equal the picks that
// really happen, so solving it and then measuring the sum is the one assertion
// that cannot pass by accident.
func TestSolveTiltHitsTheTarget(t *testing.T) {
	cases := []struct {
		name string
		ps   []float64
		n    int
		want float64 // hand-computed c, or the clamp it must reach
	}{
		// eight players at 0.5 and two picks: (1-0.5^c)*8 = 2 -> 0.5^c = 0.75
		// -> c = ln(0.75)/ln(0.5) = 0.41503749927884376.
		{"lifts an over-pessimistic board", []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}, 2, 0.4150374992},
		// four at 0.5 and three picks: (1-0.5^c)*4 = 3 -> 0.5^c = 0.25 -> c = 2.
		{"lowers an over-optimistic board", []float64{0.5, 0.5, 0.5, 0.5}, 3, 2.0},
		// sum(1-p) is already 2 at c = 1, so the correction is a no-op.
		{"leaves a calibrated board alone", []float64{0.5, 0.5, 0.5, 0.5}, 2, 1.0},
		// three players cannot supply eighteen removals however hungry the room
		// gets. this is a real board state — a thin endgame, or a toy fixture
		// driven as a 12-team draft — not defensive padding.
		{"clamps high on a thin board", []float64{0.9, 0.9, 0.9}, 18, engine.TiltCMax},
		// five players the model has already written off, one pick left to take
		// them: no exponent can push expected removals down to 1.
		{"clamps low on a written-off board", []float64{1e-8, 1e-8, 1e-8, 1e-8, 1e-8}, 1, 1 / engine.TiltCMax},
		// my own pick: nothing intervenes, so there is nothing to correct.
		{"skips when nothing intervenes", []float64{0.2, 0.3}, 0, 1.0},
	}
	for _, c := range cases {
		got := solveTilt(c.ps, c.n)
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("%s: c = %.9f, want %.9f", c.name, got, c.want)
		}
		// A clamped solve is the model admitting it cannot hit the target, so
		// only check the sum where the bisection actually ran.
		if got > 1/engine.TiltCMax && got < engine.TiltCMax && c.n > 0 {
			sum := 0.0
			for _, p := range c.ps {
				sum += 1 - math.Pow(p, got)
			}
			if math.Abs(sum-float64(c.n)) > 1e-4 {
				t.Errorf("%s: expected removals %.6f, want %d", c.name, sum, c.n)
			}
		}
	}
}

// p = 1 is my own pick and the undrafted-radar player; p = 0 is a man who is
// already gone. Both must survive any exponent, or the tilt starts inventing
// uncertainty about players nobody was uncertain about — and 0*Inf in the log
// space would surface as NaN rather than as a wrong number.
func TestSolveTiltKeepsItsFixedPointsAndOrdering(t *testing.T) {
	ps := []float64{1, 0.9, 0.6, 0.3, 0}
	c := solveTilt(ps, 2)
	tilted := make([]float64, len(ps))
	for i, p := range ps {
		tilted[i] = math.Pow(p, c)
	}
	if tilted[0] != 1 {
		t.Errorf("p = 1 moved to %.9f", tilted[0])
	}
	if tilted[len(tilted)-1] != 0 {
		t.Errorf("p = 0 moved to %.9f", tilted[len(tilted)-1])
	}
	for i := 1; i < len(tilted); i++ {
		if tilted[i] >= tilted[i-1] {
			t.Errorf("ordering broke at %d: %.9f then %.9f", i, tilted[i-1], tilted[i])
		}
	}
}

// The tilt is solved over a whole vantage's board and then read off per row. A
// segment filter is a grading choice, not a different draft — if the exponent
// were re-solved over the filtered subset, "13+ picks" rows would be corrected
// against a draft in which only long-horizon players exist. Keying by pred.idx
// is what prevents that, so assert it survives filtering.
func TestTiltedSurvivesSegmentFiltering(t *testing.T) {
	// Two vantages with deliberately opposite corrections: the first is
	// over-pessimistic (eight players at 0.5, only two picks to take them,
	// c = ln(0.75)/ln(0.5) = 0.4150375, lifting everyone to 0.75), the second
	// over-optimistic (four at 0.5, three picks, c = 2, dropping everyone to
	// 0.25). If the solve leaked into the filter these would blur together.
	var preds []pred
	for i := 0; i < 8; i++ {
		pos := "RB"
		if i%2 == 1 {
			pos = "WR"
		}
		preds = append(preds, pred{vantage: 0, from: 1, to: 3, q: 0.5, y: 1, pos: pos})
	}
	for i := 0; i < 4; i++ {
		pos := "RB"
		if i%2 == 1 {
			pos = "WR"
		}
		preds = append(preds, pred{vantage: 1, from: 9, to: 12, q: 0.5, y: 1, pos: pos})
	}
	for i := range preds {
		preds[i].idx = i
	}

	f, vts := tilted(preds, modelEngine)
	if len(vts) != 2 {
		t.Fatalf("solved %d vantages, want 2", len(vts))
	}
	if math.Abs(vts[0].c-0.4150374992) > 1e-6 {
		t.Errorf("vantage 0 c = %.9f, want 0.415037499", vts[0].c)
	}
	if math.Abs(vts[1].c-2.0) > 1e-6 {
		t.Errorf("vantage 1 c = %.9f, want 2.0", vts[1].c)
	}
	// Grading only the rbs must not move a single number: each row still reads
	// the exponent solved over its own whole board.
	var rbs []pred
	for _, p := range preds {
		if p.pos == "RB" {
			rbs = append(rbs, p)
		}
	}
	if len(rbs) != 6 {
		t.Fatalf("filtered %d rbs, want 6", len(rbs))
	}
	for _, p := range rbs {
		want := math.Pow(p.q, vts[p.vantage].c)
		if f(p) != want {
			t.Errorf("row %d graded %.12f, want %.12f", p.idx, f(p), want)
		}
	}
	// And the two vantages really did land on different answers, which is the
	// only reason the check above can fail if the keying breaks.
	if f(rbs[0]) == f(rbs[len(rbs)-1]) {
		t.Error("both vantages tilted to the same value; the fixture proves nothing")
	}
}

// A vantage's target is every pick in [from, to), including my own at `from` —
// the labels count it, because a player I take at `from` has y = 0. Getting this
// off by one would quietly mis-tilt every row in the backtest.
func TestTiltTargetsEveryPickInTheWindow(t *testing.T) {
	preds := []pred{
		{idx: 0, vantage: 0, from: 3, to: 22, q: 0.5, y: 1},
		{idx: 1, vantage: 0, from: 3, to: 22, q: 0.5, y: 1},
	}
	_, vts := tilted(preds, modelEngine)
	if vts[0].n != 19 {
		t.Errorf("target n = %d, want 19 (picks 3..21 inclusive)", vts[0].n)
	}
	if vts[0].model != 1.0 {
		t.Errorf("model expected %.4f removals, want 1.0", vts[0].model)
	}
	if vts[0].actual != 0.0 {
		t.Errorf("board really lost %.4f, want 0.0", vts[0].actual)
	}
}

// `-depth` exists to answer one question: the room warp's ordering disagrees
// between the folds, and the two folds are priced off boards of very different
// sizes (ffc 178 names, the fantasypros export 389), so "the boards are
// different" was the leading suspect. Truncating them to a common depth tests it.
//
// The cut has to be by PRICE and it has to produce one index set for every
// column. Cutting the two fantasypros columns independently would leave them
// holding different players, and the warp is indexed by rank within a position —
// a membership difference silently reranks both boards and the controlled
// experiment stops being controlled.
func TestKeepCheapestCutsByPriceAndKeepsOneIndexSet(t *testing.T) {
	// Deliberately out of price order, with an unpriced row in the middle: the
	// export's own row order is by its consensus rank, which is not the order of
	// whichever column is being priced.
	entries := []adp.Entry{
		{Name: "c", ADP: 30},
		{Name: "a", ADP: 10},
		{Name: "unpriced", ADP: 0},
		{Name: "d", ADP: 40},
		{Name: "b", ADP: 20},
	}
	cases := []struct {
		name string
		n    int
		want []string // names kept, in the board's original order
	}{
		// The default and the only regime a headline number is read from.
		{"zero keeps the whole board", 0, []string{"c", "a", "unpriced", "d", "b"}},
		{"a cut past the board keeps it too", 99, []string{"c", "a", "unpriced", "d", "b"}},
		// Cheapest by adp, but returned in board order, so the second column can
		// take the same indices without re-sorting.
		{"keeps the cheapest, in board order", 2, []string{"a", "b"}},
		{"and the next one along", 3, []string{"c", "a", "b"}},
		// An unpriced row is not "expensive", it is unpriced. It sorts last and is
		// the first thing a cut removes — otherwise a truncated board would hold
		// players the warp cannot rank and the room curve would index past them.
		{"drops unpriced rows before priced ones", 4, []string{"c", "a", "d", "b"}},
		{"and keeps them only when nothing else is left", 5,
			[]string{"c", "a", "unpriced", "d", "b"}},
	}
	for _, c := range cases {
		keep := keepCheapest(entries, c.n)
		got := pickEntries(entries, keep)
		if len(got) != len(c.want) {
			t.Errorf("%s: kept %d entries, want %d", c.name, len(got), len(c.want))
			continue
		}
		for i, e := range got {
			if e.Name != c.want[i] {
				t.Errorf("%s: kept %v, want %v", c.name, names(got), c.want)
				break
			}
		}
		// The index set must be usable on a SECOND column with the same
		// membership and order — that is the whole invariant, so assert it
		// directly rather than trusting that both callers pass the same slice.
		alt := make([]adp.Entry, len(entries))
		for i, e := range entries {
			alt[i] = adp.Entry{Name: e.Name, ADP: 100 - e.ADP} // a column that ranks the reverse
		}
		if a := pickEntries(alt, keep); len(a) != len(got) || (len(a) > 0 && a[0].Name != got[0].Name) {
			t.Errorf("%s: the same keep-set picked different players out of the alt column", c.name)
		}
	}
}

func names(entries []adp.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

// 3a's per-position gate was a sentence in a Printf — "better on both metrics and
// on every position" — emitted from a condition that only read the two OVERALL
// numbers. So the single failure the gate exists to catch, a warp that wins on
// average while wrecking one position, could not have changed a character of the
// output, and on the real backtest the sentence was already false: wr's log-loss
// moves the wrong way by +0.000032. These cases pin the three answers the check
// now computes for itself.
//
// y = 1 on every row, so a higher prediction is a better one on both metrics at
// once and each fixture's intent stays readable. q is the pre-warp model,
// qRoom the warped one, which is what modelEngine and modelRoom read.
func TestRoomPosGateComputesItsClaim(t *testing.T) {
	row := func(pos string, base, top float64) pred {
		return pred{pos: pos, y: 1, q: base, qRoom: top}
	}
	every := func(base, top float64) []pred {
		var out []pred
		for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
			out = append(out, row(pos, base, top))
		}
		return out
	}
	wrecked := every(0.90, 0.95)
	wrecked[2] = row("WR", 0.95, 0.90) // the warp made receivers visibly worse

	cases := []struct {
		name              string
		rows              []pred
		passes            bool
		both, flat, worse int
	}{
		// The clean win the ship was briefed on, which is what five of the six
		// real positions do.
		{"every position better on both", every(0.90, 0.95), true, 6, 0, 0},
		// The tripwire. Two entries because both metrics regress on that one
		// position, and one entry is enough to drop the ship verdict.
		{"one position wrecked", wrecked, false, 5, 0, 2},
		// A move smaller than the four decimals every table here prints is not a
		// win and not a loss — it is a tie, reported as one. This is the bucket
		// wr's real log-loss lands in, from the other side.
		{"moves too small to print", every(0.90, 0.900001), true, 0, 12, 0},
		// A position nobody had an adp for is absent, never a silent pass: the
		// summary must not credit the warp with four positions it never scored.
		{"positions with no rows are not scored",
			[]pred{row("QB", 0.90, 0.95), row("RB", 0.90, 0.95)}, true, 2, 0, 0},
	}

	base := namedModel{"base", modelEngine}
	top := namedModel{"top", modelRoom}
	for _, c := range cases {
		g := roomPosGate(c.rows, base, top)
		if g.passes() != c.passes {
			t.Errorf("%s: passes = %v, want %v (%s)", c.name, g.passes(), c.passes, g.line())
		}
		if len(g.both) != c.both || len(g.flat) != c.flat || len(g.worse) != c.worse {
			t.Errorf("%s: both/flat/worse = %d/%d/%d, want %d/%d/%d (%s)",
				c.name, len(g.both), len(g.flat), len(g.worse),
				c.both, c.flat, c.worse, g.line())
		}
	}
}
