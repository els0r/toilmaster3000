import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusLine } from "./StatusLine";
import type { CycleStatus } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return { ...actual, fetchStatus: vi.fn() };
});

import { fetchStatus } from "./api";
const mockFetchStatus = vi.mocked(fetchStatus);

beforeEach(() => {
  mockFetchStatus.mockReset();
});

// F1 (tracer): the status line renders outcome and counts from GET /status.
describe("StatusLine", () => {
  it("renders outcome and counts from the status payload", async () => {
    const status: CycleStatus = {
      last_run: "2026-06-18T10:00:00Z",
      outcome: "ok",
      approved_count: 3,
      queue_count: 2,
      dropped_count: 5,
    };
    mockFetchStatus.mockResolvedValue(status);

    render(<StatusLine />);

    expect(await screen.findByText(/approved 3/i)).toBeInTheDocument();
    expect(screen.getByText(/queue 2/i)).toBeInTheDocument();
    expect(screen.getByText(/dropped 5/i)).toBeInTheDocument();
    expect(screen.getByText(/ok/i)).toBeInTheDocument();
  });

  // F2: a never-run cycle renders a coherent "never run" line.
  it("renders a never-run state coherently", async () => {
    mockFetchStatus.mockResolvedValue({
      last_run: null,
      outcome: "never_run",
      approved_count: 0,
      queue_count: 0,
      dropped_count: 0,
    });

    render(<StatusLine />);

    expect(await screen.findByText(/never run/i)).toBeInTheDocument();
  });
});
