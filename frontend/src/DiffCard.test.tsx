import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, fireEvent } from "@testing-library/react";
import { DiffCard } from "./DiffCard";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    fetchDiff: vi.fn(),
  };
});

import { fetchDiff, type PRDiff } from "./api";
const mockFetchDiff = vi.mocked(fetchDiff);

const diff = (filename: string): PRDiff => ({
  files: [
    {
      filename,
      status: "modified",
      additions: 2,
      deletions: 1,
      patch: `@@ -1 +1 @@\n+in ${filename}`,
    },
  ],
  total_files: 1,
});

const q = (number = 41) => ({
  number,
  url: `https://github.com/o/r/pull/${number}`,
});

// deferred hands back a promise plus its settlers, so a test can hold a fetch
// in flight and decide when — and whether — it lands.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// flush lets a settled fetch promise's continuations run so React can apply the
// resulting state before the assertion.
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  mockFetchDiff.mockReset();
});

describe("DiffCard — fetch lifecycle", () => {
  // The card fetches its PR's diff on open and shows a loading state until it
  // lands; then it renders the changed files.
  it("shows the loading state until the diff resolves, then the files", async () => {
    const d = deferred<PRDiff>();
    mockFetchDiff.mockReturnValue(d.promise);
    render(<DiffCard q={q()} onClose={() => {}} />);

    expect(mockFetchDiff).toHaveBeenCalledWith(41);
    expect(screen.getByText(/loading diff/i)).toBeInTheDocument();

    d.resolve(diff("main.go"));
    expect(await screen.findByText("main.go")).toBeInTheDocument();
    expect(screen.queryByText(/loading diff/i)).not.toBeInTheDocument();
  });

  // A failed fetch surfaces the server's message in place of the diff.
  it("renders the fetch error instead of the diff", async () => {
    mockFetchDiff.mockRejectedValueOnce(new Error("diff request failed: 500"));
    render(<DiffCard q={q()} onClose={() => {}} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "diff request failed: 500",
    );
    expect(screen.queryByText(/loading diff/i)).not.toBeInTheDocument();
  });

  // Retry must clear the failed attempt immediately: while the second fetch is
  // in flight the card shows the loading state, not the stale error.
  it("clears the error back to the loading state while a retry is in flight", async () => {
    mockFetchDiff.mockRejectedValueOnce(new Error("diff request failed: 500"));
    const retry = deferred<PRDiff>();
    mockFetchDiff.mockReturnValueOnce(retry.promise);
    render(<DiffCard q={q()} onClose={() => {}} />);
    await screen.findByRole("alert");

    fireEvent.click(screen.getByRole("button", { name: /retry/i }));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText(/loading diff/i)).toBeInTheDocument();

    retry.resolve(diff("main.go"));
    expect(await screen.findByText("main.go")).toBeInTheDocument();
  });

  // A card kept open across a PR change refetches and drops back to loading —
  // the previous PR's files never linger under the new PR's heading.
  it("swaps back to the loading state when the PR changes", async () => {
    mockFetchDiff.mockResolvedValueOnce(diff("old.go"));
    const next = deferred<PRDiff>();
    mockFetchDiff.mockReturnValueOnce(next.promise);
    const { rerender } = render(<DiffCard q={q(41)} onClose={() => {}} />);
    await screen.findByText("old.go");

    rerender(<DiffCard q={q(42)} onClose={() => {}} />);

    expect(screen.queryByText("old.go")).not.toBeInTheDocument();
    expect(screen.getByText(/loading diff/i)).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toHaveAccessibleName("diff for #42");
    expect(mockFetchDiff).toHaveBeenLastCalledWith(42);

    next.resolve(diff("new.go"));
    expect(await screen.findByText("new.go")).toBeInTheDocument();
  });

  // A response for a superseded PR is dropped: it must never overwrite the card
  // that has already moved on.
  it("drops a response that lands after the PR changed", async () => {
    const stale = deferred<PRDiff>();
    const next = deferred<PRDiff>();
    mockFetchDiff.mockReturnValueOnce(stale.promise);
    mockFetchDiff.mockReturnValueOnce(next.promise);
    const { rerender } = render(<DiffCard q={q(41)} onClose={() => {}} />);
    rerender(<DiffCard q={q(42)} onClose={() => {}} />);

    stale.resolve(diff("stale.go"));
    await flush();

    expect(screen.queryByText("stale.go")).not.toBeInTheDocument();
    expect(screen.getByText(/loading diff/i)).toBeInTheDocument();

    next.resolve(diff("fresh.go"));
    expect(await screen.findByText("fresh.go")).toBeInTheDocument();
    expect(screen.queryByText("stale.go")).not.toBeInTheDocument();
  });

  // A failure for a superseded PR is dropped the same way — a late error never
  // lands on the card that has already moved on.
  it("drops a failure that lands after the PR changed", async () => {
    const stale = deferred<PRDiff>();
    const next = deferred<PRDiff>();
    mockFetchDiff.mockReturnValueOnce(stale.promise);
    mockFetchDiff.mockReturnValueOnce(next.promise);
    const { rerender } = render(<DiffCard q={q(41)} onClose={() => {}} />);
    rerender(<DiffCard q={q(42)} onClose={() => {}} />);

    stale.reject(new Error("diff request failed: 500"));
    await flush();

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    next.resolve(diff("fresh.go"));
    expect(await screen.findByText("fresh.go")).toBeInTheDocument();
  });

  // A response landing after the card unmounts settles quietly — no state
  // update, no React warning.
  it("settles quietly when the response lands after unmount", async () => {
    const late = deferred<PRDiff>();
    mockFetchDiff.mockReturnValue(late.promise);
    const consoleError = vi
      .spyOn(console, "error")
      .mockImplementation(() => {});
    const { unmount } = render(<DiffCard q={q()} onClose={() => {}} />);

    unmount();
    late.resolve(diff("main.go"));
    await flush();

    expect(consoleError).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });
});

describe("DiffCard — closing", () => {
  // The card offers three ways out — the × button, a backdrop click, and Escape —
  // and a click inside the card is not one of them.
  it("calls onClose from the × button, the backdrop and Escape, but not from a card click", async () => {
    mockFetchDiff.mockResolvedValue(diff("main.go"));
    const onClose = vi.fn();
    render(<DiffCard q={q()} onClose={onClose} />);
    const card = await screen.findByRole("dialog");

    fireEvent.click(card);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "close" }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(card.parentElement as HTMLElement);
    expect(onClose).toHaveBeenCalledTimes(2);

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  // The keydown listener is torn down with the card: Escape after close must
  // not reach a closed card's handler.
  it("stops listening for Escape once unmounted", async () => {
    mockFetchDiff.mockResolvedValue(diff("main.go"));
    const onClose = vi.fn();
    const { unmount } = render(<DiffCard q={q()} onClose={onClose} />);
    await screen.findByRole("dialog");

    unmount();
    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).not.toHaveBeenCalled();
  });
});
