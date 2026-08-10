# OpenCode harness adapter

## Context

ADR 0023 made every additional harness real adapter work: headless invocation,
output shape, authority, and ambient configuration need their own contract. The
OpenCode CLI (`opencode`) is now wanted as a third adapter. Unlike Claude and
Copilot, OpenCode is a multi-provider agent runtime: its global configuration,
providers, authentication, plugins, and custom agents belong to the operator.
tm3k must work with a normal installation rather than assume a particular
container, provider proxy, credential store, filesystem layout, or sandbox.

Pinned against OpenCode v1.18.1, its documented `run` command accepts a non-interactive prompt, consumes
piped stdin, and writes completed response text to non-terminal stdout. Its
JSON mode is an event stream, not a result envelope. OpenCode merges inline
configuration from `OPENCODE_CONFIG_CONTENT` after global and project config,
and its permissions are wildcard matches over shell text, not parsed command
arguments.

ADR 0028 has since moved verdict extraction out of the adapters and up into the
Screen species, so a third adapter inherits a narrower job than ADR 0024's:
return the run's result text, and nothing else.

## Decision

1. **Both legs, full parity.** `OpenCode` implements `Adapter.Screen` and
   `Agent.Act`; any hook may name `Harness: opencode`. The YAML shape is
   unchanged: `opencode` is one allowlist value.
2. **Text over stdin is the transport.** Runs invoke `opencode run --agent
   <tm3k agent>`, pass the composed prompt on stdin, and consume ordinary
   stdout as result text. Both legs end there: extraction is the Screen
   species' work (ADR 0028), so an unextractable verdict fails only after the
   run has been transcribed. A nonzero exit or blank output is an existing
   failed path, and a run that spoke before it failed keeps its text. No diff
   truncation and no argv-size limit.
3. **The operator's setup is trusted.** tm3k inherits provider, authentication,
   plugins, environment, and global configuration. It neither copies
   credentials nor assumes XDG paths, Docker, a local proxy, or a model.
   `Model` passes unchanged to `--model`; absent uses OpenCode's selected
   default.
4. **tm3k overlays only a run-private agent profile.** The inherited inline JSON
   is parsed and preserved, then tm3k adds and selects a random
   `tm3k-screen-*` or `tm3k-notifier-*` agent. OpenCode deep-merges fixed agent
   names across configuration layers, so a random name prevents an
   operator-defined field from surviving in the harness profile. Malformed
   inherited inline JSON is a run failure rather than a silent fallback. The
   overlay disables sharing, snapshots, formatting, LSP, compaction, and
   auto-update for the run.
5. **Screens have no tools.** `tm3k-screen` denies every permission: the judge
   receives one diff in its prompt and cannot obtain PR content through tools.
   Trusted inherited instructions and configuration still shape the model run.
6. **Notifiers have the gh action channel.** `tm3k-notifier` permits reads,
   search, and skills in its direct workspace, and the full `gh` shell command;
   all other tools, edits, web access, external-directory tools, subagents, and
   questions are denied. Trusted global configuration and instructions can
   still influence model context. OpenCode's shell-text permissions cannot soundly
   allow only review subcommands: wildcard command arguments admit arbitrary
   suffixes, and compounds made solely of allowed children also pass. The
   profile therefore matches Claude and Copilot's whole-gh authority. The
   `never approve` / `never merge` ceiling remains prompt-enforced under ADR
   0023.
7. **`WorkDir` remains a read grant, not a sandbox.** It is the OpenCode
   process cwd and direct-tool workspace. The notifier denies
   `external_directory`, but trusted global configuration can still influence
   the model context; operators keep the skills-only-worktree doctrine of ADR
   0027.
8. **Existing pools remain the concurrency bound.** tm3k does not add a global
   OpenCode semaphore or rely on undocumented database overrides. OpenCode
   one-shot concurrency is empirically checked before release; failures remain
   bounded by each hook's existing timeout and failure contract.

## Considered Options

- **Require the current containerized/profile-specific setup.** Rejected: a
  harness adapter must work with standard operator-managed OpenCode installs.
- **Isolate XDG directories or copy authentication per run.** Rejected: it
  assumes credential placement and breaks normal provider configurations.
- **OpenCode JSON event mode.** Rejected: its event schema is not the stable
  answer contract needed by the common fence extractor.
- **Verb-level gh permission allowlist.** Rejected: OpenCode matches shell text,
  not argv. It cannot safely distinguish a dynamic review body from appended
  flags, redirects, substitutions, or compounds.
- **Adapter-global serialization.** Rejected: it would add hidden queue time
  inside a hook's execution timeout and no portable OpenCode contract requires
  it.

## Consequences

- An OpenCode hook requires an installed `opencode` binary and whatever model
  provider/configuration its operator normally uses; binary absence still fails
  boot, while auth/model errors follow existing runtime failure semantics.
- The Text output contract and permission behavior must be re-pinned when a
  later OpenCode major version changes its CLI or permission model.
- No wire change: hook configuration stays local, hand-edited YAML.
