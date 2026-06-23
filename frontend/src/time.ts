import { useEffect, useState } from "react";

// useNow returns a clock that re-renders the caller every intervalMs, so
// relative times ("3s ago", "next sync in 7s") tick live without each
// component wiring its own timer.
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

// timeAgo renders the gap between now and a past timestamp (both ms) as a
// compact relative string, matching the design's buckets.
export function timeAgo(now: number, ts: number): string {
  const s = Math.floor((now - ts) / 1000);
  if (s < 45) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

// clock formats a timestamp (ms) as a short absolute date-time for the feed's
// hover title.
export function clock(ts: number): string {
  return new Date(ts).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
