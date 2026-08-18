# Forge adapters: GitLab behind a normalising seam

## Context

Every layer of tm3k was written against GitHub. `internal/github` was not a
client but a domain package: it owned the types the engine passes around
(`PR`, `OutboundStage`, `PRState`), the pure folds that judge GitHub's raw
vocabulary (`AllGreen` over `CheckRun`/`StatusContext`, `CollapsePRState` over
`OPEN|MERGED|CLOSED`, `ClassifyOutboundStage` over `reviewDecision`/
`mergeable`), and the `gh` shell-out. The engine imported it in eight files.

Supporting GitLab via `glab` is therefore not "write a second implementation of
an existing interface" — there was no forge-neutral interface to implement, and
three things resist a naive port:

- **GitLab has no search query language.** tm3k builds three queries by string
  concatenation (`InboundSearch` appends `-author:@me`, `AuthoredSearch` is
  `is:open author:@me`, `PRStatesSince` builds `reviewed-by:@me updated:>=…`).
  GitLab's GraphQL `mergeRequests()` takes typed arguments instead, and the
  canonical candidate set in our own docs — `team-review-requested:owner/team` —
  has no GitLab equivalent at all.
- **GitLab's REST merge-request list carries neither changed-line counts nor
  changed-file paths.** Both are load-bearing: `DiffMin`/`DiffMax` is core rule
  vocabulary and Notifier `Paths` scope matches the file list (ADR 0026).
- **The check model has different cardinality.** GitHub reports N rollup
  entries per PR; GitLab reports one `head_pipeline` status.

## Decision

1. **One instance, one Forge.** Selected at boot (`--forge`/`TM3K_FORGE`,
   defaulting to `github` so existing configuration is untouched). Every
   persisted key stays a bare PR number — `approvals.jsonl`, `armed.json`,
   `verdicts.jsonl`, `hookfires.jsonl`, `merges.jsonl` need no migration and no
   composite key. A second forge is a second process with its own `.state`.
   GitLab's `iid` is the Number, never the instance-global `id`.

2. **"PR" stays the canonical domain word**, absorbing GitLab's merge request.
   Only rendered copy adapts ("Open on GitLab"). Renaming ~200 uses of PR
   across the domain, the wire and the frontend buys nothing, and no
   forge-neutral word is idiomatic to either community.

3. **Adapters normalise; the folds stay single.** Each adapter decodes its
   forge's raw shapes and maps them into one neutral vocabulary
   (`CheckState` pass/fail/pending, the `PRState` buckets, the review
   decision); `AllGreen`, `CollapsePRState` and `ClassifyOutboundStage` are
   unchanged and there is exactly one of each. This *tightens* the decode-vs-
   judge doctrine rather than bending it: today `AllGreen` judges gh's raw
   `__typename` strings, which was always the seam leaking upward.

4. **Adapters do not share a transport.** GitHub keeps `gh pr list --json`
   plus the one `gh api graphql` for threads. GitLab is `glab api graphql`
   throughout — the only transport that supplies `diffStatsSummary` and
   per-file `diffStats` for a whole pull in one call, so the diff-size
   predicate and Notifier scope survive without the per-MR N+1 that ADR 0007
   exists to prevent. Both still shell out to the operator's own CLI auth; tm3k
   holds no token.

5. **The candidate set is a forge-typed selector the core never parses.**
   GitHub's remains the opaque search string; GitLab's is typed arguments. The
   three derived pulls become named seam obligations each adapter satisfies
   natively (exclude-self, authored-by-self, reviewed-by-self-since), never
   string concatenation.

6. **The pipeline is GitLab's verdict; the failing count rides separately.**
   The adapter emits exactly one entry from `head_pipeline.status`, so tm3k can
   never disagree with GitLab about what green means (GitLab's own status
   already accounts for `allow_failure`, manual jobs and child pipelines).
   Because cardinality is now a forge fact, `FailingChecks` stops being a fold
   over the entries and becomes adapter-supplied — GitHub computes it exactly
   as before, GitLab from `Pipeline.failedJobsCount` in the same query. The two
   ambiguous rows are pinned: `skipped` emits **zero** entries (a wholly
   skipped pipeline is no signal, and zero entries is bit-for-bit how an empty
   GitHub rollup already behaves), and `manual` is **pending** (blocked on a
   required click, retried each cycle, exactly like a never-finishing check).
   `canceled` is a fail, mirroring GitHub's `CANCELLED`.

   `PipelineStatusEnum` carries **thirteen** values, not the handful named
   above, and the adapter must map all of them or it cannot compile a total
   function. The remainder: `CREATED`, `PREPARING`, `WAITING_FOR_RESOURCE`,
   `WAITING_FOR_CALLBACK`, `PENDING`, `RUNNING` and `SCHEDULED` are **pending**;
   `CANCELING` is a **fail** alongside `CANCELED`, since the cancellation is
   already decided and only the teardown is outstanding. An unrecognised value
   is pending, never pass — the eligibility gate must never fire on no signal
   (ADR 0005).

   **`failedJobsCount` is not a reliable failing count.** A pipeline that never
   materialised any job — refused at creation, e.g. an unverified account
   denied shared runners — reports `status: FAILED` with `failedJobsCount: 0`
   and `totalJobs: 0`. The count is a display detail, so it stays as-is; but
   the adapter must never derive the *verdict* from it. The verdict is the
   status, and the one entry the adapter emits comes from the status alone.

7. **Merge-blocked carries a reason.** `Mergeable` widens from a tri-state to
   `{mergeable, blocked(reason), unknown}` and the Ready row's conflict marker
   generalises to a merge-blocked marker rendering the forge's own reason.
   Forced by GitLab's `NEED_REBASE`, which has no GitHub analogue: on a
   fast-forward project an armed Ready MR would otherwise be attempted and
   rejected every cycle forever with nothing on screen explaining why.

8. **Forge preconditions are a modern-toolchain requirement, and they refuse
   the boot.** tm3k targets a current `glab` against a current GitLab; an
   operator who wants a year-old toolchain gets a hard failure, not a
   half-working instance. Probed once at boot, and the message must name
   **which of the two versions is stale**, because they are different machines'
   problems: `reviewState` is a *server-side* GraphQL field, so a brand-new
   `glab` against an old self-hosted GitLab still cannot fetch it.

   **The squash precondition is not a project field.** `Project.squashOption`
   does not exist; the project exposes only `squashReadOnly` and
   `squashCommitTemplate`, and the squash *policy* lives on
   `BranchRule.squashOption`, which returns a display string (`"Allow"`) rather
   than the `SquashOptionSetting` enum, and is `null` on a branch rule that
   does not override it. Branch rules are a paid-tier feature, so this
   precondition cannot be a tier-independent boot probe as written. What ADR
   0016 actually needs is that the merge call can *request* a squash — a
   per-MR fact (`MergeRequest.squash` / `squashOnMerge`, and the squash
   argument on the merge mutation), not a project capability. The boot probe
   should assert the mutation accepts it, and `squashReadOnly: true` is the
   one project-level condition that genuinely refuses the boot.

   Graceful degradation was designed and then deleted. Narrow-gating merge
   while inbound kept running meant a `merge.available` wire field, an Outbound
   banner, a withheld-Arm state, and a "Forge capability" concept with
   per-capability blast radii — a large surface built to keep stale toolchains
   half-alive. Requiring a current toolchain buys all of that back for one
   sentence in the preflight. What the invariant demanded was that an armed PR
   never merge over an open objection; refusing to run at all satisfies it
   strictly.

9. **Hooks run on both forges, from one prompt composer.** The forge adapter
   declares the CLI vocabulary — binary, diff and comment command shapes, and
   the forbidden approve/merge verbs in *both* porcelain and `api` form — and
   `ComposeNotifyPrompt` stays a single template interpolating it. Six
   harness×forge combinations collapse to one piece of prose over one
   vocabulary table; per-forge prompt copies are ADR 0014's rule violated in
   prose. The ceiling stays prompt-enforced for ADR 0023's unchanged reason:
   `gh api` and `glab api` reach the approve endpoint under any verb allowlist,
   so narrowing one feigns a boundary.

   What is *not* prose is which forge a hook may run against at all — a static
   fact, hard-enforced from declared preconditions. **ADR 0031** owns that
   mechanism; it is deliberately not forge-specific.

10. **The testing line moves from core-vs-seam to pure-vs-I/O.** Normalisation
    is correctness-critical and lives in the seam, so it is extracted as pure
    functions and tested at core weight against recorded raw responses in
    `testdata/`; only exec and wire I/O stay lightly covered. A mis-mapped
    pipeline status silently changes what "green" means, and the shared folds
    cannot catch it — they only ever see the normalised value.

## Layout

`internal/forge` holds the neutral types, the pure folds, the `Client`
interface, the `Vocabulary`, and the `Fake` that backs the engine tests;
`internal/forge/github` and `internal/forge/gitlab` each own their raw decode
types and transport, invisible to everything else.

## Consequences

- **A GitHub candidate set is not portable.** `team-review-requested:` cannot
  be expressed on GitLab; a GitLab operator shapes their selector differently
  (reviewer = me, a label, or all-open-minus-mine). Accepted — the candidate
  set was always forge-native.
- **Rules are forge-scoped by content.** Author include/exclude lists hold
  GitHub logins or GitLab usernames, not both. `@me` resolves either way.
- **The GitHub adapter changes too.** Normalisation is new work on the
  already-working side, and `FailingChecks` moves off the entries. Slice 1 is
  therefore an openly horizontal, openly zero-value refactor whose acceptance
  criterion is that the entire existing suite passes unchanged — the suite is
  the proof of behaviour preservation, which is exactly when a horizontal
  slice is safe to review.
- **The Approval Feed's PR State may degrade on GitLab.** The reviewed-by-self
  pull depends on GitLab's approved-by filter; where it is unavailable the
  state stays `unknown`, which already renders no bar. Display-only — it gates
  nothing.
- **`GET /instance` is a new endpoint** carrying the boot-immutable forge and
  repo, fetched once at mount rather than on the 10s poll — enough for
  forge-named copy ("Open on GitLab"). It carries no capability verdict:
  under decision 8 an instance that boots is an instance that can merge.
  Requires `make check`.
- **GitLab's `Draft:` title prefix is stripped at the seam.** The draft fact is
  carried as a boolean, so leaving the prefix on the title would encode it
  twice and break conventional-commit parsing for every draft. Stripping a
  presentation artifact is decode, not judge.

## Verified against a live instance (issue #75)

The GitLab field names above were written from the schema as understood during
design. Issue #75 checked every one against `gitlab.com` **19.3.0-pre**
(enterprise) with `glab` **1.114.0**, and recorded the responses under
`internal/forge/gitlab/testdata/` — see that directory's `README.md` for what
in a fixture is evidence and what is substituted.

**Confirmed as designed.**

- The §4 batched pull works: pipeline status, `diffStatsSummary`, per-file
  `diffStats { path }`, approval state, reviewer `reviewState`, discussion
  counts and merge status all arrive for a whole page of merge requests in
  **one call**. The N+1 ADR 0007 exists to prevent is genuinely avoided.
- `MergeRequestReviewState` exists and carries `REQUESTED_CHANGES` — plus
  `UNREVIEWED`, `REVIEWED`, `APPROVED`, `UNAPPROVED`, `REVIEW_STARTED`.
- `DetailedMergeStatus` carries `NEED_REBASE`, which §7 is built on. It also
  carries `REQUESTED_CHANGES`, so a requested-changes review is a merge blocker
  on the forge's own terms, not only in tm3k's judgement.
- §5's three seam obligations are natively satisfiable. `Project.mergeRequests`
  takes `authorUsername`, `reviewerUsername`, `reviewState`/`reviewStates`,
  `approvedBy`, `updatedAfter`, `state`, `draft`, `labels` and `iids`, plus a
  `not:` negation input (`MergeRequestsResolverNegatedParams`) that supplies
  exclude-self directly.
- `mergeRequestAccept` takes `squash: Boolean`, so ADR 0016's always-squash is
  expressible per merge.

**Corrected in place.** Decision 6 (the thirteen-value pipeline enum, and
`failedJobsCount` being unreliable as a verdict) and decision 8 (the squash
precondition is not a project field).

**Two further corrections.**

- **`approved` is not "somebody approved".** It means *the approval
  requirement is satisfied*, and on a project requiring zero approvals it is
  `true` for every merge request — including closed and merged ones — while
  `approvedBy` is empty and `approvalsRequired` is `0`. All 41 merge requests
  in the capture read `approved: true` with nobody having approved anything.
  ADR 0013's soft dedup must therefore key off **`approvedBy.nodes` being
  non-empty**, never `approved`; reading `approved` would suppress every
  approval tm3k would ever make on a zero-approval project — the invariant
  inverted into total inaction.
- **Approval is not available over GraphQL, so §4's "`glab api graphql`
  throughout" does not hold.** GitLab exposes `mergeRequestAccept`,
  `mergeRequestRequestChanges` and `mergeRequestDestroyRequestedChanges` as
  mutations, but there is **no approve mutation** — approving is REST-only
  (`POST /projects/:id/merge_requests/:iid/approve`). The GitLab adapter is
  GraphQL for every *read* and must drop to `glab api --method POST` for the
  approve *write*. This does not weaken §9's ceiling argument, which already
  assumed `glab api` reaches the approve endpoint under any verb allowlist.

**Not established.** The `glab` and GitLab version *floors* cannot be derived
from a single current instance; one capture proves 19.3.0-pre and glab 1.114.0
suffice, and nothing about what fails below. Decision 8's preflight still needs
a floor, and picking one is a separate question from this capture.

**Not yet captured.** `MANUAL`, `SKIPPED`, `CANCELED` and `SUCCESS` pipelines,
a genuinely approved merge request, a reviewer at `REQUESTED_CHANGES`, and
`NEED_REBASE`. The capture project's pipelines all fail before any job is
created — gitlab.com withholds shared runners from unverified accounts — so
those pipeline states are unreachable there. Tracked on #72.
