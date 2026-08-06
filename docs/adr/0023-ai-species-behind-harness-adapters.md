# AI hook species behind harness adapters; generic exec contract deferred

## Context

The feature ask included "a generic hook system" and "a generic layer for
harness invocation (sandcastle-like, in Go)". The first design made hooks
arbitrary user commands: a JSON payload on stdin, a Screen answering with
verdict-JSON on stdout's last line, and harness adapters exposed as tm3k
subcommands (`tm3k screen --harness claude …`) that config commands would
call back into.

Grilling caught the YAGNI: every actual MVP use case — the security screen,
the language review-assist — is AI-based. The exec transport had no current
consumer, forced every future harness user through fragile per-CLI glue, and
put shell strings into config (blocking any later UI editing). What must stay
generic is the *signature*, not the transport: a Screen is anything that
yields a Verdict, a Notifier anything that fires a side effect.

## Decision

1. **The hook kinds are Go interfaces** — the species seam. Every hook
   receives one `PRContext`: point, repo, the PR (number, title, author, URL,
   `head_sha` — newly added to the batched inbound field list, riding the
   existing call for free), plus point extras (reasons, the manual flag).
2. **MVP ships exactly one species per kind, both AI**: AI Screen and AI
   Notifier, configured declaratively in `.config/hooks.yaml` — PascalCase,
   two lists (`Screens` / `Notifiers`) so the pre/post discipline is
   unrepresentable to violate (Screens carry no `Point`; Notifiers name
   theirs), `Harness` / `Model` / `Prompt`|`PromptFile` / `Timeout` /
   `Enabled` fields, never a command string. Boot-loaded and
   preflight-validated (unknown harness, missing prompt, bad point, duplicate
   name ⇒ refuse to start); no hot-reload, no UI editor. Every hook gets a
   stable generated `Id`, self-healed into the file at boot when absent (the
   settings.yaml precedent, keeping the rules-store `Id` idiom without CRUD);
   the verdict store and fired-ledger key on `Id`, so renames never orphan
   state.
3. **`internal/harness` owns invocation**: adapters behind a small interface,
   claude-only in MVP — each further adapter (Copilot, OpenCode) is real work
   (auth, headless flags, output shapes) added when actually wanted. The
   adapter fetches the diff itself via `gh pr diff` (no checkout), composes
   the prompt — user instructions + PR metadata + the diff fenced *as data* —
   and extracts the verdict **structurally**: prose "CAN PROCEED" in agent
   chatter means nothing, and a run with no confident extractable
   `proceed`/`hold` **errors as a failed attempt** (ADR 0022's 3-strikes
   path). The adapter never fabricates a verdict in either direction. This is
   the sanctioned home of hook-driven per-PR `gh` calls: **configuring a hook
   is the consent** (the one-batched-call doctrine, amended).
4. **The generic exec/webhook species is deferred, not designed.** When a
   non-AI hook is actually wanted (Slack, a CI-runner screen), it arrives as
   a new species implementing the same kind interface — the contract gets
   designed against a real consumer.
5. **A Screen is defense-in-depth, not a security boundary.** Structural
   parsing is the tm3k-side defense, but an injection that makes the agent
   emit a valid proceed-verdict is beyond tm3k's reach. The screen raises the
   malicious-chore attack's cost; it does not eliminate it. Recorded so
   nobody later mistakes the feature for a guarantee.

## Considered Options

- **Exec + JSON as the hook surface** (the first design). Deferred: no
  non-AI consumer exists; it optimized for a generality nobody was using
  while making the common case (AI) fragile.
- **Harness fields bolted onto command hooks** (hybrid). Rejected: forks the
  abstraction into two config species prematurely — the interface seam gives
  the same openness without the surface.
- **`tm3k screen` / `tm3k notify` subcommands.** Died with the exec contract:
  nothing external needs to invoke the adapter anymore.
- **`Name` as the state key.** Rejected for the generated `Id` (rules
  precedent): renaming a screen would orphan its verdicts and silently
  re-screen every open PR.

## Consequences

- hooks.yaml is pure declarative data — a later UI editor or CRUD needs no
  config migration.
- Hook `gh` side effects (review comments, requested changes) post as the
  **runtime** identity; a per-hook `Env` override is deferred until that
  bites.
- The review-assist's authority ceiling is prompt-enforced, not
  code-enforced: it may comment or request changes, never approve — tm3k
  cannot compel an agent holding `gh` auth, so the prompt convention is the
  control, stated in CONTEXT.md.
- Adding a harness means one adapter implementation; adding a non-AI hook
  means one species implementation; neither touches the engine, the points,
  or the verdict machinery.
