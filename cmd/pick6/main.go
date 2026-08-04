package main

import (
	"flag"
	"fmt"
	"os"
)

// version is stamped by the release workflow's -ldflags. A build from source
// says "dev", which is the honest answer for one.
var version = "dev"

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
		fmt.Printf("pick6 %s\n", version)
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
