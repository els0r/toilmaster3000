# Widen the on-demand Diff lookup from queue-only to queue-or-Staging

## Context

The Diff pill (opened from a PR row) fetches per-file diffs on demand via
`Engine.Diff`, which reaches `gh` directly — the sanctioned exception to the
no-per-PR-call rule (ADR 0007), since it is bounded by human click-rate rather
than cycle cadence, unlike the batched PR-State refresh ADR 0007 itself fixed.

Until now, `Diff` was deliberately scoped to the CURRENT Needs-Human-Review
queue snapshot only: a number absent from it returned `ErrNotInQueue` and never
reached `gh`. The route `GET /queue/{number}/diff` and its `ErrNotInQueue`
sentinel — shared with `ApproveManually`, the queue's manual-override approve
path — encoded this.

Staging (CONTEXT "Cycle Funnel": PRs "eligible, but matched no Rule") rendered
the same diff magnitude (`DiffMag`) as the queue, but as a bare, non-interactive
span — clicking it did nothing. This was reported as inconsistent with the
queue's clickable pill: the same visual affordance (a diff-size readout)
behaved differently depending on which station rendered it. Reusing the
queue's exact `DiffCard` modal for Staging requires resolving a Staging PR's
diff, which the queue-only scoping structurally prevented (a Staging PR number
404s).

## Decision

Widen `Engine.Diff` to resolve a PR number from **either** the
Needs-Human-Review queue **or** Staging — not Dropped or Approved-elsewhere,
which stay out of scope (they render no diff at all today, so there is no
reported inconsistency to fix there). Split the sentinel `Diff` used to share
with `ApproveManually`:

- `ApproveManually` keeps `ErrNotInQueue`, unchanged — it stays queue-only. A
  Staging PR has matched no rule yet and must never become manually-approvable
  through that path.
- `Diff` returns a new `ErrPRNotTracked` for a number in neither bucket.

Rename the route from `GET /queue/{number}/diff` to `GET /pipeline/{number}/diff`,
pairing it with the existing `GET /pipeline` snapshot endpoint rather than the
now-inaccurate `/queue/` prefix.

On the frontend, extract the queue's inline pill + modal-open state into a
self-contained `DiffPill` component: it owns its own open/closed state and
renders its own `DiffCard`, so both Staging and Needs-Human-Review drop in the
same component with zero parent wiring — the shared-deep-module pattern ADR
0014 already established for `PrRow`/`DiffMag`.

## Considered Options

- **Add a second, additive Staging-scoped endpoint** (e.g.
  `/staging/{number}/diff`), leaving the queue's route/sentinel/tests
  untouched. Rejected: the two lookups would be functionally identical
  (resolve a tracked PR, call the same `gh` diff seam) — two near-duplicate
  public surfaces for one capability, the copy-paste this codebase's
  conventions call out to avoid.
- **Frontend-only: link straight to GitHub instead of opening the modal.** No
  backend change at all. Rejected: it makes the pill clickable but not
  consistent — the queue would still open an in-app modal while Staging
  redirected away, two different interactions behind the same-looking pill.
- **Widen `ApproveManually` alongside `Diff`.** Not seriously considered: a
  Staging PR has matched no rule, so making it manually-approvable would let a
  human bypass rule-matching entirely — a different, unrelated capability this
  change must not grant.

## Consequences

- `DiffMag`'s comment previously cited "ADR 0014, Candidate C" for the
  wrapper/onClick variation — that citation never existed in ADR 0014's actual
  text. It now cites this ADR instead, and Staging no longer has a bare,
  non-clickable `.diff-mag` wrapper to describe: every caller renders
  `DiffPill`.
- The on-demand `gh` call ADR 0007 sanctioned as queue-only now also fires for
  Staging clicks. This does not reintroduce the per-cycle N+1 ADR 0007 removed:
  it is still bounded by human click-rate, not cycle cadence, and Staging
  cohorts are the same order of magnitude as the queue.
- `openapi.json` / `schema.d.ts` regenerate: `get-queue-diff` becomes
  `get-pipeline-diff`, and the path moves under `/pipeline/`. No other endpoint
  changes.
- Dropped and Approved-elsewhere still render no diff at all (out of scope
  here) — `Diff`'s two-bucket lookup does not cover them; extending it further
  is a future decision, not implied by this one.
