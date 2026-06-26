import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { NeedsReview } from "./NeedsReview";
import type { QueueItem } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    approveQueueItem: vi.fn(),
    fetchDiff: vi.fn(),
  };
});

import { approveQueueItem, fetchDiff, type PRDiff } from "./api";
const mockApprove = vi.mocked(approveQueueItem);
const mockFetchDiff = vi.mocked(fetchDiff);

const diff = (over: Partial<PRDiff> = {}): PRDiff => ({
  files: [
    {
      filename: "main.go",
      status: "modified",
      additions: 2,
      deletions: 1,
      patch: "@@ -1 +1 @@\n+added line\n-removed line",
    },
  ],
  total_files: 1,
  ...over,
});

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
  ...over,
});

beforeEach(() => {
  mockApprove.mockReset();
  mockApprove.mockResolvedValue();
  mockFetchDiff.mockReset();
  mockFetchDiff.mockResolvedValue(diff());
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

  // F-queue-8: the diff magnitude renders as a single clickable pill — a button
  // carrying +additions (styled as an addition), −deletions (styled as a
  // deletion), and a muted K-files count — so a human can tell a small fix from a
  // large refactor and open the diff in one click.
  it("renders the diff magnitude as a clickable pill", () => {
    render(
      <NeedsReview
        queue={[item({ additions: 40, deletions: 12, changed_files: 3 })]}
      />,
    );
    const pill = screen.getByRole("button", { name: "view diff for #41" });
    const add = screen.getByText("+40");
    const del = screen.getByText("−12");
    expect(pill).toContainElement(add);
    expect(pill).toContainElement(del);
    expect(add).toHaveClass("diff-add");
    expect(del).toHaveClass("diff-del");
    expect(pill).toHaveTextContent("3 files");
  });

  // F-queue-9: zero changed_files suppresses the files segment gracefully — just
  // the +N / −M counts remain in the pill.
  it("suppresses the files segment when changed_files is 0", () => {
    render(
      <NeedsReview
        queue={[item({ additions: 5, deletions: 0, changed_files: 0 })]}
      />,
    );
    const pill = screen.getByRole("button", { name: "view diff for #41" });
    expect(pill).toHaveTextContent("+5");
    expect(pill).toHaveTextContent("−0");
    expect(pill).not.toHaveTextContent(/files/);
  });
});

describe("DiffCard", () => {
  const openCard = () =>
    fireEvent.click(screen.getByRole("button", { name: "view diff for #41" }));

  // F-diff-1: clicking the pill fetches that PR's diff and renders a row per
  // changed file (filename + per-file counts).
  it("opens the card and renders changed files on pill click", async () => {
    render(<NeedsReview queue={[item()]} />);
    openCard();

    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(mockFetchDiff).toHaveBeenCalledWith(41);
    const card = screen.getByRole("dialog");
    expect(card).toHaveTextContent("main.go");
    expect(card).toHaveTextContent("+2");
    expect(card).toHaveTextContent("−1");
  });

  // F-diff-2: files at or under the line threshold start expanded (patch shown);
  // larger files start collapsed (patch hidden) so a giant file doesn't blow out
  // the card — "skim quickly, to the point".
  it("expands small files and collapses large files by default", async () => {
    mockFetchDiff.mockResolvedValue(
      diff({
        files: [
          {
            filename: "small.go",
            status: "modified",
            additions: 2,
            deletions: 1,
            patch: "@@ -1 +1 @@\n+small change\n-old small",
          },
          {
            filename: "big.go",
            status: "modified",
            additions: 90,
            deletions: 10,
            patch: "@@ -1 +1 @@\n+big change\n-old big",
          },
        ],
        total_files: 2,
      }),
    );
    render(<NeedsReview queue={[item()]} />);
    openCard();
    await screen.findByText("small.go");

    expect(screen.getByText("+small change")).toBeInTheDocument();
    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
  });

  // F-diff-3: a collapsed file expands when its header is clicked (and back).
  it("toggles a file open and closed on header click", async () => {
    mockFetchDiff.mockResolvedValue(
      diff({
        files: [
          {
            filename: "big.go",
            status: "modified",
            additions: 90,
            deletions: 10,
            patch: "@@ -1 +1 @@\n+big change",
          },
        ],
        total_files: 1,
      }),
    );
    render(<NeedsReview queue={[item()]} />);
    openCard();
    const header = await screen.findByRole("button", { name: /big\.go/ });

    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
    fireEvent.click(header);
    expect(screen.getByText("+big change")).toBeInTheDocument();
    fireEvent.click(header);
    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
  });

  // F-diff-4: a file GitHub omits the patch for (binary/over-large) shows a "no
  // preview" note instead of a blank diff — the header + counts still render.
  it("shows no preview for a file with no patch", async () => {
    mockFetchDiff.mockResolvedValue(
      diff({
        files: [
          {
            filename: "logo.png",
            status: "added",
            additions: 0,
            deletions: 0,
            patch: "",
          },
        ],
        total_files: 1,
      }),
    );
    render(<NeedsReview queue={[item()]} />);
    openCard();

    expect(await screen.findByText("logo.png")).toBeInTheDocument();
    expect(screen.getByText(/no preview/i)).toBeInTheDocument();
  });

  // F-diff-5: when the PR has more changed files than the fetched page, a banner
  // says how many of how many are shown — no silent truncation.
  it("shows a 'first N of M files' banner when capped", async () => {
    mockFetchDiff.mockResolvedValue(diff({ total_files: 142 }));
    render(<NeedsReview queue={[item()]} />);
    openCard();
    await screen.findByText("main.go");

    expect(screen.getByText(/first 1 of 142 files/i)).toBeInTheDocument();
  });

  // F-diff-6: when every changed file is present, there is no banner.
  it("shows no banner when all files are present", async () => {
    mockFetchDiff.mockResolvedValue(diff({ total_files: 1 }));
    render(<NeedsReview queue={[item()]} />);
    openCard();
    await screen.findByText("main.go");

    expect(screen.queryByText(/first \d+ of/i)).not.toBeInTheDocument();
  });

  // F-diff-7: the card always carries an Open-on-GitHub escape hatch pointing at
  // the PR — the card is a skim aid, not a GitHub mirror.
  it("carries an Open on GitHub link to the PR", async () => {
    render(<NeedsReview queue={[item()]} />);
    openCard();
    const link = await screen.findByRole("link", { name: /open on github/i });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/41");
  });

  // F-diff-8: the card shows a loading state until the diff resolves.
  it("shows a loading state before the diff resolves", async () => {
    let resolve!: (d: PRDiff) => void;
    mockFetchDiff.mockReturnValue(
      new Promise<PRDiff>((r) => {
        resolve = r;
      }),
    );
    render(<NeedsReview queue={[item()]} />);
    openCard();

    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    resolve(diff());
    expect(await screen.findByText("main.go")).toBeInTheDocument();
  });

  // F-diff-9: the card closes via the × button, a backdrop click, and Esc — but
  // NOT when the card body itself is clicked.
  it("closes via the × button", async () => {
    render(<NeedsReview queue={[item()]} />);
    openCard();
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes on a backdrop click but not on a card click", async () => {
    render(<NeedsReview queue={[item()]} />);
    openCard();
    const card = await screen.findByRole("dialog");

    fireEvent.click(card);
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.click(card.parentElement as HTMLElement);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    render(<NeedsReview queue={[item()]} />);
    openCard();
    await screen.findByRole("dialog");

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  // F-diff-10: a failed fetch surfaces the server's message and offers a retry
  // that re-fetches and renders the diff.
  it("shows an error and recovers on retry", async () => {
    mockFetchDiff.mockRejectedValueOnce(new Error("diff request failed: 500"));
    mockFetchDiff.mockResolvedValueOnce(diff());
    render(<NeedsReview queue={[item()]} />);
    openCard();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /diff request failed/i,
    );
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));

    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
