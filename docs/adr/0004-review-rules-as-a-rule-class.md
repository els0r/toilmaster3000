# Review Rules are a second Rule class, not a separate type

## Context

We need user-configurable gates that force a PR into Needs-Human-Review instead
of letting it be auto-approved — e.g. an `osixpatch/approve` scope, or a diff
larger than N lines — to bound the robot's blast radius. This is the realized
form of the previously-deferred "Constraint." The existing `Rule` already
predicates over author + parsed conventional-commit title parts and is fully
wired through storage (`.config/rules.yaml`), CRUD/validation, the HTTP `/rules`
surface, and the editor UI.

## Decision

A Review Rule is the **same `Rule` struct** as today's auto-approve rule,
distinguished only by a `Class` field (`approve` | `review`); an empty/absent
`Class` is read as `approve`, so existing files need no migration. The predicate
vocabulary is **identical** across both classes — including a new shared
diff-size predicate (`DiffMin`/`DiffMax`, `0 ⇒ unconstrained`, summed from
`additions + deletions`). **Class changes only the *outcome* of a match, never
how a match is computed:** an Approve Rule match auto-approves; a Review Rule
match routes the PR to Needs-Human-Review and never approves.

Evaluation precedence per candidate (after the dedup skip and a successful
conventional-commit parse — the parse-gate is **uniform**, so a non-conventional
title is invisible to both classes): Review Rules are evaluated first and **win**
over Approve Rules; a PR accumulates a `reasons` **list** (every matching Review
Rule's name, plus `breaking_change` iff an Approve Rule also matched and the
title is breaking) and is queued if that list is non-empty, else auto-approved if
an Approve Rule matched.

## Considered Options

- **A distinct `ReviewRule` type and/or a separate `review-rules.yaml` + second
  Store.** Rejected: it duplicates the entire CRUD/validate/persist/API/frontend
  stack for no behavioural gain and actively fights the requirement that the rule
  editor "function identical."
- **A configurable Invariant** (extend the hard-wired breaking-change block with
  user knobs) rather than a rule class. Rejected: Invariants are global,
  non-named, and not user-managed; the user wants *named, enable/disable-able,
  per-condition* gates — that is exactly a Rule, just with the opposite outcome.
- **Asymmetric parse-gating** (let diff-only Review Rules catch malformed-title
  PRs). Rejected for MVP: conventional commits are mandatory to be seen at all;
  the uniform gate is simpler and the blind spot (huge diff + unparseable title)
  is accepted.

## Consequences

- `Class` is **card-implied and immutable** in MVP: the "Human Review Always"
  card stamps `class: review`, the Approve card `class: approve`. Reclassifying a
  rule means delete + recreate — no in-place move affordance.
- A Review Rule match queues a PR **even when no Approve Rule matches**, so the
  queue now holds more than "Approve-matched-but-breaking." `QueueItem.reason`
  (string) becomes `reasons` (list) across engine, wire DTO, and frontend; a
  manual override records `matched_rule: "human approval: <reasons>"`.
- The diff predicate adds `additions,deletions` to the single `gh pr list` call
  (no per-PR N+1) and a `DiffMin ≤ DiffMax` validation guard.
- Because the predicate is shared, an Approve Rule may also use diff bounds
  (e.g. "auto-approve only if diff ≤ 50") — a free additional blast-radius
  control, not a special case.
