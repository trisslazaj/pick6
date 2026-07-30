package sleeper

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

// The league directory: user -> leagues -> drafts, plus the manager names.
//
// `pick6 scout` walks these to profile the room. All of them are
// unauthenticated — verified 2026-07-29 against this user's own leagues, which
// returned 1 league for 2024, 3 for 2025 and 4 for 2026 — and all of them are
// cached, because the profile is a historical read and a draft party's wifi is
// bad.

// directoryAge is the cache lifetime for the directory endpoints below.
//
// A day, because these change on human timescales: a league is created once, its
// membership settles before the draft, and display names change approximately
// never. The drafts and the picks themselves keep the 30-day completedDraftAge —
// a finished draft is immutable. And cache.Get falls back to the stale file when
// a fetch fails, so every reader here is offline after the first run.
const directoryAge = 24 * time.Hour

// League is the subset of a sleeper league the scout reads.
//
// The league NAME is deliberately not read: nothing downstream keys on it, the
// printout identifies a league by id and season, and a field nobody renders is a
// field that cannot render wrong.
type League struct {
	LeagueID string `json:"league_id"`
	Season   string `json:"season"`
	Status   string `json:"status"`
	Settings struct {
		// Type is 0 redraft, 1 keeper, 2 dynasty. Observed on this user's
		// leagues: the three 12-team half-ppr leagues are 0, both dynasty
		// leagues are 2. It is the only way to tell a dynasty startup from a
		// redraft draft, and the difference matters — a 25-round dynasty
		// startup drafts no kickers at all, so blending one into "when does he
		// take his first k" would report a tendency nobody has.
		Type     int `json:"type"`
		NumTeams int `json:"num_teams"`
	} `json:"settings"`
}

// Dynasty reports a league whose drafts answer a different question than a
// redraft draft does. Keeper (type 1) counts too: a keeper league's board starts
// with the best players already off it.
func (l League) Dynasty() bool { return l.Settings.Type != 0 }

// LeagueUser is a manager as his own league lists him. TeamName is optional —
// 5 of the 12 managers in the 2024 league have none, so callers fall back to
// DisplayName rather than rendering a blank.
type LeagueUser struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Metadata    struct {
		TeamName string `json:"team_name"`
	} `json:"metadata"`
}

// Name is the manager's display name, or his user id if sleeper has neither.
// Never empty, because it ends up in a table column.
func (u LeagueUser) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.UserID
}

// CachedUser resolves a username (or user id) to a user, off disk.
//
// GetUser stays uncached for the live path, where a typo should not be
// remembered for a day. The scout wants the opposite: one lookup ever, then
// offline.
func CachedUser(nameOrID string) (*User, error) {
	var u User
	if err := cachedDirJSON("user_"+safeName(nameOrID)+".json", apiBase+"/user/"+nameOrID, &u); err != nil {
		return nil, err
	}
	if u.UserID == "" {
		return nil, fmt.Errorf("no sleeper user %q", nameOrID)
	}
	return &u, nil
}

// UserLeagues lists the nfl leagues a user played in for one season. An empty
// list is the honest answer for a season he sat out, not an error.
func UserLeagues(userID string, season int) ([]League, error) {
	var out []League
	name := fmt.Sprintf("leagues_%s_%d.json", safeName(userID), season)
	url := fmt.Sprintf("%s/user/%s/leagues/nfl/%d", apiBase, userID, season)
	return out, cachedDirJSON(name, url, &out)
}

// UserDrafts lists every draft a user took part in for one season, including any
// mock drafts he ran. Mocks carry no league_id — they are strangers drafting, so
// the scout records them and leaves them out of the room's profile.
func UserDrafts(userID string, season int) ([]Draft, error) {
	var out []Draft
	name := fmt.Sprintf("userdrafts_%s_%d.json", safeName(userID), season)
	url := fmt.Sprintf("%s/user/%s/drafts/nfl/%d", apiBase, userID, season)
	return out, cachedDirJSON(name, url, &out)
}

// LeagueDrafts lists a league's own drafts. This is the authoritative list for a
// league: a draft the user was not personally in still shaped the room.
func LeagueDrafts(leagueID string) ([]Draft, error) {
	var out []Draft
	name := "league_" + safeName(leagueID) + "_drafts.json"
	return out, cachedDirJSON(name, apiBase+"/league/"+leagueID+"/drafts", &out)
}

// LeagueUsers lists a league's managers, for their display names.
func LeagueUsers(leagueID string) ([]LeagueUser, error) {
	var out []LeagueUser
	name := "league_" + safeName(leagueID) + "_users.json"
	return out, cachedDirJSON(name, apiBase+"/league/"+leagueID+"/users", &out)
}

// cachedDirJSON reads one directory endpoint through the disk cache.
//
// Unlike cachedJSON it does NOT treat a bare `null` as an error. The array
// endpoints answer `[]` for a season the user sat out (verified: 2019 returns
// `[]`), but sleeper answers some unknown ids with `null`, and for a list those
// two are the same thing — an empty result. The one object caller here
// (CachedUser) checks its own required field instead, which is a better error
// than "not found" anyway.
func cachedDirJSON(name, url string, v any) error {
	b, _, err := cache.Get(name, url, directoryAge)
	if err != nil {
		return err
	}
	if string(b) == "null" {
		return nil
	}
	return json.Unmarshal(b, v)
}

// safeName keeps an id fit for a filename. A username reaches this package
// straight off the command line and sleeper does not promise what characters one
// may contain — a '/' in there would write outside the cache dir.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}
