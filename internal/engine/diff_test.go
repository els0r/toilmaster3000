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

	eng, err := engine.New(fake, statePath, store)
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

// E-diff-2: Diff of a PR not in the queue is ErrNotInQueue — the pill is
// queue-only, so a number absent from the queue snapshot never reaches gh.
func TestDiffOfUnqueuedPRIsNotInQueue(t *testing.T) {
	eng, fake := queuedBreakingEngine(t)

	_, _, err := eng.Diff(context.Background(), 999)
	require.ErrorIs(t, err, engine.ErrNotInQueue)
	require.Empty(t, fake.DiffCalls(), "an unqueued number never reaches the gh diff call")
}
