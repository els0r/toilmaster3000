# The rule editor derives every surface from one predicate-descriptor list

## Context

The rule editor modal must expose the **full predicate vocabulary** a Rule can
carry: author include/exclude, an Include and Exclude regex for each of the
three title parts (type, scope, description), and the `DiffMin`/`DiffMax`
bounds. An earlier editor hand-listed its fields and silently omitted the three
title-part *excludes* — it carried them through invisibly on edit, so a
partially-exposed rule read as complete when it was not. The bug's structural
form: the vocabulary was re-enumerated in several places (modal rows,
validation, the Draft↔Rule mapping, the row summary), and one enumeration went
stale.

The editor works on a **Rule Draft** — the editable projection of a Rule, every
field held as the raw text the user types (authors as a comma-separated string,
each title part as a regex string, diff bounds as numeric-input strings with
`"" ⇒ unconstrained`). The Draft and the wire `Rule` are deliberately different
shapes: the Draft is what a human edits, the Rule is what crosses the wire.

## Decision

Name the predicate vocabulary **once**, matching the model's three predicate
kinds: the six title-part include/exclude fields as a single descriptor list,
Author and Diff size as their own small mappings. The modal rows, the per-field
regex validation, the constrains-nothing guard, the Draft↔Rule round-trip, and
the row summary all **read from that one definition** rather than each
re-listing the fields.

Saving maps the Draft back onto a Rule: empty predicates are dropped and the
card-implied `Class` is stamped (ADR 0004). Validation split: the client
regex-checks **only** the six title-part fields; author patterns are validated
server-side; diff `0` is treated as unconstrained (same as empty), not as a
constraining bound.

## Considered Options

- **Keep hand-listed fields with review discipline.** Rejected: the drift this
  allows already happened once (the hidden excludes) — same argument as ADR
  0003, one layer up from the wire.
- **Adopt the design mockup's thinner modal** (fewer visible fields). Rejected:
  a partially-exposed rule reads as complete; the editor must show every
  predicate a rule can carry.

## Consequences

- A predicate added to the model reaches the editor by adding **one
  descriptor** — there is no second place to forget. This is the structural fix
  for the hidden-excludes bug class.
- The Draft's pure logic (round-trip, validation, summary) is testable without
  the DOM; the DOM test keeps only modal interaction.
- The descriptor list is frontend implementation vocabulary, not domain
  language — like `PrRow` (ADR 0014), it is deliberately not a CONTEXT.md
  glossary term beyond the Rule Draft entry that points here.
