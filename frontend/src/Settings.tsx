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
          <p className="settings-intro">
            How the Analytics tab turns auto-approvals into the time and money the
            robot saved. Each context switch is assumed to cost a fixed refocus
            time, valued at your hourly rate.
          </p>

          <div className="field-grid">
            <label className="field">
              <span className="field-label">Minutes per switch</span>
              <input
                type="number"
                min={1}
                className="tnum"
                aria-label="minutes per switch"
                value={draft.minutes_per_switch}
                onChange={(e) =>
                  patch({ minutes_per_switch: clampInt(e.target.value, 1) })
                }
              />
              <span className="field-sub">refocus cost of one interruption (default 23)</span>
            </label>

            <label className="field">
              <span className="field-label">Hourly rate</span>
              <input
                type="number"
                min={0}
                className="tnum"
                aria-label="hourly rate"
                value={draft.hourly_rate}
                onChange={(e) =>
                  patch({ hourly_rate: clampInt(e.target.value, 0) })
                }
              />
              <span className="field-sub">your time's value, applied to the hours saved</span>
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
              disabled={saving || draft.currency.trim() === ""}
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
