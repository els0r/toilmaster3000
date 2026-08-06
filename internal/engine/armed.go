package engine

import (
	"errors"
	"fmt"
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
	inSnapshot, changesRequested := e.outboundStanding(number)
	if !inSnapshot {
		return fmt.Errorf("%w: #%d", ErrNotInOutbound, number)
	}
	if changesRequested {
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

// outboundStanding looks the PR number up in the current outbound snapshot
// (locked read): whether it is present at all, and whether it sits in the
// Changes Requested stage — the two facts Arm validates against.
func (e *Engine) outboundStanding(number int) (inSnapshot, changesRequested bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, it := range e.outbound.ChangesRequested {
		if it.Number == number {
			return true, true
		}
	}
	// Every other stage is armable — In Discussion included: the hold is a
	// wait on a counterparty, not an objection, so consent stays available
	// (and stays standing) there (ADR 0019).
	for _, stage := range [][]OutboundItem{
		e.outbound.Draft,
		e.outbound.Red,
		e.outbound.Running,
		e.outbound.AwaitingApproval,
		e.outbound.InDiscussion,
		e.outbound.Ready,
	} {
		for _, it := range stage {
			if it.Number == number {
				return true, false
			}
		}
	}
	return false, false
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
// Both fold into one Retain (a single rewrite): keep = every authored number
// EXCEPT those with changes requested. Arms on every other stage are left
// alone — stickiness across pushes (arm-while-red) is the core use case, and
// In Discussion HOLDS the armed merge without ever withdrawing consent
// (ADR 0019: a nit thread is a conversation, not an objection).
func (e *Engine) reconcileArmed(ob Outbound) {
	keep := make(map[int]bool, ob.Outgoing)
	for _, stage := range [][]OutboundItem{
		ob.Draft,
		ob.Red,
		ob.Running,
		ob.AwaitingApproval,
		ob.InDiscussion,
		ob.Ready,
	} {
		for _, it := range stage {
			keep[it.Number] = true
		}
	}

	changesRequested := make(map[int]bool, len(ob.ChangesRequested))
	for _, it := range ob.ChangesRequested {
		changesRequested[it.Number] = true
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
