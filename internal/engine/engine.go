// Package engine runs the find->approve loop and owns the single
// mutex-guarded in-memory store (dedup set, approvals feed, last cycle status).
// All approvals — automatic now, manual in a later slice — flow through one
// locked approve() path, and every HTTP read is a locked read.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
)

// DefaultPollInterval is the default wait between cycles. The driver sleeps
// this long AFTER each cycle (never a Ticker) so a slow cycle can never overlap
// itself. 5m is calm against the GitHub API; 1m is aggressive. Override per-run
// with SetPollInterval (wired to the --poll-interval flag in main).
const DefaultPollInterval = 5 * time.Minute

// MinPollInterval is the floor for the poll interval. Anything under a minute
// hammers the GitHub API for no benefit, so main rejects it at startup.
const MinPollInterval = time.Minute

// Approval is one approval record: the engine's internal read-model and the
// on-disk shape of one line in approvals.jsonl — the json tags serve that disk
// format. It is NOT the wire shape; the /approvals wire DTO is server.Approval,
// which the server maps to via approvalToBody (ADR 0002). The two are
// field-identical today only because the project uses one snake_case convention.
type Approval struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	URL         string    `json:"url"`
	MatchedRule string    `json:"matched_rule"`
	ApprovedAt  time.Time `json:"approved_at"`
}

// Status is the last cycle's outcome and counts — the engine's internal
// read-model, carrying no json tags. The /status wire DTO is server.CycleStatus
// (ADR 0002).
type Status struct {
	LastRun       *time.Time
	Outcome       string
	ApprovedCount int
	QueueCount    int
	// DroppedCount is how many candidates an eligibility gate dropped this cycle
	// (a draft PR today) — counted before parsing/matching, never approved nor
	// queued. A failed fetch evaluated nothing, so it is 0.
	DroppedCount int
}

// QueueItem is one Needs-Human-Review entry: a PR routed here for one or more
// reasons (MVP today: a breaking-change title blocking an Approve-Rule match;
// Review Rules add their names in a later slice). It is the engine's internal
// read-model, derived live — recomputed each cycle from the candidate set, never
// persisted — and carries a reasons LIST so a PR can be queued for several
// reasons at once. It is NOT the wire shape; the /queue wire DTO is
// server.QueueItem, mapped via queueItemToBody (ADR 0002).
type QueueItem struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author string `json:"author"`
	URL    string `json:"url"`
	// Additions, Deletions, and ChangedFiles are the PR's diff magnitude, carried
	// from the candidate fetch so a human triaging the queue can tell a small fix
	// from a large refactor. They are display-only here — the diff-size rule
	// predicate sums additions+deletions itself in evaluateRules.
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	ChangedFiles int      `json:"changed_files"`
	Reasons      []string `json:"reasons"`
}

// reasonBreakingChange is the queue reason for a PR blocked because its
// conventional-commit title carries the breaking "!" marker.
const reasonBreakingChange = "breaking_change"

// ManualApprovalPrefix is stamped on the matched_rule of every manual queue
// override (ApproveManually writes "<prefix><reasons joined>"). It is the single
// source of truth for the manual-vs-auto distinction: the server derives the
// wire Approval.manual flag by testing this prefix, so the two never drift.
const ManualApprovalPrefix = "human approval: "

// Engine owns the find->approve loop and the in-memory store. The zero value
// is not usable; construct with New.
type Engine struct {
	client    github.GitHubClient
	statePath string
	rules     *rule.Store
	logger    *slog.Logger

	mu    sync.Mutex
	dedup map[int]bool
	feed  []Approval  // newest-first
	queue []QueueItem // live Needs-Human-Review snapshot, recomputed each cycle
	// prStates is the live GitHub lifecycle of feed PRs, keyed by number. It is
	// volatile and NEVER persisted (the approvals.jsonl record is the frozen
	// approval moment): refreshed out-of-band at the tail of every cycle, empty
	// after a restart until the first refresh. A missing entry reads as unknown.
	prStates     map[int]github.PRState
	status       Status
	selfLogin    string        // resolved @me token (see identity.go)
	pollInterval time.Duration // wait between cycles; default DefaultPollInterval
}

// New constructs an Engine over the given client, approvals.jsonl path, and
// rule store. It loads any existing approvals into the dedup set and feed so
// approvals survive restart and are not re-approved. The rule store supplies
// the enabled rules each cycle consults to decide which candidates to approve.
func New(client github.GitHubClient, statePath string, rules *rule.Store) (*Engine, error) {
	e := &Engine{
		client:       client,
		statePath:    statePath,
		rules:        rules,
		logger:       slog.Default(),
		dedup:        map[int]bool{},
		prStates:     map[int]github.PRState{},
		status:       Status{Outcome: "never_run"},
		pollInterval: DefaultPollInterval,
	}
	if err := e.load(); err != nil {
		return nil, fmt.Errorf("load approvals: %w", err)
	}
	return e, nil
}

// Status returns a copy of the last cycle's status (locked read).
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// Approvals returns the approval feed, newest-first (locked read).
func (e *Engine) Approvals() []Approval {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Approval, len(e.feed))
	copy(out, e.feed)
	return out
}

// PRStates returns a snapshot of the known PR States keyed by number (locked
// read). A number absent from the map has not been refreshed yet; the wire layer
// reads that as the neutral "unknown" — PR State is never guessed.
func (e *Engine) PRStates() map[int]github.PRState {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[int]github.PRState, len(e.prStates))
	maps.Copy(out, e.prStates)
	return out
}

// Queue returns the live Needs-Human-Review queue snapshot (locked read). The
// queue is recomputed each cycle, so this reflects the current truth as of the
// last completed cycle.
func (e *Engine) Queue() []QueueItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]QueueItem, len(e.queue))
	copy(out, e.queue)
	return out
}

// ErrNotInQueue is returned by ApproveManually when the given PR number is not
// in the current Needs-Human-Review queue snapshot (unknown, merged/closed, no
// longer matching, or already approved).
var ErrNotInQueue = errors.New("pr not in needs-human-review queue")

// ApproveManually is the explicit human override that approves a PR blocked in
// the Needs-Human-Review queue (the only path that can approve a breaking
// change). It looks the PR up in the CURRENT queue snapshot — which carries the
// title/author/url and its reasons — and, if present, approves it through the
// SAME locked approve() path used by the cycle, recording matched_rule as
// "human approval: <reasons joined>" so the feed self-documents why a human
// stepped in.
//
// Concurrency: the snapshot read and the approve() call both take the engine
// mutex (separately, not held across), and approve() is the single funnel that
// re-checks the dedup set under lock. So a manual approve racing a cycle is
// safe — whichever reaches approve() first wins and the other is a quiet
// idempotent no-op; the manually-approved PR then sits in the dedup set and
// drops out of the queue on the next cycle rebuild.
func (e *Engine) ApproveManually(ctx context.Context, number int) error {
	item, ok := e.queueItem(number)
	if !ok {
		return fmt.Errorf("%w: #%d", ErrNotInQueue, number)
	}
	pr := github.PR{Number: item.Number, Title: item.Title, Author: item.Author, URL: item.URL}
	matchedRule := ManualApprovalPrefix + strings.Join(item.Reasons, ", ")
	e.logger.Info("human review decision: manual override approve",
		"pr", number,
		"reasons", item.Reasons,
	)
	if _, err := e.approve(ctx, pr, matchedRule); err != nil {
		return fmt.Errorf("manual approve #%d: %w", number, err)
	}
	return nil
}

// Diff fetches one queued PR's changed files on demand (the queue's Diff pill).
// It is scoped to the CURRENT queue snapshot — a number absent from it is
// ErrNotInQueue and never reaches gh (the pill is queue-only). The returned
// totalFiles is the queue item's changed_files, the authoritative count for the
// caller's "first N of M files" banner; the fetched files may be fewer (the gh
// seam returns one page — ADR 0008). The on-demand gh call is the sanctioned
// exception to the no-per-PR-call rule (ADR 0007), as it never rides the cycle.
func (e *Engine) Diff(ctx context.Context, number int) (files []github.FileDiff, totalFiles int, err error) {
	item, ok := e.queueItem(number)
	if !ok {
		return nil, 0, fmt.Errorf("%w: #%d", ErrNotInQueue, number)
	}
	files, err = e.client.Diff(ctx, number)
	if err != nil {
		return nil, 0, fmt.Errorf("diff #%d: %w", number, err)
	}
	return files, item.ChangedFiles, nil
}

// queueItem returns the queue entry for the given PR number from the current
// snapshot (locked read).
func (e *Engine) queueItem(number int) (QueueItem, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, q := range e.queue {
		if q.Number == number {
			return q, true
		}
	}
	return QueueItem{}, false
}

// RunCycleOnce runs exactly one find->approve cycle synchronously: it fetches
// the candidate set once, then for each candidate evaluates the enabled rules
// (in file order) and approves — through the locked approve() path — only those
// matched by at least one enabled rule. A failed fetch skips the whole cycle
// and is recorded as the cycle outcome; one PR's approval failure is logged and
// skipped without aborting the cycle. The background loop calls this too.
func (e *Engine) RunCycleOnce(ctx context.Context) {
	now := time.Now()
	e.logger.Info("cycle: starting")

	// PR State is refreshed at the tail of EVERY cycle (deferred so it runs even on
	// a list-fetch failure or a cycle that approved nothing): a PR approved earlier
	// can merge/close between cycles independent of new candidates.
	defer e.refreshPRStates(ctx)

	candidates, err := e.client.ListCandidates(ctx)
	if err != nil {
		e.logger.Warn("cycle: list candidates failed, skipping cycle", "error", err)
		e.recordCycle(now, fmt.Sprintf("gh error: %v", err), 0, 0, nil)
		return
	}

	selfLogin := e.SelfLogin()
	rules := e.rules.List()

	approved := 0
	dropped := 0
	// queue is rebuilt FRESH each cycle: it is current state, never persisted.
	// An item leaves the queue naturally when its PR is no longer a candidate
	// (merged/closed), stops matching, or has been manually approved (it then
	// sits in the dedup set and is skipped by the already-approved guard below).
	queue := []QueueItem{}
	for _, pr := range candidates {
		if e.alreadyApproved(pr.Number) {
			// Already approved (auto or manual): never re-approve, never queue.
			continue
		}
		if pr.IsDraft {
			// Eligibility gate: a draft PR is ineligible and is dropped before it
			// is ever parsed, matched, queued, or approved. It is counted toward the
			// cycle's dropped total.
			e.logger.Info("cycle: PR dropped, ineligible",
				"pr", pr.Number,
				"gate", "draft",
			)
			dropped++
			continue
		}
		if !github.AllGreen(pr.Checks) {
			// Eligibility gate: a PR whose pipeline is not all-green (a failing or
			// pending check, or no checks at all) is ineligible and dropped before it
			// is ever parsed, matched, queued, or approved. A pending pipeline simply
			// isn't a candidate this cycle; it becomes eligible on a later cycle once
			// the checks pass (no persistent waiting state). Counted toward the
			// cycle's dropped total.
			e.logger.Info("cycle: PR dropped, ineligible",
				"pr", pr.Number,
				"gate", "not_all_green",
			)
			dropped++
			continue
		}
		c, parsedOK := conventionalcommit.Parse(pr.Title)
		reasons, approveRuleName, approveMatched := evaluateRules(e.logger, rules, c, parsedOK, pr, selfLogin)

		if len(reasons) > 0 {
			// Review Rules win: any Review-Rule match (and/or a breaking
			// Approve-Rule match) routes the PR to Needs-Human-Review — NEVER
			// auto-approve — carrying every collected reason.
			e.logger.Info("cycle: PR routed to needs-human-review queue",
				"pr", pr.Number,
				"reasons", reasons,
			)
			queue = append(queue, QueueItem{
				Number:       pr.Number,
				Title:        pr.Title,
				Author:       pr.Author,
				URL:          pr.URL,
				Additions:    pr.Additions,
				Deletions:    pr.Deletions,
				ChangedFiles: pr.ChangedFiles,
				Reasons:      reasons,
			})
			continue
		}
		if !approveMatched {
			// No reason to queue and no Approve Rule matched (or the title did not
			// parse): never approve, never queue.
			continue
		}
		// An Approve Rule matched, no Review Rule gated it, and the title is not
		// breaking (else reasons would carry breaking_change). Auto-approve.
		ok, err := e.approve(ctx, pr, approveRuleName)
		if err != nil {
			e.logger.Warn("cycle: approve PR failed, skipping (retry next cycle)", "pr", pr.Number, "error", err)
			continue
		}
		if ok {
			approved++
		}
	}

	e.logger.Info("cycle: complete",
		"candidates", len(candidates),
		"approved", approved,
		"queued", len(queue),
		"dropped", dropped,
	)
	e.recordCycle(now, "ok", approved, dropped, queue)
}

// alreadyApproved reports whether the PR is already in the dedup set (locked
// read). Used to skip already-approved PRs both for auto-approval and for queue
// rebuilds, so a manually-approved breaking PR drops out of the queue next cycle.
func (e *Engine) alreadyApproved(number int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dedup[number]
}

// evaluateRules applies the Review-vs-Approve evaluation precedence to one
// candidate's already-parsed title (CONTEXT "Evaluation order"). It is pure (no
// I/O); the title is parsed by the caller so the breaking-change invariant gates
// off the same parse. The parse-gate is uniform: a non-conventional title is a
// non-match for both classes (every Matches call short-circuits on !parsedOK),
// so it yields no reasons and no Approve match.
//
// It returns:
//   - reasons: the Name of EVERY enabled Review Rule that matches (file order),
//     plus "breaking_change" LAST iff an Approve Rule also matched and the title
//     is breaking (breaking stays tied to an Approve match);
//   - approveRuleName / approveMatched: the FIRST enabled Approve Rule that
//     matches, for auto-approve attribution.
//
// The caller queues when reasons is non-empty (Review Rules win, never approve),
// else auto-approves when approveMatched and the title is not breaking, else
// skips. A rule whose regex fails to compile is logged and skipped — seeded/
// validated rules always compile, so this is a config fault.
func evaluateRules(logger *slog.Logger, rules []rule.Rule, c conventionalcommit.Commit, parsedOK bool, pr github.PR, selfLogin string) (reasons []string, approveRuleName string, approveMatched bool) {
	diffSize := pr.Additions + pr.Deletions // the matcher takes the SUM of the two fields

	matches := func(r rule.Rule) bool {
		match, err := r.Matches(c, parsedOK, pr.Author, selfLogin, diffSize)
		if err != nil {
			logger.Warn("cycle: rule failed to evaluate PR, skipping rule", "rule", r.Name, "pr", pr.Number, "error", err)
			return false
		}
		return match
	}

	// Review pass: collect EVERY matching enabled Review Rule's name, file order.
	for _, r := range rules {
		if r.Enabled && r.IsReview() && matches(r) {
			reasons = append(reasons, r.Name)
		}
	}

	// Approve pass: the FIRST matching enabled Approve Rule (Class != review).
	for _, r := range rules {
		if r.Enabled && !r.IsReview() && matches(r) {
			approveRuleName, approveMatched = r.Name, true
			break
		}
	}

	// breaking_change is tied to an Approve match: appended LAST, only when an
	// Approve Rule matched a breaking title.
	if approveMatched && c.Breaking {
		reasons = append(reasons, reasonBreakingChange)
	}

	return reasons, approveRuleName, approveMatched
}

// approve is the single locked path through which every approval flows. It
// checks the dedup set, calls the client, and on success appends to
// approvals.jsonl AND the in-memory feed and dedup set. The record is written
// ONLY on success, so a failed approval is retried next cycle. It returns
// (true, nil) when a new approval was made, (false, nil) when the PR was
// already in the dedup set (idempotent, quiet).
func (e *Engine) approve(ctx context.Context, pr github.PR, matchedRule string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.dedup[pr.Number] {
		return false, nil
	}

	if err := e.client.Approve(ctx, pr.Number); err != nil {
		return false, err
	}

	rec := Approval{
		Number:      pr.Number,
		Title:       pr.Title,
		Author:      pr.Author,
		URL:         pr.URL,
		MatchedRule: matchedRule,
		ApprovedAt:  time.Now(),
	}
	if err := e.appendRecord(rec); err != nil {
		// Persisting failed after the PR was approved on GitHub. Do not add to
		// the dedup set, so the in-memory and on-disk views stay consistent and
		// the (now harmlessly idempotent) approval is retried next cycle.
		return false, fmt.Errorf("persist approval #%d: %w", pr.Number, err)
	}

	e.dedup[pr.Number] = true
	e.feed = append([]Approval{rec}, e.feed...) // newest-first
	e.logger.Info("approved PR",
		"pr", pr.Number,
		"author", pr.Author,
		"matched_rule", matchedRule,
		"url", pr.URL,
	)
	return true, nil
}

// recordCycle stores the last cycle's outcome and counts under lock, and
// replaces the live queue snapshot with the freshly-recomputed queue. A failed
// fetch passes a nil queue: nothing was evaluated, so the queue is cleared
// (current truth is "unknown"; we approve and queue nothing). queue_count in
// the status reflects len(queue).
func (e *Engine) recordCycle(at time.Time, outcome string, approved, dropped int, queue []QueueItem) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := at
	e.queue = queue
	e.status = Status{
		LastRun:       &t,
		Outcome:       outcome,
		ApprovedCount: approved,
		QueueCount:    len(queue),
		DroppedCount:  dropped,
	}
}

// refreshPRStates updates the in-memory PR State of TODAY's feed entries from a
// SINGLE batched fetch (ADR 0007 — replaces the per-PR gh-pr-view N+1 that did
// not survive a higher cycle cadence). It fetches the lifecycle of every PR the
// bot reviewed since local midnight (a superset of today's feed) in one call,
// then writes the state of each today's-feed entry PRESENT in the result —
// intersecting locally, so strangers from the over-fetch are ignored. The
// approval moment in approvals.jsonl stays frozen (out-of-band).
//
// An empty feed is fetched for nothing, so the call is skipped. The refresh is
// all-or-nothing: a failed fetch is logged once and skipped, keeping ALL last-
// known state, never aborting the cycle (the per-PR approve failure semantics,
// applied wholesale). A today's-feed number ABSENT from the result (search-index
// lag, or aged out of the updated:>= window) keeps its last-known state — the map
// is updated in-place, never cleared — so a freshly-approved PR reads unknown for
// one cycle rather than flickering known->unknown->known.
func (e *Engine) refreshPRStates(ctx context.Context) {
	todays := e.todaysFeed()
	if len(todays) == 0 {
		return
	}

	raw, err := e.client.PRStatesSince(ctx, startOfLocalDay(time.Now()))
	if err != nil {
		e.logger.Warn("cycle: PR state refresh failed, keeping last known state", "error", err)
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for _, a := range todays {
		r, ok := raw[a.Number]
		if !ok {
			continue // absent from the batch: keep last-known, never reset to unknown
		}
		e.prStates[a.Number] = github.CollapsePRState(r.State, r.MergedAt)
	}
}

// todaysFeed returns the feed entries approved at or after local midnight
// (workstation tz) — the same today-scope the wire feed shows, so PR State is
// refreshed for exactly the entries that can render (locked read).
func (e *Engine) todaysFeed() []Approval {
	cutoff := startOfLocalDay(time.Now())
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Approval, 0, len(e.feed))
	for _, a := range e.feed {
		if a.ApprovedAt.Before(cutoff) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// startOfLocalDay returns local midnight of t's day in t's location — the
// inclusive today-scope cutoff. The server applies the same cutoff at the wire
// boundary; the engine needs it independently to scope the PR-State refresh.
func startOfLocalDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// SetPollInterval overrides the wait between cycles (locked write). main wires
// it to the --poll-interval flag before starting Run. A non-positive duration
// is ignored, keeping the existing interval rather than busy-looping.
func (e *Engine) SetPollInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pollInterval = d
}

// PollInterval returns the current wait between cycles (locked read).
func (e *Engine) PollInterval() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pollInterval
}

// Run drives the loop in a single goroutine: runCycle, then sleep, repeat —
// the sleep is AFTER the cycle so a slow cycle never overlaps itself. It
// returns when ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	for {
		e.RunCycleOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.PollInterval()):
		}
	}
}
