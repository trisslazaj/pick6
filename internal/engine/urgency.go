package engine

import "math"

// PSurviveAt estimates the probability a player is still available at pick
// `at`, from the ADP logistic: a player whose ADP equals that pick is a coin
// flip; well before it, he's gone; well after, he's safe.
//
// The estimate is conditional on the present: he is demonstrably available
// right now, so only the picks between now and `at` can take him. Without
// the S(at)/S(pickNo) ratio a faller — ADP 18, still here at pick 21 —
// reads ~20% with one pick to go, when the worst case is one team taking him.
// Conditioning barely moves anyone whose ADP is still ahead (S(now) ~ 1); it
// only repairs the players the market has already passed.
//
// The horizon is a parameter because the callers ask about more than one:
// my next pick during a draft, my pick after that for lookahead, and any
// vantage pair at all when the backtester scores the model against a real
// completed draft.
//
// Missing ADP (<= 0) means the player is off the drafted radar — live feeds
// register handcuffs and rookies no ADP source ranked — and is treated as
// UndraftedADP: he always survives. Missing sigma falls back to SigmaDefault;
// per-player sigma is converted from observed stdev and clamped at fetch time,
// not here.
func (s *State) PSurviveAt(p Player, at int) float64 {
	adp := p.ADP
	if adp <= 0 {
		adp = UndraftedADP
	}
	sigma := p.Sigma
	if sigma <= 0 {
		sigma = SigmaDefault
	}
	if at < s.PickNo {
		// A horizon behind us means no picks intervene. This is how a finished
		// draft arrives: NextPick falls back to the final pick, which is in the
		// past, and unguarded the ratio tops 1 — a replay frame prints "105%".
		at = s.PickNo
	}
	// The ratio S(at)/S(now) is computed in log space: log S(p) is
	// -softplus((p-adp)/sigma), so the log of the ratio is a difference of
	// softpluses and the tail degrades to exp(-(at-now)/sigma), a per-pick
	// hazard. Clamping each S separately instead would flatten that difference
	// once both exponents saturate, and a deep faller would read 100% — the
	// further past his ADP, the safer he'd look. at >= now, softplus is
	// increasing, so the result is genuinely in (0, 1]: exactly 1 on my own
	// pick, when no picks intervene and urgency falls to the value tie-break.
	a := (float64(at) - adp) / sigma
	b := (float64(s.PickNo) - adp) / sigma
	return math.Exp(softplus(b) - softplus(a))
}

// PSurvive is PSurviveAt at the horizon that matters at the clock — my next
// pick. See PSurviveAt for the model and its sentinels.
func (s *State) PSurvive(p Player) float64 {
	return s.PSurviveAt(p, s.NextPick())
}

// softplus is log(1+exp(x)) without overflow: past the clamp the +1 is
// noise (relative error under 1e-13) and the function is just x.
func softplus(x float64) float64 {
	if x > SurvivalExpClamp {
		return x
	}
	return math.Log1p(math.Exp(x))
}

// Falling reports a player the market has passed: still available a full
// FallerSigmas past his ADP, measured in his own sigma so a volatile flier
// isn't "falling" at a gap that would be seismic for a locked-in first
// rounder. Falling players are discounts — the board marks them rather than
// letting them blend into the list.
func (s *State) Falling(p Player) bool {
	if p.ADP <= 0 {
		return false // never off the radar, never falling
	}
	sigma := p.Sigma
	if sigma <= 0 {
		sigma = SigmaDefault
	}
	return (float64(s.PickNo)-p.ADP)/sigma >= FallerSigmas
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
