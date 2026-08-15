# Hook eligibility: declared preconditions, and why only Notifiers may decline

## Context

ADR 0023 made hook config boot-loaded and preflight-validated, with one uniform
failure mode: bad config refuses startup naming the offending hook, and
`checkHarnessBinaries` hard-exits when an enabled hook's harness CLI is absent.
That was right when every hook was a Screen or Notifier over a single forge.

Two things broke it. Forge support (ADR 0030) makes a **mixed portfolio** the
realistic config: one `hooks.yaml` holding GitHub-scoped hooks, GitLab-scoped
hooks, and forge-agnostic ones, with two instances reading it. Under uniform
hard-fail, neither instance boots. And the same shape appears with no forge in
sight — a Slack Notifier whose token is unset should disable that Notifier, not
take down the tool.

But the naive fix — soft-fail everything, warn and disable — is unsafe in one
direction. `buildScreener` documents that zero configured Screens leaves the
engine behaving "bit-for-bit as before screens existed". Soft-disabling a Screen
therefore does not degrade a gate, it **removes** it: every PR that Screen was
holding is auto-approved, and the only trace is a warning at startup. That is
never-fire-on-no-signal inverted.

## Decision

1. **A hook declares its preconditions** in an optional `Requires` block on the
   shared Spec — `Forge` (which forge it works against) and `Tools` (binaries
   that must be on PATH). Both are hard-checked at boot. This exists because
   tm3k **cannot inspect what a hook actually does**: with `WorkDir` (ADR 0027)
   the real behaviour lives in a skill file, so if that skill shells `gh`,
   nothing in the system can know. The operator knows, asserts it, and tm3k
   enforces the assertion. tm3k cannot verify the assertion is *complete* — an
   operator who declares nothing gets nothing — so this is a correctness
   control the operator opts into, not a boundary.

2. **Ineligible is not broken.** The two are different facts and get different
   handling:
   - **Ineligible** — the hook declares it does not apply here (`Forge: github`
     on a GitLab instance, or `Tools` naming the other forge's CLI). It was
     never a gate *on this instance*. Skipped, logged at boot, **both kinds**.
     This is what makes the mixed portfolio work.
   - **Broken** — the hook is in scope for this instance and cannot run. Classify
     checks a PATH fact only — the harness binary or a declared `Tools` binary
     missing — never a credential; tm3k has no offline probe for a token or
     login state, so that class of precondition is not boot-checkable by this
     mechanism.

3. **A broken Notifier declines; a broken Screen refuses the boot.** The
   asymmetry is not a wart — it is ADR 0021's irreconcilable failure contracts
   showing through. A Notifier's failure "can never block or divert an engine
   action", so disabling one is harmless by construction, and *declining* is
   already this codebase's word for it (the language-keyed review-assists
   decline). A Screen gates; an unrunnable Screen is a gate that does not
   exist. `checkHarnessBinaries`' own rationale settles it: a missing binary
   "must refuse startup, not burn failed attempts one screened PR at a time" —
   which also rejects the alternative of booting and letting ADR 0022's
   3-strikes machinery synthesize holds forever for a condition fully knowable
   at boot.

4. **`Requires.Tools` is additive to the grant, never a replacement.** The tool
   authority becomes the active forge's CLI **plus** whatever `Tools` declares.
   Absent `Tools` is today's behaviour bit-for-bit, and an incomplete list can
   never strip authority a hook already had — the failure mode that killed the
   replace-the-grant design, where forgetting `jq` silently breaks a working
   hook. Declaring the *other* forge's CLI is therefore self-describing: it
   makes the hook ineligible here without needing `Forge` spelled out too.

5. **Selecting a CLI is enforced; narrowing within one stays prose.** These are
   different claims and only one is honest. `Bash(gh pr comment:*)` buys nothing
   because `gh api` reaches the approve endpoint — ADR 0023's reasoning, still
   correct, and it applies identically to `glab api`. But *which* CLI a hook
   may invoke is a static fact with no such escape, so a hook on a GitLab
   instance simply never receives `gh`. Verb ceilings stay in the composed
   prompt; toolchain selection moves into config and is enforced.

## Consequences

- **One `hooks.yaml` now serves both forges**, which is the point. The cost is
  that "enabled" and "eligible" are different states, distinguishable only in
  the boot log — there is no hooks UI (ADR 0023, still deferred) and this does
  not justify building one.
- **`Requires` generalises past forges.** The Slack-token case needs no new
  mechanism, and neither will the next environmental precondition. It was
  deliberately not modelled as a predicate expression language: ADR 0023
  rejected exactly that speculative generality once already, and two concrete
  fields cover every case in hand.
- **A silently-skipped hook is a real failure mode.** An operator who
  mis-scopes a hook sees it skipped, not refused. Accepted for Notifiers by
  ADR 0021's contract; not accepted for Screens, which is why they refuse.
- **Credential and environment preconditions surface at run time, not boot,**
  and fall to ADR 0021's existing per-kind failure contracts: an unset token
  or a dead login is invisible to Classify's PATH-only check, so a Notifier
  hitting it fails its `Act` call and declines harmlessly like any other
  Notifier failure; a Screen hitting it fails its `Screen` call and holds via
  the existing 3-strikes machinery (ADR 0022) rather than being caught at
  boot.
