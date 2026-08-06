package hook_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// spec builds an enabled screen spec with distinct ID and Name, so the tables
// prove verdicts key on ID while user-facing output carries Name.
func spec(id, name string) hook.Spec {
	return hook.Spec{ID: id, Name: name, Harness: "claude", Prompt: "p", Enabled: true}
}

func inst(s hook.Spec) hook.ScreenInstance {
	return hook.ScreenInstance{Spec: s}
}

// FoldVerdicts is the correctness-critical pure core of screening (the
// AllGreen of ADR 0022): given the configured screens and the latest stored
// row per key, it judges the conjunctive disposition. Heavy tables per the
// testing doctrine.
func TestFoldVerdicts(t *testing.T) {
	sec := spec("id-sec", "security")
	lic := spec("id-lic", "license")
	off := spec("id-off", "disabled-screen")
	off.Enabled = false

	verdict := func(outcome hook.Outcome, reason string) hook.VerdictRecord {
		return hook.VerdictRecord{Outcome: outcome, Reason: reason}
	}

	tests := []struct {
		name        string
		screens     []hook.ScreenInstance
		rows        map[string]hook.VerdictRecord // screen ID -> latest row; absent = no row
		wantPending []string                      // pending screen NAMES
		wantHolds   []hook.HoldDetail
	}{
		{
			name:    "no screens configured: proceed — bit-for-bit today's behavior",
			screens: nil,
		},
		{
			name:    "all proceed: nothing pending, nothing holding — approval may fire",
			screens: []hook.ScreenInstance{inst(sec), inst(lic)},
			rows: map[string]hook.VerdictRecord{
				"id-sec": verdict(hook.Proceed, "clean"),
				"id-lic": verdict(hook.Proceed, "MIT only"),
			},
		},
		{
			name:        "missing verdict: the screen is pending — a missing verdict is never proceed",
			screens:     []hook.ScreenInstance{inst(sec), inst(lic)},
			rows:        map[string]hook.VerdictRecord{"id-sec": verdict(hook.Proceed, "clean")},
			wantPending: []string{"license"},
		},
		{
			name:        "error latest row: still pending — a failed attempt is no verdict, re-dispatch",
			screens:     []hook.ScreenInstance{inst(sec)},
			rows:        map[string]hook.VerdictRecord{"id-sec": verdict(hook.Errored, "harness timeout")},
			wantPending: []string{"security"},
		},
		{
			name:    "one hold: collected with the screen's Name and the verdict's prose reason",
			screens: []hook.ScreenInstance{inst(sec), inst(lic)},
			rows: map[string]hook.VerdictRecord{
				"id-sec": verdict(hook.Hold, "touches auth code"),
				"id-lic": verdict(hook.Proceed, "MIT only"),
			},
			wantHolds: []hook.HoldDetail{{Screen: "security", Reason: "touches auth code"}},
		},
		{
			name:    "every holding screen is carried, config order (the reasons-list doctrine)",
			screens: []hook.ScreenInstance{inst(sec), inst(lic)},
			rows: map[string]hook.VerdictRecord{
				"id-sec": verdict(hook.Hold, "touches auth code"),
				"id-lic": verdict(hook.Hold, "GPL dependency"),
			},
			wantHolds: []hook.HoldDetail{
				{Screen: "security", Reason: "touches auth code"},
				{Screen: "license", Reason: "GPL dependency"},
			},
		},
		{
			name:        "hold beside a pending screen: both facts reported — the caller's branch precedence diverts on the hold",
			screens:     []hook.ScreenInstance{inst(sec), inst(lic)},
			rows:        map[string]hook.VerdictRecord{"id-sec": verdict(hook.Hold, "touches auth code")},
			wantPending: []string{"license"},
			wantHolds:   []hook.HoldDetail{{Screen: "security", Reason: "touches auth code"}},
		},
		{
			name:    "a disabled screen never gates: no verdict needed, its hold ignored",
			screens: []hook.ScreenInstance{inst(off), inst(sec)},
			rows: map[string]hook.VerdictRecord{
				"id-off": verdict(hook.Hold, "should not matter"),
				"id-sec": verdict(hook.Proceed, "clean"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disp := hook.FoldVerdicts(tt.screens, func(id string) (hook.VerdictRecord, bool) {
				rec, ok := tt.rows[id]
				return rec, ok
			})

			var pendingNames []string
			for _, p := range disp.Pending {
				pendingNames = append(pendingNames, p.Spec.Name)
			}
			require.Equal(t, tt.wantPending, pendingNames)
			require.Equal(t, tt.wantHolds, disp.Holds)
		})
	}
}
