package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
)

// version is stamped by the release workflow's -ldflags, which only the
// prebuilt archives get.
var version = "dev"

// buildVersion is what `pick6 version` actually prints, and the fallback is the
// point of it: the readme leads with `go install ...@latest`, which compiles
// from source on the user's own machine with no ldflags anywhere, so every
// installation down that path called itself "dev" — including the one that had
// just fetched a tagged release. go records the module version it built from,
// so ask it.
//
// "(devel)" is what that field says for a plain `go build` in a checkout, which
// is a build with no version rather than a version called "(devel)".
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

const usage = `pick6 — terminal draft war room

usage:
  pick6 fetch       download and cache player data + adp, build the mapping
  pick6 tiers       print the current tier board (run fetch first)
  pick6 mock        replay a scripted draft against the real ui
  pick6 live        poll a sleeper draft and render the board
  pick6 calibrate   score the survival model against drafts that already happened
  pick6 scout       profile your leaguemates from every draft they've played
  pick6 board       the same board with no feed: mark picks by hand as they happen
  pick6 version     what build this is
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "fetch":
		if err := runFetch(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "fetch failed: %v\n", err)
			os.Exit(1)
		}
	case "tiers":
		if err := runTiers(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "tiers failed: %v\n", err)
			os.Exit(1)
		}
	case "mock":
		if err := runMock(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "mock failed: %v\n", err)
			os.Exit(1)
		}
	case "live":
		if err := runLive(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "live failed: %v\n", err)
			os.Exit(1)
		}
	case "calibrate":
		if err := runCalibrate(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "calibrate failed: %v\n", err)
			os.Exit(1)
		}
	case "scout":
		if err := runScout(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "scout failed: %v\n", err)
			os.Exit(1)
		}
	case "board":
		if err := runBoard(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "board failed: %v\n", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Printf("pick6 %s\n", buildVersion())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

// flagSet keeps the per-subcommand flag boilerplate in one place.
func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: pick6 %s [flags]\n\nflags:\n", name)
		fs.PrintDefaults()
	}
	return fs
}
