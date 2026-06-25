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
  switches_saved_money_low: 30,
  switches_saved_money_high: 78,
  switches_saved_delta: { pct: 0, state: "none" },
  delta_label: "vs yesterday",
  assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
  by_type: cohort(),
  scopes: [],
  ...over,
});

// cohort builds the fixed By-Type axis (every Conventional Commits type plus a
// trailing "other"), zeros by default, so a test can override just the rows it
// cares about while the renderer always sees the full, stable set of rows.
const COHORT_TYPES = [
  "feat", "fix", "chore", "docs", "style", "refactor",
  "perf", "test", "build", "ci", "revert", "other",
];
function cohort(
  over: Record<
    string,
    Partial<{
      count: number;
      share: number;
      auto: number;
      human: number;
      money_low: number;
      money_high: number;
    }>
  > = {},
): Analytics["by_type"] {
  return COHORT_TYPES.map((type) => ({
    type,
    count: 0,
    share: 0,
    auto: 0,
    human: 0,
    money_low: 0,
    money_high: 0,
    ...over[type],
  }));
}

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
  // and Switching costs saved as the green money range (the count is not repeated
  // here — it already leads the Auto-approved tile).
  it("renders auto/human counts + shares and switching costs from the fetch", async () => {
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
    expect(switches).toHaveTextContent("Switching costs saved");
    expect(switches).toHaveTextContent("CHF30");
    expect(switches).toHaveTextContent("CHF78");
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
        switches_saved_money_low: 0,
        switches_saved_money_high: 0,
        switches_saved_delta: { pct: 0, state: "none" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const auto = screen.getByTestId("stat-auto-approved");
    expect(auto).toHaveTextContent("0%");
    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("CHF0");
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
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything(), []);
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
    expect(mockAnalytics).toHaveBeenLastCalledWith("week", expect.anything(), []);
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
    expect(mockAnalytics).toHaveBeenLastCalledWith("days", 14, []);
  });
});

describe("AnalyticsPanel scope filter", () => {
  // A11 (slice 6): the scope filter is a searchable multi-select populated from the
  // all-time scope list on the response. The default is All scopes (no scope on the
  // fetch); picking a scope re-fetches the WHOLE view filtered to it (debounced),
  // and the toggle reflects the active selection.
  it("filters the whole view by a scope picked from the all-time list, debounced", async () => {
    mockAnalytics.mockResolvedValue(analytics({ scopes: ["api", "ci", "web"] }));

    render(<AnalyticsPanel />);
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(1);
    // Default is All scopes — no scope rides the fetch.
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything(), []);
    const toggle = screen.getByTestId("scope-toggle");
    expect(toggle).toHaveTextContent(/all scopes/i);

    fireEvent.click(toggle);
    // The menu lists every all-time scope as a checkable option.
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /api/i }));
    // Debounce window not yet elapsed -> still just the mount fetch.
    expect(mockAnalytics).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    await flush();
    expect(mockAnalytics).toHaveBeenCalledTimes(2);
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything(), ["api"]);
    // The toggle names the active selection rather than "All scopes".
    expect(toggle).toHaveTextContent(/api/i);
  });

  // A11 cont.: the control is searchable (the real log holds 45+ scopes) and OR —
  // a second pick adds to the selection, and the search box narrows the options.
  it("searches the list and ORs multiple scopes into the fetch", async () => {
    mockAnalytics.mockResolvedValue(analytics({ scopes: ["api", "ci", "web"] }));

    render(<AnalyticsPanel />);
    await flush();

    fireEvent.click(screen.getByTestId("scope-toggle"));
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /api/i }));
    // The search box narrows the option list: "w" leaves only "web".
    fireEvent.change(screen.getByPlaceholderText(/search scopes/i), {
      target: { value: "w" },
    });
    expect(screen.queryByRole("menuitemcheckbox", { name: /^ci$/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /web/i }));

    await act(async () => {
      vi.advanceTimersByTime(500);
    });
    await flush();
    expect(mockAnalytics).toHaveBeenLastCalledWith("today", expect.anything(), ["api", "web"]);
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

describe("AnalyticsPanel by-type cohort", () => {
  // A10 (slice 5): the cohort renders every standard conventional-commit type plus
  // a trailing "other", sorted by count descending (alphabetical tie-break) so the
  // heaviest types lead and zero-count rows group at the bottom, each row carrying
  // its count, its share of the range total as a percent, and the auto/human split
  // — the signal of which types still pull a human in.
  it("renders every standard type sorted by count with count, share, and the auto/human split", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        by_type: cohort({
          fix: { count: 7, share: 0.7, auto: 5, human: 2 },
          feat: { count: 3, share: 0.3, auto: 2, human: 1 },
        }),
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    // All twelve rows (11 standard + other) render: the two non-zero types lead by
    // count, then the ten zero-count rows in alphabetical order.
    const rows = screen.getAllByTestId(/^cohort-row-/);
    expect(rows.map((r) => r.getAttribute("data-testid"))).toEqual([
      "cohort-row-fix", "cohort-row-feat", "cohort-row-build", "cohort-row-chore",
      "cohort-row-ci", "cohort-row-docs", "cohort-row-other", "cohort-row-perf",
      "cohort-row-refactor", "cohort-row-revert", "cohort-row-style", "cohort-row-test",
    ]);

    const fix = screen.getByTestId("cohort-row-fix");
    expect(fix).toHaveTextContent("fix");
    expect(fix).toHaveTextContent("7");
    expect(fix).toHaveTextContent("70%");
    expect(fix).toHaveTextContent("5 auto / 2 human");
  });

  // A10b: each row carries its saved-switch money range, placed left of the count.
  // The range is the bucket's auto-priced band (server-computed); a row whose money
  // band has a real spread renders "low – high".
  it("renders each row's money range left of the count", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        by_type: cohort({
          feat: { count: 3, share: 1, auto: 2, human: 1, money_low: 20, money_high: 52 },
        }),
        assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const feat = screen.getByTestId("cohort-row-feat");
    expect(feat).toHaveTextContent("CHF20");
    expect(feat).toHaveTextContent("CHF52");
    // Money sits left of the count: its position in the row precedes the count cell.
    const money = feat.querySelector(".cohort-money");
    const count = feat.querySelector(".cohort-count");
    expect(money).not.toBeNull();
    expect(count).not.toBeNull();
    expect(money!.compareDocumentPosition(count!)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
  });

  // A10c: a row with no auto approvals prices nothing, so its band collapses to a
  // single CHF0 — never "CHF0 – CHF0".
  it("collapses a zero-savings row's money to a single figure", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        by_type: cohort({
          // all-human: real activity, zero savings.
          docs: { count: 2, share: 1, auto: 0, human: 2, money_low: 0, money_high: 0 },
        }),
        assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const docs = screen.getByTestId("cohort-row-docs");
    const money = docs.querySelector(".cohort-money");
    expect(money).not.toBeNull();
    expect(money!.textContent).toBe("CHF0");
  });

  // A11 (slice 5): a type with no approvals in the range is still shown, dimmed,
  // honoring "the set is bounded — show every row even at zero".
  it("dims a zero-count row rather than hiding it", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({ by_type: cohort({ feat: { count: 2, share: 1, auto: 2, human: 0 } }) }),
    );

    render(<AnalyticsPanel />);
    await flush();

    // docs has no approvals: present but dimmed.
    const docs = screen.getByTestId("cohort-row-docs");
    expect(docs).toHaveTextContent("docs");
    expect(docs.className).toMatch(/is-zero/);
    // feat has approvals, so it is not dimmed.
    expect(screen.getByTestId("cohort-row-feat").className).not.toMatch(/is-zero/);
  });
});

describe("AnalyticsPanel switches-saved money range", () => {
  // A7: the switches-saved stat leads with the count-scaled money range in the
  // headline (no raw count — that's the Auto-approved tile's job); there is no hours
  // figure anymore — money no longer flows through hours × rate (ADR 0012).
  it("renders the currency-prefixed money range as the headline", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        switches_saved: 57,
        switches_saved_money_low: 570,
        switches_saved_money_high: 1486,
        assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("CHF570");
    expect(switches).toHaveTextContent("CHF1486");
    // No hours figure survives the move to a money-only headline.
    expect(switches).not.toHaveTextContent(/\dh\b/);
  });

  // A8: the per-switch band renders as a read-only basis caption ("CHF10–26 /
  // switch") under the headline — the honest disclosure behind the range, not the
  // amount itself. Editing lives in the Settings tab, so the Analytics surface stays
  // pure presentation (no edit control here).
  it("renders the per-switch basis as a read-only caption", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        switches_saved_money_low: 570,
        switches_saved_money_high: 1486,
        assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const basis = screen.getByTestId("switches-basis");
    // The basis renders the per-switch band that produced the range.
    expect(basis).toHaveTextContent("CHF10");
    expect(basis).toHaveTextContent("26");
    expect(basis).toHaveTextContent("switch");
    // No editing affordance on the Analytics tab — that moved to Settings.
    expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/cost/i)).not.toBeInTheDocument();
  });

  // A9: an empty range (count 0) collapses the low==high==0 band to a single CHF0
  // headline, never a backwards-looking "CHF0 – CHF0".
  it("collapses an empty range to a single figure", async () => {
    mockAnalytics.mockResolvedValue(
      analytics({
        switches_saved: 0,
        switches_saved_money_low: 0,
        switches_saved_money_high: 0,
        assumptions: { cost_low: 10, cost_high: 26, currency: "CHF" },
      }),
    );

    render(<AnalyticsPanel />);
    await flush();

    const switches = screen.getByTestId("stat-switches-saved");
    expect(switches).toHaveTextContent("CHF0");
    expect(switches.textContent).not.toMatch(/CHF0\s*[–-]\s*CHF0/);
  });
});
