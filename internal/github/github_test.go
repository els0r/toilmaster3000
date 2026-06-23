package github_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// testRepo and testSearch are the candidate set the CLI client is scoped to in
// these tests. They stand in for whatever owner/repo and search a real
// deployment configures via TM3K_REPO/TM3K_SEARCH.
const (
	testRepo   = "example/repo"
	testSearch = "is:open team-review-requested:example/team"
)

// withFakeGh prepends a temp dir holding a fake `gh` script to PATH, so the CLI
// client's shell-out is exercised without a real gh/network. The script echoes
// its args to argsFile and prints stdout.
func withFakeGh(t *testing.T, script string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")

	ghPath := filepath.Join(dir, "gh")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + argsFile + "\n" +
		script
	require.NoError(t, os.WriteFile(ghPath, []byte(body), 0o755))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

// G1: ListCandidates shells out to gh and parses number/title/url, the nested
// author.login, and the additions/deletions diff fields into PR values — all
// from the SAME single gh pr list call (no per-PR N+1).
func TestCLIListCandidatesParsesAuthorLogin(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 7, "title": "chore: x", "url": "https://gh/pull/7", "author": {"login": "alice"}, "additions": 12, "deletions": 3, "changedFiles": 4},
  {"number": 8, "title": "fix: y", "url": "https://gh/pull/8", "author": {"login": "bob"}, "additions": 0, "deletions": 0, "changedFiles": 0}
]
JSON
`)

	prs, err := github.NewCLI(testRepo, testSearch).ListCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 2)
	require.Equal(t, github.PR{Number: 7, Title: "chore: x", Author: "alice", URL: "https://gh/pull/7", Additions: 12, Deletions: 3, ChangedFiles: 4}, prs[0])
	require.Equal(t, "bob", prs[1].Author)

	// additions/deletions/changedFiles ride the SAME single gh pr list --json call.
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "additions,deletions", "diff fields requested in the same --json arg")
	require.Contains(t, string(got), "changedFiles", "changed-files count requested in the same --json arg")
}

// G1b: ListCandidates pulls statusCheckRollup in the SAME single gh pr list
// call and decodes each rollup entry into PR.Checks — a heterogeneous mix of
// CheckRun (status/conclusion) and StatusContext (state). The CLI seam only
// decodes; AllGreen judges. This lightly covers the shell-out decode.
func TestCLIListCandidatesDecodesStatusCheckRollup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 9, "title": "chore: z", "url": "https://gh/pull/9", "author": {"login": "carol"}, "additions": 1, "deletions": 1,
   "statusCheckRollup": [
     {"__typename": "CheckRun", "status": "COMPLETED", "conclusion": "SUCCESS"},
     {"__typename": "StatusContext", "state": "SUCCESS"}
   ]}
]
JSON
`)

	prs, err := github.NewCLI(testRepo, testSearch).ListCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 1)
	require.Equal(t, []github.Check{
		{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Typename: "StatusContext", State: "SUCCESS"},
	}, prs[0].Checks, "rollup entries decode into PR.Checks")

	// The rollup rides the SAME single gh pr list --json call (no per-PR N+1).
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "statusCheckRollup", "rollup requested in the same --json arg")
}

// G1c: PRStatesSince shells out to a SINGLE `gh pr list` scoped by
// `reviewed-by:@me updated:>=<since>` over `--state all`, and decodes the array
// into a number->raw map — the decode-only batched seam the engine's tail
// refresh drives (CollapsePRState judges each bucket). One call regardless of
// feed size (the per-PR `gh pr view` N+1 is gone — ADR 0007). The bot only ever
// approves, so `reviewed-by:@me` is exactly the PRs it approved.
func TestCLIPRStatesSinceDecodesIntoMap(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 42, "state": "MERGED", "mergedAt": "2026-06-19T10:00:00Z"},
  {"number": 7, "state": "OPEN", "mergedAt": null},
  {"number": 9, "state": "CLOSED", "mergedAt": null}
]
JSON
`)

	since := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	states, err := github.NewCLI(testRepo, testSearch).PRStatesSince(context.Background(), since)
	require.NoError(t, err)
	require.Equal(t, map[int]github.RawPRState{
		42: {State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"},
		7:  {State: "OPEN", MergedAt: ""},   // JSON null -> "" -> open
		9:  {State: "CLOSED", MergedAt: ""}, // null mergedAt -> closed-without-merging
	}, states)

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "PR State is refreshed in exactly one gh call (no per-PR N+1)")
	require.NotContains(t, string(got), "pr view", "the per-PR view call is gone")
	require.Contains(t, string(got), "--state all", "merged/closed PRs have left is:open, so the refresh spans all states")
	require.Contains(t, string(got), "reviewed-by:@me updated:>=2026-06-22T00:00:00Z", "scoped to today's reviewed PRs by the since floor")
	require.Contains(t, string(got), "number,state,mergedAt", "number/state/mergedAt requested in the --json arg")
	require.Contains(t, string(got), "--limit 200", "bounded by a generous limit (truncation warned on)")
}

// G6: Diff shells out to `gh api repos/{repo}/pulls/{n}/files?per_page=100` and
// decodes each file object into a FileDiff (filename/status/additions/deletions/
// patch). On-demand, one call per user click — NOT the per-PR `gh pr view` N+1
// ADR 0007 removed (that ran per-cycle; this never touches the cycle path).
func TestCLIDiffParsesFiles(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"filename": "main.go", "status": "modified", "additions": 2, "deletions": 1, "patch": "@@ -10,6 +10,8 @@\n+a\n-b"}
]
JSON
`)

	files, err := github.NewCLI(testRepo, testSearch).Diff(context.Background(), 123)
	require.NoError(t, err)
	require.Equal(t, []github.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -10,6 +10,8 @@\n+a\n-b"},
	}, files)

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(got), "api repos/example/repo/pulls/123/files", "diff fetched via gh api on the PR's files endpoint")
	require.Contains(t, string(got), "per_page=100", "bounded to one page of 100 files")
	require.NotContains(t, string(got), "pr view", "on-demand diff is not the per-cycle pr view N+1 (ADR 0007)")
}

// G6b: GitHub omits the `patch` field for binary and over-large files. Diff
// decodes such a file with an empty Patch (the Diff card renders it as "no
// preview"), without dropping the file or erroring.
func TestCLIDiffBinaryFileHasEmptyPatch(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	withFakeGh(t, `cat <<'JSON'
[
  {"filename": "logo.png", "status": "added", "additions": 0, "deletions": 0}
]
JSON
`)

	files, err := github.NewCLI(testRepo, testSearch).Diff(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "logo.png", files[0].Filename)
	require.Empty(t, files[0].Patch, "a file with no patch field decodes to an empty Patch")
}

// G6c: a non-zero gh exit surfaces as an error so the endpoint can report the
// fetch failed rather than rendering an empty diff.
func TestCLIDiffError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	withFakeGh(t, "echo 'not found' >&2\nexit 1\n")

	_, err := github.NewCLI(testRepo, testSearch).Diff(context.Background(), 1)
	require.Error(t, err)
}

// G2: Approve shells out to `gh pr review --approve <number>`.
func TestCLIApproveInvokesReview(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, "exit 0\n")

	require.NoError(t, github.NewCLI(testRepo, testSearch).Approve(context.Background(), 42))

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(got), "pr review")
	require.Contains(t, string(got), "--approve")
	require.Contains(t, string(got), "42")
}

// G4: CurrentUser shells out to `gh api user --jq .login` and trims the
// resolved login.
func TestCLICurrentUserResolvesLogin(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, "printf 'octocat\\n'\n")

	login, err := github.NewCLI(testRepo, testSearch).CurrentUser(context.Background())
	require.NoError(t, err)
	require.Equal(t, "octocat", login)

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(got), "api user")
	require.Contains(t, string(got), "--jq")
}

// G5: a non-zero gh exit from `gh api user` surfaces as an error so preflight
// fails fast instead of proceeding with an empty @me.
func TestCLICurrentUserError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	withFakeGh(t, "echo 'not logged in' >&2\nexit 1\n")

	_, err := github.NewCLI(testRepo, testSearch).CurrentUser(context.Background())
	require.Error(t, err)
}

// G3: a non-zero gh exit surfaces as an error (so the cycle records it).
func TestCLIApproveError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	withFakeGh(t, "echo 'boom' >&2\nexit 1\n")

	err := github.NewCLI(testRepo, testSearch).Approve(context.Background(), 1)
	require.Error(t, err)
}
