# milestone 4x — engine v1.5: calibrated, continuous, league-aware

Status: **spec, ready to implement.** Extends milestone 4 (done) before milestone 6 (the
Monte Carlo opponent model in `docs/engine-v2.md`, which is NOT this — do not build it here).

Audience: an implementing agent with no prior conversation context. **Read CLAUDE.md first**;
it is the base contract and nothing here overrides it. Where this file names a formula it is
the contract, like CLAUDE.md's engine section was for milestone 4. `docs/backlog.md` is the
triage that produced this spec; you don't need it.

## ground rules (inherited — do not relitigate)

- Everything rendered is lowercase; team codes uppercase. Every tuneable constant goes in
  `internal/engine/tuning.go` (or the deliberate duplicates in `internal/adp` for fetch-time
  work — that duplication is intentional, keep it).
- Table-driven tests in `internal/engine`, matching the existing idioms: anonymous
  `cases := []struct{...}` tables, no `t.Run`, no testify, lowercase messages, a comment
  above each non-obvious test naming the bug it prevents, hand-computed expected values
  shown to enough decimals to be checkable. UI tests exist in `internal/ui` and follow the
  same spirit (render `Board.View()`, strip ANSI with the shared `ansi` regexp, assert on
  text).
- No new dependencies. No projections of our own — values come from FantasyCalc/rankings
  only; transformations of imported values are fine, inventing player values is not.
- The engine recomputes everything from scratch on every pick event. Don't cache, don't
  optimize. A few thousand extra `math.Exp` calls per frame are noise.
- Existing engine surface you build on (`internal/engine`): `PSurvive` (conditional
  logistic `S(next)/S(now)`, computed in log space via `softplus` — read `urgency.go`
  before touching anything), `Falling`, `BestLater` (0.5 threshold — phase 1 replaces its
  role in urgency but keeps it for display), `Urgency`, `NextPick`/`FollowingPick`/
  `PicksUntilMine`, `Need`, `Available` (sorted value desc, adp asc, id asc), `Cliff`,
  `DetectRun`, `TierRemaining`/`TierSize`, `MyUpcomingPicks`, `UnfilledStarters`,
  `FilledSlots`.
- Commit style: lowercase, area-prefixed, small. Update the milestone status note in
  CLAUDE.md only when a phase is genuinely done.

## phase ordering

Phase 0 first — it referees everything after it. Phases 1→2 are sequential (2 needs 1's
functions). Phases 3 and 4 are independent of 1–2 and of each other; interleave freely.

---

## phase 0 — `pick6 calibrate`: backtest survival against a real draft

Every constant in tuning.go is currently vibes. This subcommand replays the user's real
cached 2024 draft and scores every survival probability the engine would have produced
against what actually happened. Later phases are kept or dropped based on these numbers.

### data (all verified live on 2026-07-29 — trust this, don't re-derive)

- **Historical ADP: FFC serves past years.**
  `GET https://fantasyfootballcalculator.com/api/v1/adp/half-ppr?teams=12&year=2024`
  returns era-correct 2024 data (CMC adp 1.3, Bijan 2.0; 178 players, 906 drafts), and
  `year=2023` works too (Ekeler/Kelce round 1). Rows carry the FULL current schema:
  `adp, stdev, times_drafted, high, low, bye` — so per-player sigma is backtestable, not
  just SigmaDefault. **`year=2025` is MISSING from their archive** — returns
  `{"status":"Error","errors":"No ADP data found."}`. There is no `meta.year` field;
  instead `meta.start_date/end_date` confirm the window (year=2024 → 2024-08-31..09-01).
  What you get is the final trailing snapshot of that season's draft week. Cache the
  response through the existing `internal/cache` machinery like every other FFC call.
- **The user's real drafts, already reachable** (endpoints in CLAUDE.md, unauthenticated):
  draft `1133489617308684288` is the 2024 draft — **the only backtestable one**. The two
  2025 drafts (`1261824503076360192`, `1253161474382102529`) have no era ADP; print a
  note that they're skipped and do NOT fall back to 2026 ADP for them — an era mismatch
  silently invalidates every number.
- **Join** 2024 FFC names → Sleeper player ids with the matching pipeline in
  `internal/rankings` — but **exact-only**: `Index.Lookup` falls through to Levenshtein
  fuzzy matching automatically, and a 2024 name that's missing from the *current* active
  pool (retired/cut since) would silently map to a similarly-named active player whose id
  never appears in the 2024 picks, labeling him "survived forever" and quietly biasing the
  gate numbers. Add an exact-only lookup (or bypass the fuzzy stage) for calibrate; a 2024
  name with no exact match is printed and dropped, never fuzzed.

### procedure

For every seat `s` in 1..12 (using all seats multiplies the data 12×, and snake math is
seat-agnostic): walk s's picks via the engine's own `MyPick` with `mySlot = s`. At each
vantage pick `p` belonging to s with a following pick `p'`:

- Prediction set: every player still available at `p` (actual draft position `d_j >= p` or
  undrafted) with a known 2024 adp.
- Predicted: `q_j` = the engine's conditional survival from `p` to `p'` — literally
  `PSurvive` with `PickNo = p, NextPick = p'` and 2024 adp/sigma. Use the phase-1
  `PSurviveAt` if it exists; otherwise add it here (it's the only engine change phase 0
  is allowed).
- Label: `y_j = 1` if `d_j >= p'` or undrafted (he really did survive to p').

That's ~12 seats × ~15 vantages × ~100 available players ≈ tens of thousands of labeled
predictions from one draft.

### metrics (print all, lowercase, aligned)

- brier = mean over all (q−y)²  — lower is better; 0.25 is the coin-flip ceiling.
- log-loss = −mean[y·ln q + (1−y)·ln(1−q)], with q clamped to [1e-6, 1−1e-6].
- reliability table, 10 bins by predicted q: per bin show mean predicted, observed survival
  rate, and count. A calibrated model's two columns match.
- segments: by horizon length (≤6, 7–12, 13+ picks), by position, by adp depth
  (rounds 1–3 / 4–8 / 9+). The tails are where the tool earns its keep — report them.

### baselines (the model must beat all three or the fancy math is theater)

a. constant predictor: the global observed survival rate.
b. per-player sigma replaced by SigmaDefault for everyone (tests whether per-player sigma
   actually helps).
c. the unconditional logistic `S(next)` alone (tests whether conditioning helps).

### `-tune` flag

Grid-search SigmaDefault × SigmaMin × SigmaMax (and, once later phases exist, the shrink
and tilt constants) minimizing brier; print the winning combo and its metrics. **Print,
never auto-write constants** — the human moves numbers into tuning.go by hand.

Implementation note: those are compile-time `const`s bound inside `adp.Sigma` and
`PSurvive`'s fallback — you cannot sweep them through the existing functions. calibrate
carries its own parameterized `sigma(stdev, def, min, max)` and pre-fills each
`Player.Sigma` per grid point, so the engine's built-in fallback never fires during
tuning.

### gates for later phases

A phase-1/3/4 change to survival math is kept only if brier and log-loss do not get worse
on this backtest, with special attention to the reliability tails. Caveats to note in the
output: one draft is a small sample; the FFC snapshot postdates a mid-August draft by a
couple of weeks; 2025 may appear in FFC's archive later — recheck once, cheaply, near
draft day.

### deliverable

`cmd/pick6/calibrate.go`. Offline after the first ffc fetch. DoD: runs end to end, prints
metrics + baselines + reliability table for the 2024 draft; the skipped-2025 note appears.

---

## phase 1 — the v1.5 urgency bundle (engine math only)

### 1a. `PSurviveAt` — survival to an arbitrary horizon

Generalize the existing `PSurvive`: extract its body into
`func (s *State) PSurviveAt(p Player, at int) float64` where `at` replaces `NextPick()`,
with the same sentinel/sigma fallbacks and the same log-space softplus form. Guard
`at < PickNo → at = PickNo` (same reason as the finished-draft guard that's already
there). `PSurvive(p) = PSurviveAt(p, s.NextPick())`. All existing tests must pass
unchanged.

### 1b. exactly-N tilt — make expected removals equal actual picks

Exactly `N = PicksUntilMine()` players get taken before my turn, but under independence
the model's expected removals `Σ_j (1 − p_j)` can be 11 when N is 7. Correct it with one
scalar: find `c > 0` such that

    Σ_j (1 − p_j^c) = N        (sum over ALL available players, every position —
                                the N picks span positions, so normalize globally)

`f(c) = Σ (1 − p_j^c)` is continuous and strictly increasing in c, so bisection on
`c ∈ [1/TiltCMax, TiltCMax]` (TiltCMax = 64) converges in ~60 iterations. Then use
`p̃_j = p_j^c` everywhere downstream (EBest, tier-hold, the wait tag, the data tab's surv
column — one truth, no untilted numbers on screen).

Why a power: `ln p` is the player's cumulative hazard over the intervening picks, so
`p^c` scales every hazard by the same factor — the "this room drafts hungrier/slower than
the model thinks" knob. Fixed points p=1 (my own pick, undrafted-radar players) stay 1;
p=0 stays 0; ordering is preserved.

Edge cases: `N = 0` (my pick) → skip, c = 1. Clamp on BOTH ends: if `f(TiltCMax) < N`
(thin board, everyone near-certain to survive) clamp `c = TiltCMax`; if `f(1/TiltCMax) > N`
(many near-certainly-gone fallers, small N) clamp `c = 1/TiltCMax`. Implement as
`func (s *State) survivalTilt(at, n int) float64` — the CALLER supplies N, because the
correct count differs by horizon: for `at = NextPick()` it's `PicksUntilMine()`, but for
phase 2's second leg it's `q2 − PickNo − 1` (my own pick at NextPick is not an opponent
removal). Recompute per call per the no-caching philosophy.

New constants: `TiltCMax = 64.0`, `TiltTol = 1e-6`.

Tests: a constructed board where Σ(1−p) ≠ N → assert Σ(1−p̃) ≈ N to 1e-4; p=1 players
untouched; ordering preserved; N=0 skips; thin-board clamp reaches TiltCMax without
panicking.

### 1c. `EBest` — expected value of the best survivor (replaces the 0.5 threshold in urgency)

For a position, walk `Available(pos)` (already value-desc) with tilted survivals
`p̃_1, p̃_2, ...`:

    E[best available at pick `at`] = Σ_j  v_j · p̃_j · Π_{i<j} (1 − p̃_i)

Read each term as "his value × the probability he is the best one left" — he survives
AND everyone better is gone. Implement with a running product `acc`:
`ev += acc·p̃_j·v_j; acc *= (1−p̃_j)`, break when `acc < EBestEpsilon` (1e-6). If the pool
runs out with acc > 0, that residual is the "position completely emptied" event — worth 0,
add nothing. Signature: `func (s *State) EBest(pos string, at int) float64`.

Urgency becomes:

    Urgency(pos) = (v(bestNow) − EBest(pos, NextPick())) × Need(pos)

Properties to preserve and test:
- My own pick: all p̃ = 1 → EBest = v(bestNow) exactly → urgency exactly 0 (the milestone-4
  value tie-break in the UI keeps doing the pointing).
- Urgency is continuous in the p's — no jump as a player crosses 0.5.
- Worked anchor (hand-checkable): players v = 100, 90, 60 with p̃ = 0.2, 0.6, 0.99 →
  E = 100(.2) + 90(.8)(.6) + 60(.8)(.4)(.99) = 20 + 43.2 + 19.008 = 82.208; with an open
  starter slot urgency = 17.792.
- `BestLater` survives for DISPLAY only (the urgency strip's "love→hall" pair and any
  named fallback): redefine the displayed name as the modal best survivor — the j
  maximizing `p̃_j · Π_{i<j}(1−p̃_i)`. The 0.5-threshold version of BestLater can be
  deleted once nothing consumes it.
- The "safe to wait" tag decouples from urgency == 0 (which is now rarely exact): tag when
  `p̃(bestNow) ≥ SurviveThreshold` and the existing guards hold (tier ≠ 0, cliff none,
  not my pick). Same meaning as before — "your guy himself will keep." **Apply the same
  rule to the data tab's urgency strip** (`urgencyStrip` in `internal/ui/data.go` currently
  branches on `u > 0` vs "safe" — with continuous urgency that branch must switch to the
  p̃-based rule, or the two tabs contradict each other, which that file's own comment
  forbids).

### 1d. tier-hold probability (cliff states by probability, not count)

    p_hold(pos) = 1 − Π_{j available in tier(bestNow(pos))} (1 − p̃_j)

— the probability at least one member of the current tier survives to my next pick (the
complement of "all taken"). New method `func (s *State) TierHold(pos string) (float64, bool)`
(ok=false when tier 0). `Cliff` levels switch to it: red when `p_hold < TierHoldCliff`
(0.15), amber when `< TierHoldWarn` (0.5). KEEP the existing untouched-tier guard — a tier
nobody has drafted from is never amber/red, that design decision stands (detect.go's
comment explains why). Keep the remaining COUNT in the copy and add the probability:
group header reads `3 left in tier 2 · holds 34%`. Copy caveat: today's red wording
("last one in tier n" / "take him or lose the tier") is a COUNT claim, and under p_hold
red can fire with 3 players left — keep the last-one wording only when remaining == 1,
otherwise red reads `tier n unlikely to hold — holds 9%`. K/DEF (tier 0) skipped as
today.

New constants: `TierHoldWarn = 0.5`, `TierHoldCliff = 0.15`, `EBestEpsilon = 1e-6`.

DoD for phase 1: all engine tests green including new tables; `pick6 calibrate` rerun
shows the tilt is neutral-or-better on brier (the tilt changes p̃, EBest doesn't); mock
snapshots show continuous urgency ordering and the new tier copy.

---

## phase 2 — two-pick lookahead (needs phase 1)

The actual question at the clock: "wr now and rb on the way back, or the reverse?"

Let `q2 = FollowingPick()`; if `q2 == 0` (draft ends first), skip everything below. For
every ordered pair of positions `(P, Q)` with nonzero need (P == Q allowed — double-tapping
a position at the turn is legitimate):

    score(P, Q) = v(bestNow(P)) · Need(P)  +  EBest'(Q, q2) · NeedAfter(Q | bestNow(P))

- `NeedAfter(Q | x)`: Need(Q) recomputed as if player x were already on my roster — append
  x's id to a copy of `Rosters[MySlot]`, rerun the pure FilledSlots/Need logic, restore
  nothing (work on copies). Add a small helper; do not mutate State.
- `EBest'` for the second leg: exclude `bestNow(P)` from the pool (I took him — this
  matters when P == Q, and is a no-op otherwise), and tilt with the second-leg pick count
  `N2 = q2 − PickNo − 1` (all intervening picks minus my own next one; verified by direct
  count for slot 3/12 teams at multiple vantages).
- ≤ 6 positions → ≤ 36 pairs, closed form, microseconds.
- The plan is computed as-if standing at NextPick, but renders at every vantage — so the
  copy must not say "now" when it isn't my pick. Phrase both legs by their picks.

The recommendation is the argmax pair. UI: one dim line at the top of the left pane, under
the "best available" section head:

    plan  wr at 2.10 → rb at 3.03  (e 4120)

lowercase, picks shown as round.pick via the existing formatting helpers. On the data tab,
append the plan to the urgency strip if it fits the width budget, else omit — never wrap.

Tests: a constructed two-position board where greedy and lookahead disagree — WR tier
evaporating by q2 while RB holds → plan must say wr-then-rb even when v(bestNow(RB)) >
v(bestNow(WR)); plus the q2 == 0 skip and a P == Q case at the turn.

DoD: the plan line renders in mock; the disagreement test pins the reason this exists.

---

## phase 3 — league-specific (independent of phases 1–2)

### 3a. room-warped effective adp

The room is measurably QB-early (CLAUDE.md "our league" table). Build the room's own
rank→pick curve from the three cached drafts — this uses only pick ORDER and position, so
all 552 picks are usable (no historical national ADP involved, era caveat doesn't apply):

    adp_room(P, k) = mean over drafts of the overall pick at which the k-th player of
                     position P was taken; monotonize with a running max over k.

At load time, for player j whose position-adp-rank among board players is k:

    adp_eff(j) = w · adp_room(P, k) + (1 − w) · adp(j),   w = n_drafts / (n_drafts + RoomWarpPseudo)

with `RoomWarpPseudo = 2` (so w ≈ 0.6 at n=3), and w → 0 outside the observed k range.
Location shift only — sigma untouched. Mechanism (this matters — raw ADP is read in six
places and most must NOT warp): add `engine.Player.ADPEff`, populated in `loadBoard`
behind a `-room` flag on live/mock; **only `PSurviveAt` and `Falling` read it** (falling
back to raw ADP when 0). Display columns, `Available`/`dataRows` sort tie-breaks, and the
mock's `scriptedPicker` all keep raw ADP — warping the mock's picker would warp the fake
room itself. Fetch step: re-pull and cache the three draft ids (ids are in CLAUDE.md).

Gate: cross-validate on the 2024 backtest with the warp built from the two 2025 drafts
only. Small n — if brier worsens, ship it visualization-only (a "room takes qb4 by 5.02"
line in scout output) rather than wiring it into survival.

### 3b. `pick6 scout` — per-manager tendencies

Walk the proven endpoints (user → leagues → drafts → picks + league users for display
names) for 2024–2026, cache `scout.json`. Per manager: first-QB/TE/K/DEF round per draft,
positional share by round, autodraft floor (share of picks with empty `picked_by` — that
field being empty on autodraft is the observed shape, and why rosters attribute by
`draft_slot`). Shrink toward league rates: `P(m takes first P by round r) =
(count + a·leagueRate(r)) / (n + a)`, a = 2. Deliverable: the cached profile + a lowercase
fetch-style printout. This feeds v2's opponent model later (managers with high autodraft
floor simulate as pure adp); UI hints are optional here.

### 3c. vor baseline + k/def values + endgame guard

- `D_P` = median count of position P drafted per league draft (from the same cached
  drafts). Replacement level `R(P)` = imported value of the D_P-th best P on the full
  pre-draft board (static, computed at load). `vor(j) = max(0, v(j) − R(pos_j))`.
  Exactly one call site changes: the zero-urgency group-sort tie-break in `bestAvailable`
  (`internal/ui/board.go`) switches from value·need to vor·need. `bestOtherPosition`
  already ranks by urgency — leave it alone.
- K/DEF get a value at fetch so the endgame machinery can work (CLAUDE.md requires a value
  before they could ever be un-suppressed; suppression itself stays — `KDefLastRounds`
  unchanged). **Scale matters**: `ValueBase·exp(−rank/ValueDecay)` at K/DEF ranks yields
  ~3–6 against a FantasyCalc board where rank-150 skill players carry ~190 — a 30×
  mismatch that CLAUDE.md's "never mix modes in one draft" rule exists to prevent, and
  which would keep K urgency invisible forever. Instead anchor to the imported curve:
  value(K/DEF) = the imported value of the skill player with the nearest overall adp
  (interpolate between neighbors). Tier stays 0 so cliff logic keeps skipping them.
- Endgame feasibility: with `R` = my remaining picks, `U` = my unfilled starters:
  `R < U` is already lost (show nothing special); `R == U` → need forced to 0 for every
  position not among the unfilled starters, sticky dim line "every remaining pick must
  fill a starter"; `R == U+1` → non-starter needs × `EndgameSlack` (0.5). Constants in
  tuning.go.

---

## phase 4 — data hygiene (independent; small; do the injury guard first)

### 4a. injury/news guard

The cached Sleeper dump carries `injury_status`, `status`, `news_updated` (epoch ms) —
field names verified against the cached `sleeper_players.json` — and `internal/sleeper`
currently drops them. The plumbing path is longer than it looks, because engine players
come from `players.json`, not from the sleeper package directly: sleeper.Player gains the
fields → fetch copies them onto the matched adp.Player → players.json → `loadBoard`
(which lives in `cmd/pick6/mock.go` despite the name) → engine.Player (display-only
fields, like TierSrc). Injury state is therefore frozen at fetch time — 4c's staleness
guard is the mitigation, and the draft-morning refetch is the ritual. UI: red chip on the name for injury_status ∈ {Out, IR, PUP,
Doubtful, Sus} or status ≠ Active; dim `news 3h` chip when now − news_updated <
`NewsFreshHours` (48). Never touches value or survival — a truth layer. Fetch prints
flagged players still carrying a top-100 adp (those are the traps).

### 4b. sigma shrinkage + support floor + tripwire

Re-add `times_drafted`, `high`, `low` from FFC (parsed in `internal/adp/ffc.go`, currently
dropped before `Player`): thread through adp.Player → players.json → engine.Player.

- Shrinkage (at fetch, in `internal/adp` where `Sigma()` lives): shrink VARIANCE, not
  stdev: `stdev' = sqrt((n·stdev² + n0·prior²)/(n + n0))`, `n0 = ShrinkPseudoDrafts (25)`,
  prior = least-squares linear fit stdev ~ adp over the pool (fallback: pool median).
  Then the existing clamp. A 1,100-draft player is untouched; an 8-draft player moves
  most of the way to the prior.
- Support floor (engine): if `nextPick <= high` (surviving to nextPick means surviving
  picks strictly before it, and nobody in n drafts was ever taken strictly before `high`),
  floor survival: `p = max(p, 1 − 3/max(n,3))` — the rule of three: zero events in n
  trials bounds the rate near 3/n.
- Tripwire (UI): when `pickNo > low + 2` (he's past the latest pick any real draft took
  him), the amber falling treatment gains the copy `past worst observed pick — check news`
  in the group header area of the data tab row… keep it to a chip; do not add a row.

Gate all of 4b through `pick6 calibrate -tune` (historical rows carry times_drafted/high/
low too, so the floor and shrinkage are directly backtestable).

### 4c. staleness guard

Write `meta.json` next to players.json at fetch (`fetched_at`, ffc window end, total
drafts, tiers file mtime, sleeper dump mtime) — a separate file so players.json's array
shape and `loadBoard` stay untouched. Footer: `adp 26h old · 1,110 drafts`. In live mode,
a sticky amber line when fetched_at > `StaleADPHours` (24) old or the ffc window ended
more than 2 days ago. Warn, never block.

### 4d. tiers disagreement report

After rankings apply, per position compute each tiered player's rank-by-(tier,value) and
rank-by-adp; print the top 10 with `|Δrank| ≥ TierAdpGapFlag` (8). Fetch output only.
(The human chore this supports: re-transcribing the Dynatyze tiers within a week of draft
day. Nothing to build for that beyond this report.)

---

## new constants summary (all in tuning.go unless noted)

    TiltCMax          = 64.0
    TiltTol           = 1e-6
    EBestEpsilon      = 1e-6
    TierHoldWarn      = 0.5
    TierHoldCliff     = 0.15
    RoomWarpPseudo    = 2
    EndgameSlack      = 0.5
    NewsFreshHours    = 48      // ui-side is fine
    StaleADPHours     = 24
    TierAdpGapFlag    = 8
    ShrinkPseudoDrafts = 25     // internal/adp (fetch-side, deliberate duplicate zone)

## explicit non-goals

- Monte Carlo opponent simulation — milestone 6, `docs/engine-v2.md`, not here.
- The ADP over/under-performance model — parked (CLAUDE.md), stays parked.
- FPL, auction, 3rd-round reversal, any refactor of working code, any new dependency.

---

## appendix — the math from first principles

**The survival curve.** ADP is the market's average price; stdev is how much the market
disagrees. A logistic S-curve through those two numbers gives P(still available at pick p).
Conditioning (`S(next)/S(now)`) just applies the definition of conditional probability:
he's here now, so only the picks between now and my turn can remove him.

**E[best survivor] (1c).** Line players up best-first. The best survivor is #1 if he
survives (p₁). It's #2 only if #1 is gone AND #2 survives: (1−p₁)p₂. It's #3 only if both
better men are gone and he's there: (1−p₁)(1−p₂)p₃. Multiply each such probability by that
player's value and add — that's the expected value of whoever you'll actually be choosing
from later. Subtract from the best available now: the expected cost of waiting.

**The exactly-N tilt (1b).** Each survival p implies a removal chance 1−p, so the model
"expects" Σ(1−p) removals. But a draft isn't random — exactly N picks happen. If the sum
says 11 and N is 7, every probability is collectively too pessimistic. Raising every p to
a power c (c<1 lifts them, c>1 lowers them) and solving for the c that makes the sum equal
N is the gentlest possible correction: it preserves ordering, keeps certainties certain,
and in log space it's just scaling everyone's hazard by the same factor.

**Tier-hold (1d).** P(at least one survives) = 1 − P(all taken) = 1 − Π(1−pⱼ). The
complement trick: "at least one" is hard to sum directly, "none" is a simple product.

**Shrinkage (4b).** An average computed from 8 samples is mostly noise; one from 1,100 is
signal. Blend each player's measured spread with the typical spread for players at his adp,
weighted by sample size — small samples lean on the prior, big samples stand alone. This is
empirical Bayes in one line.

**Rule of three (4b).** If something happened 0 times in n trials, a reasonable upper bound
on its true rate is about 3/n. Nobody in 906 drafts took him before pick 40 → the chance
anyone does today is small and boundable, whatever the curve says.

**Brier score (phase 0).** You said 70%; did it happen? Score (0.7 − outcome)², average
over thousands of predictions. It rewards both being right and being honest about
uncertainty — a model that says 50% about everything scores 0.25; a clairvoyant scores 0.
The reliability table is the same idea made visible: of all the times the model said ~70%,
did ~70% actually survive?
