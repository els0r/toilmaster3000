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
  auto_approved: { count: 3, share: 0.75, delta: { pct: 0, state: "none" } },
  human_review: { count: 1, share: 0.25, delta: { pct: 0, state: "none" } },
  switches_saved: 3,
  switches_saved_delta: { pct: 0, state: "none" },
  delta_label: "vs yesterday",
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
        auto_approved: { count: 0, share: 0, delta: { pct: 0, state: "none" } },
        human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" } },
        switches_saved: 0,
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
        auto_approved: { count: 3, share: 0.75, delta: { pct: 0.5, state: "changed" } },
        human_review: { count: 1, share: 0.25, delta: { pct: -0.2, state: "changed" } },
        switches_saved: 3,
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
        auto_approved: { count: 5, share: 1, delta: { pct: 0, state: "new" } },
        human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" } },
        switches_saved: 5,
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
  // A3: the panel fetches the default range (today) on mount, then re-fetches with
  // the newly-selected range after the debounce — and NOT before it (a debounced
  // control: a burst of clicks collapses to one fetch).
  it("refetches with the selected range, debounced", async () => {
    mockAnalytics.mockResolvedValue(analytics());

    render(<AnalyticsPanel />);
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(1);
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything());

    fireEvent.click(screen.getByRole("button", { name: /this week/i }));
    // Debounce window not yet elapsed -> still just the mount fetch.
    expect(mockAnalytics).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(2);
    expect(mockAnalytics).toHaveBeenLastCalledWith("week", expect.anything());
  });

  // A4: selecting the custom "last X days" range reveals a day-count input, and
  // editing it re-fetches the days range with that count (the only range that
  // carries a day count on the wire).
  it("fetches the days range with the custom day count", async () => {
    mockAnalytics.mockResolvedValue(analytics());

    render(<AnalyticsPanel />);
    await flush();

    fireEvent.click(screen.getByRole("button", { name: /last .* days/i }));
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
