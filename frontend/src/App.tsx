import { useEffect, useRef, useState } from "react";
import {
  fetchApprovals,
  fetchQueue,
  fetchStatus,
  type Approval,
} from "./api";
import { AnalyticsPanel } from "./Analytics";
import { ApprovalFeed } from "./ApprovalFeed";
import { NeedsReview } from "./NeedsReview";
import { RulesSection } from "./RulesEditor";
import { StatusLine } from "./StatusLine";
import { usePollingRefetchable } from "./usePolling";

// POLL_MS: the frontend polls status + approvals + queue every 10s; the backend
// cycles every 60s, so counts, the feed, and the queue update within a poll of a
// cycle.
const POLL_MS = 10_000;

export function App() {
  const [status, refetchStatus] = usePollingRefetchable(fetchStatus, POLL_MS);
  const [approvals, refetchApprovals] = usePollingRefetchable(
    fetchApprovals,
    POLL_MS,
  );
  const [queue, refetchQueue] = usePollingRefetchable(fetchQueue, POLL_MS);

  // Numbers that first appeared in the most recent poll — the feed flashes them
  // once so a fresh approval is visible without the user hunting for it.
  const freshNumbers = useFreshApprovals(approvals);

  // After a manual override approve, pull the queue (the approved item leaves it
  // next cycle) plus status + approvals immediately, so the move shows without
  // waiting for the next interval.
  function onApproved() {
    refetchQueue();
    refetchStatus();
    refetchApprovals();
  }

  // The active tab lives in the URL hash (#review / #rules) so a reload keeps
  // your place and the Rules tab is linkable — no router dependency.
  const [tab, setTab] = useHashTab();

  // Queue-count badge: the actionable count stays on the Review tab control so
  // it's visible even from the Rules tab. Zero is shown as no badge.
  const queueCount = queue?.length ?? 0;

  return (
    <div className="app-shell">
      <div className="app-col">
        <StatusLine status={status} pollMs={POLL_MS} />

        <div className="tab-bar" role="tablist" aria-label="Sections">
          <button
            type="button"
            role="tab"
            id="tab-review"
            aria-selected={tab === "review"}
            aria-controls="panel-review"
            className={`tab${tab === "review" ? " is-active" : ""}`}
            onClick={() => setTab("review")}
          >
            Review
            {queueCount > 0 && (
              <span className="tab-badge tnum" data-testid="review-tab-badge">
                {queueCount}
              </span>
            )}
          </button>
          <button
            type="button"
            role="tab"
            id="tab-rules"
            aria-selected={tab === "rules"}
            aria-controls="panel-rules"
            className={`tab${tab === "rules" ? " is-active" : ""}`}
            onClick={() => setTab("rules")}
          >
            Rules
          </button>
          <button
            type="button"
            role="tab"
            id="tab-analytics"
            aria-selected={tab === "analytics"}
            aria-controls="panel-analytics"
            className={`tab${tab === "analytics" ? " is-active" : ""}`}
            onClick={() => setTab("analytics")}
          >
            Analytics
          </button>
        </div>

        {tab === "review" && (
          <div id="panel-review" role="tabpanel" aria-labelledby="tab-review">
            <div className="main-grid">
              <NeedsReview queue={queue} onApproved={onApproved} />
              <ApprovalFeed approvals={approvals} freshNumbers={freshNumbers} />
            </div>
          </div>
        )}
        {tab === "rules" && (
          <div id="panel-rules" role="tabpanel" aria-labelledby="tab-rules">
            <RulesSection />
          </div>
        )}
        {tab === "analytics" && (
          <div
            id="panel-analytics"
            role="tabpanel"
            aria-labelledby="tab-analytics"
          >
            <AnalyticsPanel />
          </div>
        )}
      </div>
    </div>
  );
}

// Tab is the set of selectable tabs; the hash names map 1:1 (#review / #rules /
// #analytics).
type Tab = "review" | "rules" | "analytics";
const DEFAULT_TAB: Tab = "review";

// tabFromHash reads the active tab from a location hash, falling back to the
// default (Review) for an empty or unknown hash.
function tabFromHash(hash: string): Tab {
  if (hash === "#rules") return "rules";
  if (hash === "#analytics") return "analytics";
  return DEFAULT_TAB;
}

// useHashTab keeps the active tab in sync with location.hash with no router:
// it seeds from the current hash, follows external hashchange events
// (back/forward, links), and writes the hash when a tab is selected.
function useHashTab(): [Tab, (t: Tab) => void] {
  const [tab, setTabState] = useState<Tab>(() =>
    tabFromHash(window.location.hash),
  );

  useEffect(() => {
    const onHashChange = () => setTabState(tabFromHash(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  function setTab(next: Tab) {
    // Write the hash (so a reload/link keeps the tab) and update state directly
    // — we don't lean on the resulting hashchange firing, which isn't
    // guaranteed synchronously. The hashchange listener covers external changes
    // (back/forward, a pasted #rules link) and converges to the same state.
    window.location.hash = next;
    setTabState(next);
  }

  return [tab, setTab];
}

// useFreshApprovals diffs each approvals payload against the previous one and
// returns the PR numbers that are newly present. The first successful load
// seeds the baseline (nothing flashes on initial render), and each later poll
// returns only what arrived since the prior poll.
function useFreshApprovals(approvals: Approval[] | null): Set<number> {
  const seenRef = useRef<Set<number> | null>(null);
  const [fresh, setFresh] = useState<Set<number>>(() => new Set());

  useEffect(() => {
    if (!approvals) return;
    const current = new Set(approvals.map((a) => a.number));
    if (seenRef.current === null) {
      seenRef.current = current;
      return;
    }
    const added = new Set<number>();
    for (const n of current) {
      if (!seenRef.current.has(n)) added.add(n);
    }
    seenRef.current = current;
    setFresh(added);
  }, [approvals]);

  return fresh;
}
