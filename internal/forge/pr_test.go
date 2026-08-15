package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestZeroPRIsInert pins the zero value of every enum a PR carries to the
// SAME value a real adapter emits when the forge answered "nothing" — so a PR
// nobody populated is indistinguishable from one the adapter reported unknown
// for.
//
// This is not pedantry about zero values: two things in this codebase build
// PRs by hand — forge.Fake's canned candidates and the engine's synthetic
// manual-approve PR. If their zero fields differed from the adapter's
// "unknown", every Fake-driven test would exercise a branch production never
// reaches, and the suite would stop being evidence about production.
func TestZeroPRIsInert(t *testing.T) {
	var pr forge.PR

	require.Equal(t, forge.MergeableUnknown, pr.Mergeable,
		"an unpopulated mergeability IS unknown — the adapter's value for a forge that did not answer")
	require.NotEqual(t, forge.MergeableMergeable, pr.Mergeable,
		"an unpopulated PR never clears the merge precondition")

	require.Equal(t, forge.ReviewNone, pr.ReviewDecision,
		"an unpopulated review decision IS undecided")
	require.NotEqual(t, forge.ReviewApproved, pr.ReviewDecision,
		"an unpopulated PR is never approved-elsewhere (ADR 0013's soft dedup must not misfire)")

	require.False(t, forge.AllGreen(pr.Checks),
		"an unpopulated rollup is no signal, and no signal is not green")
	require.Equal(t, 0, pr.FailingChecks)
}
