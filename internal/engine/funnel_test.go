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

// funnelEngine builds an engine over the given candidates with one permissive
// Approve Rule (any chore-typed PR) so the four terminal funnel buckets are all
// reachable in one cycle: a chore PR that is eligible approves, a non-chore
// eligible PR with no matching rule falls through to Staging, drafts and
// not-all-green PRs drop, and an APPROVED-elsewhere PR is left alone. It returns
// the engine and the fake so a test can drive RunCycleOnce and read the snapshot.
func funnelEngine(t *testing.T, candidates ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	_, err = store.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)

	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng, fake
}

func green() []github.Check {
	return []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}}
}

// F1 (tracer): a draft PR is dropped by the Ready-for-Review gate and retained
// in the funnel snapshot's dropped_draft list — what the cycle used to discard
// as a bare count is now itemized.
func TestFunnelDroppedDraft(t *testing.T) {
	pr := github.PR{Number: 3, Title: "chore: wip", Author: "ann", URL: "u3", IsDraft: true, Checks: green()}
	eng, _ := funnelEngine(t, pr)

	eng.RunCycleOnce(context.Background())

	f := eng.Funnel()
	require.Len(t, f.DroppedDraft, 1, "the draft PR is itemized in dropped_draft")
	require.Equal(t, 3, f.DroppedDraft[0].Number)
	require.Equal(t, "chore: wip", f.DroppedDraft[0].Title)
}

// F2: a not-all-green PR is dropped by the All-Green gate into dropped_red,
// carrying the count of non-passing checks folded from the rollup already in
// hand (the "N checks failing" station signal).
func TestFunnelDroppedRedCarriesFailingCount(t *testing.T) {
	pr := github.PR{Number: 5, Title: "chore: flaky", Author: "ben", URL: "u5", Checks: []github.Check{
		{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		{Typename: "StatusContext", State: "PENDING"},
	}}
	eng, _ := funnelEngine(t, pr)

	eng.RunCycleOnce(context.Background())

	f := eng.Funnel()
	require.Len(t, f.DroppedRed, 1, "the not-all-green PR is itemized in dropped_red")
	require.Equal(t, 5, f.DroppedRed[0].Number)
	require.Equal(t, 2, f.DroppedRed[0].FailingChecks, "one FAILURE + one PENDING are both non-passing")
	require.Empty(t, f.DroppedDraft, "a ready not-all-green PR is red, not draft")
}

// F3: an eligible, parseable PR that matches no rule falls through to Staging —
// genuinely new, invisible before — itemized so the user can drain it with a
// rule. (The seeded rule matches only chore; a feat PR matches nothing.)
func TestFunnelStaging(t *testing.T) {
	pr := github.PR{Number: 8, Title: "feat(ui): new panel", Author: "cara", URL: "u8", Checks: green()}
	eng, fake := funnelEngine(t, pr)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "a no-rule-match PR is never approved")
	f := eng.Funnel()
	require.Len(t, f.Staging, 1, "the unmatched eligible PR is itemized in staging")
	require.Equal(t, 8, f.Staging[0].Number)
}

// F4: an APPROVED-but-not-ours PR (absent from approvals.jsonl) is left alone as
// a soft dedup and itemized in approved_elsewhere — the highlighted "left alone"
// segment, distinct from approved_this_cycle.
func TestFunnelApprovedElsewhere(t *testing.T) {
	pr := github.PR{Number: 9, Title: "chore: tidy", Author: "dee", URL: "u9", Checks: green(), ReviewDecision: "APPROVED"}
	eng, fake := funnelEngine(t, pr)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "an approved-elsewhere PR is never approved by tm3k")
	f := eng.Funnel()
	require.Len(t, f.ApprovedElsewhere, 1, "the approved-elsewhere PR is itemized")
	require.Equal(t, 9, f.ApprovedElsewhere[0].Number)
	require.Zero(t, f.ApprovedThisCycle, "approved-elsewhere is not approved-this-cycle")
}

// F5: the six terminal segments partition Incoming — every Incoming PR lands in
// EXACTLY one stage, so the counts reconcile. One cycle is fed a candidate for
// each of the six segments (plus a standing prior-approval) and the distribution
// counts are asserted to sum to Incoming.
func TestFunnelPartitionSumsToIncoming(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	// An Approve Rule (any chore) and a Review Rule (any docs) so all six segments
	// are reachable in one cycle.
	_, err = store.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)
	_, err = store.Create(rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"})
	require.NoError(t, err)

	redChecks := []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}
	candidates := []github.PR{
		{Number: 1, Title: "chore: approve me", Author: "a", URL: "u1", Checks: green()},                            // approved this cycle -> ApprovedByTm3k
		{Number: 2, Title: "docs: gate me", Author: "b", URL: "u2", Checks: green()},                                // review match -> NeedsHumanReview
		{Number: 3, Title: "feat: no rule", Author: "c", URL: "u3", Checks: green()},                                // no match -> Staging
		{Number: 4, Title: "chore: draft", Author: "d", URL: "u4", IsDraft: true, Checks: green()},                  // draft -> DroppedDraft
		{Number: 5, Title: "chore: red", Author: "e", URL: "u5", Checks: redChecks},                                 // red -> DroppedRed
		{Number: 6, Title: "chore: elsewhere", Author: "f", URL: "u6", Checks: green(), ReviewDecision: "APPROVED"}, // approved elsewhere
	}
	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)

	// First cycle approves #1 (it becomes a dedup member); run a second cycle with
	// #1 still in the pull so it shows as the STANDING Approved-by-tm3k segment
	// (any day), exercising the prior-approval branch distinct from this-cycle.
	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())

	f := eng.Funnel()
	sum := len(f.DroppedRed) + len(f.DroppedDraft) + len(f.Staging) +
		f.NeedsHumanReview + f.ApprovedByTm3k + len(f.ApprovedElsewhere)
	require.Equal(t, f.Incoming, sum, "the six terminal segments partition Incoming")
	require.Equal(t, 6, f.Incoming)
	require.Equal(t, 1, len(f.DroppedRed))
	require.Equal(t, 1, len(f.DroppedDraft))
	require.Equal(t, 1, len(f.Staging))
	require.Equal(t, 1, f.NeedsHumanReview)
	require.Equal(t, 1, len(f.ApprovedElsewhere))
	require.Equal(t, 1, f.ApprovedByTm3k, "#1 is a STANDING dedup member still in the pull")
	require.Zero(t, f.ApprovedThisCycle, "#1 was approved on the FIRST cycle, not this one")
}

// F6: the snapshot lifecycle mirrors the queue — the zero value after a restart
// (no cycle yet), current as of the last completed cycle, and CLEARED by a failed
// candidate fetch so stale buckets are never shown.
func TestFunnelLifecycle(t *testing.T) {
	pr := github.PR{Number: 2, Title: "feat: stage", Author: "g", URL: "u2", Checks: green()}
	eng, fake := funnelEngine(t, pr)

	// Before any cycle: the zero value (empty after restart).
	pre := eng.Funnel()
	require.Zero(t, pre.Incoming)
	require.Empty(t, pre.Staging)

	// After a cycle: current snapshot with the staged PR.
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Funnel().Staging, 1, "the snapshot is current after a cycle")

	// A failed fetch clears it: nothing was evaluated, so no stale buckets show.
	fake.ListErr = context.DeadlineExceeded
	eng.RunCycleOnce(context.Background())
	cleared := eng.Funnel()
	require.Zero(t, cleared.Incoming, "a failed fetch clears Incoming")
	require.Empty(t, cleared.Staging, "a failed fetch clears the staging list")
}
