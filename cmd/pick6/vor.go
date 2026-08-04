package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/engine"
)

// leagueDemand is D_P — how many players at each position this league drafts in
// one draft — read off the same cached drafts the room curve uses.
//
// It lives in the cmd layer because the engine does no i/o and the adp package
// does not import the engine. The engine just receives the finished table on
// State.Demand, and falls back to the league's lineup shape when this returns
// nil (no cache, offline first run) rather than to an invented constant.
//
// Cheap enough to call at every launch: three json files off the disk, and a
// completed draft never changes again.
//
// Disk only, and that is the point — mock and live call this before their first
// frame, and a read that can wander onto the network can hang a board that used
// to be instant. `pick6 fetch` is what pulls these files.
func leagueDemand() map[string]int {
	drafts, _ := adp.CachedRoomDrafts(leagueDrafts)
	return adp.PositionDemand(drafts)
}

// printReplacement is the vor baseline as `pick6 fetch` reports it: how deep the
// room drafts each position, what the replacement player at that depth is worth,
// and who he is.
//
// It prints because the number is unintuitive and load-bearing. R(qb) = 314 is
// why quarterbacks stop out-sorting running backs on the frame where you are on
// the clock, and the only way to trust that is to see which quarterback the room
// would have settled for instead.
//
// The number comes from the engine itself, over a throwaway state holding the
// board fetch just built — a second implementation here would be a second thing
// to keep in step, and this printout exists precisely to verify the first one.
func printReplacement(players map[string]*adp.Player) {
	// Not leagueDemand: fetch is the command allowed to PULL these drafts, and it
	// is the run that warms the cache the board then reads offline. Calling the
	// board's disk-only version here would report "no cached drafts" on a fresh
	// machine and only start working on the second fetch.
	drafts, _ := adp.LoadRoomDrafts(leagueDrafts)
	demand := adp.PositionDemand(drafts)
	source := "your league's own drafts"
	if len(demand) == 0 {
		source = "the league lineup shape (no cached drafts)"
	}
	// Teams and Roster are only read by the engine's lineup-shape fallback, which
	// fires here exactly when the measured table has no entry for a position.
	// fetch has no draft metadata to read a real shape from — live does — so the
	// default stands in, and the row says "shape" when it is the one answering.
	s := engine.State{Players: map[string]engine.Player{}, Demand: demand,
		Teams: 12, Roster: engine.DefaultRoster}
	byPos := map[string][]*adp.Player{}
	for _, p := range players {
		s.Players[p.SleeperID] = engine.Player{ID: p.SleeperID, Pos: p.Pos, Value: p.Value}
		if p.Value > 0 {
			byPos[p.Pos] = append(byPos[p.Pos], p)
		}
	}
	for _, g := range byPos {
		// Value desc then adp asc then id, the same comparator engine.Available
		// uses — and not value alone: k/def values are borrowed off a curve and tie
		// constantly (four defenses share 148 today), so a bare value sort over map
		// order names a different replacement player on every run.
		sort.Slice(g, func(i, j int) bool {
			if g[i].Value != g[j].Value {
				return g[i].Value > g[j].Value
			}
			if g[i].ADP != g[j].ADP {
				return g[i].ADP < g[j].ADP
			}
			return g[i].SleeperID < g[j].SleeperID
		})
	}

	fmt.Printf("\nvor baseline — replacement level per position, from %s:\n", source)
	fmt.Printf("  %-5s %8s %6s %8s  %s\n",
		"pos", "depth", "valued", "r(pos)", "the player you'd have settled for")
	for _, pos := range []string{"QB", "RB", "WR", "TE", "K", "DEF"} {
		g := byPos[pos]
		// The depth the ENGINE indexed at, not the raw measured count: a position
		// whose bench cannot score (qb in a 1qb league, k, def) prices replacement
		// at startable slots however many the room drafts, and a printout indexing
		// by the measured count would name a player the engine never priced.
		d, r := s.ReplacementIndex(pos), s.Replacement(pos)
		who := fmt.Sprintf("nobody — the board values only %d, so replacement is free", len(g))
		if len(g) >= d && d > 0 {
			at := g[d-1]
			who = fmt.Sprintf("%s (adp %.1f)", trunc(strings.ToLower(at.Name), 24), at.ADP)
			if float64(at.Value) != r {
				// The engine ranks the same pool the same way, so this cannot
				// happen — and if it ever does, the name below is describing a
				// different player than the number beside it.
				who += fmt.Sprintf("  [!] engine says %.0f, this row says %d", r, at.Value)
			}
		}
		depth := dash(d)
		switch {
		case demand[pos] == 0:
			// No cached draft reached the position, so State.Demand has no entry
			// and the engine used its lineup-shape fallback for this row.
			depth = "shape"
		case d != demand[pos]:
			// The two-index rule overrode the measurement: bench bodies at this
			// position score nothing, so replacement sits at the worst starter.
			depth = fmt.Sprintf("%d st", d)
		}
		fmt.Printf("  %-5s %8s %6d %8.0f  %s\n", strings.ToLower(pos), depth, len(g), r, who)
	}
	fmt.Println("  depth is how deep replacement sits: the room's median drafted count where bench")
	fmt.Println("  bodies can score (rb/wr/te), startable slots where they cannot (qb/k/def, marked")
	fmt.Println("  \"st\"). r(pos) is the value of the depth-th best one on the pre-draft board.")
}

// printKDefValues shows what the k/def anchor produced, because a synthesized
// value is exactly the kind of number that should never reach a board
// unexplained. Head and tail of the list, and the rule in one line.
func printKDefValues(players map[string]*adp.Player) {
	var list []*adp.Player
	tiered := 0
	for _, p := range players {
		if p.Pos != "K" && p.Pos != "DEF" {
			continue
		}
		list = append(list, p)
		if p.Tier != 0 {
			tiered++
		}
	}
	if len(list) == 0 {
		return
	}
	// Two kickers really do share an adp (146.5 today), so the id breaks the tie
	// or the head-and-tail rows below swap between runs.
	sort.Slice(list, func(i, j int) bool {
		if list[i].ADP != list[j].ADP {
			return list[i].ADP < list[j].ADP
		}
		return list[i].SleeperID < list[j].SleeperID
	})

	fmt.Printf("\nk/def value — anchored to the skill curve by adp rank (%d players, %d tiered):\n",
		len(list), tiered)
	for i, p := range list {
		if i >= 3 && i < len(list)-3 {
			continue // enough to show the curve's regime at both ends
		}
		if i == len(list)-3 {
			fmt.Printf("  %s\n", strings.Repeat("·", 3))
		}
		fmt.Printf("  %-24s %-4s adp %6.1f  value %6d  tier %s\n",
			trunc(strings.ToLower(p.Name), 24), strings.ToLower(p.Pos), p.ADP, p.Value, dash(p.Tier))
	}
	fmt.Println("  value = the value of the k-th best skill player, where k is how many of them are")
	fmt.Println("  priced ahead of him. monotone by construction and exactly on the imported curve;")
	fmt.Println("  interpolating between adp neighbours is neither (value is not monotone in adp).")
	if tiered > 0 {
		fmt.Println("  warning: a kicker or defense carries a tier. cliff logic only skips tier 0.")
	} else {
		fmt.Println("  they stay untiered on purpose, so cliff logic keeps skipping them.")
	}
}
