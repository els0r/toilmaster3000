# Implementer primer

One page for implementation agents. CLAUDE.md (always in context) carries the
product summary, build rules, and invariants — this page adds the map an
implementer otherwise re-derives from `docs/architecture.md`,
`docs/development.md`, CONTEXT.md, and the code. Read this, then ONLY the
ADR(s) your brief names.

## Package map — where a change lands

- `main.go` — assembly point only: flags/env, preflight (gh auth, repo
  visibility, config loads), constructor wiring (`buildScreener` and siblings).
  Thin; no logic.
- `internal/engine` — the cycle. `RunCycleOnce`'s branch precedence IS the
  funnel partition (every incoming PR → exactly one bucket; counts sum to
  Incoming). Engine-caused changes mutate snapshots in place (ADR 0018).
  Level-triggered consults, no transition memory (armed/disarm idiom).
- `internal/forge` — the neutral vocabulary: domain types, the pure folds, the
  `Client` interface, and `forge.Fake`, the engine-test seam.
- `internal/forge/github` — the ONLY package that shells `gh` for the cycle's
  batched pulls; one batched list call, fields ride it. Raw decode types stay
  package-private; it normalises gh's vocabulary into the neutral one before
  anything leaves (ADR 0030).
- `internal/hook` — hook config (`.config/hooks.yaml`, PascalCase, Id
  self-heal), kind interfaces (Screen/Notifier), verdict store, fired-ledger,
  runners. Pure folds live here; they get the heavy test tables.
- `internal/harness` — AI harness adapters (claude, copilot, opencode), species (AIScreen,
  AINotifier), prompt composition, structural verdict extraction. The
  sanctioned home of hook-driven per-PR `gh` calls.
- `internal/rule`, `internal/conventionalcommit` — matcher + title parser
  (pure core, heavy tables). `internal/armed`, `internal/settings` — jsonl/
  yaml store precedents.
- `internal/server` — typed HTTP over engine snapshots. The wire is
  snake_case DTOs here, never widened domain structs (ADR 0002). Any DTO
  change ⇒ `make check` + commit regenerated `openapi.json` and
  `frontend/src/api/schema.d.ts`.
- `frontend/src` — Vite + React SPA. Never copy-paste a similar component:
  extend the shared deep module (`PrRow`/`StationCard`/`DiffMag`, ADR 0014)
  via props/slots. Vitest + testing-library.

## Seams — test here, never internals

- **Engine behavior**: the cycle over `forge.Fake`, with hook species faked
  at the kind interfaces. Assert outcomes: bucket placement, partition sums,
  queue contents, feed entries.
- **Pure functions** (parser, matcher, folds, validators, extraction): table
  tests, the house's heaviest coverage.
- **Stores** (`*.jsonl` append-only, latest-row-per-key wins; yaml Id
  self-heal): round-trip tests incl. reload — mirror the armed-store tests.
- **Harness**: scripted fake adapters behind the harness interfaces; real
  `claude`, `copilot`, `opencode`, and `gh` CLIs never execute in unit tests.
- **HTTP handlers**: light — shape and status, not logic.

## Store & config idioms

- State: append-only jsonl under `.state/`, replayed at boot; derived indexes
  (dedup sets, streaks) rebuilt on load. Act first, append on success —
  EXCEPT outward side-effect ledgers (hookfires), recorded at dispatch so a
  miss beats a double-post.
- Config: yaml under `.config/`, boot-loaded, preflight-validated (refuse
  startup naming the offender), stable generated `Id`s self-healed into the
  file. Absent file ⇒ feature off, behavior unchanged.

## Dev loop in a worktree

- `npm ci --silent` in `frontend/` once; then `make` targets work.
- Loop: `go test ./internal/<touched>/` per cycle; full `make test` +
  `make check` + `-race` on touched packages once, before your report.
- Filter output (`| tail`, `| grep -E "ok|FAIL"`); never dump full suites.
- Known wart: pre-existing gofmt drift in `internal/engine/engine.go` —
  leave it; no cleanup commits.
