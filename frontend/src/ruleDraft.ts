import type { Rule } from "./api";

// ruleDraft owns the Rule Draft: the editable projection of a wire Rule while it
// sits in the editor modal. Every predicate is held as the raw text the user
// types (authors as a comma-separated string, each title part as a regex string,
// the diff bounds as numeric-input strings; "" ⇒ unconstrained). This module is
// the single definition of the predicate vocabulary — the modal rows, the
// validation, the constrains-nothing guard, the Draft↔Rule round-trip, and the
// row summary all read from here rather than each re-listing the fields. Pure;
// no React.

// RuleClass discriminates the two Rule classes. It is implied by the card a rule
// lives in — never an editable field.
export type RuleClass = "approve" | "review";

// FIELD_KEYS enumerates every predicate field's draft-side key, in modal order.
// This is the one place the vocabulary is listed; blankDraft and the
// constrains-nothing guard iterate it instead of restating the field set.
export const FIELD_KEYS = [
  "authorInc",
  "authorExc",
  "typeInc",
  "typeExc",
  "scopeInc",
  "scopeExc",
  "descInc",
  "descExc",
  "diffMin",
  "diffMax",
] as const;
export type FieldKey = (typeof FIELD_KEYS)[number];

// TitlePartWireKey is the set of wire fields the six title-part regexes map onto
// — all string-typed, so a dynamic assignment stays type-safe.
type TitlePartWireKey =
  | "type_include"
  | "type_exclude"
  | "scope_include"
  | "scope_exclude"
  | "description_include"
  | "description_exclude";

// TitlePartField describes one title-part regex field. The six rows are the
// homogeneous core of the vocabulary: identical JSX, identical regex validation,
// identical summary shape. Each row carries both its draft-side keys and its
// wire-side field names, so the modal, validation, round-trip, and summary all
// drive off this one table instead of restating the field set.
export type TitlePartField = {
  part: "type" | "scope" | "description";
  includeKey: FieldKey;
  excludeKey: FieldKey;
  wireInclude: TitlePartWireKey;
  wireExclude: TitlePartWireKey;
  // Abbreviated label used in the row summary line (description ⇒ "desc").
  summaryLabel: string;
  // Modal input placeholders (include / exclude).
  placeholderInc: string;
  placeholderExc: string;
};

export const TITLE_PART_FIELDS: TitlePartField[] = [
  {
    part: "type",
    includeKey: "typeInc",
    excludeKey: "typeExc",
    wireInclude: "type_include",
    wireExclude: "type_exclude",
    summaryLabel: "type",
    placeholderInc: "^(fix|chore)$",
    placeholderExc: "(none)",
  },
  {
    part: "scope",
    includeKey: "scopeInc",
    excludeKey: "scopeExc",
    wireInclude: "scope_include",
    wireExclude: "scope_exclude",
    summaryLabel: "scope",
    placeholderInc: "^deps(-dev)?$",
    placeholderExc: "^renovate$",
  },
  {
    part: "description",
    includeKey: "descInc",
    excludeKey: "descExc",
    wireInclude: "description_include",
    wireExclude: "description_exclude",
    summaryLabel: "desc",
    placeholderInc: "(any)",
    placeholderExc: "(none)",
  },
];

// Draft is the modal's editable shape: the meta fields plus one raw-text slot per
// predicate field. `cls` is stamped from the card (no class selector in the
// modal). Keeping the field slots as one Record<FieldKey, string> lets the
// constructors and guards iterate the vocabulary rather than name each field.
export type Draft = {
  id?: string;
  name: string;
  enabled: boolean;
  isNew: boolean;
  cls: RuleClass;
} & Record<FieldKey, string>;

// blankDraft makes a fresh draft stamped with the card's class — the only place
// a rule's class is set (the modal has no class selector). Every field starts
// empty (unconstrained).
export function blankDraft(cls: RuleClass): Draft {
  const fields = Object.fromEntries(
    FIELD_KEYS.map((k) => [k, ""]),
  ) as Record<FieldKey, string>;
  return { name: "", enabled: true, isNew: true, cls, ...fields };
}

// ruleToDraft loads a wire Rule into the modal's editable text. Its class comes
// from the rule itself (empty/absent ⇒ the card's class), so an edit can never
// reclassify it — class stays card-implied and immutable. Every predicate is
// surfaced as text; nothing is carried through invisibly.
export function ruleToDraft(r: Rule, cls: RuleClass): Draft {
  // Build every field slot as text in one map, then spread once — authors and
  // diff up front, the six title parts filled from the table.
  const fields = {
    authorInc: (r.authors_include ?? []).join(", "),
    authorExc: (r.authors_exclude ?? []).join(", "),
    // 0 or absent ⇒ unconstrained ⇒ empty input.
    diffMin: r.diff_min ? String(r.diff_min) : "",
    diffMax: r.diff_max ? String(r.diff_max) : "",
  } as Record<FieldKey, string>;
  for (const f of TITLE_PART_FIELDS) {
    fields[f.includeKey] = (r[f.wireInclude] as string | undefined) ?? "";
    fields[f.excludeKey] = (r[f.wireExclude] as string | undefined) ?? "";
  }
  return {
    id: r.id,
    name: r.name,
    // enabled is omitempty on the wire, so a disabled rule arrives with the field
    // absent; absent means false.
    enabled: r.enabled ?? false,
    isNew: false,
    cls: r.class === "review" || r.class === "approve" ? r.class : cls,
    ...fields,
  };
}

// draftToRule maps the modal back onto the wire shape, dropping empty predicates
// and stamping the card-implied class. Every predicate is exposed in the modal,
// so the inputs are the sole source of truth — nothing is carried through from a
// hidden original.
export function draftToRule(d: Draft): Rule {
  const rule: Rule = {
    name: d.name.trim() || "Untitled rule",
    enabled: d.enabled,
    class: d.cls,
  };
  // id is response-only (readOnly in the schema): the server generates it on
  // create and reads it from the path on update, so it is never sent in the body.

  // Authors: the include/exclude lists come from comma-separated strings; send
  // each only when it yields at least one entry.
  const inc = splitList(d.authorInc);
  const exc = splitList(d.authorExc);
  if (inc.length) rule.authors_include = inc;
  if (exc.length) rule.authors_exclude = exc;

  // Each title-part include/exclude is sent only when non-empty, sourced from the
  // input (no carry-through from a hidden original).
  for (const f of TITLE_PART_FIELDS) {
    const inc = d[f.includeKey].trim();
    const exc = d[f.excludeKey].trim();
    if (inc) rule[f.wireInclude] = inc;
    if (exc) rule[f.wireExclude] = exc;
  }

  // Diff bounds come from the inputs: send each only when set (> 0), omit when
  // unconstrained — no spurious 0 on the wire.
  const dmin = parseDiff(d.diffMin);
  const dmax = parseDiff(d.diffMax);
  if (dmin > 0) rule.diff_min = dmin;
  if (dmax > 0) rule.diff_max = dmax;

  return rule;
}

// parseDiff reads a numeric-input string into a non-negative int; empty, NaN, or
// negative all collapse to 0 (unconstrained), matching the backend idiom.
function parseDiff(s: string): number {
  const n = parseInt(s.trim(), 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

// DraftValidation is the verdict the modal reads to gate Save and render inline
// errors: which title-part fields hold an invalid regex, whether the diff range
// is inverted, and whether the rule constrains nothing (would match every PR).
export type DraftValidation = {
  // Keyed by the offending title-part field's draft key; only the six title-part
  // fields are regex-checked (authors are validated server-side).
  regexErrors: Partial<Record<FieldKey, boolean>>;
  invertedRange: boolean;
  constrainsNothing: boolean;
};

// validateDraft computes the modal's save-gate verdict from the draft's raw text
// in one pass, so the modal stops recomputing scattered checks inline.
export function validateDraft(d: Draft): DraftValidation {
  // Inverted range mirrors the server's ErrInvalidDiffRange: invalid only when
  // BOTH bounds are non-zero and min > max. min == max and one-sided bounds are
  // fine.
  const diffMin = parseDiff(d.diffMin);
  const diffMax = parseDiff(d.diffMax);
  const invertedRange = diffMin > 0 && diffMax > 0 && diffMin > diffMax;

  // Only the six title-part regexes are validated on the client; authors are
  // checked server-side. Each is flagged under its own field key.
  const regexErrors: Partial<Record<FieldKey, boolean>> = {};
  for (const f of TITLE_PART_FIELDS) {
    regexErrors[f.includeKey] = badRe(d[f.includeKey].trim());
    regexErrors[f.excludeKey] = badRe(d[f.excludeKey].trim());
  }

  // "Constrains nothing" is per-kind, not a uniform "string is empty": authors
  // count when they yield an entry, title parts when trimmed-non-empty, and a
  // diff bound only when it parses to a positive int ("0" ⇒ unconstrained).
  const constrainsNothing =
    splitList(d.authorInc).length === 0 &&
    splitList(d.authorExc).length === 0 &&
    TITLE_PART_FIELDS.every(
      (f) => !d[f.includeKey].trim() && !d[f.excludeKey].trim(),
    ) &&
    diffMin === 0 &&
    diffMax === 0;

  return { regexErrors, invertedRange, constrainsNothing };
}

// ruleRegexError names the first title-part field of a stored rule whose regex
// no longer compiles, for the row's inline warning (e.g. after a hand-edited
// rules.yaml). Every include is checked before any exclude — the historical
// first-match order. Returns null when every part is valid.
export function ruleRegexError(r: Rule): string | null {
  for (const f of TITLE_PART_FIELDS) {
    if (badRe((r[f.wireInclude] as string | undefined) ?? "")) {
      return f.wireInclude;
    }
  }
  for (const f of TITLE_PART_FIELDS) {
    if (badRe((r[f.wireExclude] as string | undefined) ?? "")) {
      return f.wireExclude;
    }
  }
  return null;
}

// summarize renders a stored rule's predicate set as a single readable line —
// authors, then each title part (include then exclude), then the diff bound. An
// unconstrained rule reads as matching everything.
export function summarize(r: Rule): string {
  const parts: string[] = [];
  if (r.authors_include?.length) {
    parts.push(`author ∈ {${r.authors_include.join(", ")}}`);
  }
  if (r.authors_exclude?.length) {
    parts.push(`author ∉ {${r.authors_exclude.join(", ")}}`);
  }
  for (const f of TITLE_PART_FIELDS) {
    const inc = r[f.wireInclude] as string | undefined;
    const exc = r[f.wireExclude] as string | undefined;
    if (inc) parts.push(`${f.summaryLabel} ${inc}`);
    if (exc) parts.push(`${f.summaryLabel} ∉ ${exc}`);
  }
  const diff = summarizeDiff(r.diff_min ?? 0, r.diff_max ?? 0);
  if (diff) parts.push(diff);
  return parts.length ? parts.join("   ·   ") : "matches every PR";
}

// summarizeDiff renders the diff-size bound as a readable clause (0 ⇒
// unconstrained on each side): `diff 10–500` for both, `diff ≥ 10` for min-only,
// `diff ≤ 500` for max-only, "" when unconstrained.
function summarizeDiff(min: number, max: number): string {
  if (min > 0 && max > 0) return `diff ${min}–${max}`;
  if (min > 0) return `diff ≥ ${min}`;
  if (max > 0) return `diff ≤ ${max}`;
  return "";
}

// badRe reports whether a non-empty string fails to compile as a regex. Empty is
// "unconstrained", not invalid.
function badRe(s: string): boolean {
  if (!s) return false;
  try {
    new RegExp(s);
    return false;
  } catch {
    return true;
  }
}

// splitList parses a comma-separated input into trimmed, non-empty entries.
function splitList(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}
