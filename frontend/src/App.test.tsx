import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { App } from "./App";
import type {
  Approval,
  CycleStatus,
  FunnelItem,
  Pipeline,
  QueueItem,
} from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    fetchStatus: vi.fn(),
    fetchApprovals: vi.fn(),
    fetchQueue: vi.fn(),
    fetchPipeline: vi.fn(),
    fetchRules: vi.fn(),
    fetchAnalytics: vi.fn(),
    fetchSettings: vi.fn(),
  };
});

import {
  fetchStatus,
  fetchApprovals,
  fetchQueue,
  fetchPipeline,
  fetchRules,
  fetchAnalytics,
  fetchSettings,
} from "./api";
const mockStatus = vi.mocked(fetchStatus);
const mockApprovals = vi.mocked(fetchApprovals);
const mockQueue = vi.mocked(fetchQueue);
const mockPipeline = vi.mocked(fetchPipeline);
const mockRules = vi.mocked(fetchRules);
const mockAnalytics = vi.mocked(fetchAnalytics);
const mockSettings = vi.mocked(fetchSettings);

const status = (approved: number): CycleStatus => ({
  last_run: "2026-06-18T10:00:00Z",
  outcome: "ok",
  approved_count: approved,
  queue_count: 0,
  dropped_count: 0,
  staging_count: 0,
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

const emptyPipeline: Pipeline = {
  incoming: 0,
  dropped_red: [],
  dropped_draft: [],
  staging: [],
  approved_elsewhere: [],
  needs_human_review: 0,
  approved_by_tm3k: 0,
  approved_this_cycle: 0,
  search: "",
};

const funnelItem = (n: number): FunnelItem => ({
  number: n,
  title: `feat: thing ${n}`,
  title_parts: { type: "feat", scopes: [], breaking: false, description: `thing ${n}` },
  author: "dana",
  url: `https://github.com/o/r/pull/${n}`,
  failing_checks: 0,
  additions: 0,
  deletions: 0,
  changed_files: 0,
});

beforeEach(() => {
  vi.useFakeTimers();
  mockStatus.mockReset();
  mockApprovals.mockReset();
  mockQueue.mockReset();
  mockPipeline.mockReset();
  mockRules.mockReset();
  mockAnalytics.mockReset();
  mockSettings.mockReset();
  mockQueue.mockResolvedValue([]);
  mockPipeline.mockResolvedValue(emptyPipeline);
  mockRules.mockResolvedValue([]);
  mockSettings.mockResolvedValue({ cost_low: 10, cost_high: 26, currency: "CHF" });
  mockAnalytics.mockResolvedValue({
    auto_approved: { count: 0, share: 0, delta: { pct: 0, state: "none" }, series: [0] },
    human_review: { count: 0, share: 0, delta: { pct: 0, state: "none" }, series: [0] },
    switches_saved: 0,
    switches_saved_series: [0],
    switches_saved_money_low: 0,
    switches_saved_money_high: 0,
    switches_saved_delta: { pct: 0, state: "none" },
    delta_label: "vs yesterday",
    assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
    by_type: [],
    scopes: [],
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

    expect(screen.getByText(/next sync/i)).toBeInTheDocument();
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
    expect(screen.getByRole("link", { name: /#2/ })).toBeInTheDocument();
  });

  // F-funnel-poll: the Incoming distribution bar reflects the polled /pipeline
  // snapshot and updates with the next cycle's counts.
  it("polls /pipeline and updates the Incoming bar on the next cycle", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockPipeline.mockResolvedValueOnce({
      ...emptyPipeline,
      incoming: 1,
      approved_by_tm3k: 1,
    });

    render(<App />);
    await flush();
    expect(mockPipeline).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("incoming-total")).toHaveTextContent("1");

    mockPipeline.mockResolvedValueOnce({
      ...emptyPipeline,
      incoming: 4,
      approved_by_tm3k: 4,
    });
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    await flush();

    expect(mockPipeline).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("incoming-total")).toHaveTextContent("4");
  });

  // Issue #12: the Incoming filter chip shows the live configured search supplied
  // by the /pipeline endpoint (its `search` field), not a frontend constant.
  it("shows the configured search from /pipeline on the Incoming filter chip", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockPipeline.mockResolvedValueOnce({
      ...emptyPipeline,
      incoming: 2,
      search: "is:open team-review-requested:o/team",
    });

    render(<App />);
    await flush();

    const chip = screen.getByTestId("filter-chip");
    expect(chip).toHaveTextContent("is:open team-review-requested:o/team");
  });

  // F-funnel-clear: a failed candidate fetch CLEARS the funnel — the prior
  // cycle's buckets do not linger; Incoming falls back to its loading state.
  it("clears the funnel when the candidate fetch fails", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockPipeline.mockResolvedValueOnce({
      ...emptyPipeline,
      incoming: 3,
      approved_by_tm3k: 3,
    });

    render(<App />);
    await flush();
    expect(screen.getByTestId("incoming-total")).toHaveTextContent("3");

    // Next poll fails — the funnel must not keep showing "3".
    mockPipeline.mockRejectedValueOnce(new Error("pipeline request failed: 500"));
    await act(async () => {
      vi.advanceTimersByTime(10_000);
    });
    await flush();

    expect(screen.queryByTestId("incoming-total")).not.toBeInTheDocument();
    expect(screen.getByText(/loading funnel/i)).toBeInTheDocument();
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
  // F-tab-default: with no hash, the Pipeline tab is active — its funnel (with
  // the Needs-Human-Review station) shows and the Rules panel does not. The
  // heartbeat strip and tab bar are always present.
  it("defaults to the Pipeline tab with the heartbeat strip and tab bar persistent", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([approval(1)]);
    mockQueue.mockResolvedValue([queueItem(41)]);

    render(<App />);
    await flush();

    // Heartbeat strip + tab bar are always rendered.
    expect(screen.getByText(/last cycle/i)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /pipeline/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /rules/i })).toBeInTheDocument();

    // Pipeline tab is selected; its funnel is visible, Rules' is not.
    expect(screen.getByRole("tab", { name: /pipeline/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
    expect(screen.queryByText("Approve Rules")).not.toBeInTheDocument();
  });

  // F-tab-review-redirect: an old #review bookmark redirects to #pipeline so
  // links survive the rename, landing on the Pipeline tab.
  it("redirects #review to the Pipeline tab", async () => {
    window.location.hash = "#review";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(window.location.hash).toBe("#pipeline");
    expect(screen.getByRole("tab", { name: /pipeline/i })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
  });

  // F-tab-switch: clicking the Rules tab shows the Rules panel and hides the
  // Pipeline funnel, and writes the hash.
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

  // F-tab-unknown-hash: an unknown hash falls back to the default Pipeline tab.
  it("falls back to Pipeline for an unknown hash", async () => {
    window.location.hash = "#bogus";
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockQueue.mockResolvedValue([]);

    render(<App />);
    await flush();

    expect(screen.getByRole("tab", { name: /pipeline/i })).toHaveAttribute(
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

  // F-tab-badge: the Pipeline tab carries a staging-count badge (the actionable
  // "uncovered PRs awaiting a rule" signal) that stays visible from the Rules
  // tab (the count is never hidden).
  it("shows the live staging-count badge on the Pipeline tab, visible from Rules", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockPipeline.mockResolvedValue({
      ...emptyPipeline,
      incoming: 2,
      staging: [funnelItem(70), funnelItem(71)],
    });

    render(<App />);
    await flush();

    const pipelineTab = screen.getByRole("tab", { name: /pipeline/i });
    expect(pipelineTab).toHaveTextContent("2");

    // Move to Rules — the badge on the Pipeline tab control stays visible.
    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /rules/i }));
    });
    await flush();

    expect(screen.getByRole("tab", { name: /pipeline/i })).toHaveTextContent(
      "2",
    );
  });

  // F-tab-badge-zero: with empty staging the Pipeline tab shows no count badge
  // (zero is presented as the absence of a badge).
  it("hides the staging-count badge when staging is empty", async () => {
    mockStatus.mockResolvedValue(status(0));
    mockApprovals.mockResolvedValue([]);
    mockPipeline.mockResolvedValue(emptyPipeline);

    render(<App />);
    await flush();

    expect(
      screen.queryByTestId("pipeline-tab-badge"),
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
    expect(screen.getByLabelText(/low estimate/i)).toBeInTheDocument();
  });
});
