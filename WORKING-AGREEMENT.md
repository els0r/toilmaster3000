# Working Agreement — autonomous orchestration

How Claude runs a PRD to completion on this repo, distilled from the Cycle
Funnel session (PRD #1 → issues #2–#6 → PRs #7–#11). This is the contract for
"work this autonomously": it says how work is decomposed, made visible, built,
verified, and merged — and where Claude must still stop and ask.

> **Status:** draft for review. Adjust freely; the board mechanics in
> §3 in particular need your confirmation.

## 1. Roles and branches

- **Orchestrator (Claude).** Runs on a dedicated coordination branch
  `claude/autonomous-<purpose>-<id>`. This branch holds **no code commits** and
  **never opens its own PR**. It exists only so the orchestrator has a clean
  place to sit while it dispatches, verifies, and merges. Delete it at the end.
- **Implementers (subagents).** Each sub-issue is built by one subagent in an
  **isolated git worktree**, on a feature branch `feat/<n>-<slug>` cut from
  `main` (never from the coordination branch).
- All code reaches `main` through per-issue PRs, never directly.

## 2. PRD → implementation issues

- A **parent PRD** (label `prd`) defines *intent, not implementation*. It stays
  **open** until you close it — Claude does not close the PRD itself, even when
  every child has merged.
- The PRD is decomposed into **vertical-slice sub-issues** (tracer bullets):
  each is independently shippable, end-to-end (engine → API → UI as needed), and
  small enough to review in one PR.
- Every sub-issue carries:
  - **What to build** (end to end), **Acceptance criteria** (checklist), and
    **Blocked by** (explicit dependency on other issue numbers).
  - Label `ready-for-agent` once triaged.
- The dependency chain is explicit and drives sequencing. Example from this
  session: `#2 → #3 → {#4 ∥ #5} → #6`.
- Each sub-issue PR body contains `Closes #N`; the merge closes the issue. Claude
  never closes an implementation issue by hand.

## 3. The board — make status visible

You want to **see** sub-issues move through states, not just trust an internal
list. Status lives on the **GitHub Projects (v2) board** — moved between lanes,
**not** tracked with labels (`ready-for-agent`/`prd` stay as triage labels only).
Two layers, kept in sync:

1. **Projects v2 board (the source of truth you watch).** Each sub-issue moves
   through the board's **Status** field as work progresses:
   - **Todo** once triaged.
   - **In Progress** the moment its subagent is dispatched — *not* at completion.
   - **In Review** when its PR is open and under verification.
   - **Done** when the PR merges (`Closes #N` closes the issue).
   - Claude sets the project item's Status field via `gh` so the card moves on
     the board in real time.
   - **Prerequisite:** the `gh` token needs the `project` scope
     (`gh auth refresh -s project`); the default token does not carry it. Without
     it Claude cannot move cards — it flags this as a blocked handover rather than
     falling back to a status label.
2. **Session task list (mirror).** Claude keeps an internal task per sub-issue
   with the same `blocked-by` edges, flipped to **in-progress on dispatch** and
   **completed on merge**, so the live spinner reflects the board. This is a
   mirror, not the record of truth.

The rule: **a sub-issue moves to In Progress at dispatch, not at completion** —
so the board always shows the true frontier of work.

## 4. Dispatch

- **Default bundling: one issue per PR.** Smallest reviewable unit; matches the
  vertical-slice decomposition. Confirm bundling with the user before dispatching
  if a different split is on the table.
- **Parallelize independent issues.** Sub-issues sharing the same `blocked-by`
  (e.g. #4 ∥ #5) are dispatched concurrently in separate worktrees, with each
  brief **scoped off the others' files** to avoid collisions. Expect to rebase
  the second-merged branch.
- **Briefs are self-contained.** A subagent has no session memory. Each brief
  restates: the issue verbatim, prior decisions and their rationale, branch +
  base, the build/verify gates, the commit convention, and the guardrails (§7).
- **TDD is required.** Each implementer invokes `/tdd` and works
  red→green→refactor in **vertical slices** (one test → minimal code → repeat),
  testing **behavior at the highest existing seam** (e.g. the cycle over
  `github.Fake`, the `/pipeline` handler), never internals.

## 5. Conventions

- **Commits:** Conventional Commits (`type(scope): subject`), matching existing
  history. Split backend/DTO and frontend into separate commits when natural.
- **Author identity:** `Lennart Elsen <els0r@users.noreply.github.com>` — no
  Open Systems affiliation.
- **Wording constraints:** honor any maintainer constraint on commit-message
  wording for the run (e.g. "don't mention internal design jargon"). Pass it into
  every brief.
- **Generated contracts:** after any wire-DTO change, run `make generate` and
  commit both `openapi.json` and `frontend/src/api/schema.d.ts` (ADR 0003).

## 6. Verify, then merge

**The orchestrator merges — but verifies first, every time.** A subagent's
summary is a claim, not evidence.

- **Verify against facts, not prose.** Check the PR via `gh`: base `main`, head
  branch, `Closes #N` present, mergeable, files changed match the brief's scope.
- **Run the gates yourself**, on the PR branch in a throwaway worktree (cleaned
  up after). There is no CI, so this is the only gate:
  - `make test` (Go + frontend), `make check` (OpenAPI/type drift).
  - Plus, as relevant: `go test -race ./internal/...`, `go vet`, `tsc --noEmit`,
    full `vitest run`.
  - Cheap drift check: regenerate `openapi.json` from the Go DTOs and `diff`
    against the committed spec.
- **Merge when green and mergeable.** Use **rebase merge** to keep `main`'s
  linear conventional-commit history. This authority is standing for autonomous
  runs — Claude does not wait for you to click merge.
  - **Authorization to merge is not authorization to skip verification.** A PR
    that isn't green, or whose diff exceeds scope, is **not** merged — it's a
    handover (§8).
- **After merge:** sync local `main` (`git merge --ff-only origin/main`), flip
  the issue/task to done, and dispatch the next now-unblocked issue.

## 7. Guardrails — never autonomous, even with merge authority

- No `git push --force` / `--force-with-lease`.
- No `--no-verify`, `--no-gpg-sign`, or any hook-skipping flag — a failing hook
  is a signal, not an obstacle.
- No `git commit --amend` on a pushed commit; fix forward.
- No `git reset --hard`, `git clean -fd`, or `git checkout --` on anything Claude
  didn't create — investigate unfamiliar state, it may be your WIP.
- No closing an implementation issue by hand (let `Closes #N` do it); no closing
  the parent PRD without your say-so.
- **External writes you didn't request** (filing a new issue, commenting
  outward) need explicit per-action approval — even mid-run. Discovered this
  session: filing the filter-chip follow-up (#12) required your go-ahead.
- No branch or worktree deletion without asking.

## 8. When to stop and ask (handover points)

Use these sparingly — routine progress, green gates, and dispatch events don't
need a prompt. Ask when:

- **Bundle composition** differs from one-issue-per-PR, before dispatching.
- **A sub-issue can't fully meet an AC** because of an upstream/PRD gap. Ship the
  verified work with a **documented placeholder**, and propose a **follow-up
  issue** capturing the gap, its cause, and the fix. *(This session: the Incoming
  filter chip rendered a frontend constant because no endpoint exposed the
  configured search → shipped + follow-up #12.)*
- **A trade-off has no clean default** (blast radius on consumers, legacy vs
  break).
- **A gate fails or a guardrail blocks the next step** — never search for a flag
  that bypasses it.
- **Closing the parent PRD** and **filing follow-up issues** — your call.

## 9. End-of-run checklist

- [ ] All sub-issues merged; each closed via `Closes #N`.
- [ ] Parent PRD left open for your review (unless you said otherwise).
- [ ] Known gaps captured as follow-up issues (with your approval).
- [ ] `main` fast-forwarded and clean; coordination branch deleted.
- [ ] A short final report: what merged, how it was verified, what's outstanding.
