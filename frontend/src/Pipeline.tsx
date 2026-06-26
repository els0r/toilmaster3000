import { useState, type ReactNode } from "react";
import {
  createRule,
  type Approval,
  type FunnelItem,
  type Pipeline as PipelineSnapshot,
  type QueueItem,
} from "./api";
import { ApprovalFeed } from "./ApprovalFeed";
import { CommitTitle, TypeIcon } from "./CommitTitle";
import { NeedsReview } from "./NeedsReview";
import { RuleModal } from "./RulesEditor";
import {
  draftToRule,
  stagingDraft,
  type Draft,
  type RuleClass,
} from "./ruleDraft";

// SEGMENTS are the six terminal stages that partition Incoming, in funnel order
// (top→bottom of the spine). Each has a stable class hook for its color and a
// human label for the legend. The bar paints one stacked segment per non-zero
// stage; the legend lists every stage's exact count. `count` pulls the stage's
// figure from the snapshot — the itemized lists by length, the standing stages by
// their count fields (ADR: the bar partitions on current standing).
const SEGMENTS: {
  key: string;
  label: string;
  count: (p: PipelineSnapshot) => number;
}[] = [
  { key: "red", label: "pipeline red", count: (p) => len(p.dropped_red) },
  { key: "draft", label: "draft", count: (p) => len(p.dropped_draft) },
  { key: "staging", label: "staging", count: (p) => len(p.staging) },
  {
    key: "human-review",
    label: "human review",
    count: (p) => p.needs_human_review,
  },
  {
    key: "approved-tm3k",
    label: "approved by tm3k",
    count: (p) => p.approved_by_tm3k,
  },
  {
    key: "approved-elsewhere",
    label: "approved elsewhere",
    count: (p) => len(p.approved_elsewhere),
  },
];

function len(list: FunnelItem[] | null): number {
  return list?.length ?? 0;
}

// IncomingStation renders the cycle's Incoming total as a single stacked
// distribution bar (NOT a PR list — the PRs live in the stations below), with a
// legend of exact per-stage counts and the configured filter expression as a code
// chip. The bar partitions Incoming into its six terminal stages; already-approved
// PRs fold into the approved-by-tm3k segment, keeping Incoming an honest
// "everything we saw."
function IncomingStation({
  pipeline,
  filterExpr,
}: {
  pipeline: PipelineSnapshot;
  filterExpr?: string;
}) {
  const total = pipeline.incoming;
  const segments = SEGMENTS.map((s) => ({ ...s, n: s.count(pipeline) }));

  return (
    <section className="card station-incoming">
      <div className="card-head">
        <h2 className="card-title">Incoming</h2>
        <span className="card-count tnum" data-testid="incoming-total">
          {total}
        </span>
        <div className="spacer" />
        {filterExpr && (
          <code className="filter-chip" data-testid="filter-chip">
            {filterExpr}
          </code>
        )}
      </div>

      <div
        className="dist-bar"
        role="img"
        aria-label={`Incoming distribution of ${total} PRs`}
      >
        {segments
          .filter((s) => s.n > 0)
          .map((s) => (
            <div
              key={s.key}
              className={`dist-seg dist-seg-${s.key}`}
              style={{ flexGrow: s.n }}
              title={`${s.label}: ${s.n}`}
            />
          ))}
      </div>

      <div className="dist-legend">
        {segments.map((s) => (
          <span
            key={s.key}
            className="legend-item"
            data-testid={`legend-${s.key}`}
          >
            <span className={`legend-swatch dist-seg-${s.key}`} />
            <span className="legend-label">{s.label}</span>
            <span className="legend-count tnum">{s.n}</span>
          </span>
        ))}
      </div>
    </section>
  );
}

// FunnelRow renders one itemized PR row shared by the Dropped sub-queues: a type
// glyph in the gutter, the server-parsed title with its GitHub link, the author,
// and an optional trailing slot (e.g. the red card's failing-check count). The
// title is rendered from parts (never re-parsed on the client; ADR 0006).
function FunnelRow({ item, trailing }: { item: FunnelItem; trailing?: ReactNode }) {
  return (
    <div className="funnel-row">
      <span className="type-gutter" title={item.title}>
        <TypeIcon type={item.title_parts.type} />
      </span>
      <div className="funnel-body">
        <div className="funnel-titleline">
          <CommitTitle
            parts={item.title_parts}
            rawTitle={item.title}
            number={item.number}
            url={item.url}
            linkClassName="funnel-link"
          />
        </div>
        <div className="entry-meta">
          <span>{item.author}</span>
          {trailing}
        </div>
      </div>
    </div>
  );
}

// DroppedCard is one of the two side-by-side Dropped sub-queues. It carries a
// title, a count, its rows, and an empty state when its Eligibility Gate removed
// nothing. `renderTrailing` lets the red card append its per-row failing-check
// count while the draft card stays a bare title/author row.
function DroppedCard({
  testid,
  title,
  items,
  emptyNote,
  renderTrailing,
}: {
  testid: string;
  title: string;
  items: FunnelItem[];
  emptyNote: string;
  renderTrailing?: (item: FunnelItem) => ReactNode;
}) {
  return (
    <section className="card station-dropped-card" data-testid={testid}>
      <div className="card-head">
        <h2 className="card-title">{title}</h2>
        <span className="card-count tnum">{items.length}</span>
      </div>
      {items.length === 0 ? (
        <div className="card-empty">{emptyNote}</div>
      ) : (
        <div>
          {items.map((item) => (
            <FunnelRow
              key={item.number}
              item={item}
              trailing={renderTrailing?.(item)}
            />
          ))}
        </div>
      )}
    </section>
  );
}

// DroppedStation renders the two side-by-side Dropped sub-queues: pipeline-red
// (PRs the All-Green Gate removed — each row shows its failing-check count) and
// draft (PRs the Ready-for-Review Gate removed — a bare title/author row).
function DroppedStation({ pipeline }: { pipeline: PipelineSnapshot }) {
  return (
    <div className="station-dropped">
      <DroppedCard
        testid="dropped-red"
        title="Pipeline red"
        items={pipeline.dropped_red ?? []}
        emptyNote="None — nothing failed checks."
        renderTrailing={(item) => (
          <>
            <span className="sep">·</span>
            <span className="failing-checks tnum">
              {item.failing_checks} failing
            </span>
          </>
        )}
      />
      <DroppedCard
        testid="dropped-draft"
        title="Draft"
        items={pipeline.dropped_draft ?? []}
        emptyNote="None — no drafts in the pull."
      />
    </div>
  );
}

// ApprovedElsewhere renders the soft-dedup PRs — Incoming PRs GitHub already
// reports as APPROVED by someone other than tm3k — as highlighted rows ABOVE the
// ledger. tm3k deliberately left these alone (it recorded nothing to its own
// ledger), so they are surfaced here and NEVER enter the Approval feed. The
// section is omitted entirely when there are none — no empty placeholder.
function ApprovedElsewhere({ items }: { items: FunnelItem[] }) {
  if (items.length === 0) return null;
  return (
    <section
      className="card station-approved-elsewhere"
      data-testid="approved-elsewhere"
    >
      <div className="card-head">
        <h2 className="card-title">Approved elsewhere</h2>
        <span className="card-count tnum">{items.length}</span>
        <div className="spacer" />
        <span className="card-note">left alone · soft dedup</span>
      </div>
      <div>
        {items.map((item) => (
          <div key={item.number} className="elsewhere-row">
            <FunnelRow item={item} />
          </div>
        ))}
      </div>
    </section>
  );
}

// StagingRow renders one eligible-but-uncovered PR: the type glyph in the
// gutter, the server-parsed title (icon + scope pills + clean description), the
// author and diff magnitude (the change's size at a glance), and the two
// rule-minting buttons. Each button opens the FULL Rules editor pre-filled from
// this PR's parsed title — the shortcut that lets an operator drain the cohort in
// seconds. The diff magnitude reuses NeedsReview's +add/−del/files convention.
function StagingRow({
  item,
  onMint,
}: {
  item: FunnelItem;
  onMint: (cls: RuleClass) => void;
}) {
  return (
    <div className="funnel-row staging-row">
      <span className="type-gutter" title={item.title}>
        <TypeIcon type={item.title_parts.type} />
      </span>
      <div className="funnel-body">
        <div className="funnel-titleline">
          <CommitTitle
            parts={item.title_parts}
            rawTitle={item.title}
            number={item.number}
            url={item.url}
            linkClassName="funnel-link"
          />
        </div>
        <div className="entry-meta">
          <span>{item.author}</span>
          <span className="sep">·</span>
          <span className="diff-mag tnum">
            <span className="diff-add">+{item.additions}</span>
            <span className="diff-del">−{item.deletions}</span>
            {item.changed_files > 0 && (
              <span className="diff-files">{item.changed_files} files</span>
            )}
          </span>
        </div>
      </div>
      <div className="staging-actions">
        <button
          type="button"
          className="btn-ghost"
          aria-label={`add approval rule for #${item.number}`}
          onClick={() => onMint("approve")}
        >
          + Approval rule
        </button>
        <button
          type="button"
          className="btn-ghost"
          aria-label={`add human-review rule for #${item.number}`}
          onClick={() => onMint("review")}
        >
          + Human-review rule
        </button>
      </div>
    </div>
  );
}

// StagingStation renders the funnel's Staging — the previously-invisible eligible
// PRs no rule covers. Each row carries the two rule-minting shortcut buttons; a
// click opens the FULL shipped Rules editor pre-filled from the PR's parsed title
// (anchored type, un-anchored first scope, broad by design so one rule drains the
// cohort). Saving rides POST /rules unchanged (#3 makes the matched PR leave
// Staging next cycle). An empty Staging renders an unambiguous "every eligible PR
// is covered" message — "nothing to do here" reads as progress.
function StagingStation({ items }: { items: FunnelItem[] }) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    if (!draft) return;
    setError(null);
    try {
      await createRule(draftToRule(draft));
      setDraft(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <section className="card station-staging" data-testid="staging">
      <div className="card-head">
        <h2 className="card-title">Staging</h2>
        <span className="card-count tnum">{items.length}</span>
        <div className="spacer" />
        <span className="card-note">eligible · no rule yet</span>
      </div>

      {error && (
        <p className="row-alert" role="alert">
          {error}
        </p>
      )}

      {items.length === 0 ? (
        <div className="card-empty">
          Every eligible PR is covered — nothing staged.
        </div>
      ) : (
        <div>
          {items.map((item) => (
            <StagingRow
              key={item.number}
              item={item}
              onMint={(cls) => setDraft(stagingDraft(item.title_parts, cls))}
            />
          ))}
        </div>
      )}

      {draft && (
        <RuleModal
          draft={draft}
          onChange={setDraft}
          onCancel={() => setDraft(null)}
          onSave={() => void save()}
          onDelete={() => setDraft(null)}
        />
      )}
    </section>
  );
}

// PipelineFunnel renders the Cycle Funnel: Incoming (a distribution bar), Dropped
// (red + draft), Staging (the interactive rule-minting bucket),
// Needs-Human-Review (the existing queue panel), and the Approval ledger (the
// existing feed, with approved-elsewhere rows highlighted above it). It composes
// the live /pipeline snapshot with the /queue and /approvals payloads the app
// already polls.
//
// `pipeline` is null while the first snapshot is loading OR after a failed
// candidate fetch — a failed fetch CLEARS the funnel rather than showing stale
// buckets, so the funnel renders a loading state, never the prior cycle's data.
export function PipelineFunnel({
  pipeline,
  queue,
  approvals,
  freshNumbers,
  filterExpr,
  onApproved,
}: {
  pipeline: PipelineSnapshot | null;
  queue: QueueItem[] | null;
  approvals: Approval[] | null;
  freshNumbers?: Set<number>;
  filterExpr?: string;
  onApproved?: () => void;
}) {
  return (
    <div className="funnel">
      {/* Stations 1–2 (Incoming, Dropped) are the live snapshot: a null pipeline
          (first load OR a cleared failed fetch) shows the loading state, never a
          stale partition. */}
      {pipeline === null ? (
        <section className="card station-incoming">
          <div className="card-loading">Loading funnel…</div>
        </section>
      ) : (
        <>
          <IncomingStation pipeline={pipeline} filterExpr={filterExpr} />
          <DroppedStation pipeline={pipeline} />
          {/* Staging — the funnel's third terminal bucket — is interactive: each
              eligible-but-uncovered PR carries the two rule-minting shortcuts. */}
          <StagingStation items={pipeline.staging ?? []} />
        </>
      )}

      {/* Station 4 (Needs Human Review) and Station 5 (Approval ledger) reuse the
          existing panels, fed by /queue and /approvals (their own sources, with
          their own loading/empty states). */}
      <NeedsReview queue={queue} onApproved={onApproved} />
      {pipeline !== null && (
        <ApprovedElsewhere items={pipeline.approved_elsewhere ?? []} />
      )}
      <ApprovalFeed approvals={approvals} freshNumbers={freshNumbers} />
    </div>
  );
}
