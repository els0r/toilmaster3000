import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { DiffPill } from "./DiffPill";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    fetchDiff: vi.fn(),
  };
});

import { fetchDiff, type PRDiff } from "./api";
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

const item = (
  over: Partial<{
    number: number;
    url: string;
    additions: number;
    deletions: number;
    changed_files: number;
  }> = {},
) => ({
  number: 41,
  url: "https://github.com/o/r/pull/41",
  additions: 40,
  deletions: 12,
  changed_files: 3,
  ...over,
});

beforeEach(() => {
  mockFetchDiff.mockReset();
  mockFetchDiff.mockResolvedValue(diff());
});

describe("DiffPill", () => {
  // F-queue-8 (relocated): the diff magnitude renders as a single clickable
  // pill — a button carrying +additions, −deletions, and a muted K-files count —
  // so a human can tell a small fix from a large refactor and open the diff in
  // one click.
  it("renders as a clickable button showing the diff magnitude", () => {
    render(<DiffPill item={item()} />);
    const pill = screen.getByRole("button", { name: "view diff for #41" });
    const add = screen.getByText("+40");
    const del = screen.getByText("−12");
    expect(pill).toContainElement(add);
    expect(pill).toContainElement(del);
    expect(add).toHaveClass("diff-add");
    expect(del).toHaveClass("diff-del");
    expect(pill).toHaveTextContent("3 files");
  });

  // F-queue-9 (relocated): zero changed_files suppresses the files segment
  // gracefully — just the +N / −M counts remain in the pill.
  it("suppresses the files segment when changed_files is 0", () => {
    render(<DiffPill item={item({ additions: 5, deletions: 0, changed_files: 0 })} />);
    const pill = screen.getByRole("button", { name: "view diff for #41" });
    expect(pill).toHaveTextContent("+5");
    expect(pill).toHaveTextContent("−0");
    expect(pill).not.toHaveTextContent(/files/);
  });
});

describe("DiffPill — opening the card", () => {
  // F-diff-1 (relocated): clicking the pill fetches that PR's diff and renders
  // a row per changed file (filename + per-file counts) — with NO parent
  // wiring: the pill owns its own open state and the card it opens.
  it("opens the card and renders changed files on pill click", async () => {
    render(<DiffPill item={item()} />);
    fireEvent.click(screen.getByRole("button", { name: "view diff for #41" }));

    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(mockFetchDiff).toHaveBeenCalledWith(41);
    const card = screen.getByRole("dialog");
    expect(card).toHaveTextContent("main.go");
    expect(card).toHaveTextContent("+2");
    expect(card).toHaveTextContent("−1");
  });

  // The pill owns the closing transition too — no callback out to a parent.
  // Closing must return the pill to its plain closed state, still clickable.
  it("closes via the × button back to the closed pill, with no parent wiring", async () => {
    render(<DiffPill item={item()} />);
    fireEvent.click(screen.getByRole("button", { name: "view diff for #41" }));
    await screen.findByRole("dialog");

    fireEvent.click(screen.getByRole("button", { name: "close" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "view diff for #41" }),
    ).toBeInTheDocument();
  });
});

describe("DiffPill — card contents", () => {
  const openCard = () =>
    fireEvent.click(screen.getByRole("button", { name: "view diff for #41" }));

  // F-diff-2 (relocated): files at or under the line threshold start expanded
  // (patch shown); larger files start collapsed (patch hidden) so a giant file
  // doesn't blow out the card — "skim quickly, to the point".
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
    render(<DiffPill item={item()} />);
    openCard();
    await screen.findByText("small.go");

    expect(screen.getByText("+small change")).toBeInTheDocument();
    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
  });

  // F-diff-3 (relocated): a collapsed file expands when its header is clicked
  // (and back).
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
    render(<DiffPill item={item()} />);
    openCard();
    const header = await screen.findByRole("button", { name: /big\.go/ });

    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
    fireEvent.click(header);
    expect(screen.getByText("+big change")).toBeInTheDocument();
    fireEvent.click(header);
    expect(screen.queryByText("+big change")).not.toBeInTheDocument();
  });

  // F-diff-4 (relocated): a file GitHub omits the patch for (binary/over-large)
  // shows a "no preview" note instead of a blank diff — the header + counts
  // still render.
  it("shows no preview for a file with no patch", async () => {
    mockFetchDiff.mockResolvedValue(
      diff({
        files: [
          { filename: "logo.png", status: "added", additions: 0, deletions: 0, patch: "" },
        ],
        total_files: 1,
      }),
    );
    render(<DiffPill item={item()} />);
    openCard();

    expect(await screen.findByText("logo.png")).toBeInTheDocument();
    expect(screen.getByText(/no preview/i)).toBeInTheDocument();
  });

  // F-diff-5 (relocated): when the PR has more changed files than the fetched
  // page, a banner says how many of how many are shown — no silent truncation.
  it("shows a 'first N of M files' banner when capped", async () => {
    mockFetchDiff.mockResolvedValue(diff({ total_files: 142 }));
    render(<DiffPill item={item()} />);
    openCard();
    await screen.findByText("main.go");

    expect(screen.getByText(/first 1 of 142 files/i)).toBeInTheDocument();
  });

  // F-diff-6 (relocated): when every changed file is present, there is no
  // banner.
  it("shows no banner when all files are present", async () => {
    mockFetchDiff.mockResolvedValue(diff({ total_files: 1 }));
    render(<DiffPill item={item()} />);
    openCard();
    await screen.findByText("main.go");

    expect(screen.queryByText(/first \d+ of/i)).not.toBeInTheDocument();
  });

  // F-diff-7 (relocated): the card always carries an Open-on-GitHub escape
  // hatch pointing at the PR — the card is a skim aid, not a GitHub mirror.
  it("carries an Open on GitHub link to the PR", async () => {
    render(<DiffPill item={item()} />);
    openCard();
    const link = await screen.findByRole("link", { name: /open on github/i });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/41");
  });

  // F-diff-8 (relocated): the card shows a loading state until the diff
  // resolves.
  it("shows a loading state before the diff resolves", async () => {
    let resolve!: (d: PRDiff) => void;
    mockFetchDiff.mockReturnValue(
      new Promise<PRDiff>((r) => {
        resolve = r;
      }),
    );
    render(<DiffPill item={item()} />);
    openCard();

    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    resolve(diff());
    expect(await screen.findByText("main.go")).toBeInTheDocument();
  });

  // F-diff-9 (relocated): the card closes on a backdrop click but NOT when the
  // card body itself is clicked, and closes on Escape.
  it("closes on a backdrop click but not on a card click", async () => {
    render(<DiffPill item={item()} />);
    openCard();
    const card = await screen.findByRole("dialog");

    fireEvent.click(card);
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.click(card.parentElement as HTMLElement);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes on Escape", async () => {
    render(<DiffPill item={item()} />);
    openCard();
    await screen.findByRole("dialog");

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  // F-diff-10 (relocated): a failed fetch surfaces the server's message and
  // offers a retry that re-fetches and renders the diff.
  it("shows an error and recovers on retry", async () => {
    mockFetchDiff.mockRejectedValueOnce(new Error("diff request failed: 500"));
    mockFetchDiff.mockResolvedValueOnce(diff());
    render(<DiffPill item={item()} />);
    openCard();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /diff request failed/i,
    );
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));

    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
