# Development

Commands, dev loop, and run requirements. CLAUDE.md keeps only the daily traps
(`make`, `make check`); the full reference lives here.

## Commands

Always go through `make` — a bare `go build .` fails on a clean checkout because
`frontend/dist` is a generated, git-ignored artifact the binary embeds.

```sh
make build         # generate -> npm build -> go build -> ./toilmaster3000
make run           # build, then serve on http://localhost:8666
make dist-stub     # placeholder frontend/dist so Go-only work needs no node
make test          # Go + frontend, both halves, aggregated exit code
make test-go       # dist-stub, then go test -race ./...
make test-frontend # cd frontend && vitest run
make smoke         # real build, then go test -tags smoke .
make lint          # lint-go + lint-frontend, aggregated exit code
make lint-go       # dist-stub, then golangci-lint run ./...
make lint-frontend # cd frontend && tsc --noEmit
make generate      # dump openapi.json from Go DTOs, regen frontend TS types
make check         # regenerate the committed spec + types, fail on any drift
```

`test` and `lint` run both halves and aggregate the exit codes rather than
stopping at the first failure — a Go failure must not leave the frontend's state
unknown.

Each signal has exactly one home. A type error reddens `lint-frontend`, a
bundling error reddens the frontend build, Go style reddens `lint-go`. The trade
is explicit: `npm run build` is `vite build` alone, so a plain `make build` no
longer typechecks — `make lint` is what does.

## The dist stub

`main.go` does `//go:embed all:frontend/dist`, which needs the directory to be
non-empty in order to compile — not to hold the real SPA. No Go test reads the
built SPA either: the server tests inject an `fstest.MapFS` stand-in and
`main_test.go` never touches the embed. So `make dist-stub` writes a placeholder
`frontend/dist/index.html`, and Go-side work (`test-go`, `lint-go`) needs no npm
install and no vite build at all.

It writes only when `frontend/dist` is **absent**, so a real build is never
clobbered, and the placeholder carries a visible `toilmaster3000 build stub`
marker so a binary accidentally built on it is obvious rather than mysteriously
blank.

**Working-tree side effect:** on a tree with no `frontend/dist`, `make lint`
(via `lint-go`) now leaves the stub behind — a change `make lint` did not make
before. A subsequent bare `go build .` then produces a binary serving the
placeholder. The marker text is the tell; `make build` overwrites it; and
`make smoke` is the guard that the shipped artifact is the real SPA.

## The smoke test

`smoke_test.go` is tagged `//go:build smoke` and is the only test that reads the
real embedded `frontend/dist`: it asserts the shell sits at the dist root,
carries the SPA root mount, is not the stub, and that the bundles it references
are embedded alongside it. Without those assertions, `vite` moving `outDir` — or
an `index.html` that stops landing at the dist root — would ship green and fail
at runtime in `newSPAHandler` ("read SPA shell (was frontend built?)").

The build tag is load-bearing: untagged, these tests would also run under
`go test ./...` in the backend job, where `frontend/dist` is the stub. Run them
only through `make smoke`, which builds for real first.

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

`.github/workflows/ci.yml` runs on every pull request and on pushes to `main`
as six independent checks, one per signal, each named for the kind of fix it
demands:

| Job | Runs | Toolchain |
| --- | --- | --- |
| `frontend` | `npm ci`, `npm run build`, `npm test` | node |
| `backend` | `make test-go` | Go |
| `lint-go` | `make dist-stub`, golangci-lint-action | Go |
| `lint-frontend` | `npm ci`, `npm run lint` | node |
| `contract` | `npm ci`, `make check` | node + Go |
| `smoke` | `make smoke` | node + Go |

No job depends on another, so each is re-runnable on its own and a Go failure
never hides the frontend's state. The Go-only jobs install no node: they lean on
`make dist-stub` for the embed. `smoke` is the only job that builds for real and
the only one that inspects the artifact.

There is no separate `go vet` step: golangci-lint's default set includes
`govet`, so a vet finding reddens `lint-go` already. Lint runs through
[`golangci-lint-action`](https://github.com/golangci/golangci-lint-action) with
the version pinned in the workflow; `.golangci.yml` keeps the default linter set
and only lifts the caps that would otherwise hide repeat findings, plus `gofmt`.

CI is a backstop, not the loop; a PR should arrive green.

## Run requirements

Running the binary requires two settings (both required, fail-fast if missing):
`--repo`/`TM3K_REPO` and `--search`/`TM3K_SEARCH`. It needs `gh` installed and
authenticated (`gh auth login`).
