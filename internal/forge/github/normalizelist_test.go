package github

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// decodeFixture decodes a recorded raw gh response from testdata into the
// adapter's own decode type. Every normalisation test that claims to run
// against a real response shape goes through here: no exec, no network, just
// the bytes gh actually emits (ADR 0030 §10).
func decodeFixture[T any](t *testing.T, name string) T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestNormalizeInboundList runs the whole inbound decode+normalise path over a
// RECORDED `gh pr list --json ...,statusCheckRollup,reviewDecision,headRefOid,files`
// response. It is the seam's end-to-end pure test: every field the cycle
// depends on rides ONE call, and every GitHub word on the way in
// (statusCheckRollup entries, reviewDecision) is gone by the time a PR leaves.
//
// The inbound pull does not request mergeable, so mergeability stays unknown —
// which blocks a merge without moving a stage.
func TestNormalizeInboundList(t *testing.T) {
	items := decodeFixture[[]ghListItem](t, "pr_list_inbound.json")
	require.Len(t, items, 3, "the fixture is the recorded response, not a subset")

	require.Equal(t, []forge.PR{
		{
			Number:       7,
			Title:        "chore(deps): bump doublestar to v4.10.0",
			Author:       "alice",
			URL:          "https://github.com/example/repo/pull/7",
			Additions:    12,
			Deletions:    3,
			ChangedFiles: 2,
			IsDraft:      false,
			// SUCCESS + SKIPPED CheckRuns and a SUCCESS StatusContext: all pass.
			Checks:         []forge.Check{{State: forge.CheckPass}, {State: forge.CheckPass}, {State: forge.CheckPass}},
			FailingChecks:  0,
			ReviewDecision: forge.ReviewNone, // REVIEW_REQUIRED is "nobody has decided"
			Mergeable:      forge.MergeableUnknown,
			Files:          []string{"go.mod", "go.sum"},
			HeadSHA:        "9f1c0b7ad3e5a1c2b4d6e8f0a2c4e6b8d0f2a4c6",
		},
		{
			Number:       8,
			Title:        "feat(api)!: drop the v1 endpoints",
			Author:       "bob",
			URL:          "https://github.com/example/repo/pull/8",
			Additions:    240,
			Deletions:    610,
			ChangedFiles: 14,
			IsDraft:      true,
			// IN_PROGRESS, FAILURE, PENDING: pending, fail, pending — three non-passing.
			Checks:         []forge.Check{{State: forge.CheckPending}, {State: forge.CheckFail}, {State: forge.CheckPending}},
			FailingChecks:  3,
			ReviewDecision: forge.ReviewNone,
			Mergeable:      forge.MergeableUnknown,
			Files:          []string{"internal/server/server.go"},
			HeadSHA:        "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		},
		{
			Number:         9,
			Title:          "docs: fix a typo",
			Author:         "carol",
			URL:            "https://github.com/example/repo/pull/9",
			Additions:      1,
			Deletions:      1,
			ChangedFiles:   1,
			IsDraft:        false,
			Checks:         nil, // an empty rollup: no signal, and nothing failing
			FailingChecks:  0,
			ReviewDecision: forge.ReviewApproved,
			Mergeable:      forge.MergeableUnknown,
			Files:          []string{"README.md"},
			HeadSHA:        "deadbeefcafe0000deadbeefcafe0000deadbeef",
		},
	}, normalizePRs(items))
}

// TestNormalizeAuthoredList runs the outbound decode+normalise path over a
// RECORDED authored `gh pr list` response — the one that requests mergeable
// and no files. It pins the two remaining raw vocabularies: gh's mergeable
// (MERGEABLE | CONFLICTING | UNKNOWN) and its reviewDecision, including what
// happens to a token GitHub adds later. Both degrade to the neutral "unknown /
// undecided" value rather than to a guess.
func TestNormalizeAuthoredList(t *testing.T) {
	items := decodeFixture[[]ghListItem](t, "pr_list_authored.json")
	require.Len(t, items, 4, "the fixture is the recorded response, not a subset")

	prs := normalizePRs(items)
	require.Len(t, prs, 4)

	for _, pr := range prs {
		require.Empty(t, pr.Files, "the authored pull requests no files — scope is inbound-only (ADR 0026)")
	}

	require.Equal(t, forge.MergeableUnknown, prs[0].Mergeable, "gh UNKNOWN is unknown mergeability")
	require.Equal(t, forge.ReviewNone, prs[0].ReviewDecision, "an absent reviewDecision is undecided")
	require.True(t, prs[0].IsDraft, "drafts ride the authored pull — draft is a stage, not a gate")
	require.Nil(t, prs[0].Checks)
	require.Equal(t, 0, prs[0].FailingChecks)

	require.Equal(t, forge.MergeableConflicting, prs[1].Mergeable)
	require.Equal(t, forge.ReviewApproved, prs[1].ReviewDecision)
	require.Equal(t, []forge.Check{{State: forge.CheckPass}}, prs[1].Checks)

	require.Equal(t, forge.MergeableMergeable, prs[2].Mergeable)
	require.Equal(t, forge.ReviewChangesRequested, prs[2].ReviewDecision)
	require.Equal(t, []forge.Check{{State: forge.CheckPass}}, prs[2].Checks, "a NEUTRAL conclusion passes")

	require.Equal(t, forge.MergeableUnknown, prs[3].Mergeable,
		"a mergeable value GitHub added later degrades to unknown, never to mergeable")
	require.Equal(t, forge.ReviewNone, prs[3].ReviewDecision,
		"a reviewDecision GitHub added later degrades to undecided, never to approved")
	require.Equal(t, []forge.Check{{State: forge.CheckPending}}, prs[3].Checks)
	require.Equal(t, 1, prs[3].FailingChecks, "an EXPECTED StatusContext is non-passing")
}
