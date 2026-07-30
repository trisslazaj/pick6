package engine

import "math"

// CliffLevel describes how close a tier is to emptying.
type CliffLevel int

const (
	CliffNone    CliffLevel = iota
	CliffWarning            // holds under TierHoldWarn: "tier ending"
	CliffLast               // holds under TierHoldCliff: it probably won't reach me
)

// TierHold is the probability at least one player in the current tier at a
// position is still there the next time I can act on it:
//
//	1 - prod_j (1 - p~_j)   over the tier's available players
//
// "At least one" is awkward to sum directly and trivial as a complement: the
// tier fails me only if every last member is taken. ok is false for tier 0 —
// K and DEF carry no value from any source, so they carry no tier, and a
// question about their tier has no answer rather than a zero one.
//
// The horizon is my next chance to act on the tier, which ON THE CLOCK is the
// pick after this one: passing is the decision being priced, and asking whether
// a tier survives zero picks answers nothing. Every tier would hold at exactly
// 1.0 and the alarm would go silent on the one frame where you are actually
// choosing — measured, it fired on none of the 15 on-the-clock vantages of the
// scripted mock. On my last pick of the draft there is no "after", so hold
// stays 1 and nothing can be lost.
func (s *State) TierHold(pos string) (float64, bool) {
	best, ok := s.BestNow(pos)
	if !ok || best.Tier == 0 {
		return 0, false
	}
	at := s.TierHoldPick()
	c := s.survivalTilt(at, s.opponentPicksBefore(at))
	allGone := 1.0
	for id, p := range s.Players {
		if s.Taken[id] || p.Pos != pos || p.Tier != best.Tier {
			continue
		}
		allGone *= 1 - math.Pow(s.PSurviveAt(p, at), c)
	}
	return 1 - allGone, true
}

// TierHoldPick is the pick TierHold prices to, exported because the copy has to
// name it. Off the clock it is NextPick — the same horizon PSurviveTilted uses,
// so the header and the survival column below it agree and nothing needs saying.
// On the clock they diverge by design (see TierHold), and a hold of 3% printed
// directly above three survival cells reading 100% is two horizons one line
// apart with nothing marking which is which. Naming the pick is what marks it.
func (s *State) TierHoldPick() int {
	at := s.NextPick()
	if at <= s.PickNo {
		if q2 := s.FollowingPick(); q2 > 0 {
			at = q2
		}
	}
	return at
}

// Cliff reports the tier state of the best available player at a position.
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
	best, ok := s.BestNow(pos)
	if !ok || best.Tier == 0 {
		return CliffNone, 0, 0
	}
	n := s.TierRemaining(pos, best.Tier)
	if n >= s.TierSize(pos, best.Tier) {
		return CliffNone, best.Tier, n // untouched
	}
	hold, _ := s.TierHold(pos) // cannot fail once BestNow found a tiered player
	switch {
	case hold < TierHoldCliff:
		return CliffLast, best.Tier, n
	case hold < TierHoldWarn:
		return CliffWarning, best.Tier, n
	default:
		return CliffNone, best.Tier, n
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
// RunThreshold of them share a position. If two positions qualify, the one with
// more picks in the window wins.
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
