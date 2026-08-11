# Screening is a polled signal — async verdicts, fail closed

## Context

A Screen run is an AI harness invocation: one to five minutes, sometimes
more. The engine is one goroutine — `RunCycleOnce(); sleep; repeat` — and the
outbound rebuild plus the merge step run as deferreds at the tail of the
*same* cycle. Screening synchronously inside the cycle would stall the whole
app for minutes (heartbeat, snapshots, queue all stale) and — worse — make
**armed outbound merges wait on inbound AI vetting**, silently coupling the
two pulls the design keeps disjoint and fail-independent. A timed-out sync
check also leaves the PR bucket-less: outside the funnel partition, invisible
for as long as the harness misbehaves.

The codebase already has the right precedent: a pending CI pipeline "blocks
harmlessly — the set is recomputed each cycle." A Screen is the same species
of thing as CI: an external signal the loop polls, never awaits.

## Decision

1. **Screens never block the cycle.** The verdict store is consulted
   level-triggered each cycle (the armed/disarm idiom — no transition
   memory). A missing verdict dispatches a run — bounded pool (4), in-flight
   dedup on `(screen, number, head)`, per-screen timeout (default 10m, the
   only tunable) — and the PR lands in the new **Screening** funnel segment
   this cycle. The partition formula gains the segment:
   `INCOMING = … + Staging + Screening + Needs-Human-Review + …`. A ready
   verdict acts on the next pass: approval latency ≤ one cycle.
2. **Verdicts are per-head and persisted**: append-only
   `.state/verdicts.jsonl`, rows
   `(screen_id, number, head, outcome: proceed|hold|error, reason, at)`;
   the latest row per key wins. (**Amended by ADR 0028**: the key is written
   as `hook_id`, the spelling `hookfires.jsonl` and `transcripts.jsonl`
   already used — a Screen is a hook and this is its stable hook `Id`. Rows
   written under the old name still load; nothing else changes.) A new push changes the key, so the store
   misses and the PR re-screens — no invalidation logic exists or is needed.
3. **Conjunctive fold, all holds collected.** Every enabled Screen must say
   `proceed`; any `hold` diverts the PR to Needs-Human-Review —
   Invariant-family: divert, never drop — carrying **every** holding screen
   (the reasons-list doctrine) as `screen:<name>` reasons plus the prose
   `screen_holds` field on the queue item.
4. **A hold clears three ways, all level-triggered, and no other way**: a new
   push (fresh key), the manual-override Approve (the human outranks the
   robot's vetting, exactly as they outrank the breaking-change Invariant),
   or disabling the screen (+ restart). Deliberately no re-run button.
5. **The no-verdict path is bounded and ends at a human.** A failed run
   (nonzero exit, timeout, no extractable verdict) is a recorded `error` row;
   the PR stays visibly in Screening and the next cycle re-dispatches —
   transient blips self-heal. Three `error` rows for the same key synthesize
   `hold` with reason `screen unavailable: <last error>`, flowing through
   `screen_held` like any hold. Never silent limbo, never a walk-through:
   `proceed` requires an affirmative parsed verdict; everything else
   eventually summons a human with the error as its reason. Attempt cap (3)
   and pool size (4) are hardcoded — no config surface until a need shows.

## Considered Options

- **Sync-in-cycle with timeout** (parallel across candidates). Rejected: the
  app stalls for up to the timeout every cycle with new candidates; armed
  merges queue behind inbound vetting; a timed-out PR has no bucket. The
  failed-approve precedent ("outside the partition, reappears next cycle")
  tolerates a one-cycle transient, not a standing state.
- **Push-model async** (workers mutate engine state on completion). Rejected:
  ADR 0018's in-place mutations are for actions *the engine performed*; a
  verdict arriving is not an engine action. Level-triggered store-reads give
  trivial crash semantics (re-dispatch on miss) and no mid-cycle snapshot
  mutation.
- **Retry failures forever.** Rejected: burns a paid AI run every cycle
  against a broken harness, and nobody is ever summoned.
- **Fail to `hold` on first error.** Rejected: one transient network blip
  poisons a good PR into the queue when the next attempt would have said
  `proceed`.
- **No new segment** (park pending PRs in Staging, or outside the partition).
  Rejected: Staging means "matched no rule" — a lie; outside the partition
  means invisible — the funnel-partition doctrine exists precisely to forbid
  both.

## Consequences

- The wire changes: the funnel DTO gains the Screening segment and the queue
  item gains `screen_holds` — `make check` / `openapi.json` /
  `schema.d.ts` all move at implementation time.
- Screening renders as its own read-only station (which screens are pending,
  per PR — answers "why hasn't #123 gone through?"), but gets **no heartbeat
  count**: it is not actionable, and screen failures already surface in the
  strip's `review` count as synthetic holds.
- A `proceed` is silent — no badge, no feed annotation; the happy path stays
  exactly as calm as before screens existed.
- Restarts are free: verdicts, attempts, and holds all live in the store;
  in-flight runs lost to a crash simply re-dispatch on the next cycle.
- Screening cost tracks push cadence like CI does, and only on the
  would-auto-approve subset — the cheapest PRs by construction.
