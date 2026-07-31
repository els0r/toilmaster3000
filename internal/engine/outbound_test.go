package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// outboundEngine builds an engine over a fake with NO inbound candidates and
// the given authored PRs, so the outbound snapshot can be asserted in
// isolation. It returns the engine and the fake.
func outboundEngine(t *testing.T, authored ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)

	fake := github.NewFake()
	fake.Authored = authored
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng, fake
}

// O1 (tracer): one cycle folds every authored PR into exactly one outbound
// stage list — the outbound funnel partition — and Outgoing is the raw pull, so
// the distribution counts sum to it by construction. Drafts are included
// (draft is an outbound stage, not a gate), and mergeable rides each item.
func TestOutboundSnapshotPartition(t *testing.T) {
	eng, fake := outboundEngine(t,
		github.PR{Number: 1, Title: "feat(a): wip", Author: "me", URL: "u1", IsDraft: true, Checks: green()},
		github.PR{Number: 2, Title: "fix(b): broken", Author: "me", URL: "u2", Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}},
		github.PR{Number: 3, Title: "fix(c): building", Author: "me", URL: "u3", Checks: []github.Check{{Typename: "CheckRun", Status: "IN_PROGRESS"}}},
		github.PR{Number: 4, Title: "feat(d): objected", Author: "me", URL: "u4", Checks: green(), ReviewDecision: "CHANGES_REQUESTED"},
		github.PR{Number: 5, Title: "feat(e): pending review", Author: "me", URL: "u5", Checks: green(), ReviewDecision: "REVIEW_REQUIRED"},
		github.PR{Number: 6, Title: "feat(f): approved", Author: "me", URL: "u6", Checks: green(), ReviewDecision: "APPROVED", Mergeable: "CONFLICTING"},
	)

	eng.RunCycleOnce(context.Background())

	ob := eng.Outbound()
	require.Equal(t, 6, ob.Outgoing, "Outgoing is the raw authored pull")
	require.Len(t, ob.Draft, 1)
	require.Equal(t, 1, ob.Draft[0].Number)
	require.Len(t, ob.Red, 1)
	require.Equal(t, 2, ob.Red[0].Number)
	require.Len(t, ob.Running, 1)
	require.Equal(t, 3, ob.Running[0].Number)
	require.Len(t, ob.ChangesRequested, 1)
	require.Equal(t, 4, ob.ChangesRequested[0].Number)
	require.Len(t, ob.AwaitingApproval, 1)
	require.Equal(t, 5, ob.AwaitingApproval[0].Number)
	require.Len(t, ob.Ready, 1)
	require.Equal(t, 6, ob.Ready[0].Number)
	require.Equal(t, "CONFLICTING", ob.Ready[0].Mergeable, "mergeable rides the item for the Ready conflict marker")

	sum := len(ob.Draft) + len(ob.Red) + len(ob.Running) +
		len(ob.ChangesRequested) + len(ob.AwaitingApproval) + len(ob.Ready)
	require.Equal(t, ob.Outgoing, sum, "the stage lists partition the raw pull")

	require.Equal(t, 1, fake.AuthoredCallCount(), "one additional gh list call per cycle, no N+1")
	require.Empty(t, fake.ApprovedCalls(), "the outbound pull never feeds the approve path")
}

// O2: the outbound snapshot mirrors the inbound funnel lifecycle — the zero
// value after a restart until the first cycle.
func TestOutboundEmptyBeforeFirstCycle(t *testing.T) {
	eng, _ := outboundEngine(t, github.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})

	ob := eng.Outbound()
	require.Zero(t, ob.Outgoing)
	require.Empty(t, ob.AwaitingApproval)
}

// O3: a failed outbound fetch clears the snapshot — the robot never shows (or,
// in a later slice, merges on) stale authored data.
func TestOutboundClearedOnFailedFetch(t *testing.T) {
	eng, fake := outboundEngine(t, github.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})

	eng.RunCycleOnce(context.Background())
	require.Equal(t, 1, eng.Outbound().Outgoing, "first cycle populates the snapshot")

	fake.AuthoredErr = errors.New("gh exploded")
	eng.RunCycleOnce(context.Background())
	ob := eng.Outbound()
	require.Zero(t, ob.Outgoing, "a failed outbound fetch clears the snapshot")
	require.Empty(t, ob.AwaitingApproval)
}

// O4: the two pulls fail independently — a failed INBOUND fetch skips the
// inbound cycle but the outbound snapshot is still rebuilt from its own call.
func TestOutboundRebuiltDespiteFailedInboundFetch(t *testing.T) {
	eng, fake := outboundEngine(t, github.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})
	fake.ListErr = errors.New("inbound gh exploded")

	eng.RunCycleOnce(context.Background())

	require.Equal(t, 1, eng.Outbound().Outgoing, "outbound rides its own fetch, not the inbound one")
}
