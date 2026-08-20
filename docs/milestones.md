# milestone log

the build history, moved out of CLAUDE.md 2026-08-18 because it was a third of the file and
none of it is an instruction. every number here is a measured result or a retraction of one;
read it before re-litigating a decision, not before writing code. specs live beside it
(`milestone-4x.md`, `engine-v2.md`, `milestone-7.md`, `milestone-8.md`) and the write-up is
`pick6-engine.tex`.

## Milestones (build in order, each one is demoable)

1. **Data**: `pick6 fetch` works end to end — Sleeper dump cached and filtered to the active pool,
   FFC ADP pulled for the chosen format, FantasyCalc pulled and joined, rankings applied, mapping
   built, unmatched names printed. DoD: run it twice, second run hits cache; the unmatched list is
   empty for FFC (it was measured at 0/233, so anything else means the join broke). **DONE.**
2. **Static board**: `pick6 mock` renders the full three-pane UI from a scripted fake draft state. DoD: it looks good in a screenshot. **DONE.**

   Milestone 2 orders position groups by `value(bestNow) * need(pos)`, which is a
   placeholder — true urgency (the value *drop* between now and my next pick) is
   milestone 4 and needs the survival function. Cliff highlighting and the run
   banner are already live, because both are pure counting over data `fetch`
   already wrote and neither depends on survival. `/` fuzzy search landed with
   milestone 5's polish pass — see the `/` overlay under UI.
3. **Live sync**: `pick6 live <draft_id>` polls picks and updates the board in place. DoD: works against a Sleeper mock draft (Sleeper lets you run practice drafts). **DONE** — verified against a real running draft in the milestone-5 "Live-sim shakedown" (below): 74 live-applied picks over rounds 3–7, zero snake-math desyncs.

   Replayed against three real completed drafts from this user's own leagues —
   552 picks, three different draft slots, two different lineup shapes — with
   **zero snake-math desyncs**. That validates metadata parsing, roster shape,
   pick application and `SlotAt` far better than synthetic fixtures could. What
   it does *not* exercise is the polling delta path: picks arriving while the
   board is up, and on-the-clock transitions. That needed a live draft, and got
   one — see the milestone-5 "Live-sim shakedown" entry.
4. **Engine**: urgency ordering, cliff highlights, run banner, all wired into the board. Table-driven tests for snake math, survival, urgency. DoD: replaying a scripted RB run in mock mode flips the banner and re-sorts the board. **DONE.**

   Ships two visible extras: every player row shows its survival probability
   (the engine's number on screen, so nobody has to trust the ordering blind),
   and a group whose bestNow will keep reads "safe to wait". Ties are common on
   your own pick, where every urgency is exactly zero, and deliberately keep
   display order via the stable sort. The scripted-run DoD test lives in
   `internal/ui/board_test.go`.

   **Milestone 4x (`docs/milestone-4x.md`): DONE, all four phases.**
   The engine section above is v1.5. What each phase actually settled:

   - *Phase 0* — `pick6 calibrate` backtests survival against the real 2024
     draft from all twelve seats (15,923 labelled predictions). Every constant
     below is now measured rather than argued. It is the gate; run it after any
     change to survival math.
   - *Phase 1* — `PSurviveAt`, the exactly-N tilt, `EBest` urgency, probabilistic
     tier-hold. Gated: brier 0.0868 → 0.0677, log-loss 0.3007 → 0.2288.
   - *Phase 2* — `BestPlan`, the two-pick lookahead, rendered as the `plan` line.
   - *Phase 3* — `pick6 scout`, VOR tie-break, K/DEF values, endgame guard. The
     room-warped ADP shipped capped at the top five of each position (brier
     0.0670 → 0.0660, log-loss 0.2250 → 0.2222 on 2024) and **that cap was later
     removed**: it lost to full depth on both 2025 folds, on both metrics and on
     the per-position gate, and the 2024 fold that chose it turned out to have no
     causal curve. The warp is now uncapped and still on by default — `-room` is
     on,
     `-room=false` opts out. That per-position claim is **computed** by the gate
     on every run, not asserted: it was briefed as "better on every position",
     which the WR row already contradicted. The tail is wrong structurally, not by
     tuning: a national ranked list runs deeper than any finite draft, so past
     the room's appetite rank→room-pick prices players later than the market by
     construction. **Every number in this bullet is retracted — read the two notes
     below before reusing any of it. The per-position claim did not replicate on
     the 2025 folds, and the 2024 numbers themselves came from a curve built out
     of that draft's own future, so `calibrate` no longer produces them at all
     (`-lookahead` does). The cutoff is unidentified; the default stays on for the
     structural argument above plus never-worse, not for a score.** `fetch` still
     prints the full curve as a read for the human, at every depth.
   - *Phase 4* — injury/news truth layer, ADP support fields, staleness guard,
     tiers-vs-market report. Sigma shrinkage **passed** (0.0677 → 0.0670) and is
     on. The rule-of-three support floor **is built and deliberately not wired**:
     `high` is a minimum and `adp` a mean, so its window always sits before a
     player's own price where the curve already reads ~1 — it qualifies on 9,817
     backtest rows and binds on zero.

   Two results worth not relearning: the pre-tilt `-tune` recommendation to widen
   `SigmaMin` to ~14 was an artifact of scoring without the tilt (min 14 scores
   *worse* once tilted), and per-player sigma still loses to a flat 6.0 on
   log-loss even after shrinkage (0.2250 against 0.2233, both under the tilt). No
   constant was moved on one draft's evidence. `calibrate`'s model table grades
   every row against **what ships** — since 3a that is the top-5 warp row, not the
   pre-4x engine. On the 2024 fold that row now equals shrunk-sigma-plus-tilt,
   because that fold has no causal room curve, so the flat-sigma baseline **does**
   carry a "beats what ships on log-loss" marker again (0.2233 against 0.2250) and
   the tool prints it. Threat 9 in the paper is the honest reading: on the only
   fold with a per-player spread, the per-player pipeline does not win on either
   metric a reader can check.

   **The second and third folds (2026-07-30): 2025 became scorable, and the room
   warp's ordering did not survive it.** The user supplied an era-correct 2025
   ADP board (FantasyPros overall export, see the data-sources section), which
   unlocks both 2025 drafts — including `1261824503076360192`, the same twelve
   managers he drafts against in 2026. `calibrate` now scores three folds, one per
   draft, each with its own room curve built with that draft held out (asserted in
   `checkCurve`, printed on every run).

   **READ THE TIME-ORDER NOTE BELOW BEFORE ANY OF THE ROOM-WARP NUMBERS IN THIS
   LOG.** Holding out only the scored draft is not enough, and every warp figure
   written above and in the bullets below was produced without the second rule.

   - **The 2024 numbers did not move.** Every figure in this log is byte-identical
     after the change; the only new row in that fold's table is the flat-sigma
     comparison the cross-fold section needs.
   - **The 2025 folds run flat.** The export has no `stdev`/`times_drafted`/
     `high`/`low`, so every player gets `SigmaDefault` and those folds **cannot
     referee** `SigmaMin`, `SigmaMax`, the shrink or the support floor. They can
     referee the tilt, EBest, the conditioning, the warp and the need weights.
     The cross-fold table therefore forces 2024 flat too.
   - **The warp result inverts.** On 2024 the full-depth warp lost (0.0670 →
     0.0671 brier, 0.2250 → 0.2327 log-loss) and the top-5 variant won cleanly.
     On **both** 2025 folds the full-depth warp wins outright (2025-a 0.0197 →
     0.0175 / 0.0717 → 0.0616; 2025-b 0.0210 → 0.0189 / 0.0798 → 0.0701) while
     the top-5 variant, though still better overall, **fails the per-position
     half of the gate** (RB, WR and DEF regress on 2025-a; RB, WR, K and DEF on
     2025-b). The verdict text says so on every run.
   - **Board depth was the leading suspect. It was tested and it is NOT the
     explanation.** The warp is indexed by rank *within a position*, and FFC
     publishes 178 names against FantasyPros' 389 for drafts of 180 and 192 picks
     — so the k-th receiver is a different player in the two folds, and the warp's
     mean shift flips sign with it (+2.39 picks on 2024, −3.64 and −2.96 on the
     2025 folds). `calibrate -depth 178` truncates every era board to its N
     cheapest players and re-runs the whole gate. **The verdict does not move**:
     2024 still prefers top-5 (0.0670 → 0.0660 / 0.2250 → 0.2222) and both 2025
     folds still prefer uncapped (2025-a 0.0561 → 0.0502 / 0.2118 → 0.1929;
     2025-b 0.0581 → 0.0531 / 0.1923 → 0.1720). Truncation drags the mean shift
     most of the way to 2024's (−1.24 and −0.98) and changes nothing else. As a
     bonus the control makes briers genuinely comparable for once — observed rates
     become 0.8844 / 0.8800 / 0.8852 and floors 0.1022 / 0.1056 / 0.1016 — and the
     model pays 65% / 53% / 57% of its own floor, which is the untruncated
     65% / 52% / 55% again. **What is left unseparable is season and vendor**:
     2024 is FFC's sample of 906 real drafts, 2025 a three-platform ranking
     consensus, and no truncation fixes that.
   - **The cutoff was removed.** `calibrate` sweeps it on
     every fold instead of on the one it was chosen from. **No k clears the full
     gate (better overall AND no position regressing) on all three folds.** 2024
     has a clean interior optimum at k=4–5; both 2025 folds are monotone in k and
     want no cap at all. At the time this read "what survives is an interval:
     every k in [1,8] is never worse on any fold, while k≥12 and uncapped are
     worse on 2024" — **and the time-order note below kills that too**, because
     2024 set the upper bound of 8 and 2024 has no causal curve.
   - **The cap was held at 5 for one round of evidence, then dropped.** It first
     shipped on "it wins cleanly on the fold we have", was demoted to "it is
     never worse on any fold", and was finally removed once both causal folds
     beat it outright — on both metrics, on the per-position gate, and at
     `-depth 178`. The per-position claim is **retracted**. Note the shape of the
     mistake: picking a point inside a never-worse band by cross-fold tally is
     picking it on one fold, committed with more data. What would settle it: a
     fold that is FFC-priced, is not 2024, and has a cached draft before it.

   **LEAVE-ONE-OUT WAS NOT ENOUGH: the curve must also PREDATE the fold
   (2026-07-30, same day, found by review).** Measured draft start times:
   2024-09-01, 2025-09-02 (2025-b), 2025-09-04 (2025-a). So under leave-one-out
   alone the **2024 fold's curve was built entirely from drafts that ran a year
   later**, and 2025-b's was half posterior. At the clock the live tool can only
   have drafts that already happened, so those folds were measuring a tool nobody
   can run — and this is not symmetric noise, because **2024 is the only fold that
   ever preferred a capped warp**. `splitPool`/`splitByStart` now hold out each
   fold's future as well as itself; `checkCurve` errors if either rule is broken;
   `live -replay` applies the same rule through the same function so the frame you
   eyeball agrees with the paper. `calibrate -lookahead` reprints the old regime
   under a banner and is the only way to obtain any of the retracted numbers.

   - **2024 now has NO room curve at all** — nothing in the cache precedes it —
     so all its `3a` rows tie their unwarped bases, its shipped row is
     0.0670 / 0.2250, and it says nothing about the warp. It still grades the
     tilt, the shrink, sigma, the floor and the conditioning.
   - **The cutoff sweep is two folds, both 2025, both monotone toward no cap.**
     The old band "every k in [1,8] is never worse" had its **upper bound of 8 set
     exclusively by 2024**. On the two causal folds *every* k is never worse,
     including uncapped, so the band excludes nothing and is no longer an argument
     for capping. k=5 wins 1 of 2 where uncapped wins 2 of 2.
   - **2025-b's curve shrinks to one draft (2024 only, w 0.33)**, so its warp
     numbers moved: full-depth 0.0210 → **0.0200** / 0.0798 → **0.0738**, top-5
     0.0210 → **0.0209** / 0.0798 → **0.0790**.
   - **The purity table thins to one fold** (only 2025-a has two prior drafts), so
     "sample size dominates purity" is now plausible-and-unconfirmed rather than
     measured: 5 of its 6 supporting cells came from look-ahead rows.
   - **What kept the cap alive to the end was an ARGUMENT, not a score**: a
     national ranked list runs deeper than any finite draft, so past the room's
     appetite rank→room-pick prices players later than the market by
     construction. It predicted something measurable, the measurement went the
     other way on every fold that could test it, and the argument lost. `-room`
     itself stays on: uncapped is never worse on either causal fold.
   - **The tilt REPLICATES on all three folds** — better on both metrics
     everywhere (2024 brier 0.0868 → 0.0677 / log-loss 0.3007 → 0.2288; 2025-a
     0.0207 → 0.0197 / 0.0732 → 0.0717; 2025-b 0.0216 → 0.0210 / 0.0803 → 0.0798)
     — and its magnitude collapses by ~20×, for a measured reason. The tilt fixes
     a removals-budget bias, and the bias is a property of the 2024 board: the
     model expected 16.8 removals against a 12-pick window there (median exponent
     0.44) against 12.0 and 11.8 on the 2025 boards (medians 1.08 and 1.08). A
     constraint that is already satisfied does nothing, which is the correct
     behaviour for a constraint. **This upgrades the tilt from one fold to three.**
   - **One new fragility, found by the depth control: on a board no deeper than
     the draft, the tilt aims at a budget the board cannot supply.** Log-loss
     regresses under the tilt on **both** truncated 2025 folds — 0.1778 → 0.2118
     on 2025-a and 0.1914 → 0.1923 on 2025-b. The clamp is only the extreme case:
     2025-a has 5 of 180 vantages at `c = TiltCMax` (pinning c at 64 crushes every
     survival toward 0), while **2025-b has ZERO clamped vantages and regresses
     anyway**. The shared mechanism is the target — N counts every pick but the
     board only holds ranked players, so 12 picks remove only ~10.7 board players
     and the tilt over-corrects on every vantage. **So do not "fix" this with the
     c=1 clamp fallback alone and expect it to go away**: that would fix 2025-a's
     large move and not touch 2025-b's. Falling back to c=1 on a clamped vantage
     is still more honest than falling back to 64. Zero vantages clamp on any
     fold's untruncated board (0 of 516), so this is a diagnostic and not a fold
     result — but the shipped 2026 board is 201 FFC names against a 192-pick
     draft, which is the thin regime, and live the pool is thinner still in
     effective mass (registered-but-unranked players sit at p=1 contributing
     nothing to the sum). Not fixed here — it is a modelling change, not a bug fix.
   - **League purity does NOT matter; sample size does.** `calibrate` now rebuilds
     each fold's curve from every subset of the *other* drafts and scores all of
     them. The controlled comparison is the two single-draft rows — same blend
     weight w=1/3, different room. At that fixed weight the 9/12-overlap own room
     and the 5–6/12-overlap stranger league score about the same (2024: 0.0662 /
     0.2218 from 9/12 against 0.0664 / 0.2241 from 5/12 — purity wins; 2025-a:
     the 6/12 stranger beats the 9/12 own room under the full-depth warp,
     0.0181/0.0645 against 0.0185/0.0649 — purity loses). Meanwhile the **pooled
     two-draft curve beats both of its own components on 5 of 6 cells**. So mixing
     the stranger league in *helps*, and it helps because it is a second draft.
     Feed the curve more casual home leagues; do not purify it down to one.
   - **Sleeper's own ADP column beats the three-platform consensus on both 2025
     folds**, and not marginally: 2025-a brier 0.0197 → 0.0150, log-loss 0.0698 →
     0.0495; 2025-b 0.0208 → 0.0177, 0.0787 → 0.0617. QB carries most of it
     (2025-a 0.0375 → 0.0190; 2025-b 0.0369 → 0.0269), WR and TE the rest. Same
     prediction set both ways — every scored row carries both prices, and the only
     dilution is the 31 export rows where the Sleeper column was blank and avg
     stood in. It wobbles under the depth control (at 178 names 2025-b splits:
     brier 0.0578 → 0.0521 better, log-loss 0.1912 → 0.2096 worse), so the effect
     is strongest on the deep board. **This was the largest unexploited result in
     the repo, and it is now exploited**: `fetch -adp sleeper` (default, since
     2026-07-30) overlays this column's order onto FFC's own distribution for the
     live 2026 board — see the FantasyPros section above for the mechanism.
     `calibrate`'s own scored default stays `avg` for the fold comparison, because
     one platform's ranking beating a consensus on the platform's own drafts is
     the result most likely to be the room, not the market — that argument is
     about which column to *score*, not which one the live board should *use*.

   **Two objective mismatches the cross-phase review found**, both only visible on
   your own pick. **The first is FIXED** by the milestone-5 recommendation pass:
   `PickChoices` prices both legs over replacement exactly as sketched here and
   `BestPlan` is its top row, so the plan line and the ordering under it are one
   computation (`TestBestPlanIsTheTopPickChoice`). The second remains:

   - *The vor tie-break can rank an unfilled starter below a filled position.* At
     15.03 (seed 1) with rb and te open, group order is rb, wr, k, te: hockenson is
     189 against `R(TE)` 182, so te scores 7 while a bench kicker scores 9. That is
     VOR working as specified — the shallow position's headline number really does
     overstate what taking one buys — but it reads wrong next to `need rb te`.
     Feasibility (`Fills`) now outranks score inside PickChoices, which covers the
     endgame case; the mid-draft cosmetic version survives. Changing it means
     deciding whether an open slot should dominate VOR outright.
5. **Polish + release**: `pick6 board` manual mode, README with a screenshot/GIF, release workflow. DoD: `go install` works from a clean machine.

   **Offline + release pass: DONE (2026-08-03).** The last unbuilt subcommand and the
   last unbuilt keybind, plus the plumbing to ship it.

   - *`pick6 board`* — manual mode, `internal/ui.NewManualModel`. It reuses `Model`
     rather than getting its own, because the difference is one field: a manual
     draft is a scripted draft whose script is a person. Every key that needs a
     picker checks for one, so `space` and `a` are no-ops there instead of
     panicking on a nil `Autopicker`. `x` spends a pick through the same
     `State.Draft` the mock uses and names who it just spent it on, so a
     mis-selected fuzzy match is visible immediately rather than three picks later.
   - *The `/` overlay* — see the UI section for the matching rules and why
     selection and marking are two keys.
   - *`Board.Mode`* — mock / live / manual, which is what lets the footer name only
     the keys the mode binds. `live -replay` renders with `ModeLive` for the same
     reason it holds each fold's future out of the room curve: the frame you
     eyeball has to be the frame the tool shows.
   - *The footer's key ladder* (`fitFooter`) — adding one key broke the data-age
     clause at every width, which is the sort of thing the height clamp has caught
     for a year and the width budget had no equivalent for. Keys now drop before
     the age does.
   - *Release plumbing* — `.github/workflows/ci.yml` (build, vet, test on every
     push) and `release.yml` (tag `v*` → cross-compiled darwin/linux amd64+arm64
     tarballs + checksums, published with `gh release create`). It runs the tests
     before publishing because the module proxy caches a tag forever and `@latest`
     starts pointing at it immediately. `pick6 version` reports the `-ldflags`
     stamp; a source build says `dev`.
   - *The sidebar's need line stopped wrapping.* It already abbreviated `flex` to
     `fx` for a two-flex lineup and that was never enough: ten unfilled starters
     overrun a 34-cell sidebar even abbreviated, so the line wrapped and put a
     lone "def" on its own row — the exact rendering fault the abbreviation was
     added to prevent. It now shows what fits and counts the rest (`qb rb rb wr wr
     te fx +3`). Only reachable in the first few picks, since the list shrinks
     every time you draft, and only in a lineup deeper than nine slots — which is
     to say it was already reachable from `live` in this user's 2025 league.
   - *Stale comments removed* — three sites in `cmd/pick6/mock.go` still described
     the room warp as capped at `adp.RoomWarpTopK` and told the reader to go read
     that constant, which **no longer exists**. The `-room` help text said the same
     thing to users.
   **v0.1.0 shipped 2026-08-04, and the DoD is met.** Both distribution channels
   were verified against the real release, and they are genuinely independent —
   worth knowing, because only one of them touches the workflow at all:

   - *`go install ...@latest`* fetches the tagged source from the module proxy
     and compiles it on the user's own machine. It needs a tag and nothing else;
     no workflow, no archive. Verified from an empty GOBIN.
   - *The release archives* are the workflow's output, for people with no go
     toolchain. Verified by downloading all four, checking every sha256 against
     the published checksums.txt, and running the extracted binary.

   Two defects the release itself surfaced, both now fixed:

   - **`shasum` is perl.** The checksums step was written and tested on macos,
     where perl is guaranteed; the runner is linux, where coreutils is and perl
     merely usually is. It would have failed after the tests and cross-compiles
     had already passed. Note the general shape: **a tag-triggered workflow runs
     the copy of the yaml frozen in the commit that tag points at**, so a fix
     landed afterwards fixes nothing and the tag has to be cut again.
   - **Every `go install` called itself "dev"**, including one that had just
     downloaded a tagged release, because `-X main.version` only exists inside
     the workflow. `buildVersion()` falls back to `debug.ReadBuildInfo()`, which
     is where go records the module version it built from. Found only by actually
     running the DoD rather than declaring it met.

   - **Left open**: v0.1.0's archives report their version correctly but its
     `go install` path still says "dev", since the fix landed after the tag. The
     next tag makes the readme's claim true.

   **Board-tab pass: DONE** (the rest of milestone 5 is untouched). Driven by a real paused draft
   rather than by fixtures, which is why all three findings are about frames the mock never made
   anyone stare at. `live -replay` gained `-data`, matching `mock`, because the urgency strip was
   the only place the numbers behind the ordering were visible.

   - *Two subjects, one line.* The group header put a claim about a tier next to a verdict about a
     man with nothing distinguishing them. Now it names the man. See the UI section.
   - *The clock had no decision view.* Added one; `engine.CostOfPassing` is the number it ranks on,
     and `BestLater` moved to the `ActPick` horizon so "who instead" stops answering *himself*.
     Group sort moved to the same key, which shrank vor to a genuine tie-break.
   - *The sidebar hid the wheel.* Two upcoming picks is the wrong number of picks to show at slot 1.
   - *The pane never said whether it was forecasting or deciding.* Off the clock the same ordering
     is anticipation ("expect to lose 990 of te value before you pick"), not advice; unlabelled, it
     read as advice on the frame where nothing can be acted on. Both frames now carry a lead line.
   - *The board tab rendered survival in grey.* Only the data tab banded it, so the answer to the
     board's own question was its least visible number. One `survStyle` for both tabs now.
   - **Left open**: the turn, where every cost is legitimately ~0. Documented above, not faked.

   **Recommendation pass: DONE** (2026-08-02, the second milestone-5 board pass). Driven by a live
   mock (draft 1389794179919413248) paused at the exact 3.01 frame where the tool recommended
   josh allen over malik nabers on `cost 3002 vs 2419` — numbers in units nobody feels. What
   settled it: allen at 25 was **defensible on every source** (his adp is 26, this room takes qb1
   near 20, fantasycalc agrees), so the failure was the display arguing in invisible units, plus
   one real engine bug (replacement indexed at drafted counts) and one real ordering bug (ranking
   the decision on window-loss). What shipped, in order:

   - *Replacement's two-index rule* — see the VOR section. QB stopped collecting a ~930-point
     subsidy from bench arms that never start.
   - *`engine.PickChoices`, the single primary key* — see the engine section. Verdict, rows,
     plan, banner gates: one ranking. The turn degeneracy died with it.
   - *The two-tense left pane* — verdict + field on the clock, forecast off it; position groups
     deleted from both frames. See the UI section.
   - *Price chips* — every candidate speaks in picks-vs-price, the currency of "reach".
   - *Market-dissent rows* — the rice case; the board shows value-vs-market disagreements
     instead of silently taking the value curve's side.
   - *Banner re-key* — runs need to beat the market's own window forecast (`RunSurprise`), and
     both banner kinds fire only on top-two-choice positions. Round-1 noise (69% of windows by
     chance) is gone.
   - *`CostOfPassing` left the screen* — engine keeps it; the board stopped ranking on it.

   **Live-sim shakedown (2026-08-03, rounds 3-7 of the same mock, real polling).** The user
   drafted while a watcher captured board states via `-replay` (Poll uses GetPicksFresh, so
   headless polling sees every pick within seconds — the harness is a 4s loop diffing
   "replayed N picks"). Findings:

   - *The user overrode the verdict twice in three picks, both times toward information the
     board itself surfaced*: took rice over the nabers verdict (the market row), took lamar
     over the henderson verdict (the fell-17 chip), agreed on waddle. By design the verdict
     argues the value curve and the human picks the side — but if this pattern holds, the
     candidate change is letting a FALLING market pick win thin verdicts. Watch, don't build.
   - *Banner discipline held*: rounds 4-5 were wall-to-wall 4-of-6 wr windows and the run
     banner correctly stayed silent (base rate); both cliff banners seen were about top-two
     positions. Zero desyncs over 74 live-applied picks.
   - *The turn's all-100% survival column is accepted as-is* — the user's own read: make the
     first pick, the next frame carries the real numbers. Do not build the pair-horizon change.
   - *One real bug found and fixed: cliff claims under file-vs-value inversion.* Dynatyze
     tiers tate/adams above waddle; fantasycalc values waddle higher; waddle led the position
     wearing tier 8 and the board printed "last one in tier 8 — take him or lose the tier"
     over two available tier-6 men. Cliff/TierHold now speak about the BEST REMAINING BAND
     (lowest tier number still populated — `engine.bestTier`), which coincides with bestNow's
     tier whenever the orderings agree. The tension itself is deliberately unresolved: value
     orders the rows, the file owns the tier claims, and an inverted frame shows both opinions
     ("waddle 3% · 2 in tier 6"). A best man wearing a higher tier number than the count
     clause is the board saying your file and the value curve disagree about him — worth a
     second look at the CSV row when loading a new rankings file.

   The user's dynatyze tiers were loaded (`fetch -rankings tiers-2026.csv`, since superseded by smyth — 111 applied,
   100% matched; the 21-man wr tier 2 subdivided to board tiers 2-6). Note for future
   sessions: the user runs bare `pick6` resolved from `~/bin` — rebuild that binary
   (`go build -o ~/bin/pick6 ./cmd/pick6`) at the end of every build pass, or they will be
   staring at a stale board.
6. **Engine v2: opponent-aware Monte Carlo survival — SHIPPED (2026-08-11), sim is the board's
   default.** The spec (rewritten at milestone start) and every fold number live in
   `docs/engine-v2.md`; the short version:

   - *The sim replaces the survival number AND the tilt* — a rollout removes exactly the
     window's picks, so the budget the tilt balances is balanced by construction. One
     chokepoint (`engine.survivalAt`) feeds all four consumers; `-survival=adp` is the v1
     fallback on mock/board/live, tilt intact.
   - *The off-board escape is the load-bearing correction*: measured per rounds-remaining from
     the cached drafts against their own era boards (`fetch` writes `escape.json`; draft-time
     commands load it disk-only; replay holds out the replayed draft and its future, same as
     the room curve). Without it the sim loses to v1 everywhere — every simulated pick would
     eat a ranked player, and real rooms spend ~56-72% of final-two-round picks off-board.
   - *The verdict, from `calibrate`'s v2 gate (a report, not a gate — sim ships by decision)*:
     with the escape, sim beats shipped v1 on BOTH metrics on BOTH folds whose curve and
     escape predate them (2025-a 0.0711/0.3075 → 0.0701/0.2495; 2025-b 0.0774/0.3006 →
     0.0766/0.2775). 2024 — no priors, so no curve and no escape — still prefers v1, a
     configuration live 2026 is never in. WR and K improve on both metrics on both causal
     folds; RB/TE still bleed log-loss (need-model tuning, v2.1).
   - *The first scoring run was leakier than it looked and its numbers are retracted* —
     drafted-but-unranked players sat in the backtest pool before the picks that register
     them. The leak was an accidental future-informed off-board escape, which is what pointed
     at the real one.
   - *The deny chip* (`engine.Deny`): my pick only, the seat after me, their hungriest
     position by the opponent machinery, last man of its best band, and only when my own need
     is flex-or-less. Reverse-video dim chip on his row; never moves the verdict.
   - *`OpponentKDefLastRounds` moved 6 → 7*: "first kicker in round 10" was read off a
     15-round draft, and the user's own 16-round room took two kickers at remaining = 7.
     Found by the phase-A adversarial review; the 6-vs-7 backtest A/B is a wash, the fact wins.
7. **Multi-pick lookahead: the plan stops being a formula — SHIPPED (2026-08-11).** Spec was
   `docs/milestone-7.md`; the write-up is §"Two-pick lookahead" of `docs/pick6-engine.tex`.

   `PickChoices`' second leg used to be `EBest(Q, q2) - R(Q)` — an expectation over
   independent survivals computed off *today's* board. Under sim mode it is now what the
   rollouts actually deliver: leg one removes the candidate, opponents draft through the
   same v2 machinery (`sim.go`'s `rollout` refactored into a shared `simCore`, so the two
   kinds of rollout cannot drift), and at `q2` the engine's own greedy rule —
   `argmax VOR x NeedAfter` over whoever is really left — takes a player. The pair is
   scored on him, averaged over `PlanRollouts` futures. `internal/engine/lookahead.go`.

   - *The score is conditional on getting him.* The candidate comes off the board at the
     START of each rollout, not at my leg-one pick: in futures where an opponent takes him
     first the plan's premise has already failed, and the opponents who didn't take him
     spent their picks elsewhere. It is also the semantics v1 always had (leg one priced at
     `v(bestNow)`, leg two's EBest already excluding him). **Leg one's optimism is
     untouched** — milestone 7 made leg *two* real, and the honest simplification in the
     PickChoices docstring still stands as written.
   - *Common random numbers, one seed per vantage.* At 500 futures, independent sampling
     noise is worth a few hundred value points — the scale of the gaps that decide these
     rankings — so the board would reorder itself between renders on nothing. Measured
     paired variance is under half the unpaired.
   - **`-survival=adp` keeps the v1 formula.** Rollouts exist where the sim does; one
     switch, same chokepoint philosophy as `survivalAt`. Every pre-existing engine test
     runs in adp mode and is unaffected, including `TestBestPlanIsTheTopPickChoice` and
     `TestPickChoicesOnMyLastPick`, which pass unmodified.
   - *The survival path is untouched and that was verified, not assumed*: three `-data`
     frames byte-identical against the base commit. A lookahead that moved a number
     `calibrate` scores would be wired backwards.
   - *Feasibility in the leg-2 policy is a PREFERENCE, not a filter.* Written as a filter
     it dead-ended at 12.09 of the scripted mock — only k and def unfilled, three picks
     left, our own k/def suppression forbidding the only positions that could satisfy it —
     and the plan line printed a hole where the position goes. Sorting slot-closers ahead
     of the rest is identical whenever one is alive and degrades to best-available when
     none is. Suppressed positions stay excluded outright: a rollout must not "actually do"
     what the tool would never recommend.
   - *And the leg-2 policy may only name a position that passed the SAME membership test the
     first leg's candidates passed* — **found by the adversarial review, not by a test**. The
     v1 formula got this for free by maxing over `cands`; the rollouts had to be told. The gap
     is the endgame guard: membership is decided by `Need`, which multiplies bench weight by
     `endgameSlack` and takes it to **zero at R == U**, while the policy prices need with
     `needFrom`, which carries no slack (deliberately — see `NeedAfter`). So the plan line read
     `rb at 13.09 → wr at 14.04` directly above `every remaining pick must fill a starter`, with
     wr not a rendered row at all. Wrong ACTION as much as wrong label: with rb, k and def open
     and three picks left you take an rb, a k and a def. Blast radius measured at 7 of 116
     scripted frames, **none before pick 120**, which is exactly the endgame footprint the bug
     predicts.
   - *UI*: one clause on the plan line, both tenses —
     `plan wr at 3.03 → rb at 4.10 · lands tier-3 rb 100%`. An **outcome** claim (how often
     leg two lands the band the plan counts on), which is why it earns the place
     `CostOfPassing` was taken off. "That tier or better", since landing better is good
     news. Drops whole below 92 columns, before the plan does. The data tab's strip keeps
     the bare plan — it is width-starved.
   - **What the referee can't say.** A decision score has no counterfactual labels;
     `calibrate` cannot grade it and this is not pretended otherwise. The evidence is a
     scenario (`internal/engine/dod_test.go`): the market says the backs are going and the
     receivers will keep, while all six seats picking in between are full at running back
     with both receiver slots open. Greedy plans rb→wr, coming back for two men the room is
     about to eat; conditioned flips to wr→rb. Take the man you cannot get back.
     **Attribution is pinned rather than asserted**: two things differ between those runs
     (the survival model AND the second leg) and on that board the survival model carries
     most of the flip. The lookahead's separable contribution is `E[max] >= max E` — leg
     two is the best man across ALL positions in each future where the formula takes the
     best of the per-position expectations — worth +50 and +40 there. A conditioning-leak
     check runs alongside: worst move 0.064 against the unconditioned sim row.
   - **The third leg is RETIRED (2026-08-14, by milestone 8)**: the horizon became all of
     my picks, the branch and `TestConditionedPlanAtDepthThree` were deleted with the
     rewrite, and `PlanDepth` survives only as a comment nothing reads. Reviving the
     question means rebuilding it. What it was, recorded because it is now the only
     evidence of what a third leg did — **built, tested and OFF (`PlanDepth = 2`).** It clears the spec's
     stated bar and then some: over 54 scripted wheel frames (slots 1 and 12) depth 3
     changes the FIRST leg on 16, and every flip moves away from running back — coherent
     with the wheel's arithmetic, where two back-to-back picks let you double-tap the deep
     position later. Coherent is not right, and this repo does not ship a change that size
     on plausibility (see the room warp's cap). Promotion needs its own scenario fixture,
     and two confounds separated first: **`mustFill` is computed FROM the leg count**, so
     depth 3 carries an extra unit of endgame feasibility pressure; and it costs 115ms per
     pick event against 36ms, past budget, so promotion means dropping `PlanRollouts` too.
   - *Cost*: 36ms per pick event on a 200-player board (budget ~50ms), cached on
     `State.plan` keyed to the vantage and nil'd by `State.invalidate()` alongside
     `State.sim` — one method, because forgetting the second assignment means a board
     quoting last pick's plan beside this pick's survivals.
8. **Roster-score decisions: the objective stops being a formula — BUILT, GRADED, AND OFF
   (2026-08-14).** The whole milestone shipped except the one thing it was for: the new
   scorer did not clear its own gate, so `-scorer pair` (milestone 7's) is still the
   default and `-scorer roster` turns the new one on. Spec was `docs/milestone-8.md`;
   the write-up is §"The finished roster" of `docs/pick6-engine.tex`.

   `PickChoices`' score was a hand-built approximation of final-roster value: the need
   steps (1.0/0.6/0.25), the replacement discount, `EndgameSlack`, `mustFill` and the
   two-leg horizon all answered "what does this pick do to my final team" without
   simulating. Milestone 8 simulates. Each candidate is scored by the mean value of the
   **finished roster** over conditioned rollouts that run to my LAST pick —
   `U = Σ starters + BenchWeight × bench`, unfilled slot worth 0 (`engine.RosterValue`,
   `internal/engine/roster.go`). `NeedFlex`, `EndgameSlack`, R-in-the-score and
   `PlanDepth` all dissolve; the wheel is priced by construction.

   - **The gate, and it is NOT met.** `pick6 regret`, 80 paired seeds at the shipped
     `PlanRollouts = 500`, after the adversarial review fixed four bugs in the referee
     itself: the roster scorer came out **+181 (se 1516) on 2025-a and −729
     (deterministic) on 2025-b**, the two folds whose curve and escape predate them, and
     **+41889 (se 2155) on 2024**, which has no priors and does not gate. So one causal
     fold is a tie inside its own error bar and the other a 0.08% loss with no error bar
     at all — the honest word for the pair is *indistinguishable*, not *worse*. The gate
     asks for better on both, and this repo does not ship a decision-shaped change on
     plausibility. Same treatment m7's third leg got, for the same reason. **What would
     promote it**: a causal fold it wins, or the fix below.
   - **The strongest live hypothesis for why it does not win**: the continuation policy
     inside the rollouts still picks by `VOR × need` while the score is U. A rollout that
     models my later picks with a different objective from the one being maximised
     under-estimates every candidate, and not necessarily by the same amount. Making the
     leg policy U-greedy (ΔU is O(1) given the current assignment) is the obvious next
     move and was not attempted.
   - **`pick6 regret`, the referee — this is the durable part.** `calibrate` grades
     survival, which has labels; a decision has none. So: replay each cached real draft
     with my seat played by a policy while the other eleven keep their real picks; when
     my counterfactual takes a man a later real pick names, that manager takes **their
     own next surviving real pick**, and every substitution is counted and printed.
     Policies: what I really did · best-available-by-era-adp · fill-the-lineup · the v1
     formula · m7's pair score · m8's roster score. Scored two ways, never averaged: U on
     era-adp-rank values, and the same lineup on a **linear** exchange rate, so a win
     that comes from the value curve's convexity shows as a disagreement between the two
     columns. Causality is `calibrate`'s and it is a **package deal — curve, escape AND
     demand** all off the drafts that started earlier (`leagueDemand()` pools all three
     and would leak; regret builds `adp.PositionDemand` from the allowed list only).
     2024 has no priors, so it reports and does not gate.
   - **Three things the harness cannot referee, all printed on every run.** Its value
     curve is `ValueBase·exp(−rank/ValueDecay)` off era adp — position-blind, because no
     historical value source exists — so "best available" is nearly a greedy optimum of
     the exchange rate itself and a policy beats it only through lineup structure. The
     opponents cannot react. And **the `actual` row is not a fair baseline**: 7 of the
     user's real picks across the three folds were never on their era board and score 0,
     while a model policy cannot take an unpriced man at all.
   - **The adversarial review caught four bugs in the REFEREE and two in the shipped
     path**, which is the pass earning its keep for the third engine milestone running.
     In regret: `linearValue` paid a flat `BenchWeight` where U pays the two-index one,
     so the exchange-rate check varied two things; my own unranked real picks were
     registered into the pool *before* the policy chose, which is a leak of the exact
     kind that retracted v2's first scoring run; opponents were attributed by
     `DraftSlot` rather than `OwnerSlot` (26 of the 552 cached picks are traded, though
     **none of my own seat's**, asserted rather than assumed); and a one-seed run printed
     a standard error of 0 and called the win decisive. In the engine, two milestone-8
     rules had leaked onto the SHIPPED pair path — the leg-round k/def re-admission and
     the bench-weight pricing — so `planPolicy` now takes a `roster` flag and milestone
     7's leg is milestone 7's leg again. Every gate number above is post-fix; the
     pre-fix runs are not comparable and are not quoted.
   - **A real bug the build found, independent of the gate: U was paying for a backup
     quarterback.** `BenchWeight` now carries vor's own two-index rule — a bench body is
     worth `BenchWeight` only if his position can reach a lineup through a flex slot, and
     0 otherwise (QB in 1QB, K, DEF). Without it the board recommended a *second* QB for
     a quarter of a value the market inflates for scarcity, which is the ~930-point
     subsidy of the VOR section in a new place. `planPolicy` prices the same way, or the
     rollouts spend simulated picks on men the objective scores at zero. Superflex flips
     it by itself, because the rule reads the roster.
   - **Cost.** The old `legPolicy`, now `planPolicy`, sorted the whole board once per leg, which is what made
     depth 3 cost 115ms; it is now a walk down one static per-position list with a
     per-rollout cursor, and the identity is provable (the winner of the six-way compare
     is the winner of the sort, ties included). Two more hot-path fixes went with it —
     `fillSlots` split into `State.assign` over caller-owned buffers (one map lookup per
     player instead of one per slot), and `simCore.head`, a forward-only cursor past the
     dead. **Measured on an M2, cold cache, 208-player board**: the shipped pair score
     costs 17/34/47ms in rounds 1/8/15; the roster objective 366/226/41ms. The survival
     table's own recompute fell from 9.8ms to 6.1ms as a side effect, with byte-identical
     output. `PlanRollouts` stays at **500** — the authorised drop to 300 was not taken,
     because the shipped path is m7's and leaving it at the count it was measured at is
     worth more than the milliseconds.
   - **The survival path is untouched and that was verified, not assumed**: across **280
     scripted frames** (7 seeds × 5 slots × 8 vantages) the verdict, the ranked rows, the
     banner and the plan's POSITIONS are identical to 2ee2408, and the `-data` frames match
     to the last survival percentage. Two deliberate diffs on the default board, both
     stated so nobody hunts them: the plan line drops its prepositions (`plan te 10.10 →
     rb 11.03`), which is what makes a third leg fit at 100 columns at all; and the
     endgame frame gains the roster block, because there a two-pick window IS the whole
     remaining horizon and the block is honest under either scorer.
   - **UI, all of it gated on `Board.fullHorizon`** — *did the plan run to my last pick*,
     which is true everywhere under `-scorer roster` and only in the last couple of rounds
     under the pair score — so the default frame is the milestone-7 frame: the plan line grows into a skeleton (2 legs
     at 92 columns, 3 at 100+, capped at `planLegsMax` = 4); **`your team from here`**
     fills the left pane's spare space with one row per open starting slot, capped at
     `outlookRows` (5) because nine of them is the roster pane again on the wrong side of
     the divider, and with any row whose modal filler is already mine dropped (that slot
     closed by reassignment, not by a pick) — the modal filler named above `planNameShare`
     (0.35) and spoken as a band below it, the pick he
     arrives at, and the odds; and the verdict gains a consequence clause naming a player
     (`taking rb instead costs you kittle`). That clause exists **because of** common
     random numbers: future *m* of the top choice and future *m* of the runner-up are the
     same world, so the diff of their ending rosters is the choice and nothing else.
     Rows whose modal filler was already on my roster are dropped — that slot closed by
     reassignment, not by a pick.
   - **The market's dissenting man is a scored candidate** under the roster scorer, not a
     note: both he and the value curve's pick get rollouts on the shared seed, and
     whichever leads to the better team takes the row and can take the verdict.
     `marketPick` moved from the ui to `engine.MarketPick` and `marketGapPicks` to
     `engine.MarketGapPicks`, because the threshold now decides what the rollouts spend
     futures on. Under `-scorer pair` he is the dissent row he has always been.
   - **Scenario fixtures** (`internal/engine/dod_test.go`), since a decision score has no
     labels: m7's disagreement board re-asserted; `E[max] ≥ max E` re-expressed in U units
     (both arms run as rollouts on the same seed and the same two-pick window, one free to
     choose leg two and one nailed to the position the formula would have committed to —
     free wins by 30 and 22); and **the wheel fixture the m7 entry asked for**, where the
     backs keep through a 22-pick drought and the tight ends do not: the pair score takes
     the back on raw value, the roster score takes the man it cannot get back.
   - **Paired variance at full horizon is 17% of unpaired** (m7 measured "under half"),
     which is what lets the consequence clause name a player at all.
   - **The human rejected it, and that is the strongest evidence in this entry.** Shown
     the roster scorer's recommendations on the 2026 board, the user's verdict was that
     reaching for a QB and a TE early is "objectively bad" and he does not want the
     scorer. That outranks the gate: a decision score has no labels, the harness has
     three blind spots it prints on every run, and a drafter who has played this league
     for years judging the output is a better instrument than any of it. **Do not
     promote this without changing his mind first.**
   - **And the reason is now concrete rather than a vibe.** `U` sums STARTER values, and
     the value curve is not replacement-normalised across positions — that is the whole
     reason `vor.go` exists and the whole reason its two-index rule was written. Measured
     by `pick6 mock -slot 6 -seed 5 -snapshot 29 -scorer roster` (on the clock at 3.06,
     2026-08-14 board): allen 6091 / nabers 5699 / hall 5004 / warren 3453, against
     replacement levels of qb 1017, te 314, wr 72, rb 0. So vor ranks nabers over allen by
     553 while U ranks allen over nabers by 341 — U's order is the RAW-VALUE order, and the
     simulation, which is supposed to recover the difference through "what would I get at
     this position later", recovers only about half of it. The bench-weight fix stopped U
     paying for a backup quarterback; **the starter is still overpriced**, and that is
     what pulls QB and TE forward. Position sequences across seeds 3/5/11 and slots
     3/6/12 show it plainly — one frame recommends a quarterback at five separate
     vantages.
   - *Left open*: nothing is planned. Promotion would need the starter-side pricing fixed
     (the leg policy maximising ΔU rather than vor × need is the same fix from the other
     end), a causal fold it wins, AND the user's agreement. Absent all three the roster
     scorer is a documented negative result that happens to still compile.
9. **FPL draft mode: a second sport behind the same engine — BUILT (2026-08-18).** Spec was
   `docs/fpl.md`, written before the code and wrong in six measurable places (recorded there).
   Three commits, f1/f2/f3, on branch `fpl`.

   The claim phase 2 was designed around — *the engine never asks where a pick came from* —
   held. What it did not cover is that the engine asked, in three places, what the positions
   were CALLED.

   - **Seven hardcoded position lists, in six files, in three different orders.** `plan.go`'s
     `planPositions`, `sim.go`'s `simPositions`, `ui/board.go`'s `positions`, `ui/data.go`'s
     `dataFilters`, `ui/field.go`'s ladder, `ui/notes.go`'s `mapTally`, `ui/style.go`'s
     `posColor`. Every one said some permutation of QB RB WR TE K DEF; every one compiles
     against an FPL board and renders NOTHING, because the only string the two sports share
     is "DEF" and it is the wrong DEF. `State.Positions()` derives all of them from the
     lineup, and `DisplayPositions()` is the ui's own reading order — verified to reproduce
     both old literals exactly, order included, which is what let f1 land bit-identical.
   - **Found on the way, and an NFL bug**: a lineup whose flex slot is the only route a
     position has into it derived its order by ranging a Go MAP. `board -lineup "qb flex flex
     k def"` is legal and produced three different orders in 200 derivations — and that order
     is `PickChoices`' documented tie-break, so the primary key was a coin flip between
     renders of the same frame.
   - **The quota is a legality rule, not a preference.** `Roster.Quota` zeroes need for a
     position filled to its quota. Weighting it zero is not enough in the sim: the
     zero-weight guard exists to rescue a pick whose whole candidate pool priced at nothing,
     and it rescued this one straight into drafting a sixth defender. The filter removes him
     from the candidate pool outright. Mutation-tested — flipping the guard off turns the
     test red.
   - **`subdivide()` abandoned every oversized tier when one of them was flat**, and it only
     ever ran on rankings-file blocks, never on derived ones. FPL midfielders derived to two
     tiers: one of 39 and one of **213**. A board with two tiers per position has no cliffs,
     no tier notes, no ladder and no "tier broke" clause. Both fixes are no-ops on the NFL
     board — measured: nothing there derives past `TierMaxSize`.
   - **Value is an int.** `ValueBase` 250 × exp(-rank/40) over a 560-deep board rounds a
     third of the pool to zero, which is the sentinel for "nobody ranked him" — worn by
     players FPL ranks. `fpl.ValueTop` is 10000, the order of magnitude the ui's widths and
     thresholds were drawn against, floored at 1. The tail past rank ~368 is flat and forms
     one large bottom tier per position; it is 200 ranks past anything a 150-pick draft
     reaches, and the alternative (a decay derived from pool depth) is an unmeasured
     invention. Left as is, deliberately.
   - **`draft/{id}/choices` takes the LEAGUE id, not the draft id.** `league/4250/details`
     names a draft 4512, and `draft/4512/choices` returns a real, populated feed — for a
     stranger's league 4512, which has its own draft 4790. It does not error and does not
     come back empty. It would poll somebody else's draft all night looking exactly like it
     was working.
   - **The choices feed's `player_first_name` is the MANAGER's name.** The drafted player is
     named nowhere in the feed, so `fpl.NewFeed` carries the whole bootstrap — not the
     draftable pool — to name him. One of league 2400's 105 picks was a man who has since
     left the premier league: filtered off the board correctly, and without the full roll he
     lands on a roster as "unknown player", fills no squad slot, and leaves a hole in a
     lineup that is actually complete.
   - **DEF's colour had to move, and only after looking.** Slate is what NFL gives a team
     defense *because* it is drafted last and thought about least — the least saturated hue
     in the palette, 12° of hue and 1.7:1 of luminance from the dim prose beside it. Right
     for a position rendered faint for twelve of sixteen rounds; unreadable for a third of
     the FPL pool that is never suppressed and is the top-ranked position on the opening
     frame. Cornflower (NFL's receiver blue, unemployed in FPL). Eyeballed through `freeze`,
     not reasoned — the numbers said slate was no worse than QB rose, and the picture said
     otherwise.
   - *Verified*: 105 real picks from a completed league (2400) replayed across all seven
     seats, zero snake desyncs, zero unknown players. NFL bit-identical across twelve `mock
     -snapshot` frames after every pass, plus the golden tables.
   - *Not built, and deliberately*: an FPL mock, FPL calibrate/regret/scout (no cached
     drafts, no era board, no room curve — the leagues are per-season with fresh ids), and
     `-survival=adp`, which is refused rather than quietly run: the v1 logistic divides a
     pick gap by a measured sigma and FPL publishes a rank order and no sigma anywhere.
   - **The adversarial pass found eighteen things, and three of them were nfl regressions**
     — which is the argument for the pass, since the twelve-frame bit-identity check passed
     through every one of them. `live -` hung at 100% cpu forever (go's flag package treats a
     bare dash as a terminating positional, so the id-anywhere loop never made progress); a
     league whose lineup omits K and DEF lost the sim's kicker hold entirely, because the
     derived buckets put every kicker in the "unknown position" bucket, which is priced at the
     bench weight and never consults `opponentNeed`; and the endgame line's first gate was
     `Bench == 0`, which is fpl's shape rather than fpl's reason and silenced a legal nfl case.
     The gate is now `rounds > starting slots` — *there was slack to run out*.
   - **The reach chip was firing on the board's own recommendation.** `12 before his price` is
     one axis twice on an adp board and nonsense on a rank one: 150 picks over 560 ranked
     players, spread by a quota that makes the room reach down for a second keeper long before
     rank order would, so the pick counter falls behind the rank scale from round eight.
     Measured on the scripted mock at 13.04 — the verdict recommended a keeper and captioned him
     `110 before his price`. The reach half is gone; the falling half stays, because "he is
     still here after 124 picks" IS true on a rank scale.
   - **The fpl edge ignores its own cache headers.** `league/{id}/details` answers
     `no-cache, no-store, must-revalidate` and varnish serves it `x-cache: HIT` at **age
     98211 — twenty-seven hours**. `draft_status` would never have flipped. The pick feed
     measured clean and gets a nonce anyway: which path varnish fronts is a config detail nobody
     publishes, and a board thirty seconds behind looks exactly like a room that is thinking.
   - **One finding was refuted by measuring it**, and the measurement is worth keeping: the
     claim was that the faller flag reads rank as picks and is "measurably false for gkp and
     def". Over league 2400's real 105 picks the fallers at picks 60/90/105 number 6/21/31 and
     **every one is a midfielder** — this room takes keepers and defenders at or ahead of their
     rank, so they never sit past rank+6. Left alone.
   - *Left open*: this room's fingerprint starts accruing now. **Cache `draft/4250/choices`
     after draft night** — it is next season's only prior draft. And the opponent model's need
     signal is binary under a quota (`NeedStarter` or 0, with no flex or bench tier to grade),
     which is a modelling gap with **no fold to grade a fix against** — so it stays written down
     rather than guessed at.
9b. **The post-draft pass: what fifteen rounds against a real room found — BUILT
   (2026-08-19).** The user drafted league 4250 on the 19th. It went well; two things came
   back.

   - **THE BUG, and the whole point of drafting with it: the board recommended a man who
     cannot play for months, for fifteen rounds.** Ekitiké carried `draft_rank` **19** on
     draft morning with `status: "i"`, `chance_of_playing_next_round: 0` and news reading
     *"Achilles injury - Unknown return date"*. Nothing downstream could argue — value is
     `RankValue(draft_rank)`, survival is the sim over the same ordering, and the `out` chip
     was **display only by design**, a rule inherited from the nfl side and written into three
     separate comments. The rule was right about VALUE and wrong about CANDIDACY, and the
     difference only shows up where the market does not reprice: adp moves an injured player
     within days, `draft_rank` does not move at all.
   - **`engine.Player.Sidelined` marks nothing down.** It takes a man off OUR board and leaves
     him on the room's, exactly the way `Taken` does. Two published facts, no invented number:
     he cannot play the next round (`status i`/`s`, or `chance == 0`) AND fpl either names a
     return past `fpl.SidelinedWeeks` (4) or says *"Unknown return date"* in its own news line.
     **42 of 592** on the 2026-08-19 pool, and the set is byte-identical at a six-week horizon
     — two weeks would add only a suspension and an ankle, so the constant is not load-bearing.
     Eight were inside the top hundred: Ekitiké 19, J.Timber 36, Kulusevski 54, Garner 60,
     Saliba 66, Minteh 75 (*back 28 Nov*, the only one dated), Xavi 89, Mitoma 96.
   - **The split is one predicate and that is the design.** `State.OffMyBoard` gates every
     *what could I take* read — `Available`, `bestTier`, `TierHold`, `TierRemaining`/`TierSize`,
     `Replacement`, the ui's ladder and likely-gone. Every *what will the room do* read is still
     spelled `s.Taken[id]` on its own: the sim pool, the tilt's removal budget, the run
     forecast. **Rooms really do draft injured men** — league 2400 took J.Timber at 6.03 — and
     a sim that could not would spend that pick on somebody else.
   - **The adversarial diff pass earned its keep again, on the one site that is neither.**
     `newPlanPolicy` ranks MY legs out of `core.pool`, which is shared with the opponents, so
     the plan's second leg could have landed on a torn achilles one row under a verdict that
     had just refused him. Same rule the leg-2 policy already applies to suppressed positions:
     *a rollout must not do what the tool would never recommend.* Caught before it shipped;
     nothing else in the diff survived the pass.
   - **He stays a player everywhere descriptive** — `s.Players`, the feed, the notes prose, the
     data tab with his chip. In the `/` overlay his row prints **`out — off our board`** in
     cliff red where his price and odds would go, because the overlay is where you go looking
     for a man the board stopped offering and *"why isn't he there"* is the only question being
     asked. `live` re-checks against the fresh bootstrap and it moves **both ways**: a squad
     that clears comes back on, a squad that tears leaves. Offline `board` has no bootstrap, so
     its flags are as old as the fetch.
   - **Nfl is byte-identical and deliberately so.** The mechanism is sport-agnostic, the
     derivation is not, and `Sidelined` is false on every sleeper board. Verified across the
     mock frames at heights 20/24/28 and the golden tables.
   - **The second thing: the roster pane was drawing the wrong one of two true shapes.** Under
     a quota the sidebar showed the LINEUP — eleven starting slots plus however many spilled —
     so bench rows appeared one at a time as picks fell into them, a fifteen-man squad rendered
     as eleven rows that slowly became fifteen, and **the places still to fill were never once
     on screen**. `engine.Roster.Squad()` is the other shape: one row per place you may own, in
     lineup order, `gkp gkp · def×5 · mid×5 · fwd×3`. **nil without a draft cap**, so every nfl
     board keeps the pane it had.
   - Nothing collapses and a drafted man never gives way — the sidebar's own priority already
     says the ticker is what yields. What did change: **the ticker section now drops WHOLE
     rather than orphaning its header**, which is what a 24-row frame started doing once the
     pane grew four rows. That tidied the nfl frame at height 20 as a side effect.
   - *Nine tests, each verified to fail without its fix* (`internal/engine/sidelined_test.go`,
     the fpl date-parse table, the fetch report). *Left open, found while shipping and not
     touched*: `printFPLReplacement` indexes at lineup × teams (10/30/20/10) while `demandAt`
     indexes at cap × teams (20/50/50/30) — a printout that re-derived the index, which is the
     exact drift `ReplacementIndex`'s own comment exists to prevent.

10. Investigate having a global store of data + allowing new users to upload their own, without for example my own league barging into it --> lowish priority since im focusing on my own draft, but a cool thought.
11. Using Claude to rate everyones draft on a letter grade scale
