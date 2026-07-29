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

// SupportFloor raises a survival probability to what the observed sample can
// actually rule out. `high` is the earliest pick anyone in `drafts` real drafts
// took this player at; surviving to `at` means surviving every pick strictly
// before it, so at <= high describes a window no observed draft ever removed him
// inside. Zero events in n trials bounds the rate at about RuleOfThree/n, so the
// survival cannot honestly read below 1 - 3/n however confident the curve is.
//
// It only ever raises, never lowers — an observed early pick is evidence a
// player CAN go early, not evidence about how often. Absent support (high or
// drafts zero) is "no data", not "no risk", so it changes nothing.
//
// BUILT, MEASURED, AND DELIBERATELY NOT WIRED IN. `pick6 calibrate` grades it on
// the real 2024 draft and it moves nothing: of 15,923 predictions, 9,817 fall
// inside the unobserved window and the bound bites on ZERO of them against the
// raw curve and on TWO against the shrunk one. Brier and log-loss are identical
// to four decimals with it and without it, tilted or untilted. Replayed over the
// shipped 2026 board — all twelve seats, every round — it bites on 16 of 34,104
// predictions, the largest move being 74.5% -> 82.4%.
//
// The reason is structural, not seasonal: `high` is the minimum observed pick
// and adp is the mean, so high < adp always, and the floor's window therefore
// always lies before a player's own price — where the logistic already reads
// near 1. The bound is dominated by the curve almost everywhere by construction.
//
// It stays here because it is the right idea about the right failure (a tight
// curve inventing risk the sample denies) and it is one call site away: put it
// around PSurviveAt's return, which is BEFORE the exactly-n tilt, and that
// ordering matters — the tilt balances a budget over the finished per-player
// numbers, so flooring afterwards would hand back mass it had just balanced and
// break the property the tilt was adopted for. Rewire it if a source ever ships
// a `high` that is not a per-draft minimum, or if calibrate starts reporting a
// bind count worth grading.
//
// max(drafts, RuleOfThree) keeps a two-draft player from producing a negative
// floor — with n = 1 the bound 1 - 3/1 is -2, which is the rule of three saying
// it has nothing to offer, not a probability.
func SupportFloor(p float64, at, high, drafts int) float64 {
	if high <= 0 || drafts <= 0 || at > high {
		return p
	}
	return math.Max(p, 1-RuleOfThree/math.Max(float64(drafts), RuleOfThree))
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

// BestLater names the player most likely to be the best one left at my next
// pick: the argmax of p~_j * prod_{i<j}(1 - p~_i), the same per-player term
// EBest sums. ok is false only when the position is empty.
//
// This is a DISPLAY value now. Urgency prices the whole distribution, but a
// header that reads "chase -> hall" needs one name, and the modal survivor
// answers the question a human actually asks: who am I probably picking
// instead? The old definition — best value clearing a 0.5 survival threshold —
// was the urgency math, and it left with it. The two usually name the same man.
// Where they differ, the modal answer is the one higher up the board: he only
// has to outlast the intervening picks, while everyone below him also needs the
// men above him to go first. A 49% player above the line beats a 50% player
// below it, and when nobody clears 0.5 at all the modal answer is the top of
// the position rather than the deepest, safest name on it.
//
// Ties keep Available order (value desc, adp asc, id asc), so the answer is
// deterministic rather than map-order roulette.
func (s *State) BestLater(pos string) (Player, bool) {
	avail := s.Available(pos)
	if len(avail) == 0 {
		return Player{}, false
	}
	at := s.NextPick()
	c := s.survivalTilt(at, s.opponentPicksBefore(at))

	best, bestWeight, acc := avail[0], -1.0, 1.0
	for _, p := range avail {
		q := math.Pow(s.PSurviveAt(p, at), c)
		if w := acc * q; w > bestWeight {
			best, bestWeight = p, w
		}
		acc *= 1 - q
	}
	return best, true
}

// SafeToWait is the one place the "safe to wait" state is decided, so the board
// tab and the data tab cannot end up contradicting each other about the same
// position. It means what it always meant — your guy himself will keep — but it
// can no longer be inferred from urgency == 0: urgency is continuous now, and
// an exact zero only happens on my own pick.
//
// The guards around it are unchanged: an untiered position (K/DEF) makes no
// claim, an emptying tier is never safe however likely its best man is to last,
// and nothing is "safe to wait" when the wait is zero picks long.
func (s *State) SafeToWait(pos string) bool {
	if s.PicksUntilMine() <= 0 {
		return false
	}
	now, ok := s.BestNow(pos)
	if !ok || now.Tier == 0 {
		return false
	}
	if level, _, _ := s.Cliff(pos); level != CliffNone {
		return false
	}
	return s.PSurviveTilted(now) >= SurviveThreshold
}

// Urgency is the need-weighted value I expect to lose by waiting: the best
// player available now, minus the expected value of the best one still there at
// my next pick.
//
// Unlike milestone 4's version this is continuous in the survival
// probabilities — a position whose top player is 60% to last no longer reads
// identically to one whose top player is certain, and no single pick can
// re-sort the board by nudging somebody across a threshold.
//
// Zero when the position is empty or need is zero (K/DEF before the last
// rounds), and EXACTLY zero on my own pick: no picks intervene, every survival
// is 1, so EBest is bestNow's own value and the UI's value tie-break does the
// pointing. It can never go negative — EBest is a sub-convex combination of
// values that bestNow's own leads.
func (s *State) Urgency(pos string) float64 {
	now, ok := s.BestNow(pos)
	if !ok {
		return 0
	}
	need := s.Need(pos)
	if need == 0 {
		return 0 // suppressed positions skip the walk entirely; it can't matter
	}
	return (float64(now.Value) - s.EBest(pos, s.NextPick())) * need
}
