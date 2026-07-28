package adp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

// FantasyCalc is not an ADP source (maybeAdp is null across the board). It is an
// id crosswalk: every entry carries sleeperId and mflId, which lets us join MFL
// straight onto Sleeper without touching a name. It also ships tiers.
//
// Covers QB/RB/WR/TE only — no K or DEF — and about 200 players deep.
const fantasyCalcURL = "https://api.fantasycalc.com/values/current?isDynasty=false&numQbs=%d&numTeams=%d&ppr=%d"

type fantasyCalcEntry struct {
	Player struct {
		Name      string `json:"name"`
		Position  string `json:"position"`
		SleeperID string `json:"sleeperId"`
		MflID     string `json:"mflId"`
		Team      string `json:"maybeTeam"`
	} `json:"player"`
	Value        int  `json:"redraftValue"`
	OverallRank  int  `json:"overallRank"`
	PositionRank int  `json:"positionRank"`
	Tier         *int `json:"maybeTier"`
}

// Crosswalk maps ids between systems, and carries FantasyCalc's tiers.
type Crosswalk struct {
	MflToSleeper map[string]string // mfl id -> sleeper id
	Tier         map[string]int    // sleeper id -> tier
	Value        map[string]int    // sleeper id -> redraft value
	Count        int
}

// FetchCrosswalk pulls FantasyCalc and builds the id bridge.
func FetchCrosswalk(qbs, teams, ppr int) (*Crosswalk, bool, error) {
	url := fmt.Sprintf(fantasyCalcURL, qbs, teams, ppr)
	b, fetched, err := cache.Get("fantasycalc.json", url, 24*time.Hour)
	if err != nil {
		return nil, false, err
	}

	var entries []fantasyCalcEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fetched, fmt.Errorf("fantasycalc: %w", err)
	}

	cw := &Crosswalk{
		MflToSleeper: make(map[string]string, len(entries)),
		Tier:         make(map[string]int, len(entries)),
		Value:        make(map[string]int, len(entries)),
	}
	for _, e := range entries {
		sid := e.Player.SleeperID
		if sid == "" {
			continue
		}
		cw.Count++
		if e.Player.MflID != "" {
			cw.MflToSleeper[e.Player.MflID] = sid
		}
		if e.Tier != nil {
			cw.Tier[sid] = *e.Tier
		}
		cw.Value[sid] = e.Value
	}
	return cw, fetched, nil
}
