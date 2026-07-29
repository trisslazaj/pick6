# backlog — model improvements beyond v1, before/alongside v2's monte carlo

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

## explicitly still parked

- **ADP over/under-performance model** — labels now sourceable (nflverse public season
  stats + dynastyprocess id crosswalk + FFC history), but it stays parked per CLAUDE.md:
  post-milestone-6, post-draft.
- **FormatSpread as sigma inflation, bye-stack penalty** — real but low-impact; only after
  the calibration harness exists to prove they help.
