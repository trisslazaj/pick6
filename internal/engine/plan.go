package engine

import (
	"math"
	"sort"
)

// Plan is the two-pick recommendation: take the best player at First with my
// next pick, and expect to be taking Second with the one after it.
//
// Both legs carry their pick number because the plan is computed as-if standing
// at NextPick but renders at every vantage — on eleven frames out of twelve
// "now" is somebody else's pick, and copy that says otherwise is lying.
type Plan struct {
	First      string  // position to take at FirstPick
	Second     string  // position to expect at SecondPick
	FirstPick  int     // NextPick()
	SecondPick int     // FollowingPick()
	Score      float64 // what the choice is worth: U under sim, the v1 pair score under adp

	// SecondTier and SecondOdds are the conditioned lookahead's outcome claim:
	// the modal tier leg two lands at Second, and how often it lands that tier
	// or better across the simulated futures. "lands a tier-2 rb 78%."
	//
	// SecondOdds is 0 when the plan was priced by the v1 formula (sim off),
	// which has no futures to count and must not be made to look like it does.
	SecondTier int
	SecondOdds float64

	// Legs is the whole skeleton under sim: one entry per remaining pick of
	// mine, leg one being the candidate himself. nil under adp mode, which
	// plans two legs by formula and has no futures to summarise. The trio above
	// is leg two's claim, kept rather than replaced because the copy pins to it
	// and adp mode has nothing else — see PickChoice.
	Legs []PlanLeg
}

// planPositions is the order candidates are considered in, and therefore the
// tie-break: two choices that score identically resolve to the earlier one here.
// The order carries no meaning of its own (it's the lineup's), but iterating a
// fixed slice rather than a map is what stops an exact tie — two positions with
// equal value and equal need, which a fresh board produces more often than it
// sounds — from recommending a different pair on every frame.
var planPositions = []string{"QB", "RB", "WR", "TE", "K", "DEF"}

// PickChoice is one way to spend my next pick: a position, its best available
// man, and what the two-pick lookahead says that choice is worth.
type PickChoice struct {
	Pos   string
	Best  Player
	Score float64 // under sim the mean value of the team it leads to; under adp the v1 pair score
	Fills int     // open starting slots the choice ends up closing, capped at what feasibility demands

	// Second is the second leg the score chose; "" on my last pick, where there
	// is none. Under sim it is Legs[1].Pos — the leg the rollouts' own policy
	// actually took, rather than a max over positions.
	Second string

	// SecondTier and SecondOdds carry the conditioned rollouts' outcome claim
	// for this choice; both are zero under the v1 formula. See Plan.
	SecondTier int
	SecondOdds float64

	// Alt is the other man the two curves disagreed about at this position —
	// the market's own pick when Best is the value curve's, and the value
	// curve's when the market's man won. Zero when they agree, which is most
	// positions on most frames. AltIsMarket says which of the two it is.
	//
	// Under sim BOTH are scored, on the same seed, and Best is whichever led to
	// the better team; under adp the value pick is Best by definition and Alt is
	// the dissent note the board has always drawn.
	Alt         Player
	AltIsMarket bool

	// Legs and Outlook are the milestone-8 rollouts' own summaries, nil under
	// adp mode: what the plan does at each of my remaining picks, and what the
	// starting slots that are open today end up holding. Both come out of the
	// SAME futures Score is the mean of, which is what lets the pane say
	// personal things without any risk of contradicting the ranking.
	Legs    []PlanLeg
	Outlook []SlotOutlook
}

// PickChoices ranks every way to spend my next pick, and it is THE PRIMARY KEY:
// both board frames order positions by it, and BestPlan is literally its first
// row, so the plan line and the ordering under it cannot disagree. They used to
// — the on-clock list ranked by CostOfPassing while the plan ranked pairs by
// value x need, and the two named different positions on ~47% of on-the-clock
// frames of the scripted mock.
//
// UNDER SIM (milestone 8) the score is the team the choice leads to:
//
//	score(P) = mean over futures of U(my finished roster | leg one takes bestNow(P))
//
// where U is RosterValue (roster.go) — starters at full value, bench at
// BenchWeight, an unfilled slot at nothing — and the futures run to my LAST
// pick, with opponents drafting as the sim models them and my own later picks
// taken by the engine's greedy policy over whoever is really left
// (lookahead.go). Every guess the formula below makes is answered by
// arithmetic there instead: a flex is worth the man the assignment puts in it,
// an unfilled slot is worth the man who would have stood in it, and the later
// picks actually fill the positions the replacement discount stood in for.
//
// UNDER ADP the v1 formula survives wholesale, because rollouts exist where the
// sim does — one switch, the same chokepoint philosophy survivalAt follows:
//
//	score(P) = (v(bestNow(P)) - R(P))⁺ · need(P)
//	         + max over Q of (EBest'(Q, q2) - R(Q))⁺ · NeedAfter(Q | bestNow(P))
//
// Both legs are priced over replacement — R(P) is the value of the man this
// league would have handed you at that position anyway (vor.go) — which is what
// keeps a steep-topped but cheap-to-fill position from winning the argmax on
// headline value. P == Q is legitimate (double-tapping a position at the turn
// is a real strategy), and the second leg excludes bestNow(P) from its pool and
// prices need against the roster the first leg leaves behind. The replacement
// discount only separates pairs that fill DIFFERENT position sets — comparing
// qb-then-wr against wr-then-qb, both R's appear once in each and cancel.
//
// The deliberate simplification, and it is unchanged by milestone 8 because it
// is a statement about LEG ONE: the first leg is priced with today's best
// available even when my next pick is seventeen picks away and that man will
// plainly be gone by then. Every choice is scored against the same board and
// carries the same optimism; Score is a ranking, not a forecast of what I will
// actually get. "Will I get him" is the survival column's question and it is
// answered there, in its own units.
//
// On my last pick of the draft there is nothing to plan. Under sim the score is
// U(my roster + him) exactly, no rollouts; under adp it degenerates to vor x
// need. Those are the same idea — what does he buy — computed at two levels of
// honesty.
//
// Feasibility outranks score, exactly as before: with R remaining picks and U
// unfilled starters, only R - U of the picks may go on a bench player, so a
// choice is ranked first by how many open starting slots it ends up closing
// (capped at what is actually required) and only then by score. Mostly
// redundant once U prices an empty slot — but K and DEF carry synthesized
// values that can still arrive at 0 from a live feed, and a hole U prices at
// 0 × anything is invisible. Cheap insurance.
func (s *State) PickChoices() []PickChoice {
	// A position needs both a need and a player to be a leg of a choice. The
	// second condition isn't decoration: EBest on an empty position is 0, so
	// without it a board with one live position still names a dead one as the
	// second leg whenever every pair ties.
	//
	// Membership is decided by Need — the same number that decides whether the
	// board shows the position at all, so a choice can never name a group the
	// reader cannot see. The WEIGHT is the slack-free need, because the second
	// leg is priced by NeedAfter, which has no slack either; mixing the two made
	// the score depend on the order of two legs that end at the same roster.
	filled, _ := s.FilledSlots(s.MySlot)
	candAt := func(pos string, best Player) planCand {
		c := planCand{pos: pos, best: best, need: s.needFrom(pos, filled)}
		// Need above bench weight IS "this pick takes an open starting slot",
		// dedicated or flex — that is exactly what needFrom encodes, so asking
		// it here cannot drift from the need the score is weighted by.
		if c.need > NeedBench {
			c.fills1 = 1
		}
		return c
	}
	var cands []planCand
	for _, pos := range planPositions {
		if s.Need(pos) == 0 {
			continue
		}
		best, ok := s.BestNow(pos)
		if !ok {
			continue
		}
		cands = append(cands, candAt(pos, best))
		// The market's dissenting man is a CANDIDATE under sim, not a note.
		// Where the two curves disagree about who the best receiver is, the
		// futures are the only thing that can settle it — so both get scored,
		// on the same seed, and whichever leads to the better team takes the
		// row and can take the verdict. Under adp he stays what he always was:
		// a second opinion the board draws and does not rank.
		if s.Survival != SurvivalSim || s.Scorer != ScorerRoster {
			continue
		}
		if mp, ok := s.MarketPick(pos); ok && mp.ID != best.ID {
			cands = append(cands, candAt(pos, mp))
		}
	}
	if len(cands) == 0 {
		return nil
	}

	// How many of my own picks the score plans over. Two under the v1 formula,
	// which is what its arithmetic can price; ALL of them under the conditioned
	// rollouts, because the thing being scored is the finished roster and a
	// roster is not finished until my last pick. Fewer when the draft ends
	// first; one on my last pick, where there is nothing to plan.
	depth := 2
	if s.Survival == SurvivalSim && s.Scorer == ScorerRoster {
		depth = s.Rounds
	}
	mine := s.MyUpcomingPicks(depth)
	legs := len(mine)
	if legs < 1 {
		legs = 1 // the draft is over for me: score the one leg that is left
	}

	// mustFill is how many of the legs have to take an open starting slot. The
	// legs spend that many of my R remaining picks against U unfilled starters,
	// so only R - U of them can go on a bench player. R < U is already lost, and
	// filling as many as possible is still the best available answer, so it
	// clamps to the leg count rather than going higher.
	mustFill := legs - (s.MyPicksLeft() - len(s.UnfilledStarters(s.MySlot)))
	if mustFill > legs {
		mustFill = legs
	}
	if mustFill < 0 {
		mustFill = 0
	}

	// The conditioned second leg (milestone 7, lookahead.go), for every
	// candidate on one shared seed. Sim only: rollouts exist where the sim
	// does, and under -survival=adp the plan keeps the formula below — one
	// switch, the same chokepoint philosophy survivalAt follows.
	var cond map[string]planResult
	if legs > 1 && s.Survival == SurvivalSim {
		cond = s.conditionedLegs(cands, mine, mustFill)
	}

	// Every pair re-solves the same tilt: both second legs share the horizon q2
	// and its pick count, so the bisection returns the same c every time.
	// Measured at ~2ms per call on the full 201-player board against ~0.4ms for
	// a whole urgency pass — a render happens on a pick or a keypress, so this
	// stays the formula as written rather than hoisting the solve out and
	// drifting from it.
	choices := make([]PickChoice, 0, len(cands))
	for _, first := range cands {
		leg1 := s.VOR(first.best) * first.need
		if legs == 1 {
			f := first.fills1
			if f > mustFill {
				f = mustFill
			}
			score := leg1
			if s.Survival == SurvivalSim && s.Scorer == ScorerRoster {
				// The honest version of the same question. There is no future
				// to sample — this pick is the last thing that happens to my
				// roster — so U is exact rather than a mean, and the ranking is
				// literally "which finished team do I prefer".
				score = s.RosterValue(append(append(make([]string, 0, len(s.Rosters[s.MySlot])+1),
					s.Rosters[s.MySlot]...), first.best.ID))
			}
			choices = append(choices, PickChoice{Pos: first.pos, Best: first.best, Score: score, Fills: f})
			continue
		}
		if cond != nil {
			// Fills describes the lineup the futures actually finish with: the
			// modal number of today's open starting slots that end filled,
			// capped at what feasibility demands. At full horizon nearly every
			// candidate saturates that cap, which is exactly the inertness it
			// should have — U is doing the work now. It bites only where U is
			// blind: a starting slot that gets filled by somebody carrying no
			// value, where U cannot tell a filled slot from an empty one.
			r := cond[first.best.ID]
			c := PickChoice{
				Pos: first.pos, Best: first.best,
				Score: r.Score, Fills: r.Closed,
				Legs: r.Legs, Outlook: r.Outlook,
			}
			if len(r.Legs) > 1 {
				c.Second, c.SecondTier, c.SecondOdds = r.Legs[1].Pos, r.Legs[1].Tier, r.Legs[1].Odds
			}
			if s.Scorer != ScorerRoster {
				// Milestone 7's score, kept runnable so `pick6 regret` has
				// something to grade the new one against. Its Fills is the
				// count of LEGS that close a slot rather than the count of
				// SLOTS that end closed — the difference is the horizon, and a
				// two-leg window can only speak about two legs.
				c.Score = leg1 + r.Pair
				c.Fills = first.fills1
				if c.Second != "" && s.NeedAfter(c.Second, first.best.ID) > NeedBench {
					c.Fills++
				}
			}
			if c.Fills > mustFill {
				c.Fills = mustFill // past the requirement, extra fills buy nothing
			}
			choices = append(choices, c)
			continue
		}
		q2 := mine[1]
		best := PickChoice{Pos: first.pos, Best: first.best, Score: math.Inf(-1), Fills: -1}
		for _, second := range cands {
			needAfter := s.NeedAfter(second.pos, first.best.ID)
			fills := first.fills1
			if needAfter > NeedBench {
				fills++
			}
			if fills > mustFill {
				fills = mustFill // past the requirement, extra fills buy nothing
			}
			leg2 := s.ebest(second.pos, q2, first.best.ID) - s.Replacement(second.pos)
			if leg2 < 0 {
				leg2 = 0 // an expectation below replacement is a loss you'd never take
			}
			score := leg1 + leg2*needAfter
			// Feasibility first, score second — and strictly greater on both, so a
			// tie keeps the pair found first and the answer is planPositions order
			// rather than whatever the last loop happened to leave behind.
			if fills > best.Fills || (fills == best.Fills && score > best.Score) {
				best.Score, best.Fills, best.Second = score, fills, second.pos
			}
		}
		choices = append(choices, best)
	}
	// Collapse the contest: one row per position, won by whichever candidate led
	// to the better team, with the loser carried as Alt so the frame shows both
	// opinions instead of resolving one silently. The value pick is always
	// first in `choices` (candAt's loop order), so the market pick only
	// displaces him by winning outright — a tie keeps the value curve, which is
	// the conservative half of a disagreement the board is not trying to settle
	// on its own authority.
	out := make([]PickChoice, 0, len(choices))
	at := map[string]int{}
	for _, c := range choices {
		i, seen := at[c.Pos]
		if !seen {
			at[c.Pos] = len(out)
			out = append(out, c)
			continue
		}
		if c.Fills > out[i].Fills || (c.Fills == out[i].Fills && c.Score > out[i].Score) {
			c.Alt, c.AltIsMarket = out[i].Best, false
			out[i] = c
			continue
		}
		out[i].Alt, out[i].AltIsMarket = c.Best, true
	}
	// Under adp — and under sim wherever the market man was never a candidate —
	// the dissent is still worth drawing, so it is filled in here rather than
	// left to the ui to re-derive and drift from.
	for i := range out {
		if out[i].Alt.ID != "" {
			continue
		}
		if mp, ok := s.MarketPick(out[i].Pos); ok && mp.ID != out[i].Best.ID {
			out[i].Alt, out[i].AltIsMarket = mp, true
		}
	}
	// Stable, so exact score ties keep planPositions order — the same guarantee
	// the pair loop's strict > gives the second leg.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Fills != out[j].Fills {
			return out[i].Fills > out[j].Fills
		}
		return out[i].Score > out[j].Score
	})
	return out
}

// MarketPick is the market's own choice at a position: the cheapest available
// man by adp, when the market prices him at least MarketGapPicks ahead of the
// value curve's choice. ok is false when the two agree, which is most positions
// on most frames.
//
// This is the rice case, and it is the board refusing to silently take a side:
// FantasyCalc ranked rashee rice wr16 while the market priced him the best
// receiver left, and a value-ordered pane buried him seven rows deep — invisible
// — while championing a man the market priced eleven picks later. Where its two
// sources disagree, the board shows both and says which is which; since
// milestone 8 it also lets the futures pick a winner.
//
// Raw ADP, never the room-warped price: the question is what the MARKET thinks,
// and warping it toward this room's own habits would be asking the room what the
// market said.
func (s *State) MarketPick(pos string) (Player, bool) {
	valueBest, ok := s.BestNow(pos)
	if !ok || valueBest.ADP <= 0 {
		return Player{}, false
	}
	best, found := Player{}, false
	for _, p := range s.Available(pos) {
		if p.ADP <= 0 {
			continue
		}
		if !found || p.ADP < best.ADP {
			best, found = p, true
		}
	}
	if !found || best.ID == valueBest.ID || best.ADP+MarketGapPicks > valueBest.ADP {
		return Player{}, false
	}
	return best, true
}

// ScoresTheTeam reports whether the ranking on screen is the milestone-8
// objective — the value of the team each choice leads to — rather than the
// milestone-7 pair score. The display asks because the two are different
// sentences: a caption that named the wrong one would be the frame lying about
// its own arithmetic.
//
// It is deliberately NOT the same question as "did the plan run to my last
// pick". At the endgame the pair window IS the whole horizon, so the futures
// really do finish a roster and the blocks that read them are honest there
// under either scorer — while the ranking above them is still the pair score.
func (s *State) ScoresTheTeam() bool {
	return s.Survival == SurvivalSim && s.Scorer == ScorerRoster
}

// BestPlan answers the question the rest of the engine is too greedy to ask:
// wr now and rb on the way back, or the reverse? It is PickChoices' top row —
// one brain, so the plan line can never contradict the ordering rendered under
// it — kept as its own function because the plan is a pair with pick numbers
// attached and most callers want exactly that.
//
// ok is false when there is no second pick to plan for (the draft ends first)
// or when nothing available is worth anything to my roster.
func (s *State) BestPlan() (Plan, bool) {
	q2 := s.FollowingPick()
	if q2 == 0 {
		return Plan{}, false // the draft ends before I pick again: there is no plan
	}
	choices := s.PickChoices()
	if len(choices) == 0 {
		return Plan{}, false
	}
	top := choices[0]
	return Plan{
		First:      top.Pos,
		Second:     top.Second,
		FirstPick:  s.NextPick(),
		SecondPick: q2,
		Score:      top.Score,
		SecondTier: top.SecondTier,
		SecondOdds: top.SecondOdds,
		Legs:       top.Legs,
	}, true
}

// Consequence is what the runner-up costs, named in a player: the man the top
// choice's futures put on my roster and the runner-up's futures don't.
//
// It is only sayable because of common random numbers. Future m of the top
// choice and future m of the runner-up are the SAME world — same opponent
// draws, same order — so the two ending rosters differ by what the choice
// caused and nothing else. Under independent sampling the diff would be mostly
// noise and the sentence would be a lie with a name in it.
type Consequence struct {
	Runner   string  // the runner-up choice's position
	PlayerID string  // the man you lose by taking the runner-up instead
	Share    float64 // ...in this fraction of futures
	Pos      string  // his position, for the "costs you a tier at te" fallback
	Tier     int
}

// Consequence diffs the top two choices' futures. ok is false when there is no
// runner-up, when the plan was priced by the v1 formula (no futures to diff), or
// when nothing recurs often enough to name — the caller is expected to drop the
// clause entirely rather than print a hedge, because a consequence line that
// says nothing must not print.
func (s *State) Consequence() (Consequence, bool) {
	choices := s.PickChoices()
	if len(choices) < 2 || s.plan == nil || s.plan.pickNo != s.PickNo {
		return Consequence{}, false
	}
	top, ok := s.plan.res[choices[0].Best.ID]
	if !ok || len(top.futures) == 0 {
		return Consequence{}, false
	}
	alt, ok := s.plan.res[choices[1].Best.ID]
	if !ok || len(alt.futures) != len(top.futures) {
		return Consequence{}, false
	}
	lost := map[string]int{}
	for m := range top.futures {
		have := make(map[string]bool, len(alt.futures[m]))
		for _, id := range alt.futures[m] {
			have[id] = true
		}
		for _, id := range top.futures[m] {
			if !have[id] {
				lost[id]++
			}
		}
	}
	// Two exclusions, both because the clause has to READ as a consequence and
	// not merely be one.
	//
	// The candidate himself is always in the diff and is never the answer: the
	// runner-up costing you the top choice's own man is the tautology the
	// verdict block already states on its first line.
	delete(lost, choices[0].Best.ID)
	// And nobody at the RUNNER-UP'S OWN position, which is a subtler trap. Take
	// the tight end now and the futures stop needing a second one, so the diff
	// honestly reports that you end up without him — but rendered as "taking te
	// instead costs you strange" over a frame that is simultaneously offering
	// strange at tight end, it reads as a contradiction. Found by review at
	// 12.12 of the scripted mock, where all three of the verdict clause, a field
	// row and the outlook block named the same man. The informative shape of
	// this sentence is always cross-position: what the OTHER choice's futures
	// never get around to.
	for id, p := range s.Players {
		if p.Pos == choices[1].Pos {
			delete(lost, id)
		}
	}
	id, n := modalID(lost)
	if id == "" {
		return Consequence{}, false
	}
	p := s.Players[id]
	return Consequence{
		Runner:   choices[1].Pos,
		PlayerID: id,
		Share:    float64(n) / float64(len(top.futures)),
		Pos:      p.Pos,
		Tier:     p.Tier,
	}, true
}
