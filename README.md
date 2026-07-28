# pick6

Terminal war room for fantasy football drafts. Live-syncs Sleeper, tracks tiers, and tells you when the RB run means the value's gone.

## status

Milestone 1 of 6. `pick6 fetch` works; the board doesn't exist yet.

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
