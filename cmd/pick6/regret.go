package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// pick6 regret — the referee for DECISIONS, which `calibrate` cannot be.
//
// calibrate grades survival, and survival has labels: the draft either took the
// man before my turn or it didn't. A decision score has no such thing. Nobody
// recorded what would have happened if I had taken the receiver, so there is no
// row to score and no brier to compute. The room-warp cap is the scar this
// command exists because of: a decision-shaped change shipped on one fold's
// plausibility, was defended as never-worse, and was eventually retracted.
//
// So the instrument is a counterfactual replay. For each cached real draft, my
// seat is played by a POLICY while the other eleven keep their real recorded
// picks, and the thing compared is the roster each policy walks out with. It is
// not a proof — the opponents cannot react to me, and the substitution rule
// below drags the room slightly off what really happened — but it is a
// measurement over real rooms, and every policy takes the same distortion.
//
// Two things are always printed together and never averaged: U, the objective
// milestone 8 optimises, and the same lineup priced on a LINEAR exchange rate.
// If a policy wins on one and loses on the other, the value curve's convexity is
// doing the winning rather than the decisions, and the table says so.

// regretUser is the human whose seat gets replayed: sleeper `prxtani`, the same
// id replay_test.go uses. `-user` overrides it, `-slot` skips the lookup.
const regretUser = "1133491374289981440"

// regretValueScale multiplies the ValueBase·exp(−rank/ValueDecay) fallback
// before it is rounded to Player.Value's int.
//
// The curve is the repo's own and its shape is what matters — measured against
// FantasyCalc's real values it is close (rank 1 is 12x rank 100 on both) — but
// at ValueBase 250 the deep board rounds to 1, 2 and 3, and a bench full of
// integer ties would let the assignment, not the decisions, decide the score.
// Scaling is explicitly free: "values are only ever compared via differences, so
// the absolute scale doesn't matter; consistency does".
const regretValueScale = 1000

// regretTrials is how many seeds a sampling policy is averaged over. A gate
// decided by one seed is a gate decided by a coin, and the sim policies are the
// only ones with a seed at all.
const regretTrials = 5

func runRegret(args []string) error {
	fs := flagSet("regret")
	format := fs.String("format", "half-ppr", "ffc format for the era boards")
	user := fs.String("user", regretUser, "sleeper user id or username whose seat gets replayed")
	slot := fs.Int("slot", 0, "play this draft slot instead of resolving -user (all folds)")
	depth := fs.Int("depth", 0, "truncate every era board to its N cheapest players (diagnostic only)")
	rollouts := fs.Int("rollouts", 0, "override engine.PlanRollouts for the sim policies")
	bench := fs.Float64("bench", engine.BenchWeight, "U's bench weight")
	trials := fs.Int("trials", regretTrials, "seeds to average each sampling policy over")
	fpSpec := fs.String("fp", "", "`year=path` to a fantasypros csv, or a path whose filename carries the year")
	fpCol := fs.String("fp-adp", string(adp.FPAvg), "fantasypros column: avg or sleeper")
	verbose := fs.Bool("v", false, "print every counterfactual roster")
	fs.Parse(args)

	col, err := adp.ParseFPColumn(*fpCol)
	if err != nil {
		return err
	}
	fpYear, fpPath, err := parseFPSpec(*fpSpec)
	if err != nil {
		return err
	}
	if *rollouts > 0 {
		engine.PlanRollouts = *rollouts
	}
	engine.BenchWeight = *bench
	if *trials < 1 {
		*trials = 1
	}

	fmt.Println("pick6 regret — replaying my own seat with each policy at the wheel")
	fmt.Println()

	pool, hit, err := sleeper.Players()
	if err != nil {
		return fmt.Errorf("sleeper players: %w", err)
	}
	say("sleeper", hit, fmt.Sprintf("%d active fantasy players", len(pool)))
	ix := rankings.NewIndex(pool)

	roomDrafts, roomSources := adp.LoadRoomDrafts(calibrateDrafts)
	noteRoomSources(roomSources, false)
	started := map[string]int64{}
	for _, s := range roomSources {
		if s.Err == nil {
			started[s.ID] = s.Start
		}
	}
	note("objective", "U", fmt.Sprintf(
		"starters at value + %.2f x bench, unfilled slot 0 · value = %g x %g^(-rank/%g) off era adp",
		engine.BenchWeight, float64(regretValueScale), math.E, engine.ValueDecay))
	if *rollouts > 0 {
		note("rollouts", "set", fmt.Sprintf("%d futures per candidate (default 300)", *rollouts))
	}

	boards := map[int]*eraBoard{}
	var folds []*regretFold
	skipped := 0
	for _, id := range calibrateDrafts {
		f, why := loadRegretFold(ix, id, *format, fpYear, fpPath, col, *depth, boards, roomDrafts, started, *user, *slot)
		if why != "" {
			note("draft", "skipped", id+" · "+why)
			skipped++
			continue
		}
		folds = append(folds, f)
	}
	if len(folds) == 0 {
		return fmt.Errorf("nothing replayable — no draft had an era board and a seat for %s", *user)
	}
	labelRegretFolds(folds)

	results := map[string][]policyResult{}
	for _, f := range folds {
		rs := f.run(*trials, *verbose)
		regretFoldReport(f, rs)
		for _, r := range rs {
			results[r.name] = append(results[r.name], r)
		}
	}
	regretGate(folds, results)
	regretCaveats(folds, skipped)
	return nil
}

// ---- folds ----

// regretFold is one cached draft, era-priced, with my seat resolved and every
// causal input built from the drafts that PREDATE it. The rule is calibrate's
// and it is a package deal: curve, escape and demand all come off the same
// held-out list, because each of the three is a fact about rooms that had
// already drafted.
type regretFold struct {
	label, id, season, league string
	draft                     *sleeper.Draft
	feed                      []sleeper.DraftPick
	board                     *eraBoard
	seat                      int
	allowed, posterior        []string
	curve                     adp.RoomCurve
	escape                    []float64
	demand                    map[string]int

	players map[string]engine.Player // era board, valued off adp rank, room-warped
	linear  map[string]float64       // the same men on a linear exchange rate
	rank    map[string]int
	unknown int // my real picks the era board never priced
}

func loadRegretFold(ix *rankings.Index, id, format string, fpYear int, fpPath string, col adp.FPColumn,
	depth int, boards map[int]*eraBoard, roomDrafts map[string]adp.RoomDraft, started map[string]int64,
	user string, forceSlot int) (*regretFold, string) {

	d, err := sleeper.CachedDraft(id)
	if err != nil {
		return nil, strings.ToLower(err.Error())
	}
	picks, err := sleeper.CachedPicks(id)
	if err == nil {
		err = d.Validate()
	}
	if err != nil {
		return nil, d.Season + " · " + strings.ToLower(err.Error())
	}
	year, _ := strconv.Atoi(d.Season)
	b, seen := boards[year]
	if !seen {
		b = loadEraBoard(ix, format, year, fpFor(fpYear, fpPath, year), col, depth)
		boards[year] = b
	}
	if b.err != nil {
		return nil, d.Season + " · " + strings.ToLower(b.err.Error())
	}

	seat := forceSlot
	if seat == 0 {
		var ok bool
		if seat, ok = d.SlotOf(user); !ok {
			if u, uerr := sleeper.CachedUser(user); uerr == nil {
				seat, ok = d.SlotOf(u.UserID)
			}
		}
		if !ok || seat == 0 {
			return nil, d.Season + " · no seat for " + user + " in draft_order; pass -slot N"
		}
	}

	f := &regretFold{
		id: id, season: d.Season, league: d.Metadata.Name,
		draft: d, feed: picks, board: b, seat: seat,
	}
	f.allowed, f.posterior, _ = splitByStart(roomDrafts, started, id, d.StartTime)
	f.curve = curveOf(roomDrafts, f.allowed)
	if err := checkRegretCurve(roomDrafts, f); err != nil {
		return nil, err.Error()
	}
	f.escape = escapeRates(f.allowed, boards)
	// Demand is the third causal input and the easiest to leak: leagueDemand()
	// pools all three cached drafts, INCLUDING this one and everything after it,
	// and it is what mock and live hand the engine. A fold has to build its own
	// from the same held-out list the curve came from, and 2024 correctly gets
	// nothing — which sends Replacement to its lineup-shape fallback, exactly as
	// a 2024 clock would have had to.
	sub := make(map[string]adp.RoomDraft, len(f.allowed))
	for _, a := range f.allowed {
		if rd, ok := roomDrafts[a]; ok {
			sub[a] = rd
		}
	}
	if len(sub) > 0 {
		f.demand = adp.PositionDemand(sub)
	}
	f.value()
	// How many of my own real picks the era board never priced. It is counted
	// once, here, and it is not a footnote: those men score 0 through every
	// policy including "what i really did", so the actual row is carrying a
	// penalty for the board's depth rather than for its decisions. A model
	// policy cannot take an unpriced player at all — he is not on the board
	// until the pick that registers him — so it cannot be charged the same way.
	for _, pk := range picks {
		if pk.DraftSlot != seat {
			continue
		}
		if _, priced := f.players[pk.PlayerID]; !priced {
			f.unknown++
		}
	}
	return f, ""
}

// checkRegretCurve is checkCurve's assertion for this command's own fold type.
// Same two failures, same reason they are asserted in code rather than trusted
// to a comment: a curve that saw too much only ever makes the numbers better.
func checkRegretCurve(drafts map[string]adp.RoomDraft, f *regretFold) error {
	for _, id := range f.allowed {
		if id == f.id {
			return fmt.Errorf("room warp: %s is in its own curve", f.id)
		}
		if _, in := drafts[id]; !in {
			return fmt.Errorf("room warp: curve for %s names %s, which never loaded", f.id, id)
		}
		for _, post := range f.posterior {
			if id == post {
				return fmt.Errorf("room warp: curve for %s contains %s, which started after it", f.id, id)
			}
		}
	}
	if f.curve.Drafts != len(f.allowed) {
		return fmt.Errorf("room warp: curve for %s holds %d drafts against %d allowed",
			f.id, f.curve.Drafts, len(f.allowed))
	}
	return nil
}

// value turns the era board into a board the engine can rank, which it cannot
// do as it stands: an era board carries adp and sigma and NO value at all, and
// every one of Available, BestNow, VOR and Replacement would silently return
// garbage over a board of zeros.
//
// No historical FantasyCalc exists — probed, dead — and 2026 values on a 2024
// draft would be era leakage of exactly the kind this command's causality rules
// exist to prevent. So value comes off era adp rank through the repo's own
// fallback curve. It is era-consistent by construction and identical across
// policies, which is all a comparison needs.
func (f *regretFold) value() {
	board := append([]engine.Player(nil), f.board.players...)
	sort.Slice(board, func(i, j int) bool {
		if board[i].ADP != board[j].ADP {
			return board[i].ADP < board[j].ADP
		}
		return board[i].ID < board[j].ID
	})
	eff := f.curve.EffectiveADP(f.board.rows)
	f.players = make(map[string]engine.Player, len(board))
	f.linear = make(map[string]float64, len(board))
	f.rank = make(map[string]int, len(board))
	n := len(board)
	for i, p := range board {
		rank := i + 1
		p.Value = int(math.Round(regretValueScale * engine.ValueBase *
			math.Exp(-float64(rank)/engine.ValueDecay)))
		p.ADPEff = eff[p.ID]
		f.players[p.ID] = p
		f.rank[p.ID] = rank
		// The robustness check's exchange rate: linear in rank, so the k-th and
		// the (k+1)-th man are always one unit apart wherever they sit. A policy
		// that wins on the convex curve and loses here won on the curve.
		f.linear[p.ID] = float64(n - rank + 1)
	}
}

func labelRegretFolds(folds []*regretFold) {
	total := map[string]int{}
	for _, f := range folds {
		total[f.season]++
	}
	nth := map[string]int{}
	for _, f := range folds {
		nth[f.season]++
		if total[f.season] == 1 {
			f.label = f.season
			continue
		}
		f.label = fmt.Sprintf("%s-%c", f.season, 'a'+rune(nth[f.season]-1))
	}
}

// causal reports whether this fold's sim policies are running in a configuration
// a live clock could ever have been in. 2024 has nothing before it: no curve, no
// escape, no measured demand. Its sim rows are reported and never gated on, the
// same treatment v2's verdict gave it.
func (f *regretFold) causal() bool { return len(f.allowed) > 0 }

// ---- policies ----

// policy is one way to spend my seat's picks. configure sets up the state once
// per replay; pick answers with a player id at each of my vantages.
type policy struct {
	name     string
	blurb    string
	sampling bool // has a seed, so it gets averaged over trials
	setup    func(s *engine.State, seed int64, f *regretFold)
	pick     func(s *engine.State, f *regretFold, real string) string
}

func regretPolicies() []policy {
	// The engine policies differ only in how PickChoices is configured, so they
	// share one pick function: PickChoices()[0] IS the recommendation, pinned by
	// TestBestPlanIsTheTopPickChoice.
	top := func(s *engine.State, f *regretFold, real string) string {
		choices := s.PickChoices()
		if len(choices) == 0 {
			return bestAvailableID(s, f)
		}
		return choices[0].Best.ID
	}
	simSetup := func(scorer string) func(*engine.State, int64, *regretFold) {
		return func(s *engine.State, seed int64, f *regretFold) {
			s.Survival = engine.SurvivalSim
			s.Scorer = scorer
			s.SimSeed = seed
			s.OffBoard = f.escape
		}
	}
	return []policy{
		{
			name: "actual", blurb: "what i really did",
			pick: func(s *engine.State, f *regretFold, real string) string { return real },
		},
		{
			name: "best available", blurb: "the cheapest man left by era adp",
			pick: func(s *engine.State, f *regretFold, real string) string { return bestAvailableID(s, f) },
		},
		{
			name: "fill the lineup", blurb: "cheapest man who closes an open starting slot",
			pick: func(s *engine.State, f *regretFold, real string) string { return needFillID(s, f) },
		},
		{
			name: "v1 formula", blurb: "-survival=adp: the logistic, the tilt and the two-leg pair score",
			setup: func(s *engine.State, seed int64, f *regretFold) { s.Survival = engine.SurvivalADP },
			pick:  top,
		},
		{
			name: "m7 pair", blurb: "-survival=sim, the shipped two-leg pair score", sampling: true,
			setup: simSetup(engine.ScorerPair), pick: top,
		},
		{
			name: "m8 roster", blurb: "-survival=sim, the finished-roster objective", sampling: true,
			setup: simSetup(engine.ScorerRoster), pick: top,
		},
	}
}

// bestAvailableID is the era board's cheapest man left.
//
// Note what this baseline is NOT, and why there is only one of it: the spec asks
// for best-by-adp and best-by-value as two policies, and on an era board they
// are the same policy. Value here is a monotone function of adp rank, because
// there is no historical value source to disagree with adp. On the shipped 2026
// board the two genuinely differ (FantasyCalc against FFC) and the split would
// be worth having; here printing it twice would be printing one number twice.
func bestAvailableID(s *engine.State, f *regretFold) string {
	best, bestADP := "", math.Inf(1)
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		a := p.ADP
		if a <= 0 {
			a = engine.UndraftedADP
		}
		if a < bestADP || (a == bestADP && id < best) {
			best, bestADP = id, a
		}
	}
	return best
}

// needFillID is the naive human strategy the board exists to beat: take the
// cheapest man who closes a hole, and only fall back to best-available once the
// lineup is complete. K and DEF are held to the same suppression the tool obeys,
// or this policy drafts a kicker in round 2 and the comparison is a joke.
func needFillID(s *engine.State, f *regretFold) string {
	filled, _ := s.FilledSlots(s.MySlot)
	open := map[string]bool{}
	for i, want := range s.Roster.Slots {
		if filled[i] != "" {
			continue
		}
		for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
			if engine.EligibleFor(want, pos) && !s.Suppressed(pos) {
				open[pos] = true
			}
		}
	}
	best, bestADP := "", math.Inf(1)
	for id, p := range s.Players {
		if s.Taken[id] || !open[p.Pos] {
			continue
		}
		a := p.ADP
		if a <= 0 {
			a = engine.UndraftedADP
		}
		if a < bestADP || (a == bestADP && id < best) {
			best, bestADP = id, a
		}
	}
	if best == "" {
		return bestAvailableID(s, f)
	}
	return best
}

// ---- the replay ----

type policyResult struct {
	name, blurb string
	fold        string
	u, linear   float64
	// us and linears are the per-seed replays, kept rather than reduced. A
	// sampling policy's spread across seeds turned out to be several percent of
	// U while the gap the gate is about is around one — so the gate has to be a
	// PAIRED comparison with a standard error on it, and a mean alone would be a
	// coin flip wearing four significant figures. Every policy is replayed on
	// the same seed list, so us[k] and the other policy's us[k] are the same
	// world and the difference is paired.
	us, linears []float64
	subs        int
	roster      []string
	starters    []string
	trials      int
	causal      bool
}

// run replays this fold once per policy (once per seed for the sampling ones)
// and returns what each walked out with.
func (f *regretFold) run(trials int, verbose bool) []policyResult {
	var out []policyResult
	for _, p := range regretPolicies() {
		n := 1
		if p.sampling {
			n = trials
		}
		var us, lins []float64
		var last replayResult
		subs := 0
		for k := 0; k < n; k++ {
			// Seeds derive from the draft id, so a rerun replays the same
			// futures; the trial index shifts them so trial 2 is a genuinely
			// different sample rather than the same one twice.
			r := f.replay(p, simSeedOf(f.draft.DraftID)+int64(k)*0x9E3779B1)
			us = append(us, r.u)
			lins = append(lins, r.linear)
			subs += r.subs
			last = r
		}
		out = append(out, policyResult{
			name: p.name, blurb: p.blurb, fold: f.label,
			u: mean(us), linear: mean(lins), us: us, linears: lins,
			subs: subs / n, roster: last.roster, starters: last.starters,
			trials: n, causal: f.causal(),
		})
		if verbose {
			fmt.Printf("    %-16s %s\n", p.name, strings.Join(f.namesOf(last.roster), ", "))
		}
	}
	return out
}

type replayResult struct {
	u, linear float64
	subs      int
	roster    []string
	starters  []string
}

// replay is the counterfactual itself.
//
// My seat's picks are made by the policy. Every other seat takes the first of
// ITS OWN real picks that is still available — which is both the substitution
// rule ("their own next surviving real pick") and, when nothing has collided, an
// exact reproduction of the recorded draft. When a manager's own list runs out,
// they take the era board's best available. Every deviation is counted, because
// the count is how far the counterfactual drifted and that is part of the
// result rather than a footnote to it.
//
// Two leak guards, both inherited from calibrate's walk and both load-bearing:
// a drafted-but-unranked player is registered at the pick that takes him and not
// before (which handcuffs a room reaches for is information from the future),
// and no policy can ever take one, because he is not on the board until he is
// gone.
func (f *regretFold) replay(p policy, seed int64) replayResult {
	d := f.draft
	teams, rounds := d.Settings.Teams, d.Settings.Rounds

	players := make(map[string]engine.Player, len(f.players))
	for id, pl := range f.players {
		players[id] = pl
	}
	s := engine.New(players, teams, rounds, f.seat)
	s.SetRoster(engine.Roster{Slots: d.RosterSlots(), Bench: d.Settings.SlotsBench})
	s.Demand = f.demand
	if p.setup != nil {
		p.setup(s, seed, f)
	}

	sorted := append([]sleeper.DraftPick(nil), f.feed...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PickNo < sorted[j].PickNo })
	at := make(map[int]sleeper.DraftPick, len(sorted))
	queue := map[int][]sleeper.DraftPick{}
	for _, pk := range sorted {
		at[pk.PickNo] = pk
		queue[pk.DraftSlot] = append(queue[pk.DraftSlot], pk)
	}
	head := map[int]int{}

	res := replayResult{}
	for n := 1; n <= teams*rounds; n++ {
		slot := s.SlotAt(n)
		if slot == f.seat {
			real := ""
			if pk, ok := at[n]; ok {
				real = pk.PlayerID
				register(s, pk)
			}
			id := p.pick(s, f, real)
			if id == "" || s.Taken[id] {
				id = bestAvailableID(s, f)
			}
			if id == "" {
				break
			}
			s.Draft(id)
			res.roster = append(res.roster, id)
			continue
		}
		// An opponent. Walk their own remaining real picks to the first man
		// still on the board.
		q := queue[slot]
		i := head[slot]
		for i < len(q) && s.Taken[q[i].PlayerID] {
			i++
		}
		head[slot] = i
		if i >= len(q) {
			if id := bestAvailableID(s, f); id != "" {
				s.Draft(id)
				res.subs++
			}
			continue
		}
		pk := q[i]
		head[slot] = i + 1
		if pk.PickNo != n {
			res.subs++
		}
		register(s, pk)
		s.Draft(pk.PlayerID)
	}

	filled, bench := s.RosterLineup(s.Rosters[f.seat])
	res.u = s.RosterValue(s.Rosters[f.seat])
	res.linear = f.linearValue(filled, bench)
	res.starters = filled
	return res
}

// register is EnsurePlayer at the pick that takes a man, and nowhere earlier.
// A drafted player the era board never priced still has to exist — he fills his
// seat's slot when the sim computes that team's needs — but he must not sit in
// the pool at any vantage before his own pick.
func register(s *engine.State, pk sleeper.DraftPick) {
	name := strings.TrimSpace(pk.Metadata.FirstName + " " + pk.Metadata.LastName)
	s.EnsurePlayer(engine.Player{
		ID: pk.PlayerID, Name: name, Pos: pk.Metadata.Position, Team: pk.Metadata.Team,
	})
}

// linearValue is U with the exchange rate swapped: the same lineup, the same
// bench discount, priced linearly in era-adp rank instead of exponentially. The
// assignment cannot move between the two — a monotone transform of rank leaves
// the value ordering alone — so the ONLY thing that differs is what a rank is
// worth, which is exactly the confound this check is for.
func (f *regretFold) linearValue(filled, bench []string) float64 {
	total := 0.0
	for _, id := range filled {
		if id != "" {
			total += f.linear[id]
		}
	}
	for _, id := range bench {
		total += engine.BenchWeight * f.linear[id]
	}
	return total
}

func (f *regretFold) namesOf(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		p := f.players[id]
		if p.Name == "" {
			out = append(out, "("+id+")")
			continue
		}
		out = append(out, strings.ToLower(p.Name))
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

// stderr is the standard error of the mean of xs, which is the number that
// decides whether a gap between two policies is a result or a draw.
func stderr(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m, v := mean(xs), 0.0
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	v /= float64(len(xs) - 1)
	return math.Sqrt(v / float64(len(xs)))
}

// paired is b − a seed by seed. Both policies replay the same seed list, so the
// same futures drive both, and the difference between two runs of one world
// carries none of the variance the two worlds share. It is the same reason the
// engine's own rollouts use common random numbers, applied one level up.
func paired(a, b []float64) []float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = b[i] - a[i]
	}
	return out
}

// ---- the printout ----

func regretFoldReport(f *regretFold, rs []policyResult) {
	fmt.Printf("\nfold %s — %s · %s\n", f.label, f.id, strings.ToLower(f.league))
	note("draft", "replayed", fmt.Sprintf("%s · %d teams · %d rounds · %d picks · my seat %d",
		f.season, f.draft.Settings.Teams, f.draft.Settings.Rounds, len(f.feed), f.seat))
	note("board", f.board.source, fmt.Sprintf("%d players priced, valued off adp rank", len(f.board.players)))
	if f.causal() {
		note("priors", "causal", fmt.Sprintf("curve, escape and demand from %d draft(s) that started earlier",
			len(f.allowed)))
	} else {
		note("priors", "none", "nothing in the cache predates this draft: no curve, no escape, "+
			"no measured demand — a configuration a live clock is never in, so this fold reports and does not gate")
	}
	if f.unknown > 0 {
		mine := f.draft.Settings.Rounds
		note("my picks", "unpriced", fmt.Sprintf(
			"%d of %d men i really took were never on this era board — they score 0 in every row, "+
				"the actual one included", f.unknown, mine))
	}
	if f.board.truncatedFrom > 0 {
		note("depth", "truncated", fmt.Sprintf("%d of %d players — diagnostic only, not this fold's result",
			len(f.board.players), f.board.truncatedFrom))
	}

	base := 0.0
	for _, r := range rs {
		if r.name == "actual" {
			base = r.u
		}
	}
	fmt.Println()
	fmt.Printf("  %-16s %10s %9s %12s %7s  %s\n", "policy", "U", "vs actual", "linear rank", "subs", "")
	for _, r := range rs {
		delta := ""
		if base != 0 && r.name != "actual" {
			delta = fmt.Sprintf("%+.1f%%", 100*(r.u-base)/base)
		}
		trial := ""
		if r.trials > 1 {
			trial = fmt.Sprintf("  ±%.0f se over %d seeds", stderr(r.us), r.trials)
		}
		fmt.Printf("  %-16s %10.0f %9s %12.0f %7d  %s%s\n",
			r.name, r.u, delta, r.linear, r.subs, r.blurb, trial)
	}
}

// regretGate is the milestone-8 verdict, computed rather than asserted — the
// same discipline calibrate's roomGate and simGate follow, and for the same
// reason: a verdict typed into a print statement is a verdict that stops being
// true the first time the numbers move.
func regretGate(folds []*regretFold, results map[string][]policyResult) {
	fmt.Println("\ngate — the roster scorer against the one it replaces")
	byFold := map[string]map[string]policyResult{}
	for name, rs := range results {
		for _, r := range rs {
			if byFold[r.fold] == nil {
				byFold[r.fold] = map[string]policyResult{}
			}
			byFold[r.fold][name] = r
		}
	}
	fmt.Println("  paired: every seed replays both scorers through the same futures, so the")
	fmt.Println("  difference is per world and carries none of the variance the two worlds share.")
	fmt.Println()
	fmt.Printf("  %-8s %-9s %12s %12s %10s %8s   %s\n",
		"fold", "priors", "m7 U", "m8 U", "m8 - m7", "se", "linear m8 - m7")
	causalWins, causalFolds, linearAgrees, decisive := 0, 0, 0, 0
	for _, f := range folds {
		m := byFold[f.label]
		m7, m8 := m["m7 pair"], m["m8 roster"]
		du, dl := paired(m7.us, m8.us), paired(m7.linears, m8.linears)
		d, se := mean(du), stderr(du)
		tag := "reported"
		if f.causal() {
			tag = "causal"
			causalFolds++
			if d > 0 {
				causalWins++
				if d > 2*se {
					decisive++
				}
			}
			if (d > 0) == (mean(dl) > 0) {
				linearAgrees++
			}
		}
		fmt.Printf("  %-8s %-9s %12.0f %12.0f %+10.0f %8.0f   %+.0f\n",
			f.label, tag, m7.u, m8.u, d, se, mean(dl))
	}
	fmt.Println()
	switch {
	case causalFolds == 0:
		fmt.Println("  no causal fold: nothing here can decide anything. the gate is not met and")
		fmt.Println("  cannot be met until a cached draft has another cached draft before it.")
	case causalWins == causalFolds:
		fmt.Printf("  the roster scorer beats the pair scorer on U on all %d causal fold(s). the gate is met.\n",
			causalFolds)
		if decisive < causalWins {
			fmt.Printf("  %d of those %d wins is inside two standard errors, which is a lead and not a\n",
				causalWins-decisive, causalWins)
			fmt.Println("  proof. -trials buys resolution; say which number you ran before quoting one.")
		}
	default:
		fmt.Printf("  the roster scorer wins %d of %d causal fold(s) on U. the gate asks for all of them,\n",
			causalWins, causalFolds)
		fmt.Println("  so it is NOT met, and the honest move is to leave the pair scorer shipping.")
	}
	if causalFolds > 0 && linearAgrees < causalFolds {
		fmt.Printf("  the linear exchange rate disagrees on %d of %d causal fold(s): on those, the value\n",
			causalFolds-linearAgrees, causalFolds)
		fmt.Println("  curve's convexity is doing the winning rather than the decisions. read it that way.")
	}
}

// rose is calibrate's verdict() for a metric where HIGHER is better, at the
// resolution actually printed — a win nobody can see in the table is not a win.
// Its twin next door labels a metric where lower is better (brier, log-loss);
// U is a roster's worth, and more of it is the point.
func rose(base, other float64) string {
	switch {
	case math.Round(other) > math.Round(base):
		return "better"
	case math.Round(other) < math.Round(base):
		return "worse"
	default:
		return "tied"
	}
}

func regretCaveats(folds []*regretFold, skipped int) {
	fmt.Println("\ncaveats — a number without its caveat is a lie")
	fmt.Println("  U is also the quantity the m8 scorer optimises. that is not circular but it is")
	fmt.Println("  not free either: what is being tested is whether optimising U against the SIM")
	fmt.Println("  transfers to real rooms, and the opponents here are real. a scorer that wins its")
	fmt.Println("  own objective against a simulator it also owns would prove nothing; these picks")
	fmt.Println("  are the ones twelve humans actually made.")
	fmt.Println("  the opponents cannot react. they take their own recorded picks in their own order,")
	fmt.Println("  so a policy that hoards a position pays no price for it in this harness.")
	fmt.Println("  value here is a monotone function of era adp rank, because no historical value")
	fmt.Println("  source exists to disagree with adp. so 'best available' is very nearly a greedy")
	fmt.Println("  optimum of the exchange rate itself, and any policy only beats it through LINEUP")
	fmt.Println("  STRUCTURE — filling the slots it leaves empty. read a win over it as that claim")
	fmt.Println("  and not as a claim about picking better players.")
	fmt.Println("  subs count how far each counterfactual drifted: every one is a manager taking a")
	fmt.Println("  later pick of their own early because mine took their man first.")
	total := 0
	for _, f := range folds {
		total += f.unknown
	}
	if total > 0 {
		fmt.Printf("  THE ACTUAL ROW IS NOT A FAIR BASELINE and the gate does not use it: %d of my real\n", total)
		fmt.Println("  picks across the folds were handcuffs, rookies and kickers the era board never")
		fmt.Println("  priced, and an unpriced man is worth 0 to U. a model policy cannot take one — he")
		fmt.Println("  is not on the board until the pick that registers him — so the gap between the")
		fmt.Println("  actual row and everything under it is mostly board depth, not decisions.")
	}
	fmt.Println("  never compare a number across folds. different rooms, different board depths,")
	fmt.Println("  different round counts; each fold is only ever its own policies' scoreboard.")
	if skipped > 0 {
		fmt.Printf("  %d draft(s) skipped.\n", skipped)
	}
}
