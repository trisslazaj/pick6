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

In the TUI, `tab` flips between the board and a full data table — every available player with
value, tier, ADP, spread, survival and format spread on one screen (`j/k` scrolls, `p` filters
by position). The board abstracts; the table doesn't.

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
anywhere across three rounds. The chance he lasts to pick $p$ is a logistic S-curve centred on
his ADP, with the width set by his *own* spread:

$$S(p) = \frac{1}{1 + e^{(p - \mathrm{adp})/\sigma}}$$

ADP already passed → $S$ near 0, probably gone. ADP far ahead → near 1, safe. And at
$p = \mathrm{adp}$ it's exactly $\tfrac{1}{2}$ — a coin flip, which is literally what ADP means:
half the rooms had taken him by then.

The width comes from the measured standard deviation of his real draft slot (for a logistic
distribution $\mathrm{stdev} = \sigma \pi / \sqrt{3}$):

$$\sigma = \frac{\sqrt{3}}{\pi}\,\mathrm{stdev} \approx \frac{\mathrm{stdev}}{1.8138},
\qquad \sigma \ \text{clamped to} \ [0.5,\ 25]$$

So a locked-in star (stdev ≈ 1) gets a near-step-function — one pick past his ADP and he is
simply gone — while a volatile flier (stdev ≈ 40) gets a nearly flat curve: stop panicking, he
keeps.

One honesty adjustment: a player who's on the board *right now* can only be taken by the picks
between now and your turn. So the number shown is survival **conditioned on the present**:

$$p_{\text{survive}} = \frac{S(\text{nextPick})}{S(\text{pickNo})}$$

Without that, a player the room keeps passing on reads 90% gone when only one team picks before
you do. Deep past his ADP the ratio's tail becomes a clean per-pick hazard,
$e^{-(\text{nextPick} - \text{pickNo})/\sigma}$ — each pick that passes costs him the same
factor, no matter how far he's fallen. (It's computed in log space so the extremes don't
flatten; see `internal/engine/urgency.go`.)

One correction on top of that. Exactly $N$ players come off the board before your turn — that's
what a draft is — but treating every survival as independent "expects" $\sum_j (1 - p_j)$ of them
to go, and that sum is measurably not $N$. Backtested against a real draft, the model was
pessimistic across the board. So raise every probability to the single power that reconciles
them:

$$\text{find } c > 0 \ \text{ with } \ \sum_j \big(1 - p_j^{\,c}\big) = N,
\qquad \tilde p_j = p_j^{\,c}$$

It's the gentlest fix there is: $\ln p$ is a player's hazard over those picks, so $p^c$ scales
everyone's hazard by the same factor. Certainties stay certain ($1^c = 1$), nobody overtakes
anybody, and $\tilde p$ is what every number on screen means.

**Urgency.** Per position: the best player available now, versus the expected value of whoever
is still there at your next pick. Line the position up best-first — the best survivor is #1 if he
survives, #2 only if #1 is gone *and* #2 survives, and so on:

$$\mathbb{E}[\text{best later}] = \sum_j v_j\, \tilde p_j \prod_{i<j} \big(1 - \tilde p_i\big)$$

$$\text{urgency} = \Big( v(\text{bestNow}) - \mathbb{E}[\text{best later}] \Big) \times \text{need}$$

— where need is 1.0 for an open starter slot, 0.6 for flex depth, 0.25 for bench, and 0 for
kickers and defenses until the last rounds. The expected drop is what waiting costs; need is
whether you should care. The board sorts by it. And because it's an expectation rather than a
cutoff, nothing jumps: a player crossing 50% doesn't re-sort the board on a rounding error. The
wait signal is its own claim now — *safe to wait*, green, when the best man himself is more
likely than not to still be sitting there.

**Counting.** Tiers come from human rankings; where no file covers, they're derived by breaking
the value curve wherever it genuinely drops:

$$\frac{v_{i-1} - v_i}{v_{i-1}} > 0.10
\quad\text{and}\quad
v_{i-1} - v_i \ \ge\ 0.015 \cdot v_{\max}$$

A tier is a cliff when it probably won't reach you, which is a probability and not a headcount —
three players the room is about to eat is a worse cliff than one player nobody wants:

$$p_{\text{hold}} = 1 - \prod_j \big(1 - \tilde p_j\big) \quad \text{over what's left in the tier}$$

Under 50% is amber, under 15% is red, and a tier nobody has drafted out of yet is never either.
Four of the last six picks at one position is a run. And a player still available a full spread
past his ADP,

$$\frac{\text{pickNo} - \mathrm{adp}}{\sigma} \ \ge\ 1,$$

is *falling*: the draft moved past his price and he's still here. His numbers turn amber.
That's a discount.

## data sources

- **ADP** from [Fantasy Football Calculator](https://fantasyfootballcalculator.com), whose free
  ADP API made this possible.
- **Player values and ID crosswalk** from [FantasyCalc](https://fantasycalc.com).
- **Player pool and live draft feed** from [Sleeper](https://docs.sleeper.com/).

## license

MIT — see [LICENSE](LICENSE).
