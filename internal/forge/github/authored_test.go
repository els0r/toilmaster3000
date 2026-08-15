package github_test

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/els0r/toilmaster3000/internal/forge/github"
	"github.com/stretchr/testify/require"
)

// TestInboundSearch pins the disjointness of the two per-cycle pulls at the
// search: the effective inbound search is the configured one with -author:@me
// appended, so the operator's own PRs never enter the inbound funnel (they
// live on the outbound tab instead).
func TestInboundSearch(t *testing.T) {
	require.Equal(t,
		"is:open team-review-requested:example/team -author:@me",
		github.InboundSearch("is:open team-review-requested:example/team"),
	)
}

// G-OB: ListAuthored shells out to ONE gh pr list against the fixed derived
// authored search — drafts included (no draft filter) — with mergeable and
// reviewDecision riding the same single call (no per-PR N+1), and decodes
// mergeable onto the PR.
func TestCLIListAuthored(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 21, "title": "feat: mine", "url": "https://gh/pull/21", "author": {"login": "me"}, "isDraft": true, "mergeable": "UNKNOWN"},
  {"number": 22, "title": "fix: mine too", "url": "https://gh/pull/22", "author": {"login": "me"}, "reviewDecision": "APPROVED", "mergeable": "CONFLICTING"}
]
JSON
`)

	prs, err := github.NewCLI(testRepo, testSearch).ListAuthored(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 2)
	require.True(t, prs[0].IsDraft, "drafts ride the authored pull — draft is a stage, not a gate")
	require.Equal(t, forge.MergeableUnknown, prs[0].Mergeable)
	require.Equal(t, forge.MergeableConflicting, prs[1].Mergeable)
	require.Equal(t, forge.ReviewApproved, prs[1].ReviewDecision)

	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(args), "--search "+github.AuthoredSearch, "the fixed derived authored search")
	require.Contains(t, string(args), "mergeable", "mergeable rides the single list call")
	require.NotContains(t, string(args), "draft:false", "drafts are included")
}
