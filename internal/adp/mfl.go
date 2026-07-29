package adp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

// MFL is the secondary ADP source. Real drafts, free, no auth, but it needs two
// calls because the ADP feed returns bare MFL player ids, and the sample is much
// smaller than FFC's. Weight it below FFC.
const (
	mflADPURL     = "https://api.myfantasyleague.com/%d/export?TYPE=adp&FCOUNT=%d&IS_PPR=%d&IS_MOCK=-1&JSON=1"
	mflPlayersURL = "https://api.myfantasyleague.com/%d/export?TYPE=players&DETAILS=1&JSON=1"
)

// MFL encodes every numeric field as a string, so everything below is a string.
type mflADPResponse struct {
	ADP struct {
		TotalDrafts string `json:"totalDrafts"`
		Player      []struct {
			ID          string `json:"id"`
			AveragePick string `json:"averagePick"`
			Rank        string `json:"rank"`
			MinPick     string `json:"minPick"`
			MaxPick     string `json:"maxPick"`
			DraftsIn    string `json:"draftsSelectedIn"`
		} `json:"player"`
	} `json:"adp"`
}

type mflPlayersResponse struct {
	Players struct {
		Player []struct {
			ID       string `json:"id"`
			Name     string `json:"name"` // "Last, First"
			Position string `json:"position"`
			Team     string `json:"team"`
		} `json:"player"`
	} `json:"players"`
}

// MFLResult is MFL ADP with names already resolved.
type MFLResult struct {
	TotalDrafts int
	Entries     []Entry
}

// FetchMFL pulls the ADP feed and the player dictionary, and joins them.
// ppr: 0 = standard, 1 = full PPR. MFL has no half-PPR flag, so half-PPR leagues
// should read this as a rough second opinion rather than a format match.
func FetchMFL(year, teams, ppr int) (MFLResult, bool, error) {
	adpB, f1, err := cache.Get(
		fmt.Sprintf("mfl_adp_%d.json", year),
		fmt.Sprintf(mflADPURL, year, teams, ppr),
		12*time.Hour)
	if err != nil {
		return MFLResult{}, false, err
	}
	plB, f2, err := cache.Get(
		fmt.Sprintf("mfl_players_%d.json", year),
		fmt.Sprintf(mflPlayersURL, year),
		7*24*time.Hour) // the dictionary barely changes
	if err != nil {
		return MFLResult{}, f1, err
	}

	var a mflADPResponse
	if err := json.Unmarshal(adpB, &a); err != nil {
		return MFLResult{}, f1 || f2, fmt.Errorf("mfl adp: %w", err)
	}
	var p mflPlayersResponse
	if err := json.Unmarshal(plB, &p); err != nil {
		return MFLResult{}, f1 || f2, fmt.Errorf("mfl players: %w", err)
	}

	type info struct{ name, pos, team string }
	dict := make(map[string]info, len(p.Players.Player))
	for _, pl := range p.Players.Player {
		dict[pl.ID] = info{flipName(pl.Name), pl.Position, pl.Team}
	}

	total, _ := strconv.Atoi(a.ADP.TotalDrafts)
	out := MFLResult{TotalDrafts: total, Entries: make([]Entry, 0, len(a.ADP.Player))}
	for _, e := range a.ADP.Player {
		d, ok := dict[e.ID]
		if !ok {
			continue // id we can't name is an id we can't match
		}
		adp, err := strconv.ParseFloat(e.AveragePick, 64)
		if err != nil {
			continue
		}
		sample, _ := strconv.Atoi(e.DraftsIn)
		out.Entries = append(out.Entries, Entry{
			Name:         d.name,
			Pos:          d.pos,
			Team:         d.team,
			ADP:          adp,
			TimesDrafted: sample,
		})
	}
	return out, f1 || f2, nil
}

// flipName turns MFL's "Robinson, Bijan" into "Bijan Robinson".
func flipName(s string) string {
	last, first, ok := strings.Cut(s, ",")
	if !ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(first) + " " + strings.TrimSpace(last)
}
