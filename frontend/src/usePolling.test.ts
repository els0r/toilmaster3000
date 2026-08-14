import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { usePollingClearable, usePollingRefetchable } from "./usePolling";

const POLL_MS = 10_000;

// flush lets the pending fetch promises settle and their state updates commit.
async function flush() {
  await act(async () => {});
}

// tickInterval advances past one polling interval and settles what it kicked off.
async function tickInterval() {
  await act(async () => {
    vi.advanceTimersByTime(POLL_MS);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("polling lifecycle", () => {
  it("fetches once on mount and exposes the resolved value", async () => {
    const fetcher = vi.fn().mockResolvedValue("first");

    const { result } = renderHook(() =>
      usePollingRefetchable(fetcher, POLL_MS),
    );

    expect(result.current[0]).toBeNull();
    await flush();

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(result.current[0]).toBe("first");
  });

  it("re-fetches on every interval and exposes the newest value", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce("first")
      .mockResolvedValueOnce("second")
      .mockResolvedValue("third");

    const { result } = renderHook(() =>
      usePollingRefetchable(fetcher, POLL_MS),
    );
    await flush();

    await tickInterval();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(result.current[0]).toBe("second");

    await tickInterval();
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(result.current[0]).toBe("third");
  });

  it("refetch() calls the fetcher from the latest render, not the one captured at mount", async () => {
    const first = vi.fn().mockResolvedValue("first");
    const second = vi.fn().mockResolvedValue("second");

    const { result, rerender } = renderHook(
      ({ fetcher }: { fetcher: () => Promise<string> }) =>
        usePollingRefetchable(fetcher, POLL_MS),
      { initialProps: { fetcher: first } },
    );
    await flush();
    expect(result.current[0]).toBe("first");

    rerender({ fetcher: second });
    await act(async () => {
      result.current[1]();
    });

    expect(second).toHaveBeenCalledTimes(1);
    expect(first).toHaveBeenCalledTimes(1);
    expect(result.current[0]).toBe("second");
  });

  it("refetch() does not re-subscribe the interval", async () => {
    const fetcher = vi.fn().mockResolvedValue("value");

    const { result } = renderHook(() =>
      usePollingRefetchable(fetcher, POLL_MS),
    );
    await flush();

    // A refetch halfway through the interval is an extra fetch, not a reset of
    // the polling clock: the scheduled tick still lands on its original beat.
    await act(async () => {
      vi.advanceTimersByTime(POLL_MS / 2);
      result.current[1]();
    });
    expect(fetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      vi.advanceTimersByTime(POLL_MS / 2);
    });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it("unmounting stops the polling interval", async () => {
    const fetcher = vi.fn().mockResolvedValue("value");

    const { unmount } = renderHook(() =>
      usePollingRefetchable(fetcher, POLL_MS),
    );
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);

    unmount();

    await tickInterval();
    await tickInterval();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  // The cancelled guard is defensive: React 19 silently discards a state update
  // aimed at an unmounted hook, so deleting the guard does not turn this test
  // red. What it pins is that a fetch outliving its hook settles quietly — no
  // throw, no unhandled rejection, no React warning — on either policy and
  // whichever way it settles.
  it.each([
    { policy: "keep", usePolling: usePollingRefetchable, outcome: "resolve" },
    { policy: "keep", usePolling: usePollingRefetchable, outcome: "reject" },
    { policy: "clear", usePolling: usePollingClearable, outcome: "resolve" },
    { policy: "clear", usePolling: usePollingClearable, outcome: "reject" },
  ] as const)(
    "a $policy-policy fetch in flight at unmount settles quietly on $outcome",
    async ({ usePolling, outcome }) => {
      let settle: (v: string) => void = () => {};
      let fail: (e: Error) => void = () => {};
      const fetcher = () =>
        new Promise<string>((resolve, reject) => {
          settle = resolve;
          fail = reject;
        });
      const consoleError = vi
        .spyOn(console, "error")
        .mockImplementation(() => {});

      const { unmount } = renderHook(() => usePolling(fetcher, POLL_MS));
      unmount();

      // Settled deliberately outside act(): after unmount there is no hook left
      // to update, so nothing here may reach React.
      if (outcome === "resolve") settle("late");
      else fail(new Error("late failure"));
      await Promise.resolve();
      await Promise.resolve();

      expect(consoleError).not.toHaveBeenCalled();
      consoleError.mockRestore();
    },
  );
});

// The failure policy is the one axis on which the two hooks differ. Each test
// below asserts the value a failed fetch leaves behind, so swapping the two
// policies turns both of them red.
describe("failure policy", () => {
  it("keep: a failed fetch leaves the last known value in place", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce("first")
      .mockRejectedValueOnce(new Error("candidate fetch failed"))
      .mockResolvedValue("third");

    const { result } = renderHook(() =>
      usePollingRefetchable(fetcher, POLL_MS),
    );
    await flush();
    expect(result.current[0]).toBe("first");

    await tickInterval();
    expect(result.current[0]).toBe("first");

    // The loop survives the failure: the next tick still lands.
    await tickInterval();
    expect(result.current[0]).toBe("third");
  });

  it("clear: a failed fetch drops the value back to null", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce("first")
      .mockRejectedValueOnce(new Error("candidate fetch failed"))
      .mockResolvedValue("third");

    const { result } = renderHook(() => usePollingClearable(fetcher, POLL_MS));
    await flush();
    expect(result.current[0]).toBe("first");

    await tickInterval();
    expect(result.current[0]).toBeNull();

    // The loop survives the failure: the next tick still lands.
    await tickInterval();
    expect(result.current[0]).toBe("third");
  });
});

describe("re-render economy", () => {
  it("renders once on mount, once per changed value, and not at all for a kept value", async () => {
    let renders = 0;
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce("first")
      .mockRejectedValueOnce(new Error("candidate fetch failed"))
      .mockResolvedValue("third");

    const { result } = renderHook(() => {
      renders++;
      return usePollingRefetchable(fetcher, POLL_MS);
    });
    expect(renders).toBe(1);

    await flush();
    expect(result.current[0]).toBe("first");
    expect(renders).toBe(2);

    // A kept value is not a state change, so the consumer does not re-render.
    await tickInterval();
    expect(renders).toBe(2);

    await tickInterval();
    expect(result.current[0]).toBe("third");
    expect(renders).toBe(3);
  });

  it("changing the interval re-subscribes at the new cadence", async () => {
    const fetcher = vi.fn().mockResolvedValue("value");

    const { rerender } = renderHook(
      ({ ms }: { ms: number }) => usePollingRefetchable(fetcher, ms),
      { initialProps: { ms: POLL_MS } },
    );
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);

    // Re-subscription re-runs the effect, which fetches immediately.
    rerender({ ms: 1_000 });
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);

    await act(async () => {
      vi.advanceTimersByTime(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(3);

    await act(async () => {
      vi.advanceTimersByTime(1_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(4);
  });
});
