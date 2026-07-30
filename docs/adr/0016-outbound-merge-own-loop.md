# Outbound merges run in tm3k's own loop, mirroring gh-land — not GitHub native auto-merge

## Context

The Outbound direction gives tm3k a second, more destructive verb: **merging
PRs the user authors**. The user's conditions for an automatic merge are
precise: (a) approved by someone, (b) pipeline green, (c) merging permitted —
and, crucially, only for PRs the user has **explicitly Armed** (default:
Withheld, because a gentleman's-agreement approval — "approved, but please
address X" — must never merge on its own).

GitHub ships this feature natively (`gh pr merge --auto`), so the obvious
question is why tm3k doesn't just use it. Separately, the org already has a
landing convention — the `gh land` extension (`panta/tools/gh-extensions/
gh-land`): squash-merge with commit subject = PR title and commit body = PR
description + an `Approved by: <reviewers>` trailer, delete branch,
preconditions not-draft / `reviewDecision == APPROVED` / `mergeable ==
MERGEABLE`. Outbound merges must produce commits indistinguishable from a
hand-run `gh land`.

## Decision

tm3k merges armed PRs **itself, in the existing cycle loop**, replicating
gh-land's mechanics: each cycle, an Armed PR that is green + `APPROVED` +
`MERGEABLE` gets one `gh pr view` (live `body`/`reviews` — the commit message
is built from the PR description *at merge time*, never a stale arm-time copy)
followed by `gh pr merge -s -d -t "<title>" -b "<body>\nApproved by: …"`, one
retry, and an append to `merges.jsonl` on success.

The consent model is part of the decision: **Withheld by default; Arm anywhere
except Changes Requested; disarm is level-triggered** (an armed PR observed
with `reviewDecision == CHANGES_REQUESTED` is disarmed that cycle, so
Armed ∧ Changes-Requested is an impossible state — an open objection always
requires fresh consent after re-approval).

## Considered Options

- **GitHub native auto-merge (`gh pr merge --auto`).** Rejected: it enforces
  *branch protection's* conditions, not tm3k's — if protection doesn't require
  an approving review, an unapproved PR merges, violating condition (a). The
  merge also happens outside tm3k (no ledger entry at the moment of action,
  the exact audit gap an auto-merger must not have), and it needs auto-merge
  enabled repo-side.
- **Shelling out to `gh land` itself.** Rejected on mechanics, not spirit: the
  extension is interactive (y/n confirm) and operates on the *locally
  checked-out branch* (`git symbolic-ref HEAD`) — tm3k has neither a TTY nor a
  checkout. The Arm is the confirm prompt, given in advance; tm3k replicates
  the rest byte-for-byte.
- **Hybrid (tm3k verifies, then enables native auto-merge).** Rejected: two
  mechanisms to maintain, and a murky answer to "who merged this and why."

## Consequences

- Merges happen only while tm3k runs — inherent to a single-workstation tool
  with a 60s cycle, and accepted.
- `mergeable` rides the once-per-cycle outbound list call; the per-merge
  `gh pr view` is a sanctioned per-PR call in the ADR 0008 sense (rare,
  user-consented via the Arm).
- The armed set is persisted (`.state/armed.json`) — the first mutable per-PR
  state in tm3k — so an explicit human decision survives restarts.
- A failed outbound fetch clears the outbound snapshot and skips all merging
  that cycle: the robot never merges on stale data.
- A reader who sees tm3k decline to merge an armed, green, approved PR should
  check `mergeable` (conflicts block silently-but-visibly via the Ready
  conflict marker) before reaching for a bug report.
