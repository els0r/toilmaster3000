# Copilot harness adapter

## Context

ADR 0023 left `internal/harness` claude-only and priced each further adapter
as "real work (auth, headless flags, output shapes) added when actually
wanted". The GitHub Copilot CLI (`copilot`) is now wanted as a second
harness. Its relevant facts, pinned empirically against v1.0.78: `copilot -p`
is the headless mode with the prompt as an **argument** — piped stdin is not
attached to the model's context; `--silent` makes stdout exactly the agent's
response text (the documented scripting surface); `--output-format json`
emits a JSONL stream with an undocumented per-line schema; tool permissions
use `--allow-tool "shell(gh:*)"` syntax; and, unlike claude, the CLI carries
ambient workstation state into a run by default — it loads `AGENTS.md` custom
instructions from the cwd, ships a built-in GitHub MCP server, and
self-updates mid-run.

## Decision

1. **Both legs, full parity.** `Copilot` implements `Adapter.Screen` and
   `Agent.Act`; any hook may name `Harness: copilot`. The config surface is
   unchanged — `copilot` is one new allowlist value.
2. **Silent text is the transport.** Runs use `copilot -p <prompt> --silent
   --no-color`; stdout is the result text handed to the fence-level extractor
   (`ExtractVerdictText`, now the harness-neutral half of extraction — the
   JSON-envelope decode stays claude's own), and a nonzero exit is a failed
   attempt. The JSONL mode is rejected: its schema is undocumented and the
   CLI's release cadence would turn every drift into spurious failed
   attempts.
3. **The prompt rides argv; oversize errors, never truncates.** With stdin
   unattached, a composed prompt beyond the OS argv limit fails exec — a
   failed attempt on ADR 0022's 3-strikes path, ending in a hold and a human.
   A screen never judges partial signal: truncating a diff under security
   judgement invites the payload to sit past the cap.
4. **Runs are hermetic.** Both legs pass `--no-custom-instructions`
   `--disable-builtin-mcps` `--no-auto-update`: a verdict must not be
   influenced by whatever `AGENTS.md` sits in tm3k's cwd, and the built-in
   GitHub MCP would be a second, API-based action channel beside the
   instructed `gh` one. The screen leg additionally hides every tool via
   `--available-tools=__none__` — the judge parity of claude's toolless
   screen run. (Bare `--available-tools` and `--available-tools=` are no-ops
   on v1.0.78; a match-nothing name is the reliable spelling of "no tools".)
5. **The act leg's authority is exactly the gh shell.** `--allow-tool
   "shell(gh:*)"` — the analog of the claude leg's `Bash(gh:*)`: one action
   channel matching the composed prompt's instructions, posting as the same
   runtime identity. The prompt-enforced ceiling (never approve, never merge)
   remains the control, per ADR 0023.
6. **Boot preflight checks harness binaries.** For every harness an *enabled*
   hook names (harness name = binary name), a missing CLI refuses startup —
   the harness sibling of the gh gate, fixing the same latent gap for claude.
   Auth stays runtime-checked: the harness CLIs have no offline auth probe,
   and a dead login surfaces as failed attempts → hold, never silence.

## Considered Options

- **JSONL output mode.** Rejected (see 2): structurally richer but
  undocumented; silent text is the surface the CLI documents for scripting.
- **Temp-file prompt handoff for oversized diffs.** Rejected: the judge would
  need the file-read tool, breaking the no-tools stance, and the
  diff-as-fenced-data framing gets one step removed.
- **Diff truncation.** Rejected outright (see 3).
- **Leaving the built-in GitHub MCP enabled on the act leg.** Rejected: a
  second authority surface whose tool subset and identity semantics differ
  from the instructed `gh` path.

## Consequences

- The three empirically-pinned behaviors (stdin unattached, bare
  `--available-tools` a no-op, `--silent` shape) may shift in later CLI
  versions; any drift surfaces as failed attempts → holds — never a
  fabricated verdict, but worth re-pinning on a major CLI bump.
- Every `-p` run leaves a session record under `~/.copilot`; accepted noise.
- Copilot auth is the operator's (`copilot login`, or `GH_TOKEN`-family env
  precedence); gh side effects still post via `gh` as the runtime identity.
- A third adapter reuses `ExtractVerdictText` when its CLI has no structured
  envelope of its own.
