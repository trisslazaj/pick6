# milestone 7 — multi-pick lookahead (the plan gets real)

The engine is greedy: it optimises one pick at a time, and the two-pick plan that softens
this (engine.PickChoices / BestPlan, docs/engine-v2.md, §"Two-pick lookahead" in the
paper) carries a documented honest simplification: **the first leg is priced with today's
best available even when my next pick is seventeen picks away**, and the second leg is an
expected-best formula, not a simulated outcome. Milestone 6 built rollouts that simulate
the draft forward; milestone 7 points them at MY OWN picks. This file is a complete
hand-off spec: a fresh session should be able to build it from here plus the codebase.

**State of the repo when this was written (2026-08-11):** main at v0.2.0, milestone 6
shipped — `internal/engine/sim.go` holds the rollout machinery (opponent pick model,
`rollout(start, picks, far, seed)`, Jeffreys smoothing, per-vantage seeding), sim is the
board's default survival brain, and `docs/fpl.md` is the sibling spec (either order works;
if FPL's f1 lands first, this milestone inherits derived position lists for free).

## the question, plainly

Today: "take the WR now, expect an RB on the way back" is a formula — vor×need for leg
one plus expected-best-over-replacement for leg two. It cannot say *what actually tends
to happen* if you take the WR: which RBs are really still there at your next pick, how
often the tier you're counting on survives, what the pair is worth across the futures
where it doesn't.

Milestone 7: for each candidate first pick, run the rollouts **through my own picks** —
my next pick takes the candidate, opponents draft as v2 already models them, my
*following* pick takes the best legal player by the engine's own lights — and score the
pair that actually lands, future by future. The plan becomes "wr now → rb at 4.10: lands
a tier-2 rb in 78% of futures, mean pair value 9,840" instead of a point estimate.

## what already exists and gets reused unchanged

- The opponent model, roster copies, seeding, smoothing, the off-board escape, and the
  candidate machinery in `sim.go` — the conditioned rollout is the same loop with my
  picks un-skipped and policy-driven instead of skipped.
- `PickChoices`' candidate enumeration (positions with need and players), `mustFill`
  feasibility, `NeedAfter`, `VOR`/`Replacement` — the scoring vocabulary stays identical
  so the UI reads the same units.
- The pinned architectural invariant: **`BestPlan` is `PickChoices()[0]`** — one primary
  key, the plan line can never contradict the ordering under it
  (`TestBestPlanIsTheTopPickChoice`). Milestone 7 must upgrade the score *inside* that
  architecture, never add a second brain beside it.

## design

### the conditioned rollout

For each first-leg candidate `P` (same candidate set PickChoices already builds):

1. Extend the rollout window from `[PickNo, FollowingPick)` to `[PickNo, q2]` inclusive of
   my pick at `q2` — i.e. simulate through both of my next two picks.
2. At my first pick in the window: remove `bestNow(P)` (the candidate) — a decision, not
   a draw.
3. Opponent picks: exactly as today (desirability × need, gates, escape).
4. At my second pick `q2`: take the argmax of `VOR(p) × NeedAfter(pos_p | leg one)` over
   available players — the engine's own greedy choice, i.e. "what I would actually do
   there". Record who it was.
5. Score the rollout's pair exactly as PickChoices scores a pair today:
   `(v(leg1) − R(P))⁺·need(P) + (v(leg2) − R(pos₂))⁺·NeedAfter(pos₂ | leg1)` — but with
   the REAL leg-2 player from this future instead of an expected-best formula.

`score(P)` = mean over rollouts. `Second` (the plan's named position) = the modal leg-2
position. Two display quantities fall out free and go to the UI: the probability the
modal position's best remaining band survives to `q2` in the conditioned futures, and the
distribution of who leg 2 actually is (top name + its frequency).

### common random numbers — not optional

All candidates `P` share one seed per vantage, so every candidate is evaluated on the
SAME set of opponent futures. This is paired comparison: the difference between two
candidates' scores is then the difference their choice makes, not sampling noise. With
independent seeds the ranking would flicker between renders at exactly the score gaps
that matter (a few hundred value points ≈ well inside independent-rollout noise at
n=500). One seed, same draws, candidates differ only where their removals force the
opponents' hands. (The two-variant construction in calibrate's column gate and the
room/raw sim rows is the precedent.)

### what replaces what

- `PickChoices`: the pair score's second leg swaps `EBest(Q, q2) − R(Q)` for the
  conditioned-rollout mean. `Fills`/feasibility, candidate membership, tie-break order:
  unchanged. On my last pick (`q2 == 0`) the score degenerates to vor×need exactly as
  today (`TestPickChoicesOnMyLastPick` must keep passing).
- `BestPlan`: still `PickChoices()[0]`, now carrying the new display quantities.
- `Urgency`, tier-hold, survival columns, banners: untouched — this milestone changes the
  *decision* score, not the survival model, and the two must not be conflated.
- Off the clock, the same conditioned score prices the forecast frame (the pane already
  renders both tenses from one ranking).

### three picks ahead — build it, gate it behind a constant

The same machinery extends to `q3` (window to my third pick, my `q2` pick also
policy-driven, score the triple). Cost scales linearly in window length and candidate
count. Ship two-pick conditioned scoring as the default; put the third leg behind a
constant (`PlanDepth = 2`) with the third-leg variant implemented and tested but off,
promoted only if a real frame shows a decision it changes — the wheel (slots 1/12, 22-pick
gaps) is where to look, since "what survives two of my turns" is the question the sidebar
already says is the whole story there.

### cost budget

Per render-worthy recompute: ~6 candidates × 500 rollouts × window ≤ ~2.2×T picks. On the
m6 benchmark scale (~8ms per 500×22 batch) that is ~50ms per pick event, cached exactly
like the survival table (nil on every mutation, keyed to the vantage — `State.sim`'s
pattern; consider one shared cache struct). Acceptable: it recomputes on picks, not on
keypresses. If it creeps, drop plan rollouts to 300 before optimizing anything (the plan
needs coarser resolution than the survival column; sub-percent precision is wasted on a
ranking).

## edge cases, each with the answer

- **The turn (back-to-back picks)**: zero opponent picks between legs; the conditioned
  rollout degenerates to "pick the best pair off the current board", which is correct and
  matches today's behaviour. Determinism test.
- **My last pick**: no leg two, score = vor×need (pinned).
- **mustFill / endgame**: feasibility outranks score exactly as today; additionally the
  leg-2 policy in step 4 must respect it (argmax over players that keep the lineup
  completable — reuse the `Fills` logic, don't reimplement).
- **K/DEF suppression**: candidate membership already excludes suppressed positions via
  Need; the leg-2 policy must too (a rollout must not "actually do" what the tool would
  never recommend).
- **Sim-off mode (`-survival=adp`)**: the plan keeps the v1 formula scoring (EBest-based)
  — conditioned rollouts only exist where the sim does. One switch, same chokepoint
  philosophy as `survivalAt`.

## what the referee can and cannot say

`calibrate` grades survival probabilities against labels; a *decision* score has no
counterfactual labels and cannot be graded the same way. Do not pretend otherwise. What
IS gradeable: the conditioned leg-2 survival claims are near-neighbours of quantities the
backtest already scores (a vantage at my pick `q1` scoring survival to `q2`, with my
actual `q1` pick applied, is literally the next round's walk vantage). A cheap honesty
check, not a gate: for a sample of backtest vantages, compare the conditioned rollout's
P(player survives to q2 | seat's actual q1 pick) against the unconditioned sim row that
calibrate already scores — they should differ only through the removed player's knock-on
effects, and a large systematic gap means the conditioning is leaking something. Print it
in the m7 dev loop; it does not need to ship in calibrate.

The real acceptance evidence is behavioural: a table-driven scenario where greedy and
conditioned plans disagree AND the conditioned answer is demonstrably right — the
canonical one: WR tier about to break, RB tier deep enough to survive my wait; greedy
prices both legs off today's board and can prefer rb-then-wr, conditioned sees the wr
leg-2 fall apart in most futures and flips to wr-then-rb. Build that fixture; it is the
milestone's DoD centrepiece.

## ui

Small, deliberate:

- The plan line gains the odds clause: `plan wr at 3.01 → rb at 4.10 · lands tier-2 rb
  78%`. Clause-dropping rules as ever (the odds clause drops before the plan does).
- The verdict block's "then" line (on clock) speaks the same number.
- Nothing else. No new panes, no cost column resurrection (CostOfPassing stayed off-screen
  for measured reasons; the plan's odds are an outcome claim, not a loss forecast).

## dod

- Table-driven engine tests: determinism (shared seed → identical scores twice),
  common-random-numbers property (two candidates on one seed vs independent seeds — the
  paired variance is visibly smaller), turn degeneracy, last-pick degeneracy, mustFill
  respected in leg-2 policy, suppressed positions never chosen by the policy.
- `TestBestPlanIsTheTopPickChoice` and `TestPickChoicesOnMyLastPick` pass unmodified.
- The greedy-vs-conditioned disagreement fixture, asserting the flip.
- A `mock -snapshot` frame showing the odds clause, eyeballed at 92 and 100 columns.
- The paper's §"Two-pick lookahead" gets an update note and §"What is next" moves this
  from future to shipped (short; the machinery section is docs/engine-v2.md's).
- Rebuild `~/bin/pick6`.

## handoff notes

- Branch `milestone-7`, adversarial-review pass before merging (three real bugs caught by
  it so far in this repo), lowercase wry commit style.
- Do not touch the survival path. If a change here moves any number calibrate scores,
  something has been wired wrong — the plan consumes survival machinery, it must not
  feed back into it.
- If FPL's f1 (derived position lists) hasn't landed yet, this milestone works fine
  against the hardcoded lists; there is no ordering dependency between the two specs.
