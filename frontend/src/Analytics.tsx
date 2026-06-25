import { useEffect, useRef, useState } from "react";
import { fetchAnalytics, type Analytics, type AnalyticsRange } from "./api";

// DEBOUNCE_MS collapses a burst of picker changes (range clicks, day-count
// keystrokes) into a single fetch, so the lean-back dashboard doesn't hammer the
// endpoint while the user is still settling on a window.
const DEBOUNCE_MS = 300;
const DEFAULT_DAYS = 7;

// AnalyticsPanel is the look-back dashboard (the Analytics tab): a time picker
// over a stats row, showing how much toil the robot saved for the selected range.
// Its cadence is lean-back — it fetches on tab-open and on each picker change
// (debounced), NOT on the 10s poll the live panels use. The frontend is a pure
// renderer: the correctness-critical range/boundary math is server-side (ADR
// 0009 / 0011). Slice 2 adds the picker; deltas, cohort, and scope filter follow.
export function AnalyticsPanel() {
  const [data, setData] = useState<Analytics | null>(null);
  const [range, setRange] = useState<AnalyticsRange>(() => readControls().range);
  const [days, setDays] = useState<number>(() => readControls().days);
  const firstRef = useRef(true);

  useEffect(() => {
    writeControls(range, days);
    const run = () =>
      fetchAnalytics(range, days)
        .then(setData)
        .catch(() => setData(null));
    // The first run (tab-open) fetches immediately so the dashboard paints without
    // a delay; later runs (range / day-count edits) debounce so a burst collapses
    // to one request.
    if (firstRef.current) {
      firstRef.current = false;
      run();
      return;
    }
    const id = setTimeout(run, DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [range, days]);

  return (
    <section className="card analytics-card">
      <div className="card-head">
        <h2 className="card-title">Analytics</h2>
        <div className="spacer" />
        <span className="card-note">look-back</span>
      </div>

      <TimePicker
        range={range}
        days={days}
        onRange={setRange}
        onDays={setDays}
      />

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

// TimePicker is the lightweight Grafana-style range control: four mutually
// exclusive windows, plus a day-count input revealed only for the custom "last X
// days" range (the one range that carries a length on the wire). It is a pure
// controlled component — selection lives in AnalyticsPanel (and the URL); the
// server owns every boundary computation.
function TimePicker({
  range,
  days,
  onRange,
  onDays,
}: {
  range: AnalyticsRange;
  days: number;
  onRange: (r: AnalyticsRange) => void;
  onDays: (d: number) => void;
}) {
  const options: { value: AnalyticsRange; label: string }[] = [
    { value: "today", label: "Today" },
    { value: "week", label: "This week" },
    { value: "month", label: "This month" },
    { value: "days", label: `Last ${days} days` },
  ];
  return (
    <div className="time-picker" role="group" aria-label="Time range">
      {options.map(({ value, label }) => (
        <button
          key={value}
          type="button"
          className={`range-btn${range === value ? " is-active" : ""}`}
          aria-pressed={range === value}
          onClick={() => onRange(value)}
        >
          {label}
        </button>
      ))}
      {range === "days" && (
        <label className="day-count">
          <span className="day-count-label">days</span>
          <input
            type="number"
            min={1}
            className="day-count-input tnum"
            aria-label="day count"
            value={days}
            onChange={(e) => onDays(clampDays(e.target.value))}
          />
        </label>
      )}
    </div>
  );
}

// clampDays coerces the day-count input to a positive integer — an empty or
// sub-1 entry falls back to 1, so the rolling window is never zero-length or
// negative (the server also validates `days >= 1`).
function clampDays(raw: string): number {
  const n = Math.floor(Number(raw));
  return Number.isFinite(n) && n >= 1 ? n : 1;
}

// The valid range values, used to validate a range read back from the URL.
const RANGES: AnalyticsRange[] = ["today", "week", "month", "days"];

// readControls seeds the picker from the URL search params so a reload or a
// pasted link restores the selected range and day count; an absent or invalid
// value falls back to the defaults (today, 7 days).
function readControls(): { range: AnalyticsRange; days: number } {
  const p = new URLSearchParams(window.location.search);
  const r = p.get("range") ?? "";
  const range = (RANGES as string[]).includes(r) ? (r as AnalyticsRange) : "today";
  const days = clampDays(p.get("days") ?? "");
  return { range, days: p.get("days") ? days : DEFAULT_DAYS };
}

// writeControls reflects the current selection into the URL (replaceState, no
// history entry), preserving the tab hash so the picker state and the hash-tab
// routing stay orthogonal. `days` rides the URL only for the days range.
function writeControls(range: AnalyticsRange, days: number) {
  const p = new URLSearchParams(window.location.search);
  p.set("range", range);
  if (range === "days") {
    p.set("days", String(days));
  } else {
    p.delete("days");
  }
  const search = p.toString();
  const url = `${window.location.pathname}${search ? `?${search}` : ""}${window.location.hash}`;
  window.history.replaceState(null, "", url);
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
