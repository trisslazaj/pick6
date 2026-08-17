package adp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// MetaName is the freshness record fetch writes beside players.json.
//
// A separate file on purpose. players.json is a bare JSON array of Player, and
// both loadBoard and `pick6 tiers` unmarshal it as exactly that; wrapping it in
// an object to carry six fields of provenance would rewrite every reader to
// learn nothing new about any player.
const MetaName = "meta.json"

// Meta is what the board knows about its own age.
//
// The board is a photograph, not a window: adp is a trailing average, injury
// status is whatever the dump said at fetch time, and neither notices a
// saturday night acl. Nothing here blocks anything — it exists so the footer can
// say how old the picture is and the live board can go amber when it's stale,
// and so "refetch on draft morning" has something to check itself against.
type Meta struct {
	FetchedAt time.Time `json:"fetched_at"`

	Format       string `json:"format"`
	Season       int    `json:"season"`
	Players      int    `json:"players"`
	TotalDrafts  int    `json:"total_drafts"`   // drafts behind the primary adp
	ADPWindowEnd string `json:"adp_window_end"` // ffc's own end_date, "2026-07-28"

	// Inputs, by mtime. FetchedAt can be minutes old while the data behind it is
	// days old: every source is disk-cached, so a fetch that hits cache all the
	// way through moves only this timestamp. The two mtimes are what actually
	// changed.
	TiersFile  string    `json:"tiers_file"` // "" when no rankings file was given
	TiersMod   time.Time `json:"tiers_modified"`
	SleeperMod time.Time `json:"sleeper_modified"`

	// PriceFile is the fantasypros export the board's price ORDER came from —
	// separate from TiersFile, which is a rankings csv and a different input
	// entirely. "" when the board stayed on ffc's own price (`-adp ffc`, or no
	// export found for the season).
	PriceFile string    `json:"price_file"`
	PriceMod  time.Time `json:"price_modified"`
}

// Age is how long ago the board was built.
func (m Meta) Age() time.Duration { return time.Since(m.FetchedAt) }

// WindowEnd parses the last day of the adp sample window. ok is false when the
// source didn't report one, which is not an error — it just means the caller
// can't say anything about the window and should say nothing.
func (m Meta) WindowEnd() (time.Time, bool) {
	t, err := time.Parse("2006-01-02", m.ADPWindowEnd)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// WriteMeta writes the freshness record into the cache dir.
func WriteMeta(dir string, m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MetaName), b, 0o644)
}

// LoadMeta reads it back. A missing or unreadable file is an error the caller is
// expected to swallow: a board written before this file existed still draws
// fine, it just can't tell you how old it is.
func LoadMeta(dir string) (Meta, error) {
	b, err := os.ReadFile(filepath.Join(dir, MetaName))
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// ModTime is the mtime of a file, or the zero time if it isn't there. Used for
// the input timestamps above, where "no rankings file" and "unreadable rankings
// file" are the same non-event.
func ModTime(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}
