# Notifier WorkDir: an anchored, read-bounded harness run

## Context

The first real review-assist target is a company monorepo whose review criteria
already exist as **skills** under `.github/skills/` — a `golang-pr-review`
entry point that delegates to sibling skills (`golang-instrumentation`,
`golang-testing`) and to its own `references/` tree. tm3k must not learn what
that monorepo is; the harness invocation is where the company-specific anchor
belongs.

Today the operator approximates this by symlinking the skill file into
`.config/` and naming it as a `PromptFile`. That gets exactly one skill,
inlined as prose: its siblings are unreachable, its `references/` are
unreachable, and the inlined copy goes stale the day the monorepo's skill
changes — silently, and looking correct.

Pinned empirically against copilot CLI v1.0.78 and the claude CLI, in the
manner of ADR 0024:

- **Skill discovery is working-directory based** (`<cwd>/.github/skills`,
  `.agents/skills`, `.claude/skills`). The process cwd is the lever; copilot's
  `-C` is a spelling of it, and claude honours the same cwd.
- **`--no-custom-instructions` disables `AGENTS.md`, not skills.** ADR 0024's
  hermeticity flags and skill loading are orthogonal.
- **Reads are ambient within cwd; shell and writes stay gated.** copilot's
  `view` runs under `--allow-tool "shell(gh:*)"` and claude's `Read` under
  `--allowedTools "Bash(gh:*)"`, while copilot's `apply_patch` is auto-denied
  non-interactively ("could not request permission from user"). Both CLIs deny
  reads outside cwd by their own path verification.
- **The skill mechanism delivers `SKILL.md` and nothing more.** Every
  `references/` file beneath it is a plain read, bounded by cwd — so
  `copilot skill add <dir>` *advertises* skills whose supporting files it then
  cannot load.
- **The toolless screen leg sees no skills at all** (`--available-tools=__none__`
  removes the resolution mechanism).

## Decision

1. **`WorkDir` sets the harness process's working directory.**
   `Request.WorkDir` → `cmd.Dir` in `runCopilot` and `runClaude`. Not copilot's
   `-C`: cwd is the harness-neutral lever, so one field serves both adapters and
   `internal/harness` grows no per-CLI branch.
2. **Notifier-only — the field does not exist on `Spec`.** A working tree is
   mutable, unversioned input: anchor a Screen to one and its verdict depends on
   whatever branch or half-finished rebase the operator last left there, so two
   runs over the same head can disagree for reasons no ledger records. A gate
   whose input is not reproducible is not a gate. The hazard stays
   *unrepresentable* — no field on `ScreenConfig`, and `AIScreen` never
   populates `Request.WorkDir` — the same technique as `Paths` (ADR 0026) and
   `ScreenConfig` carrying no `Point` (ADR 0023).
3. **`WorkDir` is a read grant, and the named directory is its ceiling.**
   The anchor cannot be separated from read access: discovery and reads resolve
   from the same root. tm3k therefore **never** passes `--add-dir` or
   `--allow-all-paths` — that is the difference between a grant bounded by a
   directory the operator chose and one bounded by nothing. The name is
   deliberately mechanical: it promises no checkout contract, because **the tree
   is ambient and is not at the PR's head SHA**.
4. **The anchored prompt is a one-liner naming the skill; `PromptFile` is not
   re-based.** `Prompt: /golang-pr-review` (copilot's skill mechanism) or
   `Prompt: Read and follow .github/skills/golang-pr-review/SKILL.md` (claude,
   a deterministic file read). The harness coupling lives entirely in `Prompt`,
   which ADR 0026 already designates the harness-coupling escape hatch. No
   templating: `ComposeNotifyPrompt` already emits `Repository:`, `PR: #N`, and
   `URL:` immediately after the instructions, and a skill that accepts a PR ref
   finds it there.
5. **Absolute paths only, refused at boot.** `ErrBadWorkDir` rejects a relative,
   missing, or non-directory `WorkDir`, joining `ErrBadPattern`'s family. No
   `$VAR` expansion: an unset variable expands to `""`, which silently inherits
   tm3k's own cwd — the agent would then run with `.config/` as its read scope,
   find no skills, and post a generic review. The same preflight stats the
   resolved `PromptFile` for **both** kinds, closing a pre-existing hole: a
   prompt file is read at *fire* time, after `ledger.Mark`, so a typo costs that
   PR its review permanently.
6. **Composition requires attribution.** `ComposeNotifyPrompt` requires the
   posted review to state which profile it applied. tm3k cannot check that a
   skill resolved without learning what a skill is (the ADR 0026 boundary), and
   nothing comes back from `Act` to inspect — so the signal goes where a human
   is already looking. A review naming no profile is a review that loaded no
   skill.
7. **This amends ADR 0024 decision 4.** Hermetic-by-default stands unchanged —
   `--no-custom-instructions`, `--disable-builtin-mcps`, `--no-auto-update` on
   every leg. `WorkDir` is the single deliberate, operator-named breach of it,
   available to the Notifier kind only: ambient *instructions* stay off, ambient
   *skills* come on.

## Considered Options

- **`copilot skill add <dir>`** — a workstation registry, zero tm3k change.
  Rejected on evidence: the skill is listed and its `SKILL.md` loads, but
  `references/quality-checklist.md` is **denied** (`view`, outside cwd). It
  advertises a skill it cannot fully load — the model believes it has the
  criteria and silently loses every progressive-disclosure layer. Also ambient
  global state invisible from `hooks.yaml`, and harness-specific.
- **`WorkDir` at the monorepo root.** Rejected: a 3.4 GB read grant handed to an
  agent holding a `gh` publishing channel while processing attacker-controlled
  PR content — including untracked, gitignored `*.env` files that exist
  precisely because of what they contain. It also buys workspace context that a
  static review of a fetched diff does not need.
- **Inlining `SKILL.md` as `PromptFile`** (the status quo symlinks). Rejected:
  it is a copy of a file the agent could read directly, stale the moment the
  monorepo skill changes, and it defeats progressive disclosure — with
  `cwd = WorkDir`, a skill's relative `references/` resolve against the repo
  root, not the skill directory.
- **Re-basing `PromptFile` against `WorkDir`.** Withdrawn once the prompt became
  a one-liner: dual resolution semantics for a field with no remaining consumer.
- **`WorkDir` on `Spec`, inherited by both kinds** — see decision 2.
- **Enforcing a clean git tree at boot.** Rejected: it gives the hook validator
  an opinion about git, in a tool where `internal/github` is the only thing that
  touches that world, and `WorkDir` is not required to be a repository at all.
- **Running the tm3k binary itself from the monorepo.** Rejected on mechanics:
  `.config/hooks.yaml`, `PromptFile`, `rules.yaml`, and the ledgers all resolve
  against tm3k's cwd.

## Consequences

- **Doctrine: point `WorkDir` at a skills-only sparse worktree, never a working
  clone.** `git worktree add --detach <dir> <default-branch>` +
  `git sparse-checkout set .github/skills` yields ~3 MB, zero untracked files,
  a shared object store, and a read anchor pinned to the base branch — which is
  the correct context for reviewing a diff. Verified in such a worktree:
  `references/` read successfully, `<monorepo>/go.mod` denied, on **both**
  harnesses.
- **The two harnesses address the same anchor differently** — copilot resolves
  `/name` through its skill mechanism (model-discretionary), claude reads the
  path (deterministic). `WorkDir` itself is inert on neither; only the `Prompt`
  spelling differs.
- **A `WorkDir` that exists but holds no skills is undetectable to tm3k.** The
  slash form resolves to nothing and a competent-sounding generic review is
  posted once, forever. Same class as ADR 0026's "a valid pattern that matches
  nothing real stays silently non-firing" — left to doctrine and the attribution
  sentence, not to mechanism.
- **Skill-driven runs are heavier than the prompts they replace**, and a Notifier
  timeout fires *after* `ledger.Mark`: an overrun is a logged miss with the fire
  already spent, and the 3-strikes path is Screens-only. Size the Timeout for the
  pathological case, not the median.
- **ADR 0026's rejected `Skills:` bullet is corrected** in the same change: its
  stated reason ("no copilot equivalent") is empirically false. The decision
  stands on the surviving reason — skill delegation belongs in the prompt.
- **`internal/harness` sets `cmd.Dir` for the first time.** An empty `WorkDir`
  is the pre-existing behaviour bit-for-bit: `cmd.Dir = ""` inherits tm3k's cwd.
- **No wire change.** Hooks stay hand-edited and boot-loaded (ADR 0023), so
  `openapi.json` and `schema.d.ts` are untouched.
