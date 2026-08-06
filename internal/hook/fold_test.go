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
		errStreaks  map[string]int                // screen ID -> trailing error-row count; absent = 0
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
			errStreaks:  map[string]int{"id-sec": 1},
			wantPending: []string{"security"},
		},
		{
			name:        "two error attempts: still pending — transient blips self-heal",
			screens:     []hook.ScreenInstance{inst(sec)},
			rows:        map[string]hook.VerdictRecord{"id-sec": verdict(hook.Errored, "nonzero exit")},
			errStreaks:  map[string]int{"id-sec": 2},
			wantPending: []string{"security"},
		},
		{
			name:       "three error attempts synthesize a hold carrying the last error — a fold output, never a stored row",
			screens:    []hook.ScreenInstance{inst(sec)},
			rows:       map[string]hook.VerdictRecord{"id-sec": verdict(hook.Errored, "harness timeout")},
			errStreaks: map[string]int{"id-sec": 3},
			wantHolds:  []hook.HoldDetail{{Screen: "security", Reason: "screen unavailable: harness timeout"}},
		},
		{
			name:       "beyond three attempts stays held — level-triggered, no upper edge",
			screens:    []hook.ScreenInstance{inst(sec)},
			rows:       map[string]hook.VerdictRecord{"id-sec": verdict(hook.Errored, "nonzero exit")},
			errStreaks: map[string]int{"id-sec": 5},
			wantHolds:  []hook.HoldDetail{{Screen: "security", Reason: "screen unavailable: nonzero exit"}},
		},
		{
			name:    "a real verdict outranks any stale streak: the streak gates only an error latest row",
			screens: []hook.ScreenInstance{inst(sec), inst(lic)},
			rows: map[string]hook.VerdictRecord{
				"id-sec": verdict(hook.Proceed, "clean"),
				"id-lic": verdict(hook.Hold, "GPL dependency"),
			},
			errStreaks: map[string]int{"id-sec": 3, "id-lic": 3},
			wantHolds:  []hook.HoldDetail{{Screen: "license", Reason: "GPL dependency"}},
		},
		{
			name:    "a synthesized hold rides beside a real hold, config order (the reasons-list doctrine)",
			screens: []hook.ScreenInstance{inst(sec), inst(lic)},
			rows: map[string]hook.VerdictRecord{
				"id-sec": verdict(hook.Errored, "harness timeout"),
				"id-lic": verdict(hook.Hold, "GPL dependency"),
			},
			errStreaks: map[string]int{"id-sec": 3},
			wantHolds: []hook.HoldDetail{
				{Screen: "security", Reason: "screen unavailable: harness timeout"},
				{Screen: "license", Reason: "GPL dependency"},
			},
		},
		{
			name:       "a struck-out DISABLED screen never gates — disable + restart releases the synthetic hold",
			screens:    []hook.ScreenInstance{inst(off), inst(sec)},
			rows:       map[string]hook.VerdictRecord{"id-off": verdict(hook.Errored, "harness gone"), "id-sec": verdict(hook.Proceed, "clean")},
			errStreaks: map[string]int{"id-off": 4},
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
			disp := hook.FoldVerdicts(tt.screens,
				func(id string) (hook.VerdictRecord, bool) {
					rec, ok := tt.rows[id]
					return rec, ok
				},
				func(id string) int { return tt.errStreaks[id] },
			)

			var pendingNames []string
			for _, p := range disp.Pending {
				pendingNames = append(pendingNames, p.Spec.Name)
			}
			require.Equal(t, tt.wantPending, pendingNames)
			require.Equal(t, tt.wantHolds, disp.Holds)
		})
	}
}
