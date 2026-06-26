import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { PrRow, type PrIdentity } from "./PrRow";

const item = (over: Partial<PrIdentity> = {}): PrIdentity => ({
  number: 7,
  title: "fix(api): handle nil",
  title_parts: {
    type: "fix",
    scopes: ["api"],
    breaking: false,
    description: "handle nil",
  },
  url: "https://github.com/o/r/pull/7",
  author: "octocat",
  ...over,
});

describe("PrRow", () => {
  // tracer bullet: the row renders the parsed title line (description + a
  // separate #num link to the PR) and the author in the meta line. This is the
  // skeleton every funnel station shares.
  it("renders the title line and the author", () => {
    render(<PrRow item={item()} />);

    const link = screen.getByRole("link", { name: /#7/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/7");
    expect(screen.getByRole("link", { name: "Handle nil" })).toBeInTheDocument();
    expect(screen.getByText("octocat")).toBeInTheDocument();
  });

  // the left gutter carries the per-type glyph (ADR 0006: the icon is the only
  // type signal once the prefix is stripped).
  it("renders the type glyph in the gutter", () => {
    render(<PrRow item={item({ title_parts: { ...item().title_parts, type: "feat" } })} />);
    expect(screen.getByRole("img", { name: "feat" })).toBeInTheDocument();
  });

  // the meta line leads with the author; the station's `meta` content follows,
  // and PrRow inserts the separator between them (callers pass only their bit).
  it("renders the meta slot after the author, separated", () => {
    const { container } = render(
      <PrRow item={item()} meta={<span data-testid="mine">5 failing</span>} />,
    );
    expect(screen.getByTestId("mine")).toHaveTextContent("5 failing");
    // exactly one separator between author and the slot.
    expect(container.querySelectorAll(".pr-meta .sep")).toHaveLength(1);
  });

  // with no meta, the author stands alone — no dangling separator.
  it("renders no separator when there is no meta slot", () => {
    const { container } = render(<PrRow item={item()} />);
    expect(container.querySelectorAll(".pr-meta .sep")).toHaveLength(0);
  });

  // the action slot is placed in its own right-side region, outside the body.
  it("places the action slot", () => {
    const { container } = render(
      <PrRow item={item()} action={<button>Approve</button>} />,
    );
    const action = container.querySelector(".pr-action");
    expect(action).not.toBeNull();
    expect(action).toContainElement(screen.getByRole("button", { name: "Approve" }));
  });

  // density="compact" tags the row so the feed's denser scale applies; the
  // default row carries no modifier.
  it("applies the compact density modifier only when asked", () => {
    const { container, rerender } = render(<PrRow item={item()} />);
    expect(container.querySelector(".pr-row")).not.toHaveClass("pr-row--compact");
    rerender(<PrRow item={item()} density="compact" />);
    expect(container.querySelector(".pr-row")).toHaveClass("pr-row--compact");
  });
});
