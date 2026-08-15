package github

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestNormalizeMergeDetails runs the merge-time decode+normalise path over a
// RECORDED `gh pr view --json title,body,reviews` response — the sanctioned
// per-merge call (ADR 0016). It pins gh's per-review state vocabulary:
// APPROVED is the only verdict ApprovedBy counts, and CHANGES_REQUESTED is the
// only other one worth naming. COMMENTED, DISMISSED, PENDING and anything
// GitHub adds later all collapse to "no verdict" — none of them can reach the
// commit trailer.
//
// Login normalisation (the "app/" prefix, the "_osag" suffix, the dedupe) is
// deliberately NOT done here: it is gh-land commit-message parity, which is
// the shared fold's judgement, not GitHub's vocabulary.
func TestNormalizeMergeDetails(t *testing.T) {
	item := decodeFixture[ghViewItem](t, "pr_view.json")
	require.Len(t, item.Reviews, 6, "the fixture is the recorded response, not a subset")

	require.Equal(t, forge.MergeDetails{
		Title: "feat(api): add merges endpoint",
		Body:  "Adds the endpoint.\n\nCloses #12.",
		Reviews: []forge.Review{
			{Author: "alice_osag", State: forge.ReviewStateApproved},
			{Author: "carol", State: forge.ReviewStateChangesRequested},
			{Author: "dave", State: forge.ReviewStateCommented},
			{Author: "app/robo-reviewer", State: forge.ReviewStateApproved},
			{Author: "erin", State: forge.ReviewStateCommented},
			{Author: "frank", State: forge.ReviewStateCommented},
		},
	}, normalizeMergeDetails(item))
}

// TestNormalizeMergeDetailsFeedsCommitMessage closes the loop between the
// adapter's per-review mapping and the shared fold that builds the commit
// message: only the two approving reviews may reach the trailer, and the
// gh-land login normalisation happens there, not at the seam.
func TestNormalizeMergeDetailsFeedsCommitMessage(t *testing.T) {
	details := normalizeMergeDetails(decodeFixture[ghViewItem](t, "pr_view.json"))

	subject, body := forge.CommitMessage(details)
	require.Equal(t, "feat(api): add merges endpoint", subject)
	require.Equal(t, "Adds the endpoint.\n\nCloses #12.\nApproved by: alice, robo-reviewer", body)
}

// TestNormalizeFileDiffs runs the on-demand diff decode+normalise path over a
// RECORDED `gh api repos/{repo}/pulls/{n}/files` response (ADR 0008). Note the
// field spelling: the REST files API carries `filename`, where gh's list
// `files` objects carry `path` — decoding the wrong one yields silent empty
// strings.
//
// The status token rides across verbatim: added|modified|removed|renamed is a
// value the Diff card renders, not a vocabulary any fold judges.
func TestNormalizeFileDiffs(t *testing.T) {
	files := decodeFixture[[]ghFileDiff](t, "pr_files.json")
	require.Len(t, files, 3, "the fixture is the recorded response, not a subset")

	require.Equal(t, []forge.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -10,6 +10,8 @@\n+a\n-b"},
		{Filename: "assets/logo.png", Status: "added"},
		{Filename: "old/path.go", Status: "renamed"},
	}, normalizeFileDiffs(files))
}
