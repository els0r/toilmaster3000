import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  within,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import { OutboundFunnel } from "./Outbound";
import type { MergeRecord, Outbound, OutboundItem } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    armOutbound: vi.fn(),
    disarmOutbound: vi.fn(),
    fetchDiff: vi.fn(),
  };
});

import { armOutbound, disarmOutbound, fetchDiff } from "./api";
const mockArm = vi.mocked(armOutbound);
const mockDisarm = vi.mocked(disarmOutbound);
const mockFetchDiff = vi.mocked(fetchDiff);

beforeEach(() => {
  mockArm.mockReset();
  mockArm.mockResolvedValue();
  mockDisarm.mockReset();
  mockDisarm.mockResolvedValue();
  mockFetchDiff.mockReset();
  mockFetchDiff.mockResolvedValue({
    files: [
      {
        filename: "web.go",
        status: "modified",
        additions: 8,
        deletions: 2,
        patch: "@@ -1 +1 @@\n+new\n-old",
      },
    ],
    total_files: 1,
  });
});

const outboundItem = (over: Partial<OutboundItem> = {}): OutboundItem => ({
  number: 200,
  title: "feat: an authored thing",
  title_parts: {
    type: "feat",
    scopes: [],
    breaking: false,
    description: "an authored thing",
  },
  author: "lennart",
  url: "https://github.com/o/r/pull/200",
  additions: 10,
  deletions: 2,
  changed_files: 1,
  conflict: false,
  armed: false,
  ...over,
});

const outbound = (over: Partial<Outbound> = {}): Outbound => {
  const base: Outbound = {
    outgoing: 0,
    draft: [],
    red: [],
    running: [],
    changes_requested: [],
    awaiting_approval: [],
    ready: [],
    distribution: {
      draft: 0,
      red: 0,
      running: 0,
      changes_requested: 0,
      awaiting_approval: 0,
      ready: 0,
    },
    search: "is:open author:@me",
    ...over,
  };
  // Keep the distribution honest with the itemized lists unless a test
  // overrides it explicitly — the counts are the list lengths by construction.
  if (!over.distribution) {
    base.distribution = {
      draft: base.draft?.length ?? 0,
      red: base.red?.length ?? 0,
      running: base.running?.length ?? 0,
      changes_requested: base.changes_requested?.length ?? 0,
      awaiting_approval: base.awaiting_approval?.length ?? 0,
      ready: base.ready?.length ?? 0,
    };
  }
  return base;
};

describe("Outbound funnel — Outgoing", () => {
  // The Outgoing station summarizes the raw authored pull as a distribution bar
  // (counts + legend), NOT a PR list — parallel to Inbound's Incoming station.
  it("renders Outgoing as a distribution bar with the total and per-stage legend counts", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 8,
          draft: [outboundItem({ number: 1 })],
          red: [outboundItem({ number: 2 }), outboundItem({ number: 3 })],
          running: [outboundItem({ number: 4 })],
          changes_requested: [outboundItem({ number: 5 })],
          awaiting_approval: [outboundItem({ number: 6 })],
          ready: [outboundItem({ number: 7 }), outboundItem({ number: 8 })],
        })}
      />,
    );

    expect(screen.getByText("Outgoing")).toBeInTheDocument();
    expect(screen.getByTestId("outgoing-total")).toHaveTextContent("8");
    expect(
      screen.getByRole("img", { name: /outgoing distribution/i }),
    ).toBeInTheDocument();

    expect(
      within(screen.getByTestId("legend-draft")).getByText("1"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("legend-red")).getByText("2"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("legend-running")).getByText("1"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("legend-changes-requested")).getByText("1"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("legend-awaiting")).getByText("1"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("legend-ready")).getByText("2"),
    ).toBeInTheDocument();
  });

  // The derived authored search rides the snapshot's `search` field and shows as
  // a code chip so the operator can confirm which search produced the set.
  it("shows the snapshot's authored search as a code chip", () => {
    render(<OutboundFunnel outbound={outbound()} />);

    const chip = screen.getByTestId("filter-chip");
    expect(chip.tagName).toBe("CODE");
    expect(chip).toHaveTextContent("is:open author:@me");
  });
});

describe("Outbound funnel — itemized stations", () => {
  // Every stage list renders through the shared PrRow: parsed title, author,
  // diff magnitude, and a GitHub link on each row. Draft is the first itemized
  // station (an outbound stage, not a gate — finish it).
  it("renders a draft station whose rows show title, author, diff magnitude, and link", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          draft: [
            outboundItem({
              number: 210,
              title: "feat(ui): wip outbound tab",
              title_parts: {
                type: "feat",
                scopes: ["ui"],
                breaking: false,
                description: "wip outbound tab",
              },
              author: "lennart",
              url: "https://github.com/o/r/pull/210",
              additions: 120,
              deletions: 8,
              changed_files: 5,
            }),
          ],
        })}
      />,
    );

    const card = screen.getByTestId("outbound-draft");
    expect(within(card).getByText("Draft")).toBeInTheDocument();
    expect(within(card).getByText("Wip outbound tab")).toBeInTheDocument();
    expect(
      within(card).getByText("lennart", { exact: false }),
    ).toBeInTheDocument();
    expect(within(card).getByText("+120")).toBeInTheDocument();
    expect(within(card).getByText("−8")).toBeInTheDocument();
    expect(within(card).getByText(/5 files/)).toBeInTheDocument();
    const link = within(card).getByRole("link", { name: /#210/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/210");
  });

  // "Not green" splits into two side-by-side sub-cards — pipeline red (go fix
  // CI) and checks running (wait) — an author must distinguish the two.
  it("renders pipeline-red and checks-running as side-by-side sub-cards", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 2,
          red: [outboundItem({ number: 220 })],
          running: [outboundItem({ number: 221 })],
        })}
      />,
    );

    const red = screen.getByTestId("outbound-red");
    const running = screen.getByTestId("outbound-running");
    expect(within(red).getByText(/pipeline red/i)).toBeInTheDocument();
    expect(within(red).getByRole("link", { name: /#220/ })).toBeInTheDocument();
    expect(within(running).getByText(/checks running/i)).toBeInTheDocument();
    expect(
      within(running).getByRole("link", { name: /#221/ }),
    ).toBeInTheDocument();
    // The two sub-cards share one side-by-side pair container.
    expect(red.parentElement).toBe(running.parentElement);
    expect(red.parentElement).toHaveClass("station-pair");
  });

  // Changes Requested, Awaiting Approval, and Ready are distinct stations, each
  // itemizing its rows with a GitHub link.
  it("renders Changes Requested, Awaiting Approval, and Ready as distinct stations", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 3,
          changes_requested: [outboundItem({ number: 230 })],
          awaiting_approval: [outboundItem({ number: 231 })],
          ready: [outboundItem({ number: 232 })],
        })}
      />,
    );

    const changes = screen.getByTestId("outbound-changes-requested");
    expect(within(changes).getByText("Changes Requested")).toBeInTheDocument();
    expect(
      within(changes).getByRole("link", { name: /#230/ }),
    ).toBeInTheDocument();

    const awaiting = screen.getByTestId("outbound-awaiting");
    expect(within(awaiting).getByText("Awaiting Approval")).toBeInTheDocument();
    expect(
      within(awaiting).getByRole("link", { name: /#231/ }),
    ).toBeInTheDocument();

    const ready = screen.getByTestId("outbound-ready");
    expect(within(ready).getByText("Ready")).toBeInTheDocument();
    expect(
      within(ready).getByRole("link", { name: /#232/ }),
    ).toBeInTheDocument();
  });
});

describe("Outbound funnel — row markers", () => {
  // A conflicted Ready row stays in Ready (the stage partition is total) but
  // carries a visible conflict marker — it never auto-merges until resolved,
  // and fixing the conflict is on you, which is exactly what Ready means.
  it("shows a conflict marker on a conflicted Ready row and none on a mergeable one", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 2,
          ready: [
            outboundItem({ number: 240, conflict: true }),
            outboundItem({ number: 241, conflict: false }),
          ],
        })}
      />,
    );

    const ready = screen.getByTestId("outbound-ready");
    const markers = within(ready).getAllByText(/merge conflict/i);
    expect(markers).toHaveLength(1);
  });

  // A breaking (`!`) PR shows the breaking badge on its row — arm with open
  // eyes; the badge is the standing display fact, same as the inbound queue.
  it("shows the breaking badge on a breaking row", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 2,
          awaiting_approval: [
            outboundItem({
              number: 250,
              title: "feat!: drop the old wire",
              title_parts: {
                type: "feat",
                scopes: [],
                breaking: true,
                description: "drop the old wire",
              },
            }),
            outboundItem({ number: 251 }),
          ],
        })}
      />,
    );

    const awaiting = screen.getByTestId("outbound-awaiting");
    expect(within(awaiting).getAllByText("breaking change")).toHaveLength(1);
  });
});

describe("Outbound funnel — Arm/Disarm toggle", () => {
  // The toggle rides every armable station's rows: a Withheld row offers Arm,
  // an Armed row offers Disarm. Arm-while-red is the core use case, so the
  // toggle must exist far beyond Ready.
  it("offers Arm on a Withheld row and Disarm on an Armed row, in every armable stage", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 5,
          draft: [outboundItem({ number: 260 })],
          red: [outboundItem({ number: 261, armed: true })],
          running: [outboundItem({ number: 262 })],
          awaiting_approval: [outboundItem({ number: 263 })],
          ready: [outboundItem({ number: 264, armed: true })],
        })}
      />,
    );

    expect(screen.getByRole("button", { name: "arm #260" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "disarm #261" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "arm #262" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "arm #263" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "disarm #264" }),
    ).toBeInTheDocument();
  });

  // Changes Requested never offers the toggle: Armed ∧ Changes-Requested is an
  // impossible state, so the consent control is absent, not merely disabled.
  it("offers no toggle on Changes Requested rows", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          changes_requested: [outboundItem({ number: 270 })],
        })}
      />,
    );

    // The diff pill button legitimately rides every row — only the consent
    // control must be absent here.
    const card = screen.getByTestId("outbound-changes-requested");
    expect(
      within(card).queryByRole("button", { name: /arm/i }),
    ).not.toBeInTheDocument();
  });

  // Clicking Arm gives the consent through the API and refetches, so the row
  // flips to Armed/Disarm on the fresh snapshot.
  it("arms a row and refetches after the mutation", async () => {
    const onArmChanged = vi.fn();
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          awaiting_approval: [outboundItem({ number: 271 })],
        })}
        onArmChanged={onArmChanged}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "arm #271" }));

    await waitFor(() => expect(mockArm).toHaveBeenCalledWith(271));
    await waitFor(() => expect(onArmChanged).toHaveBeenCalledTimes(1));
    expect(mockDisarm).not.toHaveBeenCalled();
  });

  // Clicking Disarm withdraws the consent through the API and refetches.
  it("disarms an armed row and refetches after the mutation", async () => {
    const onArmChanged = vi.fn();
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          ready: [outboundItem({ number: 272, armed: true })],
        })}
        onArmChanged={onArmChanged}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "disarm #272" }));

    await waitFor(() => expect(mockDisarm).toHaveBeenCalledWith(272));
    await waitFor(() => expect(onArmChanged).toHaveBeenCalledTimes(1));
    expect(mockArm).not.toHaveBeenCalled();
  });

  // A server rejection (e.g. the 409 racing an incoming CHANGES_REQUESTED) is
  // surfaced, not swallowed — the operator must see why consent was refused.
  it("surfaces an arm error", async () => {
    mockArm.mockRejectedValue(
      new Error("pr has changes requested; arming is rejected: #273"),
    );
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          awaiting_approval: [outboundItem({ number: 273 })],
        })}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "arm #273" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /changes requested/,
    );
  });
});

describe("Outbound funnel — armed badge", () => {
  // The armed state is a badge riding the row in whatever stage the PR is in —
  // orthogonal to the partition, so it renders in every stage.
  it("shows the armed badge on armed rows in every stage and not on Withheld ones", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 4,
          draft: [outboundItem({ number: 280, armed: true })],
          red: [outboundItem({ number: 281, armed: true })],
          ready: [
            outboundItem({ number: 282, armed: true }),
            outboundItem({ number: 283 }),
          ],
        })}
      />,
    );

    for (const testid of ["outbound-draft", "outbound-red"]) {
      expect(
        within(screen.getByTestId(testid)).getAllByText("armed"),
      ).toHaveLength(1);
    }
    // Ready: one armed row carries the badge, the Withheld one does not.
    expect(
      within(screen.getByTestId("outbound-ready")).getAllByText("armed"),
    ).toHaveLength(1);
  });
});

// mergeRecord is one /merges ledger entry — what the robot landed today.
const mergeRecord = (over: Partial<MergeRecord> = {}): MergeRecord => ({
  number: 300,
  title: "feat(cli): landed thing",
  title_parts: {
    type: "feat",
    scopes: ["cli"],
    breaking: false,
    description: "landed thing",
  },
  url: "https://github.com/o/r/pull/300",
  merged_at: "2026-07-31T10:00:00Z",
  approved_by: ["alice", "bob"],
  ...over,
});

describe("Outbound funnel — Merged station", () => {
  // The Merged station sits at the funnel bottom: the today-scoped, read-only
  // ledger view (from merges.jsonl) answering "what did the robot land today".
  // Each row shows the landed title, a GitHub link, and the approvers the
  // commit trailer named — and NO action buttons (read-only by design).
  it("renders today's merges read-only with title, link, and approvers", () => {
    render(
      <OutboundFunnel
        outbound={outbound()}
        merges={[
          mergeRecord(),
          mergeRecord({
            number: 301,
            title: "fix: second landing",
            title_parts: {
              type: "fix",
              scopes: [],
              breaking: false,
              description: "second landing",
            },
            url: "https://github.com/o/r/pull/301",
            approved_by: ["carol"],
          }),
        ]}
      />,
    );

    const card = screen.getByTestId("outbound-merged");
    expect(within(card).getByText("Merged")).toBeInTheDocument();
    expect(within(card).getByText(/today · read-only/i)).toBeInTheDocument();
    expect(within(card).getByText("Landed thing")).toBeInTheDocument();
    expect(
      within(card).getByRole("link", { name: /#300/ }),
    ).toHaveAttribute("href", "https://github.com/o/r/pull/300");
    expect(within(card).getByText(/alice, bob/)).toBeInTheDocument();
    expect(within(card).getByText(/carol/)).toBeInTheDocument();
    expect(within(card).queryAllByRole("button")).toHaveLength(0);
  });

  // An empty ledger renders the station's empty note — the funnel keeps its
  // shape and the day starts honestly at nothing.
  it("renders an empty note when the robot has landed nothing today", () => {
    render(<OutboundFunnel outbound={outbound()} merges={[]} />);

    expect(
      within(screen.getByTestId("outbound-merged")).getByText(
        /no merges yet today/i,
      ),
    ).toBeInTheDocument();
  });
});

describe("Outbound funnel — diff pill", () => {
  // Outbound rows carry the same clickable diff pill as inbound rows: clicking
  // it fetches that PR's diff and opens the same Diff card — no more static
  // readout (the on-demand lookup now resolves outbound PRs too).
  it("opens the diff card when an outbound row's pill is clicked", async () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 1,
          awaiting_approval: [outboundItem({ number: 88 })],
        })}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "view diff for #88" }));

    expect(await screen.findByText("web.go")).toBeInTheDocument();
    expect(mockFetchDiff).toHaveBeenCalledWith(88);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  // Every station's rows carry the pill — the affordance is uniform across the
  // whole outbound funnel, not just one stage.
  it("renders a clickable pill on every station's rows", () => {
    render(
      <OutboundFunnel
        outbound={outbound({
          outgoing: 6,
          draft: [outboundItem({ number: 1 })],
          red: [outboundItem({ number: 2 })],
          running: [outboundItem({ number: 3 })],
          changes_requested: [outboundItem({ number: 4 })],
          awaiting_approval: [outboundItem({ number: 5 })],
          ready: [outboundItem({ number: 6 })],
        })}
      />,
    );

    for (const n of [1, 2, 3, 4, 5, 6]) {
      expect(
        screen.getByRole("button", { name: `view diff for #${n}` }),
      ).toBeInTheDocument();
    }
  });
});

describe("Outbound funnel — loading and empty states", () => {
  // A null snapshot (first load, or a failed authored fetch that cleared it)
  // shows a loading state and none of the stations — never stale buckets.
  it("shows a loading state and no stations when the snapshot is null", () => {
    render(<OutboundFunnel outbound={null} />);

    expect(screen.getByText(/loading outbound/i)).toBeInTheDocument();
    expect(screen.queryByTestId("outgoing-total")).not.toBeInTheDocument();
    expect(screen.queryByTestId("outbound-ready")).not.toBeInTheDocument();
  });

  // Every station renders its own empty note when its stage holds nothing —
  // the funnel's shape stays visible even with an empty pull.
  it("renders an empty note per station on an empty snapshot", () => {
    render(<OutboundFunnel outbound={outbound()} />);

    for (const testid of [
      "outbound-draft",
      "outbound-red",
      "outbound-running",
      "outbound-changes-requested",
      "outbound-awaiting",
      "outbound-ready",
    ]) {
      expect(
        within(screen.getByTestId(testid)).getByText(/none/i),
      ).toBeInTheDocument();
    }
  });
});
