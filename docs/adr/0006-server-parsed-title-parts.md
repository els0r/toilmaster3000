# The frontend renders server-parsed conventional-commit title parts

## Context

The redesigned UI renders each PR by its **parsed** conventional-commit title —
a type icon, scope pills, and a clean description (the `type(scope):` prefix
stripped) — in both the Approval Feed and the Needs-Human-Review queue. The
design prototype parsed the title in JavaScript (`parseCC`).

But a tolerant conventional-commit parser already exists in Go
(`internal/conventionalcommit`) and is the correctness-critical core the engine
matches on: parsing must tolerate mixed-case, comma/slash-separated scopes, and
malformed prefixes (see CONTEXT). A second parser in JS is a second source of
truth — the display could classify a title differently than the engine that
approved it. That is exactly the silent contract drift ADR 0003 exists to
prevent, reintroduced one layer up.

## Decision

The backend parses the title **once** (the Go parser of record) and ships the
parsed parts on the wire. A shared `server.TitleParts {type, scopes[], breaking,
description}` rides **both** `Approval` and `QueueItem`, alongside the unchanged
raw `title`. The frontend renders these parts; **it never parses a title
itself**.

Parts are computed in the DTO converters at response time (**parse-on-read**)
and are **never persisted**: `approvals.jsonl` keeps only the raw `title`, so an
improved parser re-renders historical approvals with zero migration. `QueueItem`
is already derived live each cycle, so its parts are computed the same way.

## Considered Options

- **Client-side re-parse (the prototype's `parseCC`).** Fastest, zero backend
  change — but two parsers that drift, so the displayed type/scope can disagree
  with what the engine actually matched. Rejected: the same drift ADR 0003
  closed, one layer up.
- **Persist the parsed parts into `approvals.jsonl`.** Avoids re-parsing on each
  read, but freezes the parts at the write-time parser version and needs a
  migration whenever the parser improves. Rejected: the JSONL is the durable
  record; parts are a derived view that should track the current parser.

## Consequences

- `Approval` and `QueueItem` gain a nested `title_parts` (shared
  `server.TitleParts`); the raw `title` stays as the source of truth and the
  failed-parse fallback (a non-conventional title renders raw). Both flow through
  OpenAPI → generated frontend types per ADR 0003 — no hand-written wire shape.
- `<CommitTitle>` and `<TypeIcon>` are the single frontend renderers, shared by
  feed and queue. The type icon is the **only** type signal once the prefix is
  stripped; an unknown or unparsed type falls back to a generic `commit` glyph.
- `breaking` is exposed as a parsed part and drives the queue's breaking badge as
  a **display fact**, independent of whether `breaking_change` is a queueing
  reason (which stays Approve-tied — see CONTEXT, "Matching semantics").
- First-letter capitalization of the displayed title is a **render transform
  only**; `title_parts.description` keeps the raw lower-case convention so the
  shown value never corrupts the parsed data.
- Sharing one `server.TitleParts` sub-struct across two DTOs does not violate
  ADR 0002, which forbids reusing *engine/domain* types on the wire — this is a
  server-owned DTO type, computed in the converters.
