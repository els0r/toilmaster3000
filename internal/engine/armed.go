package engine

import (
	"errors"
	"fmt"

	"github.com/els0r/toilmaster3000/internal/github"
)

// ErrNotInOutbound is returned by Arm when the given PR number is not in the
// current outbound snapshot (unknown, merged/closed, or no cycle has run yet).
// Consent is only ever given over a PR the operator can see.
var ErrNotInOutbound = errors.New("pr not in outbound snapshot")

// ErrChangesRequested is returned by Arm when the given PR sits in the
// Changes Requested stage: Armed ∧ Changes-Requested is an impossible state
// (CONTEXT "Outbound", ADR 0016) — an open objection always requires the
// feedback to be addressed and fresh consent after re-approval.
var ErrChangesRequested = errors.New("pr has changes requested; arming is rejected")

// Arm marks the PR as Armed — the per-PR consent that (in a later slice) is
// the only thing authorizing an outbound merge. The number is validated
// against the CURRENT outbound snapshot: absent → ErrNotInOutbound; in the
// Changes Requested stage → ErrChangesRequested (the arm endpoint's 4xx). The
// arm is persisted immediately, so it survives restarts and new pushes.
func (e *Engine) Arm(number int) error {
	stage, inSnapshot := e.outboundStage(number)
	if !inSnapshot {
		return fmt.Errorf("%w: #%d", ErrNotInOutbound, number)
	}
	if !armable(stage) {
		return fmt.Errorf("%w: #%d", ErrChangesRequested, number)
	}
	if err := e.armed.Arm(number); err != nil {
		return err
	}
	e.logger.Info("outbound consent: PR armed", "pr", number)
	return nil
}

// Disarm withdraws the arm (idempotent — disarming a Withheld PR is a quiet
// no-op) and persists the removal. Unlike Arm it needs no snapshot check:
// withdrawing consent is always safe, whatever the PR's stage or standing.
func (e *Engine) Disarm(number int) error {
	if err := e.armed.Disarm(number); err != nil {
		return err
	}
	e.logger.Info("outbound consent: PR disarmed", "pr", number)
	return nil
}

// ArmedSet returns a copy of the armed set keyed by PR number, for the wire
// layer to zip armed flags onto outbound rows — read live from the store (not
// baked into the snapshot), so a toggle reflects immediately, not next cycle.
func (e *Engine) ArmedSet() map[int]bool {
	return e.armed.Set()
}

// armable reports whether consent may stand over a PR in the given outbound
// stage. It is the ONE declaration of the "Arm anywhere except Changes
// Requested" doctrine (CONTEXT "Armed / Withheld", ADR 0016), read by both of
// its consumers: Arm's 4xx gate and reconcileArmed's keep loop.
//
// It is stated by EXCLUSION, and that phrasing is the point: a stage nobody has
// thought of yet is armable by default at both sites simultaneously, so adding
// a stage can never silently withdraw merge consent for the PRs in it. In
// Discussion is armable for the same reason it is worded this way — the hold is
// a wait on a counterparty, not an objection (ADR 0019).
//
// It lives in engine, not github: arming is tm3k's consent model, not a GitHub
// concept.
func armable(stage github.OutboundStage) bool {
	return stage != github.OutboundStageChangesRequested
}

// outboundStage returns the stage the PR number sits in within the current
// outbound snapshot (locked read), and whether it is in the snapshot at all —
// the two facts Arm validates against. It is a pure lookup: the consent rule
// itself lives in armable, not here.
func (e *Engine) outboundStage(number int) (github.OutboundStage, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, stage, ok := e.outbound.find(number)
	return stage, ok
}

// reconcileArmed applies the two arm-lifecycle rules to a FRESH authored pull
// (never a failed fetch — stale data must not withdraw consent):
//
//   - cleanup: an armed number absent from the pull left it as merged/closed;
//     its entry is removed.
//   - level-triggered disarm: an armed PR observed in the Changes Requested
//     stage is disarmed this cycle, keeping Armed ∧ Changes-Requested
//     impossible without any transition memory.
//
// Both fold into one Retain (a single rewrite): keep = every authored number in
// an armable stage. The loop ranges the PARTITION and asks armable, so it is
// complete over stages by construction — arms on every stage but Changes
// Requested are left alone, including stages that do not exist yet. Stickiness
// across pushes (arm-while-red) is the core use case, and In Discussion HOLDS
// the armed merge without ever withdrawing consent (ADR 0019: a nit thread is a
// conversation, not an objection).
func (e *Engine) reconcileArmed(ob Outbound) {
	keep := make(map[int]bool, ob.Outgoing())
	changesRequested := map[int]bool{}
	for stage, items := range ob {
		// The rule's unit is the STAGE, so it is asked once per stage: every PR
		// in an armable stage is kept, every PR in a non-armable one is a
		// level-triggered disarm (and is named as such in the log below).
		bucket := keep
		if !armable(stage) {
			bucket = changesRequested
		}
		for _, it := range items {
			bucket[it.Number] = true
		}
	}

	removed, err := e.armed.Retain(keep)
	if err != nil {
		e.logger.Warn("cycle: armed-set reconciliation failed to persist", "error", err)
		return
	}
	for _, n := range removed {
		if changesRequested[n] {
			e.logger.Info("cycle: armed PR disarmed, changes requested (level-triggered)", "pr", n)
		} else {
			e.logger.Info("cycle: armed entry cleaned up, PR left the pull", "pr", n)
		}
	}
}
