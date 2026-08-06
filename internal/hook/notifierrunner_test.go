package hook_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// recordingNotifier is the fake Notifier species of this slice: it records
// every PRContext it receives, optionally blocks until released, and returns
// its scripted error — the hook.Notifier seam the runner tests drive.
type recordingNotifier struct {
	err error
	// release, when non-nil, gates the run: Notify blocks until the channel is
	// closed or the run context dies — how a test holds a run "in flight".
	release chan struct{}

	mu    sync.Mutex
	calls []hook.PRContext
}

func (n *recordingNotifier) Notify(ctx context.Context, pr hook.PRContext) error {
	n.mu.Lock()
	n.calls = append(n.calls, pr)
	n.mu.Unlock()
	if n.release != nil {
		select {
		case <-n.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return n.err
}

// callCount returns how many Notify calls have happened so far.
func (n *recordingNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

// callContexts returns a copy of the received contexts.
func (n *recordingNotifier) callContexts() []hook.PRContext {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]hook.PRContext, len(n.calls))
	copy(out, n.calls)
	return out
}

// notifierSpec builds an enabled notifier spec (distinct ID vs Name, so tests
// prove the ledger keys on ID while logs carry Name).
func notifierSpec(id, name string) hook.Spec {
	return hook.Spec{ID: id, Name: name, Harness: "claude", Prompt: "p", Enabled: true}
}

// tempFires builds a FiredLedger on a temp path.
func tempFires(t *testing.T) *hook.FiredLedger {
	t.Helper()
	ledger, err := hook.NewFiredLedger(filepath.Join(t.TempDir(), "hookfires.jsonl"))
	require.NoError(t, err)
	return ledger
}

// queueEnteredCtx is the queue-entry context the runner tests share.
func queueEnteredCtx(number int) hook.PRContext {
	return hook.PRContext{
		Point:   hook.QueueEntered,
		Number:  number,
		Title:   "docs: gate me",
		Author:  "ann",
		URL:     "u",
		HeadSHA: "head-1",
		Reasons: []string{"docs gate"},
	}
}

// awaitCalls blocks until the notifier has seen n calls.
func awaitCalls(t *testing.T, n *recordingNotifier, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return n.callCount() == want },
		10*time.Second, 5*time.Millisecond, "the dispatched run reaches the notifier")
}

// NR1 (tracer): Fire dispatches an enabled Notifier attached to the context's
// point — the side effect receives the full PRContext — and the ledger row is
// recorded AT DISPATCH under the hook's ID, so the key reads fired even while
// the run is still executing (at-most-once, ADR 0021).
func TestFireDispatchesAndRecordsAtDispatch(t *testing.T) {
	n := &recordingNotifier{release: make(chan struct{})}
	ledger := tempFires(t)
	runner := hook.NewNotifierRunner(ledger,
		hook.NotifierInstance{Spec: notifierSpec("id-ra", "review assist"), Point: hook.QueueEntered, Notifier: n})

	runner.Fire(context.Background(), queueEnteredCtx(7))

	require.True(t, ledger.Fired("id-ra", 7), "the row lands at dispatch, before the side effect completes")
	awaitCalls(t, n, 1)
	close(n.release)
	require.Equal(t, []hook.PRContext{queueEnteredCtx(7)}, n.callContexts(),
		"the notifier receives the point's full context")
}

// NR2: once per PR EVER. The events are level-triggered (the queue re-presents
// its entries every cycle), so the ledger is the only thing standing between
// one announcement and one per cycle: a re-fire in the same process, a re-fire
// after a restart (fresh runner, same ledger file), and a re-fire after a
// fix-up push (new head, same PR) must all dispatch nothing.
func TestFireIsOncePerPREver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hookfires.jsonl")
	ledger, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	n := &recordingNotifier{}
	inst := hook.NotifierInstance{Spec: notifierSpec("id-ra", "review assist"), Point: hook.QueueEntered, Notifier: n}
	runner := hook.NewNotifierRunner(ledger, inst)

	runner.Fire(context.Background(), queueEnteredCtx(7))
	awaitCalls(t, n, 1)

	// The next cycle re-presents the same queue entry.
	runner.Fire(context.Background(), queueEnteredCtx(7))
	// A fix-up push: same PR, new head — a Notifier is per-PR, never per-head
	// (re-running review-assist on the fix-up push would nitpick the fixes).
	pushed := queueEnteredCtx(7)
	pushed.HeadSHA = "head-2"
	runner.Fire(context.Background(), pushed)

	// A restart: fresh runner and ledger instance over the SAME file.
	reloaded, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	hook.NewNotifierRunner(reloaded, inst).Fire(context.Background(), queueEnteredCtx(7))

	require.Never(t, func() bool { return n.callCount() > 1 }, 250*time.Millisecond, 50*time.Millisecond,
		"one announcement ever: no cycle, push, or restart re-fires the key")

	// A different PR is a fresh key: the notifier is not dead, just done with #7.
	runner.Fire(context.Background(), queueEnteredCtx(8))
	awaitCalls(t, n, 2)
}

// NR3: a Notifier fires only at its own point — attachment is topology, never
// a filter: a queue_entered notifier sees nothing of a screen_held fire and
// vice versa, and an unfired point leaves the ledger untouched (no row is
// burned for an event that never happened).
func TestFireRespectsPointAttachment(t *testing.T) {
	qn := &recordingNotifier{}
	sn := &recordingNotifier{}
	ledger := tempFires(t)
	runner := hook.NewNotifierRunner(ledger,
		hook.NotifierInstance{Spec: notifierSpec("id-q", "review assist"), Point: hook.QueueEntered, Notifier: qn},
		hook.NotifierInstance{Spec: notifierSpec("id-s", "held alert"), Point: hook.ScreenHeld, Notifier: sn})

	held := queueEnteredCtx(7)
	held.Point = hook.ScreenHeld
	held.Reasons = []string{"screen:security"}
	runner.Fire(context.Background(), held)

	awaitCalls(t, sn, 1)
	require.Never(t, func() bool { return qn.callCount() > 0 }, 250*time.Millisecond, 50*time.Millisecond,
		"the queue_entered notifier never touches a screen-held fire")
	require.True(t, ledger.Fired("id-s", 7))
	require.False(t, ledger.Fired("id-q", 7), "no row is burned for a point that did not fire")
}

// NR4: a failing side effect is a logged miss, never a retry — the ledger row
// stands from dispatch, so the level-triggered re-fire of the next cycle
// dispatches nothing (at-most-once proven with a scripted failing fake,
// ADR 0021: for outward side effects a logged miss beats a double-post).
func TestFailingNotifierIsALoggedMissNeverRetried(t *testing.T) {
	n := &recordingNotifier{err: errors.New("slack exploded")}
	ledger := tempFires(t)
	runner := hook.NewNotifierRunner(ledger,
		hook.NotifierInstance{Spec: notifierSpec("id-ra", "review assist"), Point: hook.QueueEntered, Notifier: n})

	runner.Fire(context.Background(), queueEnteredCtx(7))
	awaitCalls(t, n, 1)
	require.True(t, ledger.Fired("id-ra", 7), "the row is present even though the run failed")

	runner.Fire(context.Background(), queueEnteredCtx(7))
	require.Never(t, func() bool { return n.callCount() > 1 }, 250*time.Millisecond, 50*time.Millisecond,
		"a failed notification is never re-dispatched")
}

// NR5: several Notifiers on one point compose — each fires independently, once
// per PR, under its own ledger key — and a disabled Notifier neither fires nor
// burns a ledger row (enabling it later may still announce PRs whose events
// re-present).
func TestMultipleNotifiersOnOnePointFireIndependently(t *testing.T) {
	goRev := &recordingNotifier{}
	tsRev := &recordingNotifier{}
	off := &recordingNotifier{}
	offSpec := notifierSpec("id-off", "disabled assist")
	offSpec.Enabled = false
	ledger := tempFires(t)
	runner := hook.NewNotifierRunner(ledger,
		hook.NotifierInstance{Spec: notifierSpec("id-go", "go review"), Point: hook.QueueEntered, Notifier: goRev},
		hook.NotifierInstance{Spec: notifierSpec("id-ts", "ts review"), Point: hook.QueueEntered, Notifier: tsRev},
		hook.NotifierInstance{Spec: offSpec, Point: hook.QueueEntered, Notifier: off})

	runner.Fire(context.Background(), queueEnteredCtx(7))
	awaitCalls(t, goRev, 1)
	awaitCalls(t, tsRev, 1)
	require.True(t, ledger.Fired("id-go", 7))
	require.True(t, ledger.Fired("id-ts", 7))

	runner.Fire(context.Background(), queueEnteredCtx(7))
	require.Never(t, func() bool { return goRev.callCount()+tsRev.callCount() > 2 }, 250*time.Millisecond, 50*time.Millisecond,
		"each notifier fired exactly once for the PR")
	require.Zero(t, off.callCount(), "a disabled notifier never fires")
	require.False(t, ledger.Fired("id-off", 7), "and burns no ledger row")
}

// NR6: a hung side effect is bounded by the hook's Timeout (the only tunable):
// the run context dies at the bound, the miss is logged, and — the fire being
// spent at dispatch — nothing ever retries it.
func TestHungNotifierIsBoundedByItsTimeout(t *testing.T) {
	n := &recordingNotifier{release: make(chan struct{})} // never released: the run rides into its timeout
	defer close(n.release)
	spec := notifierSpec("id-ra", "review assist")
	spec.Timeout = hook.Duration(20 * time.Millisecond)
	ledger := tempFires(t)
	runner := hook.NewNotifierRunner(ledger,
		hook.NotifierInstance{Spec: spec, Point: hook.QueueEntered, Notifier: n})

	runner.Fire(context.Background(), queueEnteredCtx(7))
	awaitCalls(t, n, 1)

	// The timeout releases the pool slot: a fresh PR's fire still dispatches
	// (the hung run does not wedge the runner), while #7 stays fired-once.
	runner.Fire(context.Background(), queueEnteredCtx(8))
	awaitCalls(t, n, 2)
	require.True(t, ledger.Fired("id-ra", 7))
	require.True(t, ledger.Fired("id-ra", 8))
}
