package hook

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ScreenInstance pairs a Screen's declarative Spec with the species
// implementation realizing it — the unit the screener consults and
// dispatches. Nothing instantiates species in this slice: production wiring
// passes none until the harness adapters land (ADR 0023); tests fake the
// Screen interface.
type ScreenInstance struct {
	Spec   Spec
	Screen Screen
}

// HoldDetail is one holding screen on a diverted PR: the screen's
// user-facing Name (queue reasons render screen:<name>) and the verdict's
// prose reason (the screen_holds field). Name, not ID — the human reads it.
type HoldDetail struct {
	Screen string
	Reason string
}

// Disposition is the level-triggered screening read for one PR at one head:
// which enabled screens are still awaiting a verdict and which hold. Both
// facts are reported — the caller's branch precedence decides (any hold
// diverts; else any pending parks the PR in Screening; else all proceed and
// the gated action may fire). Empty on both counts means proceed.
type Disposition struct {
	// Pending are the enabled screens with no verdict for the key — no row,
	// or an error latest row (a failed attempt is no verdict; the consult
	// re-dispatches it). A missing verdict is never proceed (ADR 0021).
	Pending []ScreenInstance
	// Holds carries EVERY screen whose latest row is a hold, in config order
	// (the reasons-list doctrine, ADR 0022).
	Holds []HoldDetail
}

// FoldVerdicts is the pure conjunctive fold of ADR 0022 — the screening
// sibling of AllGreen: it only judges, over the configured screens and a
// lookup of each screen's latest stored row for the PR-head under consult.
// Disabled screens never gate: they need no verdict and any stale hold of
// theirs is ignored.
func FoldVerdicts(screens []ScreenInstance, latest func(screenID string) (VerdictRecord, bool)) Disposition {
	var d Disposition
	for _, inst := range screens {
		if !inst.Spec.Enabled {
			continue
		}
		rec, ok := latest(inst.Spec.ID)
		if !ok || rec.Outcome == Errored {
			d.Pending = append(d.Pending, inst)
			continue
		}
		if rec.Outcome == Hold {
			d.Holds = append(d.Holds, HoldDetail{Screen: inst.Spec.Name, Reason: rec.Reason})
		}
	}
	return d
}

// screenPoolSize bounds how many screen runs execute concurrently. Hardcoded
// per ADR 0022 — the per-screen Timeout is the only tunable; no config
// surface until a need shows.
const screenPoolSize = 4

// Screener is the engine's one screening dependency: the level-triggered
// consult over the verdict store plus the async run dispatcher behind it
// (ADR 0022). Consult never blocks — a missing verdict dispatches a bounded
// background run and reports the screen as pending; the recorded verdict
// acts on the next cycle pass. With zero screens every consult is an instant
// proceed, bit-for-bit today's behavior.
type Screener struct {
	store   *VerdictStore
	screens []ScreenInstance
	logger  *slog.Logger

	// sem is the run pool: dispatch goroutines block HERE (off-cycle), never
	// the consult, so at most screenPoolSize runs execute at once.
	sem chan struct{}

	mu sync.Mutex
	// inflight dedups dispatches per key: the level-triggered consult re-sees
	// a missing verdict every cycle while a run executes, and must not stack a
	// second run for the same (screen, number, head).
	inflight map[VerdictKey]bool
}

// NewScreener constructs a Screener over the verdict store and the configured
// screens. The store is required; the screens may be empty (the no-hooks
// case — every consult proceeds and nothing ever dispatches).
func NewScreener(store *VerdictStore, screens ...ScreenInstance) *Screener {
	return &Screener{
		store:    store,
		screens:  screens,
		logger:   slog.Default(),
		sem:      make(chan struct{}, screenPoolSize),
		inflight: map[VerdictKey]bool{},
	}
}

// Consult is the per-candidate, level-triggered screening read (the
// armed/disarm idiom — no transition memory): it folds the stored verdicts
// for the PR's current head and, when no screen holds, dispatches an async
// run for every pending screen (in-flight dedup makes the re-dispatch each
// cycle harmless). It NEVER waits on a run — the disposition reflects only
// what the store already knows. A holding disposition dispatches nothing:
// the PR is bound for a human, so further paid runs would be waste.
func (s *Screener) Consult(ctx context.Context, pr PRContext) Disposition {
	disp := FoldVerdicts(s.screens, func(screenID string) (VerdictRecord, bool) {
		return s.store.Latest(screenID, pr.Number, pr.HeadSHA)
	})
	if len(disp.Holds) == 0 {
		for _, inst := range disp.Pending {
			s.dispatch(ctx, inst, pr)
		}
	}
	return disp
}

// dispatch starts one bounded background run for (screen, PR-head) unless one
// is already in flight. It returns immediately: the goroutine waits for a
// pool slot, runs the screen under its per-screen timeout
// (Spec.TimeoutOrDefault — the only tunable, ADR 0022), and records exactly
// one row — the verdict, or an error row for a failed attempt (crash,
// timeout, no extractable outcome; never a fabricated verdict in either
// direction, ADR 0023). A failed persist is logged and dropped: the store
// still misses, so the next cycle's consult re-dispatches (self-healing by
// level-trigger).
func (s *Screener) dispatch(ctx context.Context, inst ScreenInstance, pr PRContext) {
	key := VerdictKey{ScreenID: inst.Spec.ID, Number: pr.Number, Head: pr.HeadSHA}
	s.mu.Lock()
	if s.inflight[key] {
		s.mu.Unlock()
		return
	}
	s.inflight[key] = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
		}()
		select {
		case s.sem <- struct{}{}: // pool slot; queueing happens here, off-cycle
		case <-ctx.Done():
			// Shutdown while waiting for a slot: the run never started, no row
			// is recorded — the next boot's consult re-dispatches on the miss
			// (in-flight runs lost to a crash have the same story, ADR 0022).
			return
		}
		defer func() { <-s.sem }()

		runCtx, cancel := context.WithTimeout(ctx, inst.Spec.TimeoutOrDefault())
		defer cancel()
		verdict, err := inst.Screen.Screen(runCtx, pr)

		rec := VerdictRecord{ScreenID: inst.Spec.ID, Number: pr.Number, Head: pr.HeadSHA, At: time.Now()}
		switch {
		case err != nil:
			rec.Outcome, rec.Reason = Errored, err.Error()
		case verdict.Outcome != Proceed && verdict.Outcome != Hold:
			// No affirmative parsed verdict is a failed attempt, never proceed
			// and never hold — the screen species contract (ADR 0023).
			rec.Outcome, rec.Reason = Errored, fmt.Sprintf("no extractable verdict (outcome %q)", verdict.Outcome)
		default:
			rec.Outcome, rec.Reason = verdict.Outcome, verdict.Reason
		}
		if rec.Outcome == Errored {
			s.logger.Warn("screen: run failed, recording error attempt",
				"screen", inst.Spec.Name, "pr", pr.Number, "head", pr.HeadSHA, "reason", rec.Reason)
		}
		if err := s.store.Append(rec); err != nil {
			s.logger.Warn("screen: persist verdict failed; the next cycle re-dispatches",
				"screen", inst.Spec.Name, "pr", pr.Number, "head", pr.HeadSHA, "error", err)
		}
	}()
}
