# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository. This is the
always-loaded entry point — keep it lean. Detailed material lives one hop away;
follow the signpost when your task reaches that area.

## What this is

`toilmaster3000` is a single-workstation PR auto-approver: one Go binary with an
embedded React SPA, served on `localhost:8666`. Each cycle it pulls a candidate
set of PRs (a repo + `gh` search query), runs each through user-defined rules,
and either auto-approves it, routes it to a human-review queue, or drops it. It
shells out to the `gh` CLI and reuses your auth — no DB, no service account, no
network surface of its own.

Read `README.md` for the product overview, `CONTEXT.md` for the full domain
model and glossary, and `docs/adr/` for the architecture decision records (each
numbered ADR explains *why* a non-obvious choice was made — consult the relevant
one before changing that area).

When running a PRD autonomously (multi-issue orchestration), follow
`WORKING-AGREEMENT.md` — it defines how a parent PRD is decomposed into vertical-
slice sub-issues, how each sub-issue's status moves through the GitHub Projects
board (In Progress *at dispatch*, not at completion), how every PR is verified
against the gates before Claude merges it, and the guardrails that stay in force.

## Build & test

Always go through `make` — a bare `go build .` fails on a clean checkout because
`frontend/dist` is a generated, git-ignored artifact the binary embeds.

- `make build` / `make run` / `make test` — build, serve on :8666, test both sides.
- `make check` — the drift guard. There is **no CI**; run it before committing any
  wire-DTO change, or the committed `openapi.json` / `schema.d.ts` go stale.

Full command list, the two-terminal dev loop, single-test invocations, and the
binary's run requirements: **`docs/development.md`**.

## Architecture

**Layered, with a strict wire boundary.** Flow: `main.go` wires everything →
`internal/engine` runs the loop → `internal/github` is the only thing that
touches `gh` → `internal/server` exposes typed HTTP → `frontend` consumes it.

Per-package responsibilities and the wire boundary in detail: read
**`docs/architecture.md`** before changing a package.

### Invariants you must not break

These hold across files; breaking one silently corrupts behavior. Keep them in
mind whenever you touch the cycle loop, the rules, or the funnel.

- **Eligibility gates run before any rule** (ADR 0005): draft PRs and
  not-all-green PRs are dropped before parsing/matching. An auto-approver must
  never fire on no signal.
- **Review Rules win over Approve Rules.** A Review-Rule match (or a breaking
  `!` title on an Approve match) routes to the queue and never auto-approves.
- **Breaking-change invariant**: a conventional-commit title carrying `!` is
  never auto-approved — only a manual override can approve it.
- **Soft dedup** (ADR 0013): a PR GitHub already reports `APPROVED` by someone
  else is left alone, never re-approved — keeps saved-switches analytics honest
  across multiple instances.
- **Cycle Funnel partition** (CONTEXT "Cycle Funnel"): every incoming PR lands
  in exactly one terminal bucket; the branch precedence in `RunCycleOnce` *is*
  the partition. The segment counts sum to `Incoming` by construction — preserve
  that when editing the cycle loop.

## Conventions

- Commit style is Conventional Commits (`type(scope): subject`) — match the
  existing history.
- The wire is snake_case; Go domain types are not. Keep them separated by the
  server DTO layer — never widen a domain struct just to serve the wire (ADR 0002).
- Run `make check` after adding/changing any endpoint or DTO.
- Tests weight the correctness-critical pure core (parser, matcher, folds,
  validator) heavily; `gh` shell-out and HTTP handlers get lighter coverage. Use
  `testify/require` (already a dep) for Go tests.
- **Never copy-paste a functionally-similar component — extract a shared deep
  module** whose narrow interface absorbs the variation via props/slots. Applies
  on both sides of the wire. Catch the duplication when you write the second
  copy, not in a later cleanup pass. ADR 0014 records the canonical case
  (`PrRow` / `DiffMag`) and the variation axes.
