package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
)

// TestFailingChecks folds a PR's rollup into a count of non-passing entries
// (fails + pendings), the dropped-red station's "N checks failing" signal. It
// reuses the SAME pass/fail/pending taxonomy as AllGreen — a non-pass entry is
// any fail or pending — so an all-green rollup folds to 0 and an empty rollup
// (no signal) folds to 0 too (nothing ran, nothing is failing). The two share
// the classification rather than duplicating it.
func TestFailingChecks(t *testing.T) {
	checkRun := func(status, conclusion string) github.Check {
		return github.Check{Typename: "CheckRun", Status: status, Conclusion: conclusion}
	}
	statusContext := func(state string) github.Check {
		return github.Check{Typename: "StatusContext", State: state}
	}

	tests := []struct {
		name   string
		checks []github.Check
		want   int
	}{
		{
			name:   "empty rollup has no failing checks (nothing ran)",
			checks: nil,
			want:   0,
		},
		{
			name:   "all-green rollup has zero failing checks",
			checks: []github.Check{checkRun("COMPLETED", "SUCCESS"), statusContext("SUCCESS")},
			want:   0,
		},
		{
			name:   "SKIPPED and NEUTRAL CheckRuns are passes, not failing",
			checks: []github.Check{checkRun("COMPLETED", "SKIPPED"), checkRun("COMPLETED", "NEUTRAL")},
			want:   0,
		},
		{
			name:   "a single failing CheckRun counts one",
			checks: []github.Check{checkRun("COMPLETED", "SUCCESS"), checkRun("COMPLETED", "FAILURE")},
			want:   1,
		},
		{
			name: "mixed CheckRun + StatusContext fails and pendings all count",
			checks: []github.Check{
				checkRun("COMPLETED", "SUCCESS"),   // pass
				checkRun("COMPLETED", "FAILURE"),   // fail
				checkRun("COMPLETED", "CANCELLED"), // fail
				checkRun("IN_PROGRESS", ""),        // pending
				statusContext("SUCCESS"),           // pass
				statusContext("ERROR"),             // fail
				statusContext("PENDING"),           // pending
			},
			want: 5,
		},
		{
			name:   "an unknown __typename entry is non-passing and counts",
			checks: []github.Check{{Typename: "Mystery"}},
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := github.FailingChecks(tt.checks); got != tt.want {
				t.Errorf("FailingChecks(%+v) = %d, want %d", tt.checks, got, tt.want)
			}
		})
	}
}
