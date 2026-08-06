package engine

import (
	"context"

	"github.com/els0r/toilmaster3000/internal/github"
)

// OutboundItem is one authored PR itemized in an outbound stage list. It is
// the engine's internal read-model — derived live each cycle, never persisted —
// carrying just enough to render a row plus the raw Mergeable signal (the wire
// layer derives the Ready conflict marker from it; the stage fold never reads
// it). The wire DTO is server.OutboundItem, mapped via outboundItemToBody (ADR
// 0002). Like FunnelItem it carries no json tags: the snapshot is a pure
// in-memory read-model.
type OutboundItem struct {
	Number       int
	Title        string
	Author       string
	URL          string
	Additions    int
	Deletions    int
	ChangedFiles int
	// Mergeable is gh's raw mergeability (MERGEABLE | CONFLICTING | UNKNOWN)
	// from the authored list call. A merge precondition, not a stage boundary:
	// a conflicted Ready item stays in Ready carrying it.
	Mergeable string
	// UnresolvedThreads is the PR's judged unresolved-review-thread count from
	// the cycle's threads call (github.UnresolvedCount over the raw
	// connection, ADR 0019) — the Discussion-gate predicate the stage fold
	// consumed, carried so the wire can render it on the row. It rides EVERY
	// item, whatever the stage: only the approved tail folds on it (precedence
	// names the primary blocker), but a draft with open nits still carries its
	// real count.
	UnresolvedThreads int
}

// Outbound is the live outbound funnel snapshot: every authored PR of the last
// completed cycle, folded into EXACTLY one stage list by ClassifyOutboundStage.
// It mirrors the inbound Funnel's lifecycle — swapped under lock at cycle end,
// the zero value after restart until the first cycle, cleared on a failed
// outbound OR threads fetch (the robot never shows, and never merges on,
// stale authored data — ADR 0016, extended by ADR 0019).
//
// Partition invariant (CONTEXT "Outbound"): the seven stage lists partition
// the raw authored pull, so
//
//	Outgoing = len(Draft) + len(Red) + len(Running) + len(ChangesRequested)
//	         + len(AwaitingApproval) + len(InDiscussion) + len(Ready)
//
// holds by construction — every authored PR is appended to exactly one list.
// The merge step walks ONLY Ready: In Discussion never merging is the
// partition itself, not an extra merge-step clause (the Discussion gate,
// ADR 0019).
type Outbound struct {
	Outgoing         int
	Draft            []OutboundItem
	Red              []OutboundItem
	Running          []OutboundItem
	ChangesRequested []OutboundItem
	AwaitingApproval []OutboundItem
	InDiscussion     []OutboundItem
	Ready            []OutboundItem
}

// Outbound returns the live outbound funnel snapshot (locked read). It is
// recomputed each cycle, so this reflects the current truth as of the last
// completed cycle; it is the zero value after a restart until the first cycle,
// and a failed outbound fetch clears it. The slices are copied so a caller
// cannot mutate the engine's snapshot.
func (e *Engine) Outbound() Outbound {
	e.mu.Lock()
	defer e.mu.Unlock()
	ob := e.outbound
	ob.Draft = append([]OutboundItem(nil), e.outbound.Draft...)
	ob.Red = append([]OutboundItem(nil), e.outbound.Red...)
	ob.Running = append([]OutboundItem(nil), e.outbound.Running...)
	ob.ChangesRequested = append([]OutboundItem(nil), e.outbound.ChangesRequested...)
	ob.AwaitingApproval = append([]OutboundItem(nil), e.outbound.AwaitingApproval...)
	ob.InDiscussion = append([]OutboundItem(nil), e.outbound.InDiscussion...)
	ob.Ready = append([]OutboundItem(nil), e.outbound.Ready...)
	return ob
}

// rebuildOutbound replaces the outbound snapshot from the cycle's two
// outbound-side batched calls: the authored-PR list and the unresolved-
// review-threads search (ADR 0019). It runs at the tail of EVERY cycle,
// independent of the inbound fetch: the pulls are disjoint by search and fail
// independently. Either call failing clears the snapshot to the zero value
// and returns before reconciliation and the merge step — current truth is
// "unknown", and the robot never acts (and never merges) on stale or guessed
// authored data (ADR 0016, extended to threads by ADR 0019).
func (e *Engine) rebuildOutbound(ctx context.Context) {
	authored, err := e.client.ListAuthored(ctx)
	if err != nil {
		e.logger.Warn("cycle: list authored PRs failed, clearing outbound snapshot", "error", err)
		e.setOutbound(Outbound{})
		return
	}

	// The threads call is skipped for an empty pull (nothing to gate — the
	// empty-feed precedent of the PR-State refresh); otherwise its result is
	// load-bearing for the partition, so a failure fails CLOSED: better a
	// blank funnel than an In-Discussion PR guessed into Ready and merged.
	threads := map[int]github.RawReviewThreads{}
	if len(authored) > 0 {
		threads, err = e.client.UnresolvedThreads(ctx)
		if err != nil {
			e.logger.Warn("cycle: unresolved-threads fetch failed, clearing outbound snapshot", "error", err)
			e.setOutbound(Outbound{})
			return
		}
	}

	// Every authored PR folds into EXACTLY one stage list (the pure
	// ClassifyOutboundStage is the partition), so the seven list lengths sum
	// to Outgoing by construction. A PR absent from the threads map carries no
	// review threads — the zero connection judges to zero unresolved.
	ob := Outbound{Outgoing: len(authored)}
	for _, pr := range authored {
		unresolved := github.UnresolvedCount(threads[pr.Number])
		item := outboundItem(pr, unresolved)
		switch github.ClassifyOutboundStage(pr, unresolved) {
		case github.OutboundStageDraft:
			ob.Draft = append(ob.Draft, item)
		case github.OutboundStageRed:
			ob.Red = append(ob.Red, item)
		case github.OutboundStageRunning:
			ob.Running = append(ob.Running, item)
		case github.OutboundStageChangesRequested:
			ob.ChangesRequested = append(ob.ChangesRequested, item)
		case github.OutboundStageAwaitingApproval:
			ob.AwaitingApproval = append(ob.AwaitingApproval, item)
		case github.OutboundStageInDiscussion:
			ob.InDiscussion = append(ob.InDiscussion, item)
		case github.OutboundStageReady:
			ob.Ready = append(ob.Ready, item)
		}
	}

	// A fresh authored pull is the ONLY trigger for arm-lifecycle changes
	// (level-triggered disarm on Changes Requested, cleanup of PRs that left
	// the pull) — a failed fetch returned above, so consent is never withdrawn
	// on stale or missing data.
	e.reconcileArmed(ob)

	e.logger.Info("cycle: outbound snapshot rebuilt",
		"outgoing", ob.Outgoing,
		"draft", len(ob.Draft),
		"red", len(ob.Red),
		"running", len(ob.Running),
		"changes_requested", len(ob.ChangesRequested),
		"awaiting_approval", len(ob.AwaitingApproval),
		"in_discussion", len(ob.InDiscussion),
		"ready", len(ob.Ready),
	)
	e.setOutbound(ob)

	// The merge step runs LAST, over the same fresh snapshot: reconciliation
	// above already applied the level-triggered disarm, so consent read now is
	// current. A failed outbound fetch returned early — the robot never merges
	// on stale data (ADR 0016).
	e.mergeArmedReady(ctx, ob)
}

// setOutbound swaps the outbound snapshot under lock.
func (e *Engine) setOutbound(ob Outbound) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outbound = ob
}

// outboundItem projects one authored PR plus its judged unresolved-thread
// count into its stage-list item.
func outboundItem(pr github.PR, unresolvedThreads int) OutboundItem {
	return OutboundItem{
		Number:            pr.Number,
		Title:             pr.Title,
		Author:            pr.Author,
		URL:               pr.URL,
		Additions:         pr.Additions,
		Deletions:         pr.Deletions,
		ChangedFiles:      pr.ChangedFiles,
		Mergeable:         pr.Mergeable,
		UnresolvedThreads: unresolvedThreads,
	}
}
