import { describe, it, expect } from "vitest";
import {
  blankDraft,
  draftToRule,
  ruleToDraft,
  validateDraft,
  ruleRegexError,
  summarize,
  type Draft,
} from "./ruleDraft";
import type { Rule } from "./api";

const rule = (over: Partial<Rule> = {}): Rule => ({
  name: "r",
  enabled: true,
  ...over,
});

// ruleDraft is the pure (no-React) module that owns the Rule Draft: the editable
// projection of a wire Rule while it sits in the editor modal. These tests are
// the module's direct surface — the same logic was once reachable only through
// the rendered modal.

describe("draftToRule", () => {
  // Slice 1 (tracer): a blank draft carries no predicates, so it maps to a rule
  // with just the name and the card-stamped class — every optional predicate is
  // omitted, never sent as an empty/zero value.
  it("maps a blank draft to a rule with only name and class", () => {
    expect(draftToRule(blankDraft("approve"))).toEqual({
      name: "Untitled rule",
      enabled: true,
      class: "approve",
    });
  });

  // Slice 2: each non-empty title-part field rides the wire (trimmed); each empty
  // one is omitted. Include and exclude are independent per part.
  it("sends trimmed title-part regexes and omits the empty ones", () => {
    const d: Draft = {
      ...blankDraft("approve"),
      name: "t",
      typeInc: "  ^chore$  ",
      scopeExc: "^renovate$",
    };
    const r = draftToRule(d);
    expect(r.type_include).toBe("^chore$");
    expect(r.scope_exclude).toBe("^renovate$");
    expect(r.type_exclude).toBeUndefined();
    expect(r.scope_include).toBeUndefined();
    expect(r.description_include).toBeUndefined();
    expect(r.description_exclude).toBeUndefined();
  });

  // Slice 3: the author lists come from a comma-separated string — split,
  // trimmed, blanks dropped. An empty author field is omitted entirely.
  it("splits the author lists and omits the empty one", () => {
    const d: Draft = {
      ...blankDraft("approve"),
      name: "a",
      authorInc: "dependabot[bot], k-tanaka, ",
    };
    const r = draftToRule(d);
    expect(r.authors_include).toEqual(["dependabot[bot]", "k-tanaka"]);
    expect(r.authors_exclude).toBeUndefined();
  });

  // Slice 4: a diff bound rides the wire only when it is a positive int; empty
  // and "0" both mean unconstrained and are omitted (never a spurious 0).
  it("sends positive diff bounds and omits 0 or empty", () => {
    const set = draftToRule({
      ...blankDraft("approve"),
      name: "d",
      diffMin: "10",
      diffMax: "500",
    });
    expect(set).toMatchObject({ diff_min: 10, diff_max: 500 });

    const unconstrained = draftToRule({
      ...blankDraft("approve"),
      name: "z",
      diffMin: "0",
      diffMax: "",
    });
    expect(unconstrained.diff_min).toBeUndefined();
    expect(unconstrained.diff_max).toBeUndefined();
  });
});

describe("ruleToDraft", () => {
  // Slice 5: a fully-populated wire Rule survives a round-trip through the Draft
  // and back unchanged (id aside, which is response-only). This proves all three
  // kinds — authors, the six title parts, diff bounds — load into the editable
  // text and map back without loss.
  it("round-trips a fully-populated rule through the draft and back", () => {
    const r: Rule = {
      name: "team chores",
      enabled: true,
      class: "review",
      authors_include: ["teammate_a"],
      authors_exclude: ["@me"],
      type_include: "^chore$",
      type_exclude: "^revert$",
      scope_include: "service-a",
      scope_exclude: "renovate",
      description_include: "wip",
      description_exclude: "skip",
      diff_min: 10,
      diff_max: 500,
    };
    expect(draftToRule(ruleToDraft(r, "approve"))).toEqual(r);
  });

  // A rule with absent class inherits the card's class (empty/absent ⇒ the card),
  // so an edit can never reclassify it.
  it("falls back to the card's class when the rule has none", () => {
    const r: Rule = { name: "seed", enabled: true, type_include: "^chore$" };
    expect(draftToRule(ruleToDraft(r, "review")).class).toBe("review");
  });
});

describe("validateDraft — constrainsNothing", () => {
  // Slice 6: a blank draft constrains nothing (it would match every PR), so the
  // guard fires.
  it("flags a blank draft", () => {
    expect(validateDraft(blankDraft("approve")).constrainsNothing).toBe(true);
  });

  // The subtle case: "0" in a diff input is a non-empty string that still means
  // unconstrained, so a draft with only diff "0" bounds still constrains nothing.
  it("treats diff \"0\" as unconstrained", () => {
    const d: Draft = { ...blankDraft("approve"), diffMin: "0", diffMax: "0" };
    expect(validateDraft(d).constrainsNothing).toBe(true);
  });

  // A single author entry is enough to constrain the rule — the guard clears.
  it("clears once any one field constrains", () => {
    const d: Draft = { ...blankDraft("approve"), authorInc: "k-tanaka" };
    expect(validateDraft(d).constrainsNothing).toBe(false);
  });
});

describe("validateDraft — invertedRange", () => {
  const withDiff = (min: string, max: string): Draft => ({
    ...blankDraft("approve"),
    diffMin: min,
    diffMax: max,
  });

  // Slice 7: an inverted range (both bounds non-zero, min > max) is invalid —
  // mirrors the server's ErrInvalidDiffRange.
  it("flags both-non-zero min > max", () => {
    expect(validateDraft(withDiff("500", "10")).invertedRange).toBe(true);
  });

  // An equal range (min == max) is a valid single-value bound.
  it("accepts min == max", () => {
    expect(validateDraft(withDiff("100", "100")).invertedRange).toBe(false);
  });

  // A one-sided bound can't be inverted — the other side is unconstrained.
  it("accepts a one-sided bound", () => {
    expect(validateDraft(withDiff("500", "")).invertedRange).toBe(false);
  });
});

describe("validateDraft — regexErrors", () => {
  // Slice 8: an invalid regex in a title-part field is flagged under that field's
  // key, so the modal can mark the offending input.
  it("flags an invalid title-part regex by field key", () => {
    const v = validateDraft({ ...blankDraft("approve"), scopeExc: "([a-z" });
    expect(v.regexErrors.scopeExc).toBe(true);
    expect(v.regexErrors.typeInc).toBeFalsy();
  });

  // Authors are NOT regex-checked on the client (the server validates them); a
  // malformed author pattern raises no client-side regex error.
  it("does not regex-validate author fields", () => {
    const v = validateDraft({ ...blankDraft("approve"), authorInc: "([a-z" });
    expect(v.regexErrors.authorInc).toBeFalsy();
    expect(Object.values(v.regexErrors).some(Boolean)).toBe(false);
  });
});

describe("ruleRegexError", () => {
  // Slice 9: a stored rule (e.g. after a hand-edited rules.yaml) whose title-part
  // regex no longer compiles surfaces the offending wire field name.
  it("names the bad title-part field", () => {
    expect(ruleRegexError(rule({ scope_include: "([a-z" }))).toBe(
      "scope_include",
    );
  });

  // First-match order: all includes are checked before any exclude, so an
  // include wins over a simultaneously-bad exclude.
  it("checks every include before any exclude", () => {
    const r = rule({ description_include: "([a-z", type_exclude: "([a-z" });
    expect(ruleRegexError(r)).toBe("description_include");
  });

  // A rule whose every regex compiles has no error.
  it("returns null when all regexes compile", () => {
    expect(ruleRegexError(rule({ type_include: "^chore$" }))).toBeNull();
  });
});

describe("summarize", () => {
  // Slice 10: the summary line renders a clause per constraining field across all
  // three kinds (note the abbreviated "desc" label for description).
  it("renders a clause for each kind", () => {
    const s = summarize(
      rule({
        authors_include: ["k-tanaka"],
        authors_exclude: ["@me"],
        type_include: "^chore$",
        scope_exclude: "renovate",
        description_include: "wip",
        diff_min: 10,
        diff_max: 500,
      }),
    );
    expect(s).toContain("author ∈ {k-tanaka}");
    expect(s).toContain("author ∉ {@me}");
    expect(s).toContain("type ^chore$");
    expect(s).toContain("scope ∉ renovate");
    expect(s).toContain("desc wip");
    expect(s).toContain("diff 10–500");
  });

  // The diff clause has three forms: both-sided, min-only, max-only.
  it("renders the one-sided diff variants", () => {
    expect(summarize(rule({ diff_min: 10 }))).toContain("diff ≥ 10");
    expect(summarize(rule({ diff_max: 500 }))).toContain("diff ≤ 500");
  });

  // A rule that constrains nothing reads as matching everything.
  it("falls back to 'matches every PR'", () => {
    expect(summarize(rule())).toBe("matches every PR");
  });
});
