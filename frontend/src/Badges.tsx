// Badges.tsx holds the shared row badges that ride a PrRow's meta line on both
// sides of the funnel pair — extracted at the second use (the inbound queue and
// the outbound stations) rather than copy-pasted, per the shared-deep-module
// convention (ADR 0014).

// BreakingBadge renders the standing breaking-change display fact: shown
// whenever title_parts.breaking is true (any `!` title), independent of what
// queued or staged the PR. Inbound it marks a PR the Invariant holds back;
// outbound it means "arm with open eyes."
export function BreakingBadge() {
  return (
    <span className="badge-breaking">
      <span className="dot" />
      breaking change
    </span>
  );
}

// ArmedBadge is the outbound consent marker: the per-PR Armed state riding
// the row in whatever stage the PR is in — orthogonal to the stage partition,
// a badge and never a stage. A row without it is Withheld, the default.
export function ArmedBadge() {
  return (
    <span className="badge-armed">
      <span className="dot" />
      armed
    </span>
  );
}

// ConflictBadge is the Ready station's conflict marker: a Ready PR whose
// branch conflicts with its base stays in Ready (the stage partition is total)
// but never auto-merges until the conflict — which is on you — is resolved.
export function ConflictBadge() {
  return (
    <span className="badge-conflict">
      <span className="dot" />
      merge conflict
    </span>
  );
}
