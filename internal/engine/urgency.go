package engine

import "math"

// PSurvive estimates the probability a player is still available at my next
// pick, from the ADP logistic: a player whose ADP equals my next pick is a coin
// flip; well before it, he's gone; well after, he's safe.
//
// Missing ADP (<= 0) means the player is off the drafted radar — live feeds
// register handcuffs and rookies no ADP source ranked — and is treated as
// UndraftedADP: he always survives. Missing sigma falls back to SigmaDefault;
// per-player sigma is converted from observed stdev and clamped at fetch time,
// not here.
func (s *State) PSurvive(p Player) float64 {
	adp := p.ADP
	if adp <= 0 {
		adp = UndraftedADP
	}
	sigma := p.Sigma
	if sigma <= 0 {
		sigma = SigmaDefault
	}
	// Clamp the quotient, not the numerator: an undrafted-radar player at a
	// tight sigma would overflow exp, but a mid-range player must come through
	// untouched.
	x := (float64(s.NextPick()) - adp) / sigma
	if x > SurvivalExpClamp {
		x = SurvivalExpClamp
	} else if x < -SurvivalExpClamp {
		x = -SurvivalExpClamp
	}
	return 1 / (1 + math.Exp(x))
}

// BestLater is the best player at a position I can still expect to be there at
// my next pick: highest value among those with PSurvive >= SurviveThreshold.
// If nobody clears the threshold, the available player most likely to survive —
// someone always technically does. ok is false only when the position is empty.
func (s *State) BestLater(pos string) (Player, bool) {
	avail := s.Available(pos)
	if len(avail) == 0 {
		return Player{}, false
	}
	// Available is value-desc, ADP-asc, ID-asc, so the first player clearing
	// the threshold is the argmax by value with a deterministic tie-break.
	for _, p := range avail {
		if s.PSurvive(p) >= SurviveThreshold {
			return p, true
		}
	}
	best, bestP := avail[0], s.PSurvive(avail[0])
	for _, p := range avail[1:] {
		if sp := s.PSurvive(p); sp > bestP {
			best, bestP = p, sp
		}
	}
	return best, true
}

// Urgency is the need-weighted value lost by waiting until my next pick:
// value(bestNow) - value(bestLater), scaled by roster need. Zero when the
// position is empty, when need is zero (K/DEF before the last rounds), or when
// bestNow will survive anyway — that zero IS the wait signal.
//
// A position down to its last player also reads zero: the fallback returns him
// as his own bestLater. Whether he vanishes outright is the cliff banner's job;
// urgency only prices the drop between now and later.
func (s *State) Urgency(pos string) float64 {
	now, ok := s.BestNow(pos)
	if !ok {
		return 0
	}
	later, _ := s.BestLater(pos) // cannot fail once BestNow succeeded
	return float64(now.Value-later.Value) * s.Need(pos)
}
