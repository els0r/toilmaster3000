# toilmaster3000

> Review what matters. To you, anyways.

This repository solves a problem that shouldn't exist: GitOps causing an endless
flood of small commits to bump image X, update docs to Y, etc. In theory, all of
these should be reviewed, in practice, this leads to endless context switching
and rubber-stamping.

No more. Tell the toilmaster what's relevant for human review, make it do the
rubber stamping for you.

**Use at your own risk.**

## What it is

`toilmaster3000` is a single-workstation tool that auto-approves the trivial PRs
you'd rubber-stamp anyway and routes everything else to a small human-review
queue. It is a single Go binary with an embedded React UI, served on
`localhost:8666` — it never leaves your machine and stores no token of its own.

You give it a **candidate set** (a repo + a `gh` search query). Every cycle it
pulls those PRs once, runs each through your **rules**, and:

- **Approve Rules** — a match auto-approves the PR (`gh pr review --approve`).
- **Review Rules** — a match forces the PR into the **Needs-Human-Review** queue
  and *wins over* any approve match, so you can carve exceptions out of broad
  approve rules.
- **Eligibility Gates** (hard-wired, not configurable) drop draft PRs and PRs
  whose CI isn't all-green *before* any rule runs — an auto-approver must never
  fire on no signal.
- **Breaking-change Invariant** — a PR whose conventional-commit title carries
  `!` is *never* auto-approved; it goes to the queue for a manual decision.

A rule predicates over the PR **author** (`@me` expands to the authenticated
user), the parsed **conventional-commit title** (`type(scope)!: description`,
each part with an optional include/exclude regex), and **diff size**
(`additions + deletions`, with optional min/max bounds). Rules are created,
named, toggled, and edited from the UI and persist to `.config/rules.yaml`.

The UI is two tabs under a "heartbeat" status strip:

- **Review** — the actionable Needs-Human-Review queue (with per-PR diff
  magnitude and an on-demand per-file diff card) beside a read-only, today-scoped
  **Approval Feed** that shows what the robot did and the live GitHub state
  (open / merged / closed) of each approval.
- **Rules** — the Approve Rules and "Human Review Always" cards.

There is no database, no service account, and no network surface beyond the `gh`
calls: it shells out to the `gh` CLI and reuses your existing auth.

## What a saved switch is worth

The Analytics tab counts the reviews the robot spared you — each auto-approval is
one PR you didn't have to context-switch into — and prices that count in money.
Because the cost of a single interruption is a *wide distribution*, not a point,
the figure is shown as a **range**: your saved-switch count times a per-switch
cost band.

The band is seeded from [Zurich developer cost-switch
research](docs/research/2026-06-25-context-switch-cost-zurich-developer.md):

| Bound | Default | Basis |
|---|---|---|
| Low | **CHF 10** | a brief ~10-min refocus valued at gross salary (≈ CHF 1.00/min) |
| High | **CHF 26** | a full 23-min flow break valued at loaded employer cost (≈ CHF 1.15/min) |

So 57 saved switches reads **CHF 570 – CHF 1486**. The band (and the currency) are
editable in the **Settings** tab — tune them to your own salary and how deep a PR
review actually pulls you out of flow. The Settings copy summarises this table;
the bounds persist to `.config/settings.yaml` (a file written under the old
point-estimate schema is migrated to these defaults on first load).

## How to run

You need the [`gh` CLI](https://cli.github.com) installed and authenticated
(`gh auth login`). The Go binary embeds the built frontend, so the frontend must
be built first — always go through `make` (a bare `go build .` fails on a clean
checkout because `frontend/dist` is a generated, git-ignored artifact).

```sh
make build   # npm build -> go build -> ./toilmaster3000
make run     # build, then serve on http://localhost:8666
```

The tool is not wired to any one repo. The candidate set is supplied at startup;
both settings are required, and a flag overrides its env var:

| flag | env | meaning |
|---|---|---|
| `--repo` | `TM3K_REPO` | `owner/name` the `gh` calls target |
| `--search` | `TM3K_SEARCH` | the `gh pr list --search` query selecting candidates, e.g. `is:open team-review-requested:owner/team` |
| `--poll-interval` | `TM3K_POLL_INTERVAL` | wait between find→approve cycles (default 5m, min 1m) |

```sh
TM3K_REPO=owner/name \
TM3K_SEARCH='is:open team-review-requested:owner/team' \
./toilmaster3000
```

Then open <http://localhost:8666>. On first run it seeds an editable
`.config/rules.yaml` (see `examples/rules.yaml` for an annotated starter set) and
writes its approval log to `.state/approvals.jsonl`. Both are git-ignored — they
hold your team's tokens and history and never belong in a commit.

### Packaged bundle

`make package` builds a self-contained `tm3k/` bundle (binary + starter rules +
`RUN.txt`) tarred as `toilmaster3000.tar.bz2`; `make install` unpacks it under
`/tmp/tm3k` and prints the run instructions.

## Technical details

- **Backend** — Go, single binary. `net/http` (Go 1.22 routing) + [huma
  v2](https://huma.rocks) for typed handlers, request validation, and a
  generated OpenAPI spec. The find→approve engine runs in one background
  goroutine: `runCycle(); sleep; repeat` (sleep *after*, so a slow cycle can't
  overlap itself). All approvals — auto and manual — flow through one
  mutex-guarded path, making the manual-approve-vs-cycle race safe.
- **GitHub access** — shells out to `gh` behind a `GitHubClient` interface; a
  fake implementation backs the tests so the engine is exercised without the
  network. One list call per cycle pulls everything (titles, authors, diff
  counts, draft flag, `statusCheckRollup`) — no per-PR N+1. PR lifecycle state
  is refreshed out-of-band in one batched search per cycle. The "is it
  all-green?" and "open/merged/closed?" judgements are pure, table-tested folds
  (`github.AllGreen`, `github.CollapsePRState`); the CLI seam only decodes.
- **Frontend** — Vite + React + TypeScript SPA, embedded via `go:embed`. Its
  wire types are *generated* from the backend's OpenAPI spec
  (`make generate` → `cmd/openapigen` → `openapi-typescript`), so the TS types
  can't drift from the Go DTOs. The wire is snake_case everywhere and owned
  exclusively by server-side DTOs; engine/domain types never cross the wire.
- **Tests** — weight is on the correctness-critical pure core: the
  conventional-commit parser, the rule matcher (author/title/diff predicates,
  Approve-vs-Review precedence, breaking-change routing), the validator, and the
  two GitHub folds. `gh` shell-out and HTTP handlers get lighter coverage;
  the frontend uses vitest. There is no CI — `make check` regenerates the
  committed spec + types and fails on any drift; run it before committing a DTO
  change.

```sh
make dev-api   # terminal 1: go run .  (API on :8666)
make dev-web   # terminal 2: vite dev server, proxies /api -> :8666, HMR
make test      # Go + frontend
make check     # fail on OpenAPI/type drift
```

See [`CONTEXT.md`](CONTEXT.md) for the full domain model and
[`docs/adr/`](docs/adr/) for the architecture decision records.
