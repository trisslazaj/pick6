# dynasty mode — scoped, deferred, not dead

scoped 2026-08-12, moved out of CLAUDE.md 2026-08-18. everything below was probed live on
2026-08-12; do not re-probe.

the engine side is genuinely cheap — same sport, same Sleeper API, same name matching — and
**superflex is already built and tested end to end** (`SuperFlexEligible`,
`slots_super_flex` in `internal/sleeper/draft.go`, the VOR two-index rule flipping QB to
measured demand, `vor_test.go:176`). What stopped it was data, not engineering. Everything
below was probed live on 2026-08-12; don't re-probe.

- *The user's startup already exists and parses today*: league `1367965323520671744`
  ("The Queen's Dads Dynasty League"), draft `1367965324149788672` — snake,
  `reversal_round: 0`, **28 rounds**, pre_draft, no start time, user at **slot 12**.
  Roster `QB RB RB WR WR WR TE FLEX FLEX SUPER_FLEX K` + 14 BN — **superflex, no DEF**,
  `rec: 1.0` (full ppr) and **`bonus_rec_te: 1.0` (full-point TE premium)**.
- *Value is solved and better than redraft's.* `isDynasty=true&numQbs=2&ppr=1` returns 475
  rows: **399 real players** (QB 66 / RB 109 / WR 155 / TE 69) plus **76 rookie-pick assets**
  ("2026 Pick 1.11" 2179, "2029 4th" 761) that must be filtered off a startup board and ARE
  the board for a rookie draft. 100% sleeperId and mflId, ages on 398/399, convex 10985 → 1.
  **Read `value`, not `redraftValue`** — both ship on both endpoints and the gap is the point
  (CMC dynasty 4691 vs redraft 8203). `numQbs=2` reorders for superflex natively (Allen rank 1).
  Board depth is 399 against 336 picks (1.19), *deeper* relative to the draft than redraft's
  201/192, so the tilt's thin-board fragility is no worse.
- **DEAD — there is no dynasty startup ADP anywhere.** FFC `dynasty`/`rookie` still return
  `players: []` (meta now admits 57 and 37 drafts behind them — they collect it, they don't
  publish it). FantasyCalc `maybeAdp` is null on the dynasty endpoint too and there is no
  `/adp` route. MFL `IS_KEEPER=K` is **36 drafts / 225 players where the median player appears
  in 8 of 36 and only 38/225 in half** — keeper leagues, where the good players are kept and
  never reach the draft, so it's a bias and not just noise (top-30 min→max spread median 27
  picks). Unusable. The resolution if this is ever revived: pseudo-ADP from dynasty value rank,
  upgraded by `internal/adp/room.go`, which already maps rank→observed pick — in redraft that's
  a *correction* to national ADP; in dynasty there is no national ADP, so the same code becomes
  the *source*. Sleeper dynasty mocks feed it.
- **DEAD — no `calibrate` fold is possible.** FantasyCalc serves `/values/current` only; every
  history route tried 404s. No era-correct past dynasty board exists, so there is nothing to
  score a past dynasty draft against. Dynasty would ship **ungraded**, which is the real
  departure from how everything else here got decided.
- **DEAD — FantasyCalc has no TE premium knob.** `teMultiplier`, `tePremium`, `numTes` all
  return byte-identical payloads. Their curve is standard-TE (Bowers overall 8, McBride 17),
  so it **systematically underprices TE in this league**, and the only fix is a rankings file
  (superflex + TE premium + a `tier` column). That file is the unblocker for both tiers and
  the TE bias at once.
- *`IS_KEEPER=R` is the opposite story and is genuinely good* — **139 drafts, 119 players,
  median player in 54 of them**, joining 71/119 to FantasyCalc's `mflId`. Real rookie-draft
  ADP. So part 2 (rookie) has the better data than part 1 (startup), which is backwards from
  what you'd guess. The user also has a completed 3-round rookie draft as a fixture,
  `1312470569605681152`.
- **The best reference draft in the account: `1251354259379732480`** — a *completed* dynasty
  superflex startup, 10 teams × 25 rounds, **250 picks**, July 2025, sharper room than the
  redraft leagues. Reached by walking `previous_league_id` back from `1312470569601499136`
  to `1251354258436001792` (`previous_league_id: 0`, i.e. the origin league). Half-ppr, **no
  TE premium and no K slot**, so it is a behavioural reference and NOT a price.
- **`Beta(r)` is wrong for superflex, and this is the concrete finding worth keeping.** The
  ramp `clamp((r-3)/4, 0, 1.5)` was validated on redraft evidence where round 1 is 0% QB and
  round 2 is 8%. That startup went **50% QB in round 1** (Allen, Lamar, Hurts, Daniels,
  Burrow at 1.01–1.08). In superflex, roster shape matters from pick one. QB also arrives in
  waves — rounds 1, 3, 6, 12 and 22 all 40–50% — which is what `RunSurprise` would need
  recalibrating against. Zero K and zero DEF across all 25 rounds.
