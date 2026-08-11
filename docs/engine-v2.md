# engine v2 — opponent-aware survival (milestone 6)

Milestones 1–5 shipped, so this is live. This file was rewritten at the start of milestone 6:
the original spec predated milestone 4x and knew nothing about the tilt, ActPick, PickChoices,
the room warp, `calibrate` or `scout`. Everything below is written against the engine as it
exists today, and the differences from the original spec are called out where they matter.

**v2 replaces the survival number and nothing else.** Everything downstream — `EBest`, urgency,
tier hold, `PickChoices`, `SafeToWait`, `BestLater`, the banners, both tabs — consumes `p~`
unchanged. **Sim is the default** — user decision, 2026-08-10: the monte carlo sim is the
feature, and the math is geared toward it. `--survival=adp` stays as the fallback and the
comparison baseline, not as the default.

One correction to that sentence, and it is the single most important integration fact:
**v2 replaces the tilt too.** The exactly-N tilt exists because independent logistic survivals
don't respect the removals budget — a draft removes exactly N players and the model expects
some other number. A rollout removes exactly N players *by construction*, every time. Applying
the tilt on top of sim output would balance a budget that is already balanced and distort every
number. Sim output is consumed raw (well, smoothed — see the rollout section). The tilt itself
stays in the code: the `adp` fallback path still needs it, and calibrate's baselines score it.

## Why

v1 treats the picks between mine as random draws from "drafters in general." They're not —
they are specific rosters, and Sleeper publishes every one of them in `State.Rosters`. If the
three teams ahead of me all have full RB rooms, the RB I want is far safer than ADP says. That
gap is the entire feature.

Two arguments that didn't exist when this spec was first written:

- **The sim natively fixes the tilt's thin-board fragility.** The depth control found that on a
  board no deeper than the draft, the tilt aims at a budget the board cannot supply — N counts
  every pick but only ranked players can be removed, so it over-corrects on every vantage
  (log-loss regressed on both truncated 2025 folds, one of them with zero clamped vantages).
  The shipped 2026 board is 201 names against a 192-pick draft, which is that regime. The sim
  has no such failure mode: it removes actual players from the actual pool, and a pick's
  "budget" is the pick itself. (It has an adjacent, smaller bias in the same direction —
  off-board picks — deferred and documented below.)
- **Sigma disappears, which makes all three calibrate folds full-strength referees.** The sim's
  uncertainty comes from sampling who the room takes, not from a measured stdev, so the two
  2025 folds — which run flat sigma and could never referee the sigma machinery — can referee
  v2 on exactly equal terms with 2024. v2 is the first survival change every fold can score in
  full.

## Opponent pick model

For an opponent team `t` about to pick at overall pick `q`, the probability they take
available player `j`:

```
weight(j)    = A(j) * need_t(pos_j) ^ Beta(round(q))
P(t takes j) = weight(j) / sum over available weight
```

- `A(j)` — ADP desirability. Let `k(j)` = 0-indexed rank of `j` among *currently available*
  players by **effective price** — `Player.price()`, i.e. the room-warped ADP when a room curve
  is loaded, raw ADP otherwise. `A(j) = exp(-k(j) / Tau)`, `Tau = 5.0`. Drafters overwhelmingly
  take someone near the top of the remaining order. Zero out weights beyond the top
  `CandidatePool = 25` — nobody is taking the 60th-best player, and it shrinks the sample
  space. Using the warped price matters: the warp *is* the measured statement "this room takes
  QBs early", which is exactly what the sim's opponents should do, and `-room=false` reverts
  both the board and the sim together instead of leaving them arguing.
- `need_t(pos)` — the same need rule as mine (`needFrom`), computed over team t's
  `FilledSlots(t)`. This is the whole point: their roster is public, so their needs are
  computable. Note `needFrom` calls `Suppressed`, which uses `KDefLastRounds` — the opponent
  path needs a variant that swaps in `OpponentKDefLastRounds` instead. Do not reuse the
  self-suppression: it answers "what will the tool recommend", not "what will the room do".
- `Beta(r)` — how much need matters by round: `Beta(r) = clamp((r - 3) / 4, 0.0, 1.5)`.
  Rounds 1–3: `Beta = 0`, need is ignored, everyone drafts best-available. This is no longer a
  guess: the 552-pick league profile measured 0% QB in round 1, 8% in round 2, 28% in round 3 —
  need turns on at round 3, exactly where the ramp starts. `need^0 = 1`, so it degrades
  gracefully.
- **Opponent K/DEF rule**: suppress `need_t(K/DEF)` until that team's remaining rounds
  `<= OpponentKDefLastRounds (7)`, not the stricter `KDefLastRounds (3)` we apply to ourselves.
  Measured over the three cached drafts, this room's first kicker goes in round 10 and its
  first defense in round 11. Too strict and the sim keeps kickers "available" that were drafted
  rounds ago; too loose and opponents draft kickers in round 7. Both corrupt every survival
  number, in opposite directions. **The constant is 7, not the 6 this spec first said, because
  of draft length**: "first kicker in round 10" reads as remaining = 6 only in a 15-round
  draft, and the user's own room ran 16 rounds and took two kickers in round 10 — remaining 7.
  Found by the phase-A review pass, verified against the cached picks.

### Zero-weight guard (required)

`need_t(K/DEF)` is 0 before the gate lifts, and `0 ^ Beta` is 0 for any `Beta > 0`. Late in a
draft the top-`CandidatePool` available players can be *entirely* K/DEF, which makes every
weight 0 and the normalizing sum 0 → division by zero.

If `sum(weight) == 0`, drop the need term for that one pick and sample by ADP desirability
alone — the same `A(j)` weights, need ignored. Do not skip the pick and do not panic. (An
earlier draft of this spec said "uniformly"; ADP-alone is what a human forced to pick from a
pool of kickers actually does, and it is what ships.)

## Rollout

Let `far` = the farthest horizon any consumer asks about: `FollowingPick()` when it exists,
else `NextPick()`. Let `S` = the ordered `(team, pick)` list in `[PickNo, far)`.
**My own picks in that window remove nobody** — I am the actor, not a random draw, and v1's
`opponentPicksBefore` already excludes my picks from the tilt's N for the same reason. At the
turn this makes the window between back-to-back picks empty, every survival reads 1, and the
pinned behaviour of `TestSurvivalTiltAcrossMyOwnPickAtTheTurn` is preserved by construction.

```
for m in 1..Rollouts (500):
    avail    = copy of available set
    rosters' = deep copy of rosters              // REQUIRED, see below
    for (t, q) in S:
        if t == me: continue                     // my picks are decisions, not draws
        sample j from P(t takes ·) over avail, need_t from rosters'[t]
        removedAt[m][j] = q
        remove j from avail; append j to rosters'[t]

p_survive(j, at) = (count of m where removedAt[m][j] is unset or >= at, + 0.5)
                 / (Rollouts + 1)
```

**One rollout set serves every horizon.** Recording *when* each player was removed makes
`p_survive(j, at)` answerable for any `at` in the window — NextPick, ActPick and the lookahead's
q2 all read the same 500 rollouts. Do not run separate simulations per horizon.

**The roster copy is not optional.** `|S|` can be up to `2T−2`, so a team at the turn picks
twice inside one rollout. Without updating `rosters'` mid-rollout, `need_t` is frozen and that
team happily drafts two RBs at identical need weight — which is exactly the behaviour v2 exists
to model correctly.

**The +0.5 / +1 smoothing (Jeffreys) is load-bearing, not decoration.** A player taken in all
500 rollouts would otherwise read exactly 0, and calibrate scores log-loss — one such player
surviving in reality costs infinity, which would tank the sim against a logistic that never
emits an exact 0. It also keeps the fixed points honest: nobody reads exactly 0 or 1 off a
finite sample, floor ~0.1%, ceiling ~99.9%.

**Determinism is a rendering requirement, not just a test one.** Seed the RNG from
`(draft id, PickNo)` — `rand.New(rand.NewSource(...))`, one source per recompute. A keypress
re-render must produce bit-identical percentages; a fresh seed per frame would make every
number on the board jiggle between frames with no pick having happened. Tests inject the seed.

**Cost**: `|S| ≤ 2T−2` picks × 500 rollouts × weights over ≤25 candidates — microseconds to
low milliseconds in Go. Recompute on every pick event like everything else. Precompute
`exp(-k/Tau)` for k in 0..24 if you feel the urge to optimize, then stop.

## Integration: the chokepoint

Every v1 consumer computes the same expression — `pow(PSurviveAt(p, at), c)` with
`c = survivalTilt(at, opponentPicksBefore(at))` — at exactly four sites: `PSurviveTilted`
(expect.go), `ebest` (expect.go), `BestLater` (urgency.go), and `TierHold` (detect.go). The UI
only ever calls `PSurviveTilted`.

Refactor those four sites onto one internal survival provider — v1 path: logistic + tilt,
v2 path: rollout table lookup — selected per `State`. The horizons in use today, unchanged:

```
PSurviveTilted / BestLater / TierHold / SafeToWait     ActPick()   (NextPick off the clock,
                                                                    FollowingPick on it)
Urgency                                                NextPick()
PickChoices' second leg (ebest)                        FollowingPick()
```

Two properties the sim path must preserve, both currently arithmetic and pinned by tests:

- **`at == PickNo` short-circuits to 1 for everyone, no rollouts run.** That keeps the
  my-own-pick chain exact: `EBest == v(bestNow)`, urgency exactly 0, the vor tie-break does the
  pointing. A sampled 0.999 is not 1 and would break the exactness the plan hangs off.
- **`Falling` and the price chips keep reading the logistic's inputs.** Falling is "the market
  has passed his price", a claim about ADP, not about this room's rosters. It doesn't consume
  survival and doesn't change.

The second leg's conditioning stays as v1 does it: `ebest(pos, q2, exclude)` drops the
hypothesized first-leg player from the *walk*, and the sim does not remove him from its pool.
This is a known small bias — him staying in the pool soaks up opponent picks that in reality
would hit someone else, so second-leg survivals read slightly safe. Modelling it properly means
per-candidate rollout sets (~6× the work for a second-order correction on a display ranking).
Accept it for v2.0 and note it here so nobody rediscovers it as a bug.

## Phase 0 — the referee comes first

**Build the rollout engine, then point `calibrate` at it before the board ever sees a sim
number.** Same order 4x ran: measure first, so tuning has numbers to aim at instead of vibes.

Mechanics, all already in reach:

- A vantage stands at a seat's pick and asks about its next one. The rosters at that vantage
  are reconstructible exactly — calibrate's `walk` already replays the pick list. Each fold's
  lineup shape comes from its cached draft `settings` (`slots_*`), same as live does.
- The sim's `A(j)` uses the fold's **causal** room curve, through the same `splitByStart`
  machinery — the curve must predate the fold, same as v1. The 2024 fold has no causal curve,
  so its sim runs on raw ADP. That is also exactly what would ship for a 2024-shaped situation,
  so the fold measures a real configuration, not a hobbled one.
- Seed per vantage from `(draft id, pick no)` so runs are reproducible to the byte.
- Cost: ~2,000 vantages per fold × 500 rollouts × ≤22 picks — seconds per fold. Fine for a
  command that already prints reliability tables.

**This is a report, not a gate — user decision, 2026-08-10.** Sim ships as the default
regardless of what the folds say: it is the feature, and the math is geared toward it.
Calibrate still scores it on every fold and prints the comparison against the adp baseline,
because if the folds hate it we want to know where and why — that steers *tuning* (Tau, the
pool, Beta), not whether it ships. The fold table lands in this file either way, including an
ugly one.

Set expectations honestly: v1-with-warp already captures the *room's* positional appetite
through prices, so the sim's marginal edge is specifically per-team roster state — expect it
late (K/DEF timing, filled-room effects), not in round 2. The reliability tables by phase are
where it should show first.

### Phase 0 results (2026-08-11, first scoring run)

**Environmental note that moves under every number below: FFC's 2025 archive came alive.**
"No ADP data found" (rechecked 2026-07-30) now serves 2025 — both 2025 folds score on FFC
boards (156 joined players, 718 drafts, real stdev) instead of the hand-exported FantasyPros
CSV. Consequences: per-player sigma machinery now runs on all three folds; the 2025 boards are
THIN (156 names vs 180/192 picks), so the tilt's thin-board over-correction is now the *live*
scoring regime (visible directly: on 2025-a the tilt moves the room-warp row's log-loss 0.2534
→ 0.3123); and none of the 2025 numbers in CLAUDE.md's milestone log are comparable to these —
different board, different depth, different sigma regime.

**THE FIRST RUN'S TABLE WAS LEAKY AND IS RETRACTED.** The initial numbers (sim log-loss
−0.0424 and −0.0118 better on the 2025 folds) were produced with every drafted-but-unranked
player sitting in the sim's pool from vantage one — including players only known to exist
because a FUTURE pick registered them. Live, an off-board player does not exist to the pool
until `EnsurePlayer` sees his pick. The phase-B review caught it, and the mechanism is worth
keeping: those future handcuffs absorbed sim removals in late thin-board windows exactly the
way real off-board picks do, i.e. **the leak was an accidental, future-informed implementation
of the off-board escape this spec had already deferred** — which is both why it scored so well
and why it had to go. Backtest pools now admit an off-board player only after his real pick.

The causal fold table, sim (room-priced, untilted) against the shipped adp row:

```
fold     shipped (brier/log-loss)   v2 sim            verdict
2024     0.0670 / 0.2250            0.0699 / 0.2511   worse on both
2025-a   0.0711 / 0.3075            0.0775 / 0.3027   brier +0.0064 worse · log-loss -0.0048 better
2025-b   0.0774 / 0.3006            0.0837 / 0.3201   worse on both
```

**As of today, v2.0 loses the backtest to shipped v1, modestly, nearly everywhere.** What the
segments say about why, consistently across both causal folds:

- **WR is the sim's genuine win**: better on both metrics on both causal folds (2025-a brier
  0.0751 → 0.0738, log-loss 0.3354 → 0.2833; 2025-b 0.0842 → 0.0802, 0.3420 → 0.2934). The
  deepest position is where roster-aware removal ordering pays, and it survives the leak fix.
- **The losses are the off-board bias, now measured**: the sim makes every window pick remove
  a ranked player, reality doesn't, so the sim over-removes the labeled board — predicted
  survival sits below shipped nearly everywhere late. The leaky run inadvertently measured
  the ceiling: absorbing off-board picks was worth the entire log-loss win. **The off-board
  escape is hereby promoted from "deferred" to the first tuning lever** — its trigger
  condition ("build it only if phase 0 shows sim over-removing") fired. It must be built
  causally: the escape rate per round measured from each fold's PRIOR drafts only, same
  discipline as the room curve.
- **K/DEF is the second lever**: the sim drafts them too eagerly once the binary gate opens
  (2025-b K predicted 0.8908 vs observed 0.9538) while the real room's median kicker is round
  14. The `OpponentKDefLastRounds` 6-vs-7 A/B is a wash (±0.0003 brier, split across folds),
  so the constant stays at the factually-grounded 7 — the flood after the gate opens is the
  problem, not the gate's timing. A graded appetite is the fix shape.
- **The room warp helps the sim**: room-priced beats raw-adp sim on both causal folds
  (2025-a 0.0775/0.3027 vs 0.0842/0.3300; 2025-b 0.0837/0.3201 vs 0.0847/0.3277), and the
  two rows tie exactly on 2024, which has no causal curve — the tie is the plumbing proof.
- The sim's mean prediction lands within 0.001 of the tilted family's: the removals budget
  matches by construction, not by correction.

Read against the pre-registered framing: the old gate ("never worse on every fold") would not
flip the default, and an honest v1-vs-v2 scoreboard today reads v1. The default flips anyway,
by decision — and these tables are the tuning agenda: off-board escape first, K/DEF appetite
second, both refereed here before they ship.

### The off-board escape shipped, and it flips both causal folds (same day)

`State.OffBoard[rem]` is the measured probability that a pick with `rem` full rounds left
after its own takes a player the ranked pool cannot see; at that probability a rollout pick
removes nobody. Measured per fold from its PRIOR drafts only (`escapeRates`, same causal
discipline as the room curve), judged against each prior draft's own era board, indexed by
rounds-remaining so 15- and 16-round drafts agree about the endgame. The measured rates are
large where they should be — ~50–67% of final-two-round picks leave the ranked board — and the
engine treats nil as no escape, so the mechanism is byte-inert until rates exist.

```
fold     shipped (brier/log-loss)   v2 sim + escape   verdict
2024     0.0670 / 0.2250            0.0699 / 0.2511   worse on both — no priors, no escape, no curve
2025-a   0.0711 / 0.3075            0.0701 / 0.2495   BETTER on both (log-loss -0.0580)
2025-b   0.0774 / 0.3006            0.0766 / 0.2775   BETTER on both (log-loss -0.0231)
```

**On every fold that can test v2 causally, v2 now beats shipped v1 on both metrics.** The 2024
loss is the fold with no past — no escape rates, no room curve — a situation the live 2026
draft is never in (three cached priors). Per position: WR and K better on both metrics on both
causal folds; RB and TE still bleed log-loss (the need model drafts TEs too eagerly and RBs
not eagerly enough) — that is the remaining v2.1 lever, alongside the K/DEF graded appetite.

Phase C must wire the same rates into the live path: compute `OffBoard` at fetch/live setup
from the cached room drafts against the live board (the adp package owns it, next to the room
curve), or the shipped sim runs escape-less — the one configuration the backtest says loses.

### Phase C: wired into the board (2026-08-11)

- **`-survival` on mock, board and live** (one shared declaration, like `-room`): `sim` is the
  default, `adp` the v1 fallback with its tilt intact. `applySim` (cmd/pick6/escape.go) is the
  only place the sim turns on, and it says which brain is running and which escape it loaded.
- **`fetch` measures the escape and writes `escape.json`** — per-draft counts (not rates, so
  any subset can be summed), each cached draft judged against its own season's era board,
  which needs the network and is why fetch owns it. Draft-time commands load it disk-only;
  `live -replay` holds the replayed draft and everything after it out of the rates, the same
  two rules the room curve applies, in the same place.
- **The measured live rates**: 56% / 72% / 36% / 25% / 22% / 14% / 14% / 3% for the last eight
  rounds-remaining, pooled over all three drafts. One honest caveat: they were measured
  against 156–178-name era boards and the 2026 board is ~201 names deep, so the true 2026
  off-board rate is somewhat lower — the sim will escape slightly too often and read the late
  board slightly safe. Nothing can measure that today; note it and let next year's fold say.
- **The deny chip shipped** (`engine.Deny` + the reverse-video-dim clause on the on-clock
  rows). It rides before the tier note in the clause order: clauses drop from the right, and
  a once-a-draft chip must not be the first thing a narrow pane sheds — which is exactly what
  happened on the first rendered frame, caught by eyeballing it.
- The sim recomputes lazily per pick behind `State.simFor` (~8ms on the full board), seeded
  from the draft id (live) or `-seed` (mock), so frames are deterministic and re-renders never
  jiggle. Mutators nil the cache.
- `~/bin/pick6` rebuilt. CLAUDE.md's constants block, milestone log, and the stale FFC-2025
  claims updated the same day.

What remains is v2.1 material, all named above: the RB/TE need-model bleed, the K/DEF graded
appetite, per-manager scout priors and the autodraft floor — every one of them refereed by
`calibrate`'s v2 gate before it ships.

## Wait signal

`SafeToWait` does not change — same threshold (`SurviveThreshold = 0.5`), same guards (tiered
position, no cliff, picks actually intervene), consuming sim `p~` like every other number.
**`WaitConfidence = 0.85` is retired.** The original spec predates SafeToWait and would have
introduced a second threshold for the same claim; one function decides the state so the tabs
cannot contradict each other, and that principle wins. v2's effect arrives through the number
itself: "everyone before you has a QB" turns dak's 61% into 94%, and the same tag reads the
better number.

## Deny indicator

Only evaluated when it's my pick. Look at the team picking immediately after me, `t+`. Compute
their need with the opponent machinery. If their max-need position `P` has exactly 1 player
left in its best remaining band (`bestTier`, not bestNow's tier — same inversion rule as
cliffs), and my own `need(P) <= NeedFlex`, tag that player with a `deny {team}` chip.

Strategically marginal, socially essential. The chip renders reverse-video on `dim` — already
reserved in the palette; it is a chip, not a category, and all six hues are spoken for. Never
auto-recommend the deny over the value pick: it must not move `PickChoices`, the verdict, or
the plan. A chip, not a verdict.

**Edge case**: at the turn, "the team picking immediately after me" is *me again*. Detect this
and skip the deny evaluation entirely rather than computing a deny against yourself.

## What v2.0 deliberately does not model

Each of these is real, measurable from data already in the cache, and cut from v2.0 on purpose.
The referee exists; any of them can be promoted later by clearing it.

- **Off-board picks.** Real drafts spend picks on players no ADP source ranked (`EnsurePlayer`
  exists because of this; 2024 ran 180 picks against a 178-name board). The sim makes every
  opponent pick remove a board player, so it slightly over-removes ranked players — the tilt's
  thin-board bias in miniature. The fix is one line (with probability r(round), measured from
  the cached drafts, a pick removes nobody) and one new constant. Build it only if phase 0's
  late-round reliability bins show sim over-removing; measure r first either way.
- **The autodraft floor.** `scout` already measures, per manager, the share of picks Sleeper
  attributed to nobody. An autodrafting seat picks pure ADP — in sim terms, `Beta = 0` for
  that pick regardless of round. Wiring it needs scout's output on the live path and a
  causality rule (profile a manager only from drafts that predate the one being scored — same
  leakage the room curve had). v2.1.
- **Per-manager tendencies.** Scout's first-QB/TE rounds and positional leans, shrunk toward
  the league rate, as per-team weight multipliers. The most v2-flavoured idea in the backlog
  and the most leakage-prone; same causality rule, same referee, after v2.0 settles.
- **Unifying the mock's autopicker.** The mock's synthetic picker is already `A(j)` without
  the need term. Folding it into the sim's sampler would be tidy and is purely cosmetic; do it
  only if it falls out for free.

## Honesty note

Leaguemates reach, autodraft, and make homer picks. The model outputs probabilities, not
predictions — 94% means 94%, and the UI always shows the percentage rather than flattening it
to "safe"/"gone". When the sim and the ADP logistic disagree wildly on a player, that's
information (a roster-driven trap or bargain), not a bug. The data tab is the natural home if
we ever want to surface the disagreement as a column; don't build that until someone misses it.

## Constants

New in `internal/engine/tuning.go` (Tau, CandidatePool and Rollouts were declared in CLAUDE.md's
constants block from the start but never landed in code, since nothing read them):

```
Tau           = 5.0   // opponent ADP desirability decay, in ranks
CandidatePool = 25    // opponent sampling pool size
Rollouts      = 500   // simulations per recompute
// Beta(r) = clamp((r-3)/4, 0, 1.5)   — a function, not a constant
```

Already present: `OpponentKDefLastRounds`, moved 6 → 7 (see the opponent K/DEF rule — the 6 was
a 15-round reading of a 16-round room). Removed: `WaitConfidence` (never landed in code; delete
it from CLAUDE.md's constants block when v2 ships).

## DoD

- Table-driven tests with seeded RNGs, in `internal/engine`:
  - determinism: same state + seed → bit-identical survival table.
  - the flagship scenario: three teams ahead of me with full RB rooms produce materially
    higher RB survival than the ADP logistic on the same board.
  - zero-weight guard: an all-K/DEF candidate pool samples uniformly instead of dividing by 0.
  - the turn: back-to-back picks with no opponents between them → every survival exactly 1.
  - my last pick: no FollowingPick, horizon stays NextPick, everything holds.
- `calibrate` scores sim at every vantage on all three folds and prints the fold table
  against the adp baseline. The numbers land in this file, good or ugly.
- Mock mode demonstrates a `safe to wait` tag whose number visibly moved for a roster reason,
  and a `deny` chip. `mock -snapshot` can print both headlessly for the readme.
