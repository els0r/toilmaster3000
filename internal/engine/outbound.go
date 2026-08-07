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

// Outbound IS the live outbound funnel partition (ADR 0025): every authored PR
// of the last completed cycle, keyed under EXACTLY one stage by
// ClassifyOutboundStage. The map is the snapshot — no wrapper struct, no stored
// count, so there is nothing for the partition to drift from. A stage with no
// PRs is simply an absent key, which reads as the nil slice.
//
// It mirrors the inbound Funnel's lifecycle — swapped under lock at cycle end,
// empty after restart until the first cycle, cleared on a failed outbound OR
// threads fetch (the robot never shows, and never merges on, stale authored
// data — ADR 0016, extended by ADR 0019).
//
// Partition invariant (CONTEXT "Outbound"): the stage lists partition the raw
// authored pull, and Outgoing folds over them, so the counts sum by
// construction in the literal sense. The merge step walks ONLY Ready: In
// Discussion never merging is the partition itself, not an extra merge-step
// clause (the Discussion gate, ADR 0019).
type Outbound map[github.OutboundStage][]OutboundItem

// Outgoing is the size of the raw authored pull, DERIVED by folding the
// partition rather than stored beside it (ADR 0025). Nothing decrements it, so
// no prune or mutation can make it disagree with the lists it counts.
func (ob Outbound) Outgoing() int {
	n := 0
	for _, items := range ob {
		n += len(items)
	}
	return n
}

// find locates one PR number in the partition, returning its item and the stage
// it sits in. It is the single lookup the engine resolves outbound numbers
// through (the Arm gate's stage, the Diff card's changed_files): it ranges the
// MAP, so it is complete over stages by construction — a stage nobody has
// thought of yet is searched like every other.
func (ob Outbound) find(number int) (OutboundItem, github.OutboundStage, bool) {
	for stage, items := range ob {
		for _, it := range items {
			if it.Number == number {
				return it, stage, true
			}
		}
	}
	return OutboundItem{}, "", false
}

// Outbound returns the live outbound funnel snapshot (locked read). It is
// recomputed each cycle, so this reflects the current truth as of the last
// completed cycle; it is empty after a restart until the first cycle, and a
// failed outbound fetch clears it. The map AND its slices are copied, so a
// caller cannot mutate the engine's snapshot — the read side is the deep copy
// precisely because the publish side (setOutbound) is not.
func (e *Engine) Outbound() Outbound {
	e.mu.Lock()
	defer e.mu.Unlock()
	ob := make(Outbound, len(e.outbound))
	for stage, items := range e.outbound {
		ob[stage] = append([]OutboundItem(nil), items...)
	}
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

	// Every authored PR is appended under the tag ClassifyOutboundStage returns,
	// so the partition IS the fold's codomain: no switch to fall through, no
	// default to swallow a PR, and the list lengths sum to Outgoing by
	// construction. A PR absent from the threads map carries no review
	// threads — the zero connection judges to zero unresolved.
	ob := Outbound{}
	for _, pr := range authored {
		unresolved := github.UnresolvedCount(threads[pr.Number])
		stage := github.ClassifyOutboundStage(pr, unresolved)
		ob[stage] = append(ob[stage], outboundItem(pr, unresolved))
	}

	// A fresh authored pull is the ONLY trigger for arm-lifecycle changes
	// (level-triggered disarm on Changes Requested, cleanup of PRs that left
	// the pull) — a failed fetch returned above, so consent is never withdrawn
	// on stale or missing data.
	e.reconcileArmed(ob)

	// The one ORDERED consumer of the stage list in Go: the per-stage counts are
	// logged in funnel order, keyed by the stage constants' own string values
	// (which is why the keys read exactly as they always have). Everything that
	// must be COMPLETE ranges the map instead; here an omission would be merely
	// cosmetic.
	stages := github.OutboundStages()
	args := make([]any, 0, 2+2*len(stages))
	args = append(args, "outgoing", ob.Outgoing())
	for _, stage := range stages {
		args = append(args, string(stage), len(ob[stage]))
	}
	e.logger.Info("cycle: outbound snapshot rebuilt", args...)
	e.setOutbound(ob)

	// The merge step runs LAST, over the same fresh snapshot: reconciliation
	// above already applied the level-triggered disarm, so consent read now is
	// current. It is handed ONLY Ready — the Discussion gate is the signature,
	// not a merge-step clause (ADR 0019). A failed outbound fetch returned
	// early — the robot never merges on stale data (ADR 0016).
	e.mergeArmedReady(ctx, ob[github.OutboundStageReady])
}

// setOutbound swaps the outbound snapshot under lock, publishing the map BY
// REFERENCE — deliberately, not by oversight. rebuildOutbound's local ob keeps
// aliasing the published map, which is the sharing ADR 0018 reasons about: the
// merge prune rebuilds the Ready slice rather than shifting it in place because
// mergeArmedReady is still ranging the header it was handed. Cloning here would
// demote that from load-bearing to belt-and-braces; the deep copy lives on the
// read accessor instead.
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
