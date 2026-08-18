package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/trisslazaj/pick6/internal/adp"
	"github.com/trisslazaj/pick6/internal/cache"
	"github.com/trisslazaj/pick6/internal/ui"
)

// notesFlag declares -notes, which mock, board and live share: the folder the
// notes tab reads. One declaration so the three commands cannot disagree about
// where your notes live. Empty means the config dir's notes folder.
func notesFlag(fs *flag.FlagSet) *string {
	return fs.String("notes", "", "folder of markdown notes for the notes tab (default: ~/.config/pick6/notes)")
}

// rankingsFlag declares -rankings, which mock, board and live share: the
// folder of rankings csvs the data tab shows one view each. The file `fetch
// -rankings` loaded is always a view too, wherever it lives, so the usual case
// needs no folder at all. Empty means the config dir's rankings folder.
func rankingsFlag(fs *flag.FlagSet) *string {
	return fs.String("rankings", "", "folder of rankings csvs, one data-tab view each (default: ~/.config/pick6/rankings)")
}

// tabFlag declares -tab for the headless frames: which tab a snapshot renders.
// -data survives as the older spelling of -tab data.
func tabFlag(fs *flag.FlagSet) *string {
	return fs.String("tab", "", "with -snapshot/-replay: which tab to render — board, data or notes")
}

// viewFlag declares -view for the headless frames: which data-tab view.
func viewFlag(fs *flag.FlagSet) *string {
	return fs.String("view", "", "with -tab data: which view — value, adp, tiers, or a rankings file's name")
}

// rankingsDir resolves -rankings the same way notesDir resolves -notes, with a
// per-sport subfolder past nfl's.
//
// Without the split, an fpl board's data tab lists every one of the user's nfl
// rankings csvs as a view, each rendering nothing but `?` rows — the names are
// simply not fpl players. A view that claims to show a file verbatim and shows
// nothing is worse than no view.
func rankingsDir(sp sport, flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	base := filepath.Join(notesRoot(), "rankings")
	if sp.name == nfl.name {
		return base
	}
	return filepath.Join(base, sp.name)
}

// fetchedRankings is the file `fetch -rankings` loaded, off meta.json; "" when
// there wasn't one, there is no meta, or this sport doesn't keep one.
func fetchedRankings(sp sport) string {
	if !sp.meta {
		return ""
	}
	dir, err := cache.Dir()
	if err != nil {
		return ""
	}
	m, err := adp.LoadMeta(dir)
	if err != nil {
		return ""
	}
	return m.TiersFile
}

// notesDir resolves the flag: the folder given, else ~/.config/pick6/notes.
// Not os.UserConfigDir(): on a mac that is "~/Library/Application Support",
// which is the wrong place for files you hand-edit the night before a draft —
// nobody can tab-complete it. ~/.config is what a terminal person types.
// $XDG_CONFIG_HOME wins when set. A folder that doesn't exist yet is fine —
// the tab says where to put the first file.
func notesDir(sp sport, flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	dir := filepath.Join(notesRoot(), "notes")
	if sp.name == nfl.name {
		return dir
	}
	// A per-sport subfolder, same rule as the rankings views and for the same
	// reason: a seat file about round-one running backs is not a note about a
	// premier league draft, and every player name in it colours nothing and
	// strikes through nothing. Notes are the human's side of the argument the
	// board is making — a different board is a different argument.
	return filepath.Join(dir, sp.name)
}

func notesRoot() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pick6")
}

// pickTab applies -tab / -data / -view to a headless board.
func pickTab(b *ui.Board, tab string, data bool, view string) {
	switch tab {
	case "data":
		b.Tab = 1
	case "notes":
		b.Tab = 2
	default:
		if data {
			b.Tab = 1
		}
	}
	if view != "" {
		b.Tab = 1
		b.SelectView(view)
	}
}
