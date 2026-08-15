# Architecture

Per-package responsibilities and the wire boundary. CLAUDE.md keeps the one-line
flow and the invariants; this is the detail you read before changing a package.

**Layered, with a strict wire boundary.** Flow: `main.go` wires everything →
`internal/engine` runs the loop → `internal/forge/github` is the only thing that
touches `gh` → `internal/server` exposes typed HTTP → `frontend` consumes it.

- **`internal/engine`** — owns the cycle loop and the single mutex-guarded
  in-memory store (dedup set, approvals feed, live queue, funnel + outbound
  snapshots, armed set, PR states). The loop is `RunCycleOnce(); sleep; repeat`
  in one goroutine — the sleep is *after* the cycle (never a `Ticker`) so a
  slow cycle can't overlap itself; default interval 5m, floor 1m
  (`--poll-interval`). One cycle: inbound fetch → gates → rules →
  approve/queue/staging fold; outbound fetch + threads fetch → stage fold;
  then two tail steps — the batched PR-State refresh (ADR 0007) and the merge
  step over Armed Ready PRs (ADR 0016, gated by ADR 0019). **All approvals —
  auto and manual — flow through one locked `approve()` path**, which is what
  makes the manual-approve-vs-cycle race safe. Ledgers (`approvals.jsonl`,
  `merges.jsonl`) are appended only on success, so failures retry next cycle;
  engine-performed changes also mutate the published snapshots in place
  (ADR 0018). Fetch failures fail closed: inbound skips the cycle, outbound or
  threads clears the outbound snapshot and skips all merging.
- **`internal/forge`** — the neutral vocabulary above the seam: the domain
  types the engine passes around (`PR`, `Check`, `OutboundStage`, `PRState`),
  the `Client` interface each adapter implements, and `fake.go`, which backs
  the tests so the engine runs without the network. The pure judgement folds
  (`AllGreen`, `CollapsePRState`, `UnresolvedCount`, `ClassifyOutboundStage`)
  live here and are table-tested; there is exactly one of each across all
  forges (ADR 0030).
- **`internal/forge/github`** — shells out to `gh` behind the `forge.Client`
  interface, owning GitHub's raw decode types and its transport. Three batched
  calls per cycle — `ListCandidates` (titles, authors, diff counts, draft flag,
  `statusCheckRollup`, `reviewDecision`), the authored list (+`mergeable`), and
  the GraphQL `UnresolvedThreads` search — plus the batched `PRStatesSince`; no
  per-PR N+1. The seam decodes and **normalises**: gh's raw vocabulary is
  mapped into the neutral values the folds judge, and `FailingChecks` is
  supplied here because cardinality is a forge fact. Normalisation is pure
  functions, table-tested against recorded responses in `testdata/`. The
  sanctioned per-PR exceptions never ride the cycle timer: on-demand `Diff`
  (user click, ADR 0008) and the moment-of-merge `gh pr view` (ADR 0016).
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
