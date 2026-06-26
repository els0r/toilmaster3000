# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

## Commands

Always go through `make` — a bare `go build .` fails on a clean checkout because
`frontend/dist` is a generated, git-ignored artifact the binary embeds.

```sh
make build         # generate -> npm build -> go build -> ./toilmaster3000
make run           # build, then serve on http://localhost:8666
make test          # Go + frontend
make test-go       # go test ./...
make test-frontend # cd frontend && vitest run
make generate      # dump openapi.json from Go DTOs, regen frontend TS types
make check         # regenerate the committed spec + types, fail on any drift
```

Dev loop (two terminals): `make dev-api` (Go API on :8666) and `make dev-web`
(vite dev server, proxies `/api` -> :8666, HMR).

Run a single Go test: `go test ./internal/engine -run TestName`.
Run a single frontend test: `cd frontend && npx vitest run src/PrRow.test.tsx`.

There is **no CI**. `make check` is the drift guard — run it before committing
any wire-DTO change, or the committed `openapi.json` / `schema.d.ts` go stale.

Running the binary requires two settings (both required, fail-fast if missing):
`--repo`/`TM3K_REPO` and `--search`/`TM3K_SEARCH`. It needs `gh` installed and
authenticated (`gh auth login`).

## Architecture

**Layered, with a strict wire boundary.** Flow: `main.go` wires everything →
`internal/engine` runs the loop → `internal/github` is the only thing that
touches `gh` → `internal/server` exposes typed HTTP → `frontend` consumes it.

- **`internal/engine`** — owns the find→approve loop and the single
  mutex-guarded in-memory store (dedup set, approvals feed, live queue, funnel
  snapshot, PR states). The loop is `RunCycleOnce(); sleep; repeat` in one
  goroutine — the sleep is *after* the cycle (never a `Ticker`) so a slow cycle
  can't overlap itself. **All approvals — auto and manual — flow through one
  locked `approve()` path**, which is what makes the manual-approve-vs-cycle
  race safe. The approval record is written to `approvals.jsonl` only on
  success, so a failed approval is retried next cycle.
- **`internal/github`** — shells out to `gh` behind the `GitHubClient`
  interface; `fake.go` backs the tests so the engine runs without the network.
  One `ListCandidates` call per cycle pulls everything (titles, authors, diff
  counts, draft flag, `statusCheckRollup`) — no per-PR N+1. The pure judgement
  folds (`AllGreen`, `FailingChecks`, `CollapsePRState`) are table-tested; the
  CLI seam only decodes. On-demand `Diff` is the sanctioned exception to the
  no-per-PR-call rule (ADR 0007/0008) — it never rides the cycle.
- **`internal/rule`** — the rule store (persisted to `.config/rules.yaml`) and
  the matcher. A rule predicates over author (`@me` → authenticated user),
  parsed conventional-commit title parts (each with optional include/exclude
  regex), and diff size. Two classes: **Approve** and **Review** (ADR 0004).
- **`internal/server`** — huma v2 typed handlers (ADR 0001). The wire is
  snake_case everywhere and owned **exclusively** by server-side DTOs; engine /
  domain types never cross the wire — the server maps each (`approvalToBody`,
  `queueItemToBody`, `funnelItemToBody`, etc.; ADR 0002). `analytics.go` and
  `pipeline.go` host the Analytics and Pipeline-funnel endpoints.
- **`internal/settings`** / **`internal/conventionalcommit`** — analytics
  assumption constants (persisted to `.config/settings.yaml`, ADR 0010) and the
  conventional-commit title parser.
- **`frontend`** — Vite + React 19 + TypeScript SPA, embedded via `go:embed`.
  Its wire types in `src/api/schema.d.ts` are **generated** from the backend's
  OpenAPI spec (`make generate` → `cmd/openapigen` → `openapi-typescript`), so
  the TS types can't drift from the Go DTOs.

### Cross-cutting invariants (the rules behind the rules)

These hold across files; breaking one silently corrupts behavior:

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
  server DTO layer — never widen a domain struct just to serve the wire.
- When adding/changing an endpoint or DTO, run `make check` before committing.
- Tests weight the correctness-critical pure core (parser, matcher, folds,
  validator) heavily; `gh` shell-out and HTTP handlers get lighter coverage.
  Use `testify/require` (already a dep) for Go tests.
- **Never copy-paste a functionally-similar component — extract a shared
  abstraction instead.** If two places render/compute the same shape with minor
  variation, build one deep module whose narrow interface absorbs the variation
  via props/slots; do not fork the code "just for this one." The four funnel
  stations each open-coded the same PR row until they were collapsed into one
  `frontend/src/PrRow.tsx` (with `DiffMag` as the shared diff-magnitude leaf);
  ADR 0014 records the design and the variation axes (`density`, the `meta` /
  `action` slots). This applies on both sides of the wire — frontend components
  and Go helpers alike. Catch the duplication when you write the second copy, not
  in a later cleanup pass.
