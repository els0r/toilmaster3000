package hook

import (
	"context"
	"log/slog"
	"time"
)

// NotifierInstance pairs a Notifier's declarative Spec and post-point with the
// species implementation realizing it — the unit the runner fires. Production
// wiring builds one per configured Notifier (main's buildNotifierRunner);
// tests fake the Notifier interface.
type NotifierInstance struct {
	Spec     Spec
	Point    Point
	Notifier Notifier
}

// notifierPoolSize bounds how many notifier runs execute concurrently — an AI
// review is a minutes-long harness run, so the side effects queue off-cycle
// exactly like screen runs. Hardcoded (the screener's pool precedent, ADR
// 0022); the per-hook Timeout stays the only tunable.
const notifierPoolSize = 4

// NotifierRunner is the engine's one notification dependency: the fired-ledger
// consult plus the async side-effect dispatcher behind it (ADR 0021). Fire
// never blocks and returns nothing — a Notifier can never block, divert, or
// reorder an engine action. The events it serves are level-triggered (the
// queue is rebuilt every cycle, re-presenting each entry's queue_entered
// moment), so the persisted ledger — not event topology — is what makes a
// Notifier fire once per PR EVER: the row is appended AT DISPATCH, and a
// failed side effect is a logged miss, never a retry (at-most-once; for
// outward side effects a logged miss beats a double-post).
type NotifierRunner struct {
	ledger    *FiredLedger
	notifiers []NotifierInstance
	logger    *slog.Logger

	// sem is the run pool: dispatched goroutines block HERE (off-cycle), never
	// Fire itself, so at most notifierPoolSize side effects execute at once.
	sem chan struct{}
}

// NewNotifierRunner constructs a NotifierRunner over the fired-ledger and the
// configured Notifiers. The ledger is required; the notifiers may be empty
// (every Fire is then a no-op).
func NewNotifierRunner(ledger *FiredLedger, notifiers ...NotifierInstance) *NotifierRunner {
	return &NotifierRunner{
		ledger:    ledger,
		notifiers: notifiers,
		logger:    slog.Default(),
		sem:       make(chan struct{}, notifierPoolSize),
	}
}

// Fire announces one completed fact: for every enabled Notifier attached to
// pr.Point whose (hook, PR) key has never fired, it appends the ledger row —
// AT DISPATCH, before the side effect runs — and starts the side effect in a
// bounded background goroutine. It returns immediately in every case.
//
// The failure contract (ADR 0021), case by case:
//   - already-fired key: nothing happens — at-most-once, across cycles,
//     restarts, and fix-up pushes alike;
//   - ledger append fails: the side effect is NOT run (a row must exist
//     before any outward effect, or a restart could double-post) — logged;
//     the level-triggered event re-presents the fire next cycle;
//   - the side effect fails: logged miss, never retried — the row already
//     stands, so no later cycle re-fires it;
//   - and in no case does the caller learn or care: a Notifier can never
//     block, divert, or reorder an engine action.
func (r *NotifierRunner) Fire(ctx context.Context, pr PRContext) {
	for _, inst := range r.notifiers {
		if !inst.Spec.Enabled || inst.Point != pr.Point {
			continue
		}
		marked, err := r.ledger.Mark(FireRecord{HookID: inst.Spec.ID, Number: pr.Number, Point: pr.Point, At: time.Now()})
		if err != nil {
			r.logger.Warn("notifier: recording fire failed; not dispatching (retry next cycle)",
				"notifier", inst.Spec.Name, "pr", pr.Number, "point", pr.Point, "error", err)
			continue
		}
		if !marked {
			continue // fired before — ever means ever
		}
		r.dispatch(ctx, inst, pr)
	}
}

// dispatch starts one bounded background run of the side effect. The ledger
// row is already appended — from here on the fire is spent: whatever happens
// to the run is logged and never retried.
func (r *NotifierRunner) dispatch(ctx context.Context, inst NotifierInstance, pr PRContext) {
	go func() {
		select {
		case r.sem <- struct{}{}: // pool slot; queueing happens here, off-cycle
		case <-ctx.Done():
			// Shutdown while waiting for a slot: the fire is already recorded,
			// so this is a logged miss — at-most-once means no retry on boot.
			r.logger.Warn("notifier: shutdown before run started; logged miss",
				"notifier", inst.Spec.Name, "pr", pr.Number, "point", pr.Point)
			return
		}
		defer func() { <-r.sem }()

		runCtx, cancel := context.WithTimeout(ctx, inst.Spec.TimeoutOrDefault())
		defer cancel()
		if err := inst.Notifier.Notify(runCtx, pr); err != nil {
			r.logger.Warn("notifier: run failed; logged miss, never retried",
				"notifier", inst.Spec.Name, "pr", pr.Number, "point", pr.Point, "error", err)
			return
		}
		r.logger.Info("notifier: fired",
			"notifier", inst.Spec.Name, "pr", pr.Number, "point", pr.Point)
	}()
}
