package engine

import (
	"math"
	"sort"
)

// Milestone 7: the plan stops being a formula.
//
// PickChoices has always scored "spend my next pick on P" as the best pair that
// choice leads, but its second leg was EBest — an expectation over independent
// survivals, computed off today's board, with no idea which players are
// actually still there when I come back. Milestone 6 built rollouts that play
// the draft forward. This file points them at MY OWN picks.
//
// Leg one takes the candidate — a decision, not a draw. The opponents draft
// exactly as the survival rollouts already model them (same pool, same
// desirability, same need gates, same off-board escape: one simCore, so the two
// cannot drift). At my next pick the engine's own greedy rule takes whoever is
// really left. The pair is then scored on the player who actually landed,
// future by future, in the units PickChoices always used.
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

// planResult is what the conditioned rollouts say about spending my next pick
// on one candidate.
type planResult struct {
	// Legs is the mean, over futures, of everything after leg one: each
	// policy-driven leg's (v - R)⁺ × need, in exactly the units PickChoices'
	// second term always carried. It replaces EBest(Q, q2) - R(Q) with the
	// value of the player who really landed.
	Legs float64

	// Second is the modal position leg two actually landed. "" when no future
	// had a legal one — a live endgame state, not a bug.
	Second string
	// SecondTier is the modal tier at that position, and SecondOdds the
	// fraction of ALL futures that landed Second at that tier or better:
	// "lands a tier-2 rb in 78% of them". Landing a BETTER tier is strictly
	// good news, so the claim is inclusive rather than exact. Tier 0 means the
	// modal man is untiered — k/def by design, or somebody no source ranked —
	// and the copy drops the tier word rather than printing "tier-0".
	SecondTier int
	SecondOdds float64
}

// planTable caches one vantage's conditioned rollouts. The argument simFor's
// cache makes, twice as loudly: the seed derives from the vantage, so every
// candidate and every re-render at one pick must read the SAME futures or the
// ranking jiggles under a keypress — and at PlanRollouts futures per candidate,
// a render that recomputed once for the banner, once for the rows and once for
// the plan line would pay three times over for byte-identical numbers. Every
// mutator nils it, exactly like s.sim.
type planTable struct {
	pickNo int
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
// on independent samples differ by their choice PLUS sampling noise, and at 500
// futures that noise is worth a few hundred value points — which is exactly the
// gap that decides these rankings. The board would reorder itself between
// renders on nothing. On one seed the draws are identical and the candidates
// diverge only where their own removals force the opponents' hands, so the
// difference between two scores is the difference the choice makes.
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
	// The leg-two policy may only take a position that passed the SAME
	// membership test the first leg's candidates passed. Not a formality: the
	// v1 formula got this for free by maxing over `cands`, and the rollouts had
	// to be told.
	//
	// The gap is the endgame guard. Membership is decided by Need, which
	// multiplies bench weight by endgameSlack and takes it to ZERO at R == U;
	// the policy prices need with needFrom, which carries no slack (deliberately
	// — see NeedAfter, where charging it made the pair score depend on the order
	// of two legs ending at the same roster). So without this the policy would
	// happily "actually do" what PickChoices deleted from the board. Found by
	// review on the scripted mock at 13.09 of seed 5 slot 9: the plan line read
	// "rb at 13.09 → wr at 14.04" directly above "every remaining pick must fill
	// a starter", with wr not a rendered row at all and Need(WR) exactly 0. It
	// is the wrong ACTION as much as the wrong label — with rb, k and def open
	// and three picks left you take an rb, a k and a def, and a bench receiver
	// is not a plan.
	allowed := make(map[string]bool, len(cands))
	for _, c := range cands {
		allowed[c.pos] = true
	}
	// One pool, built once and shared: the board is the same board for every
	// candidate. Only the random stream is restarted per candidate.
	core := s.newSimCore(s.planSeed())
	res := make(map[string]planResult, len(cands))
	for _, c := range cands {
		core.reseed(s.planSeed())
		res[c.best.ID] = s.planRollout(core, c, mine, mustFill, allowed)
	}
	s.plan = &planTable{pickNo: s.PickNo, res: res}
	return res
}

// planRollout plays the window [PickNo, my last planned pick] PlanRollouts
// times with my own picks un-skipped, and summarises what the pair was worth.
//
// The candidate comes off the board at the START of the rollout rather than at
// my leg-one pick, and that is the conditioning this score is defined by:
// score(P) is what the pair is worth GIVEN I get P. In the futures where an
// opponent would have taken him first, the premise of the plan has already
// failed — leg one's value term would be quoting a man I do not have — so those
// futures are excluded by construction, and the opponents who did not take him
// spent their picks on somebody else, which is exactly what removing him first
// makes them do. It is also the semantics the v1 formula always had: leg one is
// priced at v(bestNow) unconditionally and leg two's EBest already excluded him
// from the pool. Milestone 7 makes leg two real; it does not re-open "will I
// even get him", which the survival column and the → fallback clauses answer
// directly and in their own units.
func (s *State) planRollout(core *simCore, c planCand, mine []int, mustFill int, allowed map[string]bool) planResult {
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

	// Leg two's preference order is a fact about the CANDIDATE, not about the
	// future: need is a property of a position and of the roster leg one leaves
	// behind, replacement is static, value is static. So the argmax over
	// "everyone still available" collapses to one fixed order walked to the
	// first man still alive — which is what makes six candidates × 500 futures
	// a walk instead of a scan. Legs past the second genuinely do depend on the
	// future, since leg two changed the roster, so they recompute inside the
	// rollout. That is the cost PlanDepth gates.
	mineIDs := make([]string, 0, len(s.Rosters[s.MySlot])+legs)
	mineIDs = append(mineIDs, s.Rosters[s.MySlot]...)
	mineIDs = append(mineIDs, c.best.ID)
	order2, w2, need2 := s.legPolicy(core, mineIDs, allowed, mustFillAt(2, legs, mustFill, c.fills1))

	n := PlanRollouts
	if opp == 0 {
		// The turn: back-to-back picks, nothing intervenes, every future is the
		// same future. One rollout is the whole distribution, the score is
		// exact, and the odds clause reads an honest 100% rather than 99.8% of
		// a sample of a deterministic thing.
		n = 1
	}

	var (
		legsSum   float64
		posCount  = map[string]int{}
		tierCount = map[string]map[int]int{}
	)
	for m := 0; m < n; m++ {
		core.reset()
		core.alive[candIdx] = false // the decision this whole score is about
		rosters := s.copyRosters(windowSlots)
		ids, done, total := mineIDs, c.fills1, 0.0
		var secondPos string
		secondTier := 0

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
			ord, wts, need := order2, w2, need2
			if leg > 2 {
				ord, wts, need = s.legPolicy(core, ids, allowed, mustFillAt(leg, legs, mustFill, done))
			}
			pick := -1
			for _, i := range ord {
				if core.alive[i] {
					pick = i
					break
				}
			}
			if pick < 0 {
				continue // nothing legal left for this leg: it is worth nothing
			}
			core.alive[pick] = false
			total += wts[pick]
			p := s.Players[core.pool[pick].id]
			if leg == 2 {
				secondPos, secondTier = p.Pos, p.Tier
			}
			if leg < legs {
				// Only a later leg reads these, so only a later leg pays for the
				// copy. Appending onto ids in place would write into mineIDs'
				// spare capacity and leak leg two's man into the next rollout.
				ids = append(append(make([]string, 0, len(ids)+1), ids...), p.ID)
				if need[p.Pos] > NeedBench {
					done++
				}
			}
		}

		legsSum += total
		if secondPos != "" {
			posCount[secondPos]++
			if tierCount[secondPos] == nil {
				tierCount[secondPos] = map[int]int{}
			}
			tierCount[secondPos][secondTier]++
		}
	}

	res := planResult{Legs: legsSum / float64(n)}
	// Walked in planPositions order rather than over the map, so a modal tie
	// resolves the same way every other tie in this package does — and never to
	// map order, which would rename the plan's second leg between renders.
	best := 0
	for _, pos := range planPositions {
		if k := posCount[pos]; k > best {
			res.Second, best = pos, k
		}
	}
	if res.Second == "" {
		return res
	}
	bestT := 0
	for tier, k := range tierCount[res.Second] {
		if k > bestT || (k == bestT && tierRank(tier) < tierRank(res.SecondTier)) {
			res.SecondTier, bestT = tier, k
		}
	}
	hit := 0
	for tier, k := range tierCount[res.Second] {
		if tierRank(tier) <= tierRank(res.SecondTier) {
			hit += k
		}
	}
	res.SecondOdds = float64(hit) / float64(n)
	return res
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

// mustFillAt reports whether my leg k of legs has to close an open starting
// slot: with mustFill of them required overall and done already closed, the
// legs after this one cannot absorb the shortfall.
func mustFillAt(k, legs, mustFill, done int) bool {
	return mustFill-done >= legs-k+1
}

// legPolicy is what the engine would actually do with one of my picks: the
// preference order over the board by VOR × need, given the roster my earlier
// legs leave behind. It is the spec's "the engine's own greedy choice, i.e.
// what I would actually do there", and it is deliberately the same vocabulary
// the score is written in rather than a second opinion.
//
// Two positions are dropped outright rather than merely scoring zero, because a
// rollout must not "actually do" what the tool would never recommend: a
// SUPPRESSED one (k/def before the last three rounds), since an argmax over a
// board of zeros lands on a round-four kicker and the plan line recommends one;
// and one the caller did not put in `allowed`, which is PickChoices' own
// candidate set — see conditionedLegs for the endgame case that needs it.
//
// Feasibility is the OTHER rule and it is a preference, not a filter, which is
// the same shape PickChoices' Fills has always had — a pair that closes an open
// slot outranks one that doesn't, and no pair is ever struck off. Written as a
// filter it produced a real dead end on the scripted mock at 12.09: with only k
// and def unfilled and three picks left, mustFill demanded leg two close a
// starter, our own k/def suppression forbade the only positions that could, and
// the policy had nothing legal to take at all — the plan line rendered
// "rb at 13.07 →  at 14.06" with a hole where the position goes. Sorting
// fillers ahead of non-fillers gets the identical behaviour whenever a filler
// is alive and degrades to the best available man when none is, which is what
// the drafter would actually do.
//
// Everything it reads is static within a rollout, which is the whole point:
// need is a property of a position, replacement of a position, value of a
// player. So the argmax a rollout runs is a walk down this order to the first
// man still alive.
func (s *State) legPolicy(core *simCore, ids []string, allowed map[string]bool, mustFill bool) (order []int, weight []float64, need map[string]float64) {
	filled, _ := s.fillSlots(ids)
	need = map[string]float64{}
	repl := map[string]float64{}
	for _, cand := range core.pool {
		if _, seen := need[cand.pos]; seen {
			continue
		}
		need[cand.pos] = s.needFrom(cand.pos, filled)
		repl[cand.pos] = s.Replacement(cand.pos)
	}
	weight = make([]float64, len(core.pool))
	order = make([]int, 0, len(core.pool))
	for i, cand := range core.pool {
		n := need[cand.pos]
		if n == 0 || !allowed[cand.pos] {
			// Suppressed, or a position the board is not offering at all: the
			// tool would not recommend him, so it does not simulate taking him.
			continue
		}
		if v := float64(s.Players[cand.id].Value) - repl[cand.pos]; v > 0 {
			weight[i] = v * n
		}
		order = append(order, i)
	}
	// Stable, so players of equal weight — everybody at or below replacement,
	// which is most of a late board — keep the pool's own price order instead
	// of whatever the sort left behind.
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if mustFill {
			fa, fb := need[core.pool[ia].pos] > NeedBench, need[core.pool[ib].pos] > NeedBench
			if fa != fb {
				return fa
			}
		}
		return weight[ia] > weight[ib]
	})
	return order, weight, need
}
