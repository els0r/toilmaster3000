package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyManualApproveRaceGuard proves the found-guard at its source: when a
// cycle rebuild wins the race between approve() and applyManualApprove — the
// fresh queue already excludes the PR and the fresh funnel already counts it as
// ApprovedByTm3k — the mutation must be a total no-op. Decrementing
// NeedsHumanReview or bumping ApprovedByTm3k then would corrupt the freshly
// rebuilt partition. Deterministically reachable only here: the interleaving
// cannot be forced through the public API.
func TestApplyManualApproveRaceGuard(t *testing.T) {
	e := &Engine{
		// The rebuilt snapshots: #12 is gone from the queue, already counted as
		// a standing ApprovedByTm3k member, heartbeat already at zero.
		queue:  []QueueItem{{Number: 7, Title: "docs: other", Author: "x", URL: "u7"}},
		funnel: Funnel{Incoming: 2, NeedsHumanReview: 1, ApprovedByTm3k: 1},
		status: Status{QueueCount: 1},
	}

	e.applyManualApprove(12)

	require.Len(t, e.queue, 1, "an absent number removes nothing")
	require.Equal(t, 1, e.funnel.NeedsHumanReview, "the funnel is untouched — the rebuild already accounted for the PR")
	require.Equal(t, 1, e.funnel.ApprovedByTm3k)
	require.Equal(t, 1, e.status.QueueCount, "the heartbeat is untouched")
}
