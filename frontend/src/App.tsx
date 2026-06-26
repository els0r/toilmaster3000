import { useEffect, useRef, useState } from "react";
import {
  fetchApprovals,
  fetchPipeline,
  fetchQueue,
  fetchStatus,
  type Approval,
} from "./api";
import { AnalyticsPanel } from "./Analytics";
import { PipelineFunnel } from "./Pipeline";
import { RulesSection } from "./RulesEditor";
import { SettingsPanel } from "./Settings";
import { StatusLine } from "./StatusLine";
import { usePollingClearable, usePollingRefetchable } from "./usePolling";

// FILTER_EXPR is the configured candidate `--search` query, shown on the Incoming
// station as a code chip so an operator can confirm WHICH search produced the
// cycle's set. It is operator config (the backend's TM3K_SEARCH), not a wire DTO
// field — no /pipeline change exposes it — so the funnel renders it from this
// build-time constant rather than re-deriving it. Surfacing the live runtime value
// is a follow-up that needs a backend endpoint (out of this change's scope).
const FILTER_EXPR = "is:open draft:false";

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

  // The Cycle Funnel snapshot polls on the same 10s cadence. It clears on a
  // failed fetch (usePollingClearable) so a candidate-fetch failure shows an
  // empty funnel rather than a stale partition.
  const pipeline = usePollingClearable(fetchPipeline, POLL_MS);

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

  // The active tab lives in the URL hash (#pipeline / #rules) so a reload keeps
  // your place and each tab is linkable — no router dependency.
  const [tab, setTab] = useHashTab();

  // Staging-count badge: the actionable "uncovered PRs awaiting a rule" count
  // stays on the Pipeline tab control so it's visible even from the Rules tab.
  // Zero (or a not-yet-loaded funnel) is shown as no badge.
  const stagingCount = pipeline?.staging?.length ?? 0;

  return (
    <div className="app-shell">
      <div className="app-col">
        <StatusLine status={status} pollMs={POLL_MS} />

        <div className="tab-bar" role="tablist" aria-label="Sections">
          <button
            type="button"
            role="tab"
            id="tab-pipeline"
            aria-selected={tab === "pipeline"}
            aria-controls="panel-pipeline"
            className={`tab${tab === "pipeline" ? " is-active" : ""}`}
            onClick={() => setTab("pipeline")}
          >
            Pipeline
            {stagingCount > 0 && (
              <span className="tab-badge tnum" data-testid="pipeline-tab-badge">
                {stagingCount}
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
          {/* Settings is a meta concern (display assumptions, not the live review
              workflow), so it sits apart at the far right of the tab bar. */}
          <div className="spacer" />
          <button
            type="button"
            role="tab"
            id="tab-settings"
            aria-selected={tab === "settings"}
            aria-controls="panel-settings"
            className={`tab${tab === "settings" ? " is-active" : ""}`}
            onClick={() => setTab("settings")}
          >
            Settings
          </button>
        </div>

        {tab === "pipeline" && (
          <div
            id="panel-pipeline"
            role="tabpanel"
            aria-labelledby="tab-pipeline"
          >
            <PipelineFunnel
              pipeline={pipeline}
              queue={queue}
              approvals={approvals}
              freshNumbers={freshNumbers}
              filterExpr={FILTER_EXPR}
              onApproved={onApproved}
            />
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
        {tab === "settings" && (
          <div
            id="panel-settings"
            role="tabpanel"
            aria-labelledby="tab-settings"
          >
            <SettingsPanel />
          </div>
        )}
      </div>
    </div>
  );
}

// Tab is the set of selectable tabs; the hash names map 1:1 (#pipeline / #rules /
// #analytics / #settings).
type Tab = "pipeline" | "rules" | "analytics" | "settings";
const DEFAULT_TAB: Tab = "pipeline";

// tabFromHash reads the active tab from a location hash, falling back to the
// default (Pipeline) for an empty or unknown hash. The legacy #review hash maps
// to Pipeline so old bookmarks resolve to the renamed tab (the hash itself is
// rewritten to #pipeline by the redirect in useHashTab).
function tabFromHash(hash: string): Tab {
  if (hash === "#rules") return "rules";
  if (hash === "#analytics") return "analytics";
  if (hash === "#settings") return "settings";
  return DEFAULT_TAB;
}

// useHashTab keeps the active tab in sync with location.hash with no router:
// it seeds from the current hash, follows external hashchange events
// (back/forward, links), and writes the hash when a tab is selected. A legacy
// #review hash is redirected to #pipeline so old links survive the rename.
function useHashTab(): [Tab, (t: Tab) => void] {
  const [tab, setTabState] = useState<Tab>(() =>
    tabFromHash(window.location.hash),
  );

  useEffect(() => {
    // Redirect old #review bookmarks to the renamed Pipeline tab. Done here (not
    // in the seed) so the visible hash is rewritten, not just the active tab.
    if (window.location.hash === "#review") {
      window.location.hash = "pipeline";
    }
    const onHashChange = () => {
      if (window.location.hash === "#review") {
        window.location.hash = "pipeline";
        return;
      }
      setTabState(tabFromHash(window.location.hash));
    };
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
