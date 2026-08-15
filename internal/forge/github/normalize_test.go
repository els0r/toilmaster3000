package github

import (
	"encoding/json"
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestNormalizeCheckState is the correctness-critical half of the seam: it
// pins which RAW GitHub rollup entry maps to which neutral CheckState. A
// mis-mapped entry silently changes what "green" means, and the shared folds
// cannot catch it — they only ever see the normalised value (ADR 0030 §10).
//
// Every row's input is a recorded raw entry as gh emits it inside
// statusCheckRollup, decoded from JSON rather than hand-built, so the field
// spellings (__typename / status / conclusion / state) are pinned here too.
//
// The rollup is heterogeneous: GitHub Checks API runs arrive as CheckRun
// (carrying status + conclusion), legacy commit statuses as StatusContext
// (carrying state).
func TestNormalizeCheckState(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want forge.CheckState
	}{
		{
			name: "COMPLETED SUCCESS CheckRun is a pass",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"}`,
			want: forge.CheckPass,
		},
		{
			name: "SKIPPED CheckRun counts as pass",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SKIPPED"}`,
			want: forge.CheckPass,
		},
		{
			name: "NEUTRAL CheckRun counts as pass",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "NEUTRAL"}`,
			want: forge.CheckPass,
		},
		{
			name: "FAILURE CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"}`,
			want: forge.CheckFail,
		},
		{
			name: "CANCELLED CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "CANCELLED"}`,
			want: forge.CheckFail,
		},
		{
			name: "TIMED_OUT CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "TIMED_OUT"}`,
			want: forge.CheckFail,
		},
		{
			name: "ACTION_REQUIRED CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "ACTION_REQUIRED"}`,
			want: forge.CheckFail,
		},
		{
			name: "STARTUP_FAILURE CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "STARTUP_FAILURE"}`,
			want: forge.CheckFail,
		},
		{
			name: "STALE CheckRun is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "STALE"}`,
			want: forge.CheckFail,
		},
		{
			name: "a COMPLETED CheckRun with an unrecognised conclusion is a fail",
			raw:  `{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SOMETHING_GITHUB_ADDED"}`,
			want: forge.CheckFail,
		},
		{
			name: "IN_PROGRESS CheckRun is pending — not completed, so no verdict yet",
			raw:  `{"__typename": "CheckRun", "status": "IN_PROGRESS", "conclusion": ""}`,
			want: forge.CheckPending,
		},
		{
			name: "QUEUED CheckRun is pending",
			raw:  `{"__typename": "CheckRun", "status": "QUEUED", "conclusion": ""}`,
			want: forge.CheckPending,
		},
		{
			name: "an uncompleted CheckRun is pending even carrying a conclusion",
			raw:  `{"__typename": "CheckRun", "status": "IN_PROGRESS", "conclusion": "FAILURE"}`,
			want: forge.CheckPending,
		},
		{
			name: "SUCCESS StatusContext is a pass",
			raw:  `{"__typename": "StatusContext", "state": "SUCCESS"}`,
			want: forge.CheckPass,
		},
		{
			name: "PENDING StatusContext is pending",
			raw:  `{"__typename": "StatusContext", "state": "PENDING"}`,
			want: forge.CheckPending,
		},
		{
			name: "EXPECTED StatusContext is pending",
			raw:  `{"__typename": "StatusContext", "state": "EXPECTED"}`,
			want: forge.CheckPending,
		},
		{
			name: "FAILURE StatusContext is a fail",
			raw:  `{"__typename": "StatusContext", "state": "FAILURE"}`,
			want: forge.CheckFail,
		},
		{
			name: "ERROR StatusContext is a fail",
			raw:  `{"__typename": "StatusContext", "state": "ERROR"}`,
			want: forge.CheckFail,
		},
		{
			name: "an unrecognised StatusContext state is a fail — drawing the eye beats reading as a harmless wait",
			raw:  `{"__typename": "StatusContext", "state": "SOMETHING_GITHUB_ADDED"}`,
			want: forge.CheckFail,
		},
		{
			name: "an unrecognised entry type is a fail, defensively",
			raw:  `{"__typename": "SomethingGitHubAddedLater"}`,
			want: forge.CheckFail,
		},
		{
			name: "an entry with no type at all is a fail",
			raw:  `{}`,
			want: forge.CheckFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw ghCheck
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &raw))
			require.Equal(t, tt.want, normalizeCheckState(raw))
		})
	}
}

// TestNormalizeChecks pins the adapter-supplied failing count alongside the
// normalised rollup. Cardinality is a forge fact (ADR 0030 §6), so the count
// is the adapter's to produce — but GitHub's must stay exactly what the old
// FailingChecks fold produced: the number of NON-PASSING entries, fails and
// pendings alike, the dropped-red station's "N checks failing" signal.
//
// An all-green rollup counts 0, and an empty rollup counts 0 too — a pipeline
// that never ran has nothing failing. Emptiness is AllGreen's concern, not a
// failing check's.
func TestNormalizeChecks(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		want        []forge.Check
		wantFailing int
	}{
		{
			name:        "empty rollup normalises to no entries and no failing checks (nothing ran)",
			raw:         `[]`,
			want:        nil,
			wantFailing: 0,
		},
		{
			name: "an all-green rollup has zero failing checks",
			raw: `[{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
			       {"__typename": "StatusContext", "state": "SUCCESS"}]`,
			want:        []forge.Check{{State: forge.CheckPass}, {State: forge.CheckPass}},
			wantFailing: 0,
		},
		{
			name: "SKIPPED and NEUTRAL CheckRuns are passes, not failing",
			raw: `[{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SKIPPED"},
			       {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "NEUTRAL"}]`,
			want:        []forge.Check{{State: forge.CheckPass}, {State: forge.CheckPass}},
			wantFailing: 0,
		},
		{
			name: "a single failing CheckRun counts one",
			raw: `[{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
			       {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"}]`,
			want:        []forge.Check{{State: forge.CheckPass}, {State: forge.CheckFail}},
			wantFailing: 1,
		},
		{
			name: "mixed CheckRun + StatusContext fails and pendings all count",
			raw: `[{"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
			       {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "FAILURE"},
			       {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "CANCELLED"},
			       {"__typename": "CheckRun", "status": "IN_PROGRESS"},
			       {"__typename": "StatusContext", "state": "SUCCESS"},
			       {"__typename": "StatusContext", "state": "ERROR"},
			       {"__typename": "StatusContext", "state": "PENDING"}]`,
			want: []forge.Check{
				{State: forge.CheckPass},
				{State: forge.CheckFail},
				{State: forge.CheckFail},
				{State: forge.CheckPending},
				{State: forge.CheckPass},
				{State: forge.CheckFail},
				{State: forge.CheckPending},
			},
			wantFailing: 5,
		},
		{
			name:        "an unknown __typename entry is non-passing and counts",
			raw:         `[{"__typename": "Mystery"}]`,
			want:        []forge.Check{{State: forge.CheckFail}},
			wantFailing: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []ghCheck
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &raw))
			checks, failing := normalizeChecks(raw)
			require.Equal(t, tt.want, checks)
			require.Equal(t, tt.wantFailing, failing)
		})
	}
}
