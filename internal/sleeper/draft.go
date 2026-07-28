package sleeper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

const apiBase = "https://api.sleeper.app/v1"

// Draft is the subset of Sleeper's draft metadata we use.
type Draft struct {
	DraftID  string `json:"draft_id"`
	Type     string `json:"type"`   // must be "snake"
	Status   string `json:"status"` // pre_draft | drafting | paused | complete
	Season   string `json:"season"`
	Settings struct {
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
	// DraftOrder maps user_id -> draft slot (1-indexed). Null before a draft
	// is seeded, so callers must cope with it being empty.
	DraftOrder map[string]int `json:"draft_order"`
}

// DraftPick is one selection from the live feed.
type DraftPick struct {
	PickNo    int    `json:"pick_no"`
	Round     int    `json:"round"`
	DraftSlot int    `json:"draft_slot"`
	PlayerID  string `json:"player_id"`
	PickedBy  string `json:"picked_by"`
	Metadata  struct {
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
	var picks []DraftPick
	if err := getJSON(apiBase+"/draft/"+id+"/picks", &picks); err != nil {
		return nil, err
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].PickNo < picks[j].PickNo })
	return picks, nil
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
