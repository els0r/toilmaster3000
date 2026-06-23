package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
)

// TestAllGreen folds rollup entries into pass/fail/pending and asserts the
// All-Green invariant: true IFF there is at least one entry AND every entry is
// pass. An empty rollup is NOT green — a pipeline that never ran is no signal,
// and an auto-approver must never fire on no signal.
func TestAllGreen(t *testing.T) {
	checkRun := func(status, conclusion string) github.Check {
		return github.Check{Typename: "CheckRun", Status: status, Conclusion: conclusion}
	}
	statusContext := func(state string) github.Check {
		return github.Check{Typename: "StatusContext", State: state}
	}

	tests := []struct {
		name   string
		checks []github.Check
		want   bool
	}{
		{
			name:   "empty rollup is not green (no signal)",
			checks: nil,
			want:   false,
		},
		{
			name:   "single passing CheckRun is green",
			checks: []github.Check{checkRun("COMPLETED", "SUCCESS")},
			want:   true,
		},
		{
			name:   "single passing StatusContext is green",
			checks: []github.Check{statusContext("SUCCESS")},
			want:   true,
		},
		{
			name:   "SKIPPED CheckRun counts as pass",
			checks: []github.Check{checkRun("COMPLETED", "SKIPPED")},
			want:   true,
		},
		{
			name:   "NEUTRAL CheckRun counts as pass",
			checks: []github.Check{checkRun("COMPLETED", "NEUTRAL")},
			want:   true,
		},
		{
			name: "all-pass mix of SUCCESS/SKIPPED/NEUTRAL with a passing StatusContext is green",
			checks: []github.Check{
				checkRun("COMPLETED", "SUCCESS"),
				checkRun("COMPLETED", "SKIPPED"),
				checkRun("COMPLETED", "NEUTRAL"),
				statusContext("SUCCESS"),
			},
			want: true,
		},
		{
			name: "a single failing CheckRun blocks an otherwise-green rollup",
			checks: []github.Check{
				checkRun("COMPLETED", "SUCCESS"),
				checkRun("COMPLETED", "FAILURE"),
			},
			want: false,
		},
		{
			name:   "CANCELLED CheckRun is a fail",
			checks: []github.Check{checkRun("COMPLETED", "CANCELLED")},
			want:   false,
		},
		{
			name:   "TIMED_OUT CheckRun is a fail",
			checks: []github.Check{checkRun("COMPLETED", "TIMED_OUT")},
			want:   false,
		},
		{
			name:   "ACTION_REQUIRED CheckRun is a fail",
			checks: []github.Check{checkRun("COMPLETED", "ACTION_REQUIRED")},
			want:   false,
		},
		{
			name:   "STARTUP_FAILURE CheckRun is a fail",
			checks: []github.Check{checkRun("COMPLETED", "STARTUP_FAILURE")},
			want:   false,
		},
		{
			name:   "STALE CheckRun is a fail",
			checks: []github.Check{checkRun("COMPLETED", "STALE")},
			want:   false,
		},
		{
			name:   "FAILURE StatusContext is a fail",
			checks: []github.Check{statusContext("FAILURE")},
			want:   false,
		},
		{
			name:   "ERROR StatusContext is a fail",
			checks: []github.Check{statusContext("ERROR")},
			want:   false,
		},
		{
			name: "a single pending CheckRun (not COMPLETED) blocks an otherwise-green rollup",
			checks: []github.Check{
				checkRun("COMPLETED", "SUCCESS"),
				checkRun("IN_PROGRESS", ""),
			},
			want: false,
		},
		{
			name:   "QUEUED CheckRun (not COMPLETED) is pending and blocks",
			checks: []github.Check{checkRun("QUEUED", "")},
			want:   false,
		},
		{
			name:   "PENDING StatusContext blocks",
			checks: []github.Check{statusContext("PENDING")},
			want:   false,
		},
		{
			name:   "EXPECTED StatusContext blocks",
			checks: []github.Check{statusContext("EXPECTED")},
			want:   false,
		},
		{
			name: "mixed pass and pending is not green",
			checks: []github.Check{
				statusContext("SUCCESS"),
				checkRun("COMPLETED", "SUCCESS"),
				statusContext("PENDING"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := github.AllGreen(tt.checks); got != tt.want {
				t.Errorf("AllGreen(%+v) = %v, want %v", tt.checks, got, tt.want)
			}
		})
	}
}
