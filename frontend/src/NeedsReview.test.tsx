import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { NeedsReview } from "./NeedsReview";
import type { QueueItem } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    approveQueueItem: vi.fn(),
  };
});

import { approveQueueItem } from "./api";
const mockApprove = vi.mocked(approveQueueItem);

const item = (over: Partial<QueueItem> = {}): QueueItem => ({
  number: 41,
  title: "chore!: drop legacy flag",
  title_parts: {
    type: "chore",
    scopes: [],
    breaking: true,
    description: "drop legacy flag",
  },
  author: "bob",
  url: "https://github.com/o/r/pull/41",
  additions: 40,
  deletions: 12,
  changed_files: 3,
  reasons: ["breaking_change"],
  screen_holds: [],
  ...over,
});

beforeEach(() => {
  mockApprove.mockReset();
  mockApprove.mockResolvedValue();
});

describe("NeedsReview", () => {
  // F-queue-1: the queue composes a row with its two distinguishing pieces — the
  // breaking badge and the per-item Approve action. The shared row skeleton
  // (title, #num link, author) is specified once in PrRow.test (ADR 0014).
  it("composes a row with the breaking badge and an Approve action", () => {
    render(<NeedsReview queue={[item()]} />);

    expect(screen.getByText("breaking change")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "approve #41" }),
    ).toBeInTheDocument();
  });

  // F-queue-2: clicking Approve calls approveQueueItem and refetches (via the
  // onApproved callback).
  it("approves an item and refetches after the mutation", async () => {
    const onApproved = vi.fn();
    render(<NeedsReview queue={[item({ number: 41 })]} onApproved={onApproved} />);

    fireEvent.click(screen.getByRole("button", { name: "approve #41" }));

    await waitFor(() => expect(mockApprove).toHaveBeenCalledWith(41));
    await waitFor(() => expect(onApproved).toHaveBeenCalledTimes(1));
  });

  // F-queue-3: a server error on approve is surfaced, not swallowed.
  it("surfaces an approve error", async () => {
    mockApprove.mockRejectedValue(new Error("pr not in needs-human-review queue: #41"));
    render(<NeedsReview queue={[item({ number: 41 })]} />);

    fireEvent.click(screen.getByRole("button", { name: "approve #41" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /not in needs-human-review queue/,
    );
  });

  // F-queue-4: an empty queue renders a clear empty state and no buttons.
  it("renders an empty state when nothing needs review", () => {
    render(<NeedsReview queue={[]} />);
    expect(screen.getByText(/nothing needs review/i)).toBeInTheDocument();
    expect(screen.queryAllByRole("button")).toHaveLength(0);
  });

  // F-queue-5 (slice 1): non-breaking reasons each render one chip; a
  // breaking PR additionally shows the breaking badge.
  it("renders one chip per non-breaking reason plus the breaking badge", () => {
    render(
      <NeedsReview
        queue={[item({ reasons: ["osixpatch", "breaking_change"] })]}
      />,
    );

    // The breaking badge + the osixpatch chip = two badges; no breaking_change
    // chip (the badge represents it).
    const badges = document.querySelectorAll(".badge-breaking");
    expect(badges).toHaveLength(2);
    expect(screen.getByText("breaking change")).toBeInTheDocument();
    expect(screen.getByText("osixpatch")).toBeInTheDocument();
    expect(screen.queryByText("breaking_change")).not.toBeInTheDocument();
  });

  // F-queue-6 (slice 1): the breaking badge shows iff title_parts.breaking — an
  // Approve-tied breaking PR (reasons=[breaking_change]) shows the badge with no
  // orphan breaking_change chip.
  it("shows the breaking badge with no orphan breaking_change chip", () => {
    render(<NeedsReview queue={[item({ reasons: ["breaking_change"] })]} />);

    const badges = document.querySelectorAll(".badge-breaking");
    expect(badges).toHaveLength(1);
    expect(screen.getByText("breaking change")).toBeInTheDocument();
    expect(screen.queryByText("breaking_change")).not.toBeInTheDocument();
  });

  // F-queue-7 (slice 1): a non-breaking queued item (e.g. a Review Rule match)
  // shows its reason chip and NO breaking badge.
  it("shows no breaking badge for a non-breaking item", () => {
    render(
      <NeedsReview
        queue={[
          item({
            title: "chore(osixpatch): patch",
            title_parts: {
              type: "chore",
              scopes: ["osixpatch"],
              breaking: false,
              description: "patch",
            },
            reasons: ["osixpatch gate"],
          }),
        ]}
      />,
    );

    const badges = document.querySelectorAll(".badge-breaking");
    expect(badges).toHaveLength(1);
    expect(screen.getByText("osixpatch gate")).toBeInTheDocument();
    expect(screen.queryByText("breaking change")).not.toBeInTheDocument();
  });

  // A screen-held entry: the engine's queueItemForHolds shape — every reason
  // is screen:<name>, mirrored 1:1 by the screen_holds prose (rule reasons
  // XOR screen holds, disjoint by construction).
  const screenHeld = (): QueueItem =>
    item({
      number: 52,
      title: "chore: sneaky dep",
      title_parts: {
        type: "chore",
        scopes: [],
        breaking: false,
        description: "sneaky dep",
      },
      reasons: ["screen:security", "screen:license"],
      screen_holds: [
        { screen: "security", reason: "touches auth code without tests" },
        { screen: "license", reason: "screen unavailable: harness timeout" },
      ],
    });

  // F-queue-9 (slice 38): screen chips derive from the screen_holds structure
  // and render visually distinct from rule chips — a robot hold must read
  // differently from a rule route at a glance. A rule-routed sibling keeps its
  // rule chip and grows no screen chip; the held row keeps its override
  // Approve button (the human outranks the robot).
  it("renders screen chips distinct from rule chips, one per holding screen", () => {
    render(
      <NeedsReview queue={[screenHeld(), item({ reasons: ["osixpatch gate"] })]} />,
    );

    const screenChips = document.querySelectorAll(".badge-screen");
    expect(screenChips).toHaveLength(2);
    expect(screen.getByText("screen:security")).toBeInTheDocument();
    expect(screen.getByText("screen:license")).toBeInTheDocument();

    // The rule-routed sibling: one rule chip (plus its breaking badge), zero
    // screen chips — and the held row renders none of the rule-chip class.
    expect(screen.getByText("osixpatch gate")).toBeInTheDocument();
    expect(
      screen.getByText("osixpatch gate").classList.contains("badge-screen"),
    ).toBe(false);
    for (const chip of screenChips) {
      expect(chip.classList.contains("badge-breaking")).toBe(false);
    }

    // The override affordance stays on the held row.
    expect(
      screen.getByRole("button", { name: "approve #52" }),
    ).toBeInTheDocument();
  });

  // F-queue-10 (slice 38): the screen's prose reasoning is one click away on
  // the held row — hidden until the chip is clicked, disclosed with every
  // holding screen's own words, and dismissible by a second click.
  it("discloses the screen's prose reasoning on chip click", () => {
    render(<NeedsReview queue={[screenHeld()]} />);

    expect(
      screen.queryByText(/touches auth code without tests/),
    ).not.toBeInTheDocument();

    const chip = screen.getByRole("button", {
      name: "screen security reasoning for #52",
    });
    expect(chip).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(chip);

    expect(chip).toHaveAttribute("aria-expanded", "true");
    expect(
      screen.getByText(/touches auth code without tests/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/screen unavailable: harness timeout/),
    ).toBeInTheDocument();

    fireEvent.click(chip);
    expect(
      screen.queryByText(/touches auth code without tests/),
    ).not.toBeInTheDocument();
  });

  // F-queue-8 (shrunk): the row wires a DiffPill for the item — the pill's own
  // rendering, click-to-open, and card contents are specified once in
  // DiffPill.test.tsx (ADR 0014-style: shared leaf tested at its own seam).
  it("renders a diff pill wired to the item", () => {
    render(<NeedsReview queue={[item({ number: 41 })]} />);
    expect(
      screen.getByRole("button", { name: "view diff for #41" }),
    ).toBeInTheDocument();
  });
});
