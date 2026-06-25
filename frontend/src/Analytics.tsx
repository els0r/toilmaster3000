import { useEffect, useRef, useState } from "react";
import {
  fetchAnalytics,
  type Analytics,
  type AnalyticsRange,
  type Delta,
  type Settings,
} from "./api";

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
      <div className="card-head analytics-head">
        <div className="analytics-heading">
          <h2 className="card-title">Analytics</h2>
          <p className="analytics-subtitle">How much the robot is handling for you</p>
        </div>
        <div className="spacer" />
        <div className="picker-area">
          <TimePicker
            range={range}
            days={days}
            onRange={setRange}
            onDays={setDays}
          />
          {/* The aligned-comparison label rides under the picker (Variant 2's
              compare slot): every headline delta measures against the same
              elapsed-aligned previous window, so it is named once (ADR 0011). */}
          {data !== null && <p className="stats-delta-label">{data.delta_label}</p>}
        </div>
      </div>

      {data === null ? (
        <div className="card-loading">Loading analytics…</div>
      ) : (
        <div className="stat-tiles">
          <SplitStat
            testid="stat-auto-approved"
            sparkTestid="sparkline-auto-approved"
            label="Auto-approved"
            count={data.auto_approved.count}
            share={data.auto_approved.share}
            delta={data.auto_approved.delta}
            series={data.auto_approved.series}
            caption="of all PRs in range"
          />
          <SplitStat
            testid="stat-human-review"
            sparkTestid="sparkline-human-review"
            label="Human review"
            count={data.human_review.count}
            share={data.human_review.share}
            delta={data.human_review.delta}
            series={data.human_review.series}
            caption="held back for you"
          />
          <Stat
            testid="stat-switches-saved"
            sparkTestid="sparkline-switches-saved"
            label="Context switches saved"
            count={data.switches_saved}
            moneyLow={data.switches_saved_money_low}
            moneyHigh={data.switches_saved_money_high}
            assumptions={data.assumptions}
            delta={data.switches_saved_delta}
            series={data.switches_saved_series}
          />
        </div>
      )}
    </section>
  );
}

// TimePicker is the Variant-2 range control: a dropdown whose toggle names the
// active window and whose menu offers the four mutually-exclusive ranges, plus a
// day-count input revealed only for the custom "last X days" range (the one range
// that carries a length on the wire). It is a pure controlled component —
// selection lives in AnalyticsPanel (and the URL); the server owns every boundary
// computation.
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
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // Dismiss the menu on an outside click or Escape, so it behaves like a real
  // dropdown rather than a panel that lingers once the user looks elsewhere.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const options: { value: AnalyticsRange; label: string }[] = [
    { value: "today", label: "Today" },
    { value: "week", label: "This week" },
    { value: "month", label: "This month" },
    { value: "days", label: `Last ${days} days` },
  ];
  const active = options.find((o) => o.value === range) ?? options[0];

  const pick = (value: AnalyticsRange) => {
    onRange(value);
    setOpen(false);
  };

  return (
    <div className="range-picker" ref={rootRef}>
      <button
        type="button"
        className="range-toggle"
        data-testid="range-toggle"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <CalendarIcon />
        <span className="range-toggle-label">{active.label}</span>
        <CaretIcon open={open} />
      </button>

      {open && (
        <div className="range-menu" role="menu" aria-label="Time range">
          {options.map(({ value, label }) => (
            <button
              key={value}
              type="button"
              role="menuitemradio"
              aria-checked={range === value}
              className={`range-menu-item${range === value ? " is-active" : ""}`}
              onClick={() => pick(value)}
            >
              <span className="range-menu-check" aria-hidden="true">
                {range === value ? "✓" : ""}
              </span>
              <span className="range-menu-label">{label}</span>
            </button>
          ))}
        </div>
      )}

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

// CalendarIcon and CaretIcon dress the range toggle (Variant 2): a calendar glyph
// leads, a caret that flips when the menu is open trails. Both are presentational.
function CalendarIcon() {
  return (
    <svg className="range-toggle-cal" width="15" height="15" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="4.5" width="18" height="16" rx="2.5" fill="none" stroke="currentColor" strokeWidth="1.7" />
      <path d="M3 9.5h18M8 2.5v4M16 2.5v4" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

function CaretIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`range-toggle-caret${open ? " is-open" : ""}`}
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M6 9l6 6 6-6" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// Sparkline draws a headline's per-day series as a row of bars (Variant 2) — one
// bar per day, scaled to the series max, the latest day highlighted as "current".
// It is presentational (aria-hidden); the count beside it carries the value for
// assistive tech. An absent/empty series simply yields no bars.
function Sparkline({ series, testid }: { series: number[] | null; testid: string }) {
  const data = series ?? [];
  const max = Math.max(1, ...data);
  return (
    <div className="sparkline" data-testid={testid} aria-hidden="true">
      {data.map((v, i) => (
        <span
          key={i}
          className={`spark-bar${i === data.length - 1 ? " is-current" : ""}`}
          style={{ height: `${Math.max(8, Math.round((v / max) * 100))}%` }}
        />
      ))}
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
// fraction; the frontend formats it — the share never delta's, see slice 3), and
// the count's elapsed-aligned period delta as a badge.
function SplitStat({
  testid,
  sparkTestid,
  label,
  count,
  share,
  delta,
  series,
  caption,
}: {
  testid: string;
  sparkTestid: string;
  label: string;
  count: number;
  share: number;
  delta: Delta;
  series: number[] | null;
  caption: string;
}) {
  return (
    <div className="stat-tile" data-testid={testid}>
      <div className="stat-tile-head">
        <span className="stat-label">{label}</span>
        <DeltaBadge delta={delta} />
      </div>
      <div className="stat-tile-value">
        <span className="stat-value tnum">{count}</span>
        <span className="stat-share tnum">{percent(share)}</span>
      </div>
      <Sparkline series={series} testid={sparkTestid} />
      <span className="stat-caption">{caption}</span>
    </div>
  );
}

// Stat is the Context-switches-saved headline: the raw count (= the auto-approved
// count; a Human Review approval is a switch the human DID take, so it is not
// saved) translated into the money it represents as a low/high range (ADR 0012).
// The server owns the arithmetic; this renders the count and the money range as a
// read-only pill that surfaces the per-switch band behind it, plus the same period
// delta as the other headlines. There is no hours figure — money no longer flows
// through hours × rate. Editing the band lives in the Settings tab.
function Stat({
  testid,
  sparkTestid,
  label,
  count,
  moneyLow,
  moneyHigh,
  assumptions,
  delta,
  series,
}: {
  testid: string;
  sparkTestid: string;
  label: string;
  count: number;
  moneyLow: number;
  moneyHigh: number;
  assumptions: Settings;
  delta: Delta;
  series: number[] | null;
}) {
  return (
    <div className="stat-tile" data-testid={testid}>
      <div className="stat-tile-head">
        <span className="stat-label">{label}</span>
        <DeltaBadge delta={delta} />
      </div>
      <div className="stat-tile-value">
        <span className="stat-value tnum">{count}</span>
      </div>
      <Sparkline series={series} testid={sparkTestid} />
      <div className="stat-tile-foot">
        <MoneyPill low={moneyLow} high={moneyHigh} assumptions={assumptions} />
      </div>
    </div>
  );
}

// MoneyPill renders the money headline as a low/high range pill over its per-switch
// basis ("CHF10–26 / switch"), so the figure reads honestly without a separate chip
// (ADR 0012). An empty range (low == high == 0) collapses to a single CHF0 rather
// than a backwards "CHF0 – CHF0". It is read-only — the band is edited in the
// Settings tab — and a title repeats the basis as a full sentence on hover.
function MoneyPill({
  low,
  high,
  assumptions,
}: {
  low: number;
  high: number;
  assumptions: Settings;
}) {
  const { cost_low, cost_high, currency } = assumptions;
  const amount =
    Math.round(low) === Math.round(high)
      ? formatMoney(low, currency)
      : `${formatMoney(low, currency)} – ${formatMoney(high, currency)}`;
  return (
    <span
      className="money-pill"
      data-testid="switches-money-pill"
      title={`Each avoided review ≈ one context switch worth ${currency}${cost_low}–${cost_high} (a ~10-min refocus up to a 23-min flow break); the range scales that band by the saved-switch count`}
    >
      <span className="money-pill-amount tnum">{amount}</span>
      <span className="money-pill-basis tnum">
        {currency}
        {cost_low}–{cost_high} / switch
      </span>
    </span>
  );
}

// formatMoney prefixes the currency symbol onto the money figure rounded to whole
// units (e.g. 115 with "CHF" -> "CHF115"); the server computes the amount, the
// frontend formats it (mirroring how Stat.Share is a fraction formatted here).
function formatMoney(money: number, currency: string): string {
  return `${currency}${Math.round(money)}`;
}

// DeltaBadge renders a headline's elapsed-aligned period change (ADR 0011). The
// server classifies the comparison so the frontend never divides by zero: a
// "changed" delta shows a direction arrow + color + signed percentage; a "new"
// baseline (prior period empty) reads "new"; a "none" delta (both periods empty)
// reads "—". The arrow/color direction is derived from the sign of pct.
function DeltaBadge({ delta }: { delta: Delta }) {
  if (delta.state === "none") {
    return (
      <span className="stat-delta is-none" title="nothing to compare in either period">
        —
      </span>
    );
  }
  if (delta.state === "new") {
    return (
      <span className="stat-delta is-new" title="no prior-period baseline">
        new
      </span>
    );
  }
  const dir = delta.pct > 0 ? "up" : delta.pct < 0 ? "down" : "flat";
  const arrow = dir === "up" ? "↑" : dir === "down" ? "↓" : "→";
  return (
    <span className={`stat-delta is-${dir} tnum`}>
      {arrow} {signedPercent(delta.pct)}
    </span>
  );
}

// percent formats a 0..1 share as a whole-number percentage. An empty range is a
// 0 share -> "0%", so the empty-range guard (no divide-by-zero, server-side)
// surfaces honestly rather than as NaN.
function percent(share: number): string {
  return `${Math.round(share * 100)}%`;
}

// signedPercent formats a signed delta fraction as a whole-number percentage with
// an explicit + on growth (e.g. 0.5 -> "+50%", -0.2 -> "-20%"); the minus rides
// the number for a drop. Only called for the "changed" state, so pct is finite.
function signedPercent(pct: number): string {
  const sign = pct > 0 ? "+" : "";
  return `${sign}${Math.round(pct * 100)}%`;
}
