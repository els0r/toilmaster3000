import { useCallback, useEffect, useState } from "react";
import {
  createRule,
  deleteRule,
  fetchRules,
  updateRule,
  type Rule,
} from "./api";
import {
  blankDraft,
  draftToRule,
  ruleRegexError,
  ruleToDraft,
  summarize,
  validateDraft,
  TITLE_PART_FIELDS,
  type Draft,
  type RuleClass,
} from "./ruleDraft";

// classOf reads a rule's class, treating empty/absent as "approve" (matching the
// backend), so the two seeds and any pre-existing file fall into the Approve card.
function classOf(r: Rule): RuleClass {
  return (r.class ?? "approve") === "review" ? "review" : "approve";
}

// RulesSection owns the single shared GET /rules fetch (the "two cards fed by one
// GET /rules" design from CONTEXT.md) and renders the two cards (Approve Rules +
// Human Review Always). Both cards read the same `rules` list filtered by their
// class; each card wraps its own mutations and refetches the shared list on
// success, so a mutation in either card keeps both consistent. A fetch-level
// failure surfaces in both cards; a mutation error stays scoped to its card.
export function RulesSection() {
  const [rules, setRules] = useState<Rule[] | null>(null);
  const [fetchError, setFetchError] = useState<string | null>(null);

  const refetch = useCallback(async () => {
    try {
      setRules(await fetchRules());
      setFetchError(null);
    } catch (e) {
      setFetchError(messageOf(e));
    }
  }, []);

  useEffect(() => {
    void refetch();
  }, [refetch]);

  return (
    <div className="rules-stack">
      <RulesEditor
        cls="approve"
        title="Approve Rules"
        rules={rules}
        fetchError={fetchError}
        refetch={refetch}
      />
      <RulesEditor
        cls="review"
        title="Human Review Always"
        rules={rules}
        fetchError={fetchError}
        refetch={refetch}
      />
    </div>
  );
}

// RulesEditor renders one card for a single Rule class: it lists that class's
// rules (filtered from the shared list) and lets the user create, name, edit,
// toggle Enabled, and delete them. The "+ New rule" button stamps the card's
// class onto the draft. Each mutation refetches the shared list on success and
// surfaces its own server-error scoped to this card (a sibling card's failure
// never leaks here). The editor is identical across both classes — class is
// card-implied, not an editable field.
export function RulesEditor({
  cls,
  title,
  rules,
  fetchError,
  refetch,
}: {
  cls: RuleClass;
  title: string;
  rules: Rule[] | null;
  fetchError: string | null;
  refetch: () => Promise<void>;
}) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [mutateError, setMutateError] = useState<string | null>(null);

  // run wraps a mutation: clear this card's prior error, perform it, refetch the
  // shared list on success, surface the server's message on failure (so the list
  // never silently lies). The error stays scoped to the card that triggered it.
  const run = useCallback(
    async (mutate: () => Promise<unknown>) => {
      setMutateError(null);
      try {
        await mutate();
        await refetch();
        return true;
      } catch (e) {
        setMutateError(messageOf(e));
        return false;
      }
    },
    [refetch],
  );

  // A fetch-level failure shows in both cards; a mutation error only here.
  const error = mutateError ?? fetchError;

  // The card shows only its own class's rules; the shared list feeds both cards.
  const cardRules = rules?.filter((r) => classOf(r) === cls) ?? null;
  const enabledCount = cardRules?.filter((r) => r.enabled).length ?? 0;

  async function saveDraft() {
    if (!draft) return;
    const rule = draftToRule(draft);
    const ok = await run(() =>
      draft.isNew ? createRule(rule) : updateRule(draft.id!, rule),
    );
    if (ok) setDraft(null);
  }

  async function deleteFromModal() {
    if (!draft?.id) return;
    const ok = await run(() => deleteRule(draft.id!));
    if (ok) setDraft(null);
  }

  return (
    <section className="card">
      <div className="card-head">
        <h2 className="card-title">{title}</h2>
        <span className="card-count tnum">{cardRules?.length ?? 0}</span>
        {cardRules && (
          <span className="card-note">
            {enabledCount} of {cardRules.length} active
          </span>
        )}
        <div className="spacer" />
        <button
          type="button"
          className="btn-new"
          onClick={() => setDraft(blankDraft(cls))}
        >
          + New rule
        </button>
      </div>

      {error && (
        <p className="row-alert" role="alert">
          {error}
        </p>
      )}

      {cardRules === null ? (
        <div className="card-loading">Loading rules…</div>
      ) : cardRules.length === 0 ? (
        <div className="card-empty">No rules yet.</div>
      ) : (
        <div>
          {cardRules.map((r) => (
            <RuleRow
              key={r.id}
              rule={r}
              onToggle={() =>
                void run(() => updateRule(r.id!, { ...r, enabled: !r.enabled }))
              }
              onEdit={() => setDraft(ruleToDraft(r, cls))}
              onDelete={() => void run(() => deleteRule(r.id!))}
            />
          ))}
        </div>
      )}

      {draft && (
        <RuleModal
          draft={draft}
          onChange={setDraft}
          onCancel={() => setDraft(null)}
          onSave={() => void saveDraft()}
          onDelete={() => void deleteFromModal()}
        />
      )}
    </section>
  );
}

// RuleRow renders one rule with its toggle switch, a human-readable summary of
// what it matches, an inline regex-validity warning, and Edit/Delete actions.
function RuleRow({
  rule,
  onToggle,
  onEdit,
  onDelete,
}: {
  rule: Rule;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const regexErr = ruleRegexError(rule);
  return (
    <div className="rule-row">
      <button
        type="button"
        className={`toggle${rule.enabled ? " on" : ""}`}
        aria-label={`toggle ${rule.name}`}
        title={rule.enabled ? "Disable rule" : "Enable rule"}
        onClick={onToggle}
      >
        <span className="knob" />
      </button>

      <div className="rule-body">
        <div className="rule-nameline">
          <span className={`rule-name${rule.enabled ? "" : " off"}`}>
            {rule.name}
          </span>
          {!rule.enabled && <span className="badge-off">off</span>}
          {regexErr && (
            <span className="badge-error">
              <span className="mark">!</span>
              invalid regex in {regexErr}
            </span>
          )}
        </div>
        <div className="rule-summary">{summarize(rule)}</div>
      </div>

      <div className="rule-actions">
        <button
          type="button"
          className="btn-ghost"
          aria-label={`edit ${rule.name}`}
          onClick={onEdit}
        >
          Edit
        </button>
        <button
          type="button"
          className="btn-ghost danger"
          aria-label={`delete ${rule.name}`}
          onClick={onDelete}
        >
          Delete
        </button>
      </div>
    </div>
  );
}

// RuleModal is the create/edit form. It validates each conventional-commit regex
// on every keystroke and blocks Save while any is invalid or the rule constrains
// nothing (which would match every PR). The server validates again; its message
// surfaces through the parent's error alert.
export function RuleModal({
  draft,
  onChange,
  onCancel,
  onSave,
  onDelete,
}: {
  draft: Draft;
  onChange: (d: Draft) => void;
  onCancel: () => void;
  onSave: () => void;
  onDelete: () => void;
}) {
  const set = (patch: Partial<Draft>) => onChange({ ...draft, ...patch });

  // One verdict from the predicate-vocabulary module gates Save and feeds the
  // inline errors: per-field regex validity, the inverted-range guard, and the
  // constrains-nothing guard. The modal no longer restates any of these rules.
  const { regexErrors, invertedRange, constrainsNothing } = validateDraft(draft);
  const anyRegexErr = Object.values(regexErrors).some(Boolean);
  const saveDisabled = anyRegexErr || constrainsNothing || invertedRange;

  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h3 className="modal-title">{draft.isNew ? "New rule" : "Edit rule"}</h3>
          <div className="spacer" />
          <button
            type="button"
            className="modal-close"
            aria-label="close"
            onClick={onCancel}
          >
            ×
          </button>
        </div>

        <div className="modal-body">
          <div className="name-row">
            <label className="field" style={{ flex: 1 }}>
              <span className="field-label">Rule name</span>
              <input
                className="input"
                aria-label="rule name"
                placeholder="e.g. Dependabot patch bumps"
                value={draft.name}
                onChange={(e) => set({ name: e.target.value })}
              />
            </label>
            <button
              type="button"
              className="modal-enabled"
              aria-label="toggle rule enabled"
              onClick={() => set({ enabled: !draft.enabled })}
            >
              <span className={`mini-switch${draft.enabled ? " on" : ""}`}>
                <span className="knob" />
              </span>
              <span>{draft.enabled ? "Enabled" : "Disabled"}</span>
            </button>
          </div>

          <div className="field-grid">
            <label className="field">
              <span className="field-label">Author include</span>
              <input
                className="input sm"
                aria-label="author include"
                placeholder="dependabot[bot], k-tanaka"
                value={draft.authorInc}
                onChange={(e) => set({ authorInc: e.target.value })}
              />
              <span className="field-sub">comma-separated · regex per entry</span>
            </label>
            <label className="field">
              <span className="field-label">Author exclude</span>
              <input
                className="input sm"
                aria-label="author exclude"
                placeholder="(none)"
                value={draft.authorExc}
                onChange={(e) => set({ authorExc: e.target.value })}
              />
              <span className="field-sub">comma-separated · regex per entry</span>
            </label>
          </div>

          <div className="cc-group">
            <span className="field-label">Title — conventional commit</span>
            {/* One row per title-part field, driven by the predicate-vocabulary
                table — the six include/exclude inputs are no longer hand-wired.
                A predicate added to the table appears here automatically. */}
            {TITLE_PART_FIELDS.map((f) => (
              <div className="cc-grid" key={f.part}>
                <label className="field">
                  <span className="cc-sublabel">{f.part} include</span>
                  <input
                    className={`input sm${regexErrors[f.includeKey] ? " bad" : ""}`}
                    aria-label={`title ${f.part}`}
                    placeholder={f.placeholderInc}
                    value={draft[f.includeKey]}
                    onChange={(e) => set({ [f.includeKey]: e.target.value })}
                  />
                  {regexErrors[f.includeKey] && (
                    <span className="err-text">invalid regex</span>
                  )}
                </label>
                <label className="field">
                  <span className="cc-sublabel">{f.part} exclude</span>
                  <input
                    className={`input sm${regexErrors[f.excludeKey] ? " bad" : ""}`}
                    aria-label={`title ${f.part} exclude`}
                    placeholder={f.placeholderExc}
                    value={draft[f.excludeKey]}
                    onChange={(e) => set({ [f.excludeKey]: e.target.value })}
                  />
                  {regexErrors[f.excludeKey] && (
                    <span className="err-text">invalid regex</span>
                  )}
                </label>
              </div>
            ))}
          </div>

          <div className="diff-group">
            <span className="field-label">Diff size — total changed lines</span>
            <div className="cc-grid">
              <label className="field">
                <span className="cc-sublabel">min</span>
                <input
                  type="number"
                  min={0}
                  className={`input sm${invertedRange ? " bad" : ""}`}
                  aria-label="diff min"
                  placeholder="(none)"
                  value={draft.diffMin}
                  onChange={(e) => set({ diffMin: e.target.value })}
                />
              </label>
              <label className="field">
                <span className="cc-sublabel">max</span>
                <input
                  type="number"
                  min={0}
                  className={`input sm${invertedRange ? " bad" : ""}`}
                  aria-label="diff max"
                  placeholder="(none)"
                  value={draft.diffMax}
                  onChange={(e) => set({ diffMax: e.target.value })}
                />
              </label>
            </div>
            <span className="field-sub">0 or empty ⇒ unconstrained</span>
            {invertedRange && (
              <span className="err-text">
                diff min must not exceed diff max
              </span>
            )}
          </div>

          {constrainsNothing && (
            <div className="constrains-warn">
              <span className="mark">!</span>
              This rule constrains nothing — it would match every PR.
            </div>
          )}
        </div>

        <div className="modal-foot">
          {!draft.isNew && (
            <button
              type="button"
              className="btn-delete-text"
              aria-label="delete rule"
              onClick={onDelete}
            >
              Delete rule
            </button>
          )}
          <div className="spacer" />
          <button type="button" className="btn-cancel" onClick={onCancel}>
            Cancel
          </button>
          <button
            type="button"
            className="btn-save"
            disabled={saveDisabled}
            onClick={onSave}
          >
            Save rule
          </button>
        </div>
      </div>
    </div>
  );
}

// messageOf renders an unknown caught value as a string for the card's error
// alert. (The Draft↔Rule round-trip, validation, and summary now live in the
// predicate-vocabulary module, ./ruleDraft.)
function messageOf(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
