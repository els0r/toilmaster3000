# Context: toilmaster3000

A single-workstation tool that eliminates the toil of approving trivial PRs.
It replaces two hand-rolled bash auto-approvers (`auto-approve.sh`,
`auto-approve-service-a.sh`). Runs on `localhost:8666`; never leaves this machine.

## Glossary

### Approval Feed
A UI panel. A **read-only, observational** view of PRs that have been approved —
both auto-approvals and manual approvals made from the Needs-Human-Review queue —
with the approval timestamp. The user glances to verify the robot behaved; it has
**no action buttons**. Every entry carries a GitHub link.
- **Today-scoped** — the feed shows only approvals from the current local day
  (`approved_at ≥ local midnight`, workstation timezone). It answers "what did
  the robot do *today*," not full history. Consequence: the ~27 seeded historical
  approvals (past timestamps) never render — they live on only as the dedup set —
  and the feed empties itself across midnight as each poll re-filters. The card
  badge counts today's entries (= what is shown). The empty state is "No
  approvals yet today."
- **Per-entry shape** — each entry carries parsed **`title_parts`** (rendered as a
  type icon + scope pills + clean description; see ADR 0006) and a server-derived
  **`manual`** flag. An auto-approval shows the matched rule name in a chip; a
  **manual** entry shows a "manual override" badge **plus** the reasons (the
  `matched_rule` with the `"human approval: "` prefix stripped), so the feed still
  self-documents *why* a human stepped in.

> Note: the user originally called this an "inbox," but that term implies items
> awaiting human action. Since approval is autonomous, "feed" is the precise
> term for this panel; the actionable panel is the Needs-Human-Review queue.

### PR State
The **live GitHub lifecycle** of an already-approved PR, surfaced on each Approval
Feed entry so the user can see what became of the robot's approval. Three values,
collapsed from GitHub's `(state, merged)` pair into one enum and rendered in
GitHub's own palette:
- **`open`** (green) — `state=OPEN`. Approved, not yet merged.
- **`merged`** (purple) — `state=CLOSED` **and** `merged=true`. The happy outcome.
- **`closed`** (red) — `state=CLOSED` **and** `merged=false`. Closed *without*
  merging — a PR the robot approved that a human then rejected/abandoned. This is
  a deliberately-surfaced signal (a robot **false-positive**), not a greyed-out
  edge case.

Distinct from the **approval moment** the feed record captures (immutable,
append-only): PR State keeps changing *after* approval, so it is fetched
out-of-band and is volatile (see Engine / fetch + the wire field below).

**Rendering** — a **2px full-width colored bar at the top edge of each feed row**,
in GitHub's own (Primer) palette, added as new design tokens (light / dark):
`open` green `#1a7f37`/`#3fb950`, `merged` purple `#8250df`/`#a371f7`, `closed`
red `#cf222e`/`#f85149`. **`unknown` renders no bar** (the row looks as it does
today); the existing 1px gray `border-bottom` divider stays as the row separator.
Deliberately **color-only, no label** — accepted because tm3k is a single-user
workstation tool (the red/green color-blind clash is an acknowledged trade for a
calmer feed, not an oversight). The bar coexists with the new-approval flash
overlay. The four-value `state` enum makes the frontend switch exhaustive.

### Needs Human Review (queue)
A UI panel, **actionable**. Lists PRs **blocked from auto-approval** and routed
here for a human decision. A PR lands here for one or more reasons (see
**Reasons** below): it matched ≥1 enabled **Review Rule**, and/or an enabled
**Approve Rule** matched but the breaking-change **Invariant** blocked it. Each
entry has a GitHub link and an **Approve** button: clicking it is an explicit
human override that runs `gh pr review --approve`, records the approval to the
feed, and drops the item from the queue next cycle (it's now in the dedup set).
The feed entry's `matched_rule` is **`human approval: <reasons joined>`** (e.g.
`human approval: osixpatch, breaking_change`) — formatted from the queue item's
`reasons` so the feed self-documents why a human stepped in (replaces the old
fixed `manual (breaking override)` string).
- **Derived live, not persisted** — recomputed each cycle from the candidate
  set; an item leaves naturally when its PR merges/closes or stops matching.
- **Diff magnitude** — each item carries **`additions`**, **`deletions`**, and
  **`changed_files`**, the most decision-relevant fact for a human triaging the
  queue (a 40-line fix vs a 1000-line refactor). All three ride the single cycle
  fetch (see Engine) — no extra `gh` call. Rendered as the **Diff pill**: one
  clickable pill showing green-bold `+N`, red-bold `−M`, and a muted `K files`.
  Shown on the **queue only**, not the feed (diff size is noise when merely
  verifying the robot behaved).
- **Diff pill / Diff card** — the Diff pill (see Diff magnitude) is clickable;
  clicking it opens the **Diff card**, a modal that fetches and renders the PR's
  changes per-file (filename · status · `+N −M`, collapsible — files ≤ 40 changed
  lines start open, larger ones collapsed) so a human can skim the change without
  leaving tm3k. It is a skim aid, **not** a GitHub mirror: the card shows at most
  one page of files (banner: "showing first N of M files"), renders no preview
  for binary / over-large files (which GitHub omits the patch for), and always
  carries an **Open on GitHub** escape hatch. The diff is fetched on demand via a
  per-PR `gh` call — the one sanctioned exception to the no-per-PR-call rule (ADR
  0007), because it is user-triggered, not per-cycle (ADR 0008).
- **Title parts** — each item carries parsed **`title_parts`** (type icon + scope
  pills + clean description; see ADR 0006), rendered by the same component the
  feed uses.
- **Breaking badge (display fact)** — the queue shows a "breaking change" badge
  whenever **`title_parts.breaking`** is true (any `!` title), *independent* of
  whether `breaking_change` is a queueing reason. The reason chips render
  **`reasons` minus `breaking_change`** (the badge represents it), so an
  Approve-tied breaking PR shows the badge without an orphan chip. This widens the
  UI meaning of "breaking" to a display fact; the `breaking_change` **reason**
  stays Approve-tied (see Reasons + Matching semantics).
- **No Dismiss action in MVP.**
- **Reasons** — entry shape carries a **`reasons` list** (not a single reason): a
  PR can be queued for several reasons at once. Built per cycle as: the **name of
  every enabled Review Rule that matches** (all matches, not first), PLUS
  `"breaking_change"` **iff** an enabled Approve Rule also matched and the title
  is breaking. So `chore(osixpatch/approve)!: …` queues with
  `["osixpatch", "breaking_change"]`. `breaking_change` stays tied to an
  Approve-Rule match (a breaking PR matching only a Review Rule lists just the
  Review Rule name).

### Auto-approval
The backend autonomously approves every PR matching a filtering condition **that
is not already approved**, within ~1 minute of it appearing. No human is in the
loop for the happy path. This mirrors the existing scripts exactly — with one
narrowing: a PR GitHub already reports as `APPROVED` by someone other than tm3k is
**left alone** (soft dedup), not re-approved (see Approved elsewhere; ADR 0013).
The toil this removes — the click — is already gone once anyone has approved.

### Rule
A named, enable/disable-able matching condition for PRs. Modelled as a Go struct
**and** managed by the user through the UI (name, define, toggle). Rules persist
across restarts. Every Rule has a **class** that determines what a match *does*:

- **Approve Rule** — match → **auto-approve**. A PR is auto-approved if **any**
  enabled Approve Rule matches it (Approve Rules are OR'd). The two existing
  scripts become the first two Approve Rules (e.g. "team chores",
  "service-a — teammate_a").
- **Review Rule** — match → **route to Needs-Human-Review, never auto-approve**.
  A user-configurable, softer sibling of the breaking-change **Invariant**: where
  the Invariant is a hard-wired global block, a Review Rule is a named condition
  the user defines to gate the robot's blast radius (e.g. an osixpatch approval
  gate, or a large-diff PR). This is the rule-shaped realization of the deferred
  **Constraint** (see below). Surfaced in the UI as a separate **"Human Review
  Always"** card. The matching editor is **identical** to the Approve Rule editor.

_Avoid_: "rule class" as a user-facing term — say Approve Rule / Review Rule.

A Rule predicates over three things:
1. **Author** — include / exclude lists (`@me` is a magic token resolved via
   `gh api user`).
2. **Parsed conventional-commit title parts** — separate conditions on `type`,
   `scope`, and `description` (see below). This decomposition is required from
   v1, not raw-title regex.
3. **Diff size** — total changed lines (`additions + deletions`), gated by an
   optional **`DiffMin`/`DiffMax`** pair (parallel to the title parts'
   Include/Exclude idiom). Both are `int` with **`0 ⇒ unconstrained`** (mirrors
   the `"" ⇒ unconstrained`" string idiom); matches when `DiffMin ≤ size ≤
   DiffMax`. Shared by **both** Rule classes — the predicate vocabulary is
   identical across Approve and Review Rules; only the *outcome* of a match
   differs. **Validation:** the empty-rule guard counts diff (a rule constraining
   only `DiffMin`/`DiffMax` is valid; the rejection message is class-neutral),
   and `DiffMin ≤ DiffMax` is enforced when both are non-zero (`ErrInvalidDiffRange`).

### Conventional-commit title
A PR title parsed into `type(scope)!?: description` (e.g.
`chore(team/service-b): Add hooks` → type=`chore`, scope=
`team/service-b`, description=`Add hooks`). Parsing must be tolerant:
scopes can be comma/slash-separated and mixed-case
(`chore(Team,networking,routing/...)`), and some titles are malformed.
A PR whose title does not parse as a conventional commit is treated as a
non-match (cannot be auto-approved).

## Invariants

### Breaking changes are NEVER auto-approved
A hard, global block that overrides all rules: if a PR's conventional-commit
title carries the breaking-change marker `!` (e.g. `feat(service-a)!: ...` or
`chore!: ...`), it is **never auto-approved**, regardless of which rules match —
instead it goes to the Needs-Human-Review queue, where it can be **manually**
approved. Evaluated before/around the OR-of-rules. v1 detects the title `!` only;
the `BREAKING CHANGE:` body footer is a possible later enhancement (would require
fetching PR bodies, which the candidate-set call does not currently pull).

## Matching semantics

Each title part (`type`, `scope`, `description`) has an optional **Include**
regex and optional **Exclude** regex, matched **case-insensitively**. A part
with neither is unconstrained. A rule matches when author include/exclude pass
AND, for every part, Include (if set) matches AND Exclude (if set) does not.
Regex is the single operator (subsumes equals/contains/one-of); validated by
compiling server-side.

**Attribution:** when several enabled rules match a PR, it is approved once
(dedup by number) and `matched_rule` records the **first matching enabled rule
in `rules.yaml` order**.

### Evaluation order (per candidate)
After the dedup skip, the **Eligibility Gates** run first: if the PR is a draft
(Ready-for-Review Gate) or its pipeline is not all-green (All-Green Gate), it is
**dropped** — logged (`gate: draft|not_all_green`), counted toward the cycle's
`dropped` total, and skipped before the title is even parsed. A dropped PR never
parses, never matches, never queues. Then, for a surviving (eligible) candidate,
the title is parsed and the diff size is known. **The
parse-gate is uniform across both Rule classes:** a title that does not parse as
a conventional commit is a non-match for everything — never auto-approved AND
never queued by a Review Rule. (Consequence: a malformed-title PR with a huge
diff is *not* caught by a diff-based Review Rule; conventional commits are
mandatory to be seen by the robot at all.) For a parsed title: then:
1. **Review Rules first.** Collect the name of every enabled **Review Rule** that
   matches. If any matched, route the PR to Needs-Human-Review — **never
   auto-approve** — regardless of whether an Approve Rule also matches. A Review
   Rule match alone is sufficient to queue (it expands the queue beyond
   "Approve-matched-but-breaking").
2. **Approve path.** Find the first enabled **Approve Rule** that matches. If one
   does and the title is breaking, the breaking-change **Invariant** blocks it:
   add `breaking_change` to the reasons. Otherwise, if no Review Rule queued it,
   auto-approve (attributed to that first Approve Rule).
3. If `reasons` is non-empty → queue with all reasons; else if an Approve Rule
   matched and not breaking → approve; else skip.

A Review Rule is the realized form of what this section used to call the deferred
**Constraint** — a non-title predicate (diff size) that blocks approval and
diverts to Needs-Human-Review — now generalized into a user-configurable,
named Rule class rather than a single hard-wired threshold.

### Candidate set
The PRs the backend pulls once per cycle via a single `gh` call:
the configured **search** (e.g. `is:open team-review-requested:owner/team`)
against the configured **repo** (e.g. `owner/name`). Repo and search are
**global** — supplied at startup via `--repo`/`TM3K_REPO` and
`--search`/`TM3K_SEARCH` (a flag overrides its env var; both are required) —
not per-rule.

**Two scopes, two names (do not conflate):**
- **Incoming** — the **raw** pull, *every* PR the search returned this cycle,
  before any gate runs. This is what `github.ListCandidates()` returns and what
  the **Cycle Funnel** (see below) visualizes as its parent set.
- **Eligible candidate** — an Incoming PR that **passed both Gates** (see
  Eligibility). Only eligible candidates reach Rule evaluation; ineligible ones
  are dropped before any Rule or Invariant runs.

Each enabled Rule is applied as an in-process Go predicate over the eligible
candidates; a PR is approved if any enabled rule matches (minus already-approved
ones). *(Historical note: this section once called the eligible subset "the
candidate set"; the Cycle Funnel made the raw-vs-eligible distinction load-bearing,
so "Incoming" now names the raw pull precisely.)*

### Eligibility (Gates)
A PR must be **eligible** to be a candidate at all. Eligibility is composed of
hard-wired **Gates** — core principles, **not** user-configurable Rules. A PR
that fails any Gate is **dropped entirely**: never auto-approved, never queued,
never shown anywhere in the UI. This is the key distinction from the
breaking-change **Invariant**, which *routes a ready PR to the queue* — a Gate
*removes* the PR from consideration, an Invariant *diverts* it.

Gates are evaluated **before** everything else (before Rules, before the
breaking-change Invariant). The two Gates:

- **Ready-for-Review Gate** — draft PRs are dropped. tm3k only ever deals with
  PRs marked Ready for Review.
- **All-Green Gate** — a PR is eligible only if its pipeline is **all green**.
  Computed in-process from the `statusCheckRollup` array (fetched on the single
  list call). Each entry folds into one of three buckets:
  - **pass** — `CheckRun` completed with `SUCCESS`/`SKIPPED`/`NEUTRAL`;
    `StatusContext` `SUCCESS`.
  - **fail** — `CheckRun` `FAILURE`/`CANCELLED`/`TIMED_OUT`/`ACTION_REQUIRED`/
    `STARTUP_FAILURE`/`STALE`; `StatusContext` `FAILURE`/`ERROR`.
  - **pending** — `CheckRun` not yet `COMPLETED`; `StatusContext`
    `PENDING`/`EXPECTED`.

  **All-Green = at least one entry AND every entry is `pass`** (zero fails, zero
  pendings). Two deliberate calls:
  - **Empty pipeline ⇒ NOT eligible.** "All green" requires a pipeline that ran
    and passed, not the vacuous "nothing ran." This closes the new-PR window
    where checks haven't registered yet (an auto-approver must never fire on no
    signal). Cost: a genuinely check-less repo never auto-approves — acceptable,
    `repo` always runs CI.
  - **Pending blocks harmlessly.** A still-running pipeline isn't eligible this
    cycle; since the candidate set is recomputed from scratch every cycle, the PR
    becomes eligible automatically once checks finish. No persistent "waiting"
    state — it is simply not a candidate yet.

### Cycle Funnel (the queues view)
A UI surface that makes **every Incoming PR of the latest cycle visible**, where
before only approvals (persisted) and the Needs-Human-Review queue (live) were
shown — dropped and uncovered PRs existed only in logs and counts. It models one
cycle as a **funnel**: **Incoming** is the parent set, and every Incoming PR lands
in **exactly one** terminal stage downstream. The stages partition Incoming, so
the counts reconcile *for this cycle*:

```
INCOMING (raw pull, this cycle)
  = Dropped:draft + Dropped:pipeline-red + Staging + Needs-Human-Review
    + Approved-by-tm3k + Approved-elsewhere
```

The Incoming **distribution bar** partitions on **current standing**, so its
**Approved-by-tm3k** segment is *every* dedup-member PR still in the pull — not
just this cycle's new approvals — split visually from the highlighted
**Approved-elsewhere** segment. This means there are **three distinct "approved"
numbers**, deliberately at different scopes (the cadence-seam doctrine, same word
labeled): the bar's *standing* count (dedup members in the pull, any day), the
heartbeat's *this-cycle* count (`approvedThisCycle`), and the ledger's *today*
count (station 5). A PR tm3k approved on a prior day but still open sits in the
**Approved-by-tm3k** segment, is **not** itemized (it's done — in the ledger if
today, history otherwise), and keeps Incoming honest as "everything we saw."

The five stages:
1. **Incoming** — the raw pull (see Candidate set).
2. **Dropped** — PRs an Eligibility Gate removed, split into **two side-by-side
   sub-queues**: **draft** (Ready-for-Review Gate) and **pipeline red** (All-Green
   Gate). These were previously only a combined `dropped` count + per-PR log line.
3. **Staging** — **eligible, but matched no Rule** (`evaluateRules` returned no
   reasons and no Approve match). **Genuinely new — invisible before.** This is the
   stage the user actively drains: each Staging PR carries two actions —
   **[+ Human Review rule]** and **[+ Approve rule]** (see Staging actions) — that
   author a Rule which moves the PR (and its like) out of Staging next cycle. The
   design goal is for Staging to **grow thinner over time** as the robot/human take
   over more PRs for a named, deliberate reason. *(Unparseable-title eligible PRs
   also land here; no rule can drain them, an accepted wart under the
   conventional-commits-everywhere assumption — they should not occur.)*
4. **Needs Human Review** — the existing queue (matched a Review Rule, or an
   Approve Rule blocked by the breaking-change Invariant). Unchanged.
5. **Approval Feed** — the existing today-scoped, persisted feed (`approvals.jsonl`,
   PR State bars). Reused **unchanged**.

**Cadence seam (accepted, documented):** stages 1–4 are a **live per-cycle
snapshot** (recomputed each cycle from Incoming, never persisted — like the queue
today). Stage 5 is **today-scoped and persisted** — a deliberately *wider* scope
than the single cycle. So the funnel does not strictly sum across stage 5: a PR
approved earlier today and since merged is in the feed but gone from this cycle's
Incoming. This seam already exists today (the live queue sits beside the
today-scoped feed); the funnel extends it, it does not introduce it.

### Approved elsewhere (funnel sub-state)
An Incoming PR that GitHub reports as **already approved by someone other than
tm3k** — a human, or (increasingly) a **teammate's tm3k instance**. Detected by
adding **`reviewDecision`** to the cycle fetch (rides the same single `gh pr list`
call, no N+1): a PR with `reviewDecision == APPROVED` whose number is **absent
from `approvals.jsonl`** was approved elsewhere.
- **Behavior — existing approval is a soft dedup.** When GitHub already says
  `APPROVED`, the toil is already gone, so tm3k **does not add a redundant
  approval** and **records nothing to `approvals.jsonl`** (the feed stays the
  robot's *own* ledger). The PR shows as a **highlighted "approved elsewhere" row**
  in the funnel's approved stage — a PR tm3k **deliberately left alone**, not one
  it actioned.
- **Analytics consequence (correct).** Approved-elsewhere PRs never enter
  `approvals.jsonl`, so they are **invisible to Analytics** — right, they were not
  *your* saved switches. This prevents double-counting now that multiple tm3k
  instances run on the team.

### Staging actions (drain a PR with a rule)
Each Staging row carries two buttons — **[+ Approval rule]** and **[+ Human-review
rule]** — that open the **full shipped rule editor** (see Rule Draft) with `Class`
preset by the button and a draft **pre-filled broad, from the PR's parsed title**:
- **`TypeInclude` = `^<type>$`** (anchored — `type` is a single `\w+` token).
- **`ScopeInclude` = `<first scope>`, UN-anchored** (a substring regex against the
  raw `c.Scope` string — anchoring would break matching on multi-scope titles like
  `team/service-b`; this mirrors the seed `service-a — teammate_a`).
- **Author, Diff, all Excludes left blank.** Deliberately **broad** (type+scope,
  no author) so one rule drains the **whole type+scope cohort** at once — staging
  thins faster and rules don't proliferate one-author-at-a-time. The user narrows
  (add author/diff) before saving if they want.
- An auto-generated **name** (`<scope|type> auto-approve` / `… review`) and a
  back-reference to the originating PR, both editable.

The buttons are a **shortcut into the normal Rules CRUD**, not a new code path:
they reuse `POST /rules` and the same editor/validation. The PR leaves Staging
**next cycle**, when the new rule matches it (into approve or human-review). The
editor keeps the **complete predicate vocabulary** (all six title-part
include/exclude + `DiffMin`/`DiffMax`); the design mockup's thinner modal is **not**
adopted.

### UI layout (tabs)
The app is **three tabs** under a persistent heartbeat strip (see Cycle status):
- **Pipeline** — the **Cycle Funnel** (see above): the daily-glance surface,
  rendered as a **vertical top-down funnel** of five numbered stations on a
  connecting spine. Replaces and subsumes the former **Review** tab (the
  Needs-Human-Review queue and Approval Feed are now stations 4 and 5 of the
  funnel). Carries a **staging-count badge** (the new actionable signal — uncovered
  PRs awaiting a rule) so it stays visible from the Rules tab.
- **Rules** — the Rules section (the occasional-config surface).
- **Analytics** — the approval-history dashboard (the look-back surface; see
  Analytics).

**Pipeline station layout** (vertical, top→bottom, anti-clutter by design):
1. **Incoming** — a single **stacked distribution bar** (not a PR list): the raw
   pull partitioned into its terminal stages as colored segments + a legend with
   counts. The actual PRs live in their terminal stations below, so Incoming stays
   a one-glance summary. Shows the configured filter expression as a code chip.
2. **Dropped** — **two side-by-side cards**, pipeline-red and draft.
3. **Staging** — amber-themed; each row carries the two rule-creation buttons.
4. **Needs Human Review** — the existing queue (Approve button + Diff pill retained).
5. **Approval ledger** — the existing today-scoped feed (PR State bars retained;
   approved-elsewhere rows highlighted — see Cycle Funnel).

The design mockup (`toilmaster3000.dc.html` in the Claude Design project) is
**orientation, not authority**: where it simplified away Q1-agreed behavior
(it hid already-approved/approved-elsewhere PRs and omitted PR State bars), the
agreed model wins.

The three surfaces have different cadences (Pipeline watched constantly, Rules and
Analytics touched rarely), so they are not co-scrolled. The active tab lives in the
**URL hash** (`#pipeline` / `#rules` / `#analytics`, default Pipeline) — a reload
keeps your place and each tab is linkable, with no router dependency. *(`#review`
should redirect to `#pipeline` so old links survive the rename.)*

### Rules section (UI)
The actionable config surface (the Rules tab): lists rules, lets the user
create/name/edit/enable/disable them. Rendered as **two cards** fed by one
`GET /rules`, split by `Class`:
- the **Approve Rules** card, and
- a separate **"Human Review Always"** card for Review Rules.
The editor is **identical** in both. **`Class` is implied by the card** (the
card's "Add" sets `class`), not an editable field — so a rule **cannot be
reclassified in place**; moving it between cards means delete + recreate (a known
MVP limitation, not a bug — the redesign's modal carries **no behavior/class
toggle**, upholding ADR 0005). The `/rules` CRUD endpoints are reused unchanged
with `class` added to `RuleBody` (snake_case wire).

The editor exposes the **full predicate vocabulary** the model defines: author
include/exclude, **Include and Exclude** for each title part (type, scope,
description), and the `DiffMin`/`DiffMax` pair — every predicate a rule can carry
is visible and editable. (This replaces an earlier editor that hid the three
title-part *excludes* and carried them through invisibly on edit; a partially-
exposed rule reads as complete when it is not.)

### Rule Draft
The **editable projection of a Rule** while it is open in the editor modal: the
same predicate vocabulary, but every field held as the **raw text the user
types** — authors as a comma-separated string, each title part as a regex
string, the diff bounds as numeric-input strings (`"" ⇒ unconstrained`). The
Draft and the wire **Rule** are deliberately different shapes: the Draft is what
a human edits, the Rule is what crosses the wire. Saving maps a Rule Draft back
onto a Rule — dropping empty predicates and stamping the card-implied `Class`.

The predicate vocabulary is named **once**, matching the model's three kinds
(Author, Title parts, Diff size): the six title-part include/exclude fields as a
single descriptor list, Author and Diff size as their own small mappings. The
modal rows, the per-field regex validation, the constrains-nothing guard, the
Draft↔Rule round-trip, and the row summary all **read from that one definition**
rather than each re-listing the fields. Consequence: a predicate added to the
model reaches the editor by adding **one descriptor** — there is no second place
to forget, which is the structural form of the bug that once let the editor
silently hide the three title-part excludes. Validation note: authors are **not**
regex-checked on the client (only the six title-part regexes are); author
patterns are validated server-side, and diff `0` is treated as unconstrained
(same as empty), not as a constraining bound.

## Analytics

The **Analytics tab** is a look-back dashboard over the **approval history** — the
durable record in `approvals.jsonl`. It answers "how much toil did the robot save,
and on what." Its cadence is occasional (lean-back), not live: fetched on tab-open
and on each control change (debounced), **not** on the 10s poll timer.

### Approval history (the only analyzable signal)
Analytics is computed **exclusively from `approvals.jsonl`** — the one durable
history tm3k keeps. Two consequences fix the whole feature's meaning:
- The **Needs-Human-Review queue is never persisted** (derived live each cycle) and
  **dropped PRs** exist only in logs. So analytics can describe **approvals**, not
  the full review burden. A PR that hit the queue but was closed/merged by someone
  else — or still sits there — was never approved, so it is **invisible** to
  analytics. Accepted: this is an *approval*-history view, no new persistence.
- Within the log, **auto vs human is the `matched_rule` prefix**: an entry whose
  `matched_rule` starts with `"human approval: "` is a **Human Review** approval (a
  human stepped in via the queue Approve button); everything else is
  **Auto-approved**. The two partition all recorded approvals — `auto + human =
  total`, so their shares sum to 100%.

### Time range (lightweight Grafana-style picker)
Four selectable ranges, all in **workstation-local time** (same local-midnight
basis as the Approval Feed):
- **today** — local midnight → now.
- **this week** — **Monday** 00:00 (ISO 8601) → now.
- **this month** — calendar 1st 00:00 → now.
- **last X days** — rolling `X×24h` ending now (includes today's partial).

The three named ranges are **in-progress** (partial) when viewed.

### Previous period (elapsed-aligned delta)
Every headline stat carries a **relative change vs the previous period**, computed
**like-for-like**: only the *elapsed slice* of the current period is compared
against the **same elapsed slice** of the prior period — never partial-vs-full.
- **today** vs **yesterday 00:00 → same clock time**.
- **this week** vs **last week, Monday → same weekday+clock offset**.
- **this month** vs **last month, day-1 → same day-of-month+clock offset**;
  **clamped** when the current day-of-month has no counterpart (e.g. viewing on the
  31st caps last month at its final instant).
- **last X days** is already equal-length, so its previous period is the
  immediately-preceding `X×24h` window — like-for-like for free.

A delta is the **%-change of the count** `(now − prev)/prev`, rendered as an
up/down arrow + color + `±N%`. **Zero baseline** (prev = 0) renders **"new"**
(or **"—"** when both are zero) — never ∞ or a divide-by-zero. The delta label
names the aligned comparison (e.g. "vs last week, Mon–Wed aligned").

### Stats row
Three headline stats for the selected range (+ scope filter), each with a delta:
- **Auto-approved** — count + **share** (% of total approvals in range).
- **Human Review** — count + share. (Shares are shown for the current range but
  **not** delta'd — a share-point delta beside a count delta misreads.)
- **Context switches saved** — *the headline value*. **`count = the Auto-approved
  count`** (each auto-approval is one interruption the human did not take; a Human
  Review approval is a switch the human *did* take, so it is **not** saved). Shown
  two ways: the raw count, and **money as a range** = `count × [CostLow, CostHigh]`
  (prefixed with `Currency`) — a low/high band, never a single point, because the
  per-switch cost is a wide distribution, not a number (see the Zurich research).
  The band is rendered as the **money pill**: the count-scaled `CHF570 – CHF1486`
  on top with the per-switch basis `CHF10–26 / switch` beneath; an empty range
  collapses to a single `CHF0`. The per-switch constants are **not** edited here —
  the pill is read-only; they live in the Settings tab. *There is no time/hours
  figure:* dropping the single hourly-rate (for a direct per-switch franc band)
  detached money from an `hours × rate` chain, so the "X.Xh saved" line is gone and
  money stands alone.

### By-Type cohort
A breakdown of the range's approvals **by conventional-commit type**. The type axis
is the **fixed Conventional Commits set** — `feat, fix, chore, docs, style,
refactor, perf, test, build, ci, revert` — rendered in that order, **all rows
shown** even at zero (zeros dimmed), honoring "the set is bounded." A trailing
**`other`** bucket catches any non-standard `\w+` type the parser accepted (tm3k
does **not** restrict types — the parser's type is `\w+`; "permitted" means the
*spec* set, not a tm3k-enforced one). Each row shows **count + % of range total +
the auto/human split** (`55 auto / 4 human`) — the actionable signal is *which
types still pull a human in*. **No per-type delta** (jumpy at low counts).

### Scope filter
A **multi-select (OR)** filter over **scopes**. Options enumerate **every scope
ever seen** in the log (all-time union via `splitScopes`, case-folded) — a stable
list independent of the selected range, presented as a searchable control (the real
log already holds 45+ distinct scopes). A PR matches when its scope set contains
**any** selected scope (a title's scope is comma/slash-split into multiple scopes —
`chore(team/service-b)` → `team`, `service-b` — so one PR can match several). The
filter scopes the **entire view** — stats row, By-Type cohort, and all deltas
recompute for the selected scopes. Default: **All scopes** (no filter).

### Aggregation (server-side)
All analytics math runs **server-side** in a new `GET …/analytics` handler: it
reads the full `approvals.jsonl`, parses each title (reusing the
conventional-commit parser + `splitScopes`), computes the range and
elapsed-aligned previous-period boundaries (Go owns the tz / month-clamp / elapsed
math — table-driven tested, per the project's "test weight on pure logic" ethos),
buckets by type, applies the scope filter, and returns aggregates + the all-time
scope list as a DTO (ADR 0002, snake_case wire; titles parsed on read, ADR 0006).
The frontend is a **pure renderer** — range + scope live in component/URL state and
trigger a debounced re-fetch; the correctness-critical date logic never leaves Go.
Empty range → all zeros, deltas render "—".

## Engine

- **GitHub access:** shell out to the `gh` CLI (reuses existing auth; no PAT).
  - Fetch (once/cycle): `gh pr list --repo example/repo --search
    "is:open team-review-requested:example/team"
    --json number,title,author,url,additions,deletions,changedFiles,isDraft,statusCheckRollup,reviewDecision`
    (author from `author.login`). **`reviewDecision`** rides the **same single
    call** (no N+1): `PR` carries `ReviewDecision string`
    (`APPROVED`/`CHANGES_REQUESTED`/`REVIEW_REQUIRED`/empty), feeding the
    **approved-elsewhere** funnel sub-state (a PR `APPROVED` but absent from
    `approvals.jsonl` was approved by someone other than tm3k — soft-dedup, see
    Cycle Funnel). `additions`/`deletions`/`changedFiles` come back
    in the **same single call** (no per-PR N+1); `PR` carries them **separately**
    and the matcher sums `additions + deletions` for the diff-size predicate.
    `additions`/`deletions`/`changedFiles` are also surfaced on `QueueItem` for
    the queue's diff-magnitude line (`PR` carries `ChangedFiles int`).
    `isDraft` and `statusCheckRollup` ride the **same single call** to feed the
    Eligibility Gates with no extra N+1 — `PR` carries `IsDraft bool` and
    `Checks []Check` (one `Check` per rollup entry: `{Typename, Status,
    Conclusion, State}`). The all-green verdict is the pure, table-driven-tested
    `github.AllGreen(checks)`; the CLI seam only decodes, it does not judge.
  - Approve: `gh pr review --repo … --approve <number>`.
  - **PR-State refresh (out-of-band):** at the **tail of every cycle** (after
    find→approve, on every cycle even one that approved nothing), refresh the
    **PR State** of **today's feed entries only** via **one batched call**
    (ADR 0007 — was per-PR `gh pr view`, an N+1 that did not survive a higher
    cadence): `gh pr list --repo … --state all --search "reviewed-by:@me
    updated:>=<startOfLocalDay>" --json number,state,mergedAt --limit 200`. The
    bot only ever *approves*, so `reviewed-by:@me` ≡ approved-by-me; the seam
    returns a `map[number]raw` superset and the **engine intersects it against
    today's feed numbers locally**. Each `(state, mergedAt)` pair collapses to the
    `open|merged|closed` enum: `OPEN→open`, `CLOSED` with a non-null
    `mergedAt`→`merged`, `CLOSED` with null `mergedAt`→`closed`. Result held in an
    **in-memory `map[number]state`**, never persisted (see PR State), and updated
    **in-place** — a number absent from a result **keeps its last-known state**
    (never reset to unknown). Failure is **all-or-nothing**: a failed call is
    **logged once and skipped** (keeps all last-known state), never aborts the
    cycle. Trade-off (ADR 0007): GitHub search is eventually-consistent, so a PR
    approved *this* cycle may read `unknown` for one cycle before resolving —
    cosmetic, self-healing, and the window shrinks as cadence rises. The `--limit
    200` is guarded by a **warning logged when the result hits the limit** (no
    silent truncation).
- **`gh` seam:** a `GitHubClient` interface (`ListCandidates() []PR`,
  `Approve(number) error`, `PRStatesSince(since) (map[number]raw, error)`) — real
  impl shells out to `gh`; a fake impl backs tests so the engine is testable
  without network. `PRStatesSince` is **decode-only** (returns GitHub's raw
  `{state, mergedAt}` per number); the engine intersects against today's feed, and
  the pure, table-tested **`github.CollapsePRState(state, mergedAt)`** does the
  judging into `open|merged|closed` — same decode-vs-judge split as `AllGreen`.
- **Loop:** single goroutine, `runCycle(); sleep(60s); repeat` (sleep *after* —
  a slow cycle can never overlap itself). Not a Ticker.
- **Failure semantics:** approve first, append to `approvals.jsonl` only on
  success (failed approvals retry next cycle). One PR's failure is logged and
  skipped, never aborts the cycle. A failed candidate fetch skips the whole
  cycle (approves nothing).
- **Funnel snapshot (live, never persisted):** each cycle, alongside rebuilding
  `queue`, the engine retains what it used to discard — the **dropped-red**,
  **dropped-draft**, **staging**, and **approved-elsewhere** lists, plus the
  **distribution counts** and **approvedThisCycle** (see Cycle Funnel). Held in
  one in-memory snapshot replaced under lock at cycle end (same lifecycle as
  `queue`: empty after restart until the first cycle, current as of the last
  completed cycle). **Incoming PR objects are not hoarded** — Incoming renders as
  the distribution bar (counts), so only the four terminal lists + counts are
  kept. A failed fetch clears the snapshot (nothing evaluated). Exposed at
  `GET /pipeline`; the dropped-red items carry a **count of non-passing checks**
  ("N checks failing"), folded cheaply from the `statusCheckRollup` already in hand.

### Cycle status (UI)
A small status line (the persistent **heartbeat strip** atop all tabs) showing
the last cycle's time, outcome (`OK` / `gh error: …`), and counts (e.g.
`approved 3, staging 6, review 2, dropped 5`), so a glance confirms the robot is
alive — not just that it approved things. **`staging`** joins the strip as the new
actionable signal (uncovered PRs awaiting a rule), beside `approved`/`review`
(queue)/`dropped`. The heartbeat's **`approved` count is the last
cycle's** (the live pulse) — deliberately a different scope from the
**today-scoped** Approval Feed, so "approved 3" in the strip and N rows in the
feed are the same word at two scopes (cycle vs day), disambiguated by labels
(the feed card reads "today · read-only"). The
**`dropped`** count is the number of candidates the Eligibility Gates removed
this cycle (draft or not-all-green, combined into one count; the per-PR `gate`
reason lives in the logs). It is cheap insurance against the scariest
auto-approver failure mode — *silently* approving nothing because a
`statusCheckRollup` decode bug marks everything not-green; without the count that
is indistinguishable from "saw no PRs." A failed candidate fetch records
`dropped 0` (nothing was evaluated), parallel to `approved`/`queue`.

## HTTP API & serving

- **Single Go binary** `go:embed`s the built React app (`frontend/dist`); serves
  SPA + API on `localhost:8666`. Dev uses the vite dev server proxying to the Go
  API.
- **Framework:** stdlib `net/http` mux (Go 1.22+ routing) + **huma v2** for
  request/response validation and a generated **OpenAPI** spec. Handlers are
  typed Go structs with huma tags. (Supersedes the initial "plain vanilla
  handlers" intent — see ADR 0001.)
- **Validation split:** huma covers *structural* validation (required fields,
  types). *Semantic* guards stay in service code: regex-compiles and the
  reject-empty-rule check (these can't be expressed as schema tags).
- **Prefix:** `/api/toilmaster3000/v1` (verbose but unambiguous).
- **Wire casing:** the REST API JSON is **snake_case everywhere**, regardless of
  on-disk format (PascalCase YAML, snake_case JSONL).
- **Wire boundary (DTOs everywhere):** the wire shape is owned exclusively by
  `server`-side DTOs (`server.Approval`, `server.QueueItem`, `server.CycleStatus`,
  `RuleBody`). Engine/domain and on-disk types **never appear on the wire** —
  each handler maps through a named converter (`approvalToBody`, `queueItemToBody`,
  `cycleStatusToBody`, `ruleToBody`). The snake_case convention is shared across
  the boundary **by agreement**, not by reusing a struct: `engine.Approval`'s json
  tags exist for the `approvals.jsonl` disk format, and `server.Approval`
  independently chooses the same casing for the wire. The two being byte-identical
  today is deliberate decoupling, not redundancy — see ADR 0002. (Do not collapse
  the DTOs into the engine types; that re-leaks wire concerns into the engine.)
  - **Shared DTO sub-struct:** `server.TitleParts {type, scopes[], breaking,
    description}` rides **both** `server.Approval` and `server.QueueItem`,
    computed in the converters at response time (**parse-on-read**, never
    persisted — see ADR 0006). Sharing a *server-owned* DTO type across handlers
    does not violate ADR 0002 (which forbids reusing *engine/domain* types).
  - **Added wire fields:** `server.Approval` gains `title_parts`, a
    server-derived **`manual bool`** (from the `matched_rule` prefix), and a
    server-derived **`state`** enum (`open|merged|closed|unknown`, the live **PR
    State**); `server.QueueItem` gains `title_parts`, `additions`, `deletions`,
    and `changed_files`. The raw `title` stays on both as source of truth.
    - **`state` is sourced outside the `engine.Approval` record.** Unlike the
      other derived fields (computed purely from the record), `state` is read
      from the engine's in-memory `map[number]state` and zipped into the feed by
      `approvalToBody` under one lock. `engine.Approval` and `approvals.jsonl`
      stay **frozen** — PR State is volatile and never persisted (see PR State /
      Engine). Default when the map has no entry yet: `unknown`.

| method | path | purpose |
|---|---|---|
| `GET` | `/api/toilmaster3000/v1/status` | cycle status: `last_run`, `outcome`, counts (approved / staging / in queue / dropped) — `staging` rides `/status` so the always-polled heartbeat strip shows it from every tab without fetching `/pipeline` |
| `GET` | `/api/toilmaster3000/v1/approvals` | feed, newest-first, **today only** (`approved_at ≥ local midnight`; reads `approvals.jsonl`) |
| `GET` | `/api/toilmaster3000/v1/queue` | Needs-Human-Review items (derived live) |
| `POST` | `/api/toilmaster3000/v1/queue/{number}/approve` | manual override approve |
| `GET` | `/api/toilmaster3000/v1/pipeline` | live **Cycle Funnel** snapshot: `dropped_red[]`, `dropped_draft[]`, `staging[]`, `approved_elsewhere[]`, distribution counts, `approved_this_cycle`. Derived live each cycle, never persisted (stations 4–5 reuse `/queue` + `/approvals`) |
| `GET` | `/api/toilmaster3000/v1/analytics` | approval-history aggregates for a range (+ scope filter): totals, shares, switches-saved, by-type cohort, elapsed-aligned deltas, all-time scope list. Query: `range=today\|week\|month\|days`, `days=N` (for `days`), `scope=a,b` (repeatable/CSV) |
| `GET` | `/api/toilmaster3000/v1/settings` | analytics assumption constants: `cost_low`, `cost_high` (CHF per saved switch), `currency` |
| `PUT` | `/api/toilmaster3000/v1/settings` | update the constants (full replace) |
| `GET` | `/api/toilmaster3000/v1/rules` | list rules |
| `POST` | `/api/toilmaster3000/v1/rules` | create a rule |
| `PUT` | `/api/toilmaster3000/v1/rules/{id}` | update / enable / disable (full replace) |
| `DELETE` | `/api/toilmaster3000/v1/rules/{id}` | delete a rule |

- Each rule has a **stable generated `id`** (not derived from the user-editable
  name), stored in `rules.yaml`.
- **Polling:** backend cycles every 60s; frontend polls `status` + `approvals` +
  `queue` every 10s; rules refetched after a mutation.

## Persistence

Repo is self-contained (will be relocated later). Layout:
`toilmaster3000/{main.go, internal/, frontend/, docs/adr/, CONTEXT.md,
.config/, .state/}`.

- **`.config/rules.yaml`** — rule definitions (both classes in one flat `Rules:`
  list), loaded at startup, rewritten on every UI edit. YAML keys are
  **PascalCase** (e.g. `Name`, `Enabled`, `TypeInclude`, `Class`, `DiffMin`,
  `DiffMax`). A **`Class`** field (`approve` | `review`) discriminates the two
  Rule classes; an **empty/absent `Class` is treated as `approve`**, so the
  existing two seeds and any pre-existing file need **no migration** (the next
  mutation rewrites them with explicit `Class: approve`). (One non-stdlib
  dependency: a YAML lib.)
- **`.state/approvals.jsonl`** — append-only log, **one approval per line**. Serves as
  both the dedup set (numbers loaded into memory at startup) and the Approval
  Feed's data source. Record shape (**snake_case**):
  `{ number, title, author, url, matched_rule, approved_at }`.
- **`.config/settings.yaml`** — the analytics assumption constants, the **first
  non-rule persisted state** in tm3k. PascalCase YAML (matching `rules.yaml`):
  `CostLow: 10`, `CostHigh: 26`, `Currency: "CHF"` — the low/high CHF cost of one
  saved context switch (Zurich research: `10 min × CHF1.00/min gross` …
  `23 min × CHF1.15/min loaded`). Loaded at startup, rewritten on `PUT /settings`,
  seeded with those defaults on first run. **Self-healing migration:** a file
  missing the cost keys (the pre-range `MinutesPerSwitch`/`HourlyRate` schema, or a
  zeroed file) is reseeded to the full defaults on load, so the headline can never
  come back `CHF0 – CHF0`. Drives the Context-switches-saved **money range** (see
  Analytics); the constants are edited only in the Settings tab.
- **Seeding (rules):** on first run (no `.config/rules.yaml`), write two starter
  default rules (both enabled) as editable examples, so the tool does something
  sensible out of the box:
  - `team chores` — `AuthorsExclude: ["@me"]`, `TypeInclude: "^chore$"`,
    `ScopeExclude: "renovate"`.
  - `service-a — teammate_a` — `AuthorsInclude: ["teammate_a"]`, `ScopeInclude: "service-a"`.

## Operational

- **Preflight (fail fast at boot, clear message):** verify `gh` is installed and
  authenticated (`gh auth status`); resolve the `@me` token once via
  `gh api user`; exit if `:8666` is already in use. A broken `gh` exits hard
  rather than silently approving nothing.
- **Concurrency:** one mutex-guarded in-memory store (rules, dedup set, last
  cycle status, latest queue snapshot); HTTP reads are locked reads. **All
  approvals — auto and manual — flow through one locked `approve()` path**
  (check dedup → `gh` → append JSONL under lock), making the
  manual-approve-vs-cycle race safe.

## Testing

- **Test weight is on pure logic** (parser, matcher, validator) — the
  correctness-critical core. UI and shell-out get lighter coverage.
- **Go** (table-driven + testify): parser (no-scope, breaking `!`,
  multi/slash/mixed-case scopes, malformed doubled-prefix, non-conventional →
  non-match); matcher (author & per-part include/exclude, OR-of-rules,
  first-match attribution, breaking → queue; **diff-size DiffMin/DiffMax bounds;
  Review-vs-Approve precedence; reasons-list accumulation including all matching
  Review Rules + Approve-tied breaking_change; empty-Class ⇒ approve**);
  validator (reject-empty-rule **incl. diff**, bad-regex-per-field,
  **DiffMin ≤ DiffMax**); **`github.AllGreen` fold (pass/fail/pending buckets,
  SKIPPED/NEUTRAL as pass, empty rollup ⇒ not green, mixed CheckRun +
  StatusContext); Eligibility Gates in the cycle (draft dropped, not-all-green
  dropped, both before parse/rules; `dropped` count); **`github.CollapsePRState`
  fold (open / merged / closed-unmerged / defensive default)**. gh impl + http
  handlers lightly tested.
- **Frontend** (vitest): the three panels (feed, queue, rules editor) + cycle-
  status line; the 10s polling hook and queue **Approve** action, API mocked.
  **PR-State bar**: correct color token per `state` and **`unknown` renders no
  bar**. The **Rule Draft** round-trip, validation (constrains-nothing incl. diff
  `0`, inverted range, per-field title-part regex), and summary are tested
  **pure** (no DOM); the rules-editor DOM test keeps only modal interaction (one
  row per title-part field, Save disabled on invalid, mutations fire).

## Deferred / not yet modelled

- **Reclassifying a rule in place** (moving between the Approve and "Human Review
  Always" cards) — MVP requires delete + recreate; `Class` is card-implied.
- **`BREAKING CHANGE:` body-footer detection** — needs fetching PR bodies.
- **Dismiss action** on queue items — needs persistent hidden-state.
- **Next-day PR-State reversal visibility** — PR State is **same-day-only** (it
  inherits the feed's today-scope): a PR approved today but merged/closed
  *tomorrow* is already off the feed, so its `merged`/`closed` bar (notably the
  red **false-positive** signal) is seen by nobody. Accepted for the glance-tool
  purpose; a "recent reversals" view or persisting state is a separate feature.
