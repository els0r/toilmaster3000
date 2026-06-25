import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { App } from "./App";
import type { Approval, CycleStatus, QueueItem } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    fetchStatus: vi.fn(),
    fetchApprovals: vi.fn(),
    fetchQueue: vi.fn(),
    fetchRules: vi.fn(),
    fetchAnalytics: vi.fn(),
    fetchSettings: vi.fn(),
  };
});

import {
  fetchStatus,
  fetchApprovals,
  fetchQueue,
  fetchRules,
  fetchAnalytics,
  fetchSettings,
} from "./api";
const mockStatus = vi.mocked(fetchStatus);
const mockApprovals = vi.mocked(fetchApprovals);
const mockQueue = vi.mocked(fetchQueue);
const mockRules = vi.mocked(fetchRules);
const mockAnalytics = vi.mocked(fetchAnalytics);
const mockSettings = vi.mocked(fetchSettings);

const status = (approved: number): CycleStatus => ({
  last_run: "2026-06-18T10:00:00Z",
  outcome: "ok",
  approved_count: approved,
  queue_count: 0,
  dropped_count: 0,
});

const approval = (n: number): Approval => ({
  number: n,
  title: `chore: thing ${n}`,
  title_parts: { type: "chore", scopes: [], breaking: false, description: `thing ${n}` },
  author: "alice",
  url: `https://github.com/o/r/pull/${n}`,
  matched_rule: "approve-all",
  manual: false,
  state: "unknown",
  approved_at: "2026-06-18T10:00:00Z",
});

const queueItem = (n: number): QueueItem => ({
  number: n,
  title: `chore!: breaking ${n}`,
  title_parts: { type: "chore", scopes: [], breaking: true, description: `breaking ${n}` },
  author: "bob",
  url: `https://github.com/o/r/pull/${n}`,
  additions: 40,
  deletions: 12,
  changed_files: 3,
  reasons: ["breaking_change"],
});

beforeEach(() => {
  vi.useFakeTimers();
  mockStatus.mockReset();
  mockApprovals.mockReset();
  mockQueue.mockReset();
  mockRules.mockReset();
  mockAnalytics.mockReset();
  mockSettings.mockReset();
  mockQueue.mockResolvedValue([]);
  mockRules.mockResolvedValue([]);
  mockSettings.mockResolvedValue({ minutes_per_switch: 23, hourly_rate: 100, currency: "$" });
  mockAnalytics.mockResolvedValue({
    auto_approved: { count: 0, share: 0, delta: { pct: 0, state: "none" } },
    human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" } },
    switches_saved: 0,
    switches_saved_hours: 0,
    switches_saved_money: 0,
    switches_saved_delta: { pct: 0, state: "none" },
    delta_label: "vs yesterday",
    assumptions: { minutes_per_switch: 23, hourly_rate: 100, currency: "$" },
  });
  // Each test starts from a clean hash so the default (Review) tab applies.
  window.location.hash = "";
});

afterEach(() => {
  vi.useRealTimers();
  window.location.hash = "";
});

// flush lets queued microtasks (the resolved fetch promises) settle inside the
// fake-timer world so React can apply the resulting state.
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("App polling", () => {
  // F4: the app fetches status + approvals on mount and renders both panels.
  it("renders the status line and feed from the initial fetch", async () => {
    mockStatus.mockResolvedValue(status(1));
    mockApprovals.mockResolvedValue([approval(1)]);

    render(<App />);
    await flush();

    expect(screen.getByText(/approved 1/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /#1/ })).toBeInTheDocument();
    expect(mockStatus).toHaveBeenCalledTimes(1);
    expect(mockApprovals).toHaveBeenCalledTimes(1);
  });

  // F-queue-app: the app fetches the queue on mount and renders the Needs Human
  // Review panel with the queued breaking PR.
  it("renders the needs-review queue from the initial fetch", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([queueItem(41)]);

    render(<App />);
    await flush();

    expect(mockQueue).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "approve #41" }),
    ).toBeInTheDocument();
    // The breaking PR shows the breaking badge (slice 1), not a raw chip.
    expect(screen.getByText("breaking change")).toBeInTheDocument();
  });

  // F5: the app re-polls both endpoints every 10s and the UI reflects the new
  // cycle's data.
  it("polls status + approvals every 10s and updates the UI", async () => {
    mockStatus.mockResolvedValueOnce(status(1));
    mockApprovals.mockResolvedValueOnce([approval(1)]);

    render(<App />);
    await flush();
    expect(mockStatus).toHaveBeenCalledTimes(1);
    expect(mockApprovals).toHaveBeenCalledTimes(1);

    // Next cycle's data on the second poll.
    mockStatus.mockResolvedValueOnce(status(2));
    mockApprovals.mockResolvedValueOnce([approval(2), approval(1)]);

    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    await flush();

    expect(mockStatus).toHaveBeenCalledTimes(2);
    expect(mockApprovals).toHaveBeenCalledTimes(2);
    expect(screen.getByText(/approved 2/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /#2/ })).toBeInTheDocument();
  });

  // F6: no extra polls fire before the 10s interval elapses.
  it("does not poll again before 10s elapse", async () => {
    mockStatus.mockResolvedValue(status(1));
    mockApprovals.mockResolvedValue([approval(1)]);

    render(<App />);
    await flush();

    await act(async () => {
      vi.advanceTimersByTime(9_000);
    });
    await flush();

    expect(mockStatus).toHaveBeenCalledTimes(1);
    expect(mockApprovals).toHaveBeenCalledTimes(1);
  });
});

describe("App tabbed shell", () => {
  // F-tab-default: with no hash, the Review tab is active — its panel (queue +
  // feed) shows and the Rules panel does not. The heartbeat strip and tab bar
  // are always present.
  it("defaults to the Review tab with the heartbeat strip and tab bar persistent", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([approval(1)]);
    mockQueue.mockResolvedValue([queueItem(41)]);

    render(<App />);
    await flush();

    // Heartbeat strip + tab bar are always rendered.
    expect(screen.getByText(/last cycle/i)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /review/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /rules/i })).toBeInTheDocument();

    // Review tab is selected; its panel is visible, Rules' is not.
    expect(screen.getByRole("tab", { name: /review/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
    expect(screen.queryByText("Approve Rules")).not.toBeInTheDocument();
  });

  // F-tab-switch: clicking the Rules tab shows the Rules panel and hides the
  // Review panel, and writes the hash.
  it("switches to the Rules tab on click and updates the hash", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /rules/i }));
    });
    await flush();

    expect(window.location.hash).toBe("#rules");
    expect(screen.getByRole("tab", { name: /rules/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Approve Rules")).toBeInTheDocument();
    expect(screen.queryByText("Needs Human Review")).not.toBeInTheDocument();
  });

  // F-tab-initial-hash: loading with #rules opens the Rules tab directly (the
  // tab is linkable / reload-stable).
  it("opens the Rules tab when loaded with #rules", async () => {
    window.location.hash = "#rules";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(screen.getByRole("tab", { name: /rules/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Approve Rules")).toBeInTheDocument();
  });

  // F-tab-unknown-hash: an unknown hash falls back to the default Review tab.
  it("falls back to Review for an unknown hash", async () => {
    window.location.hash = "#bogus";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(screen.getByRole("tab", { name: /review/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
  });

  // F-tab-hashchange: an external hash change (back/forward, a link) re-syncs
  // the active tab without a router.
  it("follows an external hashchange", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();

    await act(async () => {
      window.location.hash = "#rules";
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    await flush();

    expect(screen.getByText("Approve Rules")).toBeInTheDocument();
  });

  // F-tab-badge: the Review tab carries a queue-count badge that stays visible
  // from the Rules tab (the actionable count is never hidden).
  it("shows the live queue-count badge on the Review tab, visible from Rules", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([queueItem(41), queueItem(42)]);

    render(<App />);
    await flush();

    const reviewTab = screen.getByRole("tab", { name: /review/i });
    expect(reviewTab).toHaveTextContent("2");

    // Move to Rules — the badge on the Review tab control stays visible.
    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /rules/i }));
    });
    await flush();

    expect(screen.getByRole("tab", { name: /review/i })).toHaveTextContent("2");
  });

  // F-tab-badge-zero: with an empty queue the Review tab shows no count badge
  // (zero is presented as the absence of a badge).
  it("hides the queue-count badge when the queue is empty", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(
      screen.queryByTestId("review-tab-badge"),
    ).not.toBeInTheDocument();
  });

  // F-tab-analytics: the third Analytics tab exists; clicking it shows the
  // Analytics panel (its stats row), hides the Review panel, writes #analytics,
  // and fetches the analytics endpoint (off the 10s poll).
  it("switches to the Analytics tab on click and updates the hash", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /analytics/i }));
    });
    await flush();

    expect(window.location.hash).toBe("#analytics");
    expect(screen.getByRole("tab", { name: /analytics/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByTestId("stat-auto-approved")).toBeInTheDocument();
    expect(screen.queryByText("Needs Human Review")).not.toBeInTheDocument();
    expect(mockAnalytics).toHaveBeenCalled();
  });

  // F-tab-analytics-hash: loading with #analytics opens the Analytics tab
  // directly — the tab is linkable / reload-stable, like the others.
  it("opens the Analytics tab when loaded with #analytics", async () => {
    window.location.hash = "#analytics";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(screen.getByRole("tab", { name: /analytics/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByTestId("stat-auto-approved")).toBeInTheDocument();
  });

  // F-tab-settings: the Settings tab exists; clicking it shows the settings
  // editor (the currency field among them), hides the Review panel, writes
  // #settings, and loads the persisted constants.
  it("switches to the Settings tab on click and updates the hash", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /settings/i }));
    });
    await flush();

    expect(window.location.hash).toBe("#settings");
    expect(screen.getByRole("tab", { name: /settings/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByLabelText(/currency/i)).toBeInTheDocument();
    expect(screen.queryByText("Needs Human Review")).not.toBeInTheDocument();
    expect(mockSettings).toHaveBeenCalled();
  });

  // F-tab-settings-hash: loading with #settings opens the Settings tab directly —
  // linkable / reload-stable like the others.
  it("opens the Settings tab when loaded with #settings", async () => {
    window.location.hash = "#settings";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(screen.getByRole("tab", { name: /settings/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByLabelText(/minutes per switch/i)).toBeInTheDocument();
  });
});
