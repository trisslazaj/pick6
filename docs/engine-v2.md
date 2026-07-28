# engine v2 — opponent-aware survival (milestone 6)

Don't build this until milestones 1–5 ship. Nothing here changes the board, the UI, or the
urgency math — v2 replaces the *survival function only*. `bestLater`, urgency, cliffs, and
banners all consume it unchanged.

Keep v1 behind `--survival=adp` for comparison and as the fallback when roster data is
incomplete. `--survival=sim` selects this.

## Why

v1 treats the picks between mine as random draws from "drafters in general." They're not —
they are specific rosters, and Sleeper publishes every one of them in `DraftState.rosters`.
If the three teams ahead of me all have full RB rooms, the RB I want is far safer than ADP
says. That gap is the entire feature.

## Opponent pick model

For an opponent team `t` about to pick at overall pick `q`, the probability they take
available player `j`:

```
weight(j)    = A(j) * need_t(pos_j) ^ Beta(round(q))
P(t takes j) = weight(j) / sum over available weight
```

- `A(j)` — ADP desirability. Let `k(j)` = 0-indexed rank of `j` by ADP among *currently
  available* players. `A(j) = exp(-k(j) / Tau)`, `Tau = 5.0`. Drafters overwhelmingly take
  someone near the top of the remaining ADP order, which is empirically how humans draft.
  Zero out weights beyond the top `CandidatePool = 25` — nobody is taking the 60th-best
  player, and it shrinks the sample space.
- `need_t(pos)` — the same need function as mine, computed from team t's roster. This is the
  whole point: their roster is public, so their needs are computable.
- `Beta(r)` — how much need matters by round: `Beta(r) = clamp((r - 3) / 4, 0.0, 1.5)`.
  Rounds 1–3: `Beta = 0`, need is ignored, everyone drafts best-available (matches reality).
  Ramps up so by round 9+ need dominates. `need^0 = 1` for everyone, so it degrades gracefully.
- **Opponent K/DEF rule**: suppress `need_t(K/DEF)` until that team's
  `roundsRemaining <= OpponentKDefLastRounds`, **not** the stricter `KDefLastRounds` we apply to
  ourselves. These are different questions: `KDefLastRounds = 3` governs what the tool is willing
  to *recommend*, while the opponent model has to predict what the room actually *does*. Measured
  over 552 real picks from this league, the first kicker goes in round 10 and the first defense in
  round 11 (see "Our league" in CLAUDE.md), so `OpponentKDefLastRounds = 6`. Set it too strict and
  the simulation keeps kickers "available" that were drafted rounds ago, which corrupts every
  survival number; set it to zero and opponents draft kickers in round 7, which corrupts them the
  other way.

### Zero-weight guard (required)

`need_t(K/DEF)` is 0 before the last 3 rounds, and `0 ^ Beta` is 0 for any `Beta > 0`. Late in
a draft the top-`CandidatePool` available players can be *entirely* K/DEF, which makes every
weight 0 and the normalizing sum 0 → division by zero.

If `sum(weight) == 0`, fall back to sampling uniformly over the ADP pool (i.e. ignore need for
that one pick). Do not skip the pick and do not panic.

## Rollout

Let `S` = the ordered list of `(team, pick)` between `pickNo` and my `nextPick` (exclusive).
Run `Rollouts = 500` simulations, each with a seedable RNG (`rand.New(rand.NewSource(seed))` —
determinism is required for the table tests).

```
for m in 1..Rollouts:
    avail    = copy of available set
    rosters' = deep copy of rosters          // REQUIRED, see below
    for (t, q) in S:
        sample j from P(t takes ·) over avail, using need_t computed from rosters'[t]
        remove j from avail
        append j to rosters'[t]              // REQUIRED
    for each player j still in avail: survives[j]++

p_survive(j) = survives[j] / Rollouts
```

**The roster copy is not optional.** `|S|` can be up to `2T-2`, which means a team at the turn
picks *twice inside one rollout*. Without updating `rosters'` mid-rollout, `need_t` is frozen at
its pre-rollout value and that team will happily draft two RBs at identical need weight — which
is exactly the behavior v2 exists to model correctly. Copy the roster alongside `avail` and
mutate both.

**Cost**: `|S| ≤ 2T−2` picks × 500 rollouts × weight computation over ≤25 candidates —
microseconds in Go. Recompute on every pick event like everything else. Don't optimize.

## roundsRemaining

Define it once, use it for both me and opponents:

```
roundsRemaining(t) = number of picks team t has left, including the current round
                   = rounds - currentRound + 1
```

Global (not per-team) is fine — in a snake every team has the same number of picks left at any
round boundary. It only diverges *mid-round*, and the K/DEF gate is coarse enough that the
difference never matters. If you find yourself wanting per-team precision here, you're
overthinking it.

## Wait signal

If `p_survive(bestNow(pos)) >= WaitConfidence (0.85)`, tag that player/group
`safe to wait ({p}%)` in green.

This is v2's headline feature: it licenses banking value on purpose — "everyone before you has
a QB, dak survives 94%."

## Deny indicator

Only evaluated when it's my pick. Look at the team picking immediately after me, `t+`. Compute
their urgency with the full v2 machinery. If their max-urgency position `P` has exactly 1
player left in its current tier, and my own `need(P) <= NeedFlex`, tag that player with a
`deny {team}` chip.

Strategically marginal, socially essential. Never auto-recommend the deny over the value pick —
it's a chip, not the verdict.

**Edge case**: at the turn, "the team picking immediately after me" is *me again*. Detect this
and skip the deny evaluation entirely rather than computing a deny against yourself.

## Honesty note

Leaguemates reach, autodraft, and make homer picks. The model outputs probabilities, not
predictions — 94% means 94%, and the UI always shows the percentage rather than flattening it
to "safe"/"gone". When the simulation and the ADP logistic disagree wildly on a player, that's
information (a roster-driven trap or bargain), not a bug.

## DoD

Table-driven tests with a seeded RNG. A scripted scenario where the three teams ahead of me all
have full RB rooms must produce materially higher RB survival than the ADP logistic does. Mock
mode demonstrates a `safe to wait` tag and a `deny` chip.
