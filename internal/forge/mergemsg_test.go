package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestCommitMessage pins gh-land parity for the squash commit message (ADR
// 0016): subject = PR title verbatim; body = live PR description + "\n" + an
// "Approved by:" trailer built from reviews the adapter normalised to
// approved — logins deduped after normalization, "_osag" suffixes and "app/"
// prefixes stripped, first-seen order preserved. Parity means byte-
// equivalence, so the table pins exact strings, not substrings.
func TestCommitMessage(t *testing.T) {
	approved := func(login string) forge.Review {
		return forge.Review{Author: login, State: forge.ReviewStateApproved}
	}

	tests := []struct {
		name        string
		details     forge.MergeDetails
		wantSubject string
		wantBody    string
	}{
		{
			name: "single approver appends the trailer after the body",
			details: forge.MergeDetails{
				Title:   "feat(api): add merges endpoint",
				Body:    "Adds the endpoint.",
				Reviews: []forge.Review{approved("alice")},
			},
			wantSubject: "feat(api): add merges endpoint",
			wantBody:    "Adds the endpoint.\nApproved by: alice",
		},
		{
			name: "multiple approvers joined comma-space in first-seen order",
			details: forge.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []forge.Review{approved("bob"), approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: bob, alice",
		},
		{
			name: "non-approved reviews never reach the trailer",
			details: forge.MergeDetails{
				Title: "fix: x",
				Body:  "b",
				Reviews: []forge.Review{
					{Author: "carol", State: forge.ReviewStateChangesRequested},
					{Author: "dave", State: forge.ReviewStateCommented},
					approved("alice"),
				},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice",
		},
		{
			name: "_osag suffix and app/ prefix are stripped",
			details: forge.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []forge.Review{approved("alice_osag"), approved("app/robo-reviewer")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice, robo-reviewer",
		},
		{
			name: "logins dedupe after stripping (alice and alice_osag are one)",
			details: forge.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []forge.Review{approved("alice"), approved("alice_osag"), approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice",
		},
		{
			name: "empty body is the trailer alone (no leading newline)",
			details: forge.MergeDetails{
				Title:   "fix: x",
				Body:    "",
				Reviews: []forge.Review{approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "Approved by: alice",
		},
		{
			name: "no approved reviews leaves the body untouched",
			details: forge.MergeDetails{
				Title:   "fix: x",
				Body:    "just the description",
				Reviews: []forge.Review{{Author: "carol", State: forge.ReviewStateChangesRequested}},
			},
			wantSubject: "fix: x",
			wantBody:    "just the description",
		},
		{
			name:        "no reviews at all leaves the body untouched",
			details:     forge.MergeDetails{Title: "fix: x", Body: "b"},
			wantSubject: "fix: x",
			wantBody:    "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := forge.CommitMessage(tt.details)
			require.Equal(t, tt.wantSubject, subject)
			require.Equal(t, tt.wantBody, body)
		})
	}
}

// TestApprovedBy pins the ledger-facing half of the fold: the deduped,
// normalized approver logins on their own, in first-seen order — the same list
// the trailer joins, persisted as the merge record's approved_by[].
func TestApprovedBy(t *testing.T) {
	reviews := []forge.Review{
		{Author: "bob_osag", State: forge.ReviewStateApproved},
		{Author: "app/robo", State: forge.ReviewStateApproved},
		{Author: "bob", State: forge.ReviewStateApproved},
		{Author: "carol", State: forge.ReviewStateCommented},
	}
	require.Equal(t, []string{"bob", "robo"}, forge.ApprovedBy(reviews))

	require.Empty(t, forge.ApprovedBy(nil), "no reviews yields no approvers")
}
