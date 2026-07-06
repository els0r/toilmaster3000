import { useState } from "react";
import { DiffCard } from "./DiffCard";
import { DiffMag } from "./DiffMag";

// DiffPill wraps the shared DiffMag leaf in a clickable pill: the same
// green-bold +add / red-bold −del / muted K-files magnitude, made a button
// that opens the Diff card for that PR — a skim of the change without leaving
// tm3k. It owns its own open/closed state and the card it opens, so any
// station rendering a diff need only drop this in with no parent wiring.
export function DiffPill({
  item,
}: {
  item: {
    number: number;
    url: string;
    additions: number;
    deletions: number;
    changed_files: number;
  };
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        className="diff-pill tnum"
        aria-label={`view diff for #${item.number}`}
        onClick={() => setOpen(true)}
      >
        <DiffMag item={item} />
      </button>
      {open && <DiffCard q={item} onClose={() => setOpen(false)} />}
    </>
  );
}
