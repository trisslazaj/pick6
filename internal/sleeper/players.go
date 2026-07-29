// Package sleeper talks to the Sleeper API. No auth on anything we use.
package sleeper

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

const playersURL = "https://api.sleeper.app/v1/players/nfl"

// PlayersCache is the dump's filename in the cache dir. Exported because the
// board's freshness record reports its mtime: the dump is 14.6 MB and refreshes
// at most daily, so "when did the player data last actually change" is a
// different question from "when did fetch last run".
const PlayersCache = "sleeper_players.json"

// FantasyPositions is the only set we care about. The dump has 12k players;
// filtering to these plus active drops it to ~3.2k and kills most fuzzy-match noise.
var FantasyPositions = map[string]bool{
	"QB": true, "RB": true, "WR": true, "TE": true, "K": true, "DEF": true,
}

// Player is the subset of the Sleeper dump we use.
//
// The last three fields are a truth layer and nothing more: they travel to the
// board so a human can see them, and no math anywhere reads them. The obvious
// next thought — "discount the injured guy's value" — is explicitly wrong here.
// We make no projections of our own; every value is imported, and quietly
// marking one down would invent a number the source never said.
type Player struct {
	PlayerID  string `json:"player_id"`
	FullName  string `json:"full_name"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
	Team      string `json:"team"`
	ByeWeek   int    `json:"-"`
	Active    bool   `json:"active"`

	// InjuryStatus is null for 3,090 of the 3,223 active fantasy players, so ""
	// is the normal case and means nothing is wrong. Observed values across the
	// full dump: Questionable, PUP, NA, IR, Out, Sus, COV, DNR.
	InjuryStatus string `json:"injury_status"`
	// Status is "Active" / "Inactive" / "Injured Reserve", and null for 32 of
	// the active pool. Empty means UNKNOWN, not hurt — calling those 32 injured
	// would be a fabrication.
	Status string `json:"status"`
	// NewsUpdated is an epoch in MILLIseconds, 0 when null (458 of the pool).
	// It is an int here and a *string* in draft-pick metadata; different shape,
	// different code path, don't copy one to the other.
	NewsUpdated int64 `json:"news_updated"`
}

// rawPlayer exists because bye_week comes back as a number, a string, or null
// depending on the player. Observed, not speculative.
type rawPlayer struct {
	Player
	ByeWeek json.RawMessage `json:"bye_week"`
}

// Name returns a usable display name. Defenses have full_name: null and carry
// the city/nickname in first/last instead.
func (p Player) Name() string {
	if p.FullName != "" {
		return p.FullName
	}
	if p.FirstName != "" || p.LastName != "" {
		return p.FirstName + " " + p.LastName
	}
	return p.PlayerID
}

// Players downloads (or reads from cache) the player dump and returns only the
// active fantasy-relevant players, keyed by Sleeper player_id.
func Players() (map[string]Player, bool, error) {
	// Sleeper explicitly asks callers not to pull this more than once a day.
	b, fetched, err := cache.Get(PlayersCache, playersURL, 24*time.Hour)
	if err != nil {
		return nil, false, err
	}

	var raw map[string]rawPlayer
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fetched, err
	}

	out := make(map[string]Player, 3500)
	for id, rp := range raw {
		if !rp.Active || !FantasyPositions[rp.Position] {
			continue
		}
		p := rp.Player
		p.PlayerID = id // the map key is authoritative; the field is sometimes empty
		p.ByeWeek = parseBye(rp.ByeWeek)
		out[id] = p
	}
	return out, fetched, nil
}

func parseBye(m json.RawMessage) int {
	if len(m) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(m, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		n, err := strconv.Atoi(s)
		if err == nil {
			return n
		}
	}
	return 0
}
