# Development

Commands, dev loop, and run requirements. CLAUDE.md keeps only the daily traps
(`make`, `make check`); the full reference lives here.

## Commands

Always go through `make` — a bare `go build .` fails on a clean checkout because
`frontend/dist` is a generated, git-ignored artifact the binary embeds.

```sh
make build         # generate -> npm build -> go build -> ./toilmaster3000
make run           # build, then serve on http://localhost:8666
make test          # Go + frontend
make test-go       # go test -race ./...
make test-frontend # cd frontend && vitest run
make lint          # golangci-lint run ./...
make generate      # dump openapi.json from Go DTOs, regen frontend TS types
make check         # regenerate the committed spec + types, fail on any drift
```

## Dev loop

Two terminals: `make dev-api` (Go API on :8666) and `make dev-web` (vite dev
server, proxies `/api` -> :8666, HMR).

## Running a single test

- Go: `go test ./internal/engine -run TestName`
- Frontend: `cd frontend && npx vitest run src/PrRow.test.tsx`

## Testing conventions

Test weight goes to the correctness-critical pure core — the
conventional-commit parser, the matcher, the judgement folds (`AllGreen`,
`CollapsePRState`, the stage folds), the rule validator, and the analytics
date math — as table-driven Go tests with `testify/require`. The `gh`
shell-out and the HTTP handlers get lighter coverage; the seam's `fake.go`
keeps the engine testable without the network. On the frontend (vitest), test
pure logic first (the Rule Draft round-trip, validation, row summaries — no
DOM); DOM tests keep only interaction (modal wiring, mutations firing,
poll-driven rerenders with the API mocked).

## The drift guard

`make check` is the drift guard — run it before committing any wire-DTO change,
or the committed `openapi.json` / `schema.d.ts` go stale.

## CI

`.github/workflows/ci.yml` runs on every pull request and on pushes to `main`:
one job that does `make build`, `make check`, `go vet ./...`, golangci-lint and
`make test` — the same commands you run locally, in the same order. It is a
backstop, not the loop; a PR should arrive green.

The build step comes first because everything Go-side needs `frontend/dist`,
which a clean checkout does not have. Lint runs through
[`golangci-lint-action`](https://github.com/golangci/golangci-lint-action) with
the version pinned in the workflow; `.golangci.yml` keeps the default linter set
and only lifts the caps that would otherwise hide repeat findings, plus `gofmt`.

## Run requirements

Running the binary requires two settings (both required, fail-fast if missing):
`--repo`/`TM3K_REPO` and `--search`/`TM3K_SEARCH`. It needs `gh` installed and
authenticated (`gh auth login`).
