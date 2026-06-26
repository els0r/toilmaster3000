import { useEffect, useRef, useState } from "react";

// usePolling fetches once on mount and then re-fetches every intervalMs,
// keeping the latest successful value. Errors leave the previous value in
// place (the panel keeps showing the last known state rather than blanking).
export function usePolling<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): T | null {
  const [value, setValue] = useState<T | null>(null);

  useEffect(() => {
    let cancelled = false;

    const tick = () => {
      fetcher()
        .then((v) => {
          if (!cancelled) setValue(v);
        })
        .catch(() => {
          /* keep last known value */
        });
    };

    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
    // fetcher is treated as stable; intervalMs drives re-subscription.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs]);

  return value;
}

// usePollingClearable is usePolling whose failure policy is the OPPOSITE: a
// failed fetch CLEARS the value back to null rather than keeping the last known
// one. The Cycle Funnel uses it so a failed candidate fetch renders an empty
// funnel (loading/empty stations) instead of stale buckets from a prior cycle —
// showing a wrong partition would be worse than showing none.
export function usePollingClearable<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): T | null {
  const [value, setValue] = useState<T | null>(null);

  useEffect(() => {
    let cancelled = false;

    const tick = () => {
      fetcher()
        .then((v) => {
          if (!cancelled) setValue(v);
        })
        .catch(() => {
          // Clear: a failed fetch must not leave a stale snapshot on screen.
          if (!cancelled) setValue(null);
        });
    };

    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
    // fetcher is treated as stable; intervalMs drives re-subscription.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs]);

  return value;
}

// usePollingRefetchable is usePolling that also returns a refetch() callback, so
// an action (e.g. approving a queue item) can pull the latest value immediately
// rather than waiting for the next interval. The polling cadence is unchanged.
export function usePollingRefetchable<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): [T | null, () => void] {
  const [value, setValue] = useState<T | null>(null);
  // Hold the latest fetcher so refetch() always calls the current one without
  // re-subscribing the interval on every render.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;
  const cancelledRef = useRef(false);

  const tick = () => {
    fetcherRef
      .current()
      .then((v) => {
        if (!cancelledRef.current) setValue(v);
      })
      .catch(() => {
        /* keep last known value */
      });
  };

  useEffect(() => {
    cancelledRef.current = false;
    tick();
    const id = setInterval(tick, intervalMs);
    return () => {
      cancelledRef.current = true;
      clearInterval(id);
    };
    // intervalMs drives re-subscription; tick reads the latest fetcher via ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs]);

  return [value, tick];
}
