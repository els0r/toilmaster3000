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
	Approve(ctx context.Context, number int) error
	CurrentUser(ctx context.Context) (string, error)
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
}

// ListCandidates pulls the candidate set once via a single gh call.
func (c *CLI) ListCandidates(ctx context.Context) ([]PR, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
		"--repo", c.repo,
		"--search", c.search,
		"--json", "number,title,author,url,additions,deletions,changedFiles,isDraft,statusCheckRollup",
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
			Number:       it.Number,
			Title:        it.Title,
			Author:       it.Author.Login,
			URL:          it.URL,
			Additions:    it.Additions,
			Deletions:    it.Deletions,
			ChangedFiles: it.ChangedFiles,
			IsDraft:      it.IsDraft,
			Checks:       it.StatusCheckRollup,
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
