package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/engine"
)

// The room warp is ON, so the thing worth pinning is the way out of it.
//
// Go's flag package reads a bare `-room false` as a set boolean followed by a
// positional argument, which on `live` is where the draft id lives — so a user
// typing the obvious thing gets the warp still on and a mangled command line.
// `-room=false` is the only opt-out, the help text says so, and the last case
// below is the reason it has to.
func TestRoomFlagIsOnAndOptsOutWithEquals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"on by default", nil, true},
		{"the documented opt-out", []string{"-room=false"}, false},
		{"bare -room is still on", []string{"-room"}, true},
		{"=0 is the same opt-out", []string{"-room=0"}, false},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("room", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		room := roomFlag(fs)
		if err := fs.Parse(c.args); err != nil {
			t.Fatalf("%s: parsing %v: %v", c.name, c.args, err)
		}
		if *room != c.want {
			t.Errorf("%s: -room parsed to %v, want %v", c.name, *room, c.want)
		}
	}

	fs := flag.NewFlagSet("room", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	room := roomFlag(fs)
	if err := fs.Parse([]string{"-room", "false"}); err != nil {
		t.Fatalf("parsing a spaced -room false: %v", err)
	}
	if !*room || fs.Arg(0) != "false" {
		t.Errorf("`-room false` parsed to %v with arg %q, want true and a stray \"false\" — "+
			"if this ever changes, the help text may stop insisting on the = form",
			*room, fs.Arg(0))
	}
}

// The whole of phase 3a's shipped behaviour, through the real call site.
//
// THE CURVE'S REACH IS THE LIMIT, and nothing else. There used to be a cap at
// adp.RoomWarpTopK = 5 and this test asserted it; both 2025 folds beat it on
// both metrics AND regressed fewer positions than it did, so it was removed —
// see internal/adp/room.go's RoomGapTopK comment for the numbers. This fixture's
// curve covers seven backs, so all seven must move: a warp that still stopped at
// five would pass every other assertion here.
//
// Hand-computed. One draft takes running backs at picks 1..7, so adp_room(RB, k)
// is k over n = 1 draft, and w = n/(n + RoomWarpPseudo) = 1/3. Against fake adps
// of 10k that is (k + 2*10k)/3 = 7k: 7, 14, 21, 28, 35. Non-decreasing, so the
// blend's running max never fires and each number is the blend alone.
func TestLoadBoardWarpsAsDeepAsTheCurveReaches(t *testing.T) {
	fakeBoardHome(t, 7)

	board, err := loadBoard(nfl, true, "")
	if err != nil {
		t.Fatalf("loadboard: %v", err)
	}
	cases := []struct {
		name string
		id   string
		want float64
	}{
		{"rb1 blends 1/3 of the room's pick 1", "rb1", 7},
		{"rb2", "rb2", 14},
		{"rb3", "rb3", 21},
		{"rb4", "rb4", 28},
		{"rb5", "rb5", 35},
		// Past the old cutoff and still inside the curve, so both move now.
		{"rb6 is past the retracted cutoff", "rb6", 42},
		{"rb7 likewise", "rb7", 49},
		// No receiver was ever taken in the fixture draft, so the curve has no
		// opinion about him at any rank.
		{"a position the room never took", "wr1", 0},
	}
	for _, c := range cases {
		// A tolerance because the blend is w*room + (1-w)*adp with w = 1/3, and
		// thirds do not land on the nail in binary — rb1 comes out
		// 7.000000000000001. An exact compare here fails on arithmetic rather
		// than on behaviour.
		if got := board[c.id].ADPEff; math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: %s adpeff = %.9f, want %.9f", c.name, c.id, got, c.want)
		}
	}
	// The raw price is what every other reader still sees — the display columns,
	// the sort tie-break, the mock's picker. A warp that overwrote it would move
	// the adp column on screen and make the mock draft against itself.
	if got := board["rb1"].ADP; got != 10 {
		t.Errorf("rb1 raw adp = %.4f, want 10 — the warp overwrote the market price", got)
	}

	// The opt-out, over the same cache: nobody carries a warped price at all.
	off, err := loadBoard(nfl, false, "")
	if err != nil {
		t.Fatalf("loadboard -room=false: %v", err)
	}
	for id, p := range off {
		if p.ADPEff != 0 {
			t.Errorf("-room=false left %s carrying adpeff %.4f", id, p.ADPEff)
		}
	}
}

// Draft-day wifi, and the reason the whole path reads disk only: with no drafts
// cached the board must come up on raw national adp and say so, never error and
// never wait on a network round trip. This is no longer a flag nobody passes —
// it runs on every mock and live startup.
func TestLoadBoardFallsBackWithNoCachedDrafts(t *testing.T) {
	fakeBoardHome(t, 0)

	board, err := loadBoard(nfl, true, "")
	if err != nil {
		t.Fatalf("an uncached room must not be an error: %v", err)
	}
	if len(board) == 0 {
		t.Fatal("board came back empty")
	}
	for id, p := range board {
		if p.ADPEff != 0 {
			t.Errorf("%s carries adpeff %.4f with no drafts to build a curve from", id, p.ADPEff)
		}
	}

	// And nothing may have been written while looking: a file appearing in a
	// fresh cache dir means something downloaded it, which is exactly the bug
	// adp.loadRoomDrafts carries a comment about.
	dir, err := cache.Dir()
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob(filepath.Join(dir, "draft_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) > 0 {
		t.Errorf("the board pulled %d draft file(s) off the network: %v", len(names), names)
	}
}

// fakeBoardHome points the cache dir at a temp home and fills it with an
// obviously fake board: seven running backs at adp 10, 20, ... 70 and one
// receiver. With roomRBs > 0 it also writes one completed draft that takes that
// many backs at picks 1..roomRBs, which is the whole curve.
//
// Only the first of leagueDrafts is written, so the other two report as
// uncached — the partial-failure path a real machine hits between fetches.
func fakeBoardHome(t *testing.T, roomRBs int) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "") // linux reads this first; darwin reads HOME
	dir, err := cache.Dir()
	if err != nil {
		t.Fatal(err)
	}

	var list []adp.Player
	for k := 1; k <= 7; k++ {
		list = append(list, adp.Player{
			SleeperID: fmt.Sprintf("rb%d", k),
			Name:      fmt.Sprintf("fake back %d", k),
			Pos:       "RB", Team: "FA",
			ADP: float64(10 * k), Sigma: engine.SigmaDefault, Value: 100 - k, Tier: 1,
		})
	}
	list = append(list, adp.Player{
		SleeperID: "wr1", Name: "fake receiver", Pos: "WR", Team: "FA",
		ADP: 15, Sigma: engine.SigmaDefault, Value: 90, Tier: 1,
	})
	writeJSON(t, filepath.Join(dir, "players.json"), list)
	if roomRBs == 0 {
		return
	}

	id := leagueDrafts[0]
	writeJSON(t, filepath.Join(dir, "draft_"+id+".json"), map[string]any{
		"draft_id": id, "season": "2024", "type": "snake", "status": "complete",
	})
	var picks []map[string]any
	for k := 1; k <= roomRBs; k++ {
		picks = append(picks, map[string]any{
			"pick_no":   k,
			"player_id": fmt.Sprintf("rb%d", k),
			"metadata":  map[string]any{"position": "RB"},
		})
	}
	writeJSON(t, filepath.Join(dir, "draft_"+id+"_picks.json"), picks)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
