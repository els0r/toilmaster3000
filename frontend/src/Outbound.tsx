import { useState, type ReactNode } from "react";
import { armOutbound, disarmOutbound } from "./api";
import type { MergeRecord, Outbound, OutboundItem } from "./api";
import { ArmedBadge, BreakingBadge, ConflictBadge } from "./Badges";
import { DiffPill } from "./DiffPill";
import { DistributionStation, StationCard } from "./Station";
import { clock, timeAgo, useNow } from "./time";

// SEGMENTS are the seven terminal stages that partition Outgoing, in funnel
// order (precedence draft > not-green > changes-requested > awaiting >
// in-discussion > ready). Each has a stable class hook for its color and a
// human label for the legend; the counts come from the snapshot's
// distribution block, which sums to Outgoing by construction.
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
  {
    key: "in-discussion",
    label: "in discussion",
    count: (o) => o.distribution.in_discussion,
  },
  { key: "ready", label: "ready", count: (o) => o.distribution.ready },
];

// OutboundFunnel renders the authored-PR funnel from the live /outbound
// snapshot: Outgoing (a distribution bar, parallel to Inbound's Incoming),
// then the itemized stations. `outbound` is null while the first snapshot is
// loading OR after a failed authored fetch — a failed fetch CLEARS the funnel
// (the robot never shows stale data), so null renders a loading state.
// `onArmChanged` is called after a successful Arm/Disarm toggle so the caller
// can refetch the snapshot immediately (the toggle reflects on the next
// fetch, not the next cycle). `merges` is today's ledger from GET /merges —
// the read-only Merged station at the funnel bottom (null while loading
// renders the station empty; the ledger poll keeps its last known value).
export function OutboundFunnel({
  outbound,
  onArmChanged,
  merges,
}: {
  outbound: Outbound | null;
  onArmChanged?: () => void;
  merges?: MergeRecord[] | null;
}) {
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<number | null>(null);

  // toggleArm flips one row's consent: Arm when Withheld, Disarm when Armed.
  // A server rejection (the 409 racing an incoming CHANGES_REQUESTED, a 404
  // for a row that just left the pull) is surfaced, never swallowed — the
  // operator must see why consent was refused.
  async function toggleArm(item: OutboundItem) {
    setError(null);
    setPending(item.number);
    try {
      if (item.armed) {
        await disarmOutbound(item.number);
      } else {
        await armOutbound(item.number);
      }
      onArmChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setPending(null);
    }
  }

  // armToggle is the per-row Arm/Disarm action, offered on every station
  // EXCEPT Changes Requested (Armed ∧ Changes-Requested is an impossible
  // state, so the consent control is absent there, not merely disabled).
  function armToggle(item: OutboundItem): ReactNode {
    return (
      <button
        type="button"
        className={item.armed ? "btn-disarm" : "btn-arm"}
        onClick={() => toggleArm(item)}
        disabled={pending === item.number}
        aria-label={`${item.armed ? "disarm" : "arm"} #${item.number}`}
      >
        {item.armed ? "Disarm" : "Arm"}
      </button>
    );
  }

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
      {error && (
        <p className="row-alert" role="alert">
          {error}
        </p>
      )}

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
        renderAction={armToggle}
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
          renderAction={armToggle}
        />
        <StationCard
          testid="outbound-running"
          title="Checks running"
          items={outbound.running ?? []}
          emptyNote="None — no checks in flight."
          renderMeta={outboundMeta}
          renderAction={armToggle}
        />
      </div>

      {/* Changes Requested never offers the Arm toggle: an open objection
          requires the feedback addressed and fresh consent after re-approval. */}
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
        renderAction={armToggle}
      />

      {/* In Discussion (ADR 0019): approved but held by ≥1 unresolved review
          thread — a hold on a counterparty's conversation, not an objection.
          The merge step walks only Ready, so this stage IS the discussion
          gate. */}
      <StationCard
        testid="outbound-in-discussion"
        title="In Discussion"
        note="waiting on the conversation"
        items={outbound.in_discussion ?? []}
        emptyNote="None — no open discussions."
        renderMeta={discussionMeta}
        renderAction={armToggle}
      />

      <StationCard
        testid="outbound-ready"
        title="Ready"
        note="waiting only on you"
        items={outbound.ready ?? []}
        emptyNote="None — nothing is waiting on you."
        renderMeta={readyMeta}
        renderAction={armToggle}
      />

      <MergedStation merges={merges ?? []} />
    </div>
  );
}

// MergedRow is a ledger entry shaped for the shared PrRow: every merged PR is
// the operator's own, so the author slot stays empty (PrRow omits it) and the
// row leads with the approvers instead.
type MergedRow = MergeRecord & { author: string };

// MergedStation is the funnel's read-only bottom station: today's merge
// ledger (GET /merges, the merges.jsonl analog of the Approval Feed). It
// answers "what did the robot land today" — each row names the approvers the
// commit's "Approved by:" trailer carried and links to the PR; there are no
// actions, matching the feed's observational stance.
function MergedStation({ merges }: { merges: MergeRecord[] }) {
  const now = useNow(1000);
  const rows: MergedRow[] = merges.map((m) => ({ ...m, author: "" }));

  return (
    <StationCard
      testid="outbound-merged"
      title="Merged"
      note="today · read-only"
      items={rows}
      emptyNote="No merges yet today."
      renderMeta={(m) => {
        const at = Date.parse(m.merged_at);
        return (
          <>
            <span className="feed-rule-label">approved by</span>
            <span className="feed-rule">{(m.approved_by ?? []).join(", ")}</span>
            <span className="sep">·</span>
            <span
              className="feed-time tnum"
              title={Number.isNaN(at) ? m.merged_at : clock(at)}
            >
              {Number.isNaN(at) ? m.merged_at : timeAgo(now, at)}
            </span>
          </>
        );
      }}
    />
  );
}

// outboundMeta is the meta line every outbound row shares: the PR's diff
// magnitude as the shared clickable DiffPill (the on-demand diff lookup
// resolves outbound PRs too), the breaking badge on any `!`-titled row (arm
// with open eyes), and the armed badge on an Armed row — the consent state
// riding the row in whatever stage it is in.
function outboundMeta(item: OutboundItem): ReactNode {
  return (
    <>
      <DiffPill item={item} />
      {item.title_parts.breaking && (
        <>
          <span className="sep">·</span>
          <BreakingBadge />
        </>
      )}
      {item.armed && (
        <>
          <span className="sep">·</span>
          <ArmedBadge />
        </>
      )}
    </>
  );
}

// discussionMeta extends the shared meta with the In Discussion station's
// defining datum: the unresolved-thread count holding the merge (≥1 by
// construction — zero is Ready's definition). Neutral-toned, not the error
// styling — a discussion is a hold, not an objection (ADR 0019).
function discussionMeta(item: OutboundItem): ReactNode {
  return (
    <>
      {outboundMeta(item)}
      <span className="sep">·</span>
      <span className="thread-count tnum">
        {item.unresolved_threads} unresolved
      </span>
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
