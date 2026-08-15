package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/els0r/toilmaster3000/internal/forge"
)

// CLI is the production GitHub forge.Client. It shells out to the gh CLI,
// reusing the user's existing auth (no PAT).
//
// repo and search are the candidate set, global per CONTEXT.md "Candidate set"
// — not per-rule. repo is the "owner/name" the gh calls target; search is the
// `gh pr list --search` query that selects which open PRs are candidates (e.g.
// "is:open team-review-requested:owner/team"). Both are supplied at startup so
// the tool is not wired to any one organisation's repo.
type CLI struct {
	repo   string
	search string
}

// The adapter is only useful if it is interchangeable at the seam, so the
// interface it implements is asserted at compile time.
var _ forge.Client = (*CLI)(nil)

// NewCLI returns a forge.Client backed by the gh CLI, scoped to the given
// candidate set (repo "owner/name" and the candidate `--search` query).
func NewCLI(repo, search string) *CLI { return &CLI{repo: repo, search: search} }

// listJSONFields is the --json field set of the inbound candidate list call —
// everything the cycle needs from ONE call (no per-PR N+1).
const listJSONFields = "number,title,author,url,additions,deletions,changedFiles,isDraft,statusCheckRollup,reviewDecision,headRefOid"

// AuthoredSearch is the fixed derived outbound search: every open PR the
// operator authors, drafts included (draft is an outbound stage, not a gate).
// It is not configurable — the outbound direction always means "my PRs in the
// configured repo".
const AuthoredSearch = "is:open author:@me"

// InboundSearch derives the effective inbound search from the operator's
// configured query by appending -author:@me, so the two per-cycle pulls are
// disjoint: authored PRs never enter the inbound funnel (they previously sat
// in inbound Staging as unmatchable noise) and live on the outbound tab
// instead. Applied once at startup (main); every consumer — the gh call and
// the /pipeline search chip — sees the same effective search.
func InboundSearch(configured string) string {
	return configured + " -author:@me"
}

// CheckRepoVisible verifies the configured repo resolves for the active gh
// identity via one boot-time `gh repo view` (--json keeps it quiet and
// non-interactive; the fields are discarded — only the exit code judges).
func (c *CLI) CheckRepoVisible(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", c.repo, "--json", "name")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh repo view %s: %w: %s", c.repo, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ListCandidates pulls the inbound candidate set once via a single gh call,
// with files riding it — the changed-file paths Notifier scope matches against
// (ADR 0026), appended here rather than to listJSONFields exactly as the
// outbound call appends its own mergeable: Notifiers fire at inbound points, so
// the outbound pull must pay nothing for it. The call stays ONE call and merely
// carries more.
func (c *CLI) ListCandidates(ctx context.Context) ([]forge.PR, error) {
	return c.list(ctx, c.search, listJSONFields+",files")
}

// ListAuthored pulls the outbound candidate set once via a second single gh
// call against the same repo: the fixed AuthoredSearch, with mergeable riding
// the call (the Ready conflict marker and the merge precondition). Normalised,
// not judged — ClassifyOutboundStage judges the stages.
func (c *CLI) ListAuthored(ctx context.Context) ([]forge.PR, error) {
	return c.list(ctx, AuthoredSearch, listJSONFields+",mergeable")
}

// list runs one `gh pr list` over the given search and --json field set,
// decodes each item, and normalises it into a neutral PR. Both per-cycle pulls
// (inbound candidates, outbound authored) flow through here so neither the
// decode nor the normalisation ever forks.
func (c *CLI) list(ctx context.Context, search, jsonFields string) ([]forge.PR, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", c.repo,
		"--search", search,
		"--json", jsonFields,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh pr list: %w: %s", err, stderr.String())
	}

	var items []ghListItem
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil, fmt.Errorf("decode gh pr list output: %w", err)
	}
	return normalizePRs(items), nil
}

// CurrentUser resolves the authenticated GitHub login via `gh api user`, so the
// @me author token can be expanded once at startup.
func (c *CLI) CurrentUser(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh api user: %w: %s", err, stderr.String())
	}
	login := strings.TrimSpace(stdout.String())
	if login == "" {
		return "", fmt.Errorf("gh api user: empty login")
	}
	return login, nil
}

// prStateRefreshLimit bounds the batched PR-State refresh. The search returns a
// superset of today's feed (every bot-reviewed PR updated today), so the bound is
// on that superset, not the feed; 200 is comfortably above realistic daily
// volume. Hitting it is logged as a warning so an undersized bound surfaces in
// logs instead of silently dropping PRs to unknown (ADR 0007).
const prStateRefreshLimit = 200

// PRStatesSince fetches the live lifecycle of every PR the bot reviewed (it only
// ever approves, so reviewed-by:@me == approved-by-me) updated at or after since,
// in ONE `gh pr list` over --state all — merged/closed PRs have left is:open, so
// the candidate search cannot supply them. It decodes the array and normalises it
// into a number->Lifecycle map; the engine intersects against today's feed and
// CollapsePRState judges each open|merged|closed bucket. A result that hits the
// limit is warned (no silent truncation). Replaces the per-PR `gh pr view` N+1
// (ADR 0007).
func (c *CLI) PRStatesSince(ctx context.Context, since time.Time) (map[int]forge.Lifecycle, error) {
	search := fmt.Sprintf("reviewed-by:@me updated:>=%s", since.Format(time.RFC3339))
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", c.repo,
		"--state", "all",
		"--search", search,
		"--json", "number,state,mergedAt",
		"--limit", strconv.Itoa(prStateRefreshLimit),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh pr list (pr state): %w: %s", err, stderr.String())
	}

	var items []ghPRStateItem
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil, fmt.Errorf("decode gh pr list (pr state) output: %w", err)
	}
	if len(items) == prStateRefreshLimit {
		slog.Default().Warn("pr state refresh hit the result limit; some states may be missing this cycle",
			"limit", prStateRefreshLimit,
		)
	}
	return normalizeLifecycles(items), nil
}

// threadSearchPageLimit bounds the unresolved-threads search to one GraphQL
// page — 100 is the search connection's maximum and comfortably above a
// realistic authored pull. Hitting it is logged as a warning so an undersized
// bound surfaces in logs instead of silently missing PRs' threads (the ADR
// 0007 warn-at-limit pattern).
const threadSearchPageLimit = 100

// threadPageSize bounds each PR's fetched reviewThreads page. A PR carrying
// more threads than this decodes with HasMorePages set, which UnresolvedCount
// conservatively judges as HAVING unresolved threads — hold, never resolve on
// a truncated fetch (ADR 0019).
const threadPageSize = 100

// unresolvedThreadsQuery is the one batched GraphQL search of the threads
// call: per PR of the authored pull, the first page of reviewThreads nodes
// (isResolved only — isOutdated is deliberately not requested: outdated is not
// resolved) plus the connection's page info. type ISSUE is GitHub's search
// type for issues AND pull requests; the is:pr qualifier in the search query
// narrows it.
var unresolvedThreadsQuery = fmt.Sprintf(
	`query($q: String!) { search(query: $q, type: ISSUE, first: %d) { nodes { ... on PullRequest { number reviewThreads(first: %d) { nodes { isResolved } pageInfo { hasNextPage } } } } } }`,
	threadSearchPageLimit, threadPageSize,
)

// UnresolvedThreads fetches per-PR review-thread resolution for the WHOLE
// authored pull in ONE `gh api graphql` search — the cycle's third batched
// call (ADR 0019). GraphQL is forced, not chosen: isResolved exists only in
// the GraphQL reviewThreads connection (no `gh pr list --json` field carries
// it). The search is the same authored pull ListAuthored sees, repo-qualified
// for the global search endpoint. Normalised, not judged: the pure
// UnresolvedCount folds each connection. A full search page is warned on (no
// silent truncation).
func (c *CLI) UnresolvedThreads(ctx context.Context) (map[int]forge.ReviewThreads, error) {
	search := fmt.Sprintf("repo:%s is:pr %s", c.repo, AuthoredSearch)
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql",
		"-f", "query="+unresolvedThreadsQuery,
		"-f", "q="+search,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api graphql (review threads): %w: %s", err, stderr.String())
	}

	var out ghThreadsResponse
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode gh api graphql (review threads) output: %w", err)
	}

	nodes := out.Data.Search.Nodes
	if len(nodes) == threadSearchPageLimit {
		slog.Default().Warn("unresolved-threads search hit the page limit; some PRs' threads may be missing this cycle",
			"limit", threadSearchPageLimit,
		)
	}
	return normalizeReviewThreads(nodes), nil
}

// diffPageSize bounds the on-demand diff fetch to one page. The Diff card is a
// skim aid, not a GitHub mirror — a PR with more changed files than this shows
// the first page under a "first N of M" banner (ADR 0008).
const diffPageSize = 100

// Diff fetches one PR's changed files via a single `gh api .../files` call,
// bounded to diffPageSize. Each element normalises into a FileDiff; a file
// GitHub omits the patch for (binary/over-large) arrives with an empty Patch.
func (c *CLI) Diff(ctx context.Context, number int) ([]forge.FileDiff, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=%d", c.repo, number, diffPageSize)
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api %s: %w: %s", endpoint, err, stderr.String())
	}

	var files []ghFileDiff
	if err := json.Unmarshal(stdout.Bytes(), &files); err != nil {
		return nil, fmt.Errorf("decode gh api files output: %w", err)
	}
	return normalizeFileDiffs(files), nil
}

// MergeInfo fetches one PR's live merge-time details via a single
// `gh pr view` (the sanctioned per-merge call, ADR 0016). Normalised, not
// judged — CommitMessage judges the details into the commit message.
func (c *CLI) MergeInfo(ctx context.Context, number int) (forge.MergeDetails, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number),
		"--repo", c.repo,
		"--json", "title,body,reviews",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return forge.MergeDetails{}, fmt.Errorf("gh pr view %d: %w: %s", number, err, stderr.String())
	}

	var item ghViewItem
	if err := json.Unmarshal(stdout.Bytes(), &item); err != nil {
		return forge.MergeDetails{}, fmt.Errorf("decode gh pr view output: %w", err)
	}
	return normalizeMergeDetails(item), nil
}

// Merge squash-merges one PR with the given commit subject and body, deleting
// the branch — the gh-land command shape (ADR 0016). The commit message rides
// as exec args, so no shell quoting can corrupt it.
func (c *CLI) Merge(ctx context.Context, number int, subject, body string) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "merge", strconv.Itoa(number),
		"--repo", c.repo,
		"-s", "-d",
		"-t", subject,
		"-b", body,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr merge %d: %w: %s", number, err, stderr.String())
	}
	return nil
}

// Approve records an approving review on one PR.
func (c *CLI) Approve(ctx context.Context, number int) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "review",
		"--repo", c.repo,
		"--approve", strconv.Itoa(number),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr review --approve %d: %w: %s", number, err, stderr.String())
	}
	return nil
}
