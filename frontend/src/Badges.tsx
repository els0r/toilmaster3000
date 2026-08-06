// Badges.tsx holds the shared row badges that ride a PrRow's meta line on both
// sides of the funnel pair — extracted at the second use (the inbound queue and
// the outbound stations) rather than copy-pasted, per the shared-deep-module
// convention (ADR 0014).

import { useState } from "react";
import type { ScreenHold } from "./api";

// ScreenHoldChips renders a screen-held queue row's marker: one screen:<name>
// chip per holding Screen, derived from the screen_holds structure (never
// prefix-sniffed out of reasons) and visually distinct from rule chips — a
// robot objection reads differently from a rule route at a glance (ADR 0022).
// Each chip is a disclosure button: clicking reveals the screens' prose
// reasoning right under the meta line (the DiffPill idiom — the affordance
// owns its own open state, so any station can drop it in without wiring).
export function ScreenHoldChips({
  holds,
  number,
}: {
  holds: ScreenHold[];
  number: number;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      {holds.map((h) => (
        <button
          key={h.screen}
          type="button"
          className="badge-screen"
          aria-expanded={open}
          aria-label={`screen ${h.screen} reasoning for #${number}`}
          onClick={() => setOpen((o) => !o)}
        >
          <span className="dot" />
          screen:{h.screen}
        </button>
      ))}
      {open && (
        <div className="screen-prose">
          {holds.map((h) => (
            <p key={h.screen}>
              <strong>{h.screen}</strong> {h.reason}
            </p>
          ))}
        </div>
      )}
    </>
  );
}

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
