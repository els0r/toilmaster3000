import { describe, it, expect } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { PipelineFunnel } from "./Pipeline";
import type { Approval, FunnelItem, Pipeline, QueueItem } from "./api";

const funnelItem = (over: Partial<FunnelItem> = {}): FunnelItem => ({
  number: 100,
  title: "feat: a thing",
  title_parts: { type: "feat", scopes: [], breaking: false, description: "a thing" },
  author: "dana",
  url: "https://github.com/o/r/pull/100",
  failing_checks: 0,
  ...over,
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

const pipeline = (over: Partial<Pipeline> = {}): Pipeline => ({
  incoming: 0,
  dropped_red: [],
  dropped_draft: [],
  staging: [],
  approved_elsewhere: [],
  needs_human_review: 0,
  approved_by_tm3k: 0,
  approved_this_cycle: 0,
  ...over,
});

const emptyQueue: QueueItem[] = [];
const emptyApprovals: Approval[] = [];

function renderFunnel(over: Partial<Pipeline> = {}, props: Partial<Parameters<typeof PipelineFunnel>[0]> = {}) {
  return render(
    <PipelineFunnel
      pipeline={pipeline(over)}
      queue={emptyQueue}
      approvals={emptyApprovals}
      {...props}
    />,
  );
}

describe("Pipeline funnel — Incoming", () => {
  // The bar is a distribution summary (counts + legend), NOT a PR list: the
  // itemized PRs live in their terminal stations below.
  it("renders Incoming as a distribution bar with the total, not a PR list", () => {
    renderFunnel({
      incoming: 6,
      dropped_red: [funnelItem({ number: 1 })],
      dropped_draft: [funnelItem({ number: 2 })],
      staging: [funnelItem({ number: 3 })],
      needs_human_review: 1,
      approved_by_tm3k: 1,
      approved_elsewhere: [funnelItem({ number: 4 })],
    });

    expect(screen.getByText("Incoming")).toBeInTheDocument();
    expect(screen.getByTestId("incoming-total")).toHaveTextContent("6");
    // It is a bar (an img-role region), not a list of the incoming PRs.
    expect(
      screen.getByRole("img", { name: /incoming distribution/i }),
    ).toBeInTheDocument();
  });

  // The legend lists every one of the six terminal stages with its EXACT count,
  // even the zero ones — the partition is shown in full.
  it("shows a legend with each stage's exact count", () => {
    renderFunnel({
      incoming: 9,
      dropped_red: [funnelItem({ number: 1 }), funnelItem({ number: 2 })],
      dropped_draft: [funnelItem({ number: 3 })],
      staging: [funnelItem({ number: 4 }), funnelItem({ number: 5 }), funnelItem({ number: 6 })],
      needs_human_review: 1,
      approved_by_tm3k: 1,
      approved_elsewhere: [funnelItem({ number: 7 })],
    });

    expect(within(screen.getByTestId("legend-red")).getByText("2")).toBeInTheDocument();
    expect(within(screen.getByTestId("legend-draft")).getByText("1")).toBeInTheDocument();
    expect(within(screen.getByTestId("legend-staging")).getByText("3")).toBeInTheDocument();
    expect(within(screen.getByTestId("legend-human-review")).getByText("1")).toBeInTheDocument();
    expect(within(screen.getByTestId("legend-approved-tm3k")).getByText("1")).toBeInTheDocument();
    expect(within(screen.getByTestId("legend-approved-elsewhere")).getByText("1")).toBeInTheDocument();
  });

  // The configured filter expression rides on Incoming as a code chip so the
  // operator can confirm which search produced the set.
  it("shows the configured filter expression as a code chip", () => {
    renderFunnel({ incoming: 0 }, { filterExpr: "is:open draft:false" });

    const chip = screen.getByTestId("filter-chip");
    expect(chip.tagName).toBe("CODE");
    expect(chip).toHaveTextContent("is:open draft:false");
  });
});

describe("Pipeline funnel — Dropped", () => {
  // Two side-by-side cards: pipeline-red and draft. A red row shows its
  // failing-check count; the row carries the parsed title and a GitHub link.
  it("renders a pipeline-red card whose rows show the failing-check count", () => {
    renderFunnel({
      incoming: 1,
      dropped_red: [
        funnelItem({
          number: 51,
          title: "fix: flaky build",
          title_parts: { type: "fix", scopes: [], breaking: false, description: "flaky build" },
          author: "erin",
          url: "https://github.com/o/r/pull/51",
          failing_checks: 3,
        }),
      ],
    });

    const card = screen.getByTestId("dropped-red");
    expect(within(card).getByText(/pipeline red/i)).toBeInTheDocument();
    // The failing-check count is on the row.
    expect(within(card).getByText(/3 failing/i)).toBeInTheDocument();
    // Parsed title + GitHub link.
    expect(within(card).getByText("Flaky build")).toBeInTheDocument();
    const link = within(card).getByRole("link", { name: /#51/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/51");
  });

  // The draft card's rows show title, number, author, and a GitHub link.
  it("renders a draft card whose rows show title, number, author, and link", () => {
    renderFunnel({
      incoming: 1,
      dropped_draft: [
        funnelItem({
          number: 52,
          title: "feat: wip dashboard",
          title_parts: { type: "feat", scopes: [], breaking: false, description: "wip dashboard" },
          author: "frank",
          url: "https://github.com/o/r/pull/52",
        }),
      ],
    });

    const card = screen.getByTestId("dropped-draft");
    expect(within(card).getByText(/draft/i)).toBeInTheDocument();
    expect(within(card).getByText("Wip dashboard")).toBeInTheDocument();
    expect(within(card).getByText("frank", { exact: false })).toBeInTheDocument();
    const link = within(card).getByRole("link", { name: /#52/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/52");
    // Draft is a removed-by-gate row, not a failing-checks row.
    expect(within(card).queryByText(/failing/i)).not.toBeInTheDocument();
  });

  // Each Dropped sub-card shows its own empty state when its gate removed nothing.
  it("renders an empty state for an empty Dropped sub-queue", () => {
    renderFunnel({ incoming: 0 });

    expect(
      within(screen.getByTestId("dropped-red")).getByText(/none/i),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("dropped-draft")).getByText(/none/i),
    ).toBeInTheDocument();
  });
});

describe("Pipeline funnel — stations 4 & 5 reuse + approved-elsewhere", () => {
  // Station 4 reuses the existing Needs-Human-Review queue panel: its Approve
  // button and the breaking badge render unchanged from a /queue item.
  it("reuses the Needs Human Review queue panel unchanged", () => {
    const q: QueueItem = {
      number: 80,
      title: "chore!: drop flag",
      title_parts: { type: "chore", scopes: [], breaking: true, description: "drop flag" },
      author: "bob",
      url: "https://github.com/o/r/pull/80",
      additions: 1,
      deletions: 1,
      changed_files: 1,
      reasons: ["breaking_change"],
    };
    render(
      <PipelineFunnel
        pipeline={pipeline()}
        queue={[q]}
        approvals={emptyApprovals}
      />,
    );

    expect(screen.getByText("Needs Human Review")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "approve #80" }),
    ).toBeInTheDocument();
    expect(screen.getByText("breaking change")).toBeInTheDocument();
  });

  // Station 5 reuses the existing Approval feed panel unchanged.
  it("reuses the Approval feed panel unchanged", () => {
    render(
      <PipelineFunnel
        pipeline={pipeline()}
        queue={emptyQueue}
        approvals={[approval(90)]}
      />,
    );

    expect(screen.getByText("Approval feed")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /#90/ })).toBeInTheDocument();
  });

  // Approved-elsewhere PRs render as highlighted rows ABOVE the ledger (a section
  // distinct from the feed) — they are PRs tm3k deliberately left alone, never
  // entering the robot's own ledger.
  it("renders approved-elsewhere as highlighted rows above the ledger", () => {
    render(
      <PipelineFunnel
        pipeline={pipeline({
          incoming: 1,
          approved_elsewhere: [
            funnelItem({
              number: 91,
              title: "fix: patch from a teammate",
              title_parts: { type: "fix", scopes: [], breaking: false, description: "patch from a teammate" },
              author: "grace",
              url: "https://github.com/o/r/pull/91",
            }),
          ],
        })}
        queue={emptyQueue}
        approvals={[approval(90)]}
      />,
    );

    const section = screen.getByTestId("approved-elsewhere");
    expect(within(section).getByText(/approved elsewhere/i)).toBeInTheDocument();
    const link = within(section).getByRole("link", { name: /#91/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/91");
    // The approved-elsewhere PR never enters the ledger feed itself.
    expect(
      within(screen.getByText("Approval feed").closest("section") as HTMLElement)
        .queryByRole("link", { name: /#91/ }),
    ).not.toBeInTheDocument();
  });

  // With no approved-elsewhere PRs the highlighted section is absent entirely —
  // no empty placeholder cluttering the ledger.
  it("omits the approved-elsewhere section when there are none", () => {
    render(
      <PipelineFunnel
        pipeline={pipeline()}
        queue={emptyQueue}
        approvals={emptyApprovals}
      />,
    );

    expect(screen.queryByTestId("approved-elsewhere")).not.toBeInTheDocument();
  });
});

describe("Pipeline funnel — Staging placeholder & loading", () => {
  // Station 3 (interactive Staging) ships in the next change; a clearly-marked
  // placeholder mount point reserves its spot in the funnel.
  it("renders a Staging placeholder mount point", () => {
    renderFunnel({ incoming: 0 });
    expect(screen.getByTestId("staging-placeholder")).toBeInTheDocument();
  });

  // A null snapshot (first load or a cleared failed fetch) shows the funnel
  // loading state and none of the snapshot-derived stations.
  it("shows a loading state and no buckets when the snapshot is null", () => {
    render(
      <PipelineFunnel
        pipeline={null}
        queue={emptyQueue}
        approvals={emptyApprovals}
      />,
    );

    expect(screen.getByText(/loading funnel/i)).toBeInTheDocument();
    expect(screen.queryByTestId("incoming-total")).not.toBeInTheDocument();
    expect(screen.queryByTestId("dropped-red")).not.toBeInTheDocument();
    expect(screen.queryByTestId("staging-placeholder")).not.toBeInTheDocument();
  });
});
