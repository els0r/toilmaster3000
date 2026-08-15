package github

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestNormalizeLifecycles runs the PR-State decode+normalise path over a
// RECORDED `gh pr list --state all --json number,state,mergedAt` response —
// the batched tail-of-cycle refresh that replaced the per-PR view N+1 (ADR
// 0007).
//
// It pins only the mapping of gh's state token to the neutral lifecycle. What
// a closed PR carrying a mergedAt MEANS is CollapsePRState's judgement, not
// this adapter's: the pair is normalised and handed over intact, so the
// defensive closed-with-a-mergedAt rule lives in exactly one place across all
// forges (ADR 0030 §3).
func TestNormalizeLifecycles(t *testing.T) {
	items := decodeFixture[[]ghPRStateItem](t, "pr_states.json")
	require.Len(t, items, 5, "the fixture is the recorded response, not a subset")

	require.Equal(t, map[int]forge.Lifecycle{
		42: {State: forge.LifecycleMerged, MergedAt: "2026-06-19T10:00:00Z"},
		7:  {State: forge.LifecycleOpen},                                     // JSON null -> "" -> open
		9:  {State: forge.LifecycleClosed},                                   // null mergedAt -> closed-without-merging
		11: {State: forge.LifecycleClosed, MergedAt: "2026-06-18T08:30:00Z"}, // the pair travels intact
		13: {State: forge.LifecycleUnknown},                                  // never guessed as open
	}, normalizeLifecycles(items))
}

// TestNormalizeLifecycleFeedsCollapse closes the loop the two halves of the
// seam would otherwise leave open: the adapter maps gh's token, the shared
// fold judges the bucket, and only together do they produce what the Approval
// Feed renders. A mis-mapped token would be invisible to the fold's own table.
func TestNormalizeLifecycleFeedsCollapse(t *testing.T) {
	lifecycles := normalizeLifecycles(decodeFixture[[]ghPRStateItem](t, "pr_states.json"))

	states := map[int]forge.PRState{}
	for number, lc := range lifecycles {
		states[number] = forge.CollapsePRState(lc.State, lc.MergedAt)
	}

	require.Equal(t, map[int]forge.PRState{
		42: forge.PRStateMerged,
		7:  forge.PRStateOpen,
		9:  forge.PRStateClosed,
		11: forge.PRStateMerged, // CLOSED carrying a mergedAt is merged
		13: forge.PRStateUnknown,
	}, states)
}
