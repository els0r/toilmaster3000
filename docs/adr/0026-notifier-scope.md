# Notifier scope: firing discipline's second axis

## Context

The first real review-assist use is polyglot. A PR carrying Go code and a bash
script wants a Go-specific review on the `.go` files and a bash-specific one on
the script, each keyed to its language's domain — the `/golang-pr-review` and
`/bash-pr-review` split, expressed as hooks. The hook model is flat:
`NotifierRunner.Fire` fires every enabled Notifier attached to the point,
unconditionally. A Go review-assist configured for a repo therefore comments on
bash-only PRs too, burning its once-per-PR fire on a review of nothing.

The instinct was a **meta-hook**: one hook that inspects the PR, composes the
matching prompts, and dispatches them. That reading is wrong twice over.
`examples/hooks.yaml` already documents N Notifiers sharing one point ("one
review-assist per language"), and the fired-ledger already keys
`(hook_id, number, point)` so siblings never collide — the flat model *already*
carries N reviewers. What is missing is not composition but **selection**.

Composition would also cost what the flat model gets for free. One composed run
is all-or-nothing, and at-most-once turns its crash into a logged miss for every
language at once; N runs fail independently. So the change is a guard on the
existing kind, not a new dispatch layer — there is no meta-hook, and the term is
rejected because it implies a second dispatch level the design deliberately
lacks.

## Decision

1. **Scope is firing discipline's second axis.** ADR 0021 decision 4 defined
   *cadence* — how often a hook may fire (Screens per-head, Notifiers once per
   PR ever). Scope answers the other half: **whether** a Notifier applies to
   this PR at all. A `Paths` list of globs over the PR's changed-file paths
   gates the fire; **absent `Paths` fires on every PR**, so every existing
   `hooks.yaml` keeps its meaning and the field is purely additive.
2. **Scope is Notifier-only — the field does not exist on `Spec`.** A scoped
   Screen must resolve to `proceed` where it does not apply, or the PR sits in
   Screening forever; that hands `hooks.yaml` a way to silently un-gate whole
   file classes, and a security screen scoped to `**/*.go` would auto-approve a
   malicious `Makefile`-only PR with zero screening. The hazard stays
   *unrepresentable* rather than validated against — the same technique as
   `ScreenConfig` carrying no `Point` field (ADR 0023).
3. **Full-path doublestar globs, gitignore-normalised.** Patterns match the
   file's full path via `github.com/bmatcuk/doublestar/v4`; a pattern containing
   no `/` is prefixed with `**/` at load. `**` matches zero or more segments, so
   `*.go` → `**/*.go` matches `main.go` and `internal/hook/hook.go` alike, while
   `services/api/**` keeps directory scoping. Stdlib `path.Match` was
   disqualified outright: it treats `**` as two `*`s, neither crossing `/`, so
   `**/*.go` works at exactly one directory level — the worst failure mode
   available, because sometimes-works reads as works. `ValidatePattern` gives a
   boot preflight (`ErrBadPattern`) in the existing refuse-at-boot family.
4. **Scope gates the fire before it is spent.** The predicate is evaluated in
   `Fire`, *before* `ledger.Mark`, so a declined Notifier keeps its once-per-PR
   fire. Because queue events are level-triggered, a PR that later grows into
   scope still gets its **first** run from that Notifier — a docs tweak that
   becomes a 2000-line Go change is reviewed when the Go arrives.
5. **Unknown scope fires — the uncertainty direction inverts by kind.** The
   cycle fetch caps `files` at 100 per PR; truncation is exact
   (`len(Files) < ChangedFiles`). On a truncated list with no visible match the
   Notifier **fires**. Never-fire-on-no-signal (ADR 0005) does not transfer: it
   was written for auto-*approval*, where acting without evidence is the
   consequential move. A Notifier cannot block, divert, or reorder anything, so
   *declining* is the consequential choice and firing is inert. The invariant
   generalises as **uncertainty resolves toward the harmless side, and the sides
   differ by kind** — a Screen with no verdict never proceeds; a Notifier with
   unknown scope fires. The agent then fetches the real, uncapped diff itself
   (`ComposeNotifyPrompt`), seeing past the window tm3k could not.
6. **Paths ride the inbound batch call.** `files` joins `listJSONFields` on the
   inbound pull only (line 232), mirroring how the outbound pull already appends
   its own `,mergeable` — Notifiers fire at inbound points, so the outbound pull
   pays nothing. The one-batched-call doctrine is upheld literally: the call
   stays one call and merely carries more, unlike ADR 0007's N+1 of 30 separate
   round-trips. This is *not* ADR 0008's on-demand fetch and does not replace
   it — the batch `files` field carries no `patch`, so the Diff card still needs
   its own call.

## Considered Options

- **A composing meta-hook** — one hook inspecting the PR and assembling the
  matching prompt fragments into a single run. Rejected: it solves composition
  when the gap is selection; it needs a nested config shape and a new ledger
  identity; and one all-or-nothing run means a crash is a logged miss for every
  language at once, where N runs fail independently. Its one genuine advantage —
  a single review comment instead of one per language — did not outweigh that.
- **Letting the agent route itself** — one prompt telling the agent to inspect
  the changed files and apply per-language criteria. Rejected: it costs no tm3k
  code but dissolves the per-language prompt files that are the point, and burns
  context reasoning about languages absent from the PR.
- **Scope on `Spec`, inherited by both kinds** — see decision 2. The variant
  where a non-matching Screen *holds* instead of proceeding was also rejected:
  it preserves never-fire-on-no-signal but fills the human queue with PRs
  nothing objected to.
- **Base-name matching via stdlib `path.Match`** — zero dependencies, no `**`
  ambiguity, and the upgrade to full-path is strictly additive. Rejected for
  losing monorepo directory scoping (`services/api/**`) permanently at the
  vocabulary level.
- **A `When:` condition DSL** over paths, author, size, labels. Rejected: tm3k
  already has a matcher for author/size/title — Rules. A second matching syntax
  with its own parser and validator is the copy-paste-instead-of-extract failure
  CLAUDE.md names, on a component that already exists.
- **A `Languages:` registry** mapping named languages to built-in glob sets.
  Rejected: it puts a language taxonomy inside tm3k (`.sh` vs `.bash` vs
  shebang-only, `*.go` vs `go.mod`) that the operator can express better in two
  globs.
- **`ExcludePaths`** for `vendor/**`, generated files, `**/*_test.go`.
  Deferred, not refused — the failure it prevents is one spurious review comment,
  the cheapest failure in the system. Added when it bites.
- **Fetching `files` only when some Notifier declares `Paths`.** Rejected:
  it threads hook config into `internal/github`, which knows nothing about
  hooks, to save ~0.7s on a multi-minute cycle — "the `gh` seam only decodes;
  never let it grow an opinion".
- **A `Skills:` field on `Spec`.** Rejected: skills are a claude-harness
  concept with no copilot equivalent, so the field would be silently inert for
  half the harness allowlist — exactly the boundary ADR 0023 draws. Skill
  delegation belongs in the prompt file, which is already the harness-coupling
  escape hatch.

## Consequences

- **ADR 0021's Consequences line is amended.** "A PR that gains commits after
  entering the queue gets no fresh review-assist run" is now precise as: no
  *repeat* run, but a Notifier that was previously out of scope can still make
  its *first* run. Once-per-PR-ever is per-Notifier, and a Notifier that never
  fired has spent nothing.
- **Polyglot PRs get one comment per matching language.** Accepted noise; the
  gain is that each review is written by a prompt that knows one domain.
- **Nothing reviews the seam between languages** — the bash script that invokes
  the Go binary is in both diffs and owned by neither reviewer. No new mechanism
  is needed: a Notifier with **no** `Paths` fires on everything and is exactly
  that cross-cutting reviewer.
- **Measured cost of the extra field**, worst case (kubernetes/kubernetes, 30
  PRs, three truncating at 100 files): the inbound list call goes 2.0–2.5s →
  2.8–3.3s and 84 KB → 141 KB. Fixed per cycle, one round-trip, far smaller on a
  normal queue.
- **Prompt files may delegate to skills.** A headless `claude -p` run keeps the
  Skill tool even under `--allowedTools "Bash(gh:*)"` (verified), so a
  `.config/go-review-prompt.md` can name `go-style`/`go-testing` instead of
  restating them — one source of truth for review criteria. Documented in the
  examples only, and claude-harness-specific: copilot reads it as inert prose.
- **A valid pattern that matches nothing real** (`*.golang`) stays silently
  non-firing. The boot preflight catches malformed patterns and the gitignore
  normalisation catches the common trap; the semantic residue is left to a
  decline log rather than a mechanism.
- **No wire change.** Scope is not exposed to the SPA — hooks stay hand-edited
  and boot-loaded (ADR 0023), so `openapi.json` and `schema.d.ts` are untouched.
