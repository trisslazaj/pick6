# fpl draft mode (phase 2) — implementation spec

> **BUILT 2026-08-18.** Everything below is the spec as it was written, kept because the
> reasoning is still the reasoning. What it got WRONG is corrected inline, marked
> **`[MEASURED]`**; what it missed is in milestone 9 of `docs/milestones.md`, and the shipped
> shape is the fpl section of `CLAUDE.md`. Read those two first — this file is the argument,
> they are the record.
>
> Six of its claims did not survive contact:
>
> 1. **The pool is 592, not 577** (2026-08-18), and the split is gkp 66 / def 195 / mid 261 /
>    fwd 70 before filtering, 60 / 184 / 252 / 64 after. Status counts moved too: a 481 /
>    i 48 / u 32 / d 28 / s 3.
> 2. **`news_added` is RFC3339**, as this file's own api section says — the data-mapping table
>    says "ms since epoch", and read that way every injury dates to 1970.
> 3. **`draft/{id}/choices` takes the LEAGUE id.** This file says so; what it does not say is
>    that passing the DRAFT id returns a real populated feed for a stranger's league with that
>    number, which is the trap.
> 4. **The choices feed's `player_first_name` is the MANAGER's name.** This file lists the
>    field as "carried, unused" without saying whose name it is, and the drafted player is
>    named nowhere in the feed at all.
> 5. **room.go's DEF collision does not exist** (decision 9's first sentence). `grep '"DEF"'
>    internal/adp/room.go` returns nothing. The three real collision sites are aggregate.go's
>    `isKDef`, match.go's `NewIndex`/`LookupExact`, and the curve keyspace.
> 6. **`take()` contains no fixed array** (f1.6's "the local `repl` in `take`"). `repl` is a
>    local in `newPlanPolicy`; `take` has a loop bound and an index.
>
> And three things it missed entirely, each of which would have shipped silently wrong: the
> value curve underflowing to the unranked sentinel, `subdivide()` never running on derived
> tier blocks, and `rankings.Index`'s Levenshtein fallback being live on the fpl tier path.

The second dataset behind the same engine. FPL Draft is a snake draft over a hard-quota
squad, and the whole point of phase 2's design — "the engine never asks where a pick came
from" — is that almost everything already works. This file is a complete hand-off: a fresh
session should be able to build all of it from here plus the codebase, without the
conversation that produced it.

**State of the repo when this was written (2026-08-11):** main at v0.2.0, milestone 6
shipped (engine v2 sim is the board's default; see docs/engine-v2.md), full suite green.
The user runs bare `pick6` from `~/bin` — rebuild it (`go build -o ~/bin/pick6
./cmd/pick6`) at the end of every build pass or they will be staring at a stale board.

**The deadline is real: the user's league drafts 2026-08-20T02:00:00Z** (aug 19, 10pm
eastern), 90-second pick clock, in person with the official app running the draft.

## the user's league, probed live 2026-08-11

League **4250**, 10 teams, h2h scoring, `draft_status: pre`; the user's entry is 17085.
All ten managers confirmed via `league/4250/details`. (League and manager names are
deliberately not in this file — this repo is public; they live in CLAUDE.md, which is
gitignored for exactly this reason.)

**Live sync is IN scope, and it needs no auth.** The original phase-2 plan assumed
cookie-authenticated league endpoints and settled for manual mode. Probed: once you know
the league id, `league/{id}/details` and `draft/{id}/choices` are **fully public**. A
bearer token (`x-api-authorization`, from the PL account SSO) is needed only for
`bootstrap-dynamic`, which lists the logged-in user's entries and leagues — that was used
once to discover league id 4250, and the credential was deleted. Draft night polls with no
token, no cookie, nothing that can expire mid-round. Do not build any auth plumbing.

**The five-year league history is (probably) unreachable.** FPL Draft leagues are
per-season — league 4250 and entries 170xx are this season's tiny sequential ids, and
`entry/17085/history` returns empty. Unless an old league id surfaces (an old email, a
bookmark), there is no FPL room curve and no per-manager history. The room's fingerprint
starts accruing this season: **cache this draft's choices to disk after draft night** so
next year has one prior draft.

## the api, probed live 2026-08-11

All endpoints under `https://draft.premierleague.com/api/`, all GET, all public unless
marked. These are observed shapes, not documented ones.

### `bootstrap-static` — the player pool

- **577 players** in `elements`; position via `element_type` 1–4 → `element_types` maps to
  `GKP / DEF / MID / FWD` (measured pool: 64 / 188 / 256 / 69). **`[MEASURED 2026-08-18]` 592
  now, 66 / 195 / 261 / 70, and 560 after dropping status `u`.** The pool moves; do not pin a
  number to it.
- **`draft_rank` is populated on all 577, runs 1..577 with no holes, and is FPL's own
  draft-mode ranking.** It is the ordering the entire engine wants, and it is *better*
  than a points sort: rank 5 is Isak at 41 points (injury-shortened season priced back
  up), which a points sort buries at ~150.
- `total_points` is last season's, and the curve is flat next to NFL's (top 239, 50th 134,
  120th 102) — one more reason value comes from rank, not points.
- Injury layer: `status` runs `a/i/d/s/u` (measured 514/35/14/3/11), with human-readable
  `news` ("Groin injury - Unknown return date") and `news_added` (RFC3339).
  `chance_of_playing_this_round`/`_next_round` also exist.
- `teams`: 20 entries with `short_name` (ARS, MCI...) for the team column and search.
- `settings.squad`: `size 15, select_GKP 2, select_DEF 5, select_MID 5, select_FWD 3` —
  the hard quota — plus play/min/max lineup rules the draft does not need.
- `events`: gameweeks (3 at probe time; the season had not started).

### `league/{id}/details` — the room

Keys: `league` (name, `draft_dt`, `draft_status` pre/post, `draft_pick_time_limit`,
scoring, trades), `league_entries` (per manager: `id` — the LEAGUE-entry id, `entry_id` —
the team id, `entry_name`, `player_first_name`, `short_name`), `matches`, `standings`.
Note the two ids: the choices feed's `entry` field matches `league_entries[].entry_id`.

### `draft/{id}/choices` — the pick feed (THE live endpoint)

Returns `{choices: [], idle, element_status}`. Empty `choices` pre-draft. Shape captured
from a real completed draft (league 2400, 7 teams × 15 rounds = 105 picks):

```
choice_time  RFC3339
element      player id (bootstrap-static elements[].id)
entry        team id making the pick
entry_name   display name
index        OVERALL PICK NUMBER, 1-indexed  <- the snake cross-check, = sleeper pick_no
round        1-indexed round
pick         1-indexed position within round
was_auto     bool — EXPLICIT autodraft flag (sleeper never gives this)
id, draft, league, player_first_name, player_last_name, seconds_to_pick — carried, unused
```

- Draft order is derivable from round 1's choices as they arrive. Whether it is published
  anywhere BEFORE pick 1 is unknown until FPL assigns this league's order — **re-probe
  `league/4250/details` a day or two before the draft** (look for a new field on
  league_entries). `-slot N` covers the gap exactly as it does for an unseeded Sleeper
  draft.
- League 2400's completed draft is the **replay fixture**: fetch it once, cache it, and
  drive the FPL live path against it headlessly, exactly how the Sleeper live path earned
  trust by replaying 552 real picks.
- `element-status` (also at `league/{id}/element-status`) carries per-player ownership —
  not needed for the draft board.

### polite-client rules

Same as Sleeper's: poll every 3s during a live draft, whole-list each time (no since
parameter; idempotent application self-heals dropped polls), stop when `draft_status`
flips to post. No CDN-nonce tricks needed until measured otherwise — check response
headers for cache-control during the live shakedown and add `?_=<nonce>` only if staleness
is observed (the Sleeper lesson: measure, then bypass).

## design decisions (settled — do not relitigate without new evidence)

1. **`draft_rank` is the price.** It lands in `Player.ADP` in rank units; `simPrice`
   orders the sim's desirability off it, which is all the sim needs (an ordering plus
   need). `-survival=adp` stays reachable but rank-units-as-pick-units is unblessed there
   — nobody has measured an FPL sigma. The sim is the brain.
2. **Value = `ValueBase * exp(-draft_rank / ValueDecay)`** — the existing convex fallback,
   consistent across the pool, never mixed with points. **`[MEASURED]` The shape is right and
   the BASE is not**: Player.Value is an `int`, and 250 × exp(-rank/40) over 560 players rounds
   everything past rank ~120 to single digits and past ~300 to ZERO, which is the sentinel for
   "nobody ranked him". Ships as `fpl.ValueTop` = 10000 — fantasycalc's order of magnitude, the
   one every ui width and threshold was drawn against — floored at 1.
3. **The hard quota is modelled by an explicit `Roster.Quota bool` (not by `Bench == 0`)
   plus the same engine rule** — corrected 2026-08-17: `Bench == 0` is not an FPL-only
   condition. `board -bench` defaults to 0 (`cmd/pick6/board.go:34`), so a plain NFL
   `board -lineup "qb rb rb wr wr te flex k def" -rounds 15` run with no `-bench` flag is
   a legal, currently-shippable Bench-0 roster, and reading the quota rule off `Bench == 0`
   would silently zero bench need for that user too. Give `Roster.Quota` its own bit, set
   by the FPL default roster (decision 2's board.go site), and have `needSlots` key off
   `Quota` rather than `Bench`. Fold in: `-bench` should default to
   `DefaultRoster.Bench` (6) when `-lineup` is given and `-bench` itself is omitted, so an
   NFL user who names a lineup without also remembering `-bench 6` doesn't fall into the
   same Bench-0 state by accident. **A Bench-0 roster whose rounds equal its slots — FPL's
   15/15, or the NFL `-lineup` case above with `-rounds` omitted (rounds then default to
   len(slots)+bench) — is `R == U` from pick 1** (my remaining picks always equal my
   unfilled starters when there is no bench; with an explicit `-rounds` larger than the
   lineup, R > U and the guard behaves as today), so `MustFillStarters` (`state.go:700`)
   reads true every pick of the whole draft,
   `EndgameSlack` can never fire (there is no "one spare pick" state to reach), and the
   ui's `every remaining pick must fill a starter` line (`board.go:1424-1427`) would render
   on every single frame instead of only the endgame. Gate that line — and treat
   `MustFillStarters` as alert-worthy at all — on the roster actually having a bench
   (`Roster.Bench > 0`), not on the arithmetic alone; a benchless roster is a normal state
   for it, not an emergency one.
4. **No suppression in FPL.** The K/DEF hold becomes a configurable set (default `{K,DEF}`
   with the existing gates — NFL bit-identical); the FPL roster sets it empty. Same for
   the opponents' gate inside the sim.
5. **Positions are config, not constants.** `planPositions`, `simPositions` and the UI's
   position lists derive from the roster's distinct dedicated slots in lineup order. The
   NFL default roster derives to exactly the old list in the old order — every NFL
   tie-break, weight and rng draw unchanged. That is the invariant the port hangs on, and
   the existing suite enforces it.
6. **NFL-only machinery gates off, never generalizes**: no room warp (`-room` forced off —
   NFL "DEF" room rows would collide with FPL defenders), no escape rates, no `Demand`
   (lineup-shape fallback is exactly right under a hard quota), no byes (Bye stays 0,
   every bye path self-gates), no calibrate, no scout.
7. **Own cache files**: `players_fpl.json` (and skip meta — a zero Freshness renders as
   nothing, which beats claiming the NFL board's age). An FPL fetch must never clobber the
   NFL board eight days before the NFL draft.
8. **Colors**: GKP takes violet (K's hue, unemployed in FPL), DEF keeps slate, MID takes
   mint, FWD takes rose. The NFL palette's match-the-platform principle would want the
   FPL app's own hues eyeballed against rendered frames; do that only if these grate.
9. **Tiers**: the generic rankings CSV loader is CLOSE to working, not already working —
   corrected 2026-08-17. Decision 6's DEF string collision is caught by `room.go` alone;
   two more sites test `pos == "DEF"` as a bare string and don't know FPL has its own
   defenders:
   - `internal/adp/aggregate.go` — `isKDef` (:418) is `pos == "K" || pos == "DEF"`.
     `AssignTiers` (:435-441) zeroes the tier of every player `isKDef` matches, and
     `AnchorKDefValues` rewrites every such player's value onto the borrowed skill-player
     curve (decision 6 already says FPL's 188 defenders must skip this — they're real,
     valued, drafted players, not a K/DEF anchor case). `ApplyRankings` (:597-635) calls
     both **unconditionally** after applying a file's tiers (:630, :634), so an FPL tier
     sheet's DEF rows are set by the loop and wiped by the next two statements in the same
     function call — a fpl tier CSV would load, appear to work, and then silently lose
     every defender's tier and value.
   - `internal/rankings/match.go` — `NewIndex` (~:112-117) routes every `pos == "DEF"`
     player into `byTeam`/`byDefName` only, never `byKey`, with no first-writer guard (the
     comment at :119 documents the guard for `byKey`; `byTeam`/`byDefName` have none) —
     last DEF writer for a team code wins. `LookupExact` (:158-167) then routes every
     `pos == "DEF"` lookup through those two maps and never `byKey`. Fine for the one NFL
     defense per team; under FPL's 188 real defenders (~9-10 per PL team) this collapses
     them to ~20 team-keyed entries and drops the rest.

   Fix: generalize the same way decision 5 generalizes positions — a "team defense" flag
   is a sport/quota switch like `Suppressed`, not an NFL constant, so key `isKDef` /
   `NewIndex` / `LookupExact` off it (empty for FPL, `{K,DEF}` for NFL) rather than the
   literal string. If that's more surgery than f1/f2 want to take on, the alternative is a
   non-Sleeper `rankings.Index` constructor (or a direct FPL-CSV join by name+pos, skipping
   `Index` entirely) — either is acceptable, but the current bare-string path is not: an
   FPL tier sheet with `name,position,team,tier` does NOT light up the cliff machinery as
   written today. Position strings in the CSV must be GKP/DEF/MID/FWD regardless of which
   fix is taken.
10. **Status mapping at fetch**: `i` → out, `s` → sus, `d` → dbt (reusing the existing
    chip vocabulary in ui/chips.go untouched); **`u` (left the league) is filtered out of
    the pool entirely** — an undraftable player is not a player.
11. **Two structural gifts worth knowing before debugging ghosts**: the off-board escape
    cannot exist in FPL (`draft_rank` covers all 577, every pick removes a ranked player
    by definition — the sim runs escape-less *correctly*), and the thin-board tilt regime
    cannot occur (577 ranks over 150 picks is a board ~4× deeper than the draft).

## the work, precisely

Three passes. Each pass ends with the full existing suite green — the generalizations must
be invisible to NFL by construction, not by hope. File:line references are from the
three-agent audit of 2026-08-11 (main at v0.2.0); line numbers will drift, the sites won't.
**Re-audited 2026-08-17** against milestone 8 (`planPolicy`, `roster.go`) and the board-tab
blocks that shipped after this file was written (`field.go`, `views.go`, `notes.go`'s map,
the data tab becoming views) — those sites are folded into the lists below, not called out
separately, because the rule is the same one f1.5/f2.2/f3 already state.

### f1 — engine generalization (internal/engine only)

1. **`plan.go:28` `planPositions`** — replace the package var with a derivation from the
   roster: distinct dedicated positions of `s.Roster.Slots` in lineup order, plus
   flex-eligibles for any flex slot. NFL default roster derives to exactly
   `QB,RB,WR,TE,K,DEF` in that order (verify with a test), so the tie-break order is
   unchanged. Consumers: PickChoices, Deny.
2. **`state.go` `Suppressed`** — the suppressed set becomes configurable (e.g.
   `Roster.NoEarly map[string]bool` or a State field), defaulting to `{K,DEF}` with the
   existing `KDefLastRounds` gate. FPL sets it empty. The UI's faint and `tiers`' faint
   read through the same call, so they follow for free.
3. **`sim.go` `opponentNeed`** — same configurable set on the opponents' side with
   `OpponentKDefLastRounds`. FPL empty.
4. **`sim.go:113` `simPositions`/`simPosIdx`** — build the position index from the
   roster's distinct dedicated slots at rollout start (slice, not fixed array; `needPow`
   sizes at runtime, unknown-position bucket last). NFL derivation yields the same six
   strings in the same order → byte-identical rollouts (the determinism test corpus
   already pins this).
5. **`state.go:636` `needSlots`** — the quota rule: when `s.Roster.Quota` is set (decision
   3 — the bit, not `Bench == 0`), return 0 instead of `NeedBench`. Add a test that a quota-filled position reads need 0 through
   `Need`, `NeedAfter`, `opponentNeed` and `Deny`.
6. **`lookahead.go:319` `const numPlanPos = len(simPositions)` and `newPlanPolicy` (~365-390)
   — milestone 8, postdates this file.** `numPlanPos` sizes `planPolicy`'s five fixed
   arrays (`rank`/`cursor`/`base`/`kdef`/`late`, plus the local `repl` in `take`; `vor`
   is pool-sized, not position-sized); once f1.4 turns `simPositions` into
   a derived slice this becomes a runtime size again, same as `needPow` in f1.4's sim.go
   site — the array literals must become slices sized in `newPlanPolicy`, not just the
   const. Separately, `pol.kdef[pi] = pos == "K" || pos == "DEF"` (`lookahead.go:388`) and
   `allows()`'s `s.Rounds-round+1 > KDefLastRounds` (`lookahead.go:476`) are an
   **independent, hardcoded copy** of the suppression rule — it is deliberately evaluated
   at the leg's own round rather than the vantage's (see the big comment above `allows`),
   so it cannot simply call `State.Suppressed`, but it must read the same configurable set
   f1.2 introduces rather than the literal `{K,DEF}`. Left as `{K,DEF}`, every FPL plan leg
   refuses all 188 defenders and 64 keepers until the last three rounds regardless of the
   roster's own (empty) suppressed set — the plan line and `your team from here` would go
   silent on GKP/DEF for the whole draft. Order matters here: f1.4's slice conversion
   breaks `numPlanPos` at compile time, which is the good kind of failure; the `kdef`
   literal does not — it compiles and is wrong quietly, so it needs its own line in the
   FPL checklist rather than riding along on f1.4's build error.
7. **`sim.go:337`** — the unknown-position bucket (`needPow[len(simPositions)]`) is priced
   at `powBench` directly rather than through `needSlots`, so it doesn't see f1.5's
   quota-zero rule. Under FPL this bucket should never actually price anyone (draft_rank
   covers all 577, so nothing lands in "unknown position" — see decision 11), which is why
   this is a note and not its own fix: f1.5's sim-side test can't exercise it either way
   until f1.4 lands (the bucket index depends on the derived `len(simPositions)`), so land
   f1.4 before writing f1.5's test.

DoD: entire existing suite passes untouched; new table-driven tests for each derivation
(NFL roster → old constants exactly; FPL quota roster → GKP/DEF/MID/FWD, no suppression,
quota-zero need).

### f2 — fetch, board, live (cmd/pick6 + internal/fpl client)

New package `internal/fpl`: a small client for the three endpoints (bootstrap-static,
league details, choices), disk-cached like `internal/sleeper`, stdlib http+json only.

1. **`fetch -sport fpl`** — bootstrap-static → filter `u` → map to the same
   `[]*adp.Player` shape the NFL fetch writes (see mapping below) → `players_fpl.json`.
   Prints the pool summary per position and the top of the rank board. No crosswalk, no
   room curve, no escape, no vor anchoring (every FPL player is ranked; nobody needs a
   borrowed value).
2. **`board -sport fpl`** — audit sites: sport-keyed `-lineup` vocabulary (`board.go:130`
   knownSlot gains GKP/DEF/MID/FWD, no flex; error text per sport), sport-keyed default
   roster (`board.go:55`: 2×GKP + 5×DEF + 5×MID + 3×FWD, Bench 0 — the rounds-follow-
   lineup rule then yields 15 by itself), skip `leagueDemand` (`board.go:78`), skip
   `loadEscape` (escape.go — set OffBoard nil, the note already says escape-less), force
   `-room=false` (`mock.go:177` — say so in a note), sport-keyed board file in `loadBoard`
   (`mock.go:136`), zero Freshness (`mock.go:298`). **Also sport-key `-teams`**
   (`board.go:30`, `mock.go:25` both default `12` — the league is 10, and every pick number
   in the snake math derives from `teams`; not on the list above until this pass because
   the default was never audited against decision 7's own sport). Either key the default
   off `-sport` or read team count from `league/{id}/details` and skip the flag entirely
   under FPL. **`pick6 tiers`** also needs a sport-keyed board file: it reads
   `players.json` by its own hardcoded path (`cmd/pick6/tiers.go:30-34`), not through
   `loadBoard`, so it's a second reader that would need the same `players_fpl.json` switch
   as `loadBoard` gets — add it to this list since f3's DoD assumes `tiers` works under
   FPL.
3. **`live -sport fpl <league_id>`** — poll choices every 3s; apply each choice through
   the existing `ApplyRemote` with `PickNo: index`, `Slot`/`OwnerSlot` from the seat
   mapping (no trades in FPL: owner == slot always); seat order derived from round 1's
   choices (slot n = n-th distinct entry in index order), `-slot` required before round 1
   completes if order is not published pre-draft (re-probe near draft day); register
   every element from the cached pool (no EnsurePlayer surprises — the pool is total);
   `-replay` renders one frame headlessly from a finished draft. Reuse the existing
   polling model (`ui.NewLiveModel`) — it is feed-agnostic except for construction, and
   this was actually verified 2026-08-17, not just claimed: `Poll()` is an interface
   (`internal/sleeper/feed.go:8`), `*sleeper.Draft` is nil-guarded inside the model (owner falls back to
   slot when nil, which is exactly right for FPL's owner==slot rule), `Status` is set by
   the adapter rather than read off a Sleeper type, and `ui/live_test.go:16-31`'s fake feed already drives
   the model off a bare `sleeper.Snapshot` with no Sleeper API behind it — an FPL feed is
   one more type returning that same `Snapshot`, nothing more. One thing this pass DID
   find: `cmd/pick6/live.go:105-121`'s `-replay` path hand-copies the same
   `EnsurePlayer`+`ApplyRemote` apply loop that `LiveModel.handlePoll` runs for the live
   TUI case — an FPL live command written the same way would be a *third* copy of that
   loop. Extract one apply-snapshot helper (`applySnapshot(s, snap) error`, say) before
   adding FPL's, so the loop that made the Sleeper zero-desync claim is the loop FPL's
   claim rests on too.
4. **Data mapping** (bootstrap-static element → adp.Player / engine.Player):

```
web_name (or first+second when ambiguous)  -> Name        (lowercased at render, as ever)
element_type via element_types             -> Pos         GKP/DEF/MID/FWD
teams[team].short_name                     -> Team
draft_rank                                 -> ADP         (rank units; the price)
ValueBase * exp(-draft_rank/ValueDecay)    -> Value
0                                          -> Bye, Stdev, TimesDrafted, High, Low, ADPEff
status i/s/d mapped, u filtered            -> InjuryStatus (chip vocabulary; Status stays "")
news_added — RFC3339, NOT epoch millis     -> NewsUpdated  [MEASURED: the sleeper dump's
                                              news_updated is millis; this one is a timestamp
                                              string, and 468 of 592 are null]
id (int, stringified)                      -> ID          ("fpl:123" if collisions worry you — they
                                                           shouldn't; the pools never meet)
```

5. **The rankings views are NFL-pathed and would misrepresent an FPL board.**
   `rankingsDir()` (`cmd/pick6/notes.go:40-46`) resolves `-rankings` to
   `~/.config/pick6/rankings` regardless of `-sport`, and `fetchedRankings()`
   (`cmd/pick6/notes.go:49-59`) reads the file `fetch -rankings` recorded in the *NFL*
   `meta.json`'s `TiersFile`. Left alone, an FPL board's data tab would list every one of
   the user's NFL rankings csvs as a view — each rendering nothing but `?` rows, since
   f1/f2's DEF-collision fix aside, the names simply aren't FPL players. Sport-key the
   folder (e.g. `~/.config/pick6/rankings/fpl/`) and skip `fetchedRankings()` for FPL
   entirely, consistent with decision 7 already saying FPL skips `meta.json` outright.

DoD: `pick6 fetch -sport fpl && pick6 board -sport fpl -snapshot 0` renders a correct
draftable board with real names; `pick6 live -sport fpl 2400 -replay -slot 1` replays the
captured real draft with **zero snake desyncs**; NFL suite untouched.

### f3 — display polish

Audit sites, line numbers checked against the tree as of 2026-08-17 (`priceClause` and
`slotClause` had already drifted from this file's original 2026-08-11 numbers, which is
the drift the top-of-section note above is about):

Position colors (`style.go:52` — decision 8), UI position lists derived (`board.go:15`,
`data.go:18` — same derivation as f1), data tab per-sport columns (drop bye/spread/fmt-gap,
relabel adp → rank; column head at `data.go:196`, legend at `data.go:289`), price-noun
threading ("rank" not "adp" in priceClause `board.go:795`, marketRow `board.go:1093`,
search rows `search.go:309`), quota-aware slot clauses (`slotClause`, `board.go:842` — this
is already count-aware today, `open >= 2` / `open == 1` / flex / bench, so f3 only needs to
add FPL's fourth case: a position at full quota, where `Need(pos)` is 0 and the current
`default: "bench depth"` branch would print a lie — say `"pos full"` instead when
`b.State.Need(pos) == 0` and no slot is open; the endgame line's benchless-roster skip is
`board.go:1424` (`b.State.MustFillStarters()`), see decision 3's amendment above for why
that gate must key on `Roster.Bench > 0`, not on the arithmetic alone), `tiers` position
order derived from the loaded pool (`tiers.go:45`) and its faint keyed properly
(`tiers.go:75`).

Four more sites, all shipped after this file was written and missed by its original audit:

- `internal/ui/field.go:174` — `tierLadder`'s `roomBlock`/tiers-picture block loops the
  literal `[]string{"QB", "RB", "WR", "TE"}`. Under FPL this isn't wrong so much as
  invisible: the block silently renders nothing (GKP/DEF/MID/FWD never match), and nobody
  building f3 off the original site list would know to look for it since it postdates the
  list.
- `internal/ui/notes.go` — three sites in the notes tab's draft map, all missed for the
  same reason. `mapTally` (:744) loops the same six-position NFL literal for the `gone qb
  4 · rb 6 ...` tally line (its own docstring, "in the board's position order", is already
  wrong today — there's no shared derivation backing it, corroborating decision 5's point
  that this needs to be one derivation, not N independent literals). `nameIndex` (:534)
  excludes `p.Pos == "DEF"` from last-name highlighting in note prose — correct today only
  because NFL's DEF entries have no surname to collide on (city/nickname instead); FPL's
  188 real defenders have real surnames and want the same last-name matching everyone else
  gets, so this exclusion needs to become "positions with no surname" rather than a literal
  `DEF` check. `posWords` (:564) is `qb/rb/wr/te/k/def` only — no `gkp`, `mid`, `fwd`
  aliases for a note-writer typing "he's a top gkp" the night before the draft.
- `internal/ui/views.go` — `viewStrip` labels the second view `"adp"` (:55; the data tab's
  per-sport relabel to "rank" above needs to reach this string too, not just the column
  head), `:193` iterates the package `positions` var, which is covered for free once f1's
  derivation lands and `board.go:15` is no longer a hand-written six-string literal — but
  say explicitly that GKP/MID/FWD would otherwise silently vanish from every view exactly
  the way QB/RB/WR/TE vanish from `field.go:174` above, since it's the same failure mode
  twice. `sheetIndex.find`'s DEF fallback (:343-345, `r.Pos == "DEF" && r.Team != ""`
  keying off `ix.byID[r.Team]`, the team code used as a player id) is NFL-only by
  construction and **inert, not broken, under FPL** — say so rather than flagging it, since
  it just never matches for a sport with no team-keyed DEF ids; a rankings-file row for an
  FPL defender resolves through the name/key paths above it instead.
- Two more price-noun sites the original list didn't name: the standing-selection line in
  the `/` overlay (`internal/ui/search.go:363`, the `id + tier + adp + surv` clause
  assembly — note its `" · adp %.1f"` literal at :307-309 too, same fix as priceClause) and
  the data tab's sort caption (`internal/ui/data.go:190`, `"all players — sorted by
  "+by` where `by` is literally `"value"`/`"adp"`).

DoD: rendered frames eyeballed (house rule — visual work does not ship on green tests
alone): board tab on/off clock, data tab, search overlay, a cliff banner, all with real
FPL names at 100 and 92 columns.

## deliberately unbuilt

FPL live *drafting* (we read the draft; the user picks in the official app), FPL
projections of any kind, blank-gameweek planning, waiver/season tooling — the tool is a
draft-night war room, same as NFL. Auth plumbing of any kind. An FPL mock mode (the
scripted picker would need a de-suppression pass — `mock.go:343` — and board -snapshot
covers the demo need; skip unless it falls out free).

## handoff notes

- Work on a branch (`fpl`), three commits mirroring f1/f2/f3, lowercase wry commit style
  per the log. Run the adversarial-review pass before merging (it has caught real bugs
  three times in this repo).
- The invariant to defend above all: **NFL output is bit-identical after f1**. The suite
  plus a `mock -snapshot 100` diff against main is the proof.
- Update CLAUDE.md's phase-2 section and the readme when it ships; the paper gets a short
  fpl note only if someone feels like it — it is an NFL paper.
- After the user's draft on the 19th: save `draft/4250/choices` to the cache — it is
  next season's room-curve seed.

## the room, measured 2026-08-19 (1,080 picks, 8 completed public drafts)

Cached at `fpl_choices_<id>.json` for leagues 100, 500, 1500, 2400, 4000, 4300, 4512, 5000.
The fpl analogue of CLAUDE.md's "our league" table, and the first thing anyone should read
before touching the opponent model's open bug.

**Share of each round's picks, pooled:**

```
round   GKP   DEF   MID   FWD      round   GKP   DEF   MID   FWD
   1     1%    6%   57%   36%         9     1%   56%   25%   18%
   2     0%   11%   54%   35%        10    11%   50%   24%   15%
   3     4%   12%   53%   31%        11    11%   57%   18%   14%
   4     8%   25%   47%   19%        12    15%   51%   18%   15%
   5    10%   22%   44%   24%        13    14%   51%   19%   15%
   6     6%   33%   46%   15%        14    28%   40%   17%   15%
   7    18%   24%   39%   19%        15    68%   19%    3%   10%
   8     4%   42%   36%   18%
```

**Fraction of the room's quota spent by the end of each round** (100% = every seat at its cap,
so this is normalized across the 7- and 10-manager leagues in the corpus):

```
round   GKP   DEF   MID   FWD      round   GKP   DEF   MID   FWD
   3     3%    6%   33%   34%         9    26%   45%   80%   73%
   5    12%   14%   52%   48%        11    37%   67%   89%   83%
   7    24%   26%   69%   60%        13    51%   88%   96%   92%
   8    26%   35%   76%   66%        14    66%   96%   99%   97%
```

First pick of each position, by round: MID r1 always, FWD r1 always, DEF earliest r1 /
median r2, **GKP earliest r1 / median r4 / latest r5** — the first keeper is a round-4 event
in every league in the corpus, not an endgame one.

**Three readings, and the second is the one that matters:**

1. **MID and FWD drain early and evenly** — half of everything the room ever takes at both is
   gone by round 5, with no concentration spike anywhere. There is no run to be early or late
   to; the depth is real and the board's pessimism about mid is the safe kind.
2. **The defender run is rounds 9–13** — DEF goes 45% → 88% of quota in five rounds and is the
   largest share of every round's picks from r8 on. This is a genuine cliff that no rank-based
   opponent model can see, because nothing about `draft_rank` changes at round 9; what changes
   is that ten seats simultaneously still owe three defenders each.
3. **The keeper cliff is r14–r15** — 51% spent through r13, then all of it. Combined with the
   median first keeper at r4, GKP is bimodal: one early starter, one endgame scramble for the
   bench body who can never play.

**This is direct corroboration of the open bug**, and it localizes it. The measured removal
ratios (GKP 0.42×, DEF 0.58×, MID 1.66×, FWD 1.08×) are an average over the whole draft; this
table says the DEF error is not spread evenly across it but concentrated in rounds 8–12, where
the model is still drafting by rank while the room has switched to draining a quota. A fix
weighting position by open slot count would move exactly these rounds, which makes them the
cells to grade it on — not the aggregate.


## injured players: `Sidelined`, built 2026-08-19 (post-draft)

**The spec said the injury layer is display only and nothing may ever price it. That was
right about VALUE and wrong about CANDIDACY, and the 2026 draft is the evidence.** Ekitiké
carried `draft_rank` 19 on draft morning with `status: "i"`, `chance_of_playing_next_round: 0`
and news reading "Achilles injury - Unknown return date". The board recommended him for
fifteen rounds. Nothing downstream could argue: value is `RankValue(draft_rank)`, survival is
the sim over the same ordering, and the `out` chip was display-only by design. The market that
prices this pool had simply not repriced him, and fpl publishes no second opinion.

`engine.Player.Sidelined` is the fix and it marks nothing down. He comes off **our** board the
way a drafted player does and stays on **the room's**.

**The rule, from two published facts and no invented number:**

```
cannot play next round   status i or s, or chance_of_playing_next_round == 0
AND
no return inside 4 weeks  news says "Unknown return date", or names a date past the horizon
                          ("Expected back 28 Nov", "Suspended until 30 Aug" — both parsed;
                           the string carries no year, so the season-forward year wins)
```

`fpl.SidelinedWeeks = 4`. On the 2026-08-19 pool that is **42 of 592**, and the set is
identical at six weeks — two weeks would add only a suspension and an ankle. The short half of
the `i` bucket is untouched: Baleba (back in 4 days), Andersen, Christie, Fofana, Gelhardt all
stay draftable, which is right — a four-day ankle at rank 279 is a fine late pick.

**Who it was hiding in plain sight, all inside the top hundred:** Ekitiké 19, J.Timber 36,
Kulusevski 54, Garner 60, Saliba 66, Minteh 75 (back 28 Nov), Xavi 89, Mitoma 96.

**The split is the design, and it is one predicate.** `State.OffMyBoard(id, p)` gates every
"what could I take" read; every "what will the room do" read stays spelled `s.Taken[id]`:

| off my board | still the room's |
|---|---|
| `Available` → bestNow, EBest, urgency, PickChoices, MarketPick, Deny | the sim pool (`newSimCore`) |
| `bestTier`, `TierHold`, `TierRemaining`, `TierSize` | the exactly-N tilt's removal budget |
| `Replacement` (I never settle for a man I cannot pick) | `expectedInWindow`, the run gate |
| the plan policy's own legs (`pol.rank`) — **found by the diff review** | `RoomForecast` / `expect` |
| the ui's `likely gone` and the tier ladder | |

The plan-policy row is the one that would have shipped broken: `newPlanPolicy` ranks out of
`core.pool`, which is shared with the opponents, so **my** second leg could have planned onto
a torn achilles one row under a verdict that had just refused him. Same rule the leg-2 policy
already applies to suppressed positions — a rollout must not do what the tool would never
recommend.

Rooms really do draft injured men: league 2400 took J.Timber at 6.03. A sim that could not
would spend that pick on somebody else, so the opponents keep the whole pool.

**He is still a player everywhere descriptive.** In `s.Players`, in the feed, in the notes
prose, on the data tab with his chip — and in the `/` overlay his row prints **`out — off our
board`** in cliff red where his price and odds would go, because the overlay is where you go
looking for a man the board stopped offering, and "why isn't he there" is the only question
being asked.

**`live` re-checks against the fresh bootstrap and it moves BOTH ways** — a squad that clears
comes back onto the board, a squad that tears leaves it — the same live re-check `Departed`
gets, and for the same reason: the board file froze this at fetch time and an achilles heals on
its own schedule. Offline `board -sport fpl` has no bootstrap, so its flags are as old as the
fetch. `fetch -sport fpl` names everyone it held off, deepest ten as a count.

**Nfl is deliberately untouched.** The mechanism is sport-agnostic and the derivation is not:
adp reprices an injured player within days and `draft_rank` does not, so `Sidelined` is false
on every sleeper board and the nfl frame is byte-identical.

## the roster pane under a quota

The sidebar drew the LINEUP — eleven starting slots plus however many spilled onto a bench —
which is the wrong shape to stare at while drafting a quota. Bench rows appeared one at a time
as picks fell into them, so a fifteen-man squad rendered as eleven rows that slowly became
fifteen and **never once showed the four places still to fill**.

`engine.Roster.Squad()` is the other true shape: one entry per place you may OWN, in lineup
order — `gkp gkp · def×5 · mid×5 · fwd×3` — and **nil without a draft cap**, so every nfl board
keeps the lineup-plus-bench pane it already had. A capped position the lineup never names still
gets its rows; map order never decides anything.

Nothing collapses and a drafted man never gives way. The sidebar's existing priority already
says the ticker is what yields on a short terminal — and the ticker section now **drops whole
rather than orphaning its header**, which is what a 24-row frame started doing once the pane
grew four rows. Which of the fifteen START is a separate question, and the `need` line
underneath still answers it off the same engine call the urgency weights read; two lineups on
one thirty-cell pane would just be the frame arguing with itself.
