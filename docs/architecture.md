# Architecture

Per-package responsibilities and the wire boundary. CLAUDE.md keeps the one-line
flow and the invariants; this is the detail you read before changing a package.

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
