package engine

// CliffLevel describes how close a tier is to emptying.
type CliffLevel int

const (
	CliffNone    CliffLevel = iota
	CliffWarning            // <= CliffWarn left: "tier ending"
	CliffLast               // exactly one left: "cliff — last one"
)

// Cliff reports the tier state of the best available player at a position.
//
// A cliff means a tier is *emptying*, not that it happens to be small. A tier
// that started with one player — Josh Allen alone at QB1, which is the rankings
// file's own statement about the position — is not a cliff, and treating it as
// one would put "last one, take him or lose the tier" on screen at pick 1.01 of
// an empty draft. So a cliff requires that somebody has actually been drafted
// out of the tier.
func (s *State) Cliff(pos string) (level CliffLevel, tier, remaining int) {
	best, ok := s.BestNow(pos)
	if !ok || best.Tier == 0 {
		return CliffNone, 0, 0
	}
	n := s.TierRemaining(pos, best.Tier)
	if n >= s.TierSize(pos, best.Tier) {
		return CliffNone, best.Tier, n // untouched
	}
	switch {
	case n == 1:
		return CliffLast, best.Tier, n
	case n <= CliffWarn:
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
	} else {
		r.TierBroke = true
	}
	return r, true
}
