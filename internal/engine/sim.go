package engine

import (
	"math"
	"math/rand"
	"sort"
)

// Engine v2: opponent-aware survival (docs/engine-v2.md). Instead of pricing
// the picks between mine as random draws from "drafters in general", simulate
// the actual rosters Sleeper hands us: each opponent pick samples a player by
// ADP desirability times that team's own computed need, Rollouts times, and
// survival is the fraction of futures a player lives through.
//
// This file replaces the survival number and nothing else — and it replaces
// the tilt with it. The exactly-N tilt exists because independent logistic
// survivals don't respect the removals budget; a rollout removes exactly N
// players by construction, so sim output is consumed raw (smoothed, below) and
// tilting it would balance a budget that is already balanced.

// simTable is one batch of rollouts, computed at a vantage and queried at any
// horizon inside its window. One table serves every consumer: recording WHEN
// each player was removed makes p(j, at) answerable for NextPick, ActPick and
// the lookahead's q2 from the same rollouts.
type simTable struct {
	pickNo int
	far    int
	// removals[id][i] counts the rollouts in which id was taken at overall pick
	// pickNo+i. No entry means no rollout ever took him.
	removals map[string][]uint16
	// oppPicks is every pick in [pickNo, far) that isn't mine, ascending — the
	// removals that can actually happen before a horizon.
	oppPicks []int
}

// pAt is the simulated survival of player id to horizon at.
//
// Zero opponent picks before the horizon returns exactly 1, not a smoothed
// 0.999: nobody can take a player across my own turn, and the my-own-pick
// exactness chain (EBest == v(bestNow), urgency exactly 0) needs arithmetic,
// not sampling. With picks in the window the count is Jeffreys-smoothed —
// (survives + 0.5) / (Rollouts + 1) — because a player taken in all 500
// rollouts is not probability zero, and calibrate scores log-loss, where one
// hard zero that survives in reality costs infinity.
func (t *simTable) pAt(id string, at int) float64 {
	if at > t.far {
		at = t.far
	}
	n := 0
	for _, q := range t.oppPicks {
		if q < at {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	taken := 0
	if row, ok := t.removals[id]; ok {
		for i, c := range row {
			if t.pickNo+i >= at {
				break
			}
			taken += int(c)
		}
	}
	return (float64(Rollouts-taken) + 0.5) / (float64(Rollouts) + 1)
}

// simFor returns the rollout table for the current vantage, running it on
// first use. The cache is semantics, not optimization: the seed derives from
// the vantage, so every query and every re-render at one pick reads the same
// rollouts — a keypress must not jiggle the board — and every mutator nils it.
func (s *State) simFor() *simTable {
	if s.sim == nil || s.sim.pickNo != s.PickNo {
		s.sim = s.runSim()
	}
	return s.sim
}

// simSeed mixes the base seed with the vantage: every pick gets fresh
// rollouts, every re-render at one pick gets identical ones.
func (s *State) simSeed() int64 {
	return int64(uint64(s.SimSeed) ^ uint64(s.PickNo)*0x9E3779B97F4A7C15)
}

// simDesire is A(k) = exp(-k / Tau) for the k-th best available player,
// precomputed over the candidate pool.
var simDesire = func() [CandidatePool]float64 {
	var d [CandidatePool]float64
	for k := range d {
		d[k] = math.Exp(-float64(k) / Tau)
	}
	return d
}()

// betaAt is how much need matters in a round: 0 through round 3 — everyone
// drafts best-available, which the 552-pick league profile measured rather
// than assumed — ramping to 1.5 by round 9, where need dominates.
func betaAt(round int) float64 {
	b := (float64(round) - 3) / 4
	if b < 0 {
		return 0
	}
	if b > 1.5 {
		return 1.5
	}
	return b
}

// The core indexes the pool's positions once — State.Positions(), derived from
// the lineup — so the per-candidate weight loop reads a slice instead of
// re-deriving need from the roster shape on every candidate. Unknown positions
// get the extra bucket past the end, which prices like a bench pick.

// simPrice is the price desirability ranks by: the room-warped effective price
// when a curve is loaded, with the no-ADP sentinel applied the same way
// PSurviveAt applies it. Without the sentinel a registered-but-unranked
// handcuff (ADP 0) would sort to the TOP of the pool and the sim's opponents
// would draft him first.
func simPrice(p Player) float64 {
	if adp := p.price(); adp > 0 {
		return adp
	}
	return UndraftedADP
}

// runSim plays the picks between now and my pick after next Rollouts times and
// tallies when each player came off the board. My own picks in the window are
// skipped — live, the question is conditional on my choice, and nobody can
// take a player from me across my own turn.
func (s *State) runSim() *simTable {
	far := s.FollowingPick()
	if far == 0 {
		far = s.NextPick()
	}
	if far < s.PickNo {
		far = s.PickNo // finished draft: NextPick falls back to a pick in the past
	}
	var picks []int
	for q := s.PickNo; q < far; q++ {
		if s.SlotAt(q) != s.MySlot {
			picks = append(picks, q)
		}
	}
	return s.rollout(s.PickNo, picks, far, s.simSeed())
}

// SimBacktest is the sim as a backtest vantage grades it: every pick in
// [from, to) is simulated, INCLUDING the vantage seat's own pick at `from`.
// That is the window convention calibrate's tilt already uses — at a backtest
// vantage I am one of the drafters removing a player, and the labels agree (a
// player the seat took at `from` is scored y = 0) — so the sim must expect the
// same removals count or it carries a one-pick bias against every label. The
// seat's own pick samples from its roster's needs through the same opponent
// machinery: at a backtest vantage the seat is being predicted, not advised.
//
// The caller owns the vantage: Taken and Rosters as of `from`, PickNo unread.
// Returns survival to `to` by player id; ids the rollouts never saw read the
// smoothed ceiling.
func (s *State) SimBacktest(from, to int) func(id string) float64 {
	picks := make([]int, 0, to-from)
	for q := from; q < to; q++ {
		picks = append(picks, q)
	}
	seed := int64(uint64(s.SimSeed) ^ uint64(from)*0x9E3779B97F4A7C15)
	t := s.rollout(from, picks, to, seed)
	return func(id string) float64 { return t.pAt(id, to) }
}

// simCand is one undrafted player as a rollout sees him.
type simCand struct {
	id     string
	pos    string
	posIdx int
}

// simCore is everything the two kinds of rollout share: the price-sorted board,
// the per-rollout alive mask, and one opponent pick sampled off them.
//
// The survival table below and the conditioned lookahead in lookahead.go differ
// in exactly one thing — what happens on MY picks, skipped there and
// policy-driven here — so the opponent model, the off-board escape and the
// roster copies live in one place and cannot drift apart. A lookahead that
// simulated opponents even slightly differently from the survival column would
// put two models on one frame, which is the bug survivalAt exists to prevent.
type simCore struct {
	s     *State
	pool  []simCand
	index map[string]int // player id -> pool index, for the lookahead's decisions
	alive []bool
	top   []int
	w     [CandidatePool]float64
	rng   *rand.Rand

	// pos is this draft's position vocabulary, derived from the lineup once at
	// setup. needPow is the parallel weight buffer oppPick fills, one entry per
	// position plus the unknown bucket — a field rather than a local because
	// oppPick runs ~20 times per rollout across 500 rollouts, and a slice
	// declared in there would be ten thousand heap allocations a frame where the
	// old fixed-size array was free.
	pos     []string
	needPow []float64
	// legal[pi] is "this seat can still roster a pi", read only under quota.
	// Sized and filled beside needPow because it is the same question asked of
	// the same loop; a second pass over the positions would just be the first one
	// twice.
	legal []bool
	quota bool

	// head is the first pool index that might still be alive. The candidate
	// pool is the price-ordered board's top CandidatePool survivors, and a
	// draft eats it from the front, so by the endgame a naive scan walks past
	// two hundred dead men to find twenty-five live ones — every simulated
	// pick, every future. alive only ever goes false inside a rollout, so a
	// forward-only cursor is exact rather than approximate. reset() rewinds it.
	head int

	// Scratch for the need reads. One simulated pick asks which of a seat's
	// starting slots are open, which is a fill over a roster of at most
	// Roster.Slots+Bench men; allocating those three slices per pick was, on a
	// full-horizon plan, more allocation than everything else put together.
	fillBuf []string
	usedBuf []bool
	posBuf  []string
}

// fill is assign() over the core's own scratch: the seat's filled lineup, valid
// until the next call. Nothing keeps the result — need is read off it
// immediately — so one buffer is enough and reusing it is not a risk anybody has
// to remember, because there is only one caller shape.
func (c *simCore) fill(ids []string) []string {
	if cap(c.usedBuf) < len(ids) {
		c.usedBuf = make([]bool, len(ids)*2)
		c.posBuf = make([]string, len(ids)*2)
	}
	if len(c.fillBuf) != len(c.s.Roster.Slots) {
		c.fillBuf = make([]string, len(c.s.Roster.Slots))
	}
	c.s.assign(ids, c.fillBuf, c.usedBuf[:len(ids)], c.posBuf[:len(ids)])
	return c.fillBuf
}

// newSimCore builds the pool once: the board, best effective price first, which
// is the order desirability ranks over. Each rollout walks it skipping its own
// dead.
func (s *State) newSimCore(seed int64) *simCore {
	c := &simCore{s: s, index: map[string]int{}, rng: rand.New(rand.NewSource(seed))}
	c.pos = s.simPositions()
	c.needPow = make([]float64, len(c.pos)+1)
	c.legal = make([]bool, len(c.pos)+1)
	c.legal[len(c.pos)] = true // an unknown position is nobody's quota
	c.quota = s.Roster.capped()
	price := map[string]float64{}
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		c.pool = append(c.pool, simCand{id: id, pos: p.Pos, posIdx: posIndex(c.pos, p.Pos)})
		price[id] = simPrice(p)
	}
	sort.Slice(c.pool, func(i, j int) bool {
		if price[c.pool[i].id] != price[c.pool[j].id] {
			return price[c.pool[i].id] < price[c.pool[j].id]
		}
		return c.pool[i].id < c.pool[j].id // map order must not reach the sample
	})
	for i, cand := range c.pool {
		c.index[cand.id] = i
	}
	c.alive = make([]bool, len(c.pool))
	c.top = make([]int, 0, CandidatePool)
	return c
}

// reset puts the whole board back on the table for the next rollout.
func (c *simCore) reset() {
	for i := range c.alive {
		c.alive[i] = true
	}
	c.head = 0
}

// reseed restarts the random stream. This is the common-random-numbers hook:
// the lookahead evaluates every candidate against the SAME seed, so two
// candidates' scores differ where their choices force the opponents' hands and
// nowhere else. See conditionedLegs.
func (c *simCore) reseed(seed int64) { c.rng = rand.New(rand.NewSource(seed)) }

// copyRosters copies exactly the seats that pick inside a window, fresh per
// rollout. The copy is not optional — a team at the turn picks twice inside one
// rollout, and a frozen roster would let it draft two RBs at identical need
// weight, which is exactly the behaviour v2 exists to fix.
func (s *State) copyRosters(slots map[int]bool) map[int][]string {
	out := make(map[int][]string, len(slots))
	for slot := range slots {
		out[slot] = append([]string(nil), s.Rosters[slot]...)
	}
	return out
}

// oppPick plays the pick at overall pick q for whoever is on the clock there:
// sample a player by ADP desirability times that seat's own computed need,
// remove him, and append him to that seat's roster copy.
//
// Three outcomes, and the caller has to tell them apart. took is the normal
// one. exhausted means the board holds nobody we know, and the caller must
// STOP the rollout rather than carry on: the escape draw happens before the
// emptiness check, so continuing would consume draws the original loop never
// consumed and shift every later rollout's random stream. Neither means the
// off-board escape fired — that pick spent itself on somebody the ranked pool
// cannot see and removed nobody from it. The picking team's roster is left
// as-is (we don't know the phantom's position), which slightly overstates their
// remaining need: accepted, and small next to pretending the pick ate a ranked
// player. The rng draw only happens at a nonzero rate, so a nil OffBoard
// replays byte-identical rollouts to a build without the feature.
func (c *simCore) oppPick(q int, rosters map[int][]string) (idx int, took, exhausted bool) {
	s := c.s
	if r := s.offBoardRate(q); r > 0 && c.rng.Float64() < r {
		return 0, false, false
	}
	slot := s.SlotAt(q)
	// The need read moves ahead of the candidate scan, because under a hard
	// quota need decides who is even a candidate. Nothing between here and the
	// sample touches the rng, so an nfl rollout draws exactly the numbers it drew
	// before — the golden tables are the proof.
	filled := c.fill(rosters[slot])
	round := s.Round(q)
	beta := betaAt(round)
	// Need is a fact about a position, not a candidate, and pow of the three
	// discrete need weights memoizes the same way — so both are computed once
	// per pick, not once per candidate.
	powFlex, powBench := 1.0, 1.0
	if beta > 0 {
		powFlex = math.Pow(NeedFlex, beta)
		powBench = math.Pow(NeedBench, beta)
	}
	needPow := c.needPow
	needPow[len(c.pos)] = powBench // an unknown position prices like a bench pick
	for pi, pos := range c.pos {
		need := s.opponentNeed(pos, rosters[slot], filled, round)
		c.legal[pi] = need > 0
		switch need {
		case 0:
			needPow[pi] = 0
		case NeedStarter:
			needPow[pi] = 1
		case NeedFlex:
			needPow[pi] = powFlex
		case NeedBench:
			needPow[pi] = powBench
		default:
			needPow[pi] = math.Pow(need, beta) // unreachable today
		}
	}
	for c.head < len(c.pool) && !c.alive[c.head] {
		c.head++
	}
	c.top = c.top[:0]
	filtered := false // a legal candidate was skipped, so the board is not empty
	for i := c.head; i < len(c.pool) && len(c.top) < CandidatePool; i++ {
		if !c.alive[i] {
			continue
		}
		if c.quota && !c.legal[c.pool[i].posIdx] {
			// Under a hard quota a position this seat has already filled is not
			// a bad pick, it is an impossible one — the official app refuses a
			// sixth defender. A zero WEIGHT is not enough on its own: the
			// zero-weight guard below exists to rescue a pick whose whole
			// candidate pool priced at nothing, and it would rescue this one
			// straight into drafting the illegal man.
			filtered = true
			continue
		}
		c.top = append(c.top, i)
	}
	if len(c.top) == 0 {
		// "Exhausted" means the BOARD is empty, and the caller stops the rollout
		// on it — every later pick in the window then removes nobody and every
		// survival in those rounds reads ~100%. A quota seat with nothing legal
		// left is a different thing entirely: the board is full of players, they
		// are just all at positions this one seat has filled. That is a pick
		// that removes nobody from the ranked pool, which is exactly the shape
		// the off-board escape already has.
		return 0, false, !filtered
	}
	total := 0.0
	for k, i := range c.top {
		wk := simDesire[k]
		if beta > 0 {
			wk *= needPow[c.pool[i].posIdx]
		}
		c.w[k] = wk
		total += wk
	}
	if total == 0 {
		// Zero-weight guard: late in a draft the whole candidate pool can be
		// K/DEF a team's gate still zeroes. Ignore need for this one pick and
		// draft by ADP alone — never skip the pick, never divide by zero.
		for k := range c.top {
			c.w[k] = simDesire[k]
			total += simDesire[k]
		}
	}
	r := c.rng.Float64() * total
	chosen := c.top[len(c.top)-1] // float roundoff lands on the last man
	for k, i := range c.top {
		r -= c.w[k]
		if r < 0 {
			chosen = i
			break
		}
	}
	c.alive[chosen] = false
	rosters[slot] = append(rosters[slot], c.pool[chosen].id)
	return chosen, true, false
}

// rollout is the survival table's driver: play `picks` (overall pick numbers,
// ascending, all inside [start, far)) Rollouts times from the current available
// set and tally when each player came off the board.
func (s *State) rollout(start int, picks []int, far int, seed int64) *simTable {
	t := &simTable{pickNo: start, far: far, removals: map[string][]uint16{}, oppPicks: picks}
	if len(t.oppPicks) == 0 {
		return t
	}
	core := s.newSimCore(seed)
	windowSlots := map[int]bool{}
	for _, q := range t.oppPicks {
		windowSlots[s.SlotAt(q)] = true
	}
	for m := 0; m < Rollouts; m++ {
		core.reset()
		rosters := s.copyRosters(windowSlots)
		for _, q := range t.oppPicks {
			idx, took, exhausted := core.oppPick(q, rosters)
			if exhausted {
				break // board exhausted; the picks left take nobody we know
			}
			if !took {
				continue
			}
			row := t.removals[core.pool[idx].id]
			if row == nil {
				row = make([]uint16, far-start)
				t.removals[core.pool[idx].id] = row
			}
			row[q-start]++
		}
	}
	return t
}

// offBoardRate is the escape probability for the pick at overall pick q,
// indexed by full rounds remaining after q's own. Out of range or unmeasured
// depths read 0 — early rounds are effectively never off-board anyway.
func (s *State) offBoardRate(q int) float64 {
	rem := s.Rounds - s.Round(q)
	if rem < 0 || rem >= len(s.OffBoard) {
		return 0
	}
	return s.OffBoard[rem]
}

// opponentNeed prices an opponent's need at a given round, gated by
// OpponentKDefLastRounds rather than KDefLastRounds. Two different questions,
// deliberately two constants: KDefLastRounds governs what the tool is willing
// to recommend, while the sim has to predict what the room actually does — and
// this room drafts its first kicker in round 10. Reusing our own suppression
// here would keep kickers "available" that were drafted rounds ago, corrupting
// every survival number downstream.
func (s *State) opponentNeed(pos string, ids, filled []string, round int) float64 {
	if s.Roster.holds(pos) && s.Rounds-round+1 > OpponentKDefLastRounds {
		return 0
	}
	return s.needSlots(pos, ids, filled)
}
