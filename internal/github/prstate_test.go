package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
)

// TestCollapsePRState folds GitHub's raw (state, mergedAt) pair into the three
// display buckets plus the defensive unknown. It mirrors the AllGreen split: the
// gh seam only DECODES (state, mergedAt); this pure function JUDGES the bucket.
// merged is state==MERGED, or the defensive CLOSED-with-a-mergedAt; CLOSED with
// no mergedAt is the closed-without-merging false-positive signal.
func TestCollapsePRState(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		mergedAt string
		want     github.PRState
	}{
		{name: "open", state: "OPEN", mergedAt: "", want: github.PRStateOpen},
		{name: "merged via MERGED state", state: "MERGED", mergedAt: "2026-06-19T10:00:00Z", want: github.PRStateMerged},
		{name: "merged via CLOSED with mergedAt (defensive)", state: "CLOSED", mergedAt: "2026-06-19T10:00:00Z", want: github.PRStateMerged},
		{name: "closed without merging is the false-positive signal", state: "CLOSED", mergedAt: "", want: github.PRStateClosed},
		{name: "unrecognised state is unknown, never guessed", state: "", mergedAt: "", want: github.PRStateUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := github.CollapsePRState(tc.state, tc.mergedAt); got != tc.want {
				t.Fatalf("CollapsePRState(%q, %q) = %q, want %q", tc.state, tc.mergedAt, got, tc.want)
			}
		})
	}
}
