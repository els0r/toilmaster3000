# Outbound partition as a keyed type

## Context

`github.ClassifyOutboundStage` is a pure fold returning a single tagged value,
and `rebuildOutbound` immediately discarded that tag — exploding it through a
seven-case switch into seven named struct fields on `engine.Outbound`. Eleven
non-test sites then re-enumerated the same seven stages by hand: the struct
fields, the locked copy in `Outbound()`, the classify switch, the log line,
`trackedChangedFiles`, `outboundStanding`, `reconcileArmed`, and three in the
server's wire mapping.

The hazard was not verbosity. `reconcileArmed` built its `keep` set from a
hand-written list of **six** stages, deliberately omitting Changes Requested —
that omission *is* the level-triggered disarm (ADR 0016). `outboundStanding`
omitted the same stage for the same reason. `trackedChangedFiles` included all
seven. Nothing in the code distinguished "I forgot this stage" from "I omitted
it deliberately", so adding an eighth stage meant a maintainer who missed
`reconcileArmed` would silently withdraw merge consent for every PR in it. The
Funnel-partition doctrine that CONTEXT says holds "by construction" was in fact
held by eleven hand-maintained lists agreeing with each other.

The stage set is a live axis of change: In Discussion was added by ADR 0019.

## Decision

1. **`engine.Outbound` *is* the partition** — `map[github.OutboundStage][]OutboundItem`,
   not a struct wrapping one. CONTEXT does not say Outbound *contains* the
   partition; it says every authored PR sits in exactly one stage. One name,
   one concept. `rebuildOutbound` appends by tag, so no switch and no `default`
   can drop a PR.
2. **`Outgoing` is derived**, `func (ob Outbound) Outgoing() int`. The stored
   field was never an independent witness past the first merge —
   `pruneMergedFromOutbound` decremented it by hand, so the sum was
   *maintained*, not *observed*. Derived, the prune is one operation and ADR
   0018's sum-preservation is free. The raw-pull cross-check (`len(authored) ==
   ob.Outgoing()`) survives as a test, where it costs nothing.
3. **The stage set is declared exactly once**, in `internal/github/outboundstage.go`,
   with `OutboundStages()` returning a copy of the ordered list in funnel order.
   It is exported as a function, not a slice var, for the same reason
   `Outbound()` copies its slices: an exported slice var is mutable by any
   importer.
4. **The consent rule is a predicate stated by exclusion**, `armable(stage)
   bool`, in `internal/engine/armed.go` — arming is tm3k's consent model, not a
   GitHub concept. Two consumers: `Arm`'s 4xx gate and `reconcileArmed`'s keep
   loop. The doctrine "Arm anywhere except Changes Requested" now has one
   declaration site, and its by-exclusion phrasing makes a *new* stage armable
   by default at both sites simultaneously.
5. **`mergeArmedReady` is handed `[]OutboundItem`**, not the whole snapshot. It
   only ever read Ready; taking only Ready makes the Discussion gate structural
   rather than conventional, and shrinks the aliasing surface (see 6) to one
   slice header.
6. **The snapshot is published by reference; reads clone deeply.**
   `setOutbound` stores the map as-is, so `rebuildOutbound`'s local `ob` aliases
   the published map — the sharing ADR 0018 reasons about, kept explicit rather
   than hidden behind a defensive copy. `Outbound()` clones the map *and* its
   slices. The engine constructor initialises `e.outbound` to a non-nil empty
   map, so the no-cycle-yet and populated states have identical write
   semantics.
7. **The wire is unchanged and the converter stays explicit.**
   `internal/server/outbound.go` keeps its seven named JSON fields and its
   hand-written per-stage mapping; `openapi.json` is byte-identical. Coverage is
   enforced by a round-trip test driven by `OutboundStages()`, not by
   restructuring the boundary.

## Considered Options

- **A helper on the existing struct** (`stages() iter.Seq2[...]`), leaving the
  seven fields in place. Kills the named hazard in three sites for a fraction of
  the churn. Rejected: it cannot reach the two failure modes that are
  consequences of the *fields* rather than the *lists* — a missing case in
  `Outbound()`'s clone (the new stage's slice silently shared with the live
  snapshot) and a missing case in the classify switch (the PR silently vanishes
  from the funnel).
- **An ordinal array**, `OutboundStage` as an `iota` enum with a `String()`
  method and `[NumOutboundStages][]OutboundItem`. Strictly stronger on the
  headline goal: `range` is simultaneously complete and ordered, so there is no
  second declaration to drift from the constants, and the zero value is usable
  without a constructor. Rejected for one reason — a closed set makes the
  eighth-stage property **unwriteable**. Proving that an *unanticipated* stage
  keeps its arms requires inserting a stage the production code has never heard
  of, which a fixed-size array forbids. Closure over the unknown is the property
  this ADR is buying; a table over today's seven proves only today's
  correctness. Secondarily, the constants' string values are already the log and
  wire vocabulary, so an int enum would trade the ordered list for a `String()`
  method — a wash on declarations, worse to read.
- **Deep-cloning at publish** rather than sharing the map. Rejected: it would
  demote ADR 0018's rebuild-not-shift from load-bearing to belt-and-braces,
  leaving a comment describing a hazard that no longer exists.
- **A binding table in the server converter** (`{stage, *list, *count}` triples)
  to make "declared once" literally true across the wire layer too. Rejected:
  ADR 0002's point is that the boundary *is* a hand-written mapping, and a
  missing stage there is a visible rendering gap, not a silent consent change.
  A test buys the same guarantee without trading a legible boundary for pointer
  indirection — and buys more than `make check` can, which cannot see a *missing*
  DTO field at all (`openapi.json` stays byte-identical, so drift reports clean).
- **Including the frontend.** Rejected: `SEGMENTS` and the station cards
  enumerate stages for *display* — labels that deliberately differ from the wire
  keys, per-stage copy, and per-stage behaviour (Changes Requested has no Arm
  control at all; Red and Running share a paired layout). That variation is the
  point. The frozen wire also offers no data-driven alternative.

## Consequences

- **Behavioural enumerations: zero.** The engine never names a stage except
  where the stage is the rule's subject (`armable`). **Contract enumerations:
  three**, all in `internal/server/outbound.go`, forced by the frozen JSON
  contract and guarded by a round-trip test that fails on an unhandled stage.
- **The Discussion gate stops being aspirational.** CONTEXT already claimed
  "the merge step only walks Ready, so the stage partition *is* the gate" — but
  `mergeArmedReady` took the whole snapshot and reached for `.Ready` by
  convention. Handed only `[]OutboundItem`, it now cannot reach In Discussion
  even by typo (ADR 0019).
- **ADR 0018 is amended**: the merge prune no longer "decrement[s] `Outgoing`",
  because there is no longer an `Outgoing` to decrement. Rebuild-not-shift is
  unaffected — that guards the backing array under `mergeArmedReady`'s active
  range, not the count.
- **The Funnel-partition doctrine acquires an asymmetry.** Outbound's counts now
  sum by construction in the literal sense (the count is a fold over the
  partition). Inbound's remain a *maintained* invariant: branch precedence in
  `RunCycleOnce` plus hand-mutated counts. Same words, two strengths — CONTEXT
  names the difference.
- **Drift between `OutboundStages()` and the constants is closed by test, not by
  type.** This is the price of the open set. The discipline that keeps it cheap:
  *loops that must be complete range the map; loops that must be ordered range
  the list* — and after this change the only ordered consumer in Go is the
  `rebuildOutbound` log line, where an omission is cosmetic. A `github` test
  drives the fold over its full finite input space and asserts set-equality with
  the list in both directions, so neither an unreachable constant nor an
  unlisted classifier branch survives.
- **Inbound is untouched.** The `Funnel`'s five lists have a similar shape, but
  the branch precedence in `RunCycleOnce` *is* that partition and those branches
  interleave real I/O (approve, screener consult, notifier fires). Different
  problem, different answer.
