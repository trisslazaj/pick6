# pick6

Terminal war room for fantasy football drafts. Live-syncs Sleeper, tracks tiers, and tells you when the RB run means the value's gone.

## status

Milestone 4 of 6. The board renders, live-syncs a Sleeper draft, and thinks: survival
probability, urgency ordering, cliff and run alerts are all live. Left: polish + release,
then the opponent-aware simulation.

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
pick6 live <draft_id> -user yourname   # the main event: sync a live sleeper draft
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

## the math, in plain english

The whole board answers one question: **what does waiting cost?**

**Survival.** Every player has an ADP — the average pick where real drafts take him, measured
across about a thousand recent drafts — and a spread, because the market doesn't agree on
everyone equally: a locked-in first-rounder goes inside a two-pick window, a late flier goes
anywhere across three rounds. Feed "my next pick minus his ADP, measured in his own spread"
through an S-curve and out comes the chance he's still on the board when you're up. ADP already
passed → probably gone. ADP far ahead → safe. ADP exactly at your pick → coin flip, which is
literally what ADP means — half the rooms had taken him by then.

One honesty adjustment: a player who's on the board *right now* can only be taken by the picks
between now and your turn. So the number is really "chance he lasts to my pick, given that he's
lasted this long." Without that, a player the room keeps passing on reads 90% gone when only one
team picks before you do.

**Urgency.** Per position: take the best player available now, and the best player with at least
a coin flip's chance of surviving to your next pick. The value gap between those two is what
waiting costs you — scaled by whether you actually need the position (open starter beats flex
depth beats bench, and kickers count for nothing until the end). The board sorts by that number,
and zero urgency is itself the signal: your guy will still be there. Wait.

**Counting.** Tiers come from human rankings, or from value gaps when no file provides them. A
tier that's emptying is a cliff — two left is amber, last one is red. Four of the last six picks
at one position is a run. And a player still available a full spread past his ADP is *falling*:
the draft moved past his price and he's still here. His numbers turn amber. That's a discount.

## data sources

- **ADP** from [Fantasy Football Calculator](https://fantasyfootballcalculator.com), whose free
  ADP API made this possible.
- **Player values and ID crosswalk** from [FantasyCalc](https://fantasycalc.com).
- **Player pool and live draft feed** from [Sleeper](https://docs.sleeper.com/).

## license

MIT — see [LICENSE](LICENSE).
