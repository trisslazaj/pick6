package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/trisslazaj/pick6/internal/ui"
)

// notesFlag declares -notes, which mock, board and live share: the folder the
// notes tab reads. One declaration so the three commands cannot disagree about
// where your notes live. Empty means the config dir's notes folder.
func notesFlag(fs *flag.FlagSet) *string {
	return fs.String("notes", "", "folder of markdown notes for the notes tab (default: <config dir>/pick6/notes)")
}

// tabFlag declares -tab for the headless frames: which tab a snapshot renders.
// -data survives as the older spelling of -tab data.
func tabFlag(fs *flag.FlagSet) *string {
	return fs.String("tab", "", "with -snapshot/-replay: which tab to render — board, data or notes")
}

// notesDir resolves the flag: the folder given, else the config dir's. A folder
// that doesn't exist yet is fine — the tab says where to put the first file.
func notesDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "notes"
	}
	return filepath.Join(base, "pick6", "notes")
}

// pickTab applies -tab / -data to a headless board.
func pickTab(b *ui.Board, tab string, data bool) {
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
}
