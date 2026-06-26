import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DiffMag } from "./DiffMag";

describe("DiffMag", () => {
  // the leaf renders the diff magnitude triple: green-bold +additions, red-bold
  // −deletions (a real U+2212 minus), and a muted K-files count.
  it("renders additions, deletions, and the files count", () => {
    render(<DiffMag item={{ additions: 12, deletions: 4, changed_files: 3 }} />);
    expect(screen.getByText("+12")).toBeInTheDocument();
    expect(screen.getByText("−4")).toBeInTheDocument();
    expect(screen.getByText("3 files")).toBeInTheDocument();
  });

  // the files segment is suppressed when changed_files is 0 (no "0 files").
  it("suppresses the files segment when changed_files is 0", () => {
    render(<DiffMag item={{ additions: 5, deletions: 0, changed_files: 0 }} />);
    expect(screen.getByText("+5")).toBeInTheDocument();
    expect(screen.queryByText(/files/)).not.toBeInTheDocument();
  });
});
