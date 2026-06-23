import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { CommitTitle, TypeIcon, type TitleParts } from "./CommitTitle";

const parts = (over: Partial<TitleParts> = {}): TitleParts => ({
  type: "fix",
  scopes: ["api"],
  breaking: false,
  description: "handle nil",
  ...over,
});

describe("CommitTitle", () => {
  // renders the parsed parts: scope pill, capitalized clean description (the
  // primary link), and a separate #num ↗ link to the PR. The type icon is
  // rendered by the row gutter, not by CommitTitle (see the TypeIcon tests).
  it("renders parsed parts with a separate #num link", () => {
    render(
      <CommitTitle
        parts={parts()}
        rawTitle="fix(api): handle nil"
        number={7}
        url="https://github.com/o/r/pull/7"
      />,
    );

    const link = screen.getByRole("link", { name: /#7/ });
    expect(link).toHaveAttribute("href", "https://github.com/o/r/pull/7");
    // The link is just the number + arrow, NOT the description.
    expect(link).not.toHaveTextContent("handle nil");

    expect(screen.getByText("api")).toBeInTheDocument();
    // Capitalized as a render transform (description data stays lower-case);
    // the description is the primary link.
    const desc = screen.getByRole("link", { name: "Handle nil" });
    expect(desc).toHaveAttribute("href", "https://github.com/o/r/pull/7");
  });

  // multiple scopes render one pill each.
  it("renders one pill per scope", () => {
    render(
      <CommitTitle
        parts={parts({ scopes: ["deps", "ci"] })}
        rawTitle="chore(deps,ci): bump"
        number={1}
        url="u1"
      />,
    );
    expect(screen.getByText("deps")).toBeInTheDocument();
    expect(screen.getByText("ci")).toBeInTheDocument();
  });

  // a non-conventional title (empty parsed type) falls back to the capitalized
  // raw title with the generic commit glyph — no scope pills.
  it("falls back to the raw title when not conventional", () => {
    render(
      <CommitTitle
        parts={parts({ type: "", scopes: [], description: "" })}
        rawTitle="totally not conventional"
        number={9}
        url="u9"
      />,
    );
    expect(screen.getByText("Totally not conventional")).toBeInTheDocument();
    expect(document.querySelectorAll(".scope-pill")).toHaveLength(0);
  });

  // a null scopes (Go nil slice on the wire) renders no pills and does not throw.
  it("tolerates null scopes", () => {
    render(
      <CommitTitle
        parts={parts({ scopes: null })}
        rawTitle="fix: x"
        number={2}
        url="u2"
      />,
    );
    expect(document.querySelectorAll(".scope-pill")).toHaveLength(0);
  });
});

describe("TypeIcon", () => {
  it("renders the matching glyph for a known type", () => {
    render(<TypeIcon type="feat" />);
    expect(screen.getByRole("img", { name: "feat" })).toBeInTheDocument();
  });

  it("falls back to the commit glyph for an unknown type", () => {
    render(<TypeIcon type="bogus" />);
    expect(screen.getByRole("img", { name: "commit" })).toBeInTheDocument();
  });
});
