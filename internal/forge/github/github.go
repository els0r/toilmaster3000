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

// runGH executes one gh invocation and returns its stdout. It is the ONLY
// place in tm3k that spawns a process for GitHub: every call below goes
// through it, so the buffer wiring, the stderr handling, and the shape of a
// failure are decided once rather than nine times.
//
// label names the call in the error — "gh pr list", "gh pr view 42" — so a
// failure says which of the cycle's calls broke without the caller
// reconstructing the argv. gh writes its diagnostics to stderr with a trailing
// newline, so it is trimmed: an untrimmed one wraps the error across two lines
// in the log.
func runGH(ctx context.Context, label string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", label, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

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
	_, err := runGH(ctx, "gh repo view "+c.repo,
		"repo", "view", c.repo, "--json", "name")
	return err
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
	out, err := runGH(ctx, "gh pr list",
		"pr", "list",
		"--repo", c.repo,
		"--search", search,
		"--json", jsonFields,
	)
	if err != nil {
		return nil, err
	}

	var items []ghListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("decode gh pr list output: %w", err)
	}
	return normalizePRs(items), nil
}

// CurrentUser resolves the authenticated GitHub login via `gh api user`, so the
// @me author token can be expanded once at startup.
func (c *CLI) CurrentUser(ctx context.Context) (string, error) {
	out, err := runGH(ctx, "gh api user", "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	login := strings.TrimSpace(string(out))
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
	out, err := runGH(ctx, "gh pr list (pr state)",
		"pr", "list",
		"--repo", c.repo,
		"--state", "all",
		"--search", search,
		"--json", "number,state,mergedAt",
		"--limit", strconv.Itoa(prStateRefreshLimit),
	)
	if err != nil {
		return nil, err
	}

	var items []ghPRStateItem
	if err := json.Unmarshal(out, &items); err != nil {
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
	stdout, err := runGH(ctx, "gh api graphql (review threads)",
		"api", "graphql",
		"-f", "query="+unresolvedThreadsQuery,
		"-f", "q="+search,
	)
	if err != nil {
		return nil, err
	}

	var out ghThreadsResponse
	if err := json.Unmarshal(stdout, &out); err != nil {
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
	out, err := runGH(ctx, "gh api "+endpoint, "api", endpoint)
	if err != nil {
		return nil, err
	}

	var files []ghFileDiff
	if err := json.Unmarshal(out, &files); err != nil {
		return nil, fmt.Errorf("decode gh api files output: %w", err)
	}
	return normalizeFileDiffs(files), nil
}

// MergeInfo fetches one PR's live merge-time details via a single
// `gh pr view` (the sanctioned per-merge call, ADR 0016). Normalised, not
// judged — CommitMessage judges the details into the commit message.
func (c *CLI) MergeInfo(ctx context.Context, number int) (forge.MergeDetails, error) {
	out, err := runGH(ctx, fmt.Sprintf("gh pr view %d", number),
		"pr", "view", strconv.Itoa(number),
		"--repo", c.repo,
		"--json", "title,body,reviews",
	)
	if err != nil {
		return forge.MergeDetails{}, err
	}

	var item ghViewItem
	if err := json.Unmarshal(out, &item); err != nil {
		return forge.MergeDetails{}, fmt.Errorf("decode gh pr view output: %w", err)
	}
	return normalizeMergeDetails(item), nil
}

// Merge squash-merges one PR with the given commit subject and body, deleting
// the branch — the gh-land command shape (ADR 0016). The commit message rides
// as exec args, so no shell quoting can corrupt it.
func (c *CLI) Merge(ctx context.Context, number int, subject, body string) error {
	_, err := runGH(ctx, fmt.Sprintf("gh pr merge %d", number),
		"pr", "merge", strconv.Itoa(number),
		"--repo", c.repo,
		"-s", "-d",
		"-t", subject,
		"-b", body,
	)
	return err
}

// Approve records an approving review on one PR.
func (c *CLI) Approve(ctx context.Context, number int) error {
	_, err := runGH(ctx, fmt.Sprintf("gh pr review --approve %d", number),
		"pr", "review",
		"--repo", c.repo,
		"--approve", strconv.Itoa(number),
	)
	return err
}
