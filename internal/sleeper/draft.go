package sleeper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

const apiBase = "https://api.sleeper.app/v1"

// Draft is the subset of Sleeper's draft metadata we use.
type Draft struct {
	DraftID string `json:"draft_id"`
	Type    string `json:"type"`   // must be "snake"
	Status  string `json:"status"` // pre_draft | drafting | paused | complete
	Season  string `json:"season"`
	// LeagueID is empty on a mock draft, which is how `pick6 scout` tells a
	// league's real draft from twelve strangers practising.
	LeagueID string `json:"league_id"`
	// StartTime is epoch millis. The backtester compares it against the adp
	// snapshot's window: prices from a different week are a different market,
	// and that gap belongs in the output rather than in an assumption.
	StartTime int64 `json:"start_time"`
	Settings  struct {
		Teams          int `json:"teams"`
		Rounds         int `json:"rounds"`
		ReversalRound  int `json:"reversal_round"`
		SlotsQB        int `json:"slots_qb"`
		SlotsRB        int `json:"slots_rb"`
		SlotsWR        int `json:"slots_wr"`
		SlotsTE        int `json:"slots_te"`
		SlotsFlex      int `json:"slots_flex"`
		SlotsSuperFlex int `json:"slots_super_flex"`
		SlotsK         int `json:"slots_k"`
		SlotsDEF       int `json:"slots_def"`
		SlotsBench     int `json:"slots_bn"`
	} `json:"settings"`
	// Metadata carries the league's display name and its scoring type, which
	// live here rather than in settings. The backtester prints both: three
	// cached drafts turn out to be three different leagues, so "2025" is not a
	// name for a fold, and a draft whose scoring_type is not half_ppr would be
	// scored against the wrong adp format entirely.
	Metadata struct {
		Name        string `json:"name"`
		ScoringType string `json:"scoring_type"`
	} `json:"metadata"`
	// DraftOrder maps user_id -> draft slot (1-indexed). Null before a draft
	// is seeded, so callers must cope with it being empty.
	DraftOrder map[string]int `json:"draft_order"`
	// SlotToRosterID maps draft slot -> roster id. Keys are strings.
	SlotToRosterID map[string]int `json:"slot_to_roster_id"`
}

// DraftPick is one selection from the live feed.
type DraftPick struct {
	PickNo    int `json:"pick_no"`
	Round     int `json:"round"`
	DraftSlot int `json:"draft_slot"`
	// RosterID is who actually receives the player. It differs from the roster
	// that owns DraftSlot whenever the pick has been traded.
	RosterID int    `json:"roster_id"`
	PlayerID string `json:"player_id"`
	PickedBy string `json:"picked_by"`
	Metadata struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Position  string `json:"position"`
		Team      string `json:"team"`
	} `json:"metadata"`
}

// User is the minimum needed to turn a username into an id.
type User struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// GetDraft fetches draft metadata. Never cached — during a draft this is live.
func GetDraft(id string) (*Draft, error) {
	var d Draft
	if err := getJSON(apiBase+"/draft/"+id, &d); err != nil {
		return nil, err
	}
	if d.DraftID == "" {
		return nil, fmt.Errorf("draft %s not found", id)
	}
	return &d, nil
}

// GetPicks fetches every pick made so far, sorted by pick number.
//
// Sleeper already returns them in order, but sorting is cheap and the whole
// pick-application path assumes it — a single out-of-order pick would corrupt
// the roster it lands on.
func GetPicks(id string) ([]DraftPick, error) {
	return getPicks(apiBase + "/draft/" + id + "/picks")
}

// GetPicksFresh bypasses the CDN cache, for use during a running draft.
//
// The picks endpoint sits behind Cloudflare with
// `cache-control: public, s-maxage=15, stale-while-revalidate=300`, so a plain
// request can return data 15+ seconds old no matter how fast you poll — measured
// at `age: 26` with `cf-cache-status: HIT`. A `Cache-Control: no-cache` request
// header is ignored. A unique query parameter is the only thing that produces a
// MISS, and during a live draft that staleness is the difference between seeing
// your guy get taken and finding out two picks later.
//
// Only used while a draft is actually running: at a 3s interval that's 20
// requests a minute for one user, which is a rounding error to Sleeper, and it
// stops entirely once the draft completes.
func GetPicksFresh(id string, nonce int64) ([]DraftPick, error) {
	return getPicks(fmt.Sprintf("%s/draft/%s/picks?_=%d", apiBase, id, nonce))
}

func getPicks(url string) ([]DraftPick, error) {
	var picks []DraftPick
	if err := getJSON(url, &picks); err != nil {
		return nil, err
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].PickNo < picks[j].PickNo })
	return picks, nil
}

// completedDraftAge is the cache lifetime for a finished draft. A completed
// draft's metadata and picks never change again, so the only reason not to use
// forever is that the file should eventually age out of the cache dir; 30 days
// covers a whole offseason of backtest runs on one fetch.
const completedDraftAge = 30 * 24 * time.Hour

// DraftCacheAge is how long a draft may be trusted off disk, given the status
// the directory listing reported for it.
//
// completedDraftAge is only honest about a draft that is over. `pick6 scout`
// walks every draft it can find, and this user's 2026 league sat at "pre_draft"
// with an empty draft_order and an empty pick list on 2026-07-29 — cached for 30
// days, that empty answer would still be what scout read all through September,
// hiding the one draft the profile is actually for. Anything not complete gets
// the directory's own daily lifetime instead, so it self-heals the day after the
// draft ends.
func DraftCacheAge(status string) time.Duration {
	if status == "complete" {
		return completedDraftAge
	}
	return directoryAge
}

// CachedDraft is GetDraft off disk, for the historical drafts the backtester
// replays. Live drafting must keep using GetDraft — status and draft_order
// change while a draft is running.
//
// "Cached" means may-refresh: a copy older than maxAge is refetched, and only a
// failed fetch falls back to the stale bytes. Readers on a render path want
// DiskDraft instead.
func CachedDraft(id string) (*Draft, error) { return CachedDraftAge(id, completedDraftAge) }

// CachedDraftAge is CachedDraft with the lifetime the caller's own knowledge of
// the draft justifies. See DraftCacheAge.
func CachedDraftAge(id string, maxAge time.Duration) (*Draft, error) {
	var d Draft
	if err := cachedJSON("draft_"+id+".json", apiBase+"/draft/"+id, maxAge, &d); err != nil {
		return nil, err
	}
	if d.DraftID == "" {
		return nil, fmt.Errorf("draft %s not found", id)
	}
	return &d, nil
}

// CachedPicks is GetPicks off disk, sorted by pick number for the same reason
// getPicks sorts: one out-of-order pick would corrupt the roster it lands on.
func CachedPicks(id string) ([]DraftPick, error) { return CachedPicksAge(id, completedDraftAge) }

// CachedPicksAge is CachedPicks with a caller-chosen lifetime.
func CachedPicksAge(id string, maxAge time.Duration) ([]DraftPick, error) {
	var picks []DraftPick
	err := cachedJSON("draft_"+id+"_picks.json", apiBase+"/draft/"+id+"/picks", maxAge, &picks)
	if err != nil {
		return nil, err
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].PickNo < picks[j].PickNo })
	return picks, nil
}

// DiskDraft and DiskPicks read a completed draft off disk and NEVER touch the
// network, whatever the file's age.
//
// The board uses these. cache.Get refetches once its copy ages out, with a 120s
// client timeout and a fallback to the stale bytes only when the request
// ERRORS — so against a host that blackholes packets rather than refusing them,
// six month-old draft files are twelve minutes of blank screen before `pick6
// mock` renders anything. A draft party's wifi is exactly that kind of bad, and
// the room curve is a nicety even though it is now on by default: a missing file
// costs the board a lineup-shape replacement level and the room's prices, and
// what is left is a board on raw national adp — degraded, still correct, still
// instant. `pick6 fetch` is where these files get pulled.
func DiskDraft(id string) (*Draft, error) {
	var d Draft
	if err := diskJSON("draft_"+id+".json", &d); err != nil {
		return nil, err
	}
	if d.DraftID == "" {
		return nil, fmt.Errorf("draft %s not found", id)
	}
	return &d, nil
}

func DiskPicks(id string) ([]DraftPick, error) {
	var picks []DraftPick
	if err := diskJSON("draft_"+id+"_picks.json", &picks); err != nil {
		return nil, err
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].PickNo < picks[j].PickNo })
	return picks, nil
}

func cachedJSON(name, url string, maxAge time.Duration, v any) error {
	b, _, err := cache.Get(name, url, maxAge)
	if err != nil {
		return err
	}
	return decodeCached(name, b, v)
}

func diskJSON(name string, v any) error {
	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("%s is not cached — run `pick6 fetch`", name)
	}
	return decodeCached(name, b, v)
}

func decodeCached(name string, b []byte, v any) error {
	// Sleeper answers unknown ids with a bare `null` and a 200, so the cache
	// happily stores it; catch it here rather than returning an empty result.
	if string(b) == "null" {
		return fmt.Errorf("%s: not found", name)
	}
	return json.Unmarshal(b, v)
}

// GetUser resolves a username (or user id) to a user.
func GetUser(nameOrID string) (*User, error) {
	var u User
	if err := getJSON(apiBase+"/user/"+nameOrID, &u); err != nil {
		return nil, err
	}
	if u.UserID == "" {
		return nil, fmt.Errorf("no sleeper user %q", nameOrID)
	}
	return &u, nil
}

// Validate rejects drafts the engine's snake math does not model.
func (d *Draft) Validate() error {
	if d.Type != "snake" {
		return fmt.Errorf("draft type is %q; only snake is supported", d.Type)
	}
	// Third-round reversal changes the pick order and would silently desync every
	// survival number. Refuse rather than mispredict.
	if d.Settings.ReversalRound != 0 {
		return fmt.Errorf("draft uses %d-round reversal, which is not supported",
			d.Settings.ReversalRound)
	}
	if d.Settings.Teams < 2 || d.Settings.Rounds < 1 {
		return fmt.Errorf("draft reports %d teams and %d rounds",
			d.Settings.Teams, d.Settings.Rounds)
	}
	return nil
}

// RosterSlots builds the starting lineup from the draft's own settings, in
// conventional display order. Returns nil when the settings carry no lineup, so
// callers can fall back to the league default.
func (d *Draft) RosterSlots() []string {
	s := d.Settings
	var out []string
	add := func(label string, n int) {
		for i := 0; i < n; i++ {
			out = append(out, label)
		}
	}
	add("QB", s.SlotsQB)
	add("RB", s.SlotsRB)
	add("WR", s.SlotsWR)
	add("TE", s.SlotsTE)
	add("FLEX", s.SlotsFlex)
	add("SUPERFLEX", s.SlotsSuperFlex)
	add("K", s.SlotsK)
	add("DEF", s.SlotsDEF)
	return out
}

// OwnerSlot returns the draft slot whose roster receives this pick.
//
// Normally that's just DraftSlot, but draft picks get traded: in one of this
// user's real 2025 drafts, 9 of 192 picks went to a roster other than the one
// owning that slot. Attributing by DraftSlot alone puts those players on the
// wrong team — including yours, so a pick you traded for would never appear on
// your roster. Falls back to DraftSlot when the mapping is unavailable.
func (d *Draft) OwnerSlot(p DraftPick) int {
	if p.RosterID == 0 || len(d.SlotToRosterID) == 0 {
		return p.DraftSlot
	}
	for slot, roster := range d.SlotToRosterID {
		if roster == p.RosterID {
			if n, err := strconv.Atoi(slot); err == nil {
				return n
			}
		}
	}
	return p.DraftSlot
}

// SlotOf returns a user's draft slot. Ok is false before the order is seeded.
func (d *Draft) SlotOf(userID string) (int, bool) {
	slot, ok := d.DraftOrder[userID]
	return slot, ok && slot > 0
}

func getJSON(url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pick6/0.1 (+https://github.com/trisslazaj/pick6)")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// Sleeper returns a bare `null` for unknown ids rather than a 404.
	if string(b) == "null" {
		return fmt.Errorf("%s: not found", url)
	}
	return json.Unmarshal(b, v)
}
