import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { SettingsPanel } from "./Settings";
import type { Settings } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return { ...actual, fetchSettings: vi.fn(), updateSettings: vi.fn() };
});

import { fetchSettings, updateSettings } from "./api";
const mockFetch = vi.mocked(fetchSettings);
const mockUpdate = vi.mocked(updateSettings);

const settings = (over: Partial<Settings> = {}): Settings => ({
  cost_low: 10,
  cost_high: 26,
  currency: "CHF",
  ...over,
});

beforeEach(() => {
  mockFetch.mockReset();
  mockUpdate.mockReset();
});

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("SettingsPanel", () => {
  // S1: the panel loads the persisted band and seeds the form with it, so the
  // editor opens on the user's current assumptions (not blank/defaults).
  it("loads and renders the persisted cost band", async () => {
    mockFetch.mockResolvedValue(settings({ cost_low: 7, cost_high: 30, currency: "€" }));

    render(<SettingsPanel />);
    await flush();

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText(/low estimate/i)).toHaveValue(7);
    expect(screen.getByLabelText(/high estimate/i)).toHaveValue(30);
    expect(screen.getByLabelText(/currency/i)).toHaveValue("€");
  });

  // S1b: the panel explains the mechanics (per the README) — what a saved switch
  // is worth and where the band's ends come from — so the figure is legible.
  it("explains the per-switch cost mechanics", async () => {
    mockFetch.mockResolvedValue(settings());

    render(<SettingsPanel />);
    await flush();

    const intro = screen.getByTestId("settings-intro");
    expect(intro).toHaveTextContent(/context.?switch/i);
    expect(intro).toHaveTextContent(/loaded/i);
  });

  // S2: editing the three fields — including the currency — and saving persists
  // the full replacement through updateSettings.
  it("edits all three constants (incl. currency) and saves them", async () => {
    mockFetch.mockResolvedValue(settings());
    mockUpdate.mockResolvedValue(settings({ cost_low: 12, cost_high: 40, currency: "£" }));

    render(<SettingsPanel />);
    await flush();

    fireEvent.change(screen.getByLabelText(/low estimate/i), {
      target: { value: "12" },
    });
    fireEvent.change(screen.getByLabelText(/high estimate/i), {
      target: { value: "40" },
    });
    fireEvent.change(screen.getByLabelText(/currency/i), {
      target: { value: "£" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await flush();

    expect(mockUpdate).toHaveBeenCalledWith({
      cost_low: 12,
      cost_high: 40,
      currency: "£",
    });
  });

  // S3: a successful save surfaces a confirmation so the lean-back editor gives
  // feedback the change landed.
  it("confirms after a successful save", async () => {
    mockFetch.mockResolvedValue(settings());
    mockUpdate.mockResolvedValue(settings());

    render(<SettingsPanel />);
    await flush();

    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await flush();

    expect(screen.getByRole("status")).toHaveTextContent(/saved/i);
  });
});
