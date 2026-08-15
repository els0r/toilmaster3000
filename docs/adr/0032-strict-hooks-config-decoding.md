# Strict hooks.yaml decoding supersedes silent-ignore

## Context

ADR 0023 established `hooks.yaml` as boot-loaded, preflight-validated config
with one uniform failure mode: bad config refuses startup naming the
offender. But `yaml.Unmarshal` only ever binds the keys it recognises —
anything else, a miscased key, a typo, a field that exists on the *sibling*
hook kind, is silently dropped. ADR 0026/0027 treated one instance of that as
a feature: `Paths` and `WorkDir` deliberately do not exist on `Spec`/
`ScreenConfig`, so the un-gating hazard of a scoped Screen is "unrepresentable
rather than validated against" — an operator who writes `Paths` on a Screen
anyway gets a Screen that runs exactly as if they had written nothing, no
warning.

That reasoning has a blind spot: unrepresentable at the type level is not the
same as *caught* at the config level. An operator who writes `Paths` under a
Screen entry, expecting it to scope the Screen, gets a Screen that silently
gates every PR as if they had written nothing — the single most consequential
kind of mistake this system can make (a widened, not narrowed, screening
surface), with zero signal that anything went wrong. The same blind spot
applies to every other field: a miscased `requires:` (the tag is `Requires`,
matched case-sensitively) silently drops a hook's declared preconditions,
defeating ADR 0031's whole mechanism without a trace.

## Decision

1. **`hook.Load` decodes `hooks.yaml` with `yaml.v3`'s `KnownFields(true)`**
   (via a `Decoder`, not `Unmarshal`). Any key that does not match a field of
   the struct it is decoding into refuses the boot, wrapped in
   `ErrUnknownField` — the same "refuse-at-boot naming the offender" family as
   `ErrUnknownHarness`/`ErrBadPattern`/`ErrBadWorkDir` (ADR 0023).

2. **Only genuine unknown-field failures are tagged.** `yaml.v3` reports them
   as a `*yaml.TypeError` whose `Errors` entries read `field X not found in
   type Y` — parsed out and wrapped. Every other decode failure (malformed
   YAML, `Duration`'s custom `UnmarshalYAML` rejecting a bad value) keeps
   surfacing as a plain parse error exactly as before: a bad *value* is a
   different mistake than an unrecognised *key*, and conflating the two under
   one sentinel would mislabel it.

3. **This supersedes ADR 0026 decision 2 and ADR 0027 decision 2's
   silent-ignore *consequence*, not their structural design.** `Paths` and
   `WorkDir` still do not exist on `Spec`/`ScreenConfig` — that decision
   stands, and remains the reason the hazard can be caught at all (an unknown
   key is definitionally a key with no field to decode into). What changes is
   what happens when an operator writes one anyway: previously nothing
   observable; now a named boot refusal identifying the key, and — via
   `yaml.v3`'s own line number in the wrapped message — its position in the
   file.

## Considered Options

- **Case-insensitive key matching.** Rejected: it papers over one class of
  typo (casing) without catching the field-belongs-to-the-other-kind mistake
  that motivated this ADR, and the codebase already treats miscasing as an
  error family (`ErrMissingName`, `ErrUnknownHarness`), never a normalisation
  opportunity.
- **A hand-written schema/allowlist per hook kind, checked at `validate()`
  time (after a lenient decode).** Rejected: `yaml.v3` already tracks,
  structurally, which keys had nowhere to go; re-deriving that mapping by
  hand duplicates the struct tags `KnownFields` already reads — exactly the
  class of drift-prone duplication CLAUDE.md's extract-shared-module rule
  warns against.
- **A bespoke message distinguishing "known field, wrong kind" from
  "genuinely unknown field".** Rejected for this slice: `yaml.v3`'s own
  message already names the field and the struct type it failed to match,
  which is enough to locate the mistake; a hand-maintained cross-kind
  message would need its own field inventory for a marginal readability gain.

## Consequences

- A Screen accidentally carrying `Paths` or `WorkDir` now refuses the boot
  naming the field, closing the "silently un-gates a whole file class" hazard
  ADR 0026 accepted as a residual risk.
- Every hand-written `hooks.yaml` must now be spelled exactly right —
  PascalCase, no stray keys. The self-heal write path is unaffected: marshal
  and the strict decoder read the same struct tags, so a heal-then-reload
  round-trips (`TestLoadSelfHealRoundTripsUnderStrictDecoding`).
- ADR 0026/0027's "unrepresentable" framing is now two-layered: the type
  system still makes a cross-kind field impossible to *populate*; strict
  decoding now makes writing it anyway impossible to do *silently*. Both are
  amended with a pointer here.
- **No wire change.** Hooks stay hand-edited and boot-loaded (ADR 0023), so
  `openapi.json` and `schema.d.ts` are untouched.
