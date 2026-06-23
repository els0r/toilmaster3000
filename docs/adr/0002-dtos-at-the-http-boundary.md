# Wire DTOs at the HTTP boundary; engine types never on the wire

## Context

The HTTP layer (`internal/server`) defines its own DTO struct for every wire
shape — `Approval`, `QueueItem`, `CycleStatus`, `RuleBody` — and each handler
maps the engine/domain type through a named converter before responding. Several
of these DTOs (`Approval`, `QueueItem`) are currently byte-identical to their
`engine` counterparts, differing only in that the engine types' json tags exist
for the `approvals.jsonl` on-disk format while the DTOs own the wire format.

## Decision

The wire contract is owned exclusively by `server`-side DTOs. Engine/domain and
on-disk types do not appear on the wire, even when a DTO is presently identical
to the type it mirrors. The shared snake_case convention is held by agreement
across the boundary, not by reusing a single struct.

## Considered Options

We evaluated collapsing the identical DTOs into the engine types (returning
`[]engine.Approval` directly, deleting the converters) to remove the apparent
duplication. Rejected: it re-couples the public JSON to the engine's internal
read-model and to the disk format, so an internal change (e.g. adding a
cycle-duration field to status, or changing the JSONL schema) would silently
alter the API. The duplication is the deliberate price of that decoupling.

## Consequences

- New endpoints add a DTO + converter; this boilerplate is expected, not a smell.
- Identical-looking DTO/engine pairs must NOT be "deduplicated" — the redundancy
  is intentional. CONTEXT.md "Wire boundary" records this so the next reader (or
  an architecture-review pass) does not try to collapse them.
