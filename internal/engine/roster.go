package engine

import "sort"

// Milestone 8: the objective stops being a formula.
//
// Every piece of PickChoices' old score — the need steps, the replacement
// discount, EndgameSlack, mustFill, the two-leg horizon — was a hand-built
// approximation of one quantity nobody computed: what does this pick do to the
// team I walk out with? This file computes it.
//
// U is deliberately the dullest function in the package. All the modelling is
// in how the rosters it scores get built (lookahead.go plays the draft out);
// what a finished team is worth is arithmetic, and arithmetic is what the old
// score's guesses were standing in for.

// RosterValue is U: the value of a finished roster.
//
//	U = Σ v(starter) over filled starting slots + BenchWeight × Σ v(bench)
//
// An unfilled slot contributes nothing, and that silence is the whole endgame
// guard priced rather than guarded: a plan that ends with an empty starting
// slot is worth exactly as much less as the man who would have stood in it.
// Likewise a flex slot is worth what the assignment actually put in it, so
// NeedFlex's 0.6 stops being a guess about what a flex is worth.
//
// The assignment is fillSlots' — dedicated slots first, then flex, so a
// flex-eligible player never squats on a flex slot while his own position sits
// open — over the roster sorted BY VALUE rather than by draft order. That is
// the one place this diverges from FilledSlots, and it is deliberate: the
// roster pane shows the lineup you built, in the order you built it, while U
// asks what the multiset is worth, which is the best lineup it can field. The
// two never disagree about WHICH slots are filled (that is decided by the
// position counts, which no ordering changes) — only about which of four
// receivers is the one riding the bench, and there the value order is the
// honest answer.
//
// Players whose value is zero — a live feed's handcuff nobody ranked, a K or
// DEF that arrived without one — contribute zero through either path. Harmless
// and the same blindness vor already has; Fills stays PickChoices' outer sort
// as the insurance against it.
func (s *State) RosterValue(ids []string) float64 {
	filled, bench := s.RosterLineup(ids)
	return rosterValueOf(s, filled, bench)
}

// rosterValueOf is U over an assignment that has already been computed. The
// rollouts want the lineup anyway — the "your team from here" block reads which
// man landed in which slot — and building it twice per future would be the
// whole objective's cost paid for nothing.
func rosterValueOf(s *State, filled, bench []string) float64 {
	total := 0.0
	for _, id := range filled {
		if id == "" {
			continue
		}
		total += float64(s.Players[id].Value)
	}
	for _, id := range bench {
		total += s.benchWeight(s.Players[id].Pos) * float64(s.Players[id].Value)
	}
	return total
}

// benchWeight is what a bench body at a position is worth, and it is the same
// two-index rule vor already applies — read off the ROSTER, so a superflex
// league answers differently by itself.
//
//   - A position that can reach a lineup through a flex slot (RB/WR/TE, plus QB
//     under superflex) is worth BenchWeight. A bench back genuinely converts to
//     points all season, through flex, byes and injuries, which is the claim
//     that weight has always been making.
//   - A position that CANNOT — QB in a 1QB league, K, DEF — is worth nothing. A
//     second quarterback in a 1QB league scores zero by construction; that is
//     not a new claim, it is the one vor.go's startable index already makes, and
//     it is the finding that ended the ~930-point subsidy the board used to hand
//     early quarterbacks.
//
// Without this, U prices a backup QB at a quarter of a value the market inflates
// for scarcity, and the board recommends a second one — which it did, in rounds
// 7 and 8 of the scripted mock at slot 12, before this existed. It is the same
// mistake in a new place: a bench body priced as though it could play.
func (s *State) benchWeight(pos string) float64 {
	if s.canReachFlex(pos) {
		return BenchWeight
	}
	return 0
}

// BenchWeightFor is benchWeight, exported for the one caller outside this
// package that has to price a bench body the same way U does: `pick6 regret`'s
// linear-exchange-rate check, which varies the exchange rate and must vary
// NOTHING else, or a disagreement between its two columns stops meaning what
// it says.
func (s *State) BenchWeightFor(pos string) float64 { return s.benchWeight(pos) }

// RosterLineup is U's assignment, for the display that speaks in it: one entry
// per starting slot ("" where the slot is unfilled), plus the bench, over the
// same value-ordered fill RosterValue scores.
func (s *State) RosterLineup(ids []string) (filled []string, bench []string) {
	return s.fillSlots(s.byValueDesc(ids))
}

// byValueDesc copies ids into value order, ties broken by id so a roster of
// equal-valued players assigns the same way on every call — a lineup that
// reshuffled between renders would make the "your team from here" block name a
// different bench player each frame for no reason.
func (s *State) byValueDesc(ids []string) []string {
	out := append(make([]string, 0, len(ids)), ids...)
	sort.SliceStable(out, func(i, j int) bool {
		vi, vj := s.Players[out[i]].Value, s.Players[out[j]].Value
		if vi != vj {
			return vi > vj
		}
		return out[i] < out[j]
	})
	return out
}
