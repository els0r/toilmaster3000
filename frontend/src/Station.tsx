import type { ReactNode } from "react";
import { PrRow, type PrIdentity } from "./PrRow";

// Station.tsx holds the two shared station shells both funnels (Inbound's Cycle
// Funnel and the Outbound authored funnel) are built from — the ADR 0014
// extract-a-deep-module discipline applied one level above PrRow. The variation
// between the two funnels (which stages, which colors, which per-row meta) is
// absorbed via props; the shells own the markup exactly once.

// Segment is one stage of a distribution bar: a stable key (its color class
// hook + legend testid), a human label, and its count.
export type Segment = { key: string; label: string; n: number };

// DistributionStation renders a funnel's summary station (Inbound's Incoming,
// Outbound's Outgoing) as a single stacked distribution bar — NOT a PR list;
// the itemized PRs live in the terminal stations below — plus a legend with
// every stage's exact count (zeros included: the partition is shown in full)
// and the configured search as a code chip (omitted when empty).
export function DistributionStation({
  title,
  total,
  totalTestId,
  search,
  segments,
  className,
}: {
  title: string;
  total: number;
  totalTestId: string;
  search: string;
  segments: Segment[];
  className?: string;
}) {
  return (
    <section className={`card${className ? ` ${className}` : ""}`}>
      <div className="card-head">
        <h2 className="card-title">{title}</h2>
        <span className="card-count tnum" data-testid={totalTestId}>
          {total}
        </span>
        <div className="spacer" />
        {search && (
          <code className="filter-chip" data-testid="filter-chip">
            {search}
          </code>
        )}
      </div>

      <div className="dist-body">
        <div
          className="dist-bar"
          role="img"
          aria-label={`${title} distribution of ${total} PRs`}
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
      </div>
    </section>
  );
}

// StationCard renders one itemized terminal station: a title, its count, an
// optional head note, and its rows through the shared PrRow — or an empty note
// when the stage holds nothing. `renderMeta`/`renderAction` close over the
// station's full item type (generic T), so each station keeps its
// distinguishing content (failing-check counts, diff magnitude, badges)
// without re-owning the card or row skeleton.
export function StationCard<T extends PrIdentity>({
  testid,
  title,
  note,
  items,
  emptyNote,
  renderMeta,
  renderAction,
}: {
  testid: string;
  title: string;
  note?: string;
  items: T[];
  emptyNote: string;
  renderMeta?: (item: T) => ReactNode;
  renderAction?: (item: T) => ReactNode;
}) {
  return (
    <section className="card" data-testid={testid}>
      <div className="card-head">
        <h2 className="card-title">{title}</h2>
        <span className="card-count tnum">{items.length}</span>
        {note && (
          <>
            <div className="spacer" />
            <span className="card-note">{note}</span>
          </>
        )}
      </div>
      {items.length === 0 ? (
        <div className="card-empty">{emptyNote}</div>
      ) : (
        <div className="pr-list">
          {items.map((item) => (
            <PrRow
              key={item.number}
              item={item}
              meta={renderMeta?.(item)}
              action={renderAction?.(item)}
            />
          ))}
        </div>
      )}
    </section>
  );
}
