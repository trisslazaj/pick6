package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/trisslazaj/pick6/internal/engine"
)

// sport.go is the whole of "which draft is this", in one value.
//
// The alternative was an `if fpl` at each of the two dozen sites that differ —
// the board's filename, the lineup vocabulary, the default team count, whether a
// room curve exists, whether meta.json is ours to read — and two dozen
// independent conditionals is how a sport ends up half-applied: the board reads
// the right file and the footer still claims the other one's age. One struct,
// built once from the flag, threaded to the sites that ask.
//
// Everything that is NOT here is deliberate. The engine takes no sport: it reads
// the roster and derives what it needs (see engine/positions.go), which is why
// f1 could land without this file existing.
type sport struct {
	name string // "nfl" | "fpl"

	board  string        // the cache file the board is read from
	roster engine.Roster // the default lineup, and with it the position vocabulary
	teams  int           // the default league size
	slots  []string      // what -lineup accepts, in the order the error message lists them

	// The nfl-only machinery, each gated by its own bit so a reader can see what
	// fpl is missing rather than inferring it. All four are measurements this
	// tool made from cached sleeper drafts, and fpl draft leagues are per-season
	// with sequential ids — there is no history to measure from, this season.
	room   bool // the rank->pick room warp
	escape bool // the off-board escape rate the sim draws against
	demand bool // measured positional demand, for vor's replacement index
	meta   bool // meta.json, and with it the footer's board-age clause
}

var (
	nfl = sport{
		name:   "nfl",
		board:  "players.json",
		roster: engine.DefaultRoster,
		teams:  12,
		slots:  []string{"QB", "RB", "WR", "TE", "K", "DEF", "FLEX", "SUPERFLEX"},
		room:   true, escape: true, demand: true, meta: true,
	}
	// fpl carries no flex slot on purpose: a squad is a quota, and a lineup that
	// named one would give needSlots a slot the quota rule cannot reason about.
	fplSport = sport{
		name:   "fpl",
		board:  "players_fpl.json",
		roster: engine.FPLRoster,
		teams:  10,
		slots:  []string{"GKP", "DEF", "MID", "FWD"},
	}
)

// sportFlag declares -sport on the commands that have a second sport to offer.
// Deliberately NOT part of flagSet(): calibrate, regret and scout are nfl-only
// by construction — they need cached sleeper drafts, an era-correct adp board
// and a measured room curve, none of which fpl has — and a flag they silently
// accepted would be a promise none of them can keep.
func sportFlag(fs *flag.FlagSet) *string {
	return fs.String("sport", "nfl", "which draft: nfl or fpl")
}

// resolveSport turns the flag into the value.
func resolveSport(name string) (sport, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "nfl":
		return nfl, nil
	case "fpl":
		return fplSport, nil
	}
	return sport{}, fmt.Errorf("unknown sport %q — nfl or fpl", name)
}

// fetchCmd is what to tell somebody whose board is missing. Naming the bare
// command under -sport fpl would send them to build the nfl board and leave the
// one they asked for still absent.
func (sp sport) fetchCmd() string {
	if sp.name == nfl.name {
		return "`pick6 fetch`"
	}
	return "`pick6 fetch -sport " + sp.name + "`"
}

// knownSlot reports whether -lineup may name a slot.
func (sp sport) knownSlot(s string) bool {
	for _, k := range sp.slots {
		if k == s {
			return true
		}
	}
	return false
}

// slotVocab is the lineup vocabulary as the error message prints it.
func (sp sport) slotVocab() string {
	return strings.ToLower(strings.Join(sp.slots, " "))
}

// priceNoun is what this sport calls the number in the price column.
//
// Nfl's is an average draft position in pick units, measured over a thousand
// drafts. Fpl's is fpl's own draft_rank — an ordering, not a mean — and calling
// it adp on a screen would be claiming a measurement nobody made. The two are
// comparable enough in a ten-team draft (150 picks over the top 150 ranks) that
// the "N early" arithmetic still reads true, which is why the chip stays and
// only the noun changes.
func (sp sport) priceNoun() string {
	if sp.name == nfl.name {
		return "adp"
	}
	return "rank"
}
