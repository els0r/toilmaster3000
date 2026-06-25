# Switches-saved money as a range, not a point

## Context

The "Context switches saved" headline values each avoided review in money. The
original model (ADR 0010) did this with a single point estimate:
`money = count × MinutesPerSwitch / 60 × HourlyRate`, two constants shown inline
as `× 23 min · $100/hr`.

That point is a false precision. The
[Zurich context-switch-cost research](../research/2026-06-25-context-switch-cost-zurich-developer.md)
is explicit that a switch is "a spectrum" and "any single number is an average
over a wide distribution" — a meaningful flow break costs **CHF 10–26** (10 min at
gross salary `CHF 1.00/min` … 23 min at loaded employer cost `CHF 1.15/min`), not
a single figure. Presenting one number invites the reader to over-trust it.

## Decision

Show money as a **low/high range**. Replace `MinutesPerSwitch` and `HourlyRate`
with two direct per-switch franc constants, `CostLow` and `CostHigh`:

```
money_low  = count × CostLow
money_high = count × CostHigh
```

- `Defaults()` = `CostLow: 10, CostHigh: 26, Currency: "CHF"` — the research band's
  endpoints, each traceable (`10 min × CHF1.00/min gross`, `23 min × CHF1.15/min
  loaded`). The README's "What a saved switch is worth" subsection is canonical;
  the Settings tab condenses it.
- The money pill renders `CHF570 – CHF1486` over a `CHF10–26 / switch` basis
  sub-label; an empty range collapses to a single `CHF0`. The pill stays read-only
  (the band is edited in Settings).
- `cost_low ≥ 1`, `cost_high ≥ 1` (huma minimums); the PUT handler rejects
  `cost_high < cost_low` (422) — the server owns correctness.
- **Self-healing migration:** a `settings.yaml` lacking the cost keys (the old
  `MinutesPerSwitch`/`HourlyRate` schema, or a zeroed file) is reseeded to the full
  defaults on load, so the headline can never come back `CHF0 – CHF0`.

This supersedes the money-model half of ADR 0010. The settings *store* itself
(ADR 0010's core: a persisted `.config/settings.yaml` behind GET/PUT) is unchanged
— only its fields and the derived figure.

## Considered Options

- **Minutes range × single rate** (`min_minutes`, `max_minutes`, `hourly_rate`).
  Keeps the honest `N min · CHF X/hr` decomposition, but the spread is capped at
  the minutes ratio (~2.3× for 10–23 min) — too narrow to express the research's
  full band, and it leaves the basis as two coupled knobs.
- **Minutes range × rate range** (four knobs). Most faithful to the research's two
  uncertainty axes (recovery time × gross-vs-loaded cost), but four fields to edit
  and explain for a one-line headline. Rejected as over-engineered.
- **Keep a single minutes constant for a time figure** alongside the money band.
  Rejected: with money no longer flowing through `hours × rate`, a retained
  `MinutesPerSwitch` would drive a "time saved" figure on a *different* basis than
  the money beside it — two assumptions for one tile reads as incoherent. The
  "X.Xh saved" line was dropped instead; the count already carries the magnitude.

## Consequences

- Slice 4 was "switches-saved **time & money**"; it is now money-only. The hours
  figure and `formatHours` are gone from the headline tile.
- The wire DTO changes: `cost_low`/`cost_high` replace `minutes_per_switch`/
  `hourly_rate`; the Analytics response carries `money_low`/`money_high` instead of
  a single `money` (and no `hours`). Frontend types regenerate from OpenAPI.
- The default currency moves from `$` to `CHF`; the research, and therefore the
  seeded band, is Zurich-specific. A non-Zurich user retunes all three constants —
  the same edit surface as before, one extra field.
