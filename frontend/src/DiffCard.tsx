import { useEffect, useState } from "react";
import { fetchDiff, type FileDiff, type PRDiff, type QueueItem } from "./api";

// EXPAND_THRESHOLD is the changed-line count (additions + deletions) at or below
// which a file starts expanded; larger files start collapsed so a giant file
// doesn't blow out the card (CONTEXT "Diff card").
const EXPAND_THRESHOLD = 40;

// DiffCard is the pop-up that opens when a queued PR's Diff pill is clicked. It
// fetches the PR's changed files on demand and renders them per-file so a human
// can skim the change without leaving tm3k. It is a skim aid, NOT a GitHub
// mirror: it shows at most one page of files (a "first N of M" banner past the
// cap), renders no preview for binary/over-large files, and always carries an
// Open-on-GitHub escape hatch (CONTEXT "Diff card"; ADR 0008).
export function DiffCard({ q, onClose }: { q: QueueItem; onClose: () => void }) {
  const [diff, setDiff] = useState<PRDiff | null>(null);
  const [error, setError] = useState<string | null>(null);
  // reload is bumped by Retry to re-run the fetch effect after a failure.
  const [reload, setReload] = useState(0);

  // Fetch the diff on open (and whenever the PR changes). The alive flag drops a
  // late response after the card is closed/reopened so it never lands on a stale
  // card.
  useEffect(() => {
    let alive = true;
    setDiff(null);
    setError(null);
    fetchDiff(q.number)
      .then((d) => {
        if (alive) setDiff(d);
      })
      .catch((e) => {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      alive = false;
    };
  }, [q.number, reload]);

  // Esc closes the card, matching the backdrop click and × button.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div
        className="modal diff-card"
        role="dialog"
        aria-label={`diff for #${q.number}`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h3 className="modal-title">
            Diff · <span className="tnum">#{q.number}</span>
          </h3>
          <div className="spacer" />
          <a
            className="diff-gh-link"
            href={q.url}
            target="_blank"
            rel="noreferrer"
          >
            Open on GitHub ↗
          </a>
          <button
            type="button"
            className="modal-close"
            aria-label="close"
            onClick={onClose}
          >
            ×
          </button>
        </div>

        <div className="modal-body">
          {error ? (
            <div className="diff-error">
              <p className="row-alert" role="alert">
                {error}
              </p>
              <button
                type="button"
                className="diff-retry"
                onClick={() => setReload((n) => n + 1)}
              >
                Retry
              </button>
            </div>
          ) : diff === null ? (
            <div className="card-loading">Loading diff…</div>
          ) : (
            <DiffFiles diff={diff} />
          )}
        </div>
      </div>
    </div>
  );
}

// DiffFiles renders the fetched changed files, one collapsible section each.
function DiffFiles({ diff }: { diff: PRDiff }) {
  const files = diff.files ?? [];
  const capped = files.length < diff.total_files;
  return (
    <div className="diff-file-list">
      {files.map((f) => (
        <FileSection key={f.filename} file={f} />
      ))}
      {capped && (
        <p className="diff-banner">
          showing first {files.length} of {diff.total_files} files
        </p>
      )}
    </div>
  );
}

// FileSection is one changed file: a clickable header (caret · path · +N −M)
// that toggles its patch. Files at or under EXPAND_THRESHOLD changed lines start
// expanded; larger ones start collapsed.
function FileSection({ file }: { file: FileDiff }) {
  const changed = file.additions + file.deletions;
  const [expanded, setExpanded] = useState(changed <= EXPAND_THRESHOLD);
  return (
    <section className="diff-file">
      <button
        type="button"
        className="diff-file-head"
        aria-expanded={expanded}
        onClick={() => setExpanded((v) => !v)}
      >
        <span className="diff-caret">{expanded ? "▾" : "▸"}</span>
        <span className="diff-file-name">{file.filename}</span>
        <span className="diff-add">+{file.additions}</span>
        <span className="diff-del">−{file.deletions}</span>
      </button>
      {expanded &&
        (file.patch ? (
          <Patch patch={file.patch} />
        ) : (
          <div className="diff-no-preview">
            no preview — binary or too large
          </div>
        ))}
    </section>
  );
}

// lineClass maps a unified-diff line to its role class by leading character:
// additions green, deletions red, hunk headers as dividers.
function lineClass(line: string): string {
  if (line.startsWith("+")) return "diff-line-add";
  if (line.startsWith("-")) return "diff-line-del";
  if (line.startsWith("@@")) return "diff-line-hunk";
  return "diff-line-ctx";
}

// Patch renders a file's unified-diff patch, one styled line each.
function Patch({ patch }: { patch: string }) {
  const lines = patch.split("\n");
  return (
    <pre className="diff-patch">
      {lines.map((line, i) => (
        <span key={i} className={`diff-line ${lineClass(line)}`}>
          {line}
        </span>
      ))}
    </pre>
  );
}
