import { useEffect, useRef, useState } from "react";
import {
  fetchApprovals,
  fetchMerges,
  fetchOutbound,
  fetchPipeline,
  fetchQueue,
  fetchStatus,
  type Approval,
} from "./api";
import { AnalyticsPanel } from "./Analytics";
import { OutboundFunnel } from "./Outbound";
import { PipelineFunnel } from "./Pipeline";
import { RulesSection } from "./RulesEditor";
import { SettingsPanel } from "./Settings";
import { StatusLine } from "./StatusLine";
import { usePollingClearable, usePollingRefetchable } from "./usePolling";

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
  const [pipeline] = usePollingClearable(fetchPipeline, POLL_MS);

  // The outbound snapshot polls alongside it (the tab badge needs it from every
  // tab) with the same clear-on-failure policy: a failed authored fetch shows a
  // loading funnel, never stale stages. refetchOutbound pulls a fresh snapshot
  // right after an Arm/Disarm toggle, so the row flips without waiting a poll.
  const [outbound, refetchOutbound] = usePollingClearable(
    fetchOutbound,
    POLL_MS,
  );

  // Today's merge ledger polls on the same cadence for the Merged station at
  // the outbound funnel's bottom. Like the approvals feed (its inbound analog)
  // it is a persisted ledger, so a failed poll keeps the last known value
  // rather than clearing — stale ledger beats a vanishing history.
  const [merges] = usePollingRefetchable(fetchMerges, POLL_MS);

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

  // The active tab lives in the URL hash (#inbound / #outbound / #rules /
  // #analytics) so a reload keeps your place and each tab is linkable — no
  // router dependency.
  const [tab, setTab] = useHashTab();

  // Staging-count badge: the actionable "uncovered PRs awaiting a rule" count
  // stays on the Inbound tab control so it's visible even from the Rules tab.
  // Zero (or a not-yet-loaded funnel) is shown as no badge.
  const stagingCount = pipeline?.staging?.length ?? 0;

  // Ready-count badge: the outbound actionable signal (PRs waiting only on
  // you), mirroring Inbound's staging badge. Zero shows no badge.
  const readyCount = outbound?.ready?.length ?? 0;

  return (
    <div className="app-shell">
      <div className="app-col">
        <StatusLine status={status} pollMs={POLL_MS} />

        <div className="tab-bar" role="tablist" aria-label="Sections">
          <button
            type="button"
            role="tab"
            id="tab-inbound"
            aria-selected={tab === "inbound"}
            aria-controls="panel-inbound"
            className={`tab${tab === "inbound" ? " is-active" : ""}`}
            onClick={() => setTab("inbound")}
          >
            Inbound
            {stagingCount > 0 && (
              <span className="tab-badge tnum" data-testid="inbound-tab-badge">
                {stagingCount}
              </span>
            )}
          </button>
          <button
            type="button"
            role="tab"
            id="tab-outbound"
            aria-selected={tab === "outbound"}
            aria-controls="panel-outbound"
            className={`tab${tab === "outbound" ? " is-active" : ""}`}
            onClick={() => setTab("outbound")}
          >
            Outbound
            {readyCount > 0 && (
              <span className="tab-badge tnum" data-testid="outbound-tab-badge">
                {readyCount}
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

        {tab === "inbound" && (
          <div id="panel-inbound" role="tabpanel" aria-labelledby="tab-inbound">
            <PipelineFunnel
              pipeline={pipeline}
              queue={queue}
              approvals={approvals}
              freshNumbers={freshNumbers}
              onApproved={onApproved}
            />
          </div>
        )}
        {tab === "outbound" && (
          <div
            id="panel-outbound"
            role="tabpanel"
            aria-labelledby="tab-outbound"
          >
            <OutboundFunnel
              outbound={outbound}
              onArmChanged={refetchOutbound}
              merges={merges}
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

// Tab is the set of selectable tabs; the hash names map 1:1 (#inbound /
// #outbound / #rules / #analytics / #settings).
type Tab = "inbound" | "outbound" | "rules" | "analytics" | "settings";
const DEFAULT_TAB: Tab = "inbound";

// LEGACY_HASHES are the retired hash names, redirected to #inbound so old
// bookmarks survive both renames (Review → Pipeline → Inbound).
const LEGACY_HASHES = ["#review", "#pipeline"];

// tabFromHash reads the active tab from a location hash, falling back to the
// default (Inbound) for an empty or unknown hash. The legacy #review and
// #pipeline hashes map to Inbound so old bookmarks resolve to the renamed tab
// (the hash itself is rewritten to #inbound by the redirect in useHashTab).
function tabFromHash(hash: string): Tab {
  if (hash === "#outbound") return "outbound";
  if (hash === "#rules") return "rules";
  if (hash === "#analytics") return "analytics";
  if (hash === "#settings") return "settings";
  return DEFAULT_TAB;
}

// useHashTab keeps the active tab in sync with location.hash with no router:
// it seeds from the current hash, follows external hashchange events
// (back/forward, links), and writes the hash when a tab is selected. Legacy
// #review / #pipeline hashes are redirected to #inbound so old links survive
// the renames.
function useHashTab(): [Tab, (t: Tab) => void] {
  const [tab, setTabState] = useState<Tab>(() =>
    tabFromHash(window.location.hash),
  );

  useEffect(() => {
    // Redirect old #review / #pipeline bookmarks to the renamed Inbound tab.
    // Done here (not in the seed) so the visible hash is rewritten, not just
    // the active tab.
    if (LEGACY_HASHES.includes(window.location.hash)) {
      window.location.hash = "inbound";
    }
    const onHashChange = () => {
      if (LEGACY_HASHES.includes(window.location.hash)) {
        window.location.hash = "inbound";
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
