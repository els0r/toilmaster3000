# Period deltas compare equal elapsed slices, not partial-vs-full

## Context

Each Analytics headline stat shows a relative change vs the previous period. But
the three named ranges — `today`, `this week`, `this month` — are **in-progress**
when viewed: at 10:30 on a Wednesday, "this week" covers Mon 00:00 → Wed 10:30.
The naive previous period (all of last week, a full 7 days) would compare a partial
current window against a complete prior one, so every morning reads as a steep,
meaningless drop.

## Decision

Compute deltas **like-for-like**: compare only the *elapsed slice* of the current
period against the **same elapsed slice** of the prior period.

- `today` vs yesterday 00:00 → same clock time.
- `this week` vs last week, Monday → same weekday + clock offset.
- `this month` vs last month, day 1 → same day-of-month + clock offset, **clamped**
  when the current day-of-month has no counterpart (viewing on the 31st caps last
  month at its final instant).
- `last X days` is already a fixed-length rolling window, so its previous period is
  the immediately-preceding `X×24h` window — like-for-like for free.

A delta is `(now − prev)/prev` of the count; a zero baseline renders **"new"** (or
"—" when both are zero), never ∞.

## Considered Options

- **Full previous period** (all of yesterday / last week / last calendar month).
  Simpler to implement and explain, but structurally biases in-progress periods
  negative until they complete, requiring a UI caveat that undermines the number.
  Rejected: an honest delta is worth the extra date arithmetic.

## Consequences

- The previous-period boundary math (timezone, month-length clamp, elapsed offset)
  is fiddly and **correctness-critical**, so it lives **server-side in Go** and is
  table-driven tested — matching the project's "test weight on pure logic" ethos
  and keeping the frontend a pure renderer.
- A future reader must not "simplify" this back to a full-period comparison: the
  alignment is deliberate, and the CONTEXT "Previous period" entry plus this ADR
  are the guardrail.
