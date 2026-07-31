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

// queuedBreakingEngine builds an engine whose single cycle routes one breaking
// PR (number 41) into the Needs-Human-Review queue, so its Diff pill is
// fetchable. The PR reports changed_files=12 but the fake serves a one-file
// page, mirroring the real "page cap below total" case.
func queuedBreakingEngine(t *testing.T) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	_, err = store.Create(rule.Rule{Name: "any typed", Enabled: true, TypeInclude: ".*"})
	require.NoError(t, err)

	pr := github.PR{Number: 41, Title: "chore!: drop legacy flag", Author: "bob", URL: "u41",
		Additions: 40, Deletions: 12, ChangedFiles: 12,
		Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}}}
	fake := github.NewFake(pr)
	fake.SetDiff(41, []github.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1 @@"},
	})

	eng, err := engine.New(fake, statePath, tempMerges(t), store, testArms(t))
	require.NoError(t, err)
	eng.RunCycleOnce(context.Background())
	return eng, fake
}

// E-diff-1: Diff of a queued PR returns its fetched changed files alongside the
// authoritative total — the queue item's changed_files — so the caller can show
// "first N of M files" without an extra gh call.
func TestDiffOfQueuedPRReturnsFilesAndTotal(t *testing.T) {
	eng, _ := queuedBreakingEngine(t)

	files, total, err := eng.Diff(context.Background(), 41)
	require.NoError(t, err)
	require.Equal(t, 12, total, "total_files is the queued PR's changed_files, not the fetched count")
	require.Equal(t, []github.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1 @@"},
	}, files)
}

// E-diff-2: Diff of a PR tracked in neither the queue nor Staging is
// ErrPRNotTracked, and never reaches gh.
func TestDiffOfUntrackedPRIsNotTracked(t *testing.T) {
	eng, fake := queuedBreakingEngine(t)

	_, _, err := eng.Diff(context.Background(), 999)
	require.ErrorIs(t, err, engine.ErrPRNotTracked)
	require.Empty(t, fake.DiffCalls(), "an untracked number never reaches the gh diff call")
}

// stagedEngine builds an engine whose single cycle leaves one eligible PR
// (number 70) in Staging — no rule matches it, so it falls through to Staging
// rather than the queue. Its Diff pill must resolve exactly like a queued PR's.
func stagedEngine(t *testing.T) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	// No rules: an eligible PR matching nothing falls through to Staging.

	pr := github.PR{Number: 70, Title: "feat: a new panel", Author: "camille", URL: "u70",
		Additions: 120, Deletions: 8, ChangedFiles: 5,
		Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}}}
	fake := github.NewFake(pr)
	fake.SetDiff(70, []github.FileDiff{
		{Filename: "panel.go", Status: "added", Additions: 118, Deletions: 0, Patch: "@@ -0,0 +1 @@"},
	})

	eng, err := engine.New(fake, statePath, tempMerges(t), store, testArms(t))
	require.NoError(t, err)
	eng.RunCycleOnce(context.Background())
	return eng, fake
}

// E-diff-3: Diff of a Staging PR (eligible, matched no rule) returns its
// fetched changed files alongside its authoritative total — the on-demand Diff
// pill now works from Staging exactly as it already does from the queue.
func TestDiffOfStagedPRReturnsFilesAndTotal(t *testing.T) {
	eng, _ := stagedEngine(t)

	files, total, err := eng.Diff(context.Background(), 70)
	require.NoError(t, err)
	require.Equal(t, 5, total, "total_files is the staged PR's changed_files")
	require.Equal(t, []github.FileDiff{
		{Filename: "panel.go", Status: "added", Additions: 118, Deletions: 0, Patch: "@@ -0,0 +1 @@"},
	}, files)
}

// E-diff-4: a PR that is ONLY in Staging (never queued) must never become
// manually-approvable — ApproveManually is the queue's own override path, and
// widening Diff's scope must not widen this one too (a Staging PR hasn't
// matched any rule yet).
func TestApproveManuallyStillRejectsAStagedPR(t *testing.T) {
	eng, _ := stagedEngine(t)

	err := eng.ApproveManually(context.Background(), 70)
	require.ErrorIs(t, err, engine.ErrNotInQueue)
}
