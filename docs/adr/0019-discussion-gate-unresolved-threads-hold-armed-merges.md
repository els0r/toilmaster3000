# Unresolved review threads hold armed merges — the In Discussion stage

## Context

The outbound merge (ADR 0016) fires on Armed + Ready (green + `APPROVED`) +
`MERGEABLE`. Field feedback: **too trigger-happy when armed**. Reviewers
approve with nits — "approved, but please address X" — and the robot merges
the moment the pipeline is green, before the nits can be addressed. The
gentleman's agreement the Arm was designed around was only half-enforced:
default-Withheld protects *unarmed* PRs, but an armed PR merged mid-
conversation.

The original proposal was tri-branch: comment count 0 → merge; all comments
resolved → merge; unresolved comments → a new "Actively discussing" pipeline
step.

GitHub's comment model forced the first sharpening. A PR carries three comment
species and only one is resolvable: **review threads** (inline diff chains,
`isResolved`). Issue comments (bots, LGTMs) and review-summary bodies have no
resolve mechanism — if they gated, one CI bot comment or one "LGTM" approval
body would wedge the merge *forever*, and the count-0 branch would be dead
code on any real PR. Restricting the gate to threads collapses the first two
branches into one predicate: **zero unresolved review threads**.

## Decision

1. **The gate is "zero unresolved review threads"** — review threads only;
   issue comments and review bodies never gate. Outdated ≠ resolved: a thread
   whose lines changed underneath it still holds (`isOutdated` is ignored) —
   only the explicit resolve click closes a conversation.
2. **Realized structurally as a stage, not a merge-step clause**: **In
   Discussion** — green + `APPROVED` + ≥1 unresolved thread — sits between
   Awaiting Approval and Ready in the outbound partition (precedence: draft >
   not-green > changes-requested > awaiting-approval > in-discussion > ready).
   The merge step walks only Ready, so the partition *is* the gate, and Ready
   stays honest as "nothing left but the merge".
3. **Hold, never disarm** (the conflict-marker model, not the
   Changes-Requested model): Armed ∧ In-Discussion is a valid state, the Arm
   toggle stays available on the stage, and the first cycle the PR reads zero
   unresolved threads (still green + approved + mergeable) it merges —
   including when the *reviewer's* resolve click is what tips it, with nobody
   at the keyboard. Only `CHANGES_REQUESTED` withdraws consent.
4. **Data: a third batched call per cycle** — `gh api graphql` over the
   authored search returning `map[number]unresolvedCount`, because
   `isResolved` exists only in GraphQL's `reviewThreads` connection (no
   `gh pr list/view --json` field carries it). Fail-closed: a failed threads
   call clears the outbound snapshot and skips all merging that cycle.
   Truncation is conservative: more thread pages than fetched ⇒ treated as
   *having* unresolved threads, never as resolved.
5. **No heartbeat count** — In Discussion rides `/outbound` only; the strip's
   `ready` keeps meaning "waiting only on you".

## Considered Options

- **All comment species gate.** Rejected: unresolvable species make the hold
  permanent; every bot-touched PR parks forever.
- **Marker on Ready** (the `MERGEABLE` precedent — block the merge, keep the
  stage). Rejected: a conflict is unilaterally fixable (rebase — still
  "waiting only on you"), but a discussion waits on a counterparty; leaving
  such rows in Ready falsifies the stage's meaning and inflates the ready
  badge.
- **Disarm on discussion** (the Changes-Requested precedent). Rejected: a nit
  thread is a conversation, not a formal objection; re-arm friction after
  every nit reintroduces the exact toil the Arm exists to remove.
- **Edge-triggered disarm on threads appearing after arm time.** Rejected:
  requires per-PR transition memory in `.state/armed.json`, breaking the
  level-triggered doctrine, for murky semantics (resolved-then-reopened?).
- **Replace the outbound list call with one GraphQL query** (threads plus all
  list fields, staying at two calls per cycle). Rejected: forks the shared
  `gh pr list` decode into a second dialect (checks rollup, author, draft,
  mergeable re-decoded) to save one call on a 60s cadence.

## Consequences

- The org convention becomes explicit: **a nit that must block the merge
  lives in an inline thread** — a nit in an approval's summary body is
  invisible to the gate.
- The gate is itself a gentleman's gate: GitHub permits the author to resolve
  any thread, so tm3k makes closure *explicit* rather than enforced — it
  cannot compel reviewer sign-off on the fix.
- A reviewer's resolve can trigger a merge minutes later with the author
  offline — accepted; that is what standing consent (the Arm) means.
- Three `gh` calls per cycle instead of two; thread data is load-bearing for
  the partition, so its failure blanks the outbound funnel rather than
  serving a guess (ADR 0016's "never merge on stale data", extended).
- The zero-comments and all-comments-resolved cases of the original proposal
  are one condition by construction — there is no separate "count == 0" path
  to test or explain.
