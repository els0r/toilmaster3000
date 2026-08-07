# Hooks: two kinds at fixed points — Notifiers and Screens

## Context

tm3k grows its first AI-assisted behaviors: a security vetting pass before
auto-approval (a bad actor can flag a malicious diff as `chore` and ride an
Approve Rule straight through — the rule set matches titles and sizes, never
content), and language-specific AI review assistance for PRs awaiting a
human. The original proposal was one generic hook "stage" run between
pipeline stages, mixing Slack pings, log lines, and verdict-returning AI
checks in a single mechanism.

The lumping fails at the first failure-mode question. A crashed Slack curl
must never block an approval; a crashed security check that fails *open*
defeats its entire purpose (the malicious-chore PR walks through precisely
when the checker is down). One uniform contract — "verdict optional, missing
⇒ proceed" — makes a dead security check indistinguishable from a successful
notification. The failure contracts are irreconcilable, so the kinds must be
distinct.

Two glossary collisions forced renames along the way: "stage" already means a
partition bucket a PR sits in (outbound stages, funnel stations), and "check"
already means `statusCheckRollup` everywhere (`AllGreen`, `FailingChecks`,
the "checks running" card).

## Decision

1. **Two kinds, one attachment mechanism.** A **Notifier** fires a
   side effect: output ignored, failure logged and never able to block,
   divert, or reorder an engine action. A **Screen** yields a structured
   **Verdict** — `proceed` / `hold`, each with a reason — that gates the
   engine action at its point; a missing verdict (crash, timeout, unparseable
   output) is **never** `proceed`. "Screen" (security-screening register) was
   chosen over "Check" for the CI collision; its verdict vocabulary follows
   (`hold`, the Screening funnel segment).
2. **Hooks attach to hook points, not stages** — a point is a moment the
   engine passes through, not a bucket a PR sits in. Four points, under a
   strict **pre/post discipline**: pre-points carry Screens only (they gate
   the upcoming action), post-points carry Notifiers only (they announce a
   completed fact — the hook sibling of "act first, append the ledger only on
   success"):
   - `pre_approve` (Screens) — an Approve Rule matched, no Review Rule or
     breaking title gated, the moment before `approve()`.
   - `post_approve` (Notifiers) — an approval succeeded, auto and manual
     override alike (the context carries the manual flag).
   - `queue_entered` (Notifiers) — rules routed the PR to Needs-Human-Review.
   - `screen_held` (Notifiers) — a Screen's `hold` diverted the PR there.
3. **`queue_entered` and `screen_held` are separate events**, and they
   partition queue entries: a queue item carries rule reasons XOR screen
   holds by construction, because Screens only run where no rule gated. The
   split exists so a just-screened PR never auto-receives a second AI pass —
   the review-assist Notifier attaches to `queue_entered` only, and posting
   style nits on a PR suspected of smuggling malicious code would bury the
   security concern it was held for. Kill the double-pass by topology, not by
   a skip-flag DSL.
4. **Firing discipline — Screens are per-head, Notifiers are once-per-PR.**
   A Screen's verdict is keyed `(screen, number, head)`: a gate must judge
   the code it gates, so a new push re-screens; cost is bounded like CI, one
   run per push, only on the would-auto-approve subset. A Notifier fires
   once per PR **ever**, via a persisted fired-ledger keyed `(hook, number)`
   recorded **at dispatch** (at-most-once): AI review output is
   non-falsifiable at the margin — run it 1000 times, get 1000 confident
   outputs — and a human reviewer never runs `/golang-pr-review` twice on
   one PR.

## Considered Options

- **One uniform hook contract.** Rejected: see Context — failure semantics
  are the essence of the kinds, not a detail.
- **Notifiers at pre-points.** Rejected: a pre-approve notification announces
  an action that may still fail and retry next cycle — dishonest. A "warn me
  before approving" hook is really an intervention-window feature (delayed
  approval), explicitly not built.
- **Per-head Notifier firing.** Rejected: re-running review-assist on the
  fix-up push nitpicks the fixes the author just made in response to it.
- **Retrying failed Notifiers.** Rejected: a reviewer that posted three
  comments and then crashed would duplicate them on retry; for outward-facing
  side effects a logged miss beats a double-post.
- **Reason-filtering config on one queue event** instead of the event split.
  Rejected: the events are genuinely different facts ("a human should look"
  vs "the robot's vetting objected"), disjoint by construction — topology
  expresses what a filter DSL would only approximate.
- **Outbound points (`pre_merge`/`post_merge`).** Deferred: outbound autonomy
  is consent-driven — the Arm *is* the human decision, and a pre-merge Screen
  would second-guess it. The point registry keeps adding them cheap.

## Consequences

- The review-assist never auto-runs on screen-held PRs; attaching it to both
  queue events is an explicit config act, never a default.
- A restart can never re-spam a PR with comments — the fired-ledger is
  persisted, and at-most-once means a failed notification is a logged miss,
  not a retry.
- A PR that gains commits *after* entering the queue gets no fresh
  review-assist run — accepted, mirrors how humans review. Amended by ADR 0026:
  no *repeat* run, but a Notifier the PR was previously out of scope for can
  still make its *first* run once the PR grows into scope.
- Vocabulary is load-bearing: hook point ≠ stage, Screen ≠ CI check,
  Notifier output is not a "verdict". CONTEXT.md carries the terms.
