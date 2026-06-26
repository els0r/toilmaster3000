import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ApprovalFeed } from "./ApprovalFeed";
import type { Approval } from "./api";

const approvals: Approval[] = [
  {
    number: 3,
    title: "fix: typo",
    title_parts: { type: "fix", scopes: [], breaking: false, description: "typo" },
    author: "carol",
    url: "https://github.com/o/r/pull/3",
    matched_rule: "approve-all",
    manual: false,
    state: "unknown",
    approved_at: "2026-06-18T10:02:00Z",
  },
  {
    number: 1,
    title: "chore(deps): bump deps",
    title_parts: { type: "chore", scopes: ["deps"], breaking: false, description: "bump deps" },
    author: "alice",
    url: "https://github.com/o/r/pull/1",
    matched_rule: "approve-all",
    manual: false,
    state: "unknown",
    approved_at: "2026-06-18T10:00:00Z",
  },
];

// F3: the feed renders entries newest-first, each with a working GitHub link.
describe("ApprovalFeed", () => {
  it("renders entries with GitHub links in given (newest-first) order", () => {
    render(<ApprovalFeed approvals={approvals} />);

    // Each entry has two links to the PR — the description and the separate
    // #num ↗ link. Select the #num links to assert order, newest-first.
    const numLinks = screen.getAllByRole("link", { name: /#/ });
    expect(numLinks).toHaveLength(2);
    // First entry is the newest (number 3).
    expect(numLinks[0]).toHaveTextContent("#3");
    expect(numLinks[0]).toHaveAttribute("href", "https://github.com/o/r/pull/3");
    expect(numLinks[1]).toHaveAttribute("href", "https://github.com/o/r/pull/1");
  });

  // The shared row skeleton (parsed title parts, capitalized description, scope
  // pill, type icon) is specified once in PrRow.test + CommitTitle.test
  // (ADR 0014); the feed's own concerns — ordering, rule/manual, time, and the
  // PR-State bar — are covered below.

  // Slice 4: an auto-approval shows the matched rule name in a chip (no manual
  // badge), driven by the server's `manual` flag — not a client string sniff.
  it("renders the matched rule name for an auto-approval", () => {
    render(<ApprovalFeed approvals={[approvals[0]]} />);
    expect(screen.getByText("approve-all")).toBeInTheDocument();
    expect(screen.queryByText(/manual override/i)).not.toBeInTheDocument();
  });

  // Slice 4: a manual override (server `manual: true`) shows the "manual
  // override" badge PLUS the reasons — the matched_rule with the "human
  // approval: " prefix stripped — and never the raw prefixed string.
  it("renders the manual override badge and stripped reasons for a manual entry", () => {
    const manual: Approval = {
      number: 9,
      title: "chore!: drop legacy flag",
      title_parts: { type: "chore", scopes: [], breaking: true, description: "drop legacy flag" },
      author: "bob",
      url: "https://github.com/o/r/pull/9",
      matched_rule: "human approval: osixpatch, breaking_change",
      manual: true,
      state: "unknown",
      approved_at: "2026-06-18T11:00:00Z",
    };
    render(<ApprovalFeed approvals={[manual]} />);

    expect(screen.getByText(/manual override/i)).toBeInTheDocument();
    // Reasons shown with the prefix stripped.
    expect(screen.getByText("osixpatch, breaking_change")).toBeInTheDocument();
    // The raw prefixed matched_rule never leaks to the UI.
    expect(
      screen.queryByText(/human approval:/i),
    ).not.toBeInTheDocument();
  });

  // PR State: each row is capped by a thin top bar colored by the PR's live
  // GitHub lifecycle (open/merged/closed). An "unknown" PR (not yet refreshed)
  // renders NO bar — the neutral default, never guessed.
  it("renders a colored top bar per PR state, and none for unknown", () => {
    const mk = (number: number, state: Approval["state"]): Approval => ({
      number,
      title: "fix: x",
      title_parts: { type: "fix", scopes: [], breaking: false, description: "x" },
      author: "carol",
      url: `https://github.com/o/r/pull/${number}`,
      matched_rule: "approve-all",
      manual: false,
      state,
      approved_at: "2026-06-18T10:00:00Z",
    });
    const { container } = render(
      <ApprovalFeed
        approvals={[mk(1, "merged"), mk(2, "open"), mk(3, "closed"), mk(4, "unknown")]}
      />,
    );

    expect(container.querySelector(".feed-state-merged")).toBeInTheDocument();
    expect(container.querySelector(".feed-state-open")).toBeInTheDocument();
    expect(container.querySelector(".feed-state-closed")).toBeInTheDocument();
    // Three bars for the three known states; the unknown row has none.
    expect(container.querySelectorAll(".feed-state-bar")).toHaveLength(3);
  });

  // Slice 4: the feed is today-scoped and read-only — the card note says so and
  // the empty state reads "No approvals yet today."
  it("renders the today/read-only note and the today empty state", () => {
    const { rerender } = render(<ApprovalFeed approvals={approvals} />);
    expect(screen.getByText(/today · read-only/i)).toBeInTheDocument();

    rerender(<ApprovalFeed approvals={[]} />);
    expect(screen.getByText(/no approvals yet today/i)).toBeInTheDocument();
    expect(screen.queryAllByRole("link")).toHaveLength(0);
  });
});
