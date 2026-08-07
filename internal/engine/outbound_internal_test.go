package engine

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// TestArmSurvivesAnUnanticipatedStage is the property the open stage set is
// bought for (ADR 0025): a stage the production code has never heard of must
// not silently withdraw merge consent. The disarm rule is stated by EXCLUSION
// ("everything except Changes Requested"), so a new stage is armable by
// default at both of its consumers at once — Arm's gate and reconcileArmed's
// keep loop.
//
// It must be an internal test: the public path cannot produce an unknown
// stage, so the eighth stage is hand-built into the partition here (the
// staging_internal_test.go / manualapprove_internal_test.go precedent).
func TestArmSurvivesAnUnanticipatedStage(t *testing.T) {
	const hypothetical = github.OutboundStage("hypothetical")

	arms, err := armed.NewStore(filepath.Join(t.TempDir(), "armed.json"))
	require.NoError(t, err)
	require.NoError(t, arms.Arm(42), "the operator armed #42 while it sat in the new stage")
	e := &Engine{armed: arms, logger: slog.Default()}

	ob := Outbound{hypothetical: []OutboundItem{
		{Number: 42, Title: "feat(x): in a stage nobody has thought of yet", Author: "me", URL: "u42", ChangedFiles: 3},
	}}

	require.True(t, armable(hypothetical),
		"armable is stated by exclusion, so an unknown stage is armable — inverting it to an inclusion list would break this")

	e.reconcileArmed(ob)

	require.True(t, e.armed.IsArmed(42),
		"an unanticipated stage must never withdraw consent — the keep loop ranges the partition, it does not list stages")

	it, stage, found := ob.find(42)
	require.True(t, found, "find ranges the map, so it reaches a stage it was never told about")
	require.Equal(t, hypothetical, stage)
	require.Equal(t, 3, it.ChangedFiles, "the item comes back whole — this is what the Diff card resolves")
}
