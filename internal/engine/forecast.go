package engine

import "sort"

// The room forecast: what each seat between now and my next chance to act is
// likely to take, read off the rollouts the survival number is already built
// from. Every survival on the board is a claim about these picks; this is the
// claim shown as picks instead of as a percentage. Nothing here runs a new
// simulation — the sim table records WHEN each player came off the board, and
// summing those removals per pick, per position, is the whole computation.

// PickForecast is one opponent pick in the window.
type PickForecast struct {
	PickNo int
	Slot   int
	// Pos is the position the rollouts took most often at this pick and Share
	// its share of rollouts. Pos is "" when the modal outcome was a pick that
	// left the ranked pool (a handcuff, a third kicker) — the escape hatch.
	Pos   string
	Share float64
	// Mix is every position's share at this pick; the shares sum to 1 - OffBoard.
	Mix      map[string]float64
	OffBoard float64
	// PlayerID is the man most often taken at this pick and PlayerShare how
	// often. Empty when no ranked player was ever taken here.
	PlayerID    string
	PlayerShare float64
}

// RoomForecast is the window's opponent picks in order — everything between
// PickNo and ActPick that isn't mine — under the sim. Under the adp logistic
// there are no rollouts and no forecast: nil, and the board draws nothing.
func (s *State) RoomForecast() []PickForecast {
	if s.Survival != SurvivalSim || s.Done() {
		return nil
	}
	t := s.simFor()
	at := s.ActPick()
	// Walk the removals in id order rather than map order, the same reason
	// lookahead.go's modalID sorts: two men removed at a pick equally often
	// would otherwise hand "likely gone" a different name every render.
	ids := make([]string, 0, len(t.removals))
	for id := range t.removals {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []PickForecast
	for _, q := range t.oppPicks {
		if q >= at {
			break
		}
		f := PickForecast{PickNo: q, Slot: s.SlotAt(q), Mix: map[string]float64{}}
		total, bestC := 0, 0
		for _, id := range ids {
			row := t.removals[id]
			i := q - t.pickNo
			if i < 0 || i >= len(row) || row[i] == 0 {
				continue
			}
			c := int(row[i])
			total += c
			f.Mix[s.Players[id].Pos] += float64(c) / Rollouts
			if c > bestC {
				f.PlayerID, f.PlayerShare, bestC = id, float64(c)/Rollouts, c
			}
		}
		f.OffBoard = float64(Rollouts-total) / Rollouts
		// Positions in planPositions order, modalPos's tiebreak, so an exact tie
		// between two positions names the same one twice running.
		for _, pos := range forecastPositions(f.Mix) {
			if share := f.Mix[pos]; share > f.Share {
				f.Pos, f.Share = pos, share
			}
		}
		if f.OffBoard > f.Share {
			f.Pos, f.Share = "", f.OffBoard
		}
		out = append(out, f)
	}
	return out
}

// forecastPositions is the mix's positions in planPositions order, with anything
// planPositions doesn't know sorted after it by name so a stray position is
// still reported rather than dropped.
func forecastPositions(mix map[string]float64) []string {
	out := make([]string, 0, len(mix))
	for _, pos := range planPositions {
		if _, ok := mix[pos]; ok {
			out = append(out, pos)
		}
	}
	var rest []string
	for pos := range mix {
		known := false
		for _, p := range planPositions {
			if p == pos {
				known = true
				break
			}
		}
		if !known {
			rest = append(rest, pos)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// ExpectedRemovals sums the forecast: how many of each position the window is
// expected to take before I act, plus "" for picks that leave the ranked pool.
// Sorted by expectation, largest first, so the caller can print the top of it.
type Removal struct {
	Pos    string
	Expect float64
}

func (s *State) ExpectedRemovals() []Removal {
	sum := map[string]float64{}
	for _, f := range s.RoomForecast() {
		for pos, share := range f.Mix {
			sum[pos] += share
		}
		sum[""] += f.OffBoard
	}
	var out []Removal
	for pos, e := range sum {
		if e > 0 {
			out = append(out, Removal{pos, e})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Expect != out[j].Expect {
			return out[i].Expect > out[j].Expect
		}
		return out[i].Pos < out[j].Pos
	})
	return out
}
