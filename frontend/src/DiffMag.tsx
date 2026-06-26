// DiffMag is the diff-magnitude leaf: a PR's change size as green-bold
// +additions, red-bold −deletions (a real U+2212 minus), and a muted K-files
// count (suppressed when changed_files is 0). It is the single owner of the
// +add/−del/files convention; the wrapper — a clickable .diff-pill (the queue)
// or a bare .diff-mag span (Staging) — and any onClick stay the caller's concern
// (ADR 0014, Candidate C). FunnelItem and QueueItem both satisfy the field view.
export function DiffMag({
  item,
}: {
  item: { additions: number; deletions: number; changed_files: number };
}) {
  return (
    <>
      <span className="diff-add">+{item.additions}</span>
      <span className="diff-del">−{item.deletions}</span>
      {item.changed_files > 0 && (
        <span className="diff-files">{item.changed_files} files</span>
      )}
    </>
  );
}
