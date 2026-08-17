#!/usr/bin/env bash
# The readme's screenshots, as code — the same deal as demo.tape. Every shot is
# a headless -snapshot frame piped through freeze, with the seed pinned, so a ui
# change re-renders the whole set with one command and the screenshots can never
# quietly rot out of sync with the board they claim to show.
#
# Needs `pick6 fetch` to have run (the frames draw real players from the cache),
# a pick6 on PATH, and freeze (go install github.com/charmbracelet/freeze@latest).
#
# The `script -q /dev/null` wrapper is load-bearing: it hands pick6 a pty inside
# the pipeline, because a piped pick6 is not a tty and lipgloss quantizes the
# palette to 16 colours — the red banner comes out black. The tr strips the
# carriage returns script adds; the tail drops the room-note preamble, whose one
# long line otherwise sets the window width for the whole image.
set -euo pipefail
cd "$(dirname "$0")/.."

shot() {
  local out="docs/shots/$1" skip="$2"
  shift 2
  freeze --execute "bash -c 'script -q /dev/null $* | tr -d \"\r\" | tail -n +$((skip + 1))'" \
    --output "$out" \
    --window \
    --padding 16 \
    --background "#1A1B26" \
    --font.size 14
}

mkdir -p docs/shots

# the clock: verdict block, plan line, the field ranked under it
shot board-clock.svg 2 pick6 mock -slot 3 -seed 5 -snapshot 26

# off the clock: cliff banner firing, a falling first-rounder, an avoid chip
shot board-forecast.svg 2 pick6 mock -slot 3 -seed 5 -snapshot 10

# the data tab: every number the engine holds, opinions riding the names
shot data-tab.svg 2 pick6 mock -slot 3 -seed 5 -snapshot 40 -data

# the tiers view: the ladder, every man grouped by tier, the taken struck. Not a
# rankings-file view on purpose — those are the user's own files and the readme
# ships the default board only.
shot tiers-view.svg 2 pick6 mock -slot 3 -seed 5 -snapshot 40 -tab data -view tiers

# kicker o'clock: k/def unsuppressed, the file's endgame calls on screen
shot endgame.svg 2 pick6 mock -slot 3 -seed 5 -snapshot 158 -data

# the notes tab: your own files beside the draft map, names struck as they go
shot notes-tab.svg 2 pick6 mock -slot 12 -seed 5 -snapshot 100 -tab notes -notes docs/notes-example

# the tier board, straight from the cache
shot tiers.svg 0 pick6 tiers -pos wr -depth 15
