# Analytics is computed over approval history only

## Context

The Analytics tab must show "how much toil did the robot save, and on what." The
intuitive framing is **Auto-approved vs Human Review** — but tm3k persists only
**`approvals.jsonl`** (one append-only record per approval). The
**Needs-Human-Review queue is derived live each cycle and never persisted**
(CONTEXT.md / "Needs Human Review"), and **dropped PRs** (Eligibility-Gate
failures, ADR 0005) exist only in transient logs. So the only durable, queryable
signal is *approvals that happened*.

This forces a meaning for "Human Review": within the log, an approval whose
`matched_rule` starts with `"human approval: "` is one a human made from the queue
(the manual-override Approve button); everything else is an auto-approval. The two
partition every recorded approval.

## Decision

Analytics computes **exclusively from `approvals.jsonl`**. "Auto-approved" and
"Human Review" are the `matched_rule`-prefix split of recorded approvals; their
shares sum to 100%. "Context switches saved" = the auto-approved count (a manual
approval is a switch the human *did* take, so it is not saved). No new persistence
is introduced for analytics.

## Considered Options

- **Persist a queue-event log** (`.state/queue-events.jsonl`) so "Human Review"
  could mean *everything ever routed to the queue*, approved or not — the true
  review burden. Rejected for the MVP: it adds a write path on every cycle plus
  dedup semantics for a queue that is deliberately re-derived from scratch each
  cycle, and it widens "no database, two state files" into a third event stream.
  The tab is named for and scoped to **approval** history; burden analytics is a
  separate, additive feature if it earns its keep.

## Consequences

- **"Human Review" undercounts true review load — by design.** A PR that hit the
  queue but was closed/merged by someone else, or still sits there unactioned, was
  never approved, so it never entered the log and is invisible to analytics. A
  future reader must not "fix" the number to mean more than logged manual
  approvals without first adding the queue-event persistence above.
- Because the split is a pure function of the existing `matched_rule` field, the
  feature needs **no migration** and works over all historical entries.
- "Context switches saved" is exactly the auto count — the headline rests on the
  same single source of truth as the rest of the row.
