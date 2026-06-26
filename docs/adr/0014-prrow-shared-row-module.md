# One shared PrRow module renders every funnel station's PR row

## Context

The frontend renders six queue-shaped surfaces — the Cycle Funnel's stations:
Incoming, Dropped (pipeline-red + draft), Staging, Needs-Human-Review, and the
Approval Feed. Every station that lists PRs renders the **same row anatomy**: a
type glyph in a left gutter, the server-parsed title line (`CommitTitle`), and a
meta line that always leads with the author. ADR 0006 already established
`CommitTitle`/`TypeIcon` as the single shared title renderers — but the *row
around them* was open-coded four times: `FunnelRow` and `StagingRow` in
`Pipeline.tsx`, an inline `queue-row` in `NeedsReview.tsx`, and an inline
`feed-row` in `ApprovalFeed.tsx`, each with its own `.funnel-*` / `.queue-*` /
`.feed-*` CSS triplet.

The three wire types the rows render — `FunnelItem`, `QueueItem`, `Approval` —
all carry the same identity quintet (`number`, `title`, `title_parts`, `url`,
`author`). The skeleton was shared in shape but not in code, so a change to the
row (a title-line tweak, a meta-line separator) meant four edits and three CSS
edits, and the skeleton was re-asserted in four test files.

Consolidating into one `PrRow` module forces two non-obvious choices that a
future architecture review would otherwise re-litigate.

## Decision

Introduce one deep `PrRow` module with a narrow interface —
`PrRow({ item: PrIdentity, meta?, action?, density? })` — that every station
feeds. `PrIdentity` is a hand-declared quintet the three wire types satisfy
**structurally** (no runtime adapter, no generic). `meta`/`action` are
`ReactNode` slots the caller closes over with its own full item; PrRow renders
the gutter, the title line, and the meta line's `author` + the leading `·`
before the `meta` slot.

Two load-bearing sub-decisions:

1. **Preserve the feed's denser scale as a single `density` variant — do not
   fully unify the look.** PrRow renders one canonical row; `density="compact"`
   (the feed only) selects a smaller title/meta scale. Everything else — padding,
   border, gutter alignment — is unified into one `.pr-row` class set.

2. **The feed wraps PrRow; PrRow does not own PR-State.** The Approval Feed's
   state bar and fresh-approval flash are absolute overlays. Rather than give
   PrRow an `overlay` slot, the feed wraps PrRow in its own `position: relative`
   container (as `ApprovedElsewhere` already wraps its row for the soft-dedup
   highlight). PR-State / flash knowledge stays local to the feed module, where
   the `state` enum switch already lives.

Inter-row separators move to a shared `.pr-list > * + *` rule (wrap-agnostic),
so PrRow draws no border. `CommitTitle` sheds its `linkClassName` prop — title
scale is now driven by `density` via CSS.

## Considered Options

- **Fully unify — one row look, no `density` variant.** The narrowest possible
  interface and truly one class set. Rejected: the feed is a long scan-only
  ledger (`feed-scroll`) deliberately rendered denser and smaller than the
  roomier, actionable queue. Collapsing them to one scale is a visual-design
  regression, not just a refactor. A single two-value `density` knob keeps the
  interface narrow while preserving the deliberate difference.
- **Preserve all three looks via a `variant="funnel|queue|feed"` prop.** No
  visual change at all. Rejected: it keeps three class sets alive behind one
  component — the duplication moves rather than concentrates, so PrRow stays
  shallow and fails the deletion test. `density` (one axis, two values) is the
  minimal parameterization; `variant` (three opaque modes) is the triplet
  renamed.
- **Give PrRow an `overlay` slot for the state bar + flash.** Self-contained, but
  widens the interface for a feed-only concern and makes every row pay
  `position: relative`. Rejected: it leaks the Approval Feed's PR-State lifecycle
  into the shared row. Wrapping keeps that knowledge where the state enum is
  rendered and matches the existing `ApprovedElsewhere` wrap pattern.
- **PrRow takes the whole wire item (union of the three types).** Rejected: it
  couples the shared row to every wire shape and re-couples on every new station
  — the opposite of a narrow seam. The structural `PrIdentity` view depends on
  nothing but the five fields it reads.

## Consequences

- One `PrRow.tsx` replaces `FunnelRow`, `StagingRow`, the inline `queue-row`, and
  the inline `feed-row`. The `.funnel-*` / `.queue-*` / `.feed-*` /
  `.queue-link` / `.feed-link` / `.funnel-link` CSS triplets collapse to one
  `.pr-row` set plus a `.pr-row--compact` modifier and a `.pr-list` separator
  rule.
- `PrIdentity` is a structural view, not a new wire type — it respects ADR 0003
  (wire types stay generated from OpenAPI); the three generated types remain the
  source of truth and are assignable to it.
- Test surface: a new `PrRow.test.tsx` asserts the skeleton once (gutter →
  `TypeIcon`, title line → `CommitTitle`, meta = `author · {slot}`, action
  placement, `density` modifier). Station tests shrink to their distinguishing
  slots (failing count, diff pill, approve + refetch, reason chips, state bar,
  manual badge) — the skeleton is no longer re-asserted four times.
- `CommitTitle` narrows by a prop (`linkClassName` removed); its scale is now
  expressed once, tied to `density`.
- `PrRow` is an implementation module, deliberately **not** a `CONTEXT.md`
  glossary term — the domain language is the stations and the funnel, not the
  shared renderer beneath them.
- A future run of the architecture-review command that suggests "merge the rows
  into one look" or "pull PR-State into the shared row" should reach for this
  ADR: both were considered and declined for the reasons above.
