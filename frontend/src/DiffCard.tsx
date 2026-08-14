import { useEffect, useState } from "react";
import { fetchDiff, type FileDiff, type PRDiff } from "./api";

// EXPAND_THRESHOLD is the changed-line count (additions + deletions) at or below
// which a file starts expanded; larger files start collapsed so a giant file
// doesn't blow out the card (CONTEXT "Diff card").
const EXPAND_THRESHOLD = 40;

// Result is one settled fetch tagged with the request that asked for it: the
// PR number plus the retry counter. Keeping the tag on the value is what lets
// the card tell "not fetched yet" from "fetched, but for a PR we have since
// left" without resetting state from inside the effect.
type Result = { request: string; diff: PRDiff | null; error: string | null };

// DiffCard is the pop-up that opens when a PR's Diff pill is clicked. It
// fetches the PR's changed files on demand and renders them per-file so a human
// can skim the change without leaving tm3k. It is a skim aid, NOT a GitHub
// mirror: it shows at most one page of files (a "first N of M" banner past the
// cap), renders no preview for binary/over-large files, and always carries an
// Open-on-GitHub escape hatch (CONTEXT "Diff card"; ADR 0008). Only `number`
// (the fetch key) and `url` (the escape hatch) are needed, so any station's
// item satisfies this structurally — DiffPill is the only caller.
export function DiffCard({
  q,
  onClose,
}: {
  q: { number: number; url: string };
  onClose: () => void;
}) {
  // reload is bumped by Retry to re-run the fetch effect after a failure.
  const [reload, setReload] = useState(0);
  const [result, setResult] = useState<Result | null>(null);
  // request identifies the fetch the card is currently showing. A result is
  // rendered only while it still carries the current request, so a PR change or
  // a Retry drops back to the loading state by comparison at render time —
  // no clearing setState inside the effect, and no cascading render.
  const request = `${q.number}:${reload}`;
  const current = result?.request === request ? result : null;
  const diff = current?.diff ?? null;
  const error = current?.error ?? null;

  // Fetch the diff on open (and whenever the PR changes). The alive flag drops a
  // late response after the card is closed/reopened so it never lands on a stale
  // card.
  useEffect(() => {
    let alive = true;
    fetchDiff(q.number)
      .then((d) => {
        if (alive) setResult({ request, diff: d, error: null });
      })
      .catch((e) => {
        if (alive)
          setResult({
            request,
            diff: null,
            error: e instanceof Error ? e.message : String(e),
          });
      });
    return () => {
      alive = false;
    };
  }, [q.number, request]);

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
