package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
)

// TestCollapsePRState folds the NORMALISED (lifecycle, mergedAt) pair into the
// three display buckets plus the defensive unknown. It mirrors the AllGreen
// split: the adapter only decodes and normalises the pair; this pure function
// judges the bucket. merged is lifecycle merged, or the defensive
// closed-with-a-mergedAt; closed with no mergedAt is the
// closed-without-merging false-positive signal.
func TestCollapsePRState(t *testing.T) {
	tests := []struct {
		name     string
		state    forge.LifecycleState
		mergedAt string
		want     forge.PRState
	}{
		{name: "open", state: forge.LifecycleOpen, mergedAt: "", want: forge.PRStateOpen},
		{name: "merged via the merged lifecycle", state: forge.LifecycleMerged, mergedAt: "2026-06-19T10:00:00Z", want: forge.PRStateMerged},
		{name: "merged via closed with a mergedAt (defensive)", state: forge.LifecycleClosed, mergedAt: "2026-06-19T10:00:00Z", want: forge.PRStateMerged},
		{name: "closed without merging is the false-positive signal", state: forge.LifecycleClosed, mergedAt: "", want: forge.PRStateClosed},
		{name: "an unknown lifecycle is unknown, never guessed", state: forge.LifecycleUnknown, mergedAt: "", want: forge.PRStateUnknown},
		{name: "an unknown lifecycle stays unknown even with a mergedAt", state: forge.LifecycleUnknown, mergedAt: "2026-06-19T10:00:00Z", want: forge.PRStateUnknown},
		{name: "the zero lifecycle is unknown", state: "", mergedAt: "", want: forge.PRStateUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := forge.CollapsePRState(tc.state, tc.mergedAt); got != tc.want {
				t.Fatalf("CollapsePRState(%q, %q) = %q, want %q", tc.state, tc.mergedAt, got, tc.want)
			}
		})
	}
}
