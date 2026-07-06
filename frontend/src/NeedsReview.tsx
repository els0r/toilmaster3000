import { useState } from "react";
import { approveQueueItem, type QueueItem } from "./api";
import { DiffPill } from "./DiffPill";
import { PrRow } from "./PrRow";

// REASON_BREAKING is the engine's breaking-change queue reason. When
// title_parts.breaking is true the queue shows a dedicated breaking badge, so
// this reason is filtered from the reason chips — an Approve-tied breaking PR
// shows the badge without an orphan breaking_change chip (ADR 0006).
const REASON_BREAKING = "breaking_change";

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
                    <DiffPill item={q} />
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
    </section>
  );
}
