// Package github is the seam between the engine and GitHub. The engine talks
// only to the GitHubClient interface; the production implementation shells out
// to the gh CLI (reusing its auth), and tests substitute a fake so the
// find->approve loop is provable without network access.
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
)

// PR is one candidate pull request from the candidate set. Additions and
// Deletions are the changed-line counts gh returns in the same single list
// call; they are carried separately and the diff-size rule predicate sums them
// (additions + deletions).
type PR struct {
	Number    int
	Title     string
	Author    string
	URL       string
	Additions int
	Deletions int
	// ChangedFiles is the count of files the PR touches, from the same single gh
	// list call. It is carried for human triage in the queue (how many files a
	// change spans), not for matching — the diff-size rule predicate uses only
	// additions+deletions.
	ChangedFiles int
	// IsDraft is the draft flag from the same single gh list call. A draft PR is
	// dropped by the engine's eligibility gate before it is ever parsed or
	// matched (CONTEXT "Eligibility gates").
	IsDraft bool
	// Checks is the statusCheckRollup from the same single gh list call: one
	// entry per check. The all-green eligibility gate folds these via AllGreen
	// before the PR is ever parsed or matched. The CLI seam only decodes these;
	// AllGreen does the judging.
	Checks []Check
	// ReviewDecision is gh's reviewDecision from the same single gh list call
	// (APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | ""). The CLI seam only
	// decodes it; the engine's approve path judges it. An APPROVED candidate whose
	// number is absent from approvals.jsonl was approved elsewhere — tm3k leaves it
	// alone as a soft dedup (ADR 0013), so saved-switches analytics never double-
	// counts across a team running multiple tm3k instances.
	ReviewDecision string
	// Mergeable is gh's mergeable field (MERGEABLE | CONFLICTING | UNKNOWN),
	// populated only by the authored-PR (outbound) list call — the inbound
	// candidate call does not request it. It is a MERGE precondition, not a
	// stage boundary: ClassifyOutboundStage never reads it, and a conflicted
	// Ready PR stays in Ready carrying its conflict state. UNKNOWN means GitHub
	// is still computing mergeability — retried naturally next cycle.
	Mergeable string
}

// FileDiff is one changed file of a PR, as the GitHub files API emits it: the
// path, its status (added|modified|removed|renamed), the per-file changed-line
// counts, and the unified-diff Patch. GitHub omits patch for binary and
// over-large files, so Patch is empty for those — the Diff card renders them as
// "no preview" rather than a blank diff. This is the on-demand seam behind the
// queue's Diff pill; it never rides the cycle (ADR 0008).
type FileDiff struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// Check is one entry of a PR's statusCheckRollup, as gh emits it. The rollup is
// heterogeneous: GitHub Checks API runs decode as Typename "CheckRun" (carrying
// Status/Conclusion), legacy commit statuses as "StatusContext" (carrying
// State). This struct only DECODES one entry; AllGreen judges the bucket.
type Check struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// GitHubClient is the seam the engine drives. ListCandidates pulls the
// candidate set once per cycle; Approve records an approving review on one PR;
// CurrentUser resolves the authenticated login once at startup so the matcher
// can expand the @me author token.
type GitHubClient interface {
	ListCandidates(ctx context.Context) ([]PR, error)
	// ListAuthored pulls the outbound candidate set — every open PR the
	// operator authors — once per cycle via a second `gh pr list` against the
	// same repo with the fixed derived search AuthoredSearch. Drafts are
	// included (draft is an outbound STAGE, not a gate), and mergeable +
	// reviewDecision ride the single call (no N+1). Decode-only:
	// ClassifyOutboundStage judges each PR into its stage.
	ListAuthored(ctx context.Context) ([]PR, error)
	// UnresolvedThreads pulls per-PR review-thread resolution for the whole
	// authored pull — the cycle's third batched call (ADR 0019) — via one
	// GraphQL search (isResolved exists nowhere else). Decode-only: each PR
	// maps to its raw reviewThreads connection; the pure UnresolvedCount
	// judges the fold. A PR absent from the map carries no review threads.
	// The result is load-bearing for the outbound partition: a failed call
	// makes the engine fail closed (clear the outbound snapshot, merge
	// nothing), exactly like a failed ListAuthored.
	UnresolvedThreads(ctx context.Context) (map[int]RawReviewThreads, error)
	Approve(ctx context.Context, number int) error
	CurrentUser(ctx context.Context) (string, error)
	// CheckRepoVisible reports whether the configured repo is visible to the
	// active gh identity — a boot-time preflight gate. It exists because the
	// per-cycle pulls go through GitHub's search API, which returns an EMPTY
	// result (not an error) for a repo the identity cannot see: without this
	// check a wrong active account boots fine and reports `ok` with zero
	// counts forever, the silent-blindness failure mode the preflight is
	// there to prevent.
	CheckRepoVisible(ctx context.Context) error
	// PRStatesSince fetches the live lifecycle (state + mergedAt) of every PR the
	// bot has reviewed — it only ever approves, so reviewed-by:@me == approved-by-me
	// — that was updated at or after since, in ONE batched call, for the engine's
	// tail-of-cycle Approval-Feed refresh. It returns a number->raw map (a superset
	// of today's feed; the engine intersects it against today's numbers). Decode-
	// only: CollapsePRState judges each bucket. Replaces the per-PR gh-pr-view N+1
	// that did not survive a higher cycle cadence (ADR 0007).
	PRStatesSince(ctx context.Context, since time.Time) (map[int]RawPRState, error)
	// Diff fetches one PR's changed files on demand (the queue's Diff pill), in a
	// single `gh api .../files` call bounded to one page. User-triggered, never on
	// the cycle path — the sanctioned exception to the no-per-PR-call rule (ADR
	// 0008). Files past the page cap are simply not returned; the caller compares
	// the count against the PR's changed_files to render a "first N of M" banner.
	Diff(ctx context.Context, number int) ([]FileDiff, error)
	// MergeInfo fetches one PR's live title, body, and reviews in a single
	// `gh pr view` at the moment of merge — a sanctioned per-PR call in the ADR
	// 0008 sense (rare, consented via the Arm; fires only on an actual merge),
	// which guarantees the commit message is built from the PR description as
	// it is NOW, never a stale arm-time copy (ADR 0016). Decode-only:
	// CommitMessage/ApprovedBy judge the details.
	MergeInfo(ctx context.Context, number int) (MergeDetails, error)
	// Merge squash-merges one PR with the given commit subject and body,
	// deleting the branch — the gh-land command shape
	// `gh pr merge <n> -s -d -t <subject> -b <body>` (ADR 0016). The engine
	// owns the preconditions and the one immediate retry; this call only
	// executes.
	Merge(ctx context.Context, number int, subject, body string) error
}

// CLI is the production GitHubClient. It shells out to the gh CLI, reusing the
// user's existing auth (no PAT).
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

// NewCLI returns a GitHubClient backed by the gh CLI, scoped to the given
// candidate set (repo "owner/name" and the candidate `--search` query).
func NewCLI(repo, search string) *CLI { return &CLI{repo: repo, search: search} }

// ghListItem mirrors the JSON gh emits for `gh pr list --json
// number,title,author,url,additions,deletions,changedFiles`. Author is nested
// under author.login; additions/deletions/changedFiles are top-level diff counts.
type ghListItem struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Additions    int  `json:"additions"`
	Deletions    int  `json:"deletions"`
	ChangedFiles int  `json:"changedFiles"`
	IsDraft      bool `json:"isDraft"`
	// StatusCheckRollup is gh's heterogeneous array of check entries for the PR,
	// pulled in the same single list call (no per-PR N+1).
	StatusCheckRollup []Check `json:"statusCheckRollup"`
	// ReviewDecision is gh's coarse review-state signal, pulled in the same single
	// list call (no per-PR N+1) to drive the approved-elsewhere soft dedup.
	ReviewDecision string `json:"reviewDecision"`
	// Mergeable is gh's mergeability signal, requested only by the authored
	// (outbound) list call; the inbound call leaves it empty.
	Mergeable string `json:"mergeable"`
}

// listJSONFields is the --json field set of the inbound candidate list call —
// everything the cycle needs from ONE call (no per-PR N+1).
const listJSONFields = "number,title,author,url,additions,deletions,changedFiles,isDraft,statusCheckRollup,reviewDecision"

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

// ListCandidates pulls the inbound candidate set once via a single gh call.
func (c *CLI) ListCandidates(ctx context.Context) ([]PR, error) {
	return c.list(ctx, c.search, listJSONFields)
}

// ListAuthored pulls the outbound candidate set once via a second single gh
// call against the same repo: the fixed AuthoredSearch, with mergeable riding
// the call (needed for the Ready conflict marker and, in a later slice, the
// merge precondition). Decode-only — ClassifyOutboundStage judges the stages.
func (c *CLI) ListAuthored(ctx context.Context) ([]PR, error) {
	return c.list(ctx, AuthoredSearch, listJSONFields+",mergeable")
}

// list runs one `gh pr list` over the given search and --json field set and
// decodes each item into a PR. Both per-cycle pulls (inbound candidates,
// outbound authored) flow through here so the decode never forks.
func (c *CLI) list(ctx context.Context, search, jsonFields string) ([]PR, error) {
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

	prs := make([]PR, 0, len(items))
	for _, it := range items {
		prs = append(prs, PR{
			Number:         it.Number,
			Title:          it.Title,
			Author:         it.Author.Login,
			URL:            it.URL,
			Additions:      it.Additions,
			Deletions:      it.Deletions,
			ChangedFiles:   it.ChangedFiles,
			IsDraft:        it.IsDraft,
			Checks:         it.StatusCheckRollup,
			ReviewDecision: it.ReviewDecision,
			Mergeable:      it.Mergeable,
		})
	}
	return prs, nil
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
// the candidate search cannot supply them. It decodes the array into a
// number->raw map; the engine intersects against today's feed and CollapsePRState
// judges each open|merged|closed bucket. A result that hits the limit is warned
// (no silent truncation). Replaces the per-PR `gh pr view` N+1 (ADR 0007).
func (c *CLI) PRStatesSince(ctx context.Context, since time.Time) (map[int]RawPRState, error) {
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

	// One list item carries the PR number alongside the same raw (state, mergedAt)
	// pair PRState decoded; RawPRState's json tags promote through the embedding.
	var items []struct {
		Number int `json:"number"`
		RawPRState
	}
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil, fmt.Errorf("decode gh pr list (pr state) output: %w", err)
	}
	if len(items) == prStateRefreshLimit {
		slog.Default().Warn("pr state refresh hit the result limit; some states may be missing this cycle",
			"limit", prStateRefreshLimit,
		)
	}

	states := make(map[int]RawPRState, len(items))
	for _, it := range items {
		states[it.Number] = it.RawPRState
	}
	return states, nil
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
// for the global search endpoint. Decode-only: each PR node decodes into its
// RawReviewThreads; the pure UnresolvedCount judges the fold. A full search
// page is warned on (no silent truncation).
func (c *CLI) UnresolvedThreads(ctx context.Context) (map[int]RawReviewThreads, error) {
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

	// One search node carries the PR number alongside its reviewThreads page —
	// the raw shape RawReviewThreads mirrors.
	var out struct {
		Data struct {
			Search struct {
				Nodes []struct {
					Number        int `json:"number"`
					ReviewThreads struct {
						Nodes    []ReviewThread `json:"nodes"`
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"reviewThreads"`
				} `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode gh api graphql (review threads) output: %w", err)
	}

	nodes := out.Data.Search.Nodes
	if len(nodes) == threadSearchPageLimit {
		slog.Default().Warn("unresolved-threads search hit the page limit; some PRs' threads may be missing this cycle",
			"limit", threadSearchPageLimit,
		)
	}
	threads := make(map[int]RawReviewThreads, len(nodes))
	for _, n := range nodes {
		threads[n.Number] = RawReviewThreads{
			Nodes:        n.ReviewThreads.Nodes,
			HasMorePages: n.ReviewThreads.PageInfo.HasNextPage,
		}
	}
	return threads, nil
}

// diffPageSize bounds the on-demand diff fetch to one page. The Diff card is a
// skim aid, not a GitHub mirror — a PR with more changed files than this shows
// the first page under a "first N of M" banner (ADR 0008).
const diffPageSize = 100

// Diff fetches one PR's changed files via a single `gh api .../files` call,
// bounded to diffPageSize. Each element decodes straight into a FileDiff; a file
// GitHub omits the patch for (binary/over-large) decodes with an empty Patch.
func (c *CLI) Diff(ctx context.Context, number int) ([]FileDiff, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=%d", c.repo, number, diffPageSize)
	cmd := exec.CommandContext(ctx, "gh", "api", endpoint)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api %s: %w: %s", endpoint, err, stderr.String())
	}

	var files []FileDiff
	if err := json.Unmarshal(stdout.Bytes(), &files); err != nil {
		return nil, fmt.Errorf("decode gh api files output: %w", err)
	}
	return files, nil
}

// ghViewItem mirrors the JSON gh emits for `gh pr view --json
// title,body,reviews`. Reviewer logins nest under author.login, like the list
// call's author.
type ghViewItem struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Reviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		State string `json:"state"`
	} `json:"reviews"`
}

// MergeInfo fetches one PR's live merge-time details via a single
// `gh pr view` (the sanctioned per-merge call, ADR 0016). Decode-only —
// CommitMessage judges the details into the commit message.
func (c *CLI) MergeInfo(ctx context.Context, number int) (MergeDetails, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(number),
		"--repo", c.repo,
		"--json", "title,body,reviews",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return MergeDetails{}, fmt.Errorf("gh pr view %d: %w: %s", number, err, stderr.String())
	}

	var item ghViewItem
	if err := json.Unmarshal(stdout.Bytes(), &item); err != nil {
		return MergeDetails{}, fmt.Errorf("decode gh pr view output: %w", err)
	}
	details := MergeDetails{Title: item.Title, Body: item.Body}
	for _, r := range item.Reviews {
		details.Reviews = append(details.Reviews, Review{Author: r.Author.Login, State: r.State})
	}
	return details, nil
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
