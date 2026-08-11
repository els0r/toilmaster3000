# Context: toilmaster3000

A single-workstation tool that eliminates the toil of approving trivial PRs.
It replaces two hand-rolled bash auto-approvers (`auto-approve.sh`,
`auto-approve-service-a.sh`). Runs on `localhost:8666`; never leaves this machine.

**How to read this file.** This is the domain model: the glossary that names
things and the doctrines that constrain them. Each entry states *what* a thing
is and *which decisions bind it*, then points at the ADR in `docs/adr/` that
holds the mechanics and the rationale. The wire contract of record is the
committed `openapi.json` (ADR 0003); per-package layout is
`docs/architecture.md`. When a new decision crystallises, it lands here as a
lean entry **plus** an ADR — the full mechanism lives in the ADR, never inline.

## Doctrines

Cross-cutting principles the entries below reference by name.

- **Funnel partition** — every PR in a pull lands in **exactly one** terminal
  bucket, so segment counts sum to the raw pull by construction. Holds for the
  inbound Cycle Funnel and the outbound stages at **two different strengths**:
  outbound is a keyed partition whose total is a fold over it (ADR 0025), so
  "by construction" is literal; inbound is a *maintained* invariant — the
  branch precedence in the cycle loop *is* the partition, and the counts are
  mutated by hand alongside it.
- **Gate vs Invariant** (ADR 0005) — a Gate **drops** a PR from consideration
  entirely (never approved, never queued, never shown); an Invariant **diverts**
  a ready PR to the human queue. Filter vs reroute — never conflate them.
- **Today-scoping & the cadence seam** — live surfaces are per-cycle snapshots
  (recomputed, never persisted); ledger surfaces are today-scoped (local
  midnight, workstation timezone) reads of persisted history. The same word can
  carry a different scope per surface — there are deliberately **three
  "approved" numbers**: the funnel bar's *standing* count (dedup members still
  in the pull, any day), the heartbeat's *this-cycle* count, and the feed's
  *today* count. Labels disambiguate; do not "unify" them.
- **One batched call per concern** — everything the cycle needs rides a fixed
  set of batched `gh` calls; per-PR calls are sanctioned only for rare,
  user-consented actions (the Diff card, ADR 0008; the moment-of-merge view,
  ADR 0016). ADR 0007 records the N+1 this doctrine exists to prevent. Hooks
  extend the sanction: **configuring a hook is the consent** for the per-PR
  calls the hook itself makes — tm3k's own cycle stays batched; the hook's
  fetches are the hook's business (ADR 0023).
- **Decode vs judge** — the `gh` seam only decodes GitHub's raw shapes; pure,
  table-tested folds do the judging (`AllGreen`, `CollapsePRState`, the stage
  folds). Never let the seam grow an opinion.
- **Consent model** — inbound autonomy is *rule-driven* (a match approves);
  outbound autonomy is *consent-driven* (only an explicit per-PR **Arm**
  authorizes a merge; no rules, no matching). Only `CHANGES_REQUESTED`
  withdraws consent (ADR 0016, 0019).
- **Engine-performed changes mutate the snapshots in place** (ADR 0018) — a
  change the engine itself performed (manual approve, robot merge) is applied
  to its published snapshots atomically with the ledger write; a change it
  merely *expects* is honest staleness for the next cycle to resolve.
- **An AI species accounts for itself** (ADR 0028) — every AI harness run that
  produced text is transcribed to the Transcript Sink, and the transcript can
  never alter the run's outcome: it is written after the effect has landed, so a
  sink that fails is a logged miss and nothing more. The obligation binds the AI
  *species*, never the Notifier/Screen kinds — a non-AI species owes no account.
- **Fail closed on load-bearing data** — a failed inbound fetch skips the whole
  cycle; a failed outbound or threads fetch clears the outbound snapshot and
  skips all merging that cycle. The robot never acts on stale data (ADR 0016,
  0019).

## Glossary

### Inbound / Outbound (directions)
**Inbound**: PRs others author, flowing toward your approval — verb `approve`,
autonomy rule-driven. **Outbound**: PRs you author, flowing toward merge — verb
`merge`, autonomy consent-driven (the Arm). Each PR lives on exactly one side:
tm3k appends `-author:@me` to the configured inbound search at startup, so the
two pulls are disjoint by construction. "Staging" is an inbound-only word.

### Candidate set — Incoming vs Eligible
The inbound pull: one `gh pr list` per cycle against the global
`--repo`/`TM3K_REPO` + `--search`/`TM3K_SEARCH` (flag overrides env; both
required; not per-rule). Two scopes, two names — do not conflate:
- **Incoming** — the **raw** pull, every PR the search returned this cycle,
  before any gate. The Cycle Funnel's parent set.
- **Eligible candidate** — an Incoming PR that passed both Gates. Only eligible
  candidates reach Rule evaluation.

### Eligibility (Gates)
Two hard-wired Gates — core principles, not user-configurable Rules — evaluated
after the dedup skip and **before** parse, Rules, and the breaking-change
Invariant (ADR 0005):
- **Ready-for-Review Gate** — draft PRs are dropped.
- **All-Green Gate** — eligible only if the pure fold `github.AllGreen` holds:
  **at least one** check rollup entry and every entry passes
  (`SKIPPED`/`NEUTRAL` count as pass). **Empty pipeline ⇒ not eligible** — an
  auto-approver must never fire on no signal. A pending pipeline blocks
  harmlessly: the set is recomputed each cycle, so the PR becomes eligible when
  checks finish.

### Rule
A named, enable/disable-able matching condition, persisted across restarts and
managed in the UI. Every Rule has a **class** determining what a match *does*
(ADR 0004):
- **Approve Rule** — match ⇒ auto-approve. Approve Rules are OR'd.
- **Review Rule** — match ⇒ route to Needs-Human-Review, never auto-approve.
  The user-configurable, softer sibling of the breaking-change Invariant;
  surfaced as the separate "Human Review Always" card, identical editor.

The predicate vocabulary is identical across both classes — only the outcome
differs. A Rule predicates over: **Author** (include/exclude lists, `@me`
resolved via `gh api user`), **parsed title parts** (separate Include/Exclude
regex per `type`/`scope`/`description` — decomposed from v1, never raw-title
regex), and **Diff size** (`DiffMin`/`DiffMax` over `additions + deletions`,
`0 ⇒ unconstrained`, `DiffMin ≤ DiffMax` enforced; the empty-rule guard counts
diff). *Avoid "rule class" as a user-facing term — say Approve Rule / Review
Rule.*

### Conventional-commit title
A PR title parsed into `type(scope)!?: description`. Parsing must be tolerant:
comma/slash-separated, mixed-case scopes (`chore(Team,networking,routing/...)`),
malformed titles exist. A title that does not parse is a **non-match for
everything** — never auto-approved *and* never queued (the parse-gate is
uniform across both Rule classes, ADR 0004). Consequence, accepted: a
malformed-title PR with a huge diff is invisible to a diff-based Review Rule —
conventional commits are mandatory to be seen by the robot at all.

### Auto-approval
The engine autonomously approves every eligible, rule-matching PR **that is not
already approved**, within a cycle of it appearing; no human in the happy path.
The narrowing is the soft dedup (see Approved elsewhere, ADR 0013).

### Hook (Notifier / Screen)
A user-configured action tm3k runs at a named **hook point** — an event the
cycle passes through, never a "stage" (a stage is a bucket a PR sits in). Two
kinds with irreconcilable failure contracts (ADR 0021): a **Notifier** fires
a side effect — output ignored, failure can never block or divert an engine
action, fires **once per PR ever** (persisted fired-ledger, at-most-once); a
**Screen** yields a **Verdict** (`proceed` / `hold`, with reason) gating the
action at its point — keyed per-head so a new push re-screens, and a missing
verdict is **never** `proceed` (never-fire-on-no-signal extends to hooks).
Four points under the pre/post discipline (pre-points carry Screens only,
post-points Notifiers only): `pre_approve`, `post_approve`, `queue_entered`
(rules routed it — the review-assist's home) and `screen_held` (a Screen
diverted it) — separate events, so a just-screened PR never auto-receives a
second AI pass.

**Firing discipline has two axes** (ADR 0026): *cadence* — how often a hook may
fire (Screens per-head, Notifiers once per PR ever) — and *scope* — whether a
Notifier applies to the PR at all. A Notifier's optional `Paths` matches the
changed-file paths riding the cycle fetch (gitignore-style globs: a slashless
pattern matches at any depth). Scope gates the fire *before* it is spent, so an
out-of-scope Notifier keeps its once-per-PR fire and a PR that later grows into
scope still gets its first run. Scope is **Notifier-only** — a scoped Screen
could silently un-gate whole file classes, so the field does not exist on that
kind. Language-keyed review-assists are N flat Notifiers that decline, never a
hook that composes or dispatches other hooks.

**Uncertainty resolves toward the harmless side, and the sides differ by kind**:
a Screen with no verdict never proceeds; a Notifier whose scope is unknown (the
cycle fetch caps files at 100 per PR, detectable against `changedFiles`) fires.
Same principle, opposite direction — a Screen gates, a Notifier cannot.

Screens are a **polled external signal, never an awaited call** (ADR 0022):
the cycle never blocks — a missing verdict dispatches a run and the PR sits
in the Screening segment; any `hold` diverts to Needs-Human-Review (divert,
never drop) carrying every holding screen; three failed attempts synthesize
a hold — the end state is always a human summoned, never silent limbo, never
a walk-through. A hold clears only by new push, manual override, or
disabling the screen.

MVP species are AI-only (ADR 0023): declarative `Harness`/`Model`/`Prompt`
entries in `.config/hooks.yaml`, realized by harness adapters
(`internal/harness` — claude and copilot, ADR 0024; each runs hermetic,
tool-locked to its leg) that fetch the diff themselves and return what the
harness said. Extracting a **Verdict** from that text structurally — never
fabricated in either direction — is the Screen species' work, not the
adapter's (ADR 0028). **A Screen is
defense-in-depth, not a security boundary.** The review-assist Notifier may
comment or request changes, **never approve** — that authority stays with
rules + Screens inbound and the human in the queue.

**`WorkDir` is the one sanctioned breach of hermeticity, and it is a read
grant** (ADR 0027, amending ADR 0024). A Notifier may name an absolute
directory to run the harness process in; ambient *instructions* stay off, but
whatever the harness discovers from a working directory — skills and their
supporting files — comes on. Discovery and reads resolve from the same root, so
the anchor cannot be separated from read access to its subtree: the named
directory **is** the ceiling, and tm3k never widens it. Doctrine: point it at a
purpose-built, skills-only checkout, never a working clone — a working clone
carries untracked files that are untracked for a reason, and sits on whatever
branch you last left it. `WorkDir` is Notifier-only for that second reason: a
Screen judging against a mutable tree is a gate with an irreproducible input.
The tree is ambient and is **not** at the PR's head SHA. Which skill runs is
named in the hook's `Prompt` — the harness-coupling escape hatch (ADR 0026) —
and composition requires the posted review to name the profile it applied, since
tm3k cannot verify that a skill resolved.

### Transcript / Transcript Sink
An AI run's **account of itself**: the harness's result text, recorded verbatim
by the species that ran it (ADR 0028). The **Transcript Sink** is where it goes
— `.state/transcripts.jsonl`, append-only and **write-only**: tm3k never reads
it back, so it is neither a Store nor a Ledger and loads nothing at boot. It
exists for a human with `jq`.

A row is written **iff the run produced text**, and carries no outcome: the
species writes the transcript, the runner writes the verdict, and a fact belongs
to one writer — `verdicts.jsonl` says what was decided, the sink says what was
said. `hook_id` + `number` links a row back to the fire or verdict rows that own
the outcome; `head` and `hook_name` ride along so a row reads without any join.
Both AI species transcribe, which is why the Screen extracts its verdict from
the text *after* recording it: a run that yields no verdict document is exactly
the run whose text you need.

### Approved elsewhere
An Incoming PR GitHub already reports `APPROVED` by someone other than tm3k
(detected via `reviewDecision` on the cycle fetch; number absent from
`approvals.jsonl`). Soft dedup (ADR 0013): tm3k **does not re-approve and
records nothing** — the toil is already gone, and recording it would
double-count saved switches across the team's multiple instances. Shown as a
highlighted "approved elsewhere" row in the funnel's approved stage: a PR
deliberately left alone, not actioned. Invisible to Analytics — correct, it was
not *your* saved switch.

### Needs Human Review (queue)
The actionable inbound panel: PRs **blocked from auto-approval** and routed
here for a human decision. Derived live each cycle, never persisted. Each entry
carries:
- **`reasons` list** (not a single reason): the name of **every** enabled
  Review Rule that matched, plus `"breaking_change"` iff an enabled Approve
  Rule matched and the title is breaking (ADR 0004); a screen-held entry
  instead carries `screen:<name>` per holding Screen (rule reasons XOR screen
  holds, disjoint by construction) plus the prose `screen_holds` field —
  chips stay chips, the AI's reasoning is one click away in the row detail,
  never hidden (ADR 0022).
- **Breaking badge as a display fact**: shown whenever `title_parts.breaking`,
  independent of whether `breaking_change` is a queueing reason; reason chips
  render `reasons` minus `breaking_change` (the badge represents it).
- **Diff magnitude** (`additions`/`deletions`/`changed_files`, riding the cycle
  fetch) rendered as the clickable **Diff pill** → **Diff card** modal (a skim
  aid, not a GitHub mirror; the sanctioned on-demand per-PR fetch — ADR 0008,
  widened to Staging and outbound by ADR 0015/0017). Not shown on the feed —
  diff size is noise when merely verifying the robot behaved.
- An **Approve** button: an explicit human override (`gh pr review --approve`)
  recorded to the feed as `matched_rule: "human approval: <reasons joined>"`
  and removed from the queue snapshot immediately (ADR 0018).

No Dismiss action in MVP (see Deferred).

### Staging
**Eligible, but matched no Rule** — the rules-gap bucket the user actively
drains. Each row carries two buttons, **[+ Approve rule]** and **[+ Human-review
rule]**: a shortcut into the normal Rules CRUD (same `POST /rules`, same editor
and validation — not a new code path) opening the full editor with `Class`
preset and a draft pre-filled **deliberately broad** from the parsed title —
`TypeInclude` anchored (`^<type>$`), `ScopeInclude` the first scope
**un-anchored** (anchoring would break multi-scope titles), author/diff/excludes
blank — so one rule drains the whole type+scope cohort, not one author at a
time. The PR leaves Staging next cycle, when the new rule matches it. Design
goal: Staging grows thinner over time. *(Unparseable-title eligible PRs also
land here; no rule can drain them — an accepted wart under the
conventional-commits-everywhere assumption.)*

### Cycle Funnel (inbound)
The Inbound tab's model of one cycle: **Incoming** is the parent set and every
Incoming PR lands in exactly one terminal stage (funnel partition):

```
INCOMING = Dropped:draft + Dropped:pipeline-red + Staging + Screening
         + Needs-Human-Review + Approved-by-tm3k + Approved-elsewhere
```

**Screening** is the awaiting-verdict segment: eligible, Approve-Rule-matched,
but at least one enabled pre-approve Screen has no verdict yet for the PR's
current head. Transient by design, terminal *within* the cycle — the same
per-cycle disposition semantics as every other segment. Rendered as its own
station between Staging and Needs Human Review: read-only `PrRow`s showing
which screens are pending — no buttons, deliberately nothing to do here (no
re-run, no skip). No heartbeat count either (actionable-signals-only: you
wait, and screen failures reach the strip's `review` count as synthetic
holds). A `proceed` is silent — no badge, no feed annotation; the happy path
stays calm (ADR 0022).

Six stations top-down: **Incoming** (a stacked distribution bar + legend —
counts only, the PRs live in their terminal stations; shows the search as a
code chip), **Dropped** (two side-by-side cards: pipeline-red — with a
failing-check count — and draft), **Staging**, **Screening**, **Needs Human
Review**, and the **Approval Feed**. The distribution bar partitions on **current standing**, so
its Approved-by-tm3k segment is every dedup-member PR still in the pull — not
just this cycle's approvals (see the three-approved-numbers doctrine).

**Cadence seam, accepted:** stations 1–4 are a live per-cycle snapshot; station
5 is today-scoped and persisted — a deliberately wider scope, so the funnel
does not strictly sum across station 5.

### Approval Feed
The read-only, observational ledger panel: approvals tm3k made (auto and
manual), newest-first, **today-scoped** (`approved_at ≥ local midnight`) — it
answers "what did the robot do *today*", empties itself across midnight, and
the seeded historical approvals never render (they live on only as the dedup
set). **No action buttons**; every entry links to GitHub. Entries carry
server-parsed `title_parts` (ADR 0006) and a server-derived `manual` flag: auto
shows the matched rule in a chip; manual shows a "manual override" badge plus
the reasons, so the feed self-documents why a human stepped in. *(Named "feed",
not "inbox" — nothing here awaits action.)*

### PR State
The **live GitHub lifecycle** of an already-approved PR, surfaced on each feed
entry: `open` (green) / `merged` (purple — the happy outcome) / `closed` (red —
closed *without* merging: a deliberately-surfaced robot **false-positive**
signal, not a greyed-out edge case). Volatile and never persisted — distinct
from the immutable approval moment; refreshed each cycle by one batched search
call with keep-last-known semantics (ADR 0007; accepted one-cycle index lag).
Rendered as a 2px full-width bar at the row's top edge in GitHub's Primer
palette; `unknown` renders no bar. Deliberately color-only, no label — the
red/green clash is an acknowledged single-user trade for a calmer feed.

### Rule Draft
The editable projection of a Rule while open in the editor: every field the raw
text the user types (`"" ⇒ unconstrained`), deliberately a different shape from
the wire Rule. The editor exposes the **full predicate vocabulary**, and every
editor surface (rows, validation, round-trip, summary) derives from **one
descriptor-list definition** — the structural fix for the editor that once
silently hid the three title-part excludes (ADR 0020).

### Outbound stages & the Arm
Every authored PR sits in exactly one stage (funnel partition; precedence
top-down): **Outgoing** (the raw pull as a distribution bar; shows the derived
`author:@me` search) > **Draft** (an outbound *stage*, not a gate — your PRs
must be shown) > **Not green** (two cards: pipeline red vs checks running — "go
fix CI" vs "wait") > **Changes Requested** (green but
`reviewDecision == CHANGES_REQUESTED`; the wait is on you) > the three green
stages split **by what blocks the merge**: **Awaiting Approval** (no approval
yet) > **In Discussion** (approved but ≥1 unresolved review thread, ADR 0019) >
**Ready** (green + approved + zero unresolved threads — waiting only on you).
A green unapproved PR with unresolved threads sits in Awaiting Approval —
precedence names the primary blocker. Plus **Merged**, the today-scoped ledger
station (from `merges.jsonl`).

**Armed / Withheld** (verbs Arm / Disarm): a persisted per-PR flag
(`.state/armed.json`) orthogonal to the partition — a badge riding the row,
never a stage. **Default Withheld**: tm3k never merges a PR you didn't
explicitly arm (a gentleman's-agreement approval must never merge on its own).
Arm anywhere **except Changes Requested**: an armed PR observed
`CHANGES_REQUESTED` is disarmed that cycle (level-triggered, no transition
memory), so Armed ∧ Changes-Requested is an impossible state and an open
objection always requires fresh consent. **The exclusion is the declaration**
(ADR 0025): a stage nobody has thought of yet is armable by default, because an
unanticipated stage must never silently withdraw consent. **In Discussion holds, never disarms**
(Armed ∧ In-Discussion is valid): the first cycle the threads read zero it
merges — including when the reviewer's resolve tips it with nobody at the
keyboard; that is what standing consent means (ADR 0019). New pushes do not
clear the arm (arm-while-red is the core use case); entries are cleaned up when
the PR leaves the pull merged/closed.

**Merge**: each cycle, an Armed PR that is green + `APPROVED` + zero unresolved
review threads + `mergeable == MERGEABLE` is merged in tm3k's own loop —
replicating `gh land` (squash, delete branch, commit = PR title + live-fetched
body + `Approved by:` trailer), not GitHub native auto-merge (ADR 0016). A
conflicted Ready row stays in Ready with a **conflict marker** and never
auto-merges — fixing the conflict is on you, which is what Ready means. The
**Discussion gate** is realized structurally: the merge step only walks Ready,
so the stage partition *is* the gate. Threads are the only resolvable comment
species, so only they gate (a bot comment or "LGTM" body would otherwise wedge
the merge forever); **outdated ≠ resolved** — only the explicit resolve click
closes a conversation. Org convention this implies: a nit that must block the
merge lives in an **inline thread**, not the approval's summary text.

**Breaking `!` outbound**: the Arm *is* the human decision the inbound
Invariant guarantees — the row carries the breaking badge (arm with open eyes)
but once armed merges like any other. The inbound Invariant is unchanged.

**Merge ledger**: append-only `.state/merges.jsonl`, written only on successful
merge — required for any Merged display at all (a merged PR leaves the
`is:open` pull immediately). The Merged station is today-scoped and read-only;
range-scoped merge history is an Analytics-tab concern (see Deferred), never a
station-level control.

## Invariants

### Breaking changes are NEVER auto-approved
A hard, global block overriding all rules: a conventional-commit title carrying
`!` is never auto-approved — it goes to Needs-Human-Review, where only a
**manual** override can approve it. v1 detects the title `!` only; the
`BREAKING CHANGE:` body footer would require fetching PR bodies (see Deferred).

## Matching semantics

Each title part has optional **Include** and **Exclude** regexes, matched
case-insensitively; a part with neither is unconstrained. A rule matches when
author include/exclude pass AND every part's Include (if set) matches AND
Exclude (if set) does not. Regex is the single operator (subsumes
equals/contains/one-of); validated by compiling server-side.

**Evaluation order per candidate** (ADR 0004, 0005): dedup skip → Gates
(drop) → parse (non-parse ⇒ non-match for everything) → **Review Rules first**
(collect every matching enabled Review Rule's name; any ⇒ queue, never
approve) → Approve path (first matching enabled Approve Rule; breaking title ⇒
Invariant adds `breaking_change` to reasons) → non-empty `reasons` ⇒ queue with
all reasons; else Approve match ⇒ approve; else Staging.

**Attribution**: approved once (dedup by number); `matched_rule` records the
first matching enabled rule in `rules.yaml` order.

*(Historical: a Review Rule is the realized form of the once-deferred
"Constraint"; "Incoming" now names what an older revision called "the candidate
set".)*

## Analytics

A look-back dashboard over **approval history only** — computed exclusively
from `approvals.jsonl`, the one durable history (ADR 0009). Auto vs Human
Review is the `matched_rule` prefix split (`"human approval: "`); the two
partition all recorded approvals, shares sum to 100%. Queue-but-never-approved
and dropped PRs are invisible — an accepted undercount of true review burden,
by design. Lean-back cadence: fetched on tab-open and control change
(debounced), not on the poll timer.

- **Time ranges** (workstation-local): today / this week (ISO Monday) / this
  month / last X days. **Deltas are elapsed-aligned** — the current partial
  period compares against the *same elapsed slice* of the prior period, never
  partial-vs-full; zero baseline renders "new", never ∞ (ADR 0011 — do not
  "simplify" back to full-period comparison).
- **Stats row**: Auto-approved (count + share), Human Review (count + share;
  shares not delta'd), and the headline **Context switches saved** = the
  auto-approved count (a manual approval is a switch the human *did* take).
  Money is a **range**, `count × [CostLow, CostHigh]`, rendered as the
  read-only money pill — never a single point (ADR 0012); the constants live in
  the Settings tab.
- **By-Type cohort**: the fixed Conventional Commits type set in spec order,
  all rows shown even at zero, plus a trailing `other` bucket (the parser
  accepts any `\w+` type; "permitted" means the spec set, not tm3k-enforced).
  Each row: count + share + auto/human split — the signal is *which types still
  pull a human in*. No per-type delta (jumpy at low counts).
- **Scope filter**: multi-select OR over every scope ever seen in the log
  (all-time union, case-folded, comma/slash-split so one PR can match several);
  scopes the entire view.
- **Aggregation is server-side** in `GET …/analytics`; the tz / month-clamp /
  elapsed math is table-driven-tested Go. The frontend is a pure renderer.

## UI structure

Five tabs under the persistent **heartbeat strip**, active tab in the URL hash
(`#inbound` / `#outbound` / `#rules` / `#analytics` / `#settings`, default
inbound; retired hashes `#review`/`#pipeline` redirect to `#inbound`):

- **Inbound** — the Cycle Funnel, vertical stations on a spine; carries the
  **staging-count badge** (the inbound actionable signal).
- **Outbound** — the authored funnel, same idiom; carries the **ready-count
  badge**. Every station-2–5 row has the Arm/Disarm toggle **except** Changes
  Requested, and armed state rides the row as a badge.
- **Rules** — two cards fed by one `GET /rules`, split by class: Approve Rules
  and "Human Review Always". `Class` is implied by the card's Add button, not
  editable — reclassifying in place means delete + recreate (ADR 0004; a known
  MVP limitation, not a bug).
- **Analytics** — the look-back dashboard.
- **Settings** — the analytics assumption constants (ADR 0010/0012).

The **heartbeat strip** shows the last cycle's time, outcome, and counts —
`approved` (last cycle — the live pulse), `staging`, `review`, `dropped`
(Eligibility-Gate removals; the insurance against silent
approving-nothing-because-a-decode-bug, ADR 0005), `ready`, and `merged`
(**today**, not last cycle — a deliberate asymmetry with `approved`: merges
leave the pull immediately and are rare against the cadence, so a per-cycle
count would read 0 on nearly every glance; the strip's `merged` and the Merged
station read through the same filter so they can never disagree). In Discussion
has **no** heartbeat count — the strip stays actionable-signals-only; `ready`
keeps meaning waiting-only-on-you.

The design mockup (`toilmaster3000.dc.html`, Claude Design project) is
**orientation, not authority**: where it simplified away agreed behavior, the
agreed model wins.

## Engine

Shells out to the `gh` CLI (reuses your auth; no PAT) behind the decode-only
`GitHubClient` seam; a fake backs the engine tests. Per cycle, three batched
calls — inbound list (all rule/gate/funnel fields including `reviewDecision`,
`isDraft`, `statusCheckRollup`, diff counts — no N+1), outbound list
(+`mergeable`), and the GraphQL unresolved-threads search (GraphQL is forced:
`isResolved` exists nowhere else — ADR 0019) — then two tail steps: the batched
PR-State refresh (ADR 0007) and the merge step (ADR 0016).

- **Loop**: one goroutine, `RunCycleOnce(); sleep; repeat` — sleep *after*, so
  a slow cycle can never overlap itself; never a Ticker. Default interval 5m,
  floor 1m (`--poll-interval`/`TM3K_POLL_INTERVAL`). The frontend polls its
  live endpoints every 10s.
- **Failure semantics**: act first, append the ledger only on success (failed
  approvals/merges retry next cycle). One PR's failure is logged and skipped,
  never aborts the cycle. Fetch failures fail closed (see Doctrines).
- **Funnel snapshots** (inbound and outbound): rebuilt each cycle, replaced
  under one lock, never persisted — empty after restart until the first cycle,
  then current as of the last completed cycle, plus the engine's own actions
  since (ADR 0018). Incoming PR objects are not hoarded: the bar renders
  counts; only terminal lists are kept. A failed fetch clears the snapshot.

## Wire, persistence, operational

- **Serving**: a single Go binary `go:embed`s the built React app
  (`frontend/dist`); SPA + API on `localhost:8666`. stdlib mux + **huma v2**
  (ADR 0001): huma does structural validation, semantic guards (regex compile,
  empty-rule check) stay in service code. Prefix `/api/toilmaster3000/v1`.
- **The endpoint inventory is `openapi.json`** — generated from the Go DTOs,
  committed, and drift-guarded by `make check` (ADR 0003). This file does not
  duplicate it.
- **Wire boundary**: snake_case everywhere, owned exclusively by server-side
  DTOs mapped through named converters; engine/domain/disk types never cross
  the wire, and identical-looking DTO/engine pairs are deliberate decoupling —
  do not collapse them (ADR 0002). Title parts are parsed on read, never
  persisted (ADR 0006). PR `state` is zipped in from the engine's in-memory
  map; `approvals.jsonl` stays frozen.
- **Persistence** (no DB; PascalCase YAML config, snake_case JSONL state):
  - `.config/rules.yaml` — both rule classes in one flat list; stable generated
    `id`s; absent `Class` reads as `approve` so pre-class files need no
    migration (ADR 0004). Seeded on first run with two editable examples
    ("team chores", "service-a — teammate_a").
  - `.config/settings.yaml` — the analytics constants; self-healing reseed on a
    stale schema (ADR 0010/0012).
  - `.config/hooks.yaml` — Screens + Notifiers, declarative AI-species
    fields; hand-edited, boot-loaded, preflight-validated; stable generated
    `Id`s self-healed at boot, no CRUD (ADR 0023).
  - `.state/approvals.jsonl` — append-only; both the dedup set (loaded at
    startup) and the feed's source.
  - `.state/merges.jsonl` — append-only merge ledger (ADR 0016).
  - `.state/armed.json` — the persisted armed set; the first mutable per-PR
    state in tm3k.
  - `.state/verdicts.jsonl` — append-only screen-run rows
    (`hook_id, number, head, outcome: proceed|hold|error, reason, at`);
    latest row per key wins (level-triggered), `error` rows are the persisted
    attempt count so 3-strikes survives restarts (ADR 0022). Rows written
    before ADR 0028 spell the key `screen_id`; both load, only `hook_id` is
    written.
  - `.state/hookfires.jsonl` — append-only Notifier fired-ledger
    (`hook_id, number, point, at`), loaded at boot; what makes at-most-once
    restart-safe (ADR 0021).
  - `.state/transcripts.jsonl` — append-only AI-run transcripts
    (`kind, hook_id, hook_name, number, head, at, transcript`), written by both
    AI species, **never read back**: no boot load, no wire surface, and
    therefore no line-length limit — a future reader must not inherit the
    fired-ledger's 1 MB scanner buffer (ADR 0028).
- **Preflight (fail fast at boot)**: `gh` installed and authenticated; `@me`
  resolved once; `:8666` free; and **the configured repo visible to the active
  `gh` identity** — load-bearing because the search API returns an **empty
  result, not an error**, for a repo the identity cannot see: without the
  check, a wrong active account yields perpetual `outcome: ok` with zero counts
  everywhere — silent blindness the `dropped` count cannot catch because it
  lives below the fetch. Boot-time only, decided.
- **Concurrency**: one mutex-guarded in-memory store; HTTP reads are locked
  reads; **all approvals — auto and manual — flow through one locked
  `approve()` path**, making the manual-approve-vs-cycle race safe.

## Testing

Test weight is on the correctness-critical pure core — parser, matcher, folds,
validator, date math — table-driven with `testify/require`; `gh` shell-out and
HTTP handlers get lighter coverage, and the frontend tests pure logic (Draft
round-trip, validation, summary) before DOM. Conventions and single-test
invocations: `docs/development.md`.

## Deferred / not yet modelled

- **Reclassifying a rule in place** — MVP is delete + recreate (ADR 0004).
- **`BREAKING CHANGE:` body-footer detection** — needs fetching PR bodies.
- **Dismiss action** on queue items — needs persistent hidden-state.
- **Outbound analytics** — whether/how a merge counts as a saved switch is
  undecided. Placement decided: a range-scoped merge-history view lives in the
  Analytics tab behind the existing range picker — never as a control on the
  funnel's Merged station (the glance surface stays today-scoped).
- **Commit-message preview at arm time** — the body is fetched live at merge
  time, so an arm-time preview could mislead. Skipped.
- **Next-day PR-State reversal visibility** — PR State inherits the feed's
  today-scope, so a reversal *tomorrow* (notably the red false-positive bar) is
  seen by nobody. Accepted for the glance-tool purpose; a "recent reversals"
  view is a separate feature.
- **Non-AI hook species** (exec/webhook — Slack pings, CI-runner screens) —
  the kind interfaces are the seam; the contract gets designed against a real
  consumer, not speculatively (ADR 0023).
- **Outbound hook points & pre-approve Notifiers** — `pre_merge` would
  second-guess the Arm; a pre-approve warning is an intervention-window
  feature, not a notification (ADR 0021).
- **`ExcludePaths` on Notifier scope** — `Paths` has no negation; excluding
  `vendor/**` or generated files is not expressible. The failure it would
  prevent is one spurious review comment, so it waits until it bites (ADR 0026).
- **Per-hook `Env` override & a hooks UI editor** — hooks.yaml stays
  hand-edited and boot-loaded until either bites; until then hook `gh` side
  effects post as the runtime identity (ADR 0023).
- **Screen analytics** — verdicts.jsonl exists, but Analytics stays
  approvals-only (ADR 0009); hold-rate / screen-efficacy views are a someday
  concern.
