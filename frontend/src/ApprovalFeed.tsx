import type { Approval } from "./api";
import { CommitTitle, TypeIcon } from "./CommitTitle";
import { clock, timeAgo, useNow } from "./time";

// MANUAL_PREFIX is the matched_rule prefix the backend stamps on a manual queue
// override (engine.ManualApprovalPrefix). The server derives the `manual` flag
// from it; the feed strips it off the same constant to show the bare reasons. We
// never re-derive manual-vs-auto on the client — `approval.manual` is the source
// of truth.
const MANUAL_PREFIX = "human approval: ";

// manualReasons returns the reasons behind a manual override: the matched_rule
// with the "human approval: " prefix stripped (e.g. "osixpatch, breaking_change").
function manualReasons(matchedRule: string): string {
  return matchedRule.startsWith(MANUAL_PREFIX)
    ? matchedRule.slice(MANUAL_PREFIX.length)
    : matchedRule;
}

// ApprovalFeed is the read-only Approval Feed panel: an observational history of
// approved PRs, newest-first, each with a GitHub link. No action buttons — the
// user glances to verify the robot behaved. Numbers in `freshNumbers` flash once
// to surface a just-arrived approval.
export function ApprovalFeed({
  approvals,
  freshNumbers,
}: {
  approvals: Approval[] | null;
  freshNumbers?: Set<number>;
}) {
  const now = useNow(1000);

  return (
    <section className="card">
      <div className="card-head">
        <h2 className="card-title">Approval feed</h2>
        <span className="card-count tnum">{approvals?.length ?? 0}</span>
        <div className="spacer" />
        <span className="card-note">today · read-only</span>
      </div>

      {approvals === null ? (
        <div className="card-loading">Loading approvals…</div>
      ) : approvals.length === 0 ? (
        <div className="card-empty">No approvals yet today.</div>
      ) : (
        <div className="feed-scroll">
          {approvals.map((a) => {
            const at = Date.parse(a.approved_at);
            return (
              <div key={a.number} className="feed-row">
                {/* PR State: a thin stripe down the row's left edge in GitHub's
                    palette marks the PR's live lifecycle. "unknown" (not yet
                    refreshed) shows no bar — the neutral default, never guessed. */}
                {a.state !== "unknown" && (
                  <div className={`feed-state-bar feed-state-${a.state}`} />
                )}
                {freshNumbers?.has(a.number) && <div className="feed-flash" />}
                <span className="type-gutter" title={a.title}>
                  <TypeIcon type={a.title_parts.type} />
                </span>
                <div className="feed-body">
                  <div className="feed-titleline">
                    <CommitTitle
                      parts={a.title_parts}
                      rawTitle={a.title}
                      number={a.number}
                      url={a.url}
                      linkClassName="feed-link"
                    />
                  </div>
                  <div className="feed-meta">
                    <span>{a.author}</span>
                    <span className="sep">·</span>
                    {a.manual ? (
                      <>
                        <span className="badge-manual">manual override</span>
                        <span className="feed-rule">
                          {manualReasons(a.matched_rule)}
                        </span>
                      </>
                    ) : (
                      <>
                        <span className="feed-rule-label">rule</span>
                        <span className="feed-rule">{a.matched_rule}</span>
                      </>
                    )}
                    <span className="sep">·</span>
                    <span
                      className="feed-time tnum"
                      title={Number.isNaN(at) ? a.approved_at : clock(at)}
                    >
                      {Number.isNaN(at) ? a.approved_at : timeAgo(now, at)}
                    </span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
