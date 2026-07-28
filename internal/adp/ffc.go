package adp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

// FFC is the primary ADP source: real mock drafts, ADP already in pick units,
// and a per-player stdev the engine uses for its survival curve.
//
// Free for personal and commercial use; attribution requested (see README).
// Data refreshes once per day, so don't poll it.
//
// Note: the `teams` query param is cosmetic. It is echoed back in meta.teams but
// the ADP values are identical for 8/10/12/14. There is one ADP per format.
const ffcURL = "https://fantasyfootballcalculator.com/api/v1/adp/%s?teams=%d&year=%d"

// FFCFormats that actually return data. dynasty and rookie come back empty.
var FFCFormats = []string{"half-ppr", "ppr", "standard", "2qb"}

// Comparable reports whether two formats differ only in scoring and can be
// compared pick-for-pick. 2qb is a different roster shape, not a scoring tweak:
// comparing it to a 1QB format just reports that quarterbacks go earlier in
// superflex, which is true, useless, and drowns out the real signal.
func Comparable(a, b string) bool {
	return (a == "2qb") == (b == "2qb")
}

type ffcResponse struct {
	Status string `json:"status"`
	Meta   struct {
		Type        string `json:"type"`
		Teams       int    `json:"teams"`
		Rounds      int    `json:"rounds"`
		TotalDrafts int    `json:"total_drafts"`
		StartDate   string `json:"start_date"`
		EndDate     string `json:"end_date"`
	} `json:"meta"`
	Players []struct {
		Name         string  `json:"name"`
		Position     string  `json:"position"`
		Team         string  `json:"team"`
		ADP          float64 `json:"adp"`
		ADPFormatted string  `json:"adp_formatted"`
		TimesDrafted int     `json:"times_drafted"`
		High         int     `json:"high"`
		Low          int     `json:"low"`
		Stdev        float64 `json:"stdev"`
		Bye          int     `json:"bye"`
	} `json:"players"`
}

// FFCResult is one format's worth of ADP.
type FFCResult struct {
	Format      string
	TotalDrafts int
	Window      string // e.g. "2026-07-21..2026-07-28"
	Entries     []Entry
}

// FetchFFC pulls one scoring format.
func FetchFFC(format string, teams, year int) (FFCResult, bool, error) {
	url := fmt.Sprintf(ffcURL, format, teams, year)
	name := fmt.Sprintf("ffc_%s_%d.json", format, year)

	b, fetched, err := cache.Get(name, url, 12*time.Hour)
	if err != nil {
		return FFCResult{}, false, err
	}

	var r ffcResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return FFCResult{}, fetched, fmt.Errorf("ffc %s: %w", format, err)
	}
	if r.Status != "Success" {
		return FFCResult{}, fetched, fmt.Errorf("ffc %s: status %q", format, r.Status)
	}

	out := FFCResult{
		Format:      format,
		TotalDrafts: r.Meta.TotalDrafts,
		Window:      r.Meta.StartDate + ".." + r.Meta.EndDate,
		Entries:     make([]Entry, 0, len(r.Players)),
	}
	for _, p := range r.Players {
		out.Entries = append(out.Entries, Entry{
			Name:   p.Name,
			Pos:    p.Position, // PK / DEF normalized downstream
			Team:   p.Team,
			ADP:    p.ADP,
			Stdev:  p.Stdev,
			Bye:    p.Bye,
			Sample: p.TimesDrafted,
		})
	}
	return out, fetched, nil
}
