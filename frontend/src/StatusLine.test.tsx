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

// F1 (tracer): the status line renders the heartbeat (outcome + sync) from
// GET /status. Per-stage counts live on the Incoming card, not the strip.
describe("StatusLine", () => {
  it("renders the heartbeat outcome from the status payload", async () => {
    const status: CycleStatus = {
      last_run: "2026-06-18T10:00:00Z",
      outcome: "ok",
      approved_count: 3,
      queue_count: 2,
      dropped_count: 5,
      staging_count: 4,
    };
    mockFetchStatus.mockResolvedValue(status);

    render(<StatusLine />);

    expect(await screen.findByText(/ok/i)).toBeInTheDocument();
    expect(screen.getByText(/next sync/i)).toBeInTheDocument();
    // The redundant per-stage tallies are no longer on the strip.
    expect(screen.queryByText(/approved 3/i)).not.toBeInTheDocument();
  });

  // F2: a never-run cycle renders a coherent "never run" line.
  it("renders a never-run state coherently", async () => {
    mockFetchStatus.mockResolvedValue({
      last_run: null,
      outcome: "never_run",
      approved_count: 0,
      queue_count: 0,
      dropped_count: 0,
      staging_count: 0,
    });

    render(<StatusLine />);

    expect(await screen.findByText(/never run/i)).toBeInTheDocument();
  });
});
