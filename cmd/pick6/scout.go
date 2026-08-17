package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/rankings"
	"github.com/trisslazaj/pick6/internal/sleeper"
)

// pick6 scout — who you are actually drafting against.
//
// National adp is the average of thousands of strangers. The twelve people in
// this league are not strangers and not an average: they take quarterbacks and
// tight ends early, defenses late, and each of them does it differently. This
// walks every draft those twelve have played that sleeper will hand over
// unauthenticated, and writes one profile per manager.
//
// The printout is the small half of the deliverable. The cached scout.json is
// the point: milestone 6's opponent model needs a per-manager prior to sample
// from, and this is where it comes from.

// scoutUser is whose leagues get walked when -user is not given. Hardcoding a
// person is fine here for the same reason calibrateDrafts hardcodes three draft
// ids — this is one human's tool, and the alternative is typing his own username
// into his own laptop every time.
const scoutUser = "prxtani"

// scoutFileName is the cached profile, written next to players.json.
const scoutFileName = "scout.json"

// scoutShrink is `a`, the pseudo-count in
//
//	P(m takes his first P by round r) = (count + a*leagueRate(r)) / (n + a)
//
// Two, so a manager seen once carries a third of the weight of the room and a
// manager seen three times carries three fifths. The failure this prevents is
// the whole reason the formula exists: one draft where somebody happened to take
// a tight end in round 2 is not a tight-end-early manager, and without a prior
// it reads as a certainty.
//
// It lives here rather than in engine/tuning.go for the same reason
// adp.RoomWarpPseudo does — nothing in the engine reads it. The engine will read
// the finished profile, if and when v2 lands.
const scoutShrink = 2.0

// scoutFirstPositions are the positions whose FIRST pick says something about a
// manager. Backs and receivers are excluded on purpose: everyone takes one in
// round 1, so "his first rb came in round 1" separates nobody. The four here are
// the scarce or single-slot positions, where reaching early is a choice — and
// they are exactly the four the room is measurably weird about (qb and te early,
// k and def late).
var scoutFirstPositions = []string{"QB", "TE", "K", "DEF"}

// scoutPositions is every position, in lineup order, for the tables that show
// all six.
var scoutPositions = []string{"QB", "RB", "WR", "TE", "K", "DEF"}

// scoutFile is the cached shape. Every field is derived from picks that really
// happened; nothing here is modelled or invented.
type scoutFile struct {
	FetchedAt time.Time      `json:"fetched_at"`
	UserID    string         `json:"user_id"`
	Username  string         `json:"username"`
	Seasons   []int          `json:"seasons"`
	Shrink    float64        `json:"shrink_pseudo_drafts"`
	Drafts    []scoutDraft   `json:"drafts"`
	League    scoutBaseline  `json:"league"`
	Managers  []scoutManager `json:"managers"`
}

// scoutDraft is every draft the walk found, scored or not. The skipped ones stay
// in the file with their reason: "we saw it and left it out" is information, and
// without it the next reader re-walks the same endpoints to find out why a draft
// is missing.
type scoutDraft struct {
	DraftID  string `json:"draft_id"`
	LeagueID string `json:"league_id,omitempty"`
	Season   string `json:"season"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Teams    int    `json:"teams"`
	Rounds   int    `json:"rounds"`
	Picks    int    `json:"picks"`
	Dynasty  bool   `json:"dynasty"`
	Scored   bool   `json:"scored"`
	Skip     string `json:"skip,omitempty"`
}

// scoutBaseline is the room as one drafter — the rates every manager is shrunk
// toward.
type scoutBaseline struct {
	Drafts        int                  `json:"drafts"`
	Managers      int                  `json:"managers"`
	ManagerDrafts int                  `json:"manager_drafts"`
	Picks         int                  `json:"picks"`
	Rounds        int                  `json:"rounds"`
	Teams         int                  `json:"teams"`
	AutoPicks     int                  `json:"autodraft_picks"`
	First         map[string]scoutRate `json:"first"`
	ByRound       []scoutRound         `json:"by_round"`
}

// scoutRate is the room's own curve for one position: what share of
// manager-drafts had taken their first by round r (index r-1), plus the mean
// round it happened in.
type scoutRate struct {
	ByRound []float64 `json:"by_round"`
	Mean    float64   `json:"mean_round"`
}

// scoutManager is one leaguemate.
type scoutManager struct {
	UserID   string `json:"user_id"`
	Name     string `json:"display_name"`
	TeamName string `json:"team_name,omitempty"`
	Drafts   int    `json:"drafts"`
	Picks    int    `json:"picks"`

	// AutoPicks counts his picks with an empty picked_by, and AutoFloor is that
	// as a share. MEASURED ZERO: 0 of 552 picks across all three real drafts
	// carry an empty picked_by, so this reads 0.0 for every manager in this
	// room today. That is the honest number and not a broken query — sleeper
	// attributed every one of those picks to a human. The metric ships anyway
	// because it is the right signal and v2 wants it (a manager who autodrafts
	// should simulate as pure adp), and because the day somebody does leave
	// autodraft on, this column is already watching. No proxy is substituted:
	// guessing at autodrafting from pick timing or adp obedience would be
	// inventing a measurement.
	AutoPicks int     `json:"autodraft_picks"`
	AutoFloor float64 `json:"autodraft_floor"`

	First   map[string]scoutFirst `json:"first"`
	ByRound []scoutRound          `json:"by_round"`
}

// scoutFirst is when a manager takes his first player at a position.
type scoutFirst struct {
	// Rounds is the raw record, one entry per draft, in draft order; 0 means he
	// finished that draft without one. Kept so a later reader can re-shrink with
	// a different prior instead of trusting this one.
	Rounds []int `json:"rounds"`
	// Mean is the unshrunk mean over the drafts where he took one at all. Zero
	// when he never did.
	Mean float64 `json:"mean_round"`
	// ByRound is the shrunk cdf: index r-1 is P(his first came by round r).
	ByRound []float64 `json:"by_round"`
	// Expected is that cdf's expected round. It is the one number worth
	// printing, and it lands at rounds+1 for a manager who never takes one.
	Expected float64 `json:"expected_round"`
}

// scoutRound is one round's positional counts. Counts rather than shares,
// because a share of three picks is a lie with a decimal point on it — the
// printout divides, the file keeps what was observed.
type scoutRound struct {
	Round int            `json:"round"`
	Pos   map[string]int `json:"pos"`
}

// scoutPick is the flat record the tally runs over: one pick, attributed.
type scoutPick struct {
	Draft string
	User  string
	Round int
	Pos   string
	Auto  bool
}

func runScout(args []string) error {
	fs := flagSet("scout")
	user := fs.String("user", scoutUser, "sleeper username (or user id) whose leagues to walk")
	from := fs.Int("from", 2024, "first season to walk")
	to := fs.Int("to", 2026, "last season to walk")
	withDynasty := fs.Bool("dynasty", false, "include keeper and dynasty leagues in the profile")
	verbose := fs.Bool("v", false, "list every draft found, including the skipped ones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to < *from {
		return fmt.Errorf("-to %d is before -from %d", *to, *from)
	}

	fmt.Println("pick6 scout — how this room drafts, manager by manager")
	fmt.Println()

	u, err := sleeper.CachedUser(*user)
	if err != nil {
		return err
	}
	who := u.Username
	if who == "" {
		who = u.DisplayName
	}
	note("sleeper", "cached", fmt.Sprintf("%s · user %s", strings.ToLower(who), u.UserID))

	found, names, seasons := scoutWalk(u.UserID, *from, *to)
	if len(found) == 0 {
		fmt.Println("\nno drafts found in those seasons. nothing to profile.")
		return nil
	}

	// score every draft ----------------------------------------------------
	var rows []scoutDraft
	var picks []scoutPick
	rounds := map[string]int{}
	roomDrafts := map[string]adp.RoomDraft{}
	roomTeams := map[string]int{}
	teams := map[int]int{}
	for _, f := range found {
		row, got, err := scoutRead(f, *withDynasty)
		if err != nil {
			note("draft", "skipped", fmt.Sprintf("%s · %s", f.draftID, strings.ToLower(err.Error())))
			rows = append(rows, row)
			continue
		}
		rows = append(rows, row)
		if !row.Scored {
			if *verbose {
				note("draft", "skipped", fmt.Sprintf("%s · %s · %s", row.DraftID, row.Season, row.Skip))
			}
			continue
		}
		picks = append(picks, got.picks...)
		rounds[row.DraftID] = row.Rounds
		roomDrafts[row.DraftID] = got.room
		roomTeams[row.DraftID] = row.Teams
		teams[row.Teams]++
	}
	scoutNoteSkips(rows, *verbose)

	if len(picks) == 0 {
		// Deliberately no write: an empty profile would overwrite a good one, and
		// the usual way to land here is walking a season whose drafts have not
		// happened yet.
		fmt.Println("\nno scored drafts. every draft found was pre-draft, non-snake, or outside the profile.")
		fmt.Printf("%s not rewritten.\n", strings.ToLower(scoutFileName))
		scoutPrintDrafts(rows)
		return nil
	}

	base, managers := scoutTally(picks, rounds)
	base.Drafts = len(rounds)
	base.Teams = scoutModalTeams(teams)
	for i := range managers {
		if n, ok := names[managers[i].UserID]; ok {
			managers[i].Name, managers[i].TeamName = n.display, n.team
		} else {
			// A manager who appears in a draft but in none of the league user
			// lists — a leaguemate who has since left. His picks still count;
			// he just has no name but his id.
			managers[i].Name = managers[i].UserID
		}
	}
	sort.Slice(managers, func(i, j int) bool {
		a, b := strings.ToLower(managers[i].Name), strings.ToLower(managers[j].Name)
		if a != b {
			return a < b
		}
		return managers[i].UserID < managers[j].UserID
	})

	note("drafts", "scored", fmt.Sprintf("%d drafts · %d picks · %d managers · %d manager-drafts · %d rounds deep",
		base.Drafts, base.Picks, base.Managers, base.ManagerDrafts, base.Rounds))

	profile := scoutFile{
		FetchedAt: time.Now(),
		UserID:    u.UserID,
		Username:  who,
		Seasons:   seasons,
		Shrink:    scoutShrink,
		Drafts:    rows,
		League:    base,
		Managers:  managers,
	}

	scoutPrintFirst(base, managers)
	scoutPrintShare(base)
	scoutPrintRoom(roomDrafts, roomTeams, base.Teams)
	scoutPrintAuto(base, managers)
	if *verbose {
		scoutPrintDrafts(rows)
	}

	dir, err := cache.Dir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, scoutFileName)
	b, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s\n", strings.ToLower(path))
	return nil
}

// scoutFound is one draft the walk turned up, with what the league it belongs to
// says about it.
type scoutFound struct {
	draftID  string
	leagueID string
	dynasty  bool
	mock     bool
	// status as the DIRECTORY listing reported it, which is the only copy that
	// refreshes daily. It picks the cache lifetime for the draft's own metadata
	// and picks below: 30 days is a lie about anything that has not happened yet.
	status string
}

type scoutNames struct{ display, team string }

// scoutWalk discovers drafts: user -> leagues -> drafts, plus the user's own
// draft list to catch anything not attached to a league.
//
// Both paths are walked because they answer different questions. The league path
// is authoritative for the room — it finds a league's drafts whether or not this
// user was in them — while the user path is the only way to see a mock draft. A
// year that will not load is a note and not a failure: a profile from two seasons
// is worse than one from three, and both beat an error message.
func scoutWalk(userID string, from, to int) ([]scoutFound, map[string]scoutNames, []int) {
	var order []scoutFound
	seen := map[string]bool{}
	names := map[string]scoutNames{}
	var seasons []int

	for year := from; year <= to; year++ {
		leagues, err := sleeper.UserLeagues(userID, year)
		if err != nil {
			note(strconv.Itoa(year), "skipped", "leagues · "+strings.ToLower(err.Error()))
			continue
		}
		userDrafts, err := sleeper.UserDrafts(userID, year)
		if err != nil {
			note(strconv.Itoa(year), "skipped", "drafts · "+strings.ToLower(err.Error()))
			userDrafts = nil
		}
		if len(leagues) == 0 && len(userDrafts) == 0 {
			note(strconv.Itoa(year), "cached", "no leagues, no drafts")
			continue
		}
		seasons = append(seasons, year)

		known := map[string]sleeper.League{}
		found := 0
		for _, l := range leagues {
			known[l.LeagueID] = l
			// Names last, so a manager who renamed himself between seasons shows
			// up under the name he uses now — the walk runs oldest year first.
			users, err := sleeper.LeagueUsers(l.LeagueID)
			if err != nil {
				note(strconv.Itoa(year), "skipped", "users · "+strings.ToLower(err.Error()))
			}
			for _, m := range users {
				names[m.UserID] = scoutNames{display: m.Name(), team: m.Metadata.TeamName}
			}

			drafts, err := sleeper.LeagueDrafts(l.LeagueID)
			if err != nil {
				note(strconv.Itoa(year), "skipped", "league drafts · "+strings.ToLower(err.Error()))
				continue
			}
			for _, d := range drafts {
				if seen[d.DraftID] {
					continue
				}
				seen[d.DraftID] = true
				order = append(order, scoutFound{draftID: d.DraftID, leagueID: l.LeagueID,
					dynasty: l.Dynasty(), status: d.Status})
				found++
			}
		}
		for _, d := range userDrafts {
			if seen[d.DraftID] {
				continue
			}
			seen[d.DraftID] = true
			l, ok := known[d.LeagueID]
			order = append(order, scoutFound{
				draftID:  d.DraftID,
				leagueID: d.LeagueID,
				dynasty:  ok && l.Dynasty(),
				mock:     d.LeagueID == "",
				status:   d.Status,
			})
			found++
		}
		note(strconv.Itoa(year), "cached", fmt.Sprintf("%s, %s",
			plural(len(leagues), "league"), plural(found, "draft")))
	}
	return order, names, seasons
}

// scoutGot is one scored draft reduced to what the tally and the room curve read.
type scoutGot struct {
	picks []scoutPick
	room  adp.RoomDraft
}

// scoutRead loads a draft and decides whether it belongs in the profile.
//
// The order of the refusals is deliberate — the reason printed should be the one
// that would still be true after the others were fixed. A linear dynasty rookie
// draft is refused for being linear, because that is a hard limit of the snake
// math and no flag turns it on.
func scoutRead(f scoutFound, withDynasty bool) (scoutDraft, scoutGot, error) {
	row := scoutDraft{DraftID: f.draftID, LeagueID: f.leagueID, Dynasty: f.dynasty}
	// The 30-day lifetime CachedDraft defaults to is a claim about a FINISHED
	// draft. The walk finds unfinished ones too — this user's 2026 league was
	// "pre_draft" with an empty draft_order on 2026-07-29 — and cached that long,
	// the empty answer would still be what scout read for a month after the
	// league actually drafted, leaving this season's draft out of the profile it
	// exists for. The directory listing's status refreshes daily and picks the
	// lifetime.
	age := sleeper.DraftCacheAge(f.status)
	d, err := sleeper.CachedDraftAge(f.draftID, age)
	if err != nil {
		row.Skip = strings.ToLower(err.Error())
		return row, scoutGot{}, err
	}
	row.Season, row.Type, row.Status = d.Season, d.Type, d.Status
	row.Teams, row.Rounds = d.Settings.Teams, d.Settings.Rounds

	// The draft's OWN status, now that it has been read at the lifetime the
	// listing's status earned: a draft that finished since the last scout run
	// says so here, and its picks must not come off a month-old empty file.
	picks, err := sleeper.CachedPicksAge(f.draftID, sleeper.DraftCacheAge(d.Status))
	if err != nil {
		row.Skip = strings.ToLower(err.Error())
		return row, scoutGot{}, err
	}
	row.Picks = len(picks)

	bad := d.Validate()
	switch {
	case bad != nil:
		row.Skip = strings.ToLower(bad.Error())
		return row, scoutGot{}, nil
	case f.mock:
		row.Skip = "mock draft — strangers, not this room"
		return row, scoutGot{}, nil
	case f.dynasty && !withDynasty:
		row.Skip = "dynasty or keeper league (-dynasty to include)"
		return row, scoutGot{}, nil
	case len(picks) == 0:
		row.Skip = "no picks yet"
		return row, scoutGot{}, nil
	case len(d.DraftOrder) == 0:
		// Without the order there is no user_id behind a seat, so every pick
		// would be unattributed. This is the pre-draft shape, and it is not an
		// error: the order gets seeded when the commissioner sets it.
		row.Skip = "draft order not seeded — no way to tell whose picks are whose"
		return row, scoutGot{}, nil
	}

	got := scoutGot{picks: scoutAttribute(d, picks, f.draftID), room: adp.RoomDraftOf(picks)}
	row.Scored = true
	return row, got, nil
}

// scoutAttribute turns a draft's picks into per-manager records.
//
// Attribution is by the roster that RECEIVES the player, never by the seat that
// picks. Draft picks get traded — 17 of the 2024 draft's 180 picks went to a
// roster other than the one owning the slot — and crediting those to the seat
// would put another man's tendency on your leaguemate's profile. OwnerSlot
// already inverts slot_to_roster_id; this inverts draft_order on top of it to
// get from a seat back to a person.
//
// picked_by is the fallback and not the primary, deliberately: it is empty on an
// autodrafted pick, which is exactly the manager we would most want to profile.
func scoutAttribute(d *sleeper.Draft, picks []sleeper.DraftPick, draftID string) []scoutPick {
	userOf := map[int]string{}
	for uid, slot := range d.DraftOrder {
		userOf[slot] = uid
	}
	out := make([]scoutPick, 0, len(picks))
	for _, p := range picks {
		if p.PlayerID == "" {
			continue
		}
		user := userOf[d.OwnerSlot(p)]
		if user == "" {
			user = p.PickedBy
		}
		if user == "" {
			continue // a seat with no owner and no picker: nobody to profile
		}
		out = append(out, scoutPick{
			Draft: draftID,
			User:  user,
			Round: p.Round,
			Pos:   rankings.NormalizePos(p.Metadata.Position),
			Auto:  p.PickedBy == "",
		})
	}
	return out
}

// scoutTally is the whole computation: attributed picks in, cached profile out.
//
// Pure, and separated from every network call, so the shrink math can be pinned
// by a table test with hand-computed numbers.
func scoutTally(picks []scoutPick, draftRounds map[string]int) (scoutBaseline, []scoutManager) {
	// Rounds spans the deepest draft in the set. Caveat worth carrying: this
	// user's three real drafts are 16, 15 and 15 rounds, so at r = 16 the two
	// shorter drafts contribute "never took one" to every position. That is the
	// truth about those drafts and it does bias the deepest round slightly
	// pessimistic — the same 15-vs-16 caveat that rides along with the per-draft
	// position counts.
	maxRound := 0
	for _, r := range draftRounds {
		if r > maxRound {
			maxRound = r
		}
	}
	for _, p := range picks {
		if p.Round > maxRound {
			maxRound = p.Round
		}
	}

	type seat struct{ user, draft string }
	firstAt := map[seat]map[string]int{}
	var seatOrder []seat

	mgrPicks := map[string]int{}
	mgrAuto := map[string]int{}
	mgrRounds := map[string]map[int]map[string]int{}
	leagueRounds := map[int]map[string]int{}
	autoTotal := 0

	bump := func(m map[int]map[string]int, round int, pos string) {
		if m[round] == nil {
			m[round] = map[string]int{}
		}
		m[round][pos]++
	}

	for _, p := range picks {
		k := seat{p.User, p.Draft}
		if firstAt[k] == nil {
			firstAt[k] = map[string]int{}
			seatOrder = append(seatOrder, k)
		}
		if p.Pos != "" {
			if r, ok := firstAt[k][p.Pos]; !ok || p.Round < r {
				firstAt[k][p.Pos] = p.Round
			}
			bump(leagueRounds, p.Round, p.Pos)
			if mgrRounds[p.User] == nil {
				mgrRounds[p.User] = map[int]map[string]int{}
			}
			bump(mgrRounds[p.User], p.Round, p.Pos)
		}
		mgrPicks[p.User]++
		if p.Auto {
			mgrAuto[p.User]++
			autoTotal++
		}
	}

	// The league baseline: over every manager-draft, what share had taken their
	// first at the position by round r. This is the prior each manager is pulled
	// toward, so it must be the same population the counts come from.
	leagueCount := map[string][]int{}
	leagueSum := map[string]float64{}
	leagueTook := map[string]int{}
	// Seed the four profiled positions even when nobody ever took one, so the
	// baseline always has a full-length row for them. Without this, a room that
	// drafts no kickers leaves First["K"] as a zero value whose empty cdf reads
	// as "expected round 1" — the exact opposite of never.
	for _, pos := range scoutFirstPositions {
		leagueCount[pos] = make([]int, maxRound)
	}
	for _, k := range seatOrder {
		// scoutFirstPositions only — a rb/wr "first pick" curve says nothing
		// (everyone takes one in round 1 or 2) and firstAt tracks every position
		// firstAt is keyed on, not just the profiled four.
		for _, pos := range scoutFirstPositions {
			r, ok := firstAt[k][pos]
			if !ok {
				continue
			}
			for i := r - 1; i < maxRound; i++ {
				if i >= 0 {
					leagueCount[pos][i]++
				}
			}
			leagueSum[pos] += float64(r)
			leagueTook[pos]++
		}
	}
	seats := len(seatOrder)
	base := scoutBaseline{
		Managers:      len(mgrPicks),
		ManagerDrafts: seats,
		Picks:         len(picks),
		Rounds:        maxRound,
		AutoPicks:     autoTotal,
		First:         map[string]scoutRate{},
		ByRound:       scoutRounds(leagueRounds, maxRound),
	}
	leagueRate := map[string][]float64{}
	for pos, counts := range leagueCount {
		rate := make([]float64, maxRound)
		for i, c := range counts {
			rate[i] = float64(c) / float64(seats)
		}
		leagueRate[pos] = rate
		// Guarded because a seeded-but-untaken position divides zero by zero, and
		// a NaN here does not just print badly — encoding/json refuses to marshal
		// one, so the whole cached profile would fail to write.
		mean := 0.0
		if leagueTook[pos] > 0 {
			mean = leagueSum[pos] / float64(leagueTook[pos])
		}
		base.First[pos] = scoutRate{ByRound: rate, Mean: mean}
	}

	// Per manager, in a stable order so two runs write the same file.
	byUser := map[string][]seat{}
	for _, k := range seatOrder {
		byUser[k.user] = append(byUser[k.user], k)
	}
	users := make([]string, 0, len(byUser))
	for u := range byUser {
		users = append(users, u)
	}
	sort.Strings(users)

	managers := make([]scoutManager, 0, len(users))
	for _, u := range users {
		his := byUser[u]
		m := scoutManager{
			UserID:    u,
			Drafts:    len(his),
			Picks:     mgrPicks[u],
			AutoPicks: mgrAuto[u],
			First:     map[string]scoutFirst{},
			ByRound:   scoutRounds(mgrRounds[u], maxRound),
		}
		if m.Picks > 0 {
			m.AutoFloor = float64(m.AutoPicks) / float64(m.Picks)
		}
		for _, pos := range scoutFirstPositions {
			f := scoutFirst{Rounds: make([]int, 0, len(his))}
			count := make([]int, maxRound)
			sum, took := 0, 0
			for _, k := range his {
				r := firstAt[k][pos] // 0 when he never took one
				f.Rounds = append(f.Rounds, r)
				if r == 0 {
					continue
				}
				sum, took = sum+r, took+1
				for i := r - 1; i < maxRound; i++ {
					count[i]++
				}
			}
			if took > 0 {
				f.Mean = float64(sum) / float64(took)
			}
			f.ByRound = shrunkFirstCDF(count, len(his), leagueRate[pos])
			f.Expected = expectedRound(f.ByRound)
			m.First[pos] = f
		}
		managers = append(managers, m)
	}
	return base, managers
}

// shrunkFirstCDF pulls one manager's record toward the room's:
//
//	P(m takes his first P by round r) = (count(r) + a*leagueRate(r)) / (n + a)
//
// count(r) is how many of his n drafts had his first at the position by round r,
// and both inputs are cumulative, so the result is non-decreasing in r by
// construction — a cdf cannot go backwards and this one cannot either.
//
// A manager with no drafts (n = 0) collapses to exactly the league rate, which
// is the right answer to "what do we know about him": nothing but the room.
func shrunkFirstCDF(count []int, n int, leagueRate []float64) []float64 {
	out := make([]float64, len(count))
	for r := range count {
		rate := 0.0
		if r < len(leagueRate) {
			rate = leagueRate[r]
		}
		out[r] = (float64(count[r]) + scoutShrink*rate) / (float64(n) + scoutShrink)
	}
	return out
}

// expectedRound turns a cdf over rounds into the round his first pick at the
// position is expected in:
//
//	E[R] = 1 + sum over r of (1 - F(r))
//
// which is the standard tail-sum for a positive integer variable. A manager who
// always takes one in round 3 has F = 0,0,1,1,... and scores exactly 3. A
// manager who never takes one has F = 0 everywhere and lands at rounds+1 — off
// the end of the draft, which is what "never" means and is more honest than
// leaving the cell blank.
func expectedRound(cdf []float64) float64 {
	e := 1.0
	for _, f := range cdf {
		e += 1 - f
	}
	return e
}

// scoutRounds flattens the per-round counters into the cached shape, one entry
// per round from 1 to rounds so a round nobody drafted a position in still
// appears (as an empty map) rather than silently shifting the rows below it.
func scoutRounds(counts map[int]map[string]int, rounds int) []scoutRound {
	out := make([]scoutRound, 0, rounds)
	for r := 1; r <= rounds; r++ {
		pos := map[string]int{}
		for p, n := range counts[r] {
			pos[p] = n
		}
		out = append(out, scoutRound{Round: r, Pos: pos})
	}
	return out
}

// scoutModalTeams is the league size most of the scored drafts ran at. Used only
// to turn an overall pick into round.pick for the room-curve lines, so the modal
// answer is the right one — a 10-team dynasty in the set should not renumber the
// 12-team rounds.
func scoutModalTeams(teams map[int]int) int {
	best, bestN := 12, 0
	for t, n := range teams {
		if t > 0 && (n > bestN || (n == bestN && t < best)) {
			best, bestN = t, n
		}
	}
	return best
}

func scoutNoteSkips(rows []scoutDraft, verbose bool) {
	if verbose {
		return // already printed in full
	}
	skipped := 0
	for _, r := range rows {
		if !r.Scored {
			skipped++
		}
	}
	if skipped > 0 {
		note("drafts", "skipped", fmt.Sprintf("%d not profiled — run with -v for the reasons", skipped))
	}
}

// scoutPrintFirst is the headline table: when each manager takes his first at
// the four positions where taking one early is a choice.
func scoutPrintFirst(base scoutBaseline, managers []scoutManager) {
	fmt.Printf("\nfirst pick at a position — shrunk expected round (a = %.0f):\n", scoutShrink)
	fmt.Printf("  %-20s %6s %6s", "manager", "drafts", "picks")
	for _, pos := range scoutFirstPositions {
		fmt.Printf(" %6s", strings.ToLower(pos))
	}
	fmt.Printf("  %-8s %5s\n", "leans", "auto")

	leagueShare := scoutShare(base.ByRound)
	for _, m := range managers {
		fmt.Printf("  %-20s %6d %6d", scoutLabel(m, 20), m.Drafts, m.Picks)
		for _, pos := range scoutFirstPositions {
			fmt.Printf(" %6.1f", m.First[pos].Expected)
		}
		fmt.Printf("  %-8s %4.0f%%\n", scoutLean(m.ByRound, leagueShare), 100*m.AutoFloor)
	}

	// The room's own row, so every number above has something to be early or
	// late against.
	fmt.Printf("  %-20s %6d %6d", "the room", base.Drafts, base.Picks)
	for _, pos := range scoutFirstPositions {
		fmt.Printf(" %6.1f", expectedRound(base.First[pos].ByRound))
	}
	fmt.Printf("  %-8s %4.0f%%\n", "-", 100*scoutRatio(base.AutoPicks, base.Picks))

	fmt.Printf("  the column is the expected round of his first at the position, off a curve shrunk\n")
	fmt.Printf("  toward the room: (count + %.0f*league rate) / (n + %.0f). a manager seen once keeps a\n",
		scoutShrink, scoutShrink)
	fmt.Printf("  third of his own evidence, which is deliberate — one draft is an anecdote.\n")
	fmt.Printf("  %d means he usually finishes the draft without one.\n", base.Rounds+1)
	fmt.Printf("  leans is the position he spends the most of his board on against the room, in points.\n")
	fmt.Printf("  \"the room\" pools every scored draft, which may span more than one league — the\n")
	fmt.Printf("  prior is how the people you draft against behave, not one league's house style.\n")
}

// scoutPrintShare regenerates the room's positional share by round from live
// data — the table in claude.md, except nobody typed this one.
func scoutPrintShare(base scoutBaseline) {
	fmt.Printf("\npositional share by round — the whole room:\n")
	fmt.Printf("  %5s %5s", "round", "picks")
	for _, pos := range scoutPositions {
		fmt.Printf(" %5s", strings.ToLower(pos))
	}
	fmt.Println()
	for _, r := range base.ByRound {
		total := 0
		for _, n := range r.Pos {
			total += n
		}
		if total == 0 {
			continue
		}
		fmt.Printf("  %5d %5d", r.Round, total)
		for _, pos := range scoutPositions {
			fmt.Printf(" %4.0f%%", 100*scoutRatio(r.Pos[pos], total))
		}
		fmt.Println()
	}
	fmt.Println("  rows are every manager's picks in that round, across every scored draft.")
}

// scoutPrintRoom is phase 3a's read, in the place the spec put it for the case
// where the warp failed its gate — which at full depth it did (brier 0.0670 ->
// 0.0671, log-loss 0.2250 -> 0.2327 cross-validated on the 2024 draft; those are
// the numbers `pick6 calibrate` prints today, and every other citation in the
// tree quotes the same pair). The board prices only the top adp.RoomGapTopK at
// each position, so most of what this table shows is still a read for the human
// and nothing else. It prints every depth on purpose: knowing where the room's
// opinion runs out is the same information as knowing where it is worth pricing.
//
// Built from the drafts this walk actually scored rather than from a hardcoded
// list, so a dynasty draft that got skipped above cannot sneak into the curve.
//
// Drafts at another league size are dropped even when they were scored: the
// curve is made of pick NUMBERS, and pick 30 is round 3 in a 10-team draft and
// the middle of round 3 in a 12-team one. Averaging them moves every number here
// and nothing would look wrong. `-dynasty` is the flag that makes this reachable.
func scoutPrintRoom(drafts map[string]adp.RoomDraft, teamsOf map[string]int, teams int) {
	sized := map[string]adp.RoomDraft{}
	for id, d := range drafts {
		if teamsOf[id] == teams {
			sized[id] = d
		}
	}
	curve := adp.RoomCurveOf(sized)
	if curve.Empty() {
		return
	}
	fmt.Printf("\nroom curve — where this league takes the k-th player at a position (round.pick, %d teams):\n", teams)
	for _, pos := range scoutPositions {
		if curve.Depth(pos) == 0 {
			continue
		}
		var parts []string
		for _, k := range roomCurveKs {
			pick, _, ok := curve.At(pos, k)
			if !ok {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s%d by %s",
				strings.ToLower(pos), k, roundPick(pick, teams)))
		}
		if len(parts) > 0 {
			fmt.Printf("  room takes %s\n", strings.Join(parts, " · "))
		}
	}
	fmt.Printf("  mean over %d drafts of where the k-th player at the position went, monotonized.\n", curve.Drafts)
	fmt.Printf("  survival blends the first %d of each position toward these numbers; deeper k are a\n",
		adp.RoomGapTopK)
	fmt.Println("  read only. `pick6 fetch` prints the same curve against market adp, and")
	fmt.Println("  `pick6 calibrate` prints the gate that drew the line where it is.")
}

// scoutPrintAuto reports the autodraft floor, including — especially — when it
// is zero.
func scoutPrintAuto(base scoutBaseline, managers []scoutManager) {
	fmt.Printf("\nautodraft floor — the share of a manager's picks sleeper attributed to nobody:\n")
	var loud []string
	for _, m := range managers {
		if m.AutoPicks > 0 {
			loud = append(loud, fmt.Sprintf("%s %.0f%% (%d of %d)",
				strings.ToLower(m.Name), 100*m.AutoFloor, m.AutoPicks, m.Picks))
		}
	}
	if len(loud) == 0 {
		fmt.Printf("  measured 0.0%% for all %d managers: every one of %d picks carries a picked_by.\n",
			len(managers), base.Picks)
		fmt.Println("  that is the honest number, not a broken query — this room has never left autodraft")
		fmt.Println("  on. the metric ships anyway because v2's opponent model wants it: a manager who")
		fmt.Println("  autodrafts simulates as pure adp, and this column is where it would show up.")
		return
	}
	for _, l := range loud {
		fmt.Printf("  %s\n", l)
	}
	fmt.Println("  a high floor means he lets the clock pick for him — simulate him as pure adp.")
}

func scoutPrintDrafts(rows []scoutDraft) {
	fmt.Printf("\nevery draft found:\n")
	fmt.Printf("  %-20s %5s %-7s %-10s %6s %6s %6s  %s\n",
		"draft", "year", "type", "status", "teams", "rounds", "picks", "used")
	for _, r := range rows {
		used := "yes"
		if !r.Scored {
			used = r.Skip
		}
		fmt.Printf("  %-20s %5s %-7s %-10s %6d %6d %6d  %s\n",
			r.DraftID, r.Season, strings.ToLower(r.Type), strings.ToLower(r.Status),
			r.Teams, r.Rounds, r.Picks, used)
	}
}

// scoutShare collapses per-round counts into one overall share per position.
func scoutShare(rounds []scoutRound) map[string]float64 {
	total := 0
	count := map[string]int{}
	for _, r := range rounds {
		for pos, n := range r.Pos {
			count[pos] += n
			total += n
		}
	}
	out := map[string]float64{}
	for pos, n := range count {
		out[pos] = scoutRatio(n, total)
	}
	return out
}

// scoutLean names the position a manager spends the most of his board on
// relative to the room, in percentage points. Descriptive, not predictive — it
// is the one column that answers "and what does he actually draft".
func scoutLean(rounds []scoutRound, league map[string]float64) string {
	his := scoutShare(rounds)
	best, bestPos := 0.0, ""
	for _, pos := range scoutPositions {
		if d := his[pos] - league[pos]; d > best {
			best, bestPos = d, pos
		}
	}
	if bestPos == "" {
		return "-"
	}
	return fmt.Sprintf("%s +%.0f", strings.ToLower(bestPos), 100*best)
}

// scoutLabel is a manager's name, lowercased and cut to fit its column.
// Truncated by rune, not by byte: display names carry emoji, and half an emoji
// is a broken cell.
func scoutLabel(m scoutManager, width int) string {
	name := []rune(strings.ToLower(m.Name))
	if len(name) > width {
		return string(name[:width-1]) + "…"
	}
	return string(name)
}

func scoutRatio(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}

// roundPick renders an overall pick as round.pick — "5.02". The %02d matters:
// "5.2" reads as pick two in a way that sorts and scans wrong next to "5.12",
// and the ui has made exactly that mistake before.
func roundPick(pick float64, teams int) string {
	if teams < 1 {
		teams = 12
	}
	p := int(pick + 0.5)
	if p < 1 {
		p = 1
	}
	return fmt.Sprintf("%d.%02d", (p-1)/teams+1, (p-1)%teams+1)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
