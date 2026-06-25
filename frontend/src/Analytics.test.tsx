import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { AnalyticsPanel } from "./Analytics";
import type { Analytics } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return { ...actual, fetchAnalytics: vi.fn() };
});

import { fetchAnalytics } from "./api";
const mockAnalytics = vi.mocked(fetchAnalytics);

const analytics = (over: Partial<Analytics> = {}): Analytics => ({
  auto_approved: { count: 3, share: 0.75, delta: { pct: 0, state: "none" }, series: [1, 2] },
  human_review: { count: 1, share: 0.25, delta: { pct: 0, state: "none" }, series: [0, 1] },
  switches_saved: 3,
  switches_saved_series: [1, 2],
  switches_saved_hours: 1.2,
  switches_saved_money: 115,
  switches_saved_delta: { pct: 0, state: "none" },
  delta_label: "vs yesterday",
  assumptions: { minutes_per_switch: 23, hourly_rate: 100, currency: "$" },
  ...over,
});

beforeEach(() => {
  vi.useFakeTimers();
  mockAnalytics.mockReset();
  // The picker reflects its selection into the URL; reset it so each test starts
  // from the default (today) rather than inheriting a prior test's range.
  window.history.replaceState(null, "", "/");
});

afterEach(() => {
  vi.useRealTimers();
});

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("AnalyticsPanel stats row", () => {
  // A1: the panel fetches on mount and renders the three headline stats —
  // Auto-approved and Human Review each with their count and share-as-percent,
  // and Context switches saved as the raw count (= the auto count).
  it("renders auto/human counts + shares and switches saved from the fetch", async () => {
    mockAnalytics.mockResolvedValue(analytics());

    render(<AnalyticsPanel />);
    await flush();

    expect(mockAnalytics).toHaveBeenCalledTimes(1);

    const auto = screen.getByTestId("stat-auto-approved");
    expect(auto).toHaveTextContent("Auto-approved");
    expect(auto).toHaveTextContent("3");
    expect(auto).toHaveTextContent("75%");

    const humanRev = screen.getByTestId("stat-human-review");
    expect(humanRev).toHaveTextContent("Human review");
    expect(humanRev).toHaveTextContent("1");
    expect(humanRev).toHaveTextContent("25%");

    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("Context switches saved");
    expect(switches).toHaveTextContent("3");
  });

  // A2: an empty range (all zeros) renders 0 counts and 0% shares — no NaN, no
  // divide-by-zero leaking to the UI.
  it("renders all zeros for an empty range", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        auto_approved: { count: 0, share: 0, delta: { pct: 0, state: "none" }, series: [0] },
        human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" }, series: [0] },
        switches_saved: 0,
        switches_saved_series: [0],
        switches_saved_delta: { pct: 0, state: "none" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const auto = screen.getByTestId("stat-auto-approved");
    expect(auto).toHaveTextContent("0%");
    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("0");
  });
});

describe("AnalyticsPanel period deltas", () => {
  // A5: each headline stat carries an elapsed-aligned period delta rendered as a
  // signed percentage, and the row names the aligned comparison once (slice 3).
  it("renders signed deltas on each headline stat and the comparison label", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        auto_approved: { count: 3, share: 0.75, delta: { pct: 0.5, state: "changed" }, series: [1, 2] },
        human_review: { count: 1, share: 0.25, delta: { pct: -0.2, state: "changed" }, series: [1, 0] },
        switches_saved: 3,
        switches_saved_series: [1, 2],
        switches_saved_delta: { pct: 0.5, state: "changed" },
        delta_label: "vs preceding 1 day",
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    expect(screen.getByTestId("stat-auto-approved")).toHaveTextContent("+50%");
    expect(screen.getByTestId("stat-human-review")).toHaveTextContent("-20%");
    expect(screen.getByTestId("stat-switches-saved")).toHaveTextContent("+50%");
    // The aligned-comparison label appears once for the row, not per stat.
    expect(screen.getByText("vs preceding 1 day")).toBeInTheDocument();
  });

  // A6: the zero-baseline states render as words, never as ∞/NaN — "new" when the
  // prior period was empty but this one is not, "—" when both are empty.
  it("renders 'new' for a zero baseline and '—' for both-zero", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        auto_approved: { count: 5, share: 1, delta: { pct: 0, state: "new" }, series: [2, 3] },
        human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" }, series: [0, 0] },
        switches_saved: 5,
        switches_saved_series: [2, 3],
        switches_saved_delta: { pct: 0, state: "new" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    expect(screen.getByTestId("stat-auto-approved")).toHaveTextContent("new");
    expect(screen.getByTestId("stat-human-review")).toHaveTextContent("—");
  });
});

describe("AnalyticsPanel time picker", () => {
  // A3: the Variant-2 range control is a dropdown — the toggle opens a menu, and
  // picking a range re-fetches with it after the debounce, NOT before (a burst of
  // picks collapses to one fetch). The toggle also reflects the active range.
  it("opens the dropdown and refetches the picked range, debounced", async () => {
    mockAnalytics.mockResolvedValue(analytics());

    render(<AnalyticsPanel />);
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(1);
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything());
    // The toggle names the active range; the menu is closed until opened.
    const toggle = screen.getByTestId("range-toggle");
    expect(toggle).toHaveTextContent(/today/i);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole("menuitemradio", { name: /this week/i }));
    // Debounce window not yet elapsed -> still just the mount fetch.
    expect(mockAnalytics).toHaveBeenCalledTimes(1);
    // Picking closes the menu and updates the toggle label.
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(toggle).toHaveTextContent(/this week/i);

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(2);
    expect(mockAnalytics).toHaveBeenLastCalledWith("week", expect.anything());
  });

  // A4: choosing the custom "last X days" range from the menu reveals a day-count
  // input, and editing it re-fetches the days range with that count (the only
  // range that carries a day count on the wire).
  it("fetches the days range with the custom day count chosen from the menu", async () => {
    mockAnalytics.mockResolvedValue(analytics());

    render(<AnalyticsPanel />);
    await flush();

    fireEvent.click(screen.getByTestId("range-toggle"));
    fireEvent.click(screen.getByRole("menuitemradio", { name: /last .* days/i }));
    fireEvent.change(screen.getByLabelText(/day count/i), {
      target: { value: "14" },
    });

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    await flush();
    expect(mockAnalytics).toHaveBeenLastCalledWith("days", 14);
  });
});

describe("AnalyticsPanel sparklines", () => {
  // A9 (Variant 2): each headline tile draws a sparkline of its per-day series —
  // one bar per day — so the daily trend reads at a glance beside the count.
  it("renders one sparkline bar per day in each headline's series", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        auto_approved: {
          count: 4,
          share: 1,
          delta: { pct: 0, state: "none" },
          series: [1, 0, 1, 2],
        },
        switches_saved: 4,
        switches_saved_series: [1, 0, 1, 2],
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const spark = screen.getByTestId("sparkline-auto-approved");
    expect(spark.querySelectorAll(".spark-bar")).toHaveLength(4);
    // The switches tile draws its own series too.
    expect(
      screen.getByTestId("sparkline-switches-saved").querySelectorAll(".spark-bar"),
    ).toHaveLength(4);
  });
});

describe("AnalyticsPanel switches-saved time & money", () => {
  // A7: the switches-saved stat shows its two derived figures — time in hours and
  // money with the currency prefix — alongside the raw count (slice 4, ADR 0010).
  it("renders the count, hours, and currency-prefixed money", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        switches_saved: 3,
        switches_saved_hours: 1.2,
        switches_saved_money: 115,
        assumptions: { minutes_per_switch: 23, hourly_rate: 100, currency: "$" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("3");
    expect(switches).toHaveTextContent("1.2h"); // 1.15h rounded to one decimal
    expect(switches).toHaveTextContent("$115");
  });

  // A8: the money figure is a read-only pill that renders the assumptions behind
  // it ("× 23 min · $100/hr") — editing now lives in the Settings tab, so the
  // Analytics surface stays pure presentation (no edit control here).
  it("renders the money as a pill that surfaces the assumptions, read-only", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        switches_saved_money: 307,
        assumptions: { minutes_per_switch: 23, hourly_rate: 100, currency: "$" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const pill = screen.getByTestId("switches-money-pill");
    expect(pill).toHaveTextContent("$307");
    // The pill renders the assumptions that produced the figure.
    expect(pill).toHaveTextContent("23 min");
    expect(pill).toHaveTextContent("$100/hr");
    // No editing affordance on the Analytics tab — that moved to Settings.
    expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/minutes per switch/i)).not.toBeInTheDocument();
  });
});
