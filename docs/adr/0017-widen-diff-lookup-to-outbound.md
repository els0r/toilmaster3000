# Widen the on-demand Diff lookup to the outbound snapshot

## Context

ADR 0015 widened `Engine.Diff` from queue-only to queue-or-Staging so the
clickable `DiffPill` (with its modal `DiffCard`) could ride Staging rows
exactly as it does queue rows. Outbound rows were left out: they rendered the
diff magnitude as a bare, non-interactive `DiffMag` span, because the lookup
resolved inbound PRs only — a pill on an outbound row would always 404. The
gap was flagged in PR #24 and deliberately not worked around there (widening
the wire contract was out of that slice's scope); issue #27 picks it up.

This is the same inconsistency ADR 0015 fixed for Staging, one funnel over:
the same visual affordance (a diff-size readout) behaving differently
depending on which station renders it.

## Decision

Widen `Engine.Diff`'s resolution to the outbound snapshot's six stage lists
(Draft, Red, Running, Changes Requested, Awaiting Approval, Ready) alongside
the queue and Staging. `ErrPRNotTracked` stays the single untracked sentinel;
its message widens to name all three sources. The inbound and outbound pulls
are disjoint by search, so first-match resolution is unambiguous.

Rename the route from `GET /pipeline/{number}/diff` to the direction-neutral
`GET /prs/{number}/diff` (operation `get-pipeline-diff` → `get-pr-diff`):
`/pipeline` is the *inbound* funnel's snapshot endpoint, so keeping the diff
under it would misname a lookup that now spans both funnels. This follows ADR
0015's own convention — it renamed `/queue/…` to `/pipeline/…` when the queue
prefix went inaccurate, and rejected an additive sibling endpoint as a
near-duplicate surface for one capability.

On the frontend, `outboundMeta` swaps its static `DiffMag` span for the shared
`DiffPill` — zero parent wiring, the deep-module payoff ADR 0015 set up.

## Considered Options

- **Add an outbound sibling endpoint** (`/outbound/{number}/diff`). Rejected
  for the same reason ADR 0015 rejected `/staging/{number}/diff`: two
  functionally identical lookups behind two public surfaces.
- **Keep the `/pipeline/…` path unchanged.** Cheapest, but leaves an
  inbound-flavored name on a direction-neutral capability; the embedded SPA is
  the API's only consumer, so a rename costs one regenerated contract and no
  external migration.
- **Widen `ApproveManually` alongside `Diff`.** Not considered, as in ADR
  0015: manual approval stays queue-only; an outbound (authored) PR must never
  become approvable through tm3k's own override path.

## Consequences

- The Merged station stays out of scope: its rows are ledger records
  (`merges.jsonl`), not tracked PRs, and render no diff magnitude at all.
- Dropped and Approved-elsewhere remain out of scope, as in ADR 0015 — they
  render no diff either.
- The on-demand `gh` call stays bounded by human click-rate (ADR 0007/0008);
  outbound cohorts are the same order of magnitude as the queue.
- `openapi.json` / `schema.d.ts` regenerate: `get-pipeline-diff` becomes
  `get-pr-diff` under `/prs/`. No other endpoint changes.
- An Outbound test that asserted "no buttons in the Changes-Requested card"
  as a proxy for "no Arm/Disarm toggle" now asserts the absence of the
  arm/disarm control specifically — the diff pill button legitimately rides
  every outbound row.
