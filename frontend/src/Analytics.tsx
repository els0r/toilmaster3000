import { useEffect, useState } from "react";
import { fetchAnalytics, type Analytics } from "./api";

// AnalyticsPanel is the look-back dashboard (the Analytics tab): a stats row over
// the approval history showing how much toil the robot saved. Its cadence is
// lean-back — it fetches on mount (tab-open), NOT on the 10s poll the live panels
// use. The frontend is a pure renderer; all the aggregation math is server-side
// (ADR 0009). Slice 1 is today-scoped with no time picker, deltas, cohort, or
// scope filter yet.
export function AnalyticsPanel() {
  const [data, setData] = useState<Analytics | null>(null);

  useEffect(() => {
    fetchAnalytics()
      .then(setData)
      .catch(() => setData(null));
  }, []);

  return (
    <section className="card analytics-card">
      <div className="card-head">
        <h2 className="card-title">Analytics</h2>
        <div className="spacer" />
        <span className="card-note">today · look-back</span>
      </div>

      {data === null ? (
        <div className="card-loading">Loading analytics…</div>
      ) : (
        <div className="stats-row">
          <SplitStat
            testid="stat-auto-approved"
            label="Auto-approved"
            count={data.auto_approved.count}
            share={data.auto_approved.share}
          />
          <SplitStat
            testid="stat-human-review"
            label="Human review"
            count={data.human_review.count}
            share={data.human_review.share}
          />
          <Stat
            testid="stat-switches-saved"
            label="Context switches saved"
            count={data.switches_saved}
            note="interruptions the robot spared you"
          />
        </div>
      )}
    </section>
  );
}

// SplitStat is one side of the auto-vs-human partition: a headline count plus its
// share of the range total, rendered as a percentage (the wire carries a 0..1
// fraction; the frontend formats it — the share never delta's, see slice 3).
function SplitStat({
  testid,
  label,
  count,
  share,
}: {
  testid: string;
  label: string;
  count: number;
  share: number;
}) {
  return (
    <div className="stat" data-testid={testid}>
      <span className="stat-label">{label}</span>
      <span className="stat-value tnum">{count}</span>
      <span className="stat-share tnum">{percent(share)}</span>
    </div>
  );
}

// Stat is a bare headline stat (count + a static note), used for Context switches
// saved — the count is the auto-approved count (a Human Review approval is a
// switch the human DID take, so it is not saved). Slice 4 adds the derived
// time/money figures and the editable assumption chip.
function Stat({
  testid,
  label,
  count,
  note,
}: {
  testid: string;
  label: string;
  count: number;
  note: string;
}) {
  return (
    <div className="stat" data-testid={testid}>
      <span className="stat-label">{label}</span>
      <span className="stat-value tnum">{count}</span>
      <span className="stat-note">{note}</span>
    </div>
  );
}

// percent formats a 0..1 share as a whole-number percentage. An empty range is a
// 0 share -> "0%", so the empty-range guard (no divide-by-zero, server-side)
// surfaces honestly rather than as NaN.
function percent(share: number): string {
  return `${Math.round(share * 100)}%`;
}
