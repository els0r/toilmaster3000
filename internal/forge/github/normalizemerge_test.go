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
// LOGINS are normalised here too. GitHub's "app/" bot prefix and the org's
// "_osag" EMU suffix are GitHub spellings of an identity, so mapping them to
// the bare login is decode, not judge (ADR 0030 §3) — the shared fold has no
// business knowing either token. What stays in the fold is the judgement:
// which reviews count, and that two spellings of one person are one approver.
func TestNormalizeMergeDetails(t *testing.T) {
	item := decodeFixture[ghViewItem](t, "pr_view.json")
	require.Len(t, item.Reviews, 6, "the fixture is the recorded response, not a subset")

	require.Equal(t, forge.MergeDetails{
		Title: "feat(api): add merges endpoint",
		Body:  "Adds the endpoint.\n\nCloses #12.",
		Reviews: []forge.Review{
			{Author: "alice", State: forge.ReviewStateApproved},
			{Author: "carol", State: forge.ReviewStateChangesRequested},
			{Author: "dave", State: forge.ReviewStateCommented},
			{Author: "robo-reviewer", State: forge.ReviewStateApproved},
			{Author: "erin", State: forge.ReviewStateCommented},
			{Author: "frank", State: forge.ReviewStateCommented},
		},
	}, normalizeMergeDetails(item))
}

// TestNormalizeLogin pins the GitHub login spellings on their own — the table
// the shared fold used to carry. gh-land parity means these exact two
// manglings and no others (ADR 0016).
func TestNormalizeLogin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "a plain login is untouched", raw: "alice", want: "alice"},
		{name: "the org's EMU suffix is stripped", raw: "alice_osag", want: "alice"},
		{name: "the bot prefix is stripped", raw: "app/robo-reviewer", want: "robo-reviewer"},
		{name: "both at once", raw: "app/robo_osag", want: "robo"},
		{name: "an empty login stays empty", raw: "", want: ""},
		{name: "a bare prefix strips to nothing", raw: "app/", want: ""},
		{name: "the suffix is only stripped at the end", raw: "_osag_alice", want: "_osag_alice"},
		{name: "the prefix is only stripped at the start", raw: "team/app/bot", want: "team/app/bot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeLogin(tt.raw))
		})
	}
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
