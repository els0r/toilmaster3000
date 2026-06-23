# Generate the frontend's wire types from the OpenAPI spec

## Context

huma already produces an OpenAPI document from the Go DTOs (ADR 0001). The
frontend's TypeScript wire types (`CycleStatus`, `Approval`, `QueueItem`,
`Rule`) were hand-written in `frontend/src/api.ts` to mirror those DTOs. Two
hand-maintained copies of one contract drift silently: a backend field rename or
an added field is a runtime surprise on the frontend, caught by nothing.

The obvious recipe — "run the binary with a flag to dump the spec, then generate
types" — does not work here, because the production binary `//go:embed`s
`frontend/dist`. That makes the build circular: the binary needs `dist`, `dist`
needs the generated types, the types need the spec, and the spec would need the
binary.

## Decision

The frontend's wire types are **generated** from the OpenAPI spec; they are not
hand-written. The spec is produced by a dedicated, non-embedding entrypoint,
`cmd/openapigen`, which registers the same routes as the live server via the
shared `server.Config` + `server.RegisterAPI` and marshals the document to
stdout. Because it does not embed the frontend, it builds and runs on a clean
checkout before `dist` exists — that is what breaks the cycle.

`openapi-typescript` turns the spec into `frontend/src/api/schema.d.ts`;
`api.ts` keeps its thin fetch wrappers but its exported types are aliases onto
the generated `components["schemas"][...]`. Both generated artifacts —
`openapi.json` and `schema.d.ts` — are committed as the contract of record.

## Considered Options

- **Flag on the production binary + a placeholder `dist`.** Keeps the literal
  "argument to the binary" recipe, but needs a two-pass build and a committed
  stub frontend, and couples spec generation to the engine and embed. Rejected
  as more machinery for a worse result.
- **No generation; keep hand-written types with review discipline.** This is the
  status quo the decision replaces — the drift it allows is the whole problem.

## Consequences

- A new `make generate` step (spec → types) runs before the frontend build;
  `make build` depends on it, so the embedded SPA is always built against the
  current contract. `cmd/openapigen` passes a nil engine/rules — safe because
  spec generation never invokes the handler closures.
- Generated types are exactly as strict as the schema. `enabled` is `omitempty`
  on the wire, so it generates as optional (`enabled?: boolean`); the frontend
  treats absent as `false`. `id` is `readOnly`, so it is never sent in a request
  body. These are the contract telling the truth, not regressions.
- Drift is guarded two ways: `TestOpenAPISpecMatchesCommitted` fails in
  `go test` if the built spec ≠ committed `openapi.json` (no regen needed), and
  `make check` regenerates and `git diff --exit-code`s both files. There is no
  CI, so `make check` is run by hand before committing a DTO change.
- `openapi-typescript` is pinned exactly (not `^`) so a transitive upgrade can't
  reformat the output and flap the drift check.
