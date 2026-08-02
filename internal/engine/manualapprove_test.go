package engine_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// queueEngine builds an engine over the given candidates with one Review Rule
// (any docs-typed PR) so a green docs PR lands in the Needs-Human-Review queue
// in one cycle — the manual-override subject under test. It returns the engine
// and the fake.
func queueEngine(t *testing.T, candidates ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	_, err = store.Create(rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"})
	require.NoError(t, err)

	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, statePath, tempMerges(t), store, testArms(t))
	require.NoError(t, err)
	return eng, fake
}

// queuedDocsPR is a green docs PR the seeded Review Rule routes to the queue.
func queuedDocsPR(number int) github.PR {
	return github.PR{Number: number, Title: "docs: gate me", Author: "b", URL: "u12", Checks: green()}
}

// MA1 (tracer): a manual override approve removes the PR from the queue
// snapshot immediately — the engine performed the approval, so serving the PR
// in Needs-Human-Review until the next cycle rebuild would be a known
// falsehood, not honest staleness (ADR 0018).
func TestManualApproveRemovesQueueItemImmediately(t *testing.T) {
	eng, fake := queueEngine(t, queuedDocsPR(12))

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "the docs PR is queued by the Review Rule")

	require.NoError(t, eng.ApproveManually(context.Background(), 12))

	require.Len(t, fake.ApprovedCalls(), 1, "the override actually approved on GitHub")
	require.Empty(t, eng.Queue(), "the approved item leaves the queue immediately, not next cycle")
}

// MA2: the manual approve moves the PR's funnel count between terminal
// segments — NeedsHumanReview to ApprovedByTm3k — so the partition still sums
// to Incoming by construction, and the heartbeat's queue count follows the
// queue. ApprovedThisCycle stays untouched: it is the cycle's pulse, and a
// manual override is not the cycle's doing.
func TestManualApproveMovesFunnelAndStatusCounts(t *testing.T) {
	eng, _ := queueEngine(t, queuedDocsPR(12))

	eng.RunCycleOnce(context.Background())
	require.Equal(t, 1, eng.Funnel().NeedsHumanReview, "the queued PR counts as needs-human-review")

	require.NoError(t, eng.ApproveManually(context.Background(), 12))

	f := eng.Funnel()
	require.Zero(t, f.NeedsHumanReview, "the approved PR left the needs-human-review segment")
	require.Equal(t, 1, f.ApprovedByTm3k, "and became an approved-by-tm3k member")
	require.Zero(t, f.ApprovedThisCycle, "the cycle pulse is untouched by a manual override")
	sum := len(f.DroppedRed) + len(f.DroppedDraft) + len(f.Staging) +
		f.NeedsHumanReview + f.ApprovedByTm3k + len(f.ApprovedElsewhere)
	require.Equal(t, f.Incoming, sum, "the partition still sums to Incoming")

	require.Zero(t, eng.Status().QueueCount, "the heartbeat's queue count follows the queue")
}
