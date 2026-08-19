package engine

import (
	"math"
	"sort"
)

// Milestone 8: the plan runs to the end, and the score is the team it ends with.
//
// Milestone 7 pointed the rollouts at my second pick and scored the pair in the
// units PickChoices had always used — vor × need, twice. Those units were
// themselves a hand-built approximation of one quantity: what does this pick do
// to the team I walk out with? The need steps, the replacement discount,
// EndgameSlack, mustFill and the two-leg horizon were all standing in for it.
//
// So the window now runs from my next pick to my LAST one. Leg one takes the
// candidate; the opponents draft exactly as the survival rollouts already model
// them (same simCore, so the two cannot drift); every later leg of mine is the
// engine's own greedy rule over whoever is really left; and at the end of each
// future the FINISHED ROSTER is scored with U (roster.go). score(P) is the mean.
//
// What dissolves, and it dissolves rather than resolving:
//   - NeedFlex as a price. The slot assignment computes what a flex is worth.
//   - EndgameSlack and mustFill-as-score-input. U prices the hole at the value
//     of the man who would have stood in it, which is what they approximated.
//   - The replacement discount. The later legs actually fill the positions R was
//     a stand-in for filling. R survives inside the leg POLICY, where a
//     heuristic belongs.
//   - The plan-depth constant and the third-leg promotion question (milestone 7's
//     PlanDepth, since deleted). There is no depth to pick.
//
// It changes the DECISION score and nothing else. Survival, urgency, tier holds
// and the banners keep reading survivalAt; if a change in this file moves a
// number `pick6 calibrate` scores, something has been wired backwards — the
// plan consumes the survival machinery and must never feed back into it.

// planCand is one first-leg candidate as the conditioned rollout needs it:
// PickChoices' own candidate, plus whether leg one closes an open starting slot
// (which is what decides whether a later leg is forced to close one).
type planCand struct {
	pos    string
	best   Player
	need   float64
	fills1 int
}

// PlanLeg is one of my own picks as the futures saw it land: the modal position
// and the modal band, plus how often. Leg one is the candidate himself and is
// certain by construction — the score is conditional on getting him.
type PlanLeg struct {
	Pick     int    // the overall pick this leg spends
	Pos      string // the modal position; "" when no future had a legal one
	Tier     int    // the modal tier at that position; 0 means untiered
	PlayerID string // the modal man, when one recurs at all
	// Odds is the fraction of ALL futures that landed Pos at Tier or better —
	// inclusive, because landing a better band is good news, not a miss.
	Odds float64
	// Share is the fraction that landed exactly PlayerID. The copy names him
	// above a threshold and speaks in tiers below it.
	Share float64
}

// SlotOutlook is one starting slot that is open TODAY, as the futures fill it.
// This is what "your team from here" reads: the modal filler, when he arrives,
// and how sure any of it is.
type SlotOutlook struct {
	Slot     string  // the lineup slot name: "RB", "FLEX", "K"...
	Index    int     // its index in Roster.Slots, so two RB rows stay distinct
	PlayerID string  // the modal man who ends up in it
	Share    float64 // ...and how often
	ElseID   string  // the runner-up, when there is a second real mode
	Tier     int     // his modal tier
	Odds     float64 // filled at that tier or better, inclusive
	Pick     int     // the pick he modally arrives at; 0 if he was already mine
	Filled   float64 // how often the slot ends filled at all
}

// planResult is what the conditioned rollouts say about spending my next pick
// on one candidate.
type planResult struct {
	// Score is the mean, over futures, of U(finished roster) — the milestone-8
	// objective. It is a LEVEL (a whole team's worth of value), not a
	// difference, which is why nothing renders it and why it may only ever be
	// compared against another candidate scored on the same futures.
	Score float64

	// Pair is milestone 7's score for everything after leg one: the mean of the
	// policy legs' own (v − R)⁺ × need. It rides along because it is free —
	// the policy computes that weight to make its choice — and because
	// ScorerPair is what `pick6 regret` grades the new objective against.
	Pair float64

	// Legs and Outlook are the display's half of the same rollouts: what the
	// plan does, and what the team becomes. Both are summaries of the futures
	// that produced Score, so the pane can never contradict the ranking.
	Legs    []PlanLeg
	Outlook []SlotOutlook

	// Closed is the modal number of today's open starting slots that end
	// filled. It is Fills' input — see PickChoices, where it is the insurance
	// against a zero-valued starter U cannot see.
	Closed int

	// futures is each future's finished roster, kept only so two candidates
	// scored on the SAME futures can be diffed into a consequence the verdict
	// can name ("taking rb instead costs you mcbride"). Unexported: it is an
	// artifact of the estimator, not a result.
	futures [][]string
}

// planTable caches one vantage's conditioned rollouts. The argument simFor's
// cache makes, twice as loudly: the seed derives from the vantage, so every
// candidate and every re-render at one pick must read the SAME futures or the
// ranking jiggles under a keypress — and at PlanRollouts futures per candidate
// over the whole rest of the draft, a render that recomputed once for the
// banner, once for the rows and once for the plan line would pay three times
// over for byte-identical numbers. Every mutator nils it, exactly like s.sim.
type planTable struct {
	pickNo int
	order  []string // candidate ids in PickChoices' own order, for the diff
	res    map[string]planResult
}

// covers guards the one thing pickNo alone cannot: a caller asking about a
// candidate this table never scored. It cannot happen today (the candidate list
// is a pure function of the vantage), which is precisely why it should fail
// loudly-by-recompute rather than silently return a zero score if it ever does.
func (t *planTable) covers(cands []planCand) bool {
	for _, c := range cands {
		if _, ok := t.res[c.best.ID]; !ok {
			return false
		}
	}
	return true
}

// planSeed is the one seed every candidate at this vantage is evaluated on.
//
// This is common random numbers, and it is not optional. Two candidates scored
// on independent samples differ by their choice PLUS sampling noise, and the
// gaps that decide these rankings are small against a ~30k roster value. The
// board would reorder itself between renders on nothing. On one seed the draws
// are identical and the candidates diverge only where their own removals force
// the opponents' hands, so the difference between two scores is the difference
// the choice makes — which is also what makes the consequence clause honest,
// since future m of one candidate is the same world as future m of the other.
//
// A different mixing constant from simSeed's, deliberately: the plan's futures
// and the survival table's answer different questions over different windows,
// and correlating them would hide a leak between the two rather than expose it.
func (s *State) planSeed() int64 {
	return int64(uint64(s.SimSeed) ^ uint64(s.PickNo)*0xD1B54A32D192ED03)
}

// conditionedLegs runs the conditioned rollouts for every candidate on one
// shared seed and returns each one's summary, keyed by the candidate's man.
func (s *State) conditionedLegs(cands []planCand, mine []int, mustFill int) map[string]planResult {
	if t := s.plan; t != nil && t.pickNo == s.PickNo && t.covers(cands) {
		return t.res
	}
	// The leg policy may only take a position that passed the SAME membership
	// test the first leg's candidates passed. Not a formality: the v1 formula
	// got this for free by maxing over `cands`, and the rollouts had to be told.
	//
	// The gap is the endgame guard. Membership is decided by Need, which
	// multiplies bench weight by endgameSlack and takes it to ZERO at R == U;
	// the policy prices need with needSlots, which carries no slack
	// (deliberately — see NeedAfter, where charging it made the pair score
	// depend on the order of two legs ending at the same roster). So without
	// this the policy would happily "actually do" what PickChoices deleted from
	// the board. Found by review on the scripted mock at 13.09 of seed 5 slot 9:
	// the plan line read "rb at 13.09 → wr at 14.04" directly above "every
	// remaining pick must fill a starter", with wr not a rendered row at all.
	allowed := make(map[string]bool, len(cands))
	for _, c := range cands {
		allowed[c.pos] = true
	}
	// One pool and one static preference board, built once and shared: the
	// board is the same board for every candidate, and vor and replacement are
	// static for the whole draft. Only the random stream and the cursors are
	// restarted per candidate.
	core := s.newSimCore(s.planSeed())
	// Only the full-horizon objective gets this policy's two milestone-8 rules
	// (see planPolicy.roster). Both were behaviour changes to the SHIPPED pair
	// score before this argument existed, and one of them was measurable: found
	// by review at a round-13 vantage whose leg two lands in round 14, where
	// milestone 7 forbade k/def and the new rule offered them.
	pol := s.newPlanPolicy(core, allowed, s.Scorer == ScorerRoster)
	res := make(map[string]planResult, len(cands))
	order := make([]string, 0, len(cands))
	for _, c := range cands {
		core.reseed(s.planSeed())
		res[c.best.ID] = s.planRollout(core, pol, c, mine, mustFill)
		order = append(order, c.best.ID)
	}
	s.plan = &planTable{pickNo: s.PickNo, order: order, res: res}
	return res
}

// planRollout plays the window [PickNo, my last pick] PlanRollouts times with my
// own picks un-skipped, and summarises the teams it ended with.
//
// The candidate comes off the board at the START of the rollout rather than at
// my leg-one pick, and that is the conditioning this score is defined by:
// score(P) is what my finished team is worth GIVEN I get P. In the futures where
// an opponent would have taken him first, the premise of the plan has already
// failed — the roster being scored would contain a man I do not have — so those
// futures are excluded by construction, and the opponents who did not take him
// spent their picks on somebody else, which is exactly what removing him first
// makes them do. It is also the semantics the v1 formula always had. Milestone 8
// does not re-open "will I even get him", which the survival column and the
// → fallback clauses answer directly and in their own units.
func (s *State) planRollout(core *simCore, pol *planPolicy, c planCand, mine []int, mustFill int) planResult {
	legs := len(mine)
	last := mine[legs-1]

	candIdx, known := core.index[c.best.ID]
	if !known {
		return planResult{} // taken since the candidate list was built; nothing to plan
	}

	// legAt[q - PickNo] is which of my legs picks at q, 0 for an opponent's.
	legAt := make([]int, last-s.PickNo+1)
	for i, q := range mine {
		legAt[q-s.PickNo] = i + 1
	}
	windowSlots := map[int]bool{}
	opp := 0
	for q := s.PickNo; q <= last; q++ {
		if legAt[q-s.PickNo] == 0 {
			windowSlots[s.SlotAt(q)] = true
			opp++
		}
	}

	n := PlanRollouts
	if opp == 0 {
		// Nothing intervenes anywhere in the window — my last pick, or the
		// final turn — so every future is the same future. One rollout is the
		// whole distribution, the score is exact, and the odds read an honest
		// 100% rather than 99.8% of a sample of a deterministic thing.
		n = 1
	}

	base := s.Rosters[s.MySlot]
	vantage, _ := s.RosterLineup(base)
	open := make([]int, 0, len(vantage))
	for i, id := range vantage {
		if id == "" {
			open = append(open, i)
		}
	}

	tally := newPlanTally(legs, open)
	futures := make([][]string, 0, n)
	scoreSum, pairSum := 0.0, 0.0

	for m := 0; m < n; m++ {
		core.reset()
		pol.reset()
		core.alive[candIdx] = false // the decision this whole score is about
		rosters := s.copyRosters(windowSlots)

		ids := make([]string, 0, len(base)+legs)
		ids = append(ids, base...)
		ids = append(ids, c.best.ID)
		pickAt := map[string]int{c.best.ID: mine[0]}
		done, pair := c.fills1, 0.0

		for q := s.PickNo; q <= last; q++ {
			leg := legAt[q-s.PickNo]
			if leg == 0 {
				if _, _, exhausted := core.oppPick(q, rosters); exhausted {
					break // board holds nobody we know; the picks left take nobody
				}
				continue
			}
			if leg == 1 {
				continue // already off the board, see the conditioning note above
			}
			idx, w, fills, ok := pol.take(s, ids, s.Round(q), mustFillAt(leg, legs, mustFill, done))
			if !ok {
				continue // nothing legal left for this leg: it is worth nothing
			}
			pair += w
			p := s.Players[core.pool[idx].id]
			ids = append(ids, p.ID)
			pickAt[p.ID] = q
			if fills {
				done++
			}
			tally.leg(leg, p)
		}

		filled, bench := s.RosterLineup(ids)
		scoreSum += rosterValueOf(s, filled, bench)
		pairSum += pair
		tally.finish(s, filled, pickAt)
		futures = append(futures, ids)
	}

	res := planResult{Score: scoreSum / float64(n), Pair: pairSum / float64(n), futures: futures}
	res.Legs, res.Outlook, res.Closed = tally.summarise(s, mine, n)
	return res
}

// mustFillAt reports whether my leg k of legs has to close an open starting
// slot: with mustFill of them required overall and done already closed, the
// legs after this one cannot absorb the shortfall.
func mustFillAt(k, legs, mustFill, done int) bool {
	return mustFill-done >= legs-k+1
}

// ---- the leg policy ----

// How many positions a lineup can name is core.pos's length, sized at runtime
// off the roster rather than fixed at six. The pool's "unknown position" bucket
// sits past it and is never a leg: nothing in Roster.Slots can be filled by a
// position no lineup names, so taking one would be a pick the tool would never
// recommend.

// planPolicy is what the engine would actually do with one of my picks: prefer
// the board by VOR × need, given the roster my earlier legs leave behind. It is
// the spec's "the engine's own greedy choice, i.e. what I would actually do
// there", deliberately written in the same vocabulary the old score was rather
// than as a second opinion.
//
// Milestone 8 turned it from a sort into a walk, and the identity is provable
// rather than hoped for. Within a position, need is a constant and replacement
// is static, so the weight (v − R)⁺ × need is monotone in value: the best ALIVE
// man at a position is a fixed list walked past the dead. So instead of sorting
// the whole board once per leg — which is what made a three-leg horizon cost
// 115ms — the policy keeps one static list per position with a per-rollout
// cursor and compares at most six heads. The winner of the six-way compare is
// the winner of the sort, ties included: the tie-break is the pool index, which
// is what the old stable sort over a price-ordered slice resolved to.
//
// Three membership rules, all inherited from milestone 7:
//
//   - A SUPPRESSED position is dropped outright, because an argmax over a board
//     of zeros lands on a round-four kicker and the plan line recommends one.
//     Under the full-horizon objective — and ONLY there, since a two-pick window
//     cannot reach the rounds in question — that suppression is evaluated at the
//     LEG'S OWN ROUND rather than at the vantage's, which is the only reading
//     that survives a horizon long enough to reach round 14:
//     "what the tool would never recommend" is a claim about round 4, and at
//     round 14 the tool recommends a kicker. Under vantage-round suppression a
//     rollout starting in round 1 would never fill K or DEF at all, and U would
//     price both starting slots at zero in every future — the objective blind to
//     exactly the endgame it exists to price.
//   - A position the caller did not put in `allowed` — PickChoices' own
//     candidate set — is dropped, so the policy cannot "actually do" what the
//     board deleted. See conditionedLegs.
//   - Feasibility is a PREFERENCE, not a filter, which is the same shape
//     PickChoices' Fills has always had. Written as a filter it produced a real
//     dead end on the scripted mock at 12.09: with only k and def unfilled and
//     three picks left, mustFill demanded a starter, our own suppression forbade
//     the only positions that could supply one, and the policy had nothing legal
//     to take at all — the plan line rendered a hole where the position goes.
//     Sorting fillers ahead of non-fillers is identical whenever a filler is
//     alive and degrades to best-available when none is.
type planPolicy struct {
	core *simCore
	// rank[p] is p's pool indices, best first by ((v − R)⁺ desc, pool index
	// asc). Static for the whole draft; only the cursor moves.
	rank   [][]int
	vor    []float64 // (v − R(pos))⁺ per pool index
	cursor []int
	base   []bool // in PickChoices' candidate set today
	held   []bool // subject to OUR early-draft hold, asked per leg round
	late   []bool // ...and excluded from the candidate set only by it

	// roster is true when the milestone-8 objective is the thing being scored.
	// Two of this policy's rules exist only to serve it and would be behaviour
	// changes to the shipped pair score otherwise — the leg-round suppression
	// above, and pricing a bench body at what U will actually pay for him. A
	// two-pick window neither reaches the rounds a kicker belongs in nor scores
	// a finished roster, so under ScorerPair both are simply off and the leg is
	// milestone 7's leg.
	roster bool
}

func (s *State) newPlanPolicy(core *simCore, allowed map[string]bool, roster bool) *planPolicy {
	n := len(core.pos)
	pol := &planPolicy{
		core: core, roster: roster,
		vor:    make([]float64, len(core.pool)),
		rank:   make([][]int, n),
		cursor: make([]int, n),
		base:   make([]bool, n),
		held:   make([]bool, n),
		late:   make([]bool, n),
	}
	repl := make([]float64, n)
	for pi, pos := range core.pos {
		repl[pi] = s.Replacement(pos)
		pol.base[pi] = allowed[pos]
		// The same early-draft hold State.Suppressed applies, read off the same
		// configurable set rather than off the literal {K, DEF} this line used to
		// carry. Left hardcoded, every fpl plan leg would refuse all 184
		// defenders and 60 keepers until the last three rounds — the plan line
		// and "your team from here" silent on half the squad for a whole draft.
		pol.held[pi] = s.Roster.holds(pos)
		pol.late[pi] = roster && pol.held[pi] && !allowed[pos] && s.Suppressed(pos)
	}
	for i, cand := range core.pool {
		pi := cand.posIdx
		if pi >= n {
			continue
		}
		if v := float64(s.Players[cand.id].Value) - repl[pi]; v > 0 {
			pol.vor[i] = v
		}
		pol.rank[pi] = append(pol.rank[pi], i)
	}
	// Stable over a price-ordered pool, so equal weights — everybody at or
	// below replacement, which is most of a late board — keep the pool's own
	// price order instead of whatever the sort left behind.
	for pi := range pol.rank {
		list := pol.rank[pi]
		sort.SliceStable(list, func(a, b int) bool { return pol.vor[list[a]] > pol.vor[list[b]] })
	}
	return pol
}

// reset puts every cursor back to the top of its position for a fresh rollout.
func (pol *planPolicy) reset() {
	for i := range pol.cursor {
		pol.cursor[i] = 0
	}
}

// take spends one of my legs: the best man the policy is willing to take at the
// round it happens in, removed from the board. ok is false when nothing legal is
// left, which is a live endgame state and not a bug.
func (pol *planPolicy) take(s *State, ids []string, round int, mustFill bool) (idx int, weight float64, fills, ok bool) {
	filled := pol.core.fill(ids)
	best, bestW, bestFill := -1, 0.0, false
	for pi := range pol.core.pos {
		if !pol.allows(s, pi, round) {
			continue
		}
		i := pol.next(pi)
		if i < 0 {
			continue
		}
		pos := pol.core.pos[pi]
		need := s.needSlots(pos, ids, filled)
		if need == 0 && s.Roster.capped() {
			// Under a hard quota a filled position is not merely worthless, it
			// is ILLEGAL — the official app will not let anyone draft a sixth
			// defender. Without this the walk still takes him whenever he is the
			// first allowed position with a live man, since a zero weight only
			// loses ties and the opening `best < 0` loses to nothing at all.
			continue
		}
		f := need > NeedBench
		if !f && pol.roster {
			// A bench body is worth what U will pay for him, which for a
			// backup quarterback in a 1QB league — or a second kicker, or a
			// second defense — is nothing. Without this the policy spends
			// simulated picks on men the objective then scores at zero, which
			// is the rollout "actually doing" something the tool would never
			// recommend, in the one place that rule had not been applied.
			need = s.benchWeight(pos)
		}
		w := pol.vor[i] * need
		if best < 0 || planBeats(mustFill, f, w, i, bestFill, bestW, best) {
			best, bestW, bestFill = i, w, f
		}
	}
	if best < 0 {
		return 0, 0, false, false
	}
	pol.core.alive[best] = false
	return best, bestW, bestFill, true
}

// planBeats is the old stable sort's comparator, one pair at a time:
// feasibility when it is demanded, then weight, then the pool's price order.
func planBeats(mustFill, f bool, w float64, i int, bestFill bool, bestW float64, best int) bool {
	if mustFill && f != bestFill {
		return f
	}
	if w != bestW {
		return w > bestW
	}
	return i < best
}

// allows is the membership test at one leg's round. See planPolicy's comment
// for why suppression is asked at the leg's round and not at the vantage's.
func (pol *planPolicy) allows(s *State, pi, round int) bool {
	if pol.held[pi] {
		// Our own suppression, and the round it is asked about is the LEG'S,
		// which is the only reading that survives a horizon long enough to
		// reach round 14. A caller handing in an unrestricted candidate set
		// still cannot get a round-four kicker out of this.
		if s.Rounds-round+1 > KDefLastRounds {
			return false
		}
		return pol.base[pi] || pol.late[pi]
	}
	return pol.base[pi]
}

// next is the best still-alive man at a position, or -1. The cursor only ever
// moves forward, which is sound because a rollout only ever kills players.
func (pol *planPolicy) next(pi int) int {
	list := pol.rank[pi]
	c := pol.cursor[pi]
	for c < len(list) && !pol.core.alive[list[c]] {
		c++
	}
	pol.cursor[pi] = c
	if c == len(list) {
		return -1
	}
	return list[c]
}

// ---- summarising the futures ----

// planTally counts what the futures did, so the pane's claims and the score are
// summaries of one set of rollouts rather than two.
type planTally struct {
	legPos    []map[string]int
	legTier   []map[string]map[int]int
	legPlayer []map[string]int
	open      []int
	slotBy    map[int]map[string]int
	slotTier  map[int]map[string]map[int]int
	slotPick  map[int]map[string]map[int]int
	slotFull  map[int]int
	closed    map[int]int
	// anySlot and anyPick are the same tallies pooled ACROSS the open slots.
	// Two same-position slots are interchangeable — the value-ordered fill puts
	// a man in wr1 when his partner is worse and wr2 when better — so a
	// per-slot mode splits one claim in half and reports him at 50% twice.
	// Pooled, the claim is the one worth making: he is in your lineup, this
	// often, arriving here.
	anySlot map[string]int
	anyPick map[string]map[int]int
}

func newPlanTally(legs int, open []int) *planTally {
	t := &planTally{
		legPos:    make([]map[string]int, legs+1),
		legTier:   make([]map[string]map[int]int, legs+1),
		legPlayer: make([]map[string]int, legs+1),
		open:      open,
		slotBy:    map[int]map[string]int{},
		slotTier:  map[int]map[string]map[int]int{},
		slotPick:  map[int]map[string]map[int]int{},
		slotFull:  map[int]int{},
		closed:    map[int]int{},
		anySlot:   map[string]int{},
		anyPick:   map[string]map[int]int{},
	}
	for i := range t.legPos {
		t.legPos[i] = map[string]int{}
		t.legTier[i] = map[string]map[int]int{}
		t.legPlayer[i] = map[string]int{}
	}
	for _, i := range open {
		t.slotBy[i] = map[string]int{}
		t.slotTier[i] = map[string]map[int]int{}
		t.slotPick[i] = map[string]map[int]int{}
	}
	return t
}

// leg records what one of my picks took in one future.
func (t *planTally) leg(k int, p Player) {
	t.legPos[k][p.Pos]++
	if t.legTier[k][p.Pos] == nil {
		t.legTier[k][p.Pos] = map[int]int{}
	}
	t.legTier[k][p.Pos][p.Tier]++
	t.legPlayer[k][p.ID]++
}

// finish records one future's ending lineup against the slots that were open at
// the vantage.
func (t *planTally) finish(s *State, filled []string, pickAt map[string]int) {
	closed := 0
	for _, i := range t.open {
		id := filled[i]
		t.slotBy[i][id]++
		if id == "" {
			continue
		}
		closed++
		t.slotFull[i]++
		// Keyed by POSITION as well as slot, because a flex slot is filled by
		// different positions in different futures and a tier number means
		// nothing across them. Without this a flex row could pair a modal RB
		// with a modal tier drawn from the receivers who filled it elsewhere —
		// "a tier-3 rb" naming a band no back ever landed in. Found by review.
		p := s.Players[id]
		if t.slotTier[i][p.Pos] == nil {
			t.slotTier[i][p.Pos] = map[int]int{}
		}
		t.slotTier[i][p.Pos][p.Tier]++
		if t.slotPick[i][id] == nil {
			t.slotPick[i][id] = map[int]int{}
		}
		t.slotPick[i][id][pickAt[id]]++
		t.anySlot[id]++
		if t.anyPick[id] == nil {
			t.anyPick[id] = map[int]int{}
		}
		t.anyPick[id][pickAt[id]]++
	}
	t.closed[closed]++
}

// summarise turns the counts into the claims the pane makes.
func (t *planTally) summarise(s *State, mine []int, n int) ([]PlanLeg, []SlotOutlook, int) {
	legs := make([]PlanLeg, 0, len(mine))
	for k := 1; k <= len(mine); k++ {
		leg := PlanLeg{Pick: mine[k-1]}
		if k == 1 {
			// Leg one is the decision, not a draw: it is the candidate, in
			// every future, by construction.
			legs = append(legs, leg)
			continue
		}
		leg.Pos, _ = modalPos(s.Positions(), t.legPos[k])
		if leg.Pos != "" {
			leg.Tier, leg.Odds = modalBand(t.legTier[k][leg.Pos], n)
			id, share := modalID(t.legPlayer[k])
			leg.PlayerID, leg.Share = id, float64(share)/float64(n)
		}
		legs = append(legs, leg)
	}

	out := make([]SlotOutlook, 0, len(t.open))
	// Named once each, in lineup order: a man who is the mode of two slots is
	// one claim, not two, and the second slot takes the next man instead.
	named := map[string]bool{}
	for _, i := range t.open {
		o := SlotOutlook{Slot: s.Roster.Slots[i], Index: i}
		o.Filled = float64(t.slotFull[i]) / float64(n)
		id, _ := modalFilled(t.slotBy[i], named)
		if id != "" {
			named[id] = true
			// The share is how often he ends up in the LINEUP at all, pooled
			// over the interchangeable slots, and so is the arrival pick.
			o.PlayerID, o.Share = id, float64(t.anySlot[id])/float64(n)
			o.ElseID, _ = modalFilled(t.slotBy[i], named)
			// The band is read at the modal filler's OWN position, so the two
			// halves of the row are about the same kind of player.
			o.Tier, o.Odds = modalBand(t.slotTier[i][s.Players[id].Pos], n)
			o.Pick, _ = modalInt(t.anyPick[id])
		}
		out = append(out, o)
	}

	closed, best := 0, 0
	for k, c := range t.closed {
		if c > best || (c == best && k > closed) {
			closed, best = k, c
		}
	}
	return legs, out, closed
}

// The modal helpers all walk a deterministic order rather than a Go map, so a
// tie resolves the same way on every render instead of renaming the plan's
// second leg between frames.

func modalPos(positions []string, m map[string]int) (string, int) {
	best, n := "", 0
	for _, pos := range positions {
		if k := m[pos]; k > n {
			best, n = pos, k
		}
	}
	return best, n
}

func modalID(m map[string]int) (string, int) {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	best, n := "", 0
	for _, id := range ids {
		if m[id] > n {
			best, n = id, m[id]
		}
	}
	return best, n
}

// modalFilled is modalID over a slot's tally, skipping anyone already spoken
// for. The empty string is a real outcome there — the slot went unfilled — and
// must never be named as a player.
func modalFilled(m map[string]int, skip map[string]bool) (string, int) {
	without := make(map[string]int, len(m))
	for id, k := range m {
		if id != "" && !skip[id] {
			without[id] = k
		}
	}
	return modalID(without)
}

func modalInt(m map[int]int) (int, int) {
	best, n := 0, 0
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if m[k] > n {
			best, n = k, m[k]
		}
	}
	return best, n
}

// modalBand is the modal tier plus the fraction of ALL futures that landed that
// tier or better. Inclusive on purpose: landing a better band than the plan
// counted on is good news, not a miss.
func modalBand(tiers map[int]int, n int) (int, float64) {
	best, count := 0, 0
	keys := make([]int, 0, len(tiers))
	for tier := range tiers {
		keys = append(keys, tier)
	}
	sort.Ints(keys)
	for _, tier := range keys {
		if k := tiers[tier]; k > count || (k == count && tierRank(tier) < tierRank(best)) {
			best, count = tier, k
		}
	}
	if count == 0 {
		return 0, 0
	}
	hit := 0
	for tier, k := range tiers {
		if tierRank(tier) <= tierRank(best) {
			hit += k
		}
	}
	return best, float64(hit) / float64(n)
}

// tierRank orders tiers for a "this good or better" comparison, with 0 —
// untiered, which is k/def by design and anybody no source ranked — sorting
// behind every real tier instead of ahead of tier 1.
func tierRank(t int) int {
	if t == 0 {
		return math.MaxInt32
	}
	return t
}
