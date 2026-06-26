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
make test-go       # go test ./...
make test-frontend # cd frontend && vitest run
make generate      # dump openapi.json from Go DTOs, regen frontend TS types
make check         # regenerate the committed spec + types, fail on any drift
```

## Dev loop

Two terminals: `make dev-api` (Go API on :8666) and `make dev-web` (vite dev
server, proxies `/api` -> :8666, HMR).

## Running a single test

- Go: `go test ./internal/engine -run TestName`
- Frontend: `cd frontend && npx vitest run src/PrRow.test.tsx`

## The drift guard

There is **no CI**. `make check` is the drift guard — run it before committing
any wire-DTO change, or the committed `openapi.json` / `schema.d.ts` go stale.

## Run requirements

Running the binary requires two settings (both required, fail-fast if missing):
`--repo`/`TM3K_REPO` and `--search`/`TM3K_SEARCH`. It needs `gh` installed and
authenticated (`gh auth login`).
