import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  within,
} from "@testing-library/react";
import { RulesSection } from "./RulesEditor";
import type { Rule } from "./api";

vi.mock("./api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api")>();
  return {
    ...actual,
    fetchRules: vi.fn(),
    createRule: vi.fn(),
    updateRule: vi.fn(),
    deleteRule: vi.fn(),
  };
});

import { fetchRules, createRule, updateRule, deleteRule } from "./api";
const mockFetch = vi.mocked(fetchRules);
const mockCreate = vi.mocked(createRule);
const mockUpdate = vi.mocked(updateRule);
const mockDelete = vi.mocked(deleteRule);

const rule = (over: Partial<Rule> = {}): Rule => ({
  id: "r1",
  name: "team chores",
  enabled: true,
  type_include: "^chore$",
  ...over,
});

// The Approve Rules card and the Human Review Always card are both rendered by
// RulesSection. Tests scope their queries to one card via its heading so the
// two identical editors never collide.
const approveCard = () =>
  screen.getByRole("heading", { name: "Approve Rules" }).closest("section")!;
const reviewCard = () =>
  screen
    .getByRole("heading", { name: "Human Review Always" })
    .closest("section")!;

beforeEach(() => {
  mockFetch.mockReset();
  mockCreate.mockReset();
  mockUpdate.mockReset();
  mockDelete.mockReset();
  mockCreate.mockResolvedValue(rule());
  mockUpdate.mockResolvedValue(rule());
  mockDelete.mockResolvedValue();
});

describe("RulesSection", () => {
  // F-rules-1: the list renders the fetched rules in the Approve card.
  it("lists rules from the initial fetch", async () => {
    mockFetch.mockResolvedValue([rule({ id: "r1", name: "team chores" })]);
    render(<RulesSection />);

    expect(await screen.findByText("team chores")).toBeInTheDocument();
    // One shared fetch feeds both cards.
    expect(mockFetch).toHaveBeenCalledTimes(1);
  });

  // F-rules-split: a payload carrying both classes splits into the two cards by
  // `class` (empty/absent class ⇒ approve).
  it("splits rules into the two cards by class", async () => {
    mockFetch.mockResolvedValue([
      rule({ id: "a1", name: "team chores" }), // absent class ⇒ approve
      rule({ id: "a2", name: "service-a", class: "approve" }),
      rule({ id: "v1", name: "osixpatch gate", class: "review" }),
    ]);
    render(<RulesSection />);
    await screen.findByText("team chores");

    expect(within(approveCard()).getByText("team chores")).toBeInTheDocument();
    expect(within(approveCard()).getByText("service-a")).toBeInTheDocument();
    expect(within(approveCard()).queryByText("osixpatch gate")).toBeNull();

    expect(within(reviewCard()).getByText("osixpatch gate")).toBeInTheDocument();
    expect(within(reviewCard()).queryByText("team chores")).toBeNull();
  });

  // F-rules-2 (approve card): creating a rule calls createRule with the
  // approve class and refetches.
  it("creates an Approve Rule stamped class=approve and refetches", async () => {
    mockFetch.mockResolvedValueOnce([]); // initial: empty
    mockFetch.mockResolvedValueOnce([rule({ name: "new rule" })]); // after create
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    const card = approveCard();
    fireEvent.click(within(card).getByRole("button", { name: "+ New rule" }));
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "new rule" },
    });
    fireEvent.change(screen.getByLabelText("title type"), {
      target: { value: "^feat$" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      name: "new rule",
      type_include: "^feat$",
      class: "approve",
    });
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });

  // F-rules-stamp-review: the Human Review Always card stamps class=review on
  // the draft it creates.
  it("creates a Review Rule stamped class=review and refetches", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "gate", class: "review" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    const card = reviewCard();
    fireEvent.click(within(card).getByRole("button", { name: "+ New rule" }));
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "gate" },
    });
    fireEvent.change(screen.getByLabelText("title scope"), {
      target: { value: "osixpatch" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      name: "gate",
      scope_include: "osixpatch",
      class: "review",
    });
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });

  // F-rules-modal-identical: the modal has no class field/selector in either
  // card.
  it("renders an identical modal with no class field", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(reviewCard()).getByRole("button", { name: "+ New rule" }),
    );
    // No control labelled "class" exists in the modal.
    expect(screen.queryByLabelText(/class/i)).toBeNull();
    // The modal exposes exactly the slice-4 fields, none of them a class
    // selector.
    expect(screen.getByLabelText("rule name")).toBeInTheDocument();
    expect(screen.getByLabelText("title type")).toBeInTheDocument();
  });

  // F-rules-3: toggling Enabled issues a full PUT with the flag flipped, then
  // refetches. (Review card.)
  it("toggles a Review Rule's Enabled via a full PUT and refetches", async () => {
    mockFetch.mockResolvedValue([
      rule({ id: "v1", name: "gate", enabled: true, class: "review" }),
    ]);
    render(<RulesSection />);
    await screen.findByText("gate");

    fireEvent.click(within(reviewCard()).getByLabelText("toggle gate"));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    expect(mockUpdate.mock.calls[0][0]).toBe("v1");
    expect(mockUpdate.mock.calls[0][1]).toMatchObject({
      enabled: false,
      class: "review",
    });
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });

  // F-rules-4: editing a rule's name saves a full PUT and refetches; the edit
  // carries through the rule's class and its diff bounds (not exposed in the
  // modal — slice 5 — but must not be silently dropped).
  it("edits a rule, preserving class and diff bounds", async () => {
    mockFetch.mockResolvedValue([
      rule({
        id: "v1",
        name: "gate",
        class: "review",
        diff_min: 10,
        diff_max: 500,
      }),
    ]);
    render(<RulesSection />);
    await screen.findByText("gate");

    fireEvent.click(within(reviewCard()).getByLabelText("edit gate"));
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "renamed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    expect(mockUpdate.mock.calls[0][0]).toBe("v1");
    expect(mockUpdate.mock.calls[0][1]).toMatchObject({
      name: "renamed",
      class: "review",
      diff_min: 10,
      diff_max: 500,
    });
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });

  // F-rules-5: deleting a rule from its row calls deleteRule and refetches.
  it("deletes a rule and refetches", async () => {
    mockFetch.mockResolvedValue([rule({ id: "r1", name: "team chores" })]);
    render(<RulesSection />);
    await screen.findByText("team chores");

    fireEvent.click(within(approveCard()).getByLabelText("delete team chores"));

    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("r1"));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });

  // F-rules-6: a server validation error is surfaced to the user, not
  // swallowed.
  it("surfaces a server validation error on create", async () => {
    mockFetch.mockResolvedValue([]);
    mockCreate.mockRejectedValue(
      new Error("invalid regex in scope_include: missing )"),
    );
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "bad" },
    });
    fireEvent.change(screen.getByLabelText("title scope"), {
      target: { value: "^deps$" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /invalid regex in scope_include/,
    );
  });

  // F-rules-7: the modal blocks Save while a title regex is invalid.
  it("disables Save while a title regex is invalid", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("title scope"), {
      target: { value: "([a-z" },
    });

    expect(screen.getByRole("button", { name: "Save rule" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));
    expect(mockCreate).not.toHaveBeenCalled();
  });

  // --- Slice 5: full predicate vocabulary (include + exclude per title part) ---

  // F-rules-exclude-inputs: the modal exposes an exclude input beside each
  // title-part include (type, scope, description).
  it("exposes exclude inputs for each title part", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );

    expect(screen.getByLabelText("title type exclude")).toBeInTheDocument();
    expect(screen.getByLabelText("title scope exclude")).toBeInTheDocument();
    expect(
      screen.getByLabelText("title description exclude"),
    ).toBeInTheDocument();
  });

  // F-rules-exclude-create: a created rule round-trips the include AND exclude
  // regex for every title part, sourced from the inputs (no original).
  it("round-trips type/scope/description excludes on create", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "excludes" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "excludes" },
    });
    fireEvent.change(screen.getByLabelText("title type"), {
      target: { value: "^chore$" },
    });
    fireEvent.change(screen.getByLabelText("title type exclude"), {
      target: { value: "^revert$" },
    });
    fireEvent.change(screen.getByLabelText("title scope exclude"), {
      target: { value: "^renovate$" },
    });
    fireEvent.change(screen.getByLabelText("title description exclude"), {
      target: { value: "wip" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      name: "excludes",
      class: "approve",
      type_include: "^chore$",
      type_exclude: "^revert$",
      scope_exclude: "^renovate$",
      description_exclude: "wip",
    });
  });

  // F-rules-exclude-prefill: editing the `team chores` seed surfaces its
  // previously-invisible scope_exclude (renovate) in an editable input, and a
  // changed value round-trips on save.
  it("shows and can change the team chores scope_exclude", async () => {
    mockFetch.mockResolvedValue([
      rule({
        id: "r1",
        name: "team chores",
        type_include: "^chore$",
        scope_exclude: "renovate",
      }),
    ]);
    render(<RulesSection />);
    await screen.findByText("team chores");

    fireEvent.click(within(approveCard()).getByLabelText("edit team chores"));

    const scopeExc = screen.getByLabelText("title scope exclude");
    expect(scopeExc).toHaveValue("renovate");

    fireEvent.change(scopeExc, { target: { value: "renovate|dependabot" } });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    expect(mockUpdate.mock.calls[0][1]).toMatchObject({
      scope_exclude: "renovate|dependabot",
    });
  });

  // F-rules-exclude-clear: clearing a title-part exclude on edit drops it from
  // the wire — the carry-through hack is gone, so a hidden exclude can no longer
  // resurrect itself.
  it("drops a cleared exclude on save (no carry-through)", async () => {
    mockFetch.mockResolvedValue([
      rule({
        id: "r1",
        name: "team chores",
        type_include: "^chore$",
        scope_exclude: "renovate",
      }),
    ]);
    render(<RulesSection />);
    await screen.findByText("team chores");

    fireEvent.click(within(approveCard()).getByLabelText("edit team chores"));
    fireEvent.change(screen.getByLabelText("title scope exclude"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    expect(mockUpdate.mock.calls[0][1].scope_exclude).toBeUndefined();
  });

  // F-rules-exclude-regex: an invalid regex in a title-part EXCLUDE blocks Save
  // and shows the inline error, same as the include fields.
  it("disables Save while a title-part exclude regex is invalid", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "bad exclude" },
    });
    // A valid include keeps the rule from being blocked by the constrains-
    // nothing guard, so the only thing that can disable Save is the bad exclude.
    fireEvent.change(screen.getByLabelText("title type"), {
      target: { value: "^chore$" },
    });
    const save = screen.getByRole("button", { name: "Save rule" });
    expect(save).not.toBeDisabled();

    fireEvent.change(screen.getByLabelText("title scope exclude"), {
      target: { value: "([a-z" },
    });
    expect(save).toBeDisabled();
    expect(screen.getByText("invalid regex")).toBeInTheDocument();
    fireEvent.click(save);
    expect(mockCreate).not.toHaveBeenCalled();
  });

  // F-rules-exclude-only: a rule constraining ONLY a title-part exclude is not
  // blocked by the constrains-nothing guard (mirrors the backend Validate, which
  // counts excludes toward non-empty), and round-trips on save.
  it("allows saving an exclude-only rule", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "no renovate" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "no renovate" },
    });
    fireEvent.change(screen.getByLabelText("title scope exclude"), {
      target: { value: "^renovate$" },
    });

    expect(screen.queryByText(/constrains nothing/i)).toBeNull();
    const save = screen.getByRole("button", { name: "Save rule" });
    expect(save).not.toBeDisabled();

    fireEvent.click(save);
    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      scope_exclude: "^renovate$",
    });
  });

  // F-rules-fresh-approve: a brand-new Approve Rule carries class=approve and
  // sends no diff bounds (no original to carry through).
  it("creates a fresh Approve Rule with no diff bounds dropped", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "fresh" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "fresh" },
    });
    fireEvent.change(screen.getByLabelText("title type"), {
      target: { value: "^chore$" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    const sent = mockCreate.mock.calls[0][0];
    expect(sent).toMatchObject({ name: "fresh", class: "approve" });
    expect(sent.diff_min).toBeUndefined();
    expect(sent.diff_max).toBeUndefined();
  });

  // F-rules-diff-inputs-order: the modal exposes two numeric diff inputs, and
  // they appear after the conventional-commit group (visible while editing).
  it("renders two numeric diff inputs after the conventional-commit group", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );

    const min = screen.getByLabelText("diff min");
    const max = screen.getByLabelText("diff max");
    expect(min).toBeInTheDocument();
    expect(max).toBeInTheDocument();
    expect(min).toHaveAttribute("type", "number");
    expect(max).toHaveAttribute("type", "number");

    // The diff inputs follow the conventional-commit group in document order.
    const cc = screen.getByLabelText("title description");
    expect(
      cc.compareDocumentPosition(min) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  // F-rules-diff-create: a created rule carries the diff bounds typed into the
  // two inputs.
  it("round-trips diff bounds on create", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "big diffs" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "big diffs" },
    });
    fireEvent.change(screen.getByLabelText("diff min"), {
      target: { value: "10" },
    });
    fireEvent.change(screen.getByLabelText("diff max"), {
      target: { value: "500" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      name: "big diffs",
      class: "approve",
      diff_min: 10,
      diff_max: 500,
    });
  });

  // F-rules-diff-edit-prefill: opening an existing rule with bounds pre-fills
  // the two inputs, and a re-save (driven by the inputs) round-trips them.
  it("pre-fills diff inputs when editing a rule that has bounds", async () => {
    mockFetch.mockResolvedValue([
      rule({
        id: "v1",
        name: "gate",
        class: "review",
        diff_min: 10,
        diff_max: 500,
      }),
    ]);
    render(<RulesSection />);
    await screen.findByText("gate");

    fireEvent.click(within(reviewCard()).getByLabelText("edit gate"));

    expect(screen.getByLabelText("diff min")).toHaveValue(10);
    expect(screen.getByLabelText("diff max")).toHaveValue(500);

    // Editing the max from the input drives the saved value (inputs are the
    // source of truth, not the original's carry-through).
    fireEvent.change(screen.getByLabelText("diff max"), {
      target: { value: "300" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    expect(mockUpdate.mock.calls[0][1]).toMatchObject({
      diff_min: 10,
      diff_max: 300,
    });
  });

  // F-rules-diff-clear: clearing a diff input drops the bound on the wire
  // (empty ⇒ unconstrained, omitted — not a spurious 0).
  it("omits a cleared diff bound on save", async () => {
    mockFetch.mockResolvedValue([
      rule({
        id: "v1",
        name: "gate",
        class: "review",
        diff_min: 10,
        diff_max: 500,
      }),
    ]);
    render(<RulesSection />);
    await screen.findByText("gate");

    fireEvent.click(within(reviewCard()).getByLabelText("edit gate"));
    fireEvent.change(screen.getByLabelText("diff max"), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    const sent = mockUpdate.mock.calls[0][1];
    expect(sent.diff_min).toBe(10);
    expect(sent.diff_max).toBeUndefined();
  });

  // F-rules-diff-only-saveable: a rule constraining ONLY diff size is not
  // blocked by the empty-rule guard.
  it("allows saving a diff-only rule (empty-rule guard counts diff)", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "large" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "large" },
    });
    fireEvent.change(screen.getByLabelText("diff min"), {
      target: { value: "1000" },
    });

    // The "constrains nothing" warning must not show, and Save is enabled.
    expect(screen.queryByText(/constrains nothing/i)).toBeNull();
    const save = screen.getByRole("button", { name: "Save rule" });
    expect(save).not.toBeDisabled();

    fireEvent.click(save);
    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({ diff_min: 1000 });
  });

  // F-rules-diff-inverted: an inverted range (min > max, both non-zero) blocks
  // Save with an inline message and fires no request.
  it("blocks Save on an inverted diff range with an inline message", async () => {
    mockFetch.mockResolvedValue([]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "inverted" },
    });
    fireEvent.change(screen.getByLabelText("diff min"), {
      target: { value: "500" },
    });
    fireEvent.change(screen.getByLabelText("diff max"), {
      target: { value: "10" },
    });

    expect(screen.getByText(/diff min.*diff max|must not exceed|min.*max/i)).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Save rule" });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(mockCreate).not.toHaveBeenCalled();
  });

  // F-rules-diff-equal-ok: an equal range (min == max) is valid.
  it("allows an equal diff range (min == max)", async () => {
    mockFetch.mockResolvedValueOnce([]);
    mockFetch.mockResolvedValueOnce([rule({ name: "exact" })]);
    render(<RulesSection />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.click(
      within(approveCard()).getByRole("button", { name: "+ New rule" }),
    );
    fireEvent.change(screen.getByLabelText("rule name"), {
      target: { value: "exact" },
    });
    fireEvent.change(screen.getByLabelText("diff min"), {
      target: { value: "100" },
    });
    fireEvent.change(screen.getByLabelText("diff max"), {
      target: { value: "100" },
    });

    const save = screen.getByRole("button", { name: "Save rule" });
    expect(save).not.toBeDisabled();
    fireEvent.click(save);
    await waitFor(() => expect(mockCreate).toHaveBeenCalledTimes(1));
    expect(mockCreate.mock.calls[0][0]).toMatchObject({
      diff_min: 100,
      diff_max: 100,
    });
  });

  // F-rules-summary-diff: the summary line shows the diff bound. Both-sided,
  // min-only, and max-only render distinct clauses.
  it("shows the diff bound in the rule summary line", async () => {
    mockFetch.mockResolvedValue([
      rule({ id: "a1", name: "both", diff_min: 10, diff_max: 500 }),
      rule({ id: "a2", name: "minonly", diff_min: 10, diff_max: 0 }),
      rule({ id: "a3", name: "maxonly", diff_max: 500 }),
    ]);
    render(<RulesSection />);
    await screen.findByText("both");

    const card = within(approveCard());
    expect(card.getByText(/diff 10.500/)).toBeInTheDocument();
    expect(card.getByText(/diff ≥ 10/)).toBeInTheDocument();
    expect(card.getByText(/diff ≤ 500/)).toBeInTheDocument();
  });
});
