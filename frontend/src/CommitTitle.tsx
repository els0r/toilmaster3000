import type { components } from "./api/schema";

// TitleParts is the server-parsed conventional-commit title (generated from the
// backend's TitleParts DTO). The backend is the single parser of record (ADR
// 0006); these components only RENDER the parts — they never parse a title.
export type TitleParts = components["schemas"]["TitleParts"];

// capitalize is a render-only transform of the first character. The parsed
// description keeps its raw lower-case convention; only the shown string is
// capitalized (ADR 0006), so the underlying data is never corrupted.
function capitalize(s: string): string {
  return s.length === 0 ? s : s.charAt(0).toUpperCase() + s.slice(1);
}

// IconSpec is one type glyph ported verbatim from the Claude Design source
// (toilmaster3000.dc.html): a list of 24×24 SVG paths and whether they're filled
// (feat/perf) or stroked (everything else).
type IconSpec = { fill: boolean; d: string[] };

// ICONS are the per-conventional-commit-type glyphs, transcribed 1:1 from the
// design source. The icon is the ONLY type signal once the "type(scope):" prefix
// is stripped from the shown title (ADR 0006).
const ICONS: Record<string, IconSpec> = {
  // feat: a filled star — a new capability.
  feat: { fill: true, d: ["M12 2 L14.5 9.5 L22 12 L14.5 14.5 L12 22 L9.5 14.5 L2 12 L9.5 9.5 Z"] },
  // fix: a wrench.
  fix: {
    fill: false,
    d: ["M8 9a4 4 0 0 1 8 0v4a4 4 0 0 1-8 0z", "M12 9v9", "M9 6 7.5 4", "M15 6 16.5 4", "M8 11H4", "M16 11h4", "M8 14.5H4.5", "M16 14.5h3.5"],
  },
  // chore: a wrench-and-screwdriver loop (maintenance).
  chore: {
    fill: false,
    d: ["M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"],
  },
  // docs: a document with lines.
  docs: { fill: false, d: ["M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z", "M14 2v6h6", "M8 13h8", "M8 17h6"] },
  // style: a paint brush.
  style: {
    fill: false,
    d: ["M9.06 11.9l8.07-8.06a2.85 2.85 0 1 1 4.03 4.03l-8.06 8.08", "M7.07 14.94c-1.66 0-3 1.35-3 3.02 0 1.33-2.5 1.52-2 2.02 1.08 1.1 2.49 2.02 4 2.02 2.2 0 4-1.8 4-4.04a3.01 3.01 0 0 0-3-3.02z"],
  },
  // refactor: a git-branch glyph.
  refactor: { fill: false, d: ["M6 3v12", "M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6z", "M6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6z", "M15 6a9 9 0 0 1-9 9"] },
  // perf: a filled lightning bolt.
  perf: { fill: true, d: ["M13 2 3 14 12 14 11 22 21 10 12 10z"] },
  // test: a flask.
  test: { fill: false, d: ["M9 2v6.6L3.5 18.4A1.4 1.4 0 0 0 4.7 20.5h14.6a1.4 1.4 0 0 0 1.2-2.1L15 8.6V2", "M9 2h6", "M7.5 14h9"] },
  // build: a 3D box (package).
  build: {
    fill: false,
    d: ["M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z", "M3.27 6.96 12 12.01l8.73-5.05", "M12 22.08V12"],
  },
  // ci: a refresh/pipeline cycle.
  ci: { fill: false, d: ["M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8", "M21 3v5h-5", "M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16", "M3 21v-5h5"] },
  // revert: an undo arrow.
  revert: { fill: false, d: ["M3 7v6h6", "M3 13a9 9 0 1 0 3-7.7L3 8"] },
  // commit: a node on a line — the generic fallback glyph.
  commit: { fill: false, d: ["M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z", "M3 12h6", "M15 12h6"] },
};

// typeColor mirrors the design's semantic coloring: feat/perf are blue (action),
// fix is green (success), every other type is muted (fg-3).
function typeColor(type: string): string {
  if (type === "feat" || type === "perf") return "var(--action)";
  if (type === "fix") return "var(--success)";
  return "var(--fg-3)";
}

// TypeIcon renders the SVG glyph for a conventional-commit type. An unknown or
// empty type falls back to the generic "commit" glyph (ADR 0006). Filled glyphs
// (feat/perf) paint their fill; the rest stroke. The icon sits in the row's left
// gutter, separate from the title line.
export function TypeIcon({ type }: { type: string }) {
  const key = type in ICONS ? type : "commit";
  const spec = ICONS[key];
  return (
    <svg
      className="type-icon"
      viewBox="0 0 24 24"
      width="15"
      height="15"
      role="img"
      aria-label={key}
      style={{ color: typeColor(type) }}
    >
      {spec.d.map((d, i) => (
        <path
          key={i}
          d={d}
          fill={spec.fill ? "currentColor" : "none"}
          stroke={spec.fill ? "none" : "currentColor"}
          strokeWidth={1.7}
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      ))}
    </svg>
  );
}

// CommitTitle renders a PR title line from its server-parsed parts: the
// capitalized clean description as the primary link, a SEPARATE muted "#num ↗"
// link, then the scope pills. The type icon is rendered by the row (in its left
// gutter), not here. A non-conventional title (empty parsed type) falls back to
// the capitalized raw title — the frontend never parses (ADR 0006). The title
// scale is set by the enclosing PrRow's density, not a per-caller class
// (ADR 0014), so this renderer carries no size prop.
export function CommitTitle({
  parts,
  rawTitle,
  number,
  url,
}: {
  parts: TitleParts;
  rawTitle: string;
  number: number;
  url: string;
}) {
  const parsed = parts.type !== "";
  const scopes = parts.scopes ?? [];
  return (
    <span className="commit-title">
      <a
        className="entry-link"
        href={url}
        target="_blank"
        rel="noreferrer"
      >
        {capitalize(parsed ? parts.description : rawTitle)}
      </a>
      <a
        className="entry-num tnum"
        href={url}
        target="_blank"
        rel="noreferrer"
      >
        #{number}
        <span className="arrow">↗</span>
      </a>
      {parsed &&
        scopes.map((scope) => (
          <span key={scope} className="scope-pill">
            {scope}
          </span>
        ))}
    </span>
  );
}
