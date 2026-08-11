# fpl draft mode (phase 2) — implementation spec

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
  `GKP / DEF / MID / FWD` (measured pool: 64 / 188 / 256 / 69).
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
   consistent across the pool, never mixed with points.
3. **The hard quota is modelled by `Bench: 0` plus one engine rule:** a roster with no
   bench has no bench weight — `needSlots` returns 0 where it would return `NeedBench`,
   because a sixth defender is an *illegal pick*, not a bench pick. This one line fixes
   need, the lookahead's second leg, the sim's opponents and the deny chip at once. NFL
   never sees it (every NFL roster has a bench).
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
9. **Tiers**: the generic rankings CSV loader already works — an FPL tier sheet with
   `name,position,team,tier` lights up the cliff machinery. Without one, tiers derive
   from the value curve. Position strings in the CSV must be GKP/DEF/MID/FWD.
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
5. **`state.go:606` `needSlots`** — the quota rule: when `s.Roster.Bench == 0`, return 0
   instead of `NeedBench`. Add a test that a quota-filled position reads need 0 through
   `Need`, `NeedAfter`, `opponentNeed` and `Deny`.

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
   (`mock.go:136`), zero Freshness (`mock.go:298`).
3. **`live -sport fpl <league_id>`** — poll choices every 3s; apply each choice through
   the existing `ApplyRemote` with `PickNo: index`, `Slot`/`OwnerSlot` from the seat
   mapping (no trades in FPL: owner == slot always); seat order derived from round 1's
   choices (slot n = n-th distinct entry in index order), `-slot` required before round 1
   completes if order is not published pre-draft (re-probe near draft day); register
   every element from the cached pool (no EnsurePlayer surprises — the pool is total);
   `-replay` renders one frame headlessly from a finished draft. Reuse the existing
   polling model (`ui.NewLiveModel`) — it is feed-agnostic except for construction.
4. **Data mapping** (bootstrap-static element → adp.Player / engine.Player):

```
web_name (or first+second when ambiguous)  -> Name        (lowercased at render, as ever)
element_type via element_types             -> Pos         GKP/DEF/MID/FWD
teams[team].short_name                     -> Team
draft_rank                                 -> ADP         (rank units; the price)
ValueBase * exp(-draft_rank/ValueDecay)    -> Value
0                                          -> Bye, Stdev, TimesDrafted, High, Low, ADPEff
status i/s/d mapped, u filtered            -> InjuryStatus/Status (chip vocabulary)
news, news_added (ms since epoch)          -> Status detail, NewsUpdated
id (int, stringified)                      -> ID          ("fpl:123" if collisions worry you — they
                                                           shouldn't; the pools never meet)
```

DoD: `pick6 fetch -sport fpl && pick6 board -sport fpl -snapshot 0` renders a correct
draftable board with real names; `pick6 live -sport fpl 2400 -replay -slot 1` replays the
captured real draft with **zero snake desyncs**; NFL suite untouched.

### f3 — display polish

Audit sites: position colors (`style.go:52` — decision 8), UI position lists derived
(`board.go:15`, `data.go:18` — same derivation as f1), data tab per-sport columns
(`data.go:183` — drop bye/spread/fmt-gap, relabel adp → rank; legend follows at
`data.go:213`), price-noun threading ("rank" not "adp" in priceClause `board.go:693`,
marketRow `board.go:770`, search rows `search.go:310`), quota-aware slot clauses
(`board.go:740` — "both X slots open" only when the roster has exactly two; a full quota
says "def full", never "bench depth"; the endgame line skips benchless rosters,
`board.go:1006`), `tiers` position order derived from the loaded pool (`tiers.go:45`) and
its faint keyed properly (`tiers.go:75`).

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
