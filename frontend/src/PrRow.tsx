import type { ReactNode } from "react";
import { CommitTitle, TypeIcon, type TitleParts } from "./CommitTitle";

// PrIdentity is the identity quintet every funnel station's PR row renders: the
// number, raw title, server-parsed title_parts (ADR 0006), GitHub url, and
// author. The three wire types (FunnelItem, QueueItem, Approval) all carry these
// fields, so each satisfies PrIdentity structurally — PrRow needs no runtime
// adapter and never sees a station's full wire shape (ADR 0014).
export type PrIdentity = {
  number: number;
  title: string;
  title_parts: TitleParts;
  url: string;
  author: string;
};

// PrRow is the one shared PR-row module behind every funnel station (ADR 0014):
// a type glyph in the left gutter, the parsed title line, and a meta line that
// leads with the author. `meta` is the station's distinguishing content after
// the author (PrRow inserts the leading separator); `action` is right-side,
// flex-none content (an Approve button, the staging shortcuts). `density`
// selects the feed's denser scale (compact) versus the default roomier row.
export function PrRow({
  item,
  meta,
  action,
  density = "default",
}: {
  item: PrIdentity;
  meta?: ReactNode;
  action?: ReactNode;
  density?: "default" | "compact";
}) {
  return (
    <div className={`pr-row${density === "compact" ? " pr-row--compact" : ""}`}>
      <span className="type-gutter" title={item.title}>
        <TypeIcon type={item.title_parts.type} />
      </span>
      <div className="pr-body">
        <div className="pr-titleline">
          <CommitTitle
            parts={item.title_parts}
            rawTitle={item.title}
            number={item.number}
            url={item.url}
          />
        </div>
        <div className="pr-meta">
          <span>{item.author}</span>
          {meta != null && (
            <>
              <span className="sep">·</span>
              {meta}
            </>
          )}
        </div>
      </div>
      {action != null && <div className="pr-action">{action}</div>}
    </div>
  );
}
