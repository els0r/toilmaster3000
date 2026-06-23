# Batch the PR-State refresh into one search call

## Context

PR State (the live GitHub lifecycle of an already-approved PR — `open` / `merged`
/ `closed`) is refreshed out-of-band at the tail of **every** cycle, for today's
feed entries only. The original implementation did this with **one `gh pr view`
per PR** — a per-PR N+1 that CONTEXT.md explicitly flagged as "batching deferred".

At the default 5m cadence with a handful of daily approvals this was harmless.
But the find→approve loop is a **single cadence** (one goroutine, sleep-after),
and the refresh runs in a `defer` on every cycle. So as the poll interval is
pushed toward `MinPollInterval` (1m) to raise cycle frequency, the PR-State
refresh is the **only** cost that scales with *both* cadence and feed size:
the candidate list is one call/cycle regardless, and approves fire once per PR
ever (dedup-guarded). On a busy day at 1m that is a dozen-plus `gh pr view`
subprocess spawns *per minute* — subprocess startup (~hundreds of ms each) plus
GitHub secondary-rate-limit pressure, both linear in feed size.

The candidate set cannot supply this. Its search is `is:open
team-review-requested:…`; a PR that has *merged or closed* — exactly the
transition PR State exists to surface — is no longer `is:open`, so it has already
left the candidate population. The feed population ≠ the candidate population, so
the refresh needs its own call.

## Decision

Replace the per-PR `gh pr view` with **one batched `gh pr list`** scoped by
reviewer and recency:

```
gh pr list --repo example/repo --state all \
  --search "reviewed-by:@me updated:>=<startOfLocalDay, RFC3339>" \
  --json number,state,mergedAt --limit 200
```

The bot's approval **is** a GitHub review, and the bot *only ever approves*
(`gh pr review --approve`) — it never comments or requests changes — so for the
bot's identity `reviewed-by:@me` ≡ approved-by-me, with no false inclusions. The
`updated:>=` floor bounds the result: a today-approved PR's `updatedAt` is ≥ its
approval time ≥ local midnight (the approval itself is an update).

The seam gains **`PRStatesSince(ctx, since time.Time) (map[int]RawPRState,
error)`**, replacing `PRState(ctx, number)`. It stays **decode-only** — it returns
the raw `{state, mergedAt}` per number; `CollapsePRState` still judges the bucket
(unchanged). The seam owns the search-string formatting (it consumes only the
`since` floor); the engine owns *which numbers are today's feed* and **intersects
the result against them locally** — strangers from the over-fetch are simply not
looked up.

Failure model, collapsed from per-PR to all-or-nothing:

- **Whole-call failure** → keep *all* last-known states, log once, never abort the
  cycle (still in the `defer`). The same "keep last known, never abort" principle,
  applied wholesale.
- **A number absent from the result** (search-index lag, deleted PR) → **keep its
  last-known state; update-in-place, never clear.** The engine only *writes*
  entries present in the result. A never-yet-seen number stays absent → reads as
  `unknown` (the existing default).

## Considered Options

- **Decouple the refresh cadence from the cycle cadence** (refresh PR State every
  5m while find→approve runs at 1m). Rejected: it only *rations* a still-O(N)
  operation rather than fixing it, and it adds a second cadence to a loop whose
  single-cadence, sleep-after model is a deliberate invariant. Batching makes the
  refresh O(1) in subprocess count, cheap enough to keep running every cycle.
- **GraphQL aliased query** (`gh api graphql` with one `pullRequest(number:)`
  alias per feed PR). Rejected: it fetches *exactly* the feed numbers with no
  search-index lag — but it would be the **only GraphQL in the entire seam**
  (every other call is `gh pr <verb>`), a lone foreign idiom a future reader must
  decode. The lag it avoids is cosmetic and self-healing (see Consequences).
- **Fold PR State into the candidate `gh pr list`.** Rejected: impossible — merged
  and closed PRs are not `is:open`, so they are absent from the candidate search.
- **Keep one `gh pr view` per PR.** Rejected: this is the N+1 being removed; it
  does not survive a higher cadence.

## Consequences

- The refresh is now **one subprocess regardless of feed size**, so it stays in
  the every-cycle `defer` even at 1m cadence — the design's single-cadence loop is
  untouched.
- **Accepted one-cycle index lag.** GitHub search is eventually-consistent. A PR
  approved *this* cycle may not be in the search index when the tail refresh runs,
  so it shows `unknown` (no bar) for one cadence interval, then resolves to `open`
  next cycle. It is almost never wrong (a just-approved PR is still open),
  self-heals, and the window *shrinks* as cadence rises. `update-in-place` (above)
  is what keeps this a quiet "no bar yet" rather than a known→unknown→known
  flicker. The exact, lag-free alternative was GraphQL, rejected above.
- **Silent-truncation guard.** `gh pr list` defaults to 30 results and the search
  returns a *superset* of the feed; `--limit 200` is comfortably above realistic
  daily bot-review volume, and the **seam logs a warning when the result count
  equals the limit** (it owns the constant) so an undersized bound surfaces in
  logs instead of silently dropping PRs to `unknown`.
- **Test responsibility moves.** "Only today's entries are refreshed" was a
  seam-call assertion (the engine called `PRState` only for today's numbers); it
  becomes an **engine-intersection** assertion (the seam offers states for
  non-today PRs too, and only today's land in the wire) — a stronger guarantee.
  The fake's per-number `FailState` becomes a whole-call `StateErr`, mirroring the
  new granularity.
