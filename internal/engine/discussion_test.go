package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// D1 (tracer): a green + APPROVED authored PR with an unresolved review thread
// folds into In Discussion, not Ready — the Discussion gate realized as a
// stage (ADR 0019). The judged unresolved count rides the item, and the
// threads ride the cycle in ONE batched call.
func TestUnresolvedThreadsFoldToInDiscussion(t *testing.T) {
	eng, fake := outboundEngine(t,
		github.PR{Number: 6, Title: "feat(f): approved", Author: "me", URL: "u6",
			Checks: green(), ReviewDecision: "APPROVED", Mergeable: "MERGEABLE"},
	)
	fake.SetThreads(6, github.RawReviewThreads{
		Nodes: []github.ReviewThread{{IsResolved: false}, {IsResolved: true}},
	})

	eng.RunCycleOnce(context.Background())

	ob := eng.Outbound()
	require.Empty(t, ob.Ready, "an approved PR with an unresolved thread is not Ready")
	require.Len(t, ob.InDiscussion, 1)
	require.Equal(t, 6, ob.InDiscussion[0].Number)
	require.Equal(t, 1, ob.InDiscussion[0].UnresolvedThreads, "the judged unresolved count rides the item")
	require.Equal(t, 1, ob.Outgoing, "the partition still sums — In Discussion is one of its lists")
	require.Equal(t, 1, fake.ThreadsCallCount(), "threads ride one batched call per cycle, no N+1")
}

// D2: the hold, never a disarm — an ARMED approved PR with an unresolved
// thread never merges (the merge step walks only Ready: the partition IS the
// gate, no extra merge-step clause) and KEEPS its arm (Armed ∧ In-Discussion
// is a valid state; only CHANGES_REQUESTED withdraws consent). The FIRST
// cycle the threads read zero, the still-armed PR merges — ledger append +
// Ready prune intact (ADR 0018) — even with nobody at the keyboard: that is
// what standing consent means.
func TestArmedInDiscussionHeldThenMergesOnResolution(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})
	fake.SetThreads(21, github.RawReviewThreads{Nodes: []github.ReviewThread{{IsResolved: false}}})

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21), "the Arm toggle stays available on the In Discussion stage")

	eng.RunCycleOnce(context.Background())
	require.Empty(t, fake.MergeCalls(), "an armed In-Discussion PR never merges")
	require.Empty(t, eng.Merges())
	require.True(t, eng.ArmedSet()[21], "In Discussion holds, never disarms")

	// The reviewer's resolve click closes the conversation; nobody touches tm3k.
	fake.SetThreads(21, github.RawReviewThreads{Nodes: []github.ReviewThread{{IsResolved: true}}})
	eng.RunCycleOnce(context.Background())

	require.Len(t, fake.MergeCalls(), 1, "the first zero-unresolved cycle merges")
	require.Len(t, eng.Merges(), 1, "the ledger append rides the merge")
	ob := eng.Outbound()
	require.Empty(t, ob.Ready, "Ready pruned atomically with the ledger append (ADR 0018)")
	require.Zero(t, ob.Outgoing, "Outgoing follows the prune — the partition still sums")
}

// D3: a FAILED threads call fails closed, exactly like a failed authored
// fetch (ADR 0016's "never merge on stale data", extended by ADR 0019): the
// outbound snapshot is cleared, ALL merging is skipped that cycle, and — like
// every failed outbound fetch — the armed set is untouched (consent is never
// withdrawn on missing data). The next healthy cycle recovers on its own.
func TestFailedThreadsFetchClearsOutboundAndMergesNothing(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))

	fake.ThreadsErr = errors.New("gh api graphql exploded")
	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.MergeCalls(), "no merge on guessed thread data — fail closed")
	require.Empty(t, eng.Merges())
	ob := eng.Outbound()
	require.Zero(t, ob.Outgoing, "a failed threads fetch clears the outbound snapshot")
	require.Empty(t, ob.Ready)
	require.True(t, eng.ArmedSet()[21], "a failed fetch never touches the armed set")

	// The next cycle's threads call succeeds: the still-armed Ready PR merges.
	fake.ThreadsErr = nil
	eng.RunCycleOnce(context.Background())
	require.Len(t, fake.MergeCalls(), 1, "the healthy cycle merges the still-armed PR")
}

// D4: level-triggered in BOTH directions — a NEW thread on an armed,
// previously-Ready PR moves it back to In Discussion (held again, no merge)
// while the arm stands untouched; there is no transition memory.
func TestNewThreadOnArmedReadyPRHoldsAgain(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "UNKNOWN"))

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	require.Len(t, eng.Outbound().Ready, 1, "no threads yet: the armed PR sits in Ready (unmerged only for its UNKNOWN mergeability)")

	// A reviewer opens a fresh thread on the armed, Ready PR.
	fake.SetThreads(21, github.RawReviewThreads{Nodes: []github.ReviewThread{{IsResolved: false}}})
	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.MergeCalls(), "held again — the merge step never saw it in Ready")
	ob := eng.Outbound()
	require.Empty(t, ob.Ready, "the PR left Ready for In Discussion")
	require.Len(t, ob.InDiscussion, 1)
	require.Equal(t, 21, ob.InDiscussion[0].Number)
	require.True(t, eng.ArmedSet()[21], "still armed — the hold is not a disarm")
}

// D5: the on-demand diff lookup covers the new stage (ADR 0017): a PR tracked
// only in In Discussion resolves its files and authoritative total exactly
// like every other outbound stage row.
func TestDiffOfInDiscussionPRReturnsFilesAndTotal(t *testing.T) {
	eng, fake := outboundEngine(t,
		github.PR{Number: 91, Title: "feat(api): approved with nits", Author: "me", URL: "u91",
			Additions: 12, Deletions: 2, ChangedFiles: 4,
			Checks: green(), ReviewDecision: "APPROVED"},
	)
	fake.SetThreads(91, github.RawReviewThreads{Nodes: []github.ReviewThread{{IsResolved: false}}})
	fake.SetDiff(91, []github.FileDiff{
		{Filename: "api.go", Status: "modified", Additions: 12, Deletions: 2, Patch: "@@ -1 +1 @@"},
	})
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Outbound().InDiscussion, 1, "precondition: the PR sits in In Discussion")

	files, total, err := eng.Diff(context.Background(), 91)
	require.NoError(t, err)
	require.Equal(t, 4, total, "total_files is the in-discussion item's changed_files")
	require.Equal(t, []github.FileDiff{
		{Filename: "api.go", Status: "modified", Additions: 12, Deletions: 2, Patch: "@@ -1 +1 @@"},
	}, files)
}
