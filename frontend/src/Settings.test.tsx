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
  minutes_per_switch: 23,
  hourly_rate: 100,
  currency: "$",
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
  // S1: the panel loads the persisted constants and seeds the form with them, so
  // the editor opens on the user's current assumptions (not blank/defaults).
  it("loads and renders the persisted assumption constants", async () => {
    mockFetch.mockResolvedValue(settings({ minutes_per_switch: 30, hourly_rate: 150, currency: "€" }));

    render(<SettingsPanel />);
    await flush();

    expect(mockFetch).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText(/minutes per switch/i)).toHaveValue(30);
    expect(screen.getByLabelText(/hourly rate/i)).toHaveValue(150);
    expect(screen.getByLabelText(/currency/i)).toHaveValue("€");
  });

  // S2: editing the three fields — including the currency — and saving persists
  // the full replacement through updateSettings.
  it("edits all three constants (incl. currency) and saves them", async () => {
    mockFetch.mockResolvedValue(settings());
    mockUpdate.mockResolvedValue(settings({ minutes_per_switch: 45, hourly_rate: 200, currency: "£" }));

    render(<SettingsPanel />);
    await flush();

    fireEvent.change(screen.getByLabelText(/minutes per switch/i), {
      target: { value: "45" },
    });
    fireEvent.change(screen.getByLabelText(/hourly rate/i), {
      target: { value: "200" },
    });
    fireEvent.change(screen.getByLabelText(/currency/i), {
      target: { value: "£" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await flush();

    expect(mockUpdate).toHaveBeenCalledWith({
      minutes_per_switch: 45,
      hourly_rate: 200,
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
