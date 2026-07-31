import type { ReactNode } from "react";
import type { Outbound, OutboundItem } from "./api";
import { BreakingBadge, ConflictBadge } from "./Badges";
import { DiffMag } from "./DiffMag";
import { DistributionStation, StationCard } from "./Station";

// SEGMENTS are the six terminal stages that partition Outgoing, in funnel order
// (precedence draft > not-green > changes-requested > awaiting/ready). Each has
// a stable class hook for its color and a human label for the legend; the
// counts come from the snapshot's distribution block, which sums to Outgoing by
// construction.
const SEGMENTS: {
  key: string;
  label: string;
  count: (o: Outbound) => number;
}[] = [
  { key: "draft", label: "draft", count: (o) => o.distribution.draft },
  { key: "red", label: "pipeline red", count: (o) => o.distribution.red },
  {
    key: "running",
    label: "checks running",
    count: (o) => o.distribution.running,
  },
  {
    key: "changes-requested",
    label: "changes requested",
    count: (o) => o.distribution.changes_requested,
  },
  {
    key: "awaiting",
    label: "awaiting approval",
    count: (o) => o.distribution.awaiting_approval,
  },
  { key: "ready", label: "ready", count: (o) => o.distribution.ready },
];

// OutboundFunnel renders the authored-PR funnel from the live /outbound
// snapshot: Outgoing (a distribution bar, parallel to Inbound's Incoming),
// then the itemized stations. `outbound` is null while the first snapshot is
// loading OR after a failed authored fetch — a failed fetch CLEARS the funnel
// (the robot never shows stale data), so null renders a loading state.
export function OutboundFunnel({ outbound }: { outbound: Outbound | null }) {
  if (outbound === null) {
    return (
      <div className="funnel">
        <section className="card">
          <div className="card-loading">Loading outbound…</div>
        </section>
      </div>
    );
  }

  return (
    <div className="funnel">
      <DistributionStation
        title="Outgoing"
        total={outbound.outgoing}
        totalTestId="outgoing-total"
        search={outbound.search}
        segments={SEGMENTS.map((s) => ({ ...s, n: s.count(outbound) }))}
        className="station-outgoing"
      />

      <StationCard
        testid="outbound-draft"
        title="Draft"
        note="finish it"
        items={outbound.draft ?? []}
        emptyNote="None — nothing of yours in draft."
        renderMeta={outboundMeta}
      />

      {/* Not green — two side-by-side sub-cards: an author must distinguish
          "go fix CI" (red) from "wait" (running). */}
      <div className="station-pair">
        <StationCard
          testid="outbound-red"
          title="Pipeline red"
          items={outbound.red ?? []}
          emptyNote="None — nothing of yours failed checks."
          renderMeta={outboundMeta}
        />
        <StationCard
          testid="outbound-running"
          title="Checks running"
          items={outbound.running ?? []}
          emptyNote="None — no checks in flight."
          renderMeta={outboundMeta}
        />
      </div>

      <StationCard
        testid="outbound-changes-requested"
        title="Changes Requested"
        note="address the feedback"
        items={outbound.changes_requested ?? []}
        emptyNote="None — no open objections."
        renderMeta={outboundMeta}
      />

      <StationCard
        testid="outbound-awaiting"
        title="Awaiting Approval"
        note="waiting on a reviewer"
        items={outbound.awaiting_approval ?? []}
        emptyNote="None — nothing green is unreviewed."
        renderMeta={outboundMeta}
      />

      <StationCard
        testid="outbound-ready"
        title="Ready"
        note="waiting only on you"
        items={outbound.ready ?? []}
        emptyNote="None — nothing is waiting on you."
        renderMeta={readyMeta}
      />
    </div>
  );
}

// outboundMeta is the meta line every outbound row shares: the PR's diff
// magnitude through the shared DiffMag leaf, plus the breaking badge on any
// `!`-titled row (arm with open eyes) and — on the Ready station only — the
// conflict marker on a non-mergeable row. The magnitude is a static readout
// (not the clickable DiffPill): the on-demand diff endpoint resolves
// queue/Staging PRs only (ADR 0015), so a pill here would always fail.
function outboundMeta(item: OutboundItem): ReactNode {
  return (
    <>
      <span className="diff-mag tnum">
        <DiffMag item={item} />
      </span>
      {item.title_parts.breaking && (
        <>
          <span className="sep">·</span>
          <BreakingBadge />
        </>
      )}
    </>
  );
}

// readyMeta extends the shared meta with the Ready station's conflict marker.
function readyMeta(item: OutboundItem): ReactNode {
  return (
    <>
      {outboundMeta(item)}
      {item.conflict && (
        <>
          <span className="sep">·</span>
          <ConflictBadge />
        </>
      )}
    </>
  );
}
