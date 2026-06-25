import { useEffect, useState } from "react";
import { fetchSettings, updateSettings, type Settings } from "./api";

// SettingsPanel is the Settings tab: the editor for the analytics assumption
// constants (ADR 0010). It owns editing — minutes-per-switch, hourly-rate, and
// currency — so the Analytics tab stays a pure presentation surface (the money
// figure there is a read-only pill that renders these same constants). Saving
// full-replaces them through PUT /settings; the Analytics tab recomputes its
// time/money figures on its next fetch.
export function SettingsPanel() {
  const [draft, setDraft] = useState<Settings | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchSettings()
      .then(setDraft)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  // patch updates one field and clears the prior save confirmation, so the
  // "Saved" note never lingers over an edited-but-unsaved form.
  const patch = (next: Partial<Settings>) => {
    setSaved(false);
    setDraft((d) => (d ? { ...d, ...next } : d));
  };

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      const stored = await updateSettings(draft);
      setDraft(stored);
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="card">
      <div className="card-head">
        <h2 className="card-title">Settings</h2>
        <span className="card-note">analytics assumptions</span>
      </div>

      {error && (
        <p className="row-alert" role="alert">
          {error}
        </p>
      )}

      {draft === null ? (
        <div className="card-loading">Loading settings…</div>
      ) : (
        <div className="settings-form">
          <p className="settings-intro" data-testid="settings-intro">
            Every PR the robot auto-approves is one code review you didn't
            context-switch into. Zurich developer research values a meaningful
            flow-breaking switch at a wide band, not a single number, so the
            Analytics money figure is shown as a <strong>range</strong>: your
            saved-switch count times the per-switch band below. The low end is a
            brief <strong>~10-min refocus at gross salary</strong> (≈ CHF 10); the
            high end is a full <strong>23-min flow break at loaded employer
            cost</strong> (≈ CHF 26). See the README's “What a saved switch is
            worth” for the full derivation — tune the band to your own numbers.
          </p>

          <div className="field-grid">
            <label className="field">
              <span className="field-label">Low estimate / switch</span>
              <input
                type="number"
                min={1}
                className="tnum"
                aria-label="low estimate per switch"
                value={draft.cost_low}
                onChange={(e) =>
                  patch({ cost_low: clampInt(e.target.value, 1) })
                }
              />
              <span className="field-sub">conservative: ~10-min refocus at gross salary (default 10)</span>
            </label>

            <label className="field">
              <span className="field-label">High estimate / switch</span>
              <input
                type="number"
                min={1}
                className="tnum"
                aria-label="high estimate per switch"
                value={draft.cost_high}
                onChange={(e) =>
                  patch({ cost_high: clampInt(e.target.value, 1) })
                }
              />
              <span className="field-sub">a full 23-min flow break at loaded cost (default 26)</span>
            </label>

            <label className="field">
              <span className="field-label">Currency</span>
              <input
                type="text"
                className="settings-currency"
                aria-label="currency"
                value={draft.currency}
                onChange={(e) => patch({ currency: e.target.value })}
              />
              <span className="field-sub">symbol prefixed onto the money figure</span>
            </label>
          </div>

          <div className="settings-actions">
            {saved && (
              <span className="settings-saved" role="status">
                Saved
              </span>
            )}
            <div className="spacer" />
            <button
              type="button"
              className="btn-save"
              disabled={
                saving ||
                draft.currency.trim() === "" ||
                draft.cost_high < draft.cost_low
              }
              onClick={save}
            >
              {saving ? "Saving…" : "Save"}
            </button>
          </div>
        </div>
      )}
    </section>
  );
}

// clampInt coerces a number input to an integer at or above min, so a blank or
// out-of-range entry never reaches the server (which also validates structurally).
function clampInt(raw: string, min: number): number {
  const n = Math.floor(Number(raw));
  return Number.isFinite(n) && n >= min ? n : min;
}
