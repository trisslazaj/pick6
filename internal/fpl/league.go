package fpl

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
)

// League is the room: who is in it, how many, and when it drafts.
type League struct {
	Info struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		DraftStatus string `json:"draft_status"` // pre | post
		DraftDT     string `json:"draft_dt"`
		PickLimit   int    `json:"draft_pick_time_limit"`
		MaxEntries  int    `json:"max_entries"`
		Scoring     string `json:"scoring"`
	} `json:"league"`
	Entries []Entry `json:"league_entries"`
}

// Entry is one manager. Note the two ids: the choices feed's `entry` field
// matches EntryID, not ID.
type Entry struct {
	EntryID   int    `json:"entry_id"`
	ID        int    `json:"id"`
	EntryName string `json:"entry_name"`
	FirstName string `json:"player_first_name"`
	LastName  string `json:"player_last_name"`
	ShortName string `json:"short_name"`
}

// Manager is the human's name, which is what a person recognises. The team name
// is theirs to change and half this league still reads CHANGE NAME.
func (e Entry) Manager() string {
	n := e.FirstName
	if e.LastName != "" {
		if n != "" {
			n += " "
		}
		n += e.LastName
	}
	if n == "" {
		return e.EntryName
	}
	return n
}

// Teams is how many managers are actually in the draft.
//
// Not max_entries: the fixture league (2400) advertises 8 and drafted with 7,
// carrying one league_entries row with a null entry_id where the eighth would
// be. Every pick number in the snake math derives from this count, so counting
// the seats that exist beats reading the number of seats that were sold.
func (l *League) Teams() int {
	n := 0
	for _, e := range l.Entries {
		if e.EntryID != 0 {
			n++
		}
	}
	return n
}

// Complete reports whether the draft is over.
func (l *League) Complete() bool { return l.Info.DraftStatus == "post" }

// Choice is one pick from the draft feed.
type Choice struct {
	Element   int    `json:"element"` // bootstrap-static elements[].id
	Entry     int    `json:"entry"`   // league_entries[].entry_id
	EntryName string `json:"entry_name"`
	Index     int    `json:"index"` // OVERALL pick number, 1-indexed — sleeper's pick_no
	Round     int    `json:"round"`
	Pick      int    `json:"pick"` // 1-indexed position within the round
	// WasAuto is FPL telling you outright that the clock ran out, which sleeper
	// never does. Unused by the board today; kept because it is free and it is
	// the one thing a room's fingerprint most wants next season.
	WasAuto bool   `json:"was_auto"`
	Time    string `json:"choice_time"`
}

// choicesResp is the feed's envelope.
type choicesResp struct {
	Choices []Choice `json:"choices"`
}

// GetLeague fetches the room. Never cached by us, and bypassed at the edge —
// see nonced().
func GetLeague(leagueID string) (*League, error) {
	var l League
	if err := getJSON(nonced(apiBase+"/league/"+leagueID+"/details"), &l); err != nil {
		return nil, err
	}
	if len(l.Entries) == 0 {
		return nil, fmt.Errorf("league %s has no entries — check the id", leagueID)
	}
	return &l, nil
}

// GetChoices fetches the pick feed, sorted by overall pick number.
//
// THE ID IS THE LEAGUE ID, not the draft id, however much the path says
// "draft". league/4250/details names a draft 4512, and draft/4512/choices
// returns a REAL FEED — for a stranger's league 4512, which has its own draft
// 4790. It does not error, it does not come back empty, and it would poll
// somebody else's draft all night looking exactly like it was working. Pass the
// league id everywhere and never the draft id.
func GetChoices(leagueID string) ([]Choice, error) {
	var r choicesResp
	if err := getJSON(nonced(apiBase+"/draft/"+leagueID+"/choices"), &r); err != nil {
		return nil, err
	}
	// Sorted anyway: the feed arrives in order, and one out-of-order pick would
	// corrupt the roster it lands on.
	sort.Slice(r.Choices, func(i, j int) bool { return r.Choices[i].Index < r.Choices[j].Index })
	return r.Choices, nil
}

// Seats maps entry id -> 1-indexed draft slot, learned from round one.
//
// Round one IS the draft order — seat n is whoever picks nth — so the map
// builds itself as the first round happens and is complete the moment it ends.
// Before that it is partial, which is exactly right: a pick can only come from
// an entry that has already picked.
func Seats(choices []Choice) map[int]int {
	seats := map[int]int{}
	for _, c := range choices {
		if c.Round == 1 && c.Pick > 0 {
			seats[c.Entry] = c.Pick
		}
	}
	return seats
}

// SeatOfEntry finds a manager's seat once round one has reached them.
func SeatOfEntry(choices []Choice, entryID int) (int, bool) {
	s, ok := Seats(choices)[entryID]
	return s, ok
}

// FindEntry resolves a name — the manager's, or their team's — to an entry id.
// Match is case-insensitive and on any part of either name, because "triss" is
// what a person types.
func (l *League) FindEntry(name string) (Entry, error) {
	if id, err := strconv.Atoi(name); err == nil {
		for _, e := range l.Entries {
			if e.EntryID == id {
				return e, nil
			}
		}
		return Entry{}, fmt.Errorf("no entry %d in this league", id)
	}
	want := normalize(name)
	var hits []Entry
	for _, e := range l.Entries {
		if e.EntryID == 0 {
			continue
		}
		if contains(normalize(e.Manager()), want) || contains(normalize(e.EntryName), want) {
			hits = append(hits, e)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Entry{}, fmt.Errorf("no manager matching %q — try one of: %s", name, l.names())
	default:
		return Entry{}, fmt.Errorf("%q matches %d managers — be more specific", name, len(hits))
	}
}

func (l *League) names() string {
	var out []string
	for _, e := range l.Entries {
		if e.EntryID != 0 {
			out = append(out, e.Manager())
		}
	}
	return joinComma(out)
}

func getJSON(url string, v any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pick6/0.1 (+https://github.com/trisslazaj/pick6)")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
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
	return json.Unmarshal(b, v)
}

// nonce is the cache-buster's counter, SEEDED RANDOMLY rather than started at
// zero — the same reasoning the sleeper feed's carries. A counter from zero
// makes the first request of every process `?_=1`, the same url every time, so
// a caller that shells out once per poll reads the edge's copy forever. Only a
// long-lived process increments past it, and headless polling is how this tool
// gets exercised.
var nonce atomic.Int64

func init() { nonce.Store(rand.Int64()) }

// nonced appends a unique query parameter, because the fpl edge ignores the
// request's cache directives AND its own response's.
//
// MEASURED 2026-08-18, and this is not the sleeper trick applied on faith.
// `league/{id}/details` answers `cache-control: max-age=0, no-cache, no-store,
// must-revalidate` and is then served by varnish with `x-cache: HIT, HIT` and
// **age: 98211** — twenty-seven hours old. Three requests in a row, three
// identical stale bodies. So `draft_status` would never have flipped to post,
// the board would have polled a finished draft forever, and a manager who
// joined the league that morning would not exist as far as the seat count was
// concerned. With `?_=<nonce>`: `x-cache: MISS`, `age: 0`.
//
// The pick feed measured clean on the same probe (MISS, no age) and gets one
// anyway. That is a change of prior, not speculation: the same host demonstrably
// ignores no-store on a sibling path, which path varnish fronts is a config
// detail nobody publishes, and this is the one endpoint whose staleness would be
// invisible — a board that is thirty seconds behind looks exactly like a room
// that is thinking.
func nonced(url string) string {
	return url + "?_=" + strconv.FormatInt(nonce.Add(1), 10)
}
