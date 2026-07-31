package engine

import (
	"context"
	"time"

	"github.com/els0r/toilmaster3000/internal/github"
)

// mergeArmedReady is the merge step at the tail of the cycle (ADR 0016): for
// each Ready PR that is Armed and MERGEABLE, land it exactly as the org's
// gh land would and record the success in the merge ledger. It runs ONLY over
// a fresh outbound snapshot — a failed outbound fetch returned before this, so
// the robot never merges on stale data. The stage already guarantees green +
// APPROVED (that is what Ready means); Armed is the human consent (read live,
// AFTER the level-triggered reconciliation, so a just-disarmed PR never
// merges); MERGEABLE is the final gh-land precondition — CONFLICTING and
// UNKNOWN block the merge but never move the PR out of Ready.
func (e *Engine) mergeArmedReady(ctx context.Context, ob Outbound) {
	armedSet := e.armed.Set()
	for _, it := range ob.Ready {
		if !armedSet[it.Number] {
			continue // Withheld: no consent, no merge — ever.
		}
		if it.Mergeable != github.MergeableMergeable {
			e.logger.Info("cycle: armed ready PR not mergeable, merge blocked",
				"pr", it.Number,
				"mergeable", it.Mergeable,
			)
			continue
		}
		e.mergeOne(ctx, it)
	}
}

// mergeOne lands one armed, green, approved, mergeable PR: one gh pr view for
// the LIVE title/body/reviews (the sanctioned per-merge call — description
// edits made after arming are what lands), the pure gh-land commit-message
// construction, then the squash merge with one immediate retry (gh-land
// parity). The merge call and the ledger append run under the engine lock —
// the same locked mutation discipline as approve(). The ledger is appended
// ONLY on success; a failed merge is logged and the still-armed PR retries
// next cycle.
func (e *Engine) mergeOne(ctx context.Context, it OutboundItem) {
	info, err := e.client.MergeInfo(ctx, it.Number)
	if err != nil {
		e.logger.Warn("cycle: merge info fetch failed, skipping merge (retry next cycle)",
			"pr", it.Number,
			"error", err,
		)
		return
	}
	subject, body := github.CommitMessage(info)

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.client.Merge(ctx, it.Number, subject, body); err != nil {
		e.logger.Warn("cycle: merge failed, retrying once immediately", "pr", it.Number, "error", err)
		if err := e.client.Merge(ctx, it.Number, subject, body); err != nil {
			e.logger.Warn("cycle: merge retry failed, skipping (retry next cycle)", "pr", it.Number, "error", err)
			return
		}
	}

	rec := Merge{
		Number:     it.Number,
		Title:      info.Title,
		URL:        it.URL,
		MergedAt:   time.Now(),
		ApprovedBy: github.ApprovedBy(info.Reviews),
	}
	if err := e.appendMergeRecord(rec); err != nil {
		// The PR IS merged on GitHub but the ledger line was lost — unlike a
		// failed approval this cannot be retried (the PR has left the pull), so
		// log loudly and keep the in-memory record: the UI stays honest for this
		// session even though the durable ledger is short one line.
		e.logger.Error("cycle: merged PR could not be recorded to the merge ledger",
			"pr", it.Number,
			"error", err,
		)
	}
	e.merges = append([]Merge{rec}, e.merges...) // newest-first
	e.logger.Info("merged PR",
		"pr", it.Number,
		"subject", subject,
		"approved_by", rec.ApprovedBy,
		"url", it.URL,
	)
}

// appendMergeRecord appends one merge as a JSON line to merges.jsonl, creating
// the .state directory and file if needed. Callers must hold e.mu.
func (e *Engine) appendMergeRecord(rec Merge) error {
	return appendJSONL(e.mergesPath, rec)
}
