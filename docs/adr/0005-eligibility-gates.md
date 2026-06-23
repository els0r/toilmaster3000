# Eligibility Gates filter the candidate set before any Rule or Invariant

## Context

tm3k auto-approves PRs and routes some to Needs-Human-Review, all derived from a
single candidate set (`gh pr list` against the team review-request search). Two
core principles — not user-configurable Rules — must hold:

1. **Draft PRs must never be touched.** tm3k only deals with PRs marked Ready for
   Review: a draft must not auto-approve *and* must not appear in
   Needs-Human-Review.
2. **A PR must have an all-green pipeline to be eligible.** A red or still-running
   pipeline means the robot has no business acting.

The existing breaking-change **Invariant** sits right beside these conceptually,
but it *diverts* a ready PR to the queue. These two principles are different in
kind: a draft or red PR isn't something a human should see in tm3k either — it's
simply not ready for the robot to have an opinion.

## Decision

Introduce **Eligibility**, composed of two hard-wired **Gates** evaluated per
candidate, after the dedup skip and **before** parse / Rules / breaking-change
Invariant:

- **Ready-for-Review Gate** — a draft PR is dropped.
- **All-Green Gate** — a PR is eligible only if `github.AllGreen(checks)` holds.

A Gate **drops** the PR entirely (never approved, never queued, never shown) —
in contrast to an Invariant, which *routes* a ready PR to the queue. Filter, not
divert.

Both signals ride the **same single `gh pr list` call** via added `--json` fields
`isDraft` and `statusCheckRollup` (no per-PR N+1). `PR` carries `IsDraft bool` and
`Checks []Check`; the CLI seam only decodes. The verdict is the pure,
table-driven-tested `github.AllGreen`, which folds each rollup entry into
pass / fail / pending and requires **≥1 entry and zero non-pass**.

## Considered Options

- **Push `draft:false` into the search query** (server-side draft exclusion).
  Rejected: drafts would never come back, so they could not be logged or counted.
  We deliberately filter **in-process** instead, so both Gates share one mental
  model and every drop is observable (per-PR `gate: draft|not_all_green` log + a
  `dropped` count in the cycle status). The All-Green Gate forces in-process logic
  anyway, so uniformity costs nothing. Silent filtering is the scariest failure
  mode of an auto-approver — the `dropped` count is the insurance.
- **Empty rollup ⇒ all-green (vacuous truth).** Rejected: "every entry is pass"
  is trivially true for zero entries, which would let a brand-new PR — check-less
  for the seconds before CI registers — sail straight to auto-approval on **no
  signal**. We require **at least one passing check**. Cost: a genuinely
  check-less repo never auto-approves; accepted, `repo` always runs CI, and "no
  signal" is exactly what this Gate exists to refuse.
- **Strict `SUCCESS`-only is green.** Rejected: would permanently block any PR
  with a conditionally-`SKIPPED` or `NEUTRAL` job. We count `SKIPPED`/`NEUTRAL`
  as pass, matching GitHub's own "checks passed" UI.
- **Model the Gates as Invariants (route to queue).** Rejected: that would put
  drafts and red PRs in front of a human, contradicting principle 1.

## Consequences

- The Candidate set is redefined as the **eligible** PRs; "Candidate" now implies
  ready-for-review and all-green. Rules operate on a pre-filtered, clean set.
- A **pending** pipeline blocks harmlessly: the PR isn't a candidate this cycle,
  and because the candidate set is recomputed from scratch each cycle, it becomes
  eligible automatically once checks finish — no persistent "waiting" state.
- Cycle status gains a **`dropped`** count (draft + not-all-green combined),
  surfaced through `engine.Status` → `server.CycleStatus` DTO → OpenAPI →
  generated frontend types → `StatusLine`. A failed candidate fetch records
  `dropped 0`, parallel to `approved`/`queue`.
- `github.AllGreen` is correctness-critical pure logic and is tested at matcher
  weight (mixed CheckRun + StatusContext, every bucket, empty rollup, SKIPPED/
  NEUTRAL); the `gh` shell-out that decodes the rollup stays lightly tested.
