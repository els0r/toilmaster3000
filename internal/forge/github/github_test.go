package github_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/els0r/toilmaster3000/internal/forge/github"
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
	require.Equal(t, forge.PR{
		Number: 7, Title: "chore: x", Author: "alice", URL: "https://gh/pull/7",
		Additions: 12, Deletions: 3, ChangedFiles: 4,
		// The inbound pull asks for neither, so both normalise to their
		// "nobody answered" value rather than to a guess.
		ReviewDecision: forge.ReviewNone, Mergeable: forge.MergeableUnknown,
	}, prs[0])
	require.Equal(t, "bob", prs[1].Author)

	// additions/deletions/changedFiles ride the SAME single gh pr list --json call.
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "additions,deletions", "diff fields requested in the same --json arg")
	require.Contains(t, string(got), "changedFiles", "changed-files count requested in the same --json arg")
}

// G1b: ListCandidates pulls statusCheckRollup in the SAME single gh pr list
// call and turns each rollup entry into a NORMALISED PR.Checks entry — a
// heterogeneous mix of CheckRun (status/conclusion) and StatusContext (state)
// arriving as one neutral vocabulary. The CLI seam only decodes and
// normalises; AllGreen judges. This lightly covers the shell-out; which raw
// entry maps to which state is table-tested in TestNormalizeCheckState.
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
	require.Equal(t, []forge.Check{
		{State: forge.CheckPass},
		{State: forge.CheckPass},
	}, prs[0].Checks, "rollup entries decode and normalise into PR.Checks")

	// The rollup rides the SAME single gh pr list --json call (no per-PR N+1).
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "statusCheckRollup", "rollup requested in the same --json arg")
}

// G1d: ListCandidates pulls headRefOid in the SAME single gh pr list call and
// decodes it into PR.HeadSHA — the head the Screen verdict store keys on
// (ADR 0022/0023). Riding the existing batched call means screening adds no
// gh call of its own.
func TestCLIListCandidatesDecodesHeadRefOid(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 11, "title": "chore: head", "url": "https://gh/pull/11", "author": {"login": "erin"}, "headRefOid": "deadbeefcafe"}
]
JSON
`)

	prs, err := github.NewCLI(testRepo, testSearch).ListCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 1)
	require.Equal(t, "deadbeefcafe", prs[0].HeadSHA, "headRefOid decodes into PR.HeadSHA")

	// The head rides the SAME single gh pr list --json call (no per-PR N+1).
	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "headRefOid", "head requested in the same --json arg")
}

// G1e: ListCandidates pulls each PR's changed-file paths in the SAME single gh
// pr list call and decodes them into PR.Files — the scope axis of Notifier
// firing discipline (ADR 0026). The call stays ONE call and merely carries
// more, which is not the per-PR N+1 ADR 0007 removed. Note the field spelling:
// gh's list `files` objects carry `path`, where the REST files API behind Diff
// carries `filename` — decoding the wrong one yields silent empty strings.
func TestCLIListCandidatesDecodesChangedFilePaths(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
[
  {"number": 12, "title": "feat: polyglot", "url": "https://gh/pull/12", "author": {"login": "ann"}, "changedFiles": 2,
   "files": [{"path": "internal/hook/scope.go", "additions": 40, "deletions": 0, "changeType": "ADDED"},
             {"path": "scripts/release.sh", "additions": 3, "deletions": 1, "changeType": "MODIFIED"}]},
  {"number": 13, "title": "docs: readme", "url": "https://gh/pull/13", "author": {"login": "bob"}, "changedFiles": 0}
]
JSON
`)

	prs, err := github.NewCLI(testRepo, testSearch).ListCandidates(context.Background())
	require.NoError(t, err)
	require.Len(t, prs, 2)
	require.Equal(t, []string{"internal/hook/scope.go", "scripts/release.sh"}, prs[0].Files,
		"the files array decodes into the PR's changed-file path list")
	require.Equal(t, 2, prs[0].ChangedFiles, "the true count rides beside the list, so truncation is detectable")
	require.Empty(t, prs[1].Files, "a PR gh reports no files for decodes to an empty list, never an error")

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("pr list")), "candidate set is fetched in exactly one gh call")
	require.Contains(t, string(got), "files", "the paths ride the same --json arg")
}

// G1f: the outbound authored pull does NOT request files. Notifiers fire at
// inbound points only, so the outbound call pays nothing for scope (ADR 0026);
// it keeps requesting its own extra field, mergeable.
func TestCLIListAuthoredDoesNotRequestFiles(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, "printf '[]\\n'\n")

	_, err := github.NewCLI(testRepo, testSearch).ListAuthored(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Contains(t, string(got), "mergeable", "the outbound call keeps its own extra field")
	require.NotContains(t, string(got), ",files", "scope is inbound-only: the outbound pull carries no files")
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
	require.Equal(t, map[int]forge.Lifecycle{
		42: {State: forge.LifecycleMerged, MergedAt: "2026-06-19T10:00:00Z"},
		7:  {State: forge.LifecycleOpen, MergedAt: ""},   // JSON null -> "" -> open
		9:  {State: forge.LifecycleClosed, MergedAt: ""}, // null mergedAt -> closed-without-merging
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

// G1d: UnresolvedThreads shells out to ONE `gh api graphql` search over the
// authored pull — GraphQL is forced, not chosen: isResolved exists only in the
// GraphQL reviewThreads connection (ADR 0019) — and decodes each PR node into
// its raw reviewThreads connection (nodes + page info). Decode-only: the pure
// UnresolvedCount judges the fold. One call regardless of pull size (the
// cycle's third batched call — no per-PR N+1).
func TestCLIUnresolvedThreadsDecodesIntoMap(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	argsFile := withFakeGh(t, `cat <<'JSON'
{"data": {"search": {"nodes": [
  {"number": 21, "reviewThreads": {"nodes": [{"isResolved": true}, {"isResolved": false}], "pageInfo": {"hasNextPage": false}}},
  {"number": 22, "reviewThreads": {"nodes": [], "pageInfo": {"hasNextPage": true}}}
]}}}
JSON
`)

	threads, err := github.NewCLI(testRepo, testSearch).UnresolvedThreads(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[int]forge.ReviewThreads{
		21: {Nodes: []forge.ReviewThread{{IsResolved: true}, {IsResolved: false}}},
		22: {Nodes: []forge.ReviewThread{}, HasMorePages: true},
	}, threads)

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(got, []byte("api graphql")), "threads for the whole pull ride exactly one GraphQL call")
	require.NotContains(t, string(got), "pr list", "the threads call never forks the list decode (ADR 0019)")
	require.Contains(t, string(got), "isResolved", "the resolution flag rides the query")
	require.NotContains(t, string(got), "isOutdated", "outdated is ignored entirely — outdated is not resolved")
	require.Contains(t, string(got), "repo:example/repo is:pr "+github.AuthoredSearch,
		"the search scopes the SAME authored pull as ListAuthored, repo-qualified")
}

// G1f: a search result that FILLS the page is warned on (the ADR 0007
// warn-at-limit pattern): PRs beyond the page are silently absent from the
// map, so the undersized bound must surface in logs, never pass quietly.
func TestCLIUnresolvedThreadsWarnsAtSearchPageLimit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Exactly the search-page limit (100) of PR nodes: a full page.
	nodes := make([]string, 0, 100)
	for i := range 100 {
		nodes = append(nodes, fmt.Sprintf(
			`{"number": %d, "reviewThreads": {"nodes": [], "pageInfo": {"hasNextPage": false}}}`, i+1))
	}
	payload := `{"data": {"search": {"nodes": [` + strings.Join(nodes, ",") + `]}}}`
	payloadFile := filepath.Join(t.TempDir(), "payload.json")
	require.NoError(t, os.WriteFile(payloadFile, []byte(payload), 0o644))
	withFakeGh(t, "cat "+payloadFile+"\n")

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	threads, err := github.NewCLI(testRepo, testSearch).UnresolvedThreads(context.Background())
	require.NoError(t, err)
	require.Len(t, threads, 100)
	require.Contains(t, logs.String(), "unresolved-threads search hit the page limit",
		"a full search page fires the warn-at-limit")
}

// G1e: a non-zero gh exit from the threads call surfaces as an error, so the
// engine can fail closed (clear the outbound snapshot, merge nothing) instead
// of reading an empty result as all-threads-resolved.
func TestCLIUnresolvedThreadsError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	withFakeGh(t, "echo 'graphql exploded' >&2\nexit 1\n")

	_, err := github.NewCLI(testRepo, testSearch).UnresolvedThreads(context.Background())
	require.Error(t, err)
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
	require.Equal(t, []forge.FileDiff{
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
