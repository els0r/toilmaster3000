package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// outboundEngine builds an engine over a fake with NO inbound candidates and
// the given authored PRs, so the outbound snapshot can be asserted in
// isolation. It returns the engine and the fake.
func outboundEngine(t *testing.T, authored ...forge.PR) (*engine.Engine, *forge.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)

	fake := forge.NewFake()
	fake.Authored = authored
	eng, err := engine.New(fake, statePath, tempMerges(t), store, testArms(t), nil)
	require.NoError(t, err)
	return eng, fake
}

// O1 (tracer): one cycle keys every authored PR under exactly one outbound
// stage — the outbound funnel partition — and Outgoing, derived by folding it,
// equals the raw pull. Drafts are included (draft is an outbound stage, not a
// gate), mergeable rides each item, and an approved PR with an unresolved
// review thread lands in In Discussion, not Ready (ADR 0019).
func TestOutboundSnapshotPartition(t *testing.T) {
	authored := []forge.PR{
		{Number: 1, Title: "feat(a): wip", Author: "me", URL: "u1", IsDraft: true, Checks: green()},
		{Number: 2, Title: "fix(b): broken", Author: "me", URL: "u2", Checks: []forge.Check{{State: forge.CheckFail}}, FailingChecks: 1},
		{Number: 3, Title: "fix(c): building", Author: "me", URL: "u3", Checks: []forge.Check{{State: forge.CheckPending}}, FailingChecks: 1},
		{Number: 4, Title: "feat(d): objected", Author: "me", URL: "u4", Checks: green(), ReviewDecision: forge.ReviewChangesRequested},
		{Number: 5, Title: "feat(e): pending review", Author: "me", URL: "u5", Checks: green(), ReviewDecision: forge.ReviewNone},
		{Number: 6, Title: "feat(f): approved", Author: "me", URL: "u6", Checks: green(), ReviewDecision: forge.ReviewApproved, Mergeable: forge.MergeableConflicting},
		{Number: 7, Title: "feat(g): approved with nits", Author: "me", URL: "u7", Checks: green(), ReviewDecision: forge.ReviewApproved, Mergeable: forge.MergeableMergeable},
	}
	eng, fake := outboundEngine(t, authored...)
	fake.SetThreads(7, forge.ReviewThreads{Nodes: []forge.ReviewThread{{IsResolved: false}}})

	eng.RunCycleOnce(context.Background())

	ob := eng.Outbound()
	require.Len(t, ob[forge.OutboundStageDraft], 1)
	require.Equal(t, 1, ob[forge.OutboundStageDraft][0].Number)
	require.Len(t, ob[forge.OutboundStageRed], 1)
	require.Equal(t, 2, ob[forge.OutboundStageRed][0].Number)
	require.Len(t, ob[forge.OutboundStageRunning], 1)
	require.Equal(t, 3, ob[forge.OutboundStageRunning][0].Number)
	require.Len(t, ob[forge.OutboundStageChangesRequested], 1)
	require.Equal(t, 4, ob[forge.OutboundStageChangesRequested][0].Number)
	require.Len(t, ob[forge.OutboundStageAwaitingApproval], 1)
	require.Equal(t, 5, ob[forge.OutboundStageAwaitingApproval][0].Number)
	require.Len(t, ob[forge.OutboundStageInDiscussion], 1)
	require.Equal(t, 7, ob[forge.OutboundStageInDiscussion][0].Number)
	require.Len(t, ob[forge.OutboundStageReady], 1)
	require.Equal(t, 6, ob[forge.OutboundStageReady][0].Number)
	require.Equal(t, forge.MergeableConflicting, ob[forge.OutboundStageReady][0].Mergeable, "mergeable rides the item for the Ready conflict marker")

	// The raw-pull cross-check: Outgoing is DERIVED by folding the partition, so
	// this is the witness that the partition really did absorb the whole pull —
	// no PR dropped on the floor, none counted twice (ADR 0025).
	require.Equal(t, len(authored), ob.Outgoing(), "the stage lists partition the raw pull")

	require.Equal(t, 1, fake.AuthoredCallCount(), "one additional gh list call per cycle, no N+1")
	require.Empty(t, fake.ApprovedCalls(), "the outbound pull never feeds the approve path")
}

// O2: the outbound snapshot mirrors the inbound funnel lifecycle — empty after
// a restart until the first cycle.
func TestOutboundEmptyBeforeFirstCycle(t *testing.T) {
	eng, _ := outboundEngine(t, forge.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})

	ob := eng.Outbound()
	require.Zero(t, ob.Outgoing())
	require.Empty(t, ob[forge.OutboundStageAwaitingApproval])
}

// O3: a failed outbound fetch clears the snapshot — the robot never shows (or,
// in a later slice, merges on) stale authored data.
func TestOutboundClearedOnFailedFetch(t *testing.T) {
	eng, fake := outboundEngine(t, forge.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})

	eng.RunCycleOnce(context.Background())
	require.Equal(t, 1, eng.Outbound().Outgoing(), "first cycle populates the snapshot")

	fake.AuthoredErr = errors.New("gh exploded")
	eng.RunCycleOnce(context.Background())
	ob := eng.Outbound()
	require.Zero(t, ob.Outgoing(), "a failed outbound fetch clears the snapshot")
	require.Empty(t, ob[forge.OutboundStageAwaitingApproval])
}

// O4: the two pulls fail independently — a failed INBOUND fetch skips the
// inbound cycle but the outbound snapshot is still rebuilt from its own call.
func TestOutboundRebuiltDespiteFailedInboundFetch(t *testing.T) {
	eng, fake := outboundEngine(t, forge.PR{Number: 1, Title: "feat: x", Author: "me", URL: "u1", Checks: green()})
	fake.ListErr = errors.New("inbound gh exploded")

	eng.RunCycleOnce(context.Background())

	require.Equal(t, 1, eng.Outbound().Outgoing(), "outbound rides its own fetch, not the inbound one")
}
