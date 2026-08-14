import { useCallback, useEffect, useRef, useState } from "react";

// FailurePolicy is the one axis on which the polling hooks differ: what a
// failed fetch does to the value the hook is holding. Each policy has an
// exported hook below, whose name carries the choice to the call site.
type FailurePolicy = "keep" | "clear";

// usePollingWithPolicy is the polling machinery both exported hooks share. It
// fetches once on mount, re-fetches every intervalMs, and returns the latest
// value plus a refetch() callback so a mutation (a manual approve, an
// Arm/Disarm toggle) can pull a fresh value immediately rather than waiting for
// the next interval.
function usePollingWithPolicy<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  policy: FailurePolicy,
): [T | null, () => void] {
  const [value, setValue] = useState<T | null>(null);

  // Hold the latest fetcher so refetch() always calls the current one without
  // re-subscribing the interval on every render. Synced in an effect — never
  // written during render, where a ref write is not a legal side effect.
  const fetcherRef = useRef(fetcher);
  useEffect(() => {
    fetcherRef.current = fetcher;
  });

  // Set while no effect is subscribed, so a fetch that outlives its hook
  // settles into nothing.
  const cancelledRef = useRef(false);

  const tick = useCallback(() => {
    fetcherRef
      .current()
      .then((v) => {
        if (!cancelledRef.current) setValue(v);
      })
      .catch(() => {
        // "keep" leaves the last known value in place; "clear" blanks it.
        if (policy === "clear" && !cancelledRef.current) setValue(null);
      });
  }, [policy]);

  useEffect(() => {
    cancelledRef.current = false;
    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelledRef.current = true;
      clearInterval(id);
    };
  }, [intervalMs, tick]);

  return [value, tick];
}

// usePollingRefetchable keeps the last known value when a fetch fails: the
// panel goes on showing the last known state rather than blanking. The
// ledger-backed surfaces (approval feed, merge ledger) poll through it — a
// stale ledger beats a vanishing history.
export function usePollingRefetchable<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): [T | null, () => void] {
  return usePollingWithPolicy(fetcher, intervalMs, "keep");
}

// usePollingClearable CLEARS the value back to null when a fetch fails. The
// Cycle Funnel polls through it so a failed candidate fetch renders an empty
// funnel (loading/empty stations) instead of stale buckets from a prior cycle:
// every Incoming PR lands in exactly one terminal stage, and a partition
// stitched from two different cycles is not that partition. Showing a wrong
// partition would be worse than showing none.
export function usePollingClearable<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): [T | null, () => void] {
  return usePollingWithPolicy(fetcher, intervalMs, "clear");
}
