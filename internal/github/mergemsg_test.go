package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// TestCommitMessage pins gh-land parity for the squash commit message (ADR
// 0016): subject = PR title verbatim; body = live PR description + "\n" + an
// "Approved by:" trailer built from reviews with state APPROVED — logins
// deduped after normalization, "_osag" suffixes and "app/" prefixes stripped,
// first-seen order preserved. Parity means byte-equivalence, so the table pins
// exact strings, not substrings.
func TestCommitMessage(t *testing.T) {
	approved := func(login string) github.Review {
		return github.Review{Author: login, State: "APPROVED"}
	}

	tests := []struct {
		name        string
		details     github.MergeDetails
		wantSubject string
		wantBody    string
	}{
		{
			name: "single approver appends the trailer after the body",
			details: github.MergeDetails{
				Title:   "feat(api): add merges endpoint",
				Body:    "Adds the endpoint.",
				Reviews: []github.Review{approved("alice")},
			},
			wantSubject: "feat(api): add merges endpoint",
			wantBody:    "Adds the endpoint.\nApproved by: alice",
		},
		{
			name: "multiple approvers joined comma-space in first-seen order",
			details: github.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []github.Review{approved("bob"), approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: bob, alice",
		},
		{
			name: "non-APPROVED reviews never reach the trailer",
			details: github.MergeDetails{
				Title: "fix: x",
				Body:  "b",
				Reviews: []github.Review{
					{Author: "carol", State: "CHANGES_REQUESTED"},
					{Author: "dave", State: "COMMENTED"},
					approved("alice"),
				},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice",
		},
		{
			name: "_osag suffix and app/ prefix are stripped",
			details: github.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []github.Review{approved("alice_osag"), approved("app/robo-reviewer")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice, robo-reviewer",
		},
		{
			name: "logins dedupe after stripping (alice and alice_osag are one)",
			details: github.MergeDetails{
				Title:   "fix: x",
				Body:    "b",
				Reviews: []github.Review{approved("alice"), approved("alice_osag"), approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "b\nApproved by: alice",
		},
		{
			name: "empty body is the trailer alone (no leading newline)",
			details: github.MergeDetails{
				Title:   "fix: x",
				Body:    "",
				Reviews: []github.Review{approved("alice")},
			},
			wantSubject: "fix: x",
			wantBody:    "Approved by: alice",
		},
		{
			name: "no approved reviews leaves the body untouched",
			details: github.MergeDetails{
				Title:   "fix: x",
				Body:    "just the description",
				Reviews: []github.Review{{Author: "carol", State: "CHANGES_REQUESTED"}},
			},
			wantSubject: "fix: x",
			wantBody:    "just the description",
		},
		{
			name:        "no reviews at all leaves the body untouched",
			details:     github.MergeDetails{Title: "fix: x", Body: "b"},
			wantSubject: "fix: x",
			wantBody:    "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := github.CommitMessage(tt.details)
			require.Equal(t, tt.wantSubject, subject)
			require.Equal(t, tt.wantBody, body)
		})
	}
}

// TestApprovedBy pins the ledger-facing half of the fold: the deduped,
// normalized approver logins on their own, in first-seen order — the same list
// the trailer joins, persisted as the merge record's approved_by[].
func TestApprovedBy(t *testing.T) {
	reviews := []github.Review{
		{Author: "bob_osag", State: "APPROVED"},
		{Author: "app/robo", State: "APPROVED"},
		{Author: "bob", State: "APPROVED"},
		{Author: "carol", State: "COMMENTED"},
	}
	require.Equal(t, []string{"bob", "robo"}, github.ApprovedBy(reviews))

	require.Empty(t, github.ApprovedBy(nil), "no reviews yields no approvers")
}
