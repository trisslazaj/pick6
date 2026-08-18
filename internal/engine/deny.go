package engine

// Deny is v2's spite check, evaluated only when it's my pick: the seat picking
// immediately after me, their hungriest position, and whether that position's
// best remaining band is down to its last man. If it is — and I don't need him
// myself at starter weight — taking him is worth a chip.
//
// Strategically marginal, socially essential. It is a CHIP, not a verdict: it
// must not move PickChoices, the plan, or the banner gates, and the UI renders
// it reverse-video on dim next to the man's row. Their need is priced through
// the opponent machinery (opponentNeed, the room's K/DEF gate), because the
// seat after me is being predicted, not advised.
//
// ok is false off the clock, at the turn (the seat after me is me — no deny
// against yourself), on the draft's last pick, when their hungriest position
// still has depth, when its band holds more than one man, and when I need the
// man myself at starter weight — that's a pick, not a deny.
func (s *State) Deny() (Player, int, bool) {
	if s.Done() || s.OnTheClock() != s.MySlot {
		return Player{}, 0, false
	}
	next := s.PickNo + 1
	if next > s.Teams*s.Rounds {
		return Player{}, 0, false
	}
	them := s.SlotAt(next)
	if them == s.MySlot {
		return Player{}, 0, false
	}

	filled, _ := s.FilledSlots(them)
	round := s.Round(next)
	bestPos, bestNeed := "", 0.0
	for _, pos := range s.Positions() {
		if _, ok := s.BestNow(pos); !ok {
			continue // an empty position cannot be denied
		}
		if need := s.opponentNeed(pos, filled, round); need > bestNeed {
			bestPos, bestNeed = pos, need
		}
	}
	if bestNeed == 0 {
		return Player{}, 0, false
	}
	band, ok := s.bestTier(bestPos)
	if !ok || s.TierRemaining(bestPos, band) != 1 {
		return Player{}, 0, false
	}
	if s.Need(bestPos) > NeedFlex {
		return Player{}, 0, false // I want him for myself; that's a pick, not a deny
	}
	for _, p := range s.Available(bestPos) {
		if p.Tier == band {
			return p, them, true
		}
	}
	return Player{}, 0, false
}
