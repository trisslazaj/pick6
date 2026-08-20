package engine

import "sort"

// CliffLevel describes how close a tier is to emptying.
type CliffLevel int

const (
	CliffNone    CliffLevel = iota
	CliffWarning            // holds under TierHoldWarn: "tier ending"
	CliffLast               // holds under TierHoldCliff: it probably won't reach me
)

// bestTier is the tier the position's cliff machinery speaks about: the
// LOWEST-numbered tier that still has an available player, which is the best
// quality band the rankings file says is left. ok is false when no available
// player carries a tier at all (K and DEF, or an emptied position).
//
// This is NOT always bestNow's tier, and the difference is a real board state
// rather than a corner case: a rankings file's ordering can disagree with the
// value curve's. Dynatyze puts carnell tate and davante adams a band above
// jaylen waddle; fantasycalc values waddle higher. Waddle then leads the
// position by value wearing tier 8 while tier 6 still has two men — and read
// off bestNow, the board printed "last one in tier 8 — take him or lose the
// tier", an empty threat while a better tier per the file's own worldview was
// stocked. The cliff question that means something is "will the best remaining
// BAND reach me", so the band is what it asks about. Where file order and
// value order agree — nearly everywhere — the two definitions are identical.
func (s *State) bestTier(pos string) (int, bool) {
	best, found := 0, false
	for id, p := range s.Players {
		if s.OffMyBoard(id, p) || p.Pos != pos || p.Tier == 0 {
			continue
		}
		if !found || p.Tier < best {
			best, found = p.Tier, true
		}
	}
	return best, found
}

// TierHold is the probability at least one player in the position's best
// remaining tier (bestTier above) is still there the next time I can act on it:
//
//	1 - prod_j (1 - p~_j)   over the tier's available players
//
// "At least one" is awkward to sum directly and trivial as a complement: the
// tier fails me only if every last member is taken. ok is false when nothing
// available carries a tier — K and DEF carry no value from any source, so they
// carry no tier, and a question about their tier has no answer rather than a
// zero one.
//
// The horizon is my next chance to act on the tier, which ON THE CLOCK is the
// pick after this one: passing is the decision being priced, and asking whether
// a tier survives zero picks answers nothing. Every tier would hold at exactly
// 1.0 and the alarm would go silent on the one frame where you are actually
// choosing — measured, it fired on none of the 15 on-the-clock vantages of the
// scripted mock. On my last pick of the draft there is no "after", so hold
// stays 1 and nothing can be lost.
func (s *State) TierHold(pos string) (float64, bool) {
	tier, ok := s.bestTier(pos)
	if !ok {
		return 0, false
	}
	f := s.survivalAt(s.ActPick())
	allGone := 1.0
	for id, p := range s.Players {
		if s.OffMyBoard(id, p) || p.Pos != pos || p.Tier != tier {
			continue
		}
		allGone *= 1 - f(p)
	}
	return 1 - allGone, true
}

// ActPick is my next chance to act, and the one horizon every probability on
// screen is priced to — the tier holds in the group headers and the survival
// column under them both read it, so no two numbers on a frame answer different
// questions.
//
// Off the clock it is NextPick. On the clock NextPick is THIS pick, and pricing
// to it asks whether a player survives zero picks: the answer is 1 for
// everybody, and the survival column went blank on the one frame where you are
// actually choosing. What passing costs you is measured to the pick after, so
// that is what both numbers use. On my last pick of the draft there is no
// "after" and it stays NextPick — nothing can be taken, so nothing is at risk.
func (s *State) ActPick() int {
	at := s.NextPick()
	if at <= s.PickNo {
		if q2 := s.FollowingPick(); q2 > 0 {
			at = q2
		}
	}
	return at
}

// Cliff reports the state of the position's best remaining tier (bestTier).
//
// The levels come from TierHold, not from the raw remaining count. A count
// cannot tell the difference between three players the room is about to eat and
// one player nobody wants, and those are opposite situations: the first is the
// cliff, the second is a free wait. The count is still returned, because the
// copy quotes it and "last one in tier 2" is a claim only the count can make.
//
// A cliff still means a tier is *emptying*, not that it happens to be small. A
// tier that started with one player — Josh Allen alone at QB1, which is the
// rankings file's own statement about the position — is not a cliff, and
// treating it as one would put "last one, take him or lose the tier" on screen
// at pick 1.01 of an empty draft. So a cliff still requires that somebody has
// actually been drafted out of the tier, whatever the probability says.
func (s *State) Cliff(pos string) (level CliffLevel, tier, remaining int) {
	best, ok := s.bestTier(pos)
	if !ok {
		return CliffNone, 0, 0
	}
	n := s.TierRemaining(pos, best)
	if n >= s.TierSize(pos, best) {
		return CliffNone, best, n // untouched
	}
	hold, _ := s.TierHold(pos) // cannot fail once bestTier found a tiered player
	switch {
	case hold < TierHoldCliff:
		return CliffLast, best, n
	case hold < TierHoldWarn:
		return CliffWarning, best, n
	default:
		return CliffNone, best, n
	}
}

// Run describes an active positional run.
type Run struct {
	Pos       string
	Count     int  // picks at that position inside the window
	TierBroke bool // the position's current top tier is empty
	TierLeft  int  // players left in the current top tier
	Tier      int
}

// DetectRun looks at the last RunWindow picks and reports a run when at least
// RunThreshold of them share a position AND that count beats what the market
// itself expected the window to look like. If two positions qualify, the one
// with more picks in the window wins.
//
// The second condition is the base-rate gate. Round 1 is nearly all backs and
// receivers by construction — this room's own history runs 47% rb, 53% wr —
// so four backs in six picks there is Tuesday, not a run, and the flat
// threshold fired on ~69% of round-1 windows by chance. The market's forecast
// for the window is free: the position mix of the next RunWindow available
// players by adp is who the market expects to go next, so a run must clear
// that expectation by RunSurprise picks. The forecast is read off the CURRENT
// board rather than the board as it stood when the window opened — a gate,
// not a model, and the difference is at most the window itself.
func (s *State) DetectRun() (Run, bool) {
	window := s.Picks
	if len(window) > RunWindow {
		window = window[len(window)-RunWindow:]
	}
	counts := map[string]int{}
	for _, p := range window {
		counts[s.Players[p.PlayerID].Pos]++
	}

	best, bestN := "", 0
	for pos, n := range counts {
		if n > bestN || (n == bestN && pos < best) {
			best, bestN = pos, n
		}
	}
	if bestN < RunThreshold {
		return Run{}, false
	}
	if float64(bestN) < s.expectedInWindow(best)+RunSurprise {
		return Run{}, false
	}

	r := Run{Pos: best, Count: bestN}
	if _, tier, remaining := s.Cliff(best); tier != 0 {
		r.Tier, r.TierLeft = tier, remaining
	} else if _, ok := s.BestNow(best); !ok {
		// Tier 0 has two causes and only one of them is "no value left". A
		// position with nobody available is genuinely exhausted; K and DEF are
		// tier 0 permanently, by design, with a full board of players still on
		// it. Inferring TierBroke from the tier alone put "def run — tier broke,
		// no value left." above an accent-bordered def group listing eight
		// defenses, on exactly the rounds this league drafts them.
		r.TierBroke = true
	}
	return r, true
}

// expectedInWindow is the market's forecast for a position's share of the next
// RunWindow picks: how many of the next RunWindow available players by adp
// play there. Players without an adp are off the drafted radar and carry no
// forecast; a board with fewer priced players than the window (the endgame)
// just counts what it has, which biases the expectation LOW and lets late runs
// fire more easily — the right direction, since a thin board is exactly where
// a run empties a position.
func (s *State) expectedInWindow(pos string) float64 {
	type pricedPlayer struct {
		adp float64
		pos string
		id  string
	}
	var priced []pricedPlayer
	for id, p := range s.Players {
		if s.Taken[id] || p.ADP <= 0 {
			continue
		}
		priced = append(priced, pricedPlayer{p.ADP, p.Pos, id})
	}
	// Same id tiebreak Available() uses: two players priced identically at the
	// window's cutoff would otherwise land on either side of it depending on map
	// order, and the gate would flicker between renders on nothing.
	sort.Slice(priced, func(i, j int) bool {
		if priced[i].adp != priced[j].adp {
			return priced[i].adp < priced[j].adp
		}
		return priced[i].id < priced[j].id
	})
	if len(priced) > RunWindow {
		priced = priced[:RunWindow]
	}
	n := 0
	for _, p := range priced {
		if p.pos == pos {
			n++
		}
	}
	return float64(n)
}
