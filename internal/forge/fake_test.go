package forge_test

import (
	"context"
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestFakeRejectsAFailingRollupWithNoFailingCount guards the one seam fact a
// hand-built PR can silently get wrong now that FailingChecks is
// adapter-supplied rather than folded (ADR 0030 §6).
//
// Before the seam split, a fixture only had to set Checks — the count was
// folded from them, so it could not disagree. Now a fixture sets two fields,
// and a fixture that sets only Checks quietly claims "nothing is failing"
// about a red PR. That is not a hypothetical: it happened in this very
// refactor, and the dropped-red station would have rendered "0 checks
// failing".
//
// The guard is DIRECTIONAL, not an equality check, because the exact count is
// a forge fact: GitHub counts non-passing rollup entries, GitLab counts failed
// jobs behind a single pipeline entry (ADR 0030 §6), so entries and count need
// not match. What holds on every forge is that a failure implies a nonzero
// count.
func TestFakeRejectsAFailingRollupWithNoFailingCount(t *testing.T) {
	red := forge.PR{Number: 5, Checks: []forge.Check{{State: forge.CheckFail}}}

	require.PanicsWithError(t,
		"forge.Fake: PR #5 has a failing check but FailingChecks is 0 — a canned PR must carry the count its adapter would supply (ADR 0030 §6)",
		func() {
			_, _ = forge.NewFake(red).ListCandidates(context.Background())
		},
		"a canned inbound PR whose rollup contradicts its count is a fixture bug, not a test case")

	require.PanicsWithError(t,
		"forge.Fake: PR #5 has a failing check but FailingChecks is 0 — a canned PR must carry the count its adapter would supply (ADR 0030 §6)",
		func() {
			f := forge.NewFake()
			f.Authored = []forge.PR{red}
			_, _ = f.ListAuthored(context.Background())
		},
		"the authored pull is canned the same way and gets the same guard")
}

// TestFakeAcceptsCoherentRollups pins what the guard must NOT reject, so it
// cannot drift into an equality check that a GitLab-shaped adapter would fail.
func TestFakeAcceptsCoherentRollups(t *testing.T) {
	ok := []forge.PR{
		{Number: 1, Checks: []forge.Check{{State: forge.CheckPass}}},
		{Number: 2, Checks: []forge.Check{{State: forge.CheckFail}}, FailingChecks: 1},
		{Number: 3, Checks: []forge.Check{{State: forge.CheckFail}, {State: forge.CheckPending}}, FailingChecks: 2},
		// A pending entry is not a failure, so it implies nothing about the count.
		{Number: 4, Checks: []forge.Check{{State: forge.CheckPending}}},
		// One pipeline entry, seven failed jobs behind it — the GitLab shape.
		{Number: 5, Checks: []forge.Check{{State: forge.CheckFail}}, FailingChecks: 7},
		// No rollup at all: nothing ran, nothing is failing.
		{Number: 6},
	}

	prs, err := forge.NewFake(ok...).ListCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 6)
}
