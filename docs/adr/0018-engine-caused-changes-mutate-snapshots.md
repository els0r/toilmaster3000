# Engine-caused changes mutate the published snapshots immediately

## Context

The queue, funnel, and outbound snapshots are recomputed once per cycle; the
approval and merge ledgers are appended the moment an action succeeds. That
split produced visible contradictions the UI could not refetch its way out of:
a manually-approved PR sat in Needs-Human-Review *and* the Approval Feed for
up to a cycle, and a robot-merged PR sat in Ready (armed) *and* the Merged
station — `rebuildOutbound` published the snapshot *before* running the merge
step over it, so the very cycle that merged a PR served it as Ready for
another 60s.

The frontend already refetched every affected endpoint immediately after each
manual action; it faithfully re-pulled known-false data. Re-pulling from
GitHub instead would not fix it either: the search API is eventually
consistent, so a just-merged PR can linger in the `is:open` results.

## Decision

A state change the engine itself **performed** is applied to its published
snapshots immediately, atomically with the ledger write. A change the engine
merely **expects** (a new rule that would match a Staging PR, a red check that
might rerun green) is never pre-applied — that is honest staleness, resolved
by the next cycle.

Concretely:

- **Manual approve** (`applyManualApprove`): under one lock, remove the PR
  from the queue snapshot and — only when it was actually still queued — move
  its funnel count from `NeedsHumanReview` to `ApprovedByTm3k` and decrement
  the heartbeat's `QueueCount`. The found-guard makes the mutation a no-op
  when a cycle rebuild won the race and already recomputed the snapshots
  without the PR. `ApprovedThisCycle` stays untouched: it is the cycle's
  pulse, and a manual override is not the cycle's doing.
- **Robot merge** (`pruneMergedFromOutbound`): in the same critical section as
  the merge-ledger append, remove the PR from the published outbound
  snapshot's Ready list and decrement `Outgoing`. `/outbound` and `/merges`
  can never disagree — the row leaves Ready in the same atomic step that puts
  it in the ledger. A failed merge mutates nothing: the PR genuinely is still
  Ready.

Both mutations move a PR *between* partition segments (or remove it from both
sides of a partition), so the funnel and outbound sum invariants keep holding
by construction.

## Considered Options

- **Frontend optimistic removal.** Rejected: the frontend is a pure renderer,
  and the next 10s poll would resurrect the stale row from the unmutated
  snapshot — a flicker instead of a fix.
- **Trigger a full cycle after each manual action.** Rejected: per-action `gh`
  list calls, and GitHub search's eventual consistency means the fresh pull
  can still contain the just-merged/approved PR.
- **Merge before publishing the outbound snapshot** (reorder
  `rebuildOutbound`). Rejected: while the merges run, the *previous* cycle's
  snapshot — which also lists the PR as Ready — keeps being served against a
  growing merge ledger, so the duplicate window survives; publication would
  also stall behind N sequential merge calls.

## Consequences

- The snapshot's meaning sharpens from "what the last cycle saw" to "the
  engine's current knowledge": cycle output plus the engine's own actions
  since. Anything GitHub does between cycles (a human approving, CI going red)
  still lands only on the next rebuild.
- `mergeArmedReady` iterates the same slice header the prune edits, so the
  prune rebuilds the Ready slice instead of shifting in place — both share the
  backing array.
- The merged PR's armed entry still waits for the next cycle's
  `reconcileArmed` cleanup; harmless, since armed flags are only zipped onto
  rows present in the snapshot.
- `onApproved` in the frontend refetches `/pipeline` alongside queue, status,
  and approvals, so the corrected funnel bar repaints with the queue panel.
