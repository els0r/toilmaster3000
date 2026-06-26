import { useEffect, useRef, useState } from "react";
import { fetchStatus, type CycleStatus } from "./api";
import { timeAgo, useNow } from "./time";

// StatusLine is the heartbeat strip: a glance that confirms the robot is alive —
// last cycle, outcome (OK or a backed-off error), approved/queue counts, and a
// live countdown to the next poll.
//
// When given a `status` prop it renders that (the App polls and drives it); with
// no prop it self-fetches once on mount (standalone use).
export function StatusLine({
  status: provided,
  pollMs = 10_000,
}: {
  status?: CycleStatus | null;
  pollMs?: number;
}) {
  const [fetched, setFetched] = useState<CycleStatus | null>(null);
  const controlled = provided !== undefined;

  useEffect(() => {
    if (controlled) return;
    fetchStatus().then(setFetched).catch(() => setFetched(null));
  }, [controlled]);

  const status = controlled ? provided : fetched;

  const now = useNow(1000);
  // Record when the displayed status last changed identity (i.e. a fresh poll
  // landed) so the "next sync" countdown restarts from each poll.
  const polledAtRef = useRef(now);
  useEffect(() => {
    polledAtRef.current = Date.now();
  }, [status]);

  if (!status) {
    return (
      <header className="hb-strip">
        <Brand idle />
        <span className="hb-ago">Loading status…</span>
      </header>
    );
  }

  if (status.outcome === "never_run") {
    return (
      <header className="hb-strip">
        <Brand idle />
        <span className="hb-ago">Cycle has never run</span>
        <div className="spacer" />
        <Counts status={status} />
      </header>
    );
  }

  const isErr = status.outcome !== "ok";
  const lastRunMs = status.last_run ? Date.parse(status.last_run) : null;
  const lastRunAgo =
    lastRunMs !== null && !Number.isNaN(lastRunMs)
      ? timeAgo(now, lastRunMs)
      : "—";

  const nextInS = Math.max(
    0,
    Math.ceil((pollMs - (now - polledAtRef.current)) / 1000),
  );
  const nextSyncText = nextInS <= 0 ? "now" : `in ${nextInS}s`;

  return (
    <header className="hb-strip">
      <Brand />

      <div className="hb-group">
        <span className="hb-label">Last cycle</span>
        <span className="hb-ago tnum">{lastRunAgo}</span>
      </div>

      {isErr ? (
        <div className="hb-badge-err">
          <span className="hb-bang">!</span>
          <span className="label">{status.outcome}</span>
        </div>
      ) : (
        <div className="hb-badge-ok">
          <span className="dot" />
          <span className="label">OK</span>
        </div>
      )}

      <div className="spacer" />

      <Counts status={status} />

      <div className="hb-sync">
        <span className="hb-spinner" />
        <span className="text tnum">next sync {nextSyncText}</span>
      </div>
    </header>
  );
}

// Brand is the pulsing dot + wordmark. `idle` stills the pulse for the loading
// and never-run states.
function Brand({ idle = false }: { idle?: boolean }) {
  const mod = idle ? " is-idle" : "";
  return (
    <div className="hb-brand">
      <span className="hb-pulse">
        <span className={`hb-pulse-ring${mod}`} />
        <span className={`hb-pulse-dot${mod}`} />
      </span>
      <span className="hb-title">
        toilmaster<span className="hb-title-dim">3000</span>
      </span>
    </div>
  );
}

// Counts renders the approved/queue/dropped tallies plus the live funnel's
// staging count — the new actionable signal surfaced on the always-polled
// heartbeat so it shows from every tab. Each label+number stays in a single
// element so "approved N" / "staging N" read as one phrase.
function Counts({ status }: { status: CycleStatus }) {
  return (
    <div className="hb-counts tnum">
      <span className="approved">approved {status.approved_count}</span>
      <span className="sep">·</span>
      <span className="queue">queue {status.queue_count}</span>
      <span className="sep">·</span>
      <span className="dropped">dropped {status.dropped_count}</span>
      <span className="sep">·</span>
      <span className="staging">staging {status.staging_count}</span>
    </div>
  );
}
