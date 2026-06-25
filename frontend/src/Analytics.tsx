import { useEffect, useRef, useState } from "react";
import {
  fetchAnalytics,
  updateSettings,
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

  // refetch pulls the current range immediately (not behind the debounce). The
  // assumption-chip Save uses it so the time/money figures recompute server-side
  // the moment the constants change — no restart, no waiting on the picker timer.
  const refetch = () =>
    fetchAnalytics(range, days)
      .then(setData)
      .catch(() => setData(null));

  useEffect(() => {
    writeControls(range, days);
    const run = () => refetch();
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
        <TimePicker
          range={range}
          days={days}
          onRange={setRange}
          onDays={setDays}
        />
      </div>

      {data === null ? (
        <div className="card-loading">Loading analytics…</div>
      ) : (
        <>
          <div className="stats-row">
            <SplitStat
              testid="stat-auto-approved"
              label="Auto-approved"
              count={data.auto_approved.count}
              share={data.auto_approved.share}
              delta={data.auto_approved.delta}
            />
            <SplitStat
              testid="stat-human-review"
              label="Human review"
              count={data.human_review.count}
              share={data.human_review.share}
              delta={data.human_review.delta}
            />
            <Stat
              testid="stat-switches-saved"
              label="Context switches saved"
              count={data.switches_saved}
              hours={data.switches_saved_hours}
              money={data.switches_saved_money}
              assumptions={data.assumptions}
              delta={data.switches_saved_delta}
              onSave={async (next) => {
                await updateSettings(next);
                await refetch();
              }}
            />
          </div>
          {/* One label for the row: every headline delta compares against the same
              elapsed-aligned previous window, so the comparison is named once, not
              per stat (ADR 0011). */}
          <p className="stats-delta-label">{data.delta_label}</p>
        </>
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
// fraction; the frontend formats it — the share never delta's, see slice 3), and
// the count's elapsed-aligned period delta as a badge.
function SplitStat({
  testid,
  label,
  count,
  share,
  delta,
}: {
  testid: string;
  label: string;
  count: number;
  share: number;
  delta: Delta;
}) {
  return (
    <div className="stat" data-testid={testid}>
      <span className="stat-label">{label}</span>
      <span className="stat-value tnum">{count}</span>
      <span className="stat-share tnum">{percent(share)}</span>
      <DeltaBadge delta={delta} />
    </div>
  );
}

// Stat is the Context-switches-saved headline: the raw count (= the auto-approved
// count; a Human Review approval is a switch the human DID take, so it is not
// saved) translated into the time and money it represents (slice 4, ADR 0010).
// The server owns the arithmetic; this renders hours and currency-prefixed money,
// the same period delta as the other headlines, and the clickable assumption chip
// that edits the constants the figures were computed from.
function Stat({
  testid,
  label,
  count,
  hours,
  money,
  assumptions,
  delta,
  onSave,
}: {
  testid: string;
  label: string;
  count: number;
  hours: number;
  money: number;
  assumptions: Settings;
  delta: Delta;
  onSave: (next: Settings) => Promise<void>;
}) {
  return (
    <div className="stat" data-testid={testid}>
      <span className="stat-label">{label}</span>
      <span className="stat-value tnum">{count}</span>
      <span className="stat-figures tnum">
        {formatHours(hours)} · {formatMoney(money, assumptions.currency)}
      </span>
      <AssumptionChip assumptions={assumptions} onSave={onSave} />
      <DeltaBadge delta={delta} />
    </div>
  );
}

// AssumptionChip renders the constants behind the time/money figures inline as a
// clickable chip ("× 23 min · $100/hr") and, when opened, a popover that edits and
// persists them (ADR 0010). Saving calls onSave (PUT /settings + a re-fetch), so
// the figures recompute server-side without a restart. The draft is local until
// Save, so a cancel leaves the persisted constants untouched.
function AssumptionChip({
  assumptions,
  onSave,
}: {
  assumptions: Settings;
  onSave: (next: Settings) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Settings>(assumptions);
  const [saving, setSaving] = useState(false);

  // Re-seed the draft whenever the persisted constants change (a fresh fetch) or
  // the popover (re)opens, so an edit always starts from the current values.
  const openEditor = () => {
    setDraft(assumptions);
    setOpen(true);
  };

  const save = async () => {
    setSaving(true);
    try {
      await onSave(draft);
      setOpen(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <span className="assumption">
      <button
        type="button"
        className="assumption-chip tnum"
        aria-label="assumptions"
        aria-expanded={open}
        onClick={openEditor}
      >
        × {assumptions.minutes_per_switch} min ·{" "}
        {assumptions.currency}
        {assumptions.hourly_rate}/hr
      </button>
      {open && (
        <div className="assumption-popover" role="dialog" aria-label="Edit assumptions">
          <label className="assumption-field">
            <span>Minutes per switch</span>
            <input
              type="number"
              min={1}
              className="tnum"
              aria-label="minutes per switch"
              value={draft.minutes_per_switch}
              onChange={(e) =>
                setDraft({ ...draft, minutes_per_switch: clampInt(e.target.value, 1) })
              }
            />
          </label>
          <label className="assumption-field">
            <span>Hourly rate</span>
            <input
              type="number"
              min={0}
              className="tnum"
              aria-label="hourly rate"
              value={draft.hourly_rate}
              onChange={(e) =>
                setDraft({ ...draft, hourly_rate: clampInt(e.target.value, 0) })
              }
            />
          </label>
          <label className="assumption-field">
            <span>Currency</span>
            <input
              type="text"
              className="assumption-currency"
              aria-label="currency"
              value={draft.currency}
              onChange={(e) => setDraft({ ...draft, currency: e.target.value })}
            />
          </label>
          <div className="assumption-actions">
            <button type="button" className="btn-cancel" onClick={() => setOpen(false)}>
              Cancel
            </button>
            <button
              type="button"
              className="btn-save"
              disabled={saving || draft.currency.trim() === ""}
              onClick={save}
            >
              Save
            </button>
          </div>
        </div>
      )}
    </span>
  );
}

// clampInt coerces a number input to an integer at or above min, so a blank or
// out-of-range entry never reaches the server (which also validates structurally).
function clampInt(raw: string, min: number): number {
  const n = Math.floor(Number(raw));
  return Number.isFinite(n) && n >= min ? n : min;
}

// formatHours renders the derived time to one decimal hour (e.g. 1.15 -> "1.2h"),
// the lean-back precision the dashboard wants — the server owns the arithmetic.
function formatHours(hours: number): string {
  return `${hours.toFixed(1)}h`;
}

// formatMoney prefixes the currency symbol onto the money figure rounded to whole
// units (e.g. 115 with "$" -> "$115"); the server computes the amount, the
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
