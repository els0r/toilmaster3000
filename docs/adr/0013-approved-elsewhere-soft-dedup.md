# An existing approval is a soft dedup — tm3k declines to re-approve

## Context

tm3k's headline invariant is that the backend **autonomously approves every PR
matching a filtering condition** — any all-green, ready, rule-matching candidate
is auto-approved within a cycle. Until now, "approved" meant exactly one thing:
present in tm3k's own `approvals.jsonl` (the dedup set, loaded at startup). tm3k
had no notion of an approval made by anyone else — it never fetched review state,
so a PR a teammate (or a human) had already approved looked identical to an
untouched one, and tm3k would add its own redundant approval.

That assumption is breaking down: **multiple tm3k instances now run across the
team**, each as a different `@me`. The same PR can be approved by a teammate's
instance and still be returned by the configured search (`is:open …`). The user
also wants this made **visible** — seeing that "others approved them" is a signal
that the team is adopting the tool — which surfaced as the **Cycle Funnel**'s
*approved-elsewhere* sub-state.

Making approved-elsewhere visible forces a behavioral question: when GitHub
already reports a PR as `APPROVED` by someone other than tm3k, should the robot
still add its own approval?

## Decision

Add **`reviewDecision`** to the single per-cycle `gh pr list` fetch (no N+1) and
treat an existing approval as a **soft dedup**:

- A candidate whose `reviewDecision == APPROVED` but whose number is **absent from
  `approvals.jsonl`** was **approved elsewhere**. tm3k **does not approve it** and
  **records nothing** to `approvals.jsonl`.
- It is surfaced in the Cycle Funnel's approved stage as a **highlighted
  "approved elsewhere" row** — a PR tm3k **deliberately left alone**, not one it
  actioned.

This narrows the headline invariant: the robot approves every matching candidate
**that is not already approved**. The toil an auto-approver exists to remove —
the context switch of clicking *Approve* — is **already gone** once anyone has
approved, so there is nothing left for tm3k to save.

## Considered Options

- **Keep approving redundantly (status quo).** Rejected. It re-approves PRs a
  teammate's instance already cleared, adding noise to GitHub's review timeline,
  and — worse — every redundant approval would land in `approvals.jsonl` and be
  counted by Analytics as a saved context switch. With several instances on the
  team, the same switch would be counted once per instance: systematic
  double-counting of the one number (switches saved) the tool exists to report.
- **Hard dedup across the team (shared approval ledger).** Rejected as
  over-engineering for a single-workstation tool: it would require shared state or
  a cross-instance fetch of *who* approved. `reviewDecision` is a coarse,
  per-PR, single-call signal that is sufficient — we need "is it already approved,"
  not "by whom."
- **Approve-elsewhere as display-only, still approve.** Rejected: it would make
  the funnel row a cosmetic lie — labelling a PR "left alone" while the robot
  votes on it anyway — and keeps the double-counting.

## Consequences

- The core approve path gains a pre-check: an `APPROVED`-but-not-ours candidate is
  skipped like an already-deduped one. `PR` carries `ReviewDecision string`; the
  `gh` seam only decodes it.
- **Analytics stays honest.** Approved-elsewhere PRs never enter
  `approvals.jsonl`, so they are invisible to Analytics — correct, they were not
  *your* saved switches. This is the mechanism that prevents cross-instance
  double-counting.
- The headline invariant in CONTEXT is reworded from "approves every PR matching a
  filtering condition" to "…every matching PR **not already approved**." A reader
  who sees the robot decline an all-green, rule-matching PR should reach for this
  ADR, not a bug report.
- **Eventual-consistency window (accepted).** GitHub's search/review state is
  eventually consistent, so for one cycle two instances may both see a PR as
  not-yet-approved and both approve it — a harmless redundant approval that
  resolves as the index catches up. Same self-healing trade-off as the PR-State
  refresh (ADR 0007); the window shrinks as cadence rises.
- The decision is scoped to **auto-approval**. The manual Needs-Human-Review
  *Approve* button is an explicit human override and is unaffected.
