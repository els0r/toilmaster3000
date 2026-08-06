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

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/hook"
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
	// ScreenHolds is the prose behind a screen-held entry: one {screen, reason}
	// per holding Screen, mirroring the screen:<name> entries in Reasons (chips
	// stay chips; the AI's reasoning is one click away — ADR 0022). Empty on
	// rule-routed entries: rule reasons XOR screen holds, disjoint by
	// construction (ADR 0021).
	ScreenHolds []ScreenHold `json:"screen_holds"`
}

// ScreenHold is one holding Screen on a screen-held queue entry: the screen's
// user-facing name and its verdict's prose reason.
type ScreenHold struct {
	Screen string `json:"screen"`
	Reason string `json:"reason"`
}

// FunnelItem is one PR itemized in a terminal funnel bucket (dropped_red,
// dropped_draft, staging, approved_elsewhere) of the Cycle Funnel snapshot. It
// is the engine's internal read-model — derived live each cycle, never persisted
// — carrying just enough to render a row; the wire DTO is server.FunnelItem,
// mapped via funnelItemToBody (ADR 0002). FailingChecks is meaningful only on
// the dropped_red bucket (the "N checks failing" signal, folded from the rollup
// already in hand); it is 0 on every other bucket. It carries no json tags: the
// funnel is never persisted (unlike Approval/QueueItem, whose tags serve a disk
// format), so it is a pure in-memory read-model like Status.
type FunnelItem struct {
	Number        int
	Title         string
	Author        string
	URL           string
	FailingChecks int
	// Additions, Deletions, and ChangedFiles are the PR's diff magnitude, threaded
	// from the github.PR's same fields (the single list fetch — no extra call). The
	// Staging area renders them; they are populated on every bucket, harmless where
	// unused.
	Additions    int
	Deletions    int
	ChangedFiles int
	// PendingScreens names the enabled Screens still awaiting a verdict for the
	// PR's current head — meaningful only on the screening bucket (the station's
	// "why hasn't #N gone through?" signal, ADR 0022); nil on every other bucket
	// (the FailingChecks pattern).
	PendingScreens []string
}

// Funnel is the live Cycle Funnel snapshot: what each cycle saw, retained
// instead of discarded. It holds the FIVE terminal item lists the cycle used to
// drop plus the distribution counts that partition Incoming, and is swapped
// under lock at cycle end (same lifecycle as the queue: empty after restart
// until the first cycle, current as of the last completed cycle; a failed fetch
// clears it). The raw Incoming PR set is NOT hoarded — Incoming renders as the
// distribution bar (counts), so only the five lists + counts are kept.
//
// Partition invariant (CONTEXT "Cycle Funnel"): every Incoming PR lands in
// EXACTLY one terminal stage, so
//
//	Incoming = len(DroppedRed) + len(DroppedDraft) + len(Staging) + len(Screening)
//	         + NeedsHumanReview + ApprovedByTm3k + len(ApprovedElsewhere)
//
// Screening is the awaiting-verdict segment (ADR 0022): eligible,
// Approve-Rule-matched, but at least one enabled Screen has no verdict yet for
// the PR's current head — transient by design, terminal within the cycle.
// ApprovedByTm3k is the STANDING segment — every dedup-set member still in this
// cycle's pull (any day) — distinct from ApprovedThisCycle (newly approved this
// cycle); both are counts, only the five lists are itemized.
type Funnel struct {
	Incoming          int
	DroppedRed        []FunnelItem
	DroppedDraft      []FunnelItem
	Staging           []FunnelItem
	Screening         []FunnelItem
	ApprovedElsewhere []FunnelItem
	NeedsHumanReview  int
	ApprovedByTm3k    int
	ApprovedThisCycle int
}

// reasonBreakingChange is the queue reason for a PR blocked because its
// conventional-commit title carries the breaking "!" marker.
const reasonBreakingChange = "breaking_change"

// reasonScreenPrefix prefixes the queue reason a holding Screen contributes:
// "screen:" + the screen's user-facing Name (ADR 0022).
const reasonScreenPrefix = "screen:"

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
	// mergesPath is the merge ledger file (merges.jsonl) — the outbound analog
	// of statePath's approvals.jsonl, appended only on a successful merge (see
	// merges.go).
	mergesPath string
	rules      *rule.Store
	// armed is the persisted Armed/Withheld consent set of the outbound
	// direction (ADR 0016) — see armed.go for the arm lifecycle the engine
	// enforces (snapshot-validated arming, level-triggered disarm, cleanup).
	armed *armed.Store
	// screener is the screening dependency (ADR 0022): the level-triggered
	// verdict consult + async run dispatch the approve branch gates through.
	// nil means no screens are configured — the consult is skipped entirely
	// and behavior is bit-for-bit pre-screening.
	screener *hook.Screener
	logger   *slog.Logger

	mu     sync.Mutex
	dedup  map[int]bool
	feed   []Approval // newest-first
	// merges is the in-memory merge ledger, newest-first — loaded from
	// merges.jsonl at startup, appended on every successful outbound merge
	// (see merges.go). Durable by design: a merged PR leaves the is:open pull
	// immediately, so the ledger is the only signal left for the Merged
	// station and the heartbeat's merged count.
	merges []Merge
	queue  []QueueItem // live Needs-Human-Review snapshot, recomputed each cycle
	funnel Funnel      // live Cycle Funnel snapshot, recomputed each cycle
	// outbound is the live outbound funnel snapshot (the authored direction),
	// recomputed each cycle from its OWN list call and cleared when that call
	// fails — see outbound.go.
	outbound Outbound
	// prStates is the live GitHub lifecycle of feed PRs, keyed by number. It is
	// volatile and NEVER persisted (the approvals.jsonl record is the frozen
	// approval moment): refreshed out-of-band at the tail of every cycle, empty
	// after a restart until the first refresh. A missing entry reads as unknown.
	prStates     map[int]github.PRState
	status       Status
	selfLogin    string        // resolved @me token (see identity.go)
	pollInterval time.Duration // wait between cycles; default DefaultPollInterval
}

// New constructs an Engine over the given client, approvals.jsonl path,
// merges.jsonl path, rule store, armed store, and screener. It loads any
// existing approvals into the dedup set and feed so approvals survive restart
// and are not re-approved, and any existing merge ledger so the Merged station
// survives restart too. The rule store supplies the enabled rules each cycle
// consults to decide which candidates to approve; the armed store carries the
// outbound consent set the cycle reconciles (see armed.go) and the merge step
// obeys (see merge.go); the screener gates the approve branch through the
// level-triggered verdict consult (ADR 0022) — nil means no screens are
// configured and the branch is skipped entirely.
func New(client github.GitHubClient, statePath, mergesPath string, rules *rule.Store, arms *armed.Store, screener *hook.Screener) (*Engine, error) {
	e := &Engine{
		client:       client,
		statePath:    statePath,
		mergesPath:   mergesPath,
		rules:        rules,
		armed:        arms,
		screener:     screener,
		logger:       slog.Default(),
		dedup:        map[int]bool{},
		prStates:     map[int]github.PRState{},
		status:       Status{Outcome: "never_run"},
		pollInterval: DefaultPollInterval,
	}
	if err := e.load(); err != nil {
		return nil, fmt.Errorf("load approvals: %w", err)
	}
	if err := e.loadMerges(); err != nil {
		return nil, fmt.Errorf("load merges: %w", err)
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

// Funnel returns the live Cycle Funnel snapshot (locked read). It is recomputed
// each cycle, so this reflects the current truth as of the last completed cycle;
// it is the zero value after a restart until the first cycle, and a failed
// candidate fetch clears it. The slices are copied so a caller cannot mutate the
// engine's snapshot.
func (e *Engine) Funnel() Funnel {
	e.mu.Lock()
	defer e.mu.Unlock()
	f := e.funnel
	f.DroppedRed = append([]FunnelItem(nil), e.funnel.DroppedRed...)
	f.DroppedDraft = append([]FunnelItem(nil), e.funnel.DroppedDraft...)
	f.Staging = append([]FunnelItem(nil), e.funnel.Staging...)
	f.Screening = append([]FunnelItem(nil), e.funnel.Screening...)
	f.ApprovedElsewhere = append([]FunnelItem(nil), e.funnel.ApprovedElsewhere...)
	return f
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
	e.applyManualApprove(number)
	return nil
}

// applyManualApprove applies a just-performed manual override to the published
// snapshots (ADR 0018): the engine approved the PR, so serving it in
// Needs-Human-Review until the next cycle rebuild would be a known falsehood.
// Under one lock it removes the PR from the queue snapshot and — only when the
// PR was actually still queued — moves its funnel count from NeedsHumanReview
// to ApprovedByTm3k (the partition keeps summing to Incoming: the PR migrates
// between terminal segments) and decrements the heartbeat's queue count. The
// found-guard makes the mutation a no-op when a cycle rebuild won the race and
// already recomputed the snapshots without the PR — adjusting counts then would
// corrupt the fresh partition. ApprovedThisCycle stays untouched: it is the
// cycle's pulse, and a manual override is not the cycle's doing.
func (e *Engine) applyManualApprove(number int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	queue := make([]QueueItem, 0, len(e.queue))
	for _, q := range e.queue {
		if q.Number != number {
			queue = append(queue, q)
		}
	}
	if len(queue) == len(e.queue) {
		return // a cycle rebuild won the race; the fresh snapshots are already correct
	}
	e.queue = queue
	e.funnel.NeedsHumanReview--
	e.funnel.ApprovedByTm3k++
	e.status.QueueCount--
}

// ErrPRNotTracked is returned by Diff when the given PR number is not
// currently tracked in the Needs-Human-Review queue, Staging, or the outbound
// snapshot — the buckets the on-demand Diff pill may be opened from (ADR 0015,
// widened to outbound by ADR 0017).
var ErrPRNotTracked = errors.New("pr not tracked in queue, staging, or outbound")

// Diff fetches one tracked PR's changed files on demand (the Diff pill, opened
// from the queue, Staging, or an outbound stage row). It is scoped to the
// CURRENT snapshots — a number absent from all is ErrPRNotTracked and never
// reaches gh.
// The returned totalFiles is the tracked item's changed_files, the
// authoritative count for the caller's "first N of M files" banner; the
// fetched files may be fewer (the gh seam returns one page — ADR 0008). The
// on-demand gh call is the sanctioned exception to the no-per-PR-call rule
// (ADR 0007), as it never rides the cycle — widening it from the queue alone to
// also cover Staging (ADR 0015) does not reintroduce that per-cycle N+1, since
// it stays bounded by human click-rate, not cycle cadence.
func (e *Engine) Diff(ctx context.Context, number int) (files []github.FileDiff, totalFiles int, err error) {
	changedFiles, ok := e.trackedChangedFiles(number)
	if !ok {
		return nil, 0, fmt.Errorf("%w: #%d", ErrPRNotTracked, number)
	}
	files, err = e.client.Diff(ctx, number)
	if err != nil {
		return nil, 0, fmt.Errorf("diff #%d: %w", number, err)
	}
	return files, changedFiles, nil
}

// trackedChangedFiles returns the authoritative changed_files count for a PR
// number currently tracked in the queue, Staging, or any outbound stage list
// (locked read) — the buckets Diff resolves from. Inbound and outbound pulls
// are disjoint by search, so first match wins without ambiguity.
func (e *Engine) trackedChangedFiles(number int) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, q := range e.queue {
		if q.Number == number {
			return q.ChangedFiles, true
		}
	}
	for _, s := range e.funnel.Staging {
		if s.Number == number {
			return s.ChangedFiles, true
		}
	}
	for _, stage := range [][]OutboundItem{
		e.outbound.Draft, e.outbound.Red, e.outbound.Running,
		e.outbound.ChangesRequested, e.outbound.AwaitingApproval,
		e.outbound.InDiscussion, e.outbound.Ready,
	} {
		for _, o := range stage {
			if o.Number == number {
				return o.ChangedFiles, true
			}
		}
	}
	return 0, false
}

// queueItem returns the queue entry for the given PR number from the current
// snapshot (locked read). Used only by ApproveManually — the manual-override
// approve path must stay queue-only even though Diff (above) also resolves
// Staging: a Staging PR has matched no rule yet, so it must never become
// manually-approvable.
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
// (in file order) and approves — through the locked approve() path — every
// matching PR NOT ALREADY APPROVED. "Already approved" is two cases: it sits in
// tm3k's own dedup set, or GitHub reports it APPROVED by someone else (soft
// dedup, ADR 0013) — both are left alone, never re-approved. A failed fetch
// skips the whole cycle and is recorded as the cycle outcome; one PR's approval
// failure is logged and skipped without aborting the cycle. The background loop
// calls this too.
func (e *Engine) RunCycleOnce(ctx context.Context) {
	now := time.Now()
	e.logger.Info("cycle: starting")

	// PR State is refreshed at the tail of EVERY cycle (deferred so it runs even on
	// a list-fetch failure or a cycle that approved nothing): a PR approved earlier
	// can merge/close between cycles independent of new candidates.
	defer e.refreshPRStates(ctx)

	// The outbound snapshot is rebuilt at the tail of EVERY cycle too (deferred
	// so it runs even when the inbound fetch fails): the two pulls are disjoint
	// by search and fail independently — only a failed OUTBOUND fetch clears the
	// outbound snapshot.
	defer e.rebuildOutbound(ctx)

	candidates, err := e.client.ListCandidates(ctx)
	if err != nil {
		e.logger.Warn("cycle: list candidates failed, skipping cycle", "error", err)
		// A failed fetch evaluated nothing: clear the queue AND the funnel snapshot
		// (the zero Funnel) so neither shows stale buckets.
		e.recordCycle(now, fmt.Sprintf("gh error: %v", err), 0, 0, nil, Funnel{})
		return
	}

	selfLogin := e.SelfLogin()
	rules := e.rules.List()

	approved := 0
	dropped := 0
	// funnel retains what the cycle sees and used to discard: the four terminal
	// item lists + the distribution counts that partition Incoming. It is built in
	// this single pass — every Incoming PR is recorded into EXACTLY one bucket at
	// the first terminal branch it reaches, so the precedence here IS the partition
	// (dedup-approved -> approved-elsewhere -> draft -> not-all-green -> queue ->
	// staging/approve), and the six segment counts sum to Incoming by construction.
	funnel := Funnel{Incoming: len(candidates)}
	// queue is rebuilt FRESH each cycle: it is current state, never persisted.
	// An item leaves the queue naturally when its PR is no longer a candidate
	// (merged/closed), stops matching, or has been manually approved (it then
	// sits in the dedup set and is skipped by the already-approved guard below).
	queue := []QueueItem{}
	for _, pr := range candidates {
		if e.alreadyApproved(pr.Number) {
			// Already approved (auto or manual): never re-approve, never queue. It is
			// the STANDING Approved-by-tm3k segment — every dedup-set member still in
			// the pull, any day (distinct from approvedThisCycle). Counted, not
			// itemized: it is done (in the ledger if today, history otherwise), and
			// counting it keeps Incoming an honest "everything we saw". This precedence
			// sits ABOVE the draft/red gates (US#29), so an already-approved PR folds
			// into its approved segment even when also draft/red.
			funnel.ApprovedByTm3k++
			continue
		}
		if approvedElsewhere(pr) {
			// Soft dedup (ADR 0013): GitHub already reports this PR as APPROVED by
			// someone other than tm3k (it is APPROVED yet absent from approvals.jsonl,
			// so not our approval). The context switch an auto-approver exists to save
			// is already gone, so tm3k leaves it alone — exactly like an already-
			// deduped PR: it does not approve and records nothing to the ledger. This
			// keeps saved-switches analytics from double-counting across a team running
			// multiple tm3k instances.
			e.logger.Info("cycle: PR left alone, approved elsewhere",
				"pr", pr.Number,
			)
			funnel.ApprovedElsewhere = append(funnel.ApprovedElsewhere, funnelItem(pr, 0))
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
			funnel.DroppedDraft = append(funnel.DroppedDraft, funnelItem(pr, 0))
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
			// dropped_red carries the count of non-passing checks, folded cheaply from
			// the rollup already in hand (same taxonomy as AllGreen).
			funnel.DroppedRed = append(funnel.DroppedRed, funnelItem(pr, github.FailingChecks(pr.Checks)))
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
			// parse): never approve, never queue — this is the Staging fall-through
			// (eligible, but matched no Rule; an unparseable eligible title lands here
			// too, an accepted wart). Itemized so the user can drain it with a rule.
			funnel.Staging = append(funnel.Staging, funnelItem(pr, 0))
			continue
		}
		// An Approve Rule matched, no Review Rule gated it, and the title is not
		// breaking (else reasons would carry breaking_change) — the PR is on the
		// would-auto-approve path. Screens gate exactly here (the pre_approve
		// moment, ADR 0021): the level-triggered verdict consult never blocks
		// (ADR 0022) — any hold diverts to the queue (divert, never drop), any
		// missing verdict dispatches an async run and parks the PR in the
		// Screening segment this cycle; only an all-proceed fold falls through
		// to approve. With no screener configured this is a straight
		// fall-through, bit-for-bit pre-screening behavior.
		if e.screener != nil {
			disp := e.screener.Consult(ctx, hook.PRContext{
				Point:   hook.PreApprove,
				Number:  pr.Number,
				Title:   pr.Title,
				Author:  pr.Author,
				URL:     pr.URL,
				HeadSHA: pr.HeadSHA,
			})
			if len(disp.Holds) > 0 {
				// A screen held: divert to Needs-Human-Review carrying EVERY
				// holding screen — screen:<name> reasons (disjoint from rule
				// reasons by construction: screens only run where no rule
				// gated) plus the prose screen_holds the human reads.
				e.logger.Info("cycle: PR held by screen, routed to needs-human-review queue",
					"pr", pr.Number,
					"screens", screenNames(disp.Holds),
				)
				queue = append(queue, queueItemForHolds(pr, disp.Holds))
				continue
			}
			if len(disp.Pending) > 0 {
				// At least one screen has no verdict for this head yet: the PR
				// parks in Screening (a run was dispatched; the verdict acts on
				// the next pass — approval latency ≤ one cycle).
				funnel.Screening = append(funnel.Screening, screeningItem(pr, disp.Pending))
				continue
			}
		}
		ok, err := e.approve(ctx, pr, approveRuleName)
		if err != nil {
			e.logger.Warn("cycle: approve PR failed, skipping (retry next cycle)", "pr", pr.Number, "error", err)
			// A failed approval is a transient error retried next cycle: the PR is not
			// in the dedup set and reached no terminal stage, so it is the one case that
			// sits outside this cycle's partition (it reappears next cycle).
			continue
		}
		if ok {
			// Approved this cycle: the PR is now a dedup member, so it joins the
			// STANDING Approved-by-tm3k segment (keeping the partition whole) AND the
			// narrower ApprovedThisCycle count (the heartbeat's this-cycle pulse).
			approved++
			funnel.ApprovedByTm3k++
			funnel.ApprovedThisCycle++
		}
	}

	// NeedsHumanReview is a count-only segment of the partition: the queue is
	// itemized via /queue (station 4 reuses it), so the funnel keeps only its size.
	funnel.NeedsHumanReview = len(queue)

	e.logger.Info("cycle: complete",
		"candidates", len(candidates),
		"approved", approved,
		"queued", len(queue),
		"dropped", dropped,
		"staging", len(funnel.Staging),
	)
	e.recordCycle(now, "ok", approved, dropped, queue, funnel)
}

// screeningItem projects a would-auto-approve candidate into its Screening
// FunnelItem, naming the screens still awaiting a verdict for its head (the
// station's "why hasn't #N gone through?" signal).
func screeningItem(pr github.PR, pending []hook.ScreenInstance) FunnelItem {
	it := funnelItem(pr, 0)
	it.PendingScreens = make([]string, 0, len(pending))
	for _, p := range pending {
		it.PendingScreens = append(it.PendingScreens, p.Spec.Name)
	}
	return it
}

// queueItemForHolds projects a screen-held candidate into its queue entry:
// screen:<name> reasons plus the {screen, reason} prose, one pair per holding
// screen (every holding screen is carried — the reasons-list doctrine).
func queueItemForHolds(pr github.PR, holds []hook.HoldDetail) QueueItem {
	reasons := make([]string, 0, len(holds))
	screenHolds := make([]ScreenHold, 0, len(holds))
	for _, h := range holds {
		reasons = append(reasons, reasonScreenPrefix+h.Screen)
		screenHolds = append(screenHolds, ScreenHold{Screen: h.Screen, Reason: h.Reason})
	}
	return QueueItem{
		Number:       pr.Number,
		Title:        pr.Title,
		Author:       pr.Author,
		URL:          pr.URL,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
		Reasons:      reasons,
		ScreenHolds:  screenHolds,
	}
}

// screenNames lists the holding screens' names for the cycle log line.
func screenNames(holds []hook.HoldDetail) []string {
	names := make([]string, 0, len(holds))
	for _, h := range holds {
		names = append(names, h.Screen)
	}
	return names
}

// funnelItem projects a candidate PR into a terminal-bucket FunnelItem,
// carrying the count of non-passing checks (meaningful only on dropped_red; 0
// elsewhere).
func funnelItem(pr github.PR, failingChecks int) FunnelItem {
	return FunnelItem{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        pr.Author,
		URL:           pr.URL,
		FailingChecks: failingChecks,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		ChangedFiles:  pr.ChangedFiles,
	}
}

// reviewDecisionApproved is gh's reviewDecision value for a PR an approving
// review has already cleared.
const reviewDecisionApproved = "APPROVED"

// approvedElsewhere reports whether GitHub already considers the PR APPROVED.
// It is consulted only AFTER the dedup-set check, so a true result here means
// the approval is NOT tm3k's own (the PR is absent from approvals.jsonl): it was
// approved elsewhere (a teammate, a human, or another tm3k instance). Per ADR
// 0013 such a PR is left alone — a soft dedup — so tm3k never re-approves it and
// records nothing, keeping saved-switches analytics honest across instances.
func approvedElsewhere(pr github.PR) bool {
	return pr.ReviewDecision == reviewDecisionApproved
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
// replaces the live queue AND funnel snapshots with the freshly-recomputed ones,
// swapped together at cycle end (one lock). A failed fetch passes a nil queue and
// the zero Funnel: nothing was evaluated, so both are cleared (current truth is
// "unknown"; we approve and queue nothing, and the funnel shows no stale
// buckets). queue_count in the status reflects len(queue).
func (e *Engine) recordCycle(at time.Time, outcome string, approved, dropped int, queue []QueueItem, funnel Funnel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := at
	e.queue = queue
	e.funnel = funnel
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
