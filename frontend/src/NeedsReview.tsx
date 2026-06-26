import { useState } from "react";
import { approveQueueItem, type QueueItem } from "./api";
import { DiffCard } from "./DiffCard";
import { DiffMag } from "./DiffMag";
import { PrRow } from "./PrRow";

// REASON_BREAKING is the engine's breaking-change queue reason. When
// title_parts.breaking is true the queue shows a dedicated breaking badge, so
// this reason is filtered from the reason chips — an Approve-tied breaking PR
// shows the badge without an orphan breaking_change chip (ADR 0006).
const REASON_BREAKING = "breaking_change";

// DiffPill wraps the shared DiffMag leaf in a clickable pill: the same green-bold
// +add / red-bold −del / muted K-files magnitude, made a button that opens the
// Diff card for that PR — a skim of the change without leaving tm3k. The
// magnitude convention lives in DiffMag; clickability is this caller's concern.
function DiffPill({ q, onOpen }: { q: QueueItem; onOpen: () => void }) {
  return (
    <button
      type="button"
      className="diff-pill tnum"
      aria-label={`view diff for #${q.number}`}
      onClick={onOpen}
    >
      <DiffMag item={q} />
    </button>
  );
}

// NeedsReview is the actionable Needs-Human-Review panel: PRs routed here for
// one or more reasons. Each entry has a GitHub link, one badge per reason, and
// an Approve button — an explicit human override. Approving calls the backend
// and then refetches, so the approved item leaves the queue on the next cycle.
export function NeedsReview({
  queue,
  onApproved,
}: {
  queue: QueueItem[] | null;
  onApproved?: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<number | null>(null);
  // diffFor is the PR number whose Diff card is open (null = none). The pill sets
  // it; the card clears it on close.
  const [diffFor, setDiffFor] = useState<number | null>(null);

  async function approve(number: number) {
    setError(null);
    setPending(number);
    try {
      await approveQueueItem(number);
      onApproved?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setPending(null);
    }
  }

  return (
    <section className="card">
      <div className="card-head action">
        <h2 className="card-title">Needs Human Review</h2>
        <span className="card-count action tnum">{queue?.length ?? 0}</span>
        <div className="spacer" />
        <span className="card-note action">held back from auto-approval</span>
      </div>

      {error && (
        <p className="row-alert" role="alert">
          {error}
        </p>
      )}

      {queue === null ? (
        <div className="card-loading">Loading queue…</div>
      ) : queue.length === 0 ? (
        <div className="card-empty">
          Nothing needs review — the robot has it handled.
        </div>
      ) : (
        <div className="pr-list">
          {queue.map((q) => {
            // The breaking badge represents the breaking_change reason, so the
            // reason chips exclude it: an Approve-tied breaking PR shows the
            // badge without an orphan breaking_change chip.
            const reasonChips = (q.reasons ?? []).filter(
              (r) => r !== REASON_BREAKING,
            );
            return (
              <PrRow
                key={q.number}
                item={q}
                meta={
                  <>
                    <DiffPill q={q} onOpen={() => setDiffFor(q.number)} />
                    {(q.title_parts.breaking || reasonChips.length > 0) && (
                      <span className="sep">·</span>
                    )}
                    {q.title_parts.breaking && (
                      <span className="badge-breaking">
                        <span className="dot" />
                        breaking change
                      </span>
                    )}
                    {reasonChips.map((reason) => (
                      <span key={reason} className="badge-breaking">
                        <span className="dot" />
                        {reason}
                      </span>
                    ))}
                  </>
                }
                action={
                  <button
                    type="button"
                    className="btn-approve"
                    onClick={() => approve(q.number)}
                    disabled={pending === q.number}
                    aria-label={`approve #${q.number}`}
                  >
                    Approve
                  </button>
                }
              />
            );
          })}
        </div>
      )}

      {diffFor !== null &&
        (() => {
          const q = queue?.find((x) => x.number === diffFor);
          return q ? (
            <DiffCard q={q} onClose={() => setDiffFor(null)} />
          ) : null;
        })()}
    </section>
  );
}
