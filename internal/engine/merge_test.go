package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// tempMerges returns a throwaway merges.jsonl path — the merge ledger every
// engine test that does not assert merging itself wires in.
func tempMerges(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "merges.jsonl")
}

// mergeEngine builds an engine over a fake with NO inbound candidates, the
// given authored PRs, and a merge ledger over the given merges.jsonl path (so
// a test can rebuild an engine over the same file to prove persistence). It
// returns the engine and the fake.
func mergeEngine(t *testing.T, mergesPath string, authored ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	arms, err := armed.NewStore(filepath.Join(t.TempDir(), "armed.json"))
	require.NoError(t, err)

	fake := github.NewFake()
	fake.Authored = authored
	eng, err := engine.New(fake, statePath, mergesPath, store, arms)
	require.NoError(t, err)
	return eng, fake
}

// readyPR is an authored PR in the Ready stage (green + APPROVED) with the
// given mergeability — the merge step's subject under test.
func readyPR(number int, mergeable string) github.PR {
	return github.PR{
		Number: number, Title: "feat(x)!: stale arm-time title", Author: "me",
		URL: "https://github.com/o/r/pull/21", Checks: green(),
		ReviewDecision: "APPROVED", Mergeable: mergeable,
	}
}

// M1 (tracer): an armed PR that is green + APPROVED + MERGEABLE is merged the
// first cycle those conditions hold — squash with the gh-land commit message
// built from the LIVE gh pr view details (never a stale list-time copy), and
// the success is appended to the merge ledger. A breaking (!) title merges
// like any other: the arm IS the human decision (the inbound Invariant is a
// rules concern and is untouched).
func TestArmedReadyPRMerges(t *testing.T) {
	mergesPath := tempMerges(t)
	eng, fake := mergeEngine(t, mergesPath, readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat(x)!: live title",
		Body:  "Live body.",
		Reviews: []github.Review{
			{Author: "alice_osag", State: "APPROVED"},
			{Author: "bob", State: "COMMENTED"},
		},
	})

	eng.RunCycleOnce(context.Background())
	require.Empty(t, fake.MergeCalls(), "a Withheld PR never merges — consent first")
	require.NoError(t, eng.Arm(21))

	eng.RunCycleOnce(context.Background())

	require.Equal(t, []github.MergeCall{
		{Number: 21, Subject: "feat(x)!: live title", Body: "Live body.\nApproved by: alice"},
	}, fake.MergeCalls(), "one squash merge with the gh-land commit message from the live details")

	merges := eng.Merges()
	require.Len(t, merges, 1)
	require.Equal(t, 21, merges[0].Number)
	require.Equal(t, "feat(x)!: live title", merges[0].Title, "the ledger records what landed (the live title)")
	require.Equal(t, "https://github.com/o/r/pull/21", merges[0].URL)
	require.Equal(t, []string{"alice"}, merges[0].ApprovedBy)
	require.False(t, merges[0].MergedAt.IsZero())
}

// M2: the merge ledger is durable — a fresh engine over the same merges.jsonl
// loads the record, newest-first (the approvals.jsonl analog: a merged PR
// leaves the is:open pull immediately, so the ledger is the only signal left).
func TestMergeLedgerSurvivesRestart(t *testing.T) {
	mergesPath := tempMerges(t)
	eng, fake := mergeEngine(t, mergesPath, readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})
	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Merges(), 1)

	restarted, _ := mergeEngine(t, mergesPath)
	merges := restarted.Merges()
	require.Len(t, merges, 1, "the ledger survives a restart")
	require.Equal(t, 21, merges[0].Number)
	require.Equal(t, []string{"alice"}, merges[0].ApprovedBy)
}

// M3: a not-MERGEABLE Ready PR never merges but KEEPS its stage: CONFLICTING
// (fixing the conflict is on you — exactly what Ready means) and UNKNOWN
// (GitHub still computing; retried naturally next cycle) both block the merge
// without moving the PR.
func TestNotMergeableBlocksMergeButKeepsStage(t *testing.T) {
	for _, mergeable := range []string{"CONFLICTING", "UNKNOWN"} {
		t.Run(mergeable, func(t *testing.T) {
			eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, mergeable))

			eng.RunCycleOnce(context.Background())
			require.NoError(t, eng.Arm(21))
			eng.RunCycleOnce(context.Background())

			require.Empty(t, fake.MergeCalls(), "a %s PR never merges", mergeable)
			require.Empty(t, eng.Merges(), "nothing lands in the ledger")
			ob := eng.Outbound()
			require.Len(t, ob.Ready, 1, "the PR stays in Ready — mergeable is a precondition, not a stage boundary")
			require.Equal(t, 21, ob.Ready[0].Number)
		})
	}
}

// M4: an armed PR outside Ready never merges — the merge preconditions are the
// stage's (green + APPROVED), so an armed awaiting-approval PR waits.
func TestArmedNotReadyNeverMerges(t *testing.T) {
	pr := readyPR(21, "MERGEABLE")
	pr.ReviewDecision = "REVIEW_REQUIRED" // green but unapproved: Awaiting Approval
	eng, fake := mergeEngine(t, tempMerges(t), pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.MergeCalls())
	require.Empty(t, eng.Merges())
}

// M5: a failed outbound fetch skips ALL merging that cycle — the robot never
// merges on stale data (ADR 0016).
func TestFailedOutboundFetchMergesNothing(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))

	fake.AuthoredErr = errors.New("gh exploded")
	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.MergeCalls(), "no merge on a failed fetch")
	require.Empty(t, eng.Merges())
}

// M6: a transiently failing merge call gets ONE immediate retry (gh-land
// parity) and lands on the second attempt — still exactly one ledger record.
func TestFailedMergeRetriesOnceImmediately(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})
	fake.FailMerge(21, 1) // first attempt fails, the immediate retry succeeds

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())

	require.Len(t, fake.MergeCalls(), 2, "the failed attempt is retried once immediately")
	require.Len(t, eng.Merges(), 1, "the retried success appends exactly one record")
}

// M7: when both the attempt and its immediate retry fail, the cycle moves on —
// the ledger stays untouched (append only on success) and the STILL-armed PR is
// retried on the next cycle, where it lands.
func TestFailedMergeRetriesNextCycle(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})
	fake.FailMerge(21, 2) // attempt + immediate retry both fail this cycle

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())

	require.Len(t, fake.MergeCalls(), 2, "attempt + one immediate retry, then give up this cycle")
	require.Empty(t, eng.Merges(), "a failed merge appends nothing")
	require.True(t, eng.ArmedSet()[21], "the PR stays armed for the next cycle")

	eng.RunCycleOnce(context.Background())
	require.Len(t, fake.MergeCalls(), 3, "the next cycle retries the merge")
	require.Len(t, eng.Merges(), 1, "the retried success lands in the ledger")
}

// M8: a failed merge-info fetch (the per-merge gh pr view) skips that PR's
// merge — the commit message is built from live details or not at all.
func TestFailedMergeInfoSkipsMerge(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.MergeInfoErr = errors.New("gh pr view exploded")

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.MergeCalls(), "no merge without live details")
	require.Empty(t, eng.Merges())
}

// M9: the merged PR leaves the is:open pull, so the NEXT cycle's slice-3
// reconciliation cleans its armed entry — the merge step itself does not need
// to touch the armed set (verified here rather than duplicated).
func TestMergedPRArmedEntryCleanedUp(t *testing.T) {
	eng, fake := mergeEngine(t, tempMerges(t), readyPR(21, "MERGEABLE"))
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat: t", Body: "b",
		Reviews: []github.Review{{Author: "alice", State: "APPROVED"}},
	})

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Merges(), 1, "the PR merged")

	// Merged: the PR is gone from the authored pull next cycle.
	fake.Authored = nil
	eng.RunCycleOnce(context.Background())

	require.False(t, eng.ArmedSet()[21], "the merged PR's armed entry is cleaned up by the existing reconciliation")
	require.Len(t, fake.MergeCalls(), 1, "no re-merge attempt")
}
