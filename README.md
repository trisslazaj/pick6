# pick6

Terminal war room for fantasy football drafts. Live-syncs Sleeper, tracks tiers, and tells you when the RB run means the value's gone.

## status

Milestone 2 of 6. The board renders; live Sleeper sync isn't wired up yet.

## install

```
go install github.com/trisslazaj/pick6/cmd/pick6@latest
```

That puts the binary in `go env GOPATH`/bin, which is **not** on most people's
PATH by default. Either add it, or install somewhere that already is:

```
GOBIN=$HOME/bin go install ./cmd/pick6
```

Working on the code instead? `go run ./cmd/pick6 <cmd>` always runs current
source, with nothing to reinstall.

## use

```
pick6 fetch                  # pull data (do this first)
pick6 mock                   # watch a scripted draft play out on the real board
pick6 mock -auto=false       # step through it yourself with space
pick6 tiers                  # print the current tier board
pick6 mock -snapshot 26      # print one frame, no tui (for screenshots)
```

```
go run ./cmd/pick6 fetch
```

Pulls the Sleeper player pool, half-PPR ADP, and player values; matches everything onto Sleeper
player ids; derives value tiers; and caches it all under `~/Library/Caches/pick6/`. Re-running
inside 12 hours is served entirely from disk.

```
pick6 fetch -format half-ppr -teams 12
pick6 fetch -rankings my-rankings.csv     # your tiers and points win
```

Bring your own rankings and they take priority over everything fetched. Column order doesn't
matter and unknown columns are ignored, so FantasyPros exports and hand-made files both load:

```
name,position,team,tier,points
```

## data sources

- **ADP** from [Fantasy Football Calculator](https://fantasyfootballcalculator.com), whose free
  ADP API made this possible.
- **Player values and ID crosswalk** from [FantasyCalc](https://fantasycalc.com).
- **Player pool and live draft feed** from [Sleeper](https://docs.sleeper.com/).

## license

MIT — see [LICENSE](LICENSE).
