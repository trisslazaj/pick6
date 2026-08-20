// Package fpl talks to the FPL Draft API. No auth on anything we use, and that
// is load-bearing rather than lucky: the one endpoint that needs a bearer token
// (bootstrap-dynamic) only lists which leagues you are in, which is a thing you
// look up once and write down. Draft night polls with no token, no cookie, and
// nothing that can expire mid-round.
package fpl

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/cache"
)

const apiBase = "https://draft.premierleague.com/api"

// BootstrapCache is the pool's filename in the cache dir. Its own file, never
// players.json: an fpl fetch eight days before the nfl draft must not be able to
// clobber the nfl board.
const BootstrapCache = "fpl_bootstrap.json"

// Element is one player out of bootstrap-static.
//
// The fields past Status are a truth layer and nothing more, exactly as in the
// sleeper dump: they travel to the board so a human can read them, and no math
// anywhere prices them. "Discount the injured man's value" is as wrong here as
// it is there — we make no projections of our own.
type Element struct {
	ID          int    `json:"id"`
	WebName     string `json:"web_name"`
	FirstName   string `json:"first_name"`
	SecondName  string `json:"second_name"`
	Team        int    `json:"team"`
	ElementType int    `json:"element_type"`
	// DraftRank is FPL's own draft-mode ranking, and it is the price. Populated
	// on every element, 1..N with no holes, and better than a points sort: it
	// prices an injury-shortened season back up where the market has it, which
	// a total_points sort buries a hundred and ten ranks deep.
	DraftRank   int    `json:"draft_rank"`
	TotalPoints int    `json:"total_points"`
	Status      string `json:"status"` // a available, i injured, d doubtful, s suspended, u left the league
	News        string `json:"news"`
	// NewsAdded is RFC3339 and null for most of the pool. It is NOT epoch
	// millis — the sleeper dump's news_updated is, and copying that assumption
	// here reads every date as 1970.
	NewsAdded  *time.Time `json:"news_added"`
	ChanceNext *int       `json:"chance_of_playing_next_round"`
	ChanceThis *int       `json:"chance_of_playing_this_round"`
}

// Team is one of the twenty clubs; we want the short name and nothing else.
type Team struct {
	ID        int    `json:"id"`
	ShortName string `json:"short_name"`
}

// ElementType maps element_type to the position string the whole engine speaks.
type ElementType struct {
	ID    int    `json:"id"`
	Short string `json:"singular_name_short"` // GKP DEF MID FWD
}

// Squad is the hard quota: fifteen men, and exactly this many at each position.
// There is no bench and no flex, so a squad slot you have already filled twice
// is worth nothing rather than "bench depth" — see engine.Roster.Quota.
type Squad struct {
	Size int `json:"size"`
	GKP  int `json:"select_GKP"`
	DEF  int `json:"select_DEF"`
	MID  int `json:"select_MID"`
	FWD  int `json:"select_FWD"`

	// Play and the min/max pairs are the FORMATION rules, and they are a
	// different shape from the squad above: you own fifteen and start eleven.
	// One keeper starts and the second never can, at least three defenders, at
	// least two midfielders, at least one forward — seven places spoken for, four
	// free to come from def, mid or fwd, and four men on the bench.
	Play   int `json:"play"`
	MinGKP int `json:"min_play_GKP"`
	MaxGKP int `json:"max_play_GKP"`
	MinDEF int `json:"min_play_DEF"`
	MaxDEF int `json:"max_play_DEF"`
	MinMID int `json:"min_play_MID"`
	MaxMID int `json:"max_play_MID"`
	MinFWD int `json:"min_play_FWD"`
	MaxFWD int `json:"max_play_FWD"`
}

// Bootstrap is the subset of bootstrap-static we read.
type Bootstrap struct {
	Elements     []Element     `json:"elements"`
	Teams        []Team        `json:"teams"`
	ElementTypes []ElementType `json:"element_types"`
	Settings     struct {
		Squad Squad `json:"squad"`
	} `json:"settings"`
}

// GetBootstrap downloads (or reads from cache) the player pool.
func GetBootstrap(maxAge time.Duration) (*Bootstrap, bool, error) {
	b, fetched, err := cache.Get(BootstrapCache, apiBase+"/bootstrap-static", maxAge)
	if err != nil {
		return nil, false, err
	}
	var bs Bootstrap
	if err := json.Unmarshal(b, &bs); err != nil {
		return nil, fetched, fmt.Errorf("fpl bootstrap: %w", err)
	}
	if len(bs.Elements) == 0 {
		return nil, fetched, fmt.Errorf("fpl bootstrap: no players")
	}
	return &bs, fetched, nil
}

// Pos is the position string for an element, "" if the type is unknown.
func (b *Bootstrap) Pos(e Element) string {
	for _, t := range b.ElementTypes {
		if t.ID == e.ElementType {
			return t.Short
		}
	}
	return ""
}

// TeamCode is the club's short name, "" if the id is unknown.
func (b *Bootstrap) TeamCode(e Element) string {
	for _, t := range b.Teams {
		if t.ID == e.Team {
			return t.ShortName
		}
	}
	return ""
}

// Positions is the squad's positions in quota order — the order the roster's
// slots are built in, and therefore the order every derived position list in the
// engine comes out in.
func (b *Bootstrap) Positions() []string {
	return []string{"GKP", "DEF", "MID", "FWD"}
}

// Draftable is the pool minus the players nobody can draft.
//
// Status "u" means the player has left the league. Thirty-two of them carry a
// draft_rank like everyone else, and leaving them in would put men who are not
// in the Premier League on a board that claims to show who is available. An
// undraftable player is not a player.
//
// Sorted by draft_rank, which is the board's order everywhere downstream.
func (b *Bootstrap) Draftable() []Element {
	out := make([]Element, 0, len(b.Elements))
	for _, e := range b.Elements {
		if e.Status == "u" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DraftRank < out[j].DraftRank })
	return out
}

// Name is what the board prints. web_name is FPL's own display name and is what
// the official app shows, so it is what the user will be reading off the phone
// next to this board.
//
// Twelve names repeat across the pool (two Hendersons, three Wilsons), and none
// of the collisions share a club — every surface that prints a name prints the
// team code beside it, so the pair disambiguates without lengthening the one
// column that fights hardest for width.
func (e Element) Name() string { return e.WebName }

// InjuryChip maps FPL's status letter onto the chip vocabulary the ui already
// speaks, so nothing in ui/chips.go has to learn a second alphabet.
//
// "u" never arrives here — Draftable drops it.
func (e Element) InjuryChip() string {
	switch e.Status {
	case "i":
		return "Out"
	case "s":
		return "Sus"
	case "d":
		return "Questionable"
	}
	return ""
}

// NewsMillis is news_added as the epoch-millisecond stamp the board's news chip
// reads, 0 when there is no news.
func (e Element) NewsMillis() int64 {
	if e.NewsAdded == nil {
		return 0
	}
	return e.NewsAdded.UnixMilli()
}

// ValueTop is where the curve starts, and it exists because Player.Value is an
// INT.
//
// The engine's own rank fallback is ValueBase (250) × exp(-rank/ValueDecay), and
// 250 is a fine number for a 200-deep nfl board. Over 560 fpl players it is a
// disaster: rank 300 prices at 0.14 and rounds to zero, so a third of the pool
// arrives at the board with the value reserved for "nobody ranked him" — no
// tier, no vor, no place in the plan. Ten thousand puts the curve on the same
// order of magnitude as fantasycalc's imported values (10465 at rank 1), which
// is the scale every column width and every threshold in the ui was drawn
// against.
const ValueTop = 10000.0

// RankValue is the value curve: the engine's existing convex fallback, run over
// draft_rank rather than over a rank we invented.
//
// Value is only ever compared by differences, so the scale is arbitrary and the
// CONSISTENCY is the whole point — every fpl player is priced by this one
// function, so the board can never be in the mode-mixing state the nfl one
// avoids by anchoring k and def onto the imported curve.
//
// Floored at 1 rather than 0 past the point the exponential underflows. Zero is
// already a sentinel here — it is what a player registered from a pick feed that
// no source ranked carries, and what RosterValue and vor both read as "worth
// nothing". Every fpl player IS ranked, so none of them may wear it, however
// deep. One is a real if tiny number and keeps that distinction honest.
func RankValue(rank int, decay float64) int {
	if rank <= 0 {
		return 0
	}
	if v := int(math.Round(ValueTop * math.Exp(-float64(rank)/decay))); v > 0 {
		return v
	}
	return 1
}

// Departed is the stringified ids of players the pool still lists who have left
// the league — Draftable's complement, and the half a live board needs.
//
// The board is built from players_fpl.json, which was filtered when `fetch` ran.
// A player who transfers out between that fetch and the draft is therefore still
// ON the board while the official app refuses to let anybody draft him — and
// because no real pick can ever remove him, nothing in the sim ever removes him
// either: his survival climbs toward 100% while the faller chip grows, which is
// the board painting an undraftable man as the biggest bargain on the screen.
// One such player sits inside the top 150 today.
//
// So `live` re-checks against the bootstrap it already fetched for the squad
// quota. Dropping him is safe even in a draft that already took him: the feed's
// name roll re-registers a drafted player whatever his status, which is exactly
// how league 2400's replay handles the one it has.
func (b *Bootstrap) Departed() []string {
	var out []string
	for _, e := range b.Elements {
		if e.Status == "u" {
			out = append(out, strconv.Itoa(e.ID))
		}
	}
	sort.Strings(out)
	return out
}

// SidelinedWeeks is how far out a return has to be before a player stops being
// a candidate for OUR board. Four is a gameweek-shaped number and the corpus is
// insensitive to it: on the 2026-08-19 bootstrap the set is identical at four
// weeks and at six, and moving it to two only adds a suspension and an ankle.
const SidelinedWeeks = 4

// returnPat pulls the date out of fpl's own news line. Two phrasings carry one,
// measured across the pool: "Achilles injury - Expected back 28 Nov" and
// "Suspended until 30 Aug". Everything else says "Unknown return date", which
// is not a missing field — it is fpl saying nobody knows.
var returnPat = regexp.MustCompile(`(?i)(?:expected back|suspended until)\s+(\d{1,2})\s+([a-z]{3})`)

// ReturnDate is when fpl says he is back, if it says at all.
//
// The string carries no year, and it has to: a season runs august to may, so a
// "10 Feb" read in september is next year's and a "28 Nov" read in september is
// this one's. The rule is the first year that puts the date inside the season
// ahead — within thirty days behind us, since a return date drifts a little
// past before somebody updates it.
func (e Element) ReturnDate(now time.Time) (time.Time, bool) {
	m := returnPat.FindStringSubmatch(e.News)
	if m == nil {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(m[1])
	if err != nil {
		return time.Time{}, false
	}
	mon, ok := months[strings.ToLower(m[2])]
	if !ok {
		return time.Time{}, false
	}
	for _, year := range []int{now.Year(), now.Year() + 1} {
		d := time.Date(year, mon, day, 0, 0, 0, 0, now.Location())
		// Reject a rolled-over day (31 Feb) rather than letting time.Date
		// normalise it into march and report a confident wrong date.
		if d.Day() != day {
			return time.Time{}, false
		}
		if d.After(now.AddDate(0, 0, -30)) {
			return d, true
		}
	}
	return time.Time{}, false
}

// Sidelined reports a player who cannot play now and will not be back soon —
// the state fpl's own draft_rank refuses to price.
//
// This is the one injury fact that reaches the engine, and it earns the
// exception by not being a projection. Marking a man down because he is
// questionable would be inventing a number about a real person; this reads two
// published facts (he cannot play the next round, and fpl either names a return
// past the horizon or says nobody knows) and answers the only question the
// board asks, which is whether to put him at the top of a recommendation.
//
// It matters here in a way it does not in nfl because draft_rank does not move
// for it. Ekitiké sat at rank 19 on draft morning with an achilles and no
// return date, so the board led with him for fifteen rounds — the market that
// prices this pool had simply not repriced him, and nothing downstream could.
// He stays ON the board as a player: the room can still draft him, the sim
// still lets opponents take him, and search still finds him.
func (e Element) Sidelined(now time.Time) bool {
	if !e.cannotPlay() {
		return false
	}
	back, ok := e.ReturnDate(now)
	if !ok {
		return true // "unknown return date" — fpl's words, not an absent field
	}
	return back.After(now.AddDate(0, 0, 7*SidelinedWeeks))
}

// cannotPlay is "he is not available for the next round", off both fields that
// say so. Status is the primary — i is injured, s is suspended — and the chance
// arm catches the case where fpl zeroes a doubtful player without moving him.
// Measured on the 2026-08-19 pool the two agree exactly: 87 elements at chance
// 0, and the same 87 carry i, s or u.
func (e Element) cannotPlay() bool {
	if e.Status == "i" || e.Status == "s" {
		return true
	}
	return e.ChanceNext != nil && *e.ChanceNext == 0
}

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

// SidelinedIDs is the stringified ids of pool players who are sidelined right
// now — the same live re-check Departed gets, for the same reason. The board
// froze this at fetch time and an achilles heals on its own schedule; a squad
// that clears between the fetch and the draft has to come back onto the board,
// and one that tears has to leave it.
func (b *Bootstrap) SidelinedIDs(now time.Time) map[string]bool {
	out := map[string]bool{}
	for _, e := range b.Elements {
		if e.Status != "u" && e.Sidelined(now) {
			out[strconv.Itoa(e.ID)] = true
		}
	}
	return out
}
