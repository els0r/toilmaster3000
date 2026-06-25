# Plan: Analytics tab

> Date: 2026-06-25
> Source: grilling session (see `CONTEXT.md` → "## Analytics", and ADRs
> [0009](../adr/0009-analytics-over-approval-history.md),
> [0010](../adr/0010-settings-store-for-assumptions.md),
> [0011](../adr/0011-elapsed-aligned-period-delta.md)).

A third tab that turns the durable approval history (`.state/approvals.jsonl`)
into a look-back dashboard: how much toil the robot saved (auto vs human, context
switches saved → time → money), broken down by conventional-commit type and
filterable by scope, over a Grafana-style time range with an honest
period-over-period delta.

All six slices are **AFK** — every design decision was settled in the grilling and
recorded in `CONTEXT.md` + the ADRs above.

## Dependency graph

```
1 skeleton ──┬─> 2 ranges ──┬─> 3 deltas ─┐
             │              └─> 5 cohort ──┤
             └─> 4 settings/$ ─────────────┴─> 6 scope filter
```

Slice 6 is the integration point — it scopes everything before it.

---

## Slice 1 — Walking skeleton: Analytics tab + stats row (today)

**Type:** AFK · **Blocked by:** None — can start immediately

### What to build

The thinnest complete path through every layer, proving the wiring. A new
`GET /api/toilmaster3000/v1/analytics` handler reads the full `approvals.jsonl`,
partitions today's approvals (local-midnight basis, as the Approval Feed already
uses) into **Auto-approved** vs **Human Review** by the `matched_rule` prefix
(`"human approval: "` ⇒ human; else auto), and returns counts + each side's share
of the range total through a server-owned DTO (snake_case wire, ADR 0002). A third
tab lives at URL hash `#analytics` beside Review and Rules; opening it fetches the
endpoint and renders the stats row: Auto-approved (count + %), Human Review
(count + %), and Context switches saved as a **raw count** (= the auto count; a
human approval is a switch the human did take, so it is not saved). No ranges, no
deltas, no cohort, no scope filter yet.

### Acceptance criteria

- [ ] `GET /analytics` returns auto/human counts + shares for today's approvals, computed from the full log, through a typed DTO with snake_case fields.
- [ ] Auto/human split is the `matched_rule` prefix per ADR 0009; shares sum to 100% (empty range → all zeros, no divide-by-zero).
- [ ] A third tab renders at `#analytics`, is linkable, and survives reload (no router dependency, matching the existing hash-tab pattern).
- [ ] The stats row renders Auto-approved, Human Review (each count + share), and Context switches saved (raw count = auto count).
- [ ] Go aggregation logic is unit-tested table-driven; OpenAPI regenerates and frontend types are generated from it (ADR 0003).

---

## Slice 2 — Time picker: selectable ranges

**Type:** AFK · **Blocked by:** Slice 1

### What to build

A lightweight Grafana-style time picker offering `today`, `this week`, `this
month`, and a custom `last X days`. The endpoint accepts `range=today|week|month|days`
and `days=N` (for the custom range) and computes range boundaries in
**workstation-local time**: today = local midnight → now; this week = **Monday**
00:00 (ISO 8601) → now; this month = calendar 1st 00:00 → now; last X days =
rolling `X×24h` ending now. The picker control drives the stats row; changing the
range triggers a debounced re-fetch and the numbers recompute.

### Acceptance criteria

- [ ] Endpoint accepts and validates `range` + `days`; invalid input is rejected structurally (huma) and `days` is required/validated for the `days` range.
- [ ] All four ranges compute correct local-tz boundaries (Monday week start, calendar month, rolling X days).
- [ ] The picker UI selects a range and a custom day count; selection lives in component/URL state and re-fetches debounced.
- [ ] Boundary math is table-driven tested in Go, including week-start and rolling-window edges.

---

## Slice 3 — Elapsed-aligned period deltas

**Type:** AFK · **Blocked by:** Slice 2

### What to build

Each headline stat shows a relative change vs the previous period, computed
**like-for-like**: only the elapsed slice of the current period is compared against
the same elapsed slice of the prior period (today vs yesterday-to-same-clock-time;
this week vs last week to same weekday+offset; this month vs last month to same
day-of-month+offset, **clamped** when the day has no counterpart; last X days vs the
preceding X×24h window). The delta is the `(now − prev)/prev` %-change of the
**count**, rendered as an up/down arrow + color + `±N%`. A **zero baseline** renders
**"new"** (or "—" when both are zero), never ∞. The delta carries a label naming the
aligned comparison (e.g. "vs last week, Mon–Wed aligned"). Deltas appear only on the
headline stats (Auto-approved, Human Review, Context switches saved); shares are not
delta'd. (ADR 0011)

### Acceptance criteria

- [ ] Server computes the elapsed-aligned previous-period window for all four ranges, with month-day clamping.
- [ ] Each headline stat returns a signed %-change of its count vs the aligned previous count.
- [ ] Zero previous → "new"; both zero → "—"; never infinity or NaN on the wire.
- [ ] UI renders arrow + color + `±N%` and the aligned-comparison label.
- [ ] Previous-period boundary + clamp + zero-baseline logic is table-driven tested in Go.

---

## Slice 4 — Switches-saved time & money + editable assumptions

**Type:** AFK · **Blocked by:** Slice 1

### What to build

A new `.config/settings.yaml` — the first non-rule persisted state — holds the
analytics assumption constants (`MinutesPerSwitch: 23`, `HourlyRate: 100`,
`Currency: "$"`, PascalCase YAML like `rules.yaml`), seeded with those defaults on
first run. `GET`/`PUT /settings` read and full-replace them through a DTO
(snake_case wire). The Context-switches-saved stat gains its two derived figures:
**time** = `count × MinutesPerSwitch / 60` hours and **money** = `hours × HourlyRate`
prefixed with `Currency`. The two constants render inline as a clickable
**assumption chip** (`× 23 min · $100/hr`); clicking it opens a popover that edits
and persists them, after which the figures recompute. (ADR 0010)

### Acceptance criteria

- [ ] `.config/settings.yaml` is seeded with the defaults on first run and loaded at startup; `GET /settings` returns the three constants, `PUT /settings` full-replaces and persists them.
- [ ] Switches-saved renders count + hours + money using the persisted constants, with the currency prefix.
- [ ] The assumption chip is clickable, edits the constants via a popover, persists through `PUT /settings`, and the figures update without restart.
- [ ] Settings round-trip (load → serve → save → reload) and the time/money arithmetic are tested.

---

## Slice 5 — By-Type cohort

**Type:** AFK · **Blocked by:** Slice 2

### What to build

A breakdown of the selected range's approvals by conventional-commit type. The axis
is the **fixed Conventional Commits set** — `feat, fix, chore, docs, style,
refactor, perf, test, build, ci, revert` — rendered in that order with **all rows
shown** (zeros dimmed), plus a trailing **`other`** bucket for any non-standard
`\w+` type the parser accepted (tm3k does not restrict types). Each row shows count
+ % of range total + the **auto/human split** (e.g. `55 auto / 4 human`) — the
actionable signal being which types still pull a human in. No per-type delta.

### Acceptance criteria

- [ ] Server buckets the range's approvals by parsed type into the fixed set + `other`, each with count, % of range total, and auto/human counts.
- [ ] Unparsed / non-conventional titles are handled defensively (do not crash; fall into `other`/unknown handling).
- [ ] UI renders every standard type row in fixed order, zeros dimmed, with the auto/human split.
- [ ] Type bucketing (incl. the `other` bucket and case handling) is table-driven tested.

---

## Slice 6 — Scope filter (multi-select, scopes the whole view)

**Type:** AFK · **Blocked by:** Slices 3, 4, 5

### What to build

A multi-select (OR) scope filter. The endpoint enumerates **every scope ever seen**
in the log (all-time union via the existing `splitScopes`, case-folded) and returns
that stable list regardless of the selected range; it also accepts `scope=a,b`
(repeatable/CSV) and applies it to the **entire aggregation**. A PR matches when its
scope set contains any selected scope (titles are comma/slash-split, so one PR can
match several). The UI presents a searchable multi-select control (the real log
already holds 45+ scopes); selecting scopes filters the stats row, switches-saved,
deltas, and the By-Type cohort together. Default is **All scopes** (no filter). Last
slice because it scopes everything built before it.

### Acceptance criteria

- [ ] `GET /analytics` returns the all-time, case-folded scope list independent of range, and accepts a multi-value `scope` filter.
- [ ] The filter applies to the whole aggregation — stats, switches-saved, deltas, and cohort all recompute for the selected scopes (OR semantics; multi-scope titles match any).
- [ ] UI is a searchable multi-select defaulting to All scopes; selection lives in component/URL state and re-fetches debounced.
- [ ] Scope enumeration (comma/slash split, case-fold dedup so `API`/`api` collapse) and the OR filter are table-driven tested.
- [ ] Selecting scopes with no approvals in the range yields zeros gracefully (deltas "—").
