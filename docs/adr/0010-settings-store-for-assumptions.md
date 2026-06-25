# A settings store for the analytics assumption constants

> **Superseded in part by [ADR 0012](0012-switches-saved-money-as-a-range.md).**
> The store, its `.config/settings.yaml` home, and the GET/PUT-full-replace shape
> below still stand. The *money model* does not: `MinutesPerSwitch`/`HourlyRate`
> and the `hours × rate` formula were replaced by a per-switch `CostLow`/`CostHigh`
> **range**, and the time figure was dropped. Read this for the store rationale,
> 0012 for the current constants.

## Context

The Analytics "Context switches saved" headline translates the auto-approved count
into **time** (`× minutes-per-switch`) and **money** (`× hourly-rate`) — "this is
where the money is." Those two constants need values. The money figure only lands
if it is *the user's own* rate, not a hard-coded guess.

tm3k advertises "no database, no service account" and keeps exactly two state
files: `.config/rules.yaml` and `.state/approvals.jsonl`. Any home for these
constants is therefore a deliberate deviation worth recording.

## Decision

Introduce **`.config/settings.yaml`** — the **first non-rule persisted state** in
tm3k — holding `MinutesPerSwitch: 23`, `HourlyRate: 100`, `Currency: "$"`
(PascalCase YAML, matching `rules.yaml`; seeded with those defaults on first run).
Expose it via `GET`/`PUT /settings` (full replace, like rules). The constants are
editable in-app through the clickable **assumption chip** in the Analytics tab.

## Considered Options

- **Hard-coded constants in Go.** Simplest, but `$100/hr` is arbitrary for any
  given user and only changes via recompile — it weakens the very headline the
  feature exists to deliver.
- **Env vars / flags** (`TM3K_MINUTES_PER_SWITCH`, `TM3K_HOURLY_RATE`), mirroring
  the `--repo`/`--search`/`--poll-interval` idiom. Rejected: tuning the money
  figure would require a restart, and these are personal knobs a user will want to
  nudge while looking at the dashboard, not boot-time deployment config.

## Consequences

- A third persisted artifact exists; the "two state files" description in the
  README is now "two state files plus an optional settings file." Future
  non-rule, non-history config has an obvious home — but the bar for adding to it
  stays high (this store exists for *display assumptions*, not behavior).
- 23 min is Gloria Mark's measured refocus-after-interruption figure, used as the
  seeded default and shown inline as the assumption so the estimate stays honest.
- Settings flow through their own DTO at the wire boundary (ADR 0002), snake_case;
  the YAML stays PascalCase like `rules.yaml`.
