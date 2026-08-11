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

// simPositions indexes the pool's positions once, so the per-candidate weight
// loop reads an array instead of re-deriving need from the roster shape.
var simPositions = [...]string{"QB", "RB", "WR", "TE", "K", "DEF"}

// simPosIdx maps a position to its simPositions index; unknown positions get
// the extra bucket at the end, which prices like a bench pick.
func simPosIdx(pos string) int {
	for i, p := range simPositions {
		if p == pos {
			return i
		}
	}
	return len(simPositions)
}

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

// rollout is the shared core: play `picks` (overall pick numbers, ascending,
// all inside [start, far)) Rollouts times from the current available set and
// tally when each player came off the board.
func (s *State) rollout(start int, picks []int, far int, seed int64) *simTable {
	t := &simTable{pickNo: start, far: far, removals: map[string][]uint16{}, oppPicks: picks}
	if len(t.oppPicks) == 0 {
		return t
	}

	// The board, best effective price first — the order desirability ranks
	// over. Sorted once; each rollout walks it skipping its own dead.
	type simCand struct {
		id     string
		pos    string
		posIdx int
	}
	var pool []simCand
	price := map[string]float64{}
	for id, p := range s.Players {
		if s.Taken[id] {
			continue
		}
		pool = append(pool, simCand{id: id, pos: p.Pos, posIdx: simPosIdx(p.Pos)})
		price[id] = simPrice(p)
	}
	sort.Slice(pool, func(i, j int) bool {
		if price[pool[i].id] != price[pool[j].id] {
			return price[pool[i].id] < price[pool[j].id]
		}
		return pool[i].id < pool[j].id // map order must not reach the sample
	})

	// Only the slots that pick in the window mutate; copy exactly those, fresh
	// per rollout. The copy is not optional — a team at the turn picks twice
	// inside one rollout, and a frozen roster would let it draft two RBs at
	// identical need weight, which is exactly the behaviour v2 exists to fix.
	windowSlots := map[int]bool{}
	for _, q := range t.oppPicks {
		windowSlots[s.SlotAt(q)] = true
	}

	rng := rand.New(rand.NewSource(seed))
	alive := make([]bool, len(pool))
	top := make([]int, 0, CandidatePool)
	var w [CandidatePool]float64
	for m := 0; m < Rollouts; m++ {
		for i := range alive {
			alive[i] = true
		}
		rosters := make(map[int][]string, len(windowSlots))
		for slot := range windowSlots {
			rosters[slot] = append([]string(nil), s.Rosters[slot]...)
		}
		for _, q := range t.oppPicks {
			// The off-board escape: with the measured probability for this
			// depth, the pick spends itself on somebody the ranked pool cannot
			// see and removes nobody from it. The picking team's roster is
			// left as-is — we don't know the phantom's position — which
			// slightly overstates their remaining need; accepted, and small
			// next to pretending the pick ate a ranked player. The rng draw
			// only happens at a nonzero rate, so a nil OffBoard replays
			// byte-identical rollouts to a build without this feature.
			if r := s.offBoardRate(q); r > 0 && rng.Float64() < r {
				continue
			}
			slot := s.SlotAt(q)
			top = top[:0]
			for i := 0; i < len(pool) && len(top) < CandidatePool; i++ {
				if alive[i] {
					top = append(top, i)
				}
			}
			if len(top) == 0 {
				break // board exhausted; the picks left take nobody we know
			}
			filled, _ := s.fillSlots(rosters[slot])
			round := s.Round(q)
			beta := betaAt(round)
			// Need is a fact about a position, not a candidate, and pow of the
			// three discrete need weights memoizes the same way — so both are
			// computed once per pick, not once per candidate.
			powFlex, powBench := 1.0, 1.0
			if beta > 0 {
				powFlex = math.Pow(NeedFlex, beta)
				powBench = math.Pow(NeedBench, beta)
			}
			var needPow [len(simPositions) + 1]float64
			needPow[len(simPositions)] = powBench // an unknown position prices like a bench pick
			for pi, pos := range simPositions {
				switch need := s.opponentNeed(pos, filled, round); need {
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
			total := 0.0
			for k, i := range top {
				wk := simDesire[k]
				if beta > 0 {
					wk *= needPow[pool[i].posIdx]
				}
				w[k] = wk
				total += wk
			}
			if total == 0 {
				// Zero-weight guard: late in a draft the whole candidate pool
				// can be K/DEF a team's gate still zeroes. Ignore need for
				// this one pick and draft by ADP alone — never skip the pick,
				// never divide by zero.
				for k := range top {
					w[k] = simDesire[k]
					total += simDesire[k]
				}
			}
			r := rng.Float64() * total
			chosen := top[len(top)-1] // float roundoff lands on the last man
			for k, i := range top {
				r -= w[k]
				if r < 0 {
					chosen = i
					break
				}
			}
			alive[chosen] = false
			rosters[slot] = append(rosters[slot], pool[chosen].id)
			row := t.removals[pool[chosen].id]
			if row == nil {
				row = make([]uint16, far-start)
				t.removals[pool[chosen].id] = row
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
func (s *State) opponentNeed(pos string, filled []string, round int) float64 {
	if (pos == "K" || pos == "DEF") && s.Rounds-round+1 > OpponentKDefLastRounds {
		return 0
	}
	return s.needSlots(pos, filled)
}
