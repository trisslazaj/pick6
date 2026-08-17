# milestone 8 — the score becomes the team you end with

The decision score is a hand-built approximation of a quantity nobody computes. Every
piece of `PickChoices`' formula — the need steps (1.0 / 0.6 / 0.25), the replacement
discount, `EndgameSlack`, `mustFill`, the two-leg horizon — exists to answer "what does
this pick do to my final roster" *without simulating the draft*. Milestone 6 built the
simulator; milestone 7 pointed it at leg two and stopped. Milestone 8 runs it to my last
pick and scores the thing itself: **the finished roster**. This file is a complete
hand-off spec: a fresh session should be able to build it from here plus the codebase.

**State of the repo when this was written (2026-08-13):** main at 2ee2408 (milestone 7
merged), v0.2.0 tagged. `internal/engine/lookahead.go` holds the conditioned rollouts
(shared `simCore`, CRN via `planSeed`, `legPolicy`, the `planTable` cache);
`PlanDepth = 2` with a third-leg variant built and off; `docs/fpl.md` is the sibling
spec (no ordering dependency, but see the calendar note in the handoff section).

## the question, plainly

Today: "take the wr" is scored as `(v − R)⁺ × need` summed over two legs, where `need`
is a guessed step function, `R` is a static stand-in for "you could fill this later",
and the horizon stops at my second pick. Those are all proxies for one question — *how
much better is the team I walk out with?* — and each has its own scar tissue: the m4x
wart where an unfilled TE ranked below a bench K, the endgame guard bolted on from
outside, the third leg stuck at "coherent is not right" because a deeper horizon
changes decisions and nothing can say whether the changes are good.

Milestone 8: for each candidate, take him, play the draft out to **my last pick** —
opponents exactly as the sim models them, my later picks by the engine's own greedy
policy — and score each future's **ending roster**:

```
U(roster) = Σ v(starter) over filled starting slots  +  BenchWeight × Σ v(bench)
score(P)  = mean over futures of U(final roster | leg one takes bestNow(P))
```

Rank candidates by that. An unfilled slot at draft end contributes 0, so feasibility
stops being a guard and becomes a price. A flex contributes exactly what the slot
assignment says, so 0.6 stops being a guess. The later picks *actually fill* the
positions the replacement discount was approximating. And because the engine now holds
hundreds of finished versions of my team, the pane can finally say personal things:
what the team probably becomes, and what each choice costs **named in players**.

## what already exists and gets reused unchanged

- The whole conditioned-rollout machine: `simCore`, opponent model, escape, roster
  copies, `planSeed` CRN, the `planTable` cache keyed to the vantage, `legPolicy`'s
  membership rules (suppressed positions never taken; `allowed` = the candidate set;
  mustFill as a *preference*, never a filter — all three m7 rules survive verbatim).
- The conditioning semantics: **the candidate comes off the board at rollout start**.
  score(P) stays "the team you end with *given you get him*"; the survival column and
  the `→ fallback` clauses keep owning "will you get him". Do not reopen this.
- The slot-assignment logic in `fillSlots` — U's starter assignment must be the same
  greedy fill (dedicated then flex) the roster pane and `needFrom` already use. If it
  only returns counts today, factor the assignment out; do not write a second one.
- The pinned invariant: `BestPlan` is `PickChoices()[0]`, one primary key, banners and
  both tenses read it. m8 swaps the score *inside* that architecture.
- `Fills` stays as the outer sort. Mostly redundant once U prices empty slots — but
  K/DEF can arrive from a live feed at value 0, and a hole U prices at 0 × anything is
  invisible. Cheap insurance, keeps `TestBestPlanIsTheTopPickChoice` untouched.

## design

### the objective

`(*State).RosterValue(ids)` — it reads the state's players and lineup shape, so it cannot
be a free function; pure in the sense that it mutates nothing, and table-tested:

1. Assign players to starting slots greedily by value, dedicated slots first, then
   flex — `fillSlots` semantics exactly, including two-flex lineups and superflex.
2. Sum assigned starters at full value. Everyone else is bench at `BenchWeight`.
3. Unfilled slots add nothing. That silence is the endgame guard, priced.

`BenchWeight = 0.25` in tuning.go, inheriting `NeedBench`'s role and value. It is the
ONE constant the objective owns, and the regret harness (below) is where it gets swept.

### the estimator

In `lookahead.go`, the rollout window extends from `mine[1]` to `mine[len(mine)-1]` —
every remaining pick of mine, not two. Per future: leg one is the candidate (removed at
start, per the conditioning), every later leg is `legPolicy`'s first alive man, and at
the end the future's finished roster is scored with U. `score(P)` = mean U. Same one
seed per vantage across all candidates (CRN — the ranking gaps are a few hundred
points on a ~30k quantity; unpaired noise would swamp them).

Two implementation notes that make full horizon affordable:

- **`legPolicy` must stop sorting the board per leg.** Within a position,
  `(v − R)⁺ × need` is monotone in value, so the argmax factorizes: keep one static
  value-ordered list per position and compare the best *alive* man at each of ≤ 6
  positions — O(positions) per leg with per-position cursors, instead of the global
  sort that made depth 3 cost 115ms. Identical results, provably: the winner of the
  6-way compare is the winner of the sort.
- **`PlanRollouts` drops 500 → 300** (m7's own spec pre-authorized this). A ranking
  needs coarser resolution than a survival column.

Cost target: round 1 is the worst case (~180 picks × 300 futures × ~7 candidates).
Budget ~250ms per pick event, shrinking every round — against a 2–3s poll, cached on
`State.plan`, computed when the *previous* pick lands, so the clock frame renders warm.
The old ~50ms figure was a budget for this cache's fill, not for a render; re-state it
in the code comment rather than silently blowing past it.

**Plan B, only if measurement demands it** (cost, or paired variance at full horizon
swamping the gaps CRN is supposed to protect): truncate at H = 6 of my picks and
complete U's unfilled slots with `EBest(pos, pick of that leg)` as a terminal value.
Documented here so the fallback is a decision, not an improvisation. Measure first.

### what dissolves, and where the constants retreat to

- `NeedFlex` — out of the score. The assignment computes flex worth exactly.
- `EndgameSlack`, mustFill-as-score-input — out of the score. U prices the hole.
- `R(P)` — out of the score. The later legs actually fill the positions. R survives
  inside `legPolicy` (it is a fine heuristic for a *policy*) and in the vor tie-break
  contexts that never touched the plan.
- `PlanDepth` and the third-leg branch — **deleted**. The horizon is all my picks; the
  promotion question this spec's predecessor left open dissolves rather than resolves.
- The m4x wart (te below k at 15.03) — dissolves: the open TE slot is priced at the TE
  the futures put in it, not at `v − R = 7`.
- `NeedStarter/NeedFlex/NeedBench` keep their jobs everywhere else: `legPolicy`
  ordering, urgency, `SafeToWait`, the *opponents'* need model, and all of adp mode.
  They retreat from the objective to the policy, which is where guesses belong.

### what must not move

Survival, the tilt, tier holds, `SafeToWait`, banners' inputs, the deny chip, the
opponents' machinery: untouched. `-survival=adp` keeps the v1 formula **wholesale** —
same switch, same chokepoint philosophy as `survivalAt` and m7. Every pre-existing
engine test runs in adp mode and must pass unmodified, including
`TestBestPlanIsTheTopPickChoice` and `TestPickChoicesOnMyLastPick`. The m7 verification
ritual repeats: three `-data` frames byte-identical against the base commit, because a
lookahead that moves a number `calibrate` scores is wired backwards.

### degeneracies, each with the answer

- **My last pick**: no rollouts — `score = U(roster + candidate)`, exact. That
  difference IS vor × need computed honestly, so the degeneracy stays the same idea.
- **The turn**: the m7 `n = 1` shortcut applies only when zero opponent picks remain in
  the whole window, which at full horizon means the draft's final picks. Fine — it was
  an exactness optimization, not a behavior.
- **Endgame membership**: unchanged from m7 — the policy takes only `allowed`
  positions, mustFill is a preference, suppression is absolute. A rollout must not
  "actually do" what the tool would never recommend.
- **Registered-but-unranked players** (value 0) on my own roster: they contribute 0 to
  U through either assignment path; harmless, same as today's vor.

## the referee comes first: `pick6 regret`

`calibrate` grades survival and cannot grade decisions; the room-warp cap is the scar
from shipping a decision-shaped change on one fold's plausibility. So the instrument
lands **before** the scorer, and prints its baselines before the contender exists.

**Replay-regret.** For each cached real draft (three today; every future cached draft
joins automatically): replay it with **my seat played by a policy** — at each of my
real vantages, the policy picks off the board as it actually stood; the other eleven
seats keep their real recorded picks. When my counterfactual pick collides with a
player a later real pick names, that manager receives **their own next surviving real
pick**, else the era board's best available by adp; every substitution is counted and
printed, because the count is part of the result. Ending rosters are scored two ways,
both printed, never averaged:

- **U on the era board**, with value derived from era adp rank through the existing
  `ValueBase · exp(−rank/ValueDecay)` fallback (no historical FantasyCalc exists —
  probed and dead, see CLAUDE.md's dynasty section — and 2026 values on a 2024 draft
  would be era leakage; the fallback is era-consistent by construction and identical
  across policies, which is all a comparison needs).
- **Era-adp rank captured** (sum of drafted players' rank-value at their era prices) —
  the exchange-rate robustness check. If the new scorer wins on U and loses here, the
  value curve is doing the winning, and the tool says so rather than hiding it.

**Policies on the table**: what I actually did · v1 formula (adp mode) · shipped m7
scorer (sim, depth 2) · the m8 roster scorer · best-available-by-era-adp ·
best-available-by-value. Sim policies get era room curves and escapes with the fold's
future held out — reuse calibrate's `splitByStart` plumbing, same causality rule, same
reason.

**The gate**: m8 ships only if the roster scorer beats the shipped m7 scorer on U on
**both causal folds** (2025-a, 2025-b). 2024 has no prior drafts, so its sim policies
run in a configuration live 2026 is never in — report it, don't gate on it (the same
treatment v2's verdict gave it). Two caveats stated on every run, in the printout:
U is also the quantity the new scorer optimizes (the test is whether optimizing it
against the *sim* transfers to *real* rooms — the opponents here are real); and
substitution counts bound how far each counterfactual drifted from the recorded draft.

**Scenario fixtures** (dod_test style) ride alongside as the qualitative half: the m7
disagreement scenario re-asserted under full horizon; a wheel fixture (slots 1/12,
where the retired depth-3 experiment flipped 16 of 54 first legs away from rb — the
regret table is how we finally learn whether such flips are right); the 12.09/13.09
endgame states that already have regression history.

## ui

The math change is what makes the pane personal: the score's native units are now
"your finished team", so the display additions are the recommendation's homework shown,
not decoration. Frames first (mock -snapshot at 92 and 100 columns) before anything
commits, per the working style.

- **The verdict learns consequences, named in players.** CRN means the top choice and
  the runner-up were scored on the same futures; diff their ending rosters and name
  where they diverge: `taking rb instead costs you mcbride — warren at te in most
  futures`. Naming rule: name the modal displaced player when he recurs in ≥
  `planNameShare` (~0.35, a ui constant) of futures; below that speak in tiers
  (`costs you a tier at te`); when the diff is diffuse or tiny, **drop the clause
  entirely** — a consequence line that says nothing must not print. One clause, verdict
  block only, drops before the tier note does.
- **The plan line grows into the skeleton**: `plan wr now → rb 4.10 → te 5.03 → qb
  6.10 · lands tier-2 rb 78%`. Legs drop whole from the right (joinClauses rule); the
  odds clause stays pinned to leg two — the nearest claim is the actionable one — and
  drops before any leg does. The data tab's width-starved strip keeps two legs.
- **`your team from here`** — the block for the left pane's spare space, both tenses,
  under the field rows. One row per *unfilled* starting slot, read from the top
  choice's futures (one brain: the block can never contradict the verdict): the modal
  filler — named when his share clears `planNameShare`, else his modal tier — the pick
  it modally arrives at, and the inclusive odds (`SecondOdds` semantics, per leg).
  `else <name>` only on named rows with a strong second mode. Rows drop from the
  bottom on height pressure; the whole block drops before the field rows give ground.
- **The market's dissenting pick becomes a scored candidate** (one more rollout set on
  the shared seed). Behavior change, deliberate: when the futures say the market's man
  leads to the better roster, he **wins the row and can win the verdict** — the rice
  case graduates from a dissent note to a contest the board can call either way. When
  he loses, the row renders exactly as today.
- Captions follow the score: `if not him — ranked by the team each leads to` on the
  clock, and the off-clock long form gets the same tail. `Plan`/`PickChoice` grow a
  per-leg claims slice (pos, pick, tier, odds, name, share) replacing the
  Second/SecondTier/SecondOdds trio; the endgame line stays (it names slots k/def
  suppression hides, which U cannot render).

## dod

- `RosterValue` table tests: flex assignment, two-flex lineup (the user's real 2025
  shape), superflex, bench discount, unfilled-slot pricing, value-0 players.
- `pick6 regret` lands first, printing the baseline policies, both scorings, collision
  counts, and the two caveats — **before** the m8 scorer exists in the tree.
- The gate, recorded in this file and CLAUDE.md before merge: roster scorer vs shipped
  on both causal folds, both scorings quoted.
- Determinism (same seed twice → identical ranking), paired-variance measured at full
  horizon and quoted against unpaired (m7 measured "under half"; know the new number),
  cost benchmark per round quoted (rounds 1 / 8 / 15).
- All pre-existing engine tests pass unmodified in adp mode; three `-data` frames
  byte-identical against the base commit.
- The wheel fixture and the re-asserted m7 disagreement fixture.
- Frames eyeballed at 92/100 columns: on-clock with the consequence clause, off-clock
  with the skeleton + roster block, an endgame frame where the block and the endgame
  line coexist, and a market-candidate-wins frame if one exists in the scripted mocks.
- Paper: §"Two-pick lookahead" gains the objective's upgrade note; the honest-
  simplification paragraph about leg one's optimism survives (conditioning unchanged).
- Rebuild `~/bin/pick6`.

## handoff notes

- Branch `milestone-8`, adversarial-review pass before merging (it has caught real
  bugs in every engine milestone so far), lowercase wry commit style.
- **Do not touch the survival path.** If any number calibrate scores moves, stop.
- Keep `PlanRollouts` overridable in tests (the existing var pattern) — full-horizon
  rollouts at 300 futures per frame would make the scripted-mock walks crawl.
- Calendar: the FPL draft is 2026-08-20 and the NFL drafts follow soon after. The
  referee-first build order exists so this can stop safely at any phase: harness with
  baselines is shippable alone; the scorer merges only with the gate green; the UI
  pass rides last. `-survival=adp` remains the draft-night rip cord for everything.
- CLAUDE.md's milestone list: this entry is 8; the global-store and Claude-grades
  ideas renumber to 9 and 10.

---

## postscript: what actually shipped (2026-08-14)

Built in full, graded, and **switched off**. `-scorer pair` (milestone 7's) remains the
default; `-scorer roster` turns the objective above on, and brings the three display
features with it. The order of this file's own build was inverted — the scorer landed
before the referee rather than after — which cost nothing in the end, because the referee
printed every baseline before it scored the contender and the contender lost.

**The gate, as specified, is not met.** Over 80 paired seeds of `pick6 regret`, at the
shipped `PlanRollouts = 500` and after the adversarial review fixed four bugs in the
referee itself:

```
fold     priors            m7 U         m8 U    m8 - m7       se   linear m8 - m7
2024     reported        808459       850348     +41889     2155    +140
2025-a   causal          978267       978449       +181     1516     +11
2025-b   causal          929417       928688       -729        0      -1
```

(80 paired seeds at the shipped `PlanRollouts = 500`, after the adversarial review fixed
four bugs in the referee itself. 2025-a is a tie inside its own error bar and 2025-b is a
0.08% loss with no error bar at all — the honest word for the pair is *indistinguishable*.
2024 is a large win and does not gate: it has no priors, so its sim policies run in a
configuration a live clock is never in.)

Four things worth carrying forward:

1. **The strongest hypothesis for why it does not win**, and it was not attempted: the
   continuation policy inside the rollouts still picks by `VOR × need` while the score is
   `U`. A rollout that models my later picks with a different objective from the one being
   maximised under-estimates every candidate, and not necessarily by the same amount.
   `ΔU` for adding one player is O(1) given the current assignment (his value if a slot is
   open, the displacement if he beats a starter, `benchWeight × value` otherwise), so a
   U-greedy leg policy is cheap. That is the next move.

2. **The harness has three blind spots and they are printed on every run.** Its value
   curve is `ValueBase·exp(−rank/ValueDecay)` off era adp, which is *position-blind* — no
   historical value source exists — so it cannot referee any rule about cross-position
   pricing, which is a large part of what `U` is for. The opponents cannot react. And the
   `actual` row is unfairly penalised: 7 of the user's real picks across the folds were
   handcuffs the era board never priced, worth 0 to `U`, while a model policy cannot take
   an unpriced man at all.

3. **A real bug fell out of the build, independent of the gate**: `U` was paying a quarter
   of an inflated value for a *backup quarterback*. `BenchWeight` now carries vor's own
   two-index rule — a bench body is worth it only if his position can reach a lineup
   through a flex slot — and `planPolicy` (this spec's `legPolicy`, renamed) prices the
   same way. Before that fix the board
   recommended a second QB in a 1QB league, which is the ~930-point subsidy of the VOR
   section wearing a new hat. The fix is right and it is also what flipped the gate from
   passing to failing, which is the most interesting thing in this file: the harness's
   position-blind value curve cannot see why a backup QB is worthless, so a correction
   that is right about football reads as pure loss to it.

4. **`PlanRollouts` stayed at 500.** The drop to 300 this spec pre-authorised was not
   taken, because the shipped path is milestone 7's and leaving it at the count it was
   measured at keeps the default board byte-identical. A promotion should take the drop
   with it: the roster objective costs 366ms in round 1 at 500 against a ~250ms budget,
   and ~220ms at 300.

Everything else in this spec was built as written — `RosterValue`, the full-horizon
estimator, the O(positions) leg policy (renamed `planPolicy`), `pick6 regret`, the plan
skeleton, `your team from here`, the consequence clause, the market's dissenting man as a
scored candidate, and the wheel fixture — with three deliberate departures worth naming:

1. The `Second`/`SecondTier`/`SecondOdds` trio was **kept** alongside `Legs` rather than
   replaced by it, because adp mode has no futures to summarise and nothing else to say.
2. `best-available-by-value` was **dropped** as a regret policy. On an era board value is a
   monotone function of adp rank, so it is the same policy as best-available-by-adp, and
   printing it twice would be printing one number twice. `fill-the-lineup` took its place
   as a genuinely different baseline.
3. The plan line's drop order **inverts** the spec's: leg two outranks the odds clause, and
   the odds outrank every leg after it. The spec's order meant the legs always filled the
   line and the odds never rendered at any terminal width, silently deleting a claim
   milestone 7 shipped.

`PlanDepth` is retired to a comment. The survival path is untouched and it was verified
rather than assumed.

## the human's verdict (2026-08-14, same day)

Shown the roster scorer's recommendations on the 2026 board, the user rejected it: reaching
for a quarterback and a tight end early is "objectively bad" and he does not want the
scorer. **That outranks the gate.** A decision score has no labels; the harness has three
blind spots it prints on every run; a drafter who has played this league for years reading
the output is a better instrument than either.

The mechanism is concrete, and it is the starter side of the same mistake the bench weight
fixed. `U` sums STARTER values, and the value curve is not replacement-normalised across
positions — which is the entire reason `vor.go` exists. Measured at 3.06 of the scripted
mock:

```
              value    vor   replacement
josh allen     6216   5135          1081
malik nabers   5724   5675            49
breece hall    5183   5183             0
tyler warren   3421   3200           221
```

vor ranks nabers over allen by 540. `U` ranks allen over nabers by 473 — i.e. `U`'s order
is the RAW-VALUE order, and the simulation that is supposed to recover the difference
through "what would I get at this position later" recovers only about half of it.

So the promotion bar is now three things, not one: fix the starter-side pricing (the leg
policy maximising ΔU instead of vor × need is the same fix approached from the other end),
win a causal fold, and change the user's mind. Absent all three this is a documented
negative result that happens to still compile — which is what the `Negative results`
section of the paper is for, and where it will go if it is ever removed from the tree.

The board a human actually drafts with is unaffected and that was measured, not argued:
across 280 scripted frames (7 seeds x 5 slots x 8 vantages) the verdict, the ranked rows,
the banner and the plan's positions are identical to 2ee2408.
