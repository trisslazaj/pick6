# backlog — model improvements beyond v1, before/alongside v2's monte carlo

> implementation spec for the accepted slice: **docs/milestone-4x.md** — hand that file to
> the implementing agent; this one is the triage that produced it.

Spitballed and triaged 2026-07-29. Constraints respected throughout: no projections of our
own, no server/db, free sources only. Items marked ✦ were independently proposed by multiple
lenses — treat that as a signal.

## tier 1 — the v1.5 urgency bundle (pure engine math, no new data)

1. ✦ **E[best survivor] urgency.** Replace the binary 0.5 bestLater threshold with the
   expectation over order statistics: iterate Available(pos) value-desc, keep a running
   product; `E = Σ v_j · p_j · Π_{i<j}(1−p_i)`, truncate when the product < 1e-6.
   Urgency = (v(bestNow) − E) × need. Fixes the discontinuity (p=0.51 counts fully, p=0.49
   not at all) and correctly prices "three WRs at 60% each" vs "one at 65%". Also finally
   makes near-pick urgency continuous: a 40% risk contributes 0.4×gap instead of rounding
   to zero. Keep BestLater for the displayed name; price by the expectation.
2. ✦ **Exactly-N mass conservation.** Exactly PicksUntilMine players get taken, but the
   independence product doesn't know that. One-scalar proportional-hazards tilt: find c
   solving `Σ_j (1 − p_j^c) = N` (bisection, monotone, microseconds), use `p_j^c`
   downstream. Players at p=1 stay at 1; ordering preserved.
3. **Tier-hold probability for cliffs.** `p_hold = 1 − Π_{j∈tier}(1−p_j)`; amber < 0.5,
   red < 0.15; header copy "3 left in tier 2 · holds 34%". Fixes "2 left" firing amber when
   both survive trivially, and a 5-man tier being eaten mid-run showing nothing.

## tier 2 — the decision upgrade

4. **Two-pick closed-form lookahead.** The actual question at the clock: "wr now and rb on
   the way back, or the reverse?" For each ordered position pair (P,Q), score
   `v(bestNow(P))·need(P) + E[best Q at FollowingPick()]·need(Q | after taking P)` — need
   recomputed on a roster copy, ≤36 pairs, closed form, no simulation. Render the second leg
   as a subtitle: "then: qb at 3.03". This is the parked multi-pick idea's cheap 80%.

## tier 3 — league-specific (mines the three cached real drafts; feeds v2)

5. **Room-warped ADP.** Per position, the room's own rank→pick curve `adp_room(P, k)` =
   mean pick at which the k-th P went, monotonized; blend `adp' = w·adp_room + (1−w)·adp`
   with shrinkage w ≈ n/(n+2). Needs only pick order + position — NOT historical national
   ADP — so all 552 picks are usable. Encodes "this room takes QB4 by round 5" before v2.
6. **`pick6 scout` — per-manager profiles.** First-QB round, positional shares, autodraft
   floor per seat, shrunk toward league rates (n≤3). Powers a UI hint now ("2 of the 4
   seats before your pick still need a qb") and v2's opponent model later.
7. **VOR baselines + endgame guard.** Replacement level R(P) = value of the D_P-th best P,
   D_P measured from league history; vor = max(0, v − R). Use vor for cross-position level
   comparisons and the run banner's "best value" pick. Give K/DEF the sanctioned fallback
   value `ValueBase·exp(−rank/ValueDecay)` so the endgame can actually recommend them, and
   add the feasibility guard: when picks remaining == starters unfilled, everything else
   goes to need 0.

## tier 4 — data hygiene and trust (small, high real-world value)

8. **Injury/news guard.** The Sleeper dump already carries injury_status/status/
   news_updated and we drop them. Red chip for Out/IR/PUP/Doubtful, dim "news 3h" chip when
   news_updated < 48h. Never touches value or survival — a truth layer. Prevents the single
   worst failure: recommending the guy who tore his ACL last night.
9. **Sigma shrinkage + support floor.** Re-add times_drafted/high/low from FFC (parsed,
   currently dropped). Empirical-bayes variance shrinkage toward an adp-linear prior with
   pseudo-count ~25; `nextPick < high` gives a rule-of-three support floor; `pickNo > low+2`
   flips the faller chip to "past worst observed pick — check news".
10. **Staleness guard.** Write fetch metadata (fetched_at, ffc window, tiers mtime) into
    players.json; footer shows "adp Nh old"; sticky amber in live mode when >24h. Warn,
    never block.
11. **Tiers refresh ritual.** Re-transcribe the Dynatyze graphic within a week of draft day
    (the one manual chore that matters), plus a fetch-time disagreement report: flag players
    whose tier rank and adp rank differ by ≥8.

## testing — measurement before more features

12. ✦ **`pick6 calibrate` — backtest survival.** FFC serves historical years (verified:
    2023 + 2024 era-correct with per-player stdev; **2025 missing from their archive**).
    Replay the cached 2024 draft from all 12 seats: ~15 horizons × ~100 available players
    per seat → thousands of labeled p_survive predictions. Brier score, log-loss, 10-bin
    reliability table; baselines: constant rate and SigmaDefault-only. Turns every tuning.go
    constant from vibes into measurement, and gates which of the ideas above actually stay.

## in-season — managing the team (scoped 2026-09-04, unbuilt)

Drafted with pick6 in every 2026 league and the verdict was *"fine more or less — didn't agree
with it most of the time, good enough, helped overall."* v1.0.0 is that board. The draft is
three hours a year; the season is seventeen weeks of waivers, lineups and trades, and the tool
goes quiet the moment the last pick lands. This is the note for what would fill those weeks,
written so the next pass starts from measured shapes rather than guesses. Nothing here is
built, and it would live under the same rules — no projections of our own, one binary, no
server, lowercase.

**The data is there, all of it unauthenticated (probed 2026-09-04).** Sleeper, per league:
`/v1/league/<id>/rosters` (every team's `players`/`starters` — 16 and 10 in the user's rooms —
plus `waiver_position`, `waiver_budget_used`, `total_moves`), `/matchups/<week>` (`starters`,
`points`, `players_points`), `/transactions/<round>` (12 on file in round 1 of 2026 already:
`type` free_agent/waiver/trade, `adds`/`drops` keyed player → roster, `settings.waiver_bid` for
faab), `/users`, `/v1/state/nfl` (the current week) and `/v1/players/nfl/trending/add` — the
market's own waiver signal, 643k adds on the top man in 24h. `settings.waiver_type` is 0 in two
of the user's rooms and 2 in the third; read it, don't assume. The 2026 redraft rooms are
`1389721431385845760`, `1388044281582743552`, `1388043417581285376`, each carrying a
`previous_league_id` back to its 2025 room — seasons chain now, which the 2025 rooms never did.
Two dynasty leagues besides, one full-ppr.

Fpl draft, same host as the pick feed, nonce on every request as before:
`league/{id}/element-status` (who owns whom, 44 kB), `draft/league/{id}/transactions` (the
waiver and free-agent log: `element_in`/`element_out`/`entry`/`event`/`kind` f|w/`result` —
note the path: `draft/{id}/transactions` and `league/{id}/transactions` both 404),
`entry/{entry_id}/event/{gw}` (ANY manager's lineup, captain flags and all — public),
`entry/{entry_id}/public`, `event/{gw}/live` (every player's points by stat, 411 kB), `game`
(`current_event`, `waivers_processed`), `pl/event-status`. Only `entry/{id}/my-team` wants a
token (403). One surprise, recorded not explained: `league/4250/details` answered
`draft_status: "pre"` two weeks after the draft, with fifteen rounds of transactions on file.

**What would earn its place, in the order it would help:**

1. **`waivers`** — the free-agent pool ranked the way the board ranks the draft: value over
   replacement × need, where need reads MY REAL ROSTER off the rosters endpoint rather than the
   draft feed. The need/vor machinery exists; what's missing is a roster source that isn't a
   draft and a value source that isn't draft morning. FantasyCalc's `values/current` reprices
   weekly, and trending adds are the market's forecast of the window — the run detector's idea,
   one week wide. Fpl's `draft_rank` does not reprice in-season (known); `event/{gw}/live`
   totals are a published fact and the honest substitute, not a projection.
2. **A lineup CHECKER, not an optimizer** — an empty starting slot, a starter on bye, a starter
   whose `injury_status` is out, a starter fpl marks `i`/`s` or `chance_of_playing_next_round`
   0. All published facts, and `Sidelined` already reads the fpl half. Start/sit by projection
   is a projection in a trench coat and stays a non-goal.
3. **Bye-week planner** — starters sharing a bye per week. `ByeConflictThreshold` is already the
   sidebar's count; this is the same count with a week axis.
4. **Trade pricing** — both sides on FantasyCalc value, which the board already imports. A
   number, never a verdict.
5. **Fpl waiver order** — `waiver_pick` per entry is in league details, `transaction_mode` says
   waivers vs free-for-all, and the transactions log says what the room reaches for.

**And a referee exists for the first time.** A waiver claim has a label the draft never had —
what the man actually scored after it, public in `players_points` and `event/{gw}/live` by the
following week, and nflverse season stats close the nfl side (see the parked over/under model
below). So `waivers` could be graded the way `calibrate` graded survival, and should be, before
its ranking is trusted.

Not this: a start/sit model, a roster grade, anything that needs the user logged in.

## explicitly still parked

- **ADP over/under-performance model** — labels now sourceable (nflverse public season
  stats + dynastyprocess id crosswalk + FFC history), but it stays parked per CLAUDE.md:
  post-milestone-6, post-draft.
- **FormatSpread as sigma inflation, bye-stack penalty** — real but low-impact; only after
  the calibration harness exists to prove they help.
