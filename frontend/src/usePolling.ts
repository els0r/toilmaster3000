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
