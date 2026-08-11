package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// stubNotifier is the fake Notifier species of the engine tests: it records
// every PRContext it receives, optionally blocks until released, and returns
// its scripted error — the hook.Notifier seam the cycle fires through.
type stubNotifier struct {
	err     error
	release chan struct{} // non-nil: Notify blocks until closed (or ctx dies)

	mu    sync.Mutex
	calls []hook.PRContext
}

func (n *stubNotifier) Notify(ctx context.Context, pr hook.PRContext) error {
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

func (n *stubNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func (n *stubNotifier) callContexts() []hook.PRContext {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]hook.PRContext, len(n.calls))
	copy(out, n.calls)
	return out
}

// notifierInstance builds an enabled Notifier instance on the given point.
func notifierInstance(id, name string, point hook.Point, n hook.Notifier) hook.NotifierInstance {
	return hook.NotifierInstance{
		Spec:     hook.Spec{ID: id, Name: name, Harness: "claude", Prompt: "p", Enabled: true},
		Point:    point,
		Notifier: n,
	}
}

// firesLedger builds a FiredLedger on the given path.
func firesLedger(t *testing.T, path string) *hook.FiredLedger {
	t.Helper()
	ledger, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	return ledger
}

// reviewRules seeds a rule store with the docs-gate Review Rule plus a
// permissive chore Approve Rule — enough to route, approve, and stage
// candidates in one cycle.
func reviewRules(t *testing.T, dir string) *rule.Store {
	t.Helper()
	rules, err := rule.NewStore(filepath.Join(dir, "rules.yaml"))
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"})
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)
	return rules
}

// notifierEngine builds an engine over the candidates with the shared rule
// set and the given NotifierRunner wired in.
func notifierEngine(t *testing.T, runner *hook.NotifierRunner, candidates ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	dir := t.TempDir()
	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, filepath.Join(dir, "approvals.jsonl"), tempMerges(t), reviewRules(t, dir), testArms(t), nil)
	require.NoError(t, err)
	eng.SetNotifierRunner(runner)
	return eng, fake
}

// awaitNotified blocks until the notifier has seen want calls.
func awaitNotified(t *testing.T, n *stubNotifier, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return n.callCount() == want },
		10*time.Second, 5*time.Millisecond, "the fired notifier receives its run")
}

// neverMoreCalls asserts the notifier's call count stays at want.
func neverMoreCalls(t *testing.T, n *stubNotifier, want int, msg string) {
	t.Helper()
	require.Never(t, func() bool { return n.callCount() > want }, 250*time.Millisecond, 50*time.Millisecond, msg)
}

// EN1 (tracer): a PR the rules route to Needs-Human-Review fires each
// queue_entered Notifier exactly once, EVER — the queue re-presents the entry
// every cycle and a restart rebuilds everything, but the persisted fired-
// ledger holds the line (AC 1). The context carries the point, the PR, and
// the queue reasons.
// EN1b: the cycle carries the PR's changed-file paths and true file count into
// the hook payload (ADR 0026) — the scope axis rides the batched inbound fetch,
// so a scoped Notifier can select itself without any per-PR call of its own.
// Both are needed: the paths to match, the count to detect gh's 100-file cap.
func TestQueueEnteredCarriesTheChangedFilePathsToTheHook(t *testing.T) {
	n := &stubNotifier{}
	pr := github.PR{
		Number: 13, Title: "docs: gate me", Author: "ann", URL: "u13", Checks: green(), HeadSHA: "head-1",
		ChangedFiles: 2, Files: []string{"docs/adr/0026-notifier-scope.md", "internal/hook/scope.go"},
	}
	eng, _ := notifierEngine(t, hook.NewNotifierRunner(
		firesLedger(t, filepath.Join(t.TempDir(), "hookfires.jsonl")),
		notifierInstance("id-ra", "review assist", hook.QueueEntered, n)), pr)

	eng.RunCycleOnce(context.Background())

	awaitNotified(t, n, 1)
	got := n.callContexts()[0]
	require.Equal(t, []string{"docs/adr/0026-notifier-scope.md", "internal/hook/scope.go"}, got.Files,
		"the changed-file paths of the batched fetch reach the hook payload")
	require.Equal(t, 2, got.ChangedFiles, "beside the true count, so truncation stays detectable")
}

// EN1c: a scoped Notifier selects itself inside a real cycle. Both directions
// ride on the payload the engine populates: the Go reviewer runs on the queued
// PR carrying Go files and stays silent on the docs-only one, keeping that
// PR's once-per-PR fire unspent (ADR 0026 decision 3) rather than spending it
// on a review of nothing.
func TestScopedNotifierSelectsItselfPerQueuedPR(t *testing.T) {
	n := &stubNotifier{}
	ledger := firesLedger(t, filepath.Join(t.TempDir(), "hookfires.jsonl"))
	inst := notifierInstance("id-go", "go review assist", hook.QueueEntered, n)
	inst.Scope = hook.NewScope([]string{"*.go"})
	docsOnly := github.PR{
		Number: 14, Title: "docs: gate me", Author: "ann", URL: "u14", Checks: green(), HeadSHA: "h14",
		ChangedFiles: 1, Files: []string{"README.md"},
	}
	withGo := github.PR{
		Number: 15, Title: "docs: gate me too", Author: "bob", URL: "u15", Checks: green(), HeadSHA: "h15",
		ChangedFiles: 2, Files: []string{"README.md", "internal/hook/scope.go"},
	}
	eng, _ := notifierEngine(t, hook.NewNotifierRunner(ledger, inst), docsOnly, withGo)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 2, "both PRs are queued; only the Notifier selects")

	awaitNotified(t, n, 1)
	require.Equal(t, 15, n.callContexts()[0].Number, "only the PR carrying Go code is reviewed")
	require.True(t, ledger.Fired("id-go", 15))
	neverMoreCalls(t, n, 1, "a Go reviewer never comments on a docs-only PR")
	require.False(t, ledger.Fired("id-go", 14), "its fire stays unspent for the day that PR grows Go code")
}

func TestQueueEnteredFiresOnceEverAcrossCyclesAndRestart(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "hookfires.jsonl")
	n := &stubNotifier{}
	inst := notifierInstance("id-ra", "review assist", hook.QueueEntered, n)
	pr := github.PR{Number: 12, Title: "docs: gate me", Author: "ann", URL: "u12", Checks: green(), HeadSHA: "head-1"}
	eng, _ := notifierEngine(t, hook.NewNotifierRunner(firesLedger(t, ledgerPath), inst), pr)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "the docs PR is queued by the Review Rule")

	awaitNotified(t, n, 1)
	got := n.callContexts()[0]
	require.Equal(t, hook.QueueEntered, got.Point)
	require.Equal(t, 12, got.Number)
	require.Equal(t, "docs: gate me", got.Title)
	require.Equal(t, "ann", got.Author)
	require.Equal(t, "u12", got.URL)
	require.Equal(t, "head-1", got.HeadSHA)
	require.Equal(t, []string{"docs gate"}, got.Reasons, "the point extra: why the PR sits in the queue")
	require.False(t, got.Manual, "manual is a post_approve fact; zero-valued here")

	// The next cycle re-presents the same queue entry — no second announcement.
	eng.RunCycleOnce(context.Background())
	neverMoreCalls(t, n, 1, "the level-triggered queue re-entry never re-fires")

	// A restart: fresh engine, fresh runner, SAME ledger file.
	eng2, _ := notifierEngine(t, hook.NewNotifierRunner(firesLedger(t, ledgerPath), inst), pr)
	eng2.RunCycleOnce(context.Background())
	require.Len(t, eng2.Queue(), 1, "the PR still sits in the rebuilt queue")
	neverMoreCalls(t, n, 1, "a restart between cycles does not re-fire")
}

// EN2: a Screen's hold diverts through screen_held, NEVER queue_entered — the
// two events partition queue entries by topology, so a review-assist attached
// only to queue_entered never touches a screen-held PR (AC 2; the double-pass
// is killed by topology, not a skip-flag, ADR 0021). The 3-strikes synthetic
// hold flows through screen_held like any hold (ADR 0022). The screen_held
// context carries the screen:<name> reasons.
func TestScreenHeldFiresScreenHeldNeverQueueEntered(t *testing.T) {
	dir := t.TempDir()
	store, err := hook.NewVerdictStore(filepath.Join(dir, "verdicts.jsonl"))
	require.NoError(t, err)
	// #31 carries a stored hold; #32 has struck out (three error rows), so its
	// hold is the synthesized screen-unavailable one.
	require.NoError(t, store.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 31, Head: "h31", Outcome: hook.Hold, Reason: "touches auth code", At: time.Now()}))
	for range 3 {
		require.NoError(t, store.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 32, Head: "h32", Outcome: hook.Errored, Reason: "harness exploded", At: time.Now()}))
	}
	screens := []hook.ScreenInstance{{
		Spec:   hook.Spec{ID: "id-sec", Name: "security", Harness: "claude", Prompt: "p", Enabled: true},
		Screen: newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, false),
	}}

	assist := &stubNotifier{}
	held := &stubNotifier{}
	runner := hook.NewNotifierRunner(firesLedger(t, filepath.Join(dir, "hookfires.jsonl")),
		notifierInstance("id-ra", "review assist", hook.QueueEntered, assist),
		notifierInstance("id-ha", "held alert", hook.ScreenHeld, held))

	candidates := []github.PR{
		{Number: 31, Title: "chore: held", Author: "mal", URL: "u31", Checks: green(), HeadSHA: "h31"},
		{Number: 32, Title: "chore: struck out", Author: "bob", URL: "u32", Checks: green(), HeadSHA: "h32"},
	}
	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, filepath.Join(dir, "approvals.jsonl"), tempMerges(t), reviewRules(t, dir), testArms(t), hook.NewScreener(store, screens...))
	require.NoError(t, err)
	eng.SetNotifierRunner(runner)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 2, "both holds divert to the queue")
	require.Empty(t, fake.ApprovedCalls())

	require.Eventually(t, func() bool { return held.callCount() == 2 },
		10*time.Second, 5*time.Millisecond, "each screen-held PR fires screen_held once")
	byNumber := map[int]hook.PRContext{}
	for _, c := range held.callContexts() {
		byNumber[c.Number] = c
	}
	require.Equal(t, hook.ScreenHeld, byNumber[31].Point)
	require.Equal(t, []string{"screen:security"}, byNumber[31].Reasons, "the point extra: the holding screens")
	require.Equal(t, hook.ScreenHeld, byNumber[32].Point)
	require.Equal(t, []string{"screen:security"}, byNumber[32].Reasons, "the synthetic 3-strikes hold flows through screen_held too")

	neverMoreCalls(t, assist, 0, "a review-assist on queue_entered never touches a screen-held PR")
}

// EN3a: post_approve announces a COMPLETED approval only (the post-point
// discipline: announce facts, ADR 0021): a failed approve() fires nothing and
// burns no ledger row — the fact never happened — and the retried, successful
// approval of the next cycle fires exactly once, manual flag false (AC 3).
func TestPostApproveFiresOnAutoApprovalOnlyAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	ledger := firesLedger(t, filepath.Join(dir, "hookfires.jsonl"))
	n := &stubNotifier{}
	runner := hook.NewNotifierRunner(ledger, notifierInstance("id-pa", "approval ping", hook.PostApprove, n))
	pr := github.PR{Number: 21, Title: "chore: bump dep", Author: "ann", URL: "u21", Checks: green(), HeadSHA: "h21"}
	eng, fake := notifierEngine(t, runner, pr)

	fake.FailApprove(21)
	eng.RunCycleOnce(context.Background())
	require.Len(t, fake.ApprovedCalls(), 1, "the approval was attempted and failed")
	neverMoreCalls(t, n, 0, "a failed approval is no fact: nothing fires")
	require.False(t, ledger.Fired("id-pa", 21), "and no ledger row is burned")

	fake.HealApprove(21)
	eng.RunCycleOnce(context.Background())
	awaitNotified(t, n, 1)
	got := n.callContexts()[0]
	require.Equal(t, hook.PostApprove, got.Point)
	require.Equal(t, 21, got.Number)
	require.Equal(t, "h21", got.HeadSHA)
	require.False(t, got.Manual, "an auto-approval carries manual=false")

	eng.RunCycleOnce(context.Background())
	neverMoreCalls(t, n, 1, "the standing dedup member never re-announces")
}

// EN3b: the manual-override approve fires post_approve too — auto and manual
// alike announce through one event, distinguished by the manual flag in the
// hook context (ADR 0021: "the context carries the manual flag"), with the
// overridden queue reasons as the point extra.
func TestPostApproveFiresOnManualOverrideWithManualFlag(t *testing.T) {
	dir := t.TempDir()
	n := &stubNotifier{}
	runner := hook.NewNotifierRunner(firesLedger(t, filepath.Join(dir, "hookfires.jsonl")),
		notifierInstance("id-pa", "approval ping", hook.PostApprove, n))
	pr := github.PR{Number: 12, Title: "docs: gate me", Author: "ann", URL: "u12", Checks: green(), HeadSHA: "h12"}
	eng, fake := notifierEngine(t, runner, pr)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "the docs PR is queued by the Review Rule")
	neverMoreCalls(t, n, 0, "queueing is not approving: post_approve stays silent")

	require.NoError(t, eng.ApproveManually(context.Background(), 12))
	require.Equal(t, []int{12}, fake.ApprovedCalls())

	awaitNotified(t, n, 1)
	got := n.callContexts()[0]
	require.Equal(t, hook.PostApprove, got.Point)
	require.Equal(t, 12, got.Number)
	require.Equal(t, "docs: gate me", got.Title)
	require.True(t, got.Manual, "a manual override carries manual=true")
	require.Equal(t, []string{"docs gate"}, got.Reasons, "the point extra: the reasons the human overrode")
	require.Equal(t, "h12", got.HeadSHA,
		"the override path owes a head like every other point: an AI species records it, and a head-less row cannot say which commit was reviewed")

	// The next cycle sees the PR as a standing dedup member — no re-announce.
	eng.RunCycleOnce(context.Background())
	neverMoreCalls(t, n, 1, "one approval, one announcement")
}

// EN3c: the manual-override announcement is built from the queue item, which
// carries the PR's file COUNT but not its paths — the engine genuinely does not
// know a manual override's file list. That is unknown scope, and unknown scope
// FIRES (ADR 0026 decision 5): a scoped Notifier announces the override rather
// than silently declining every one of them. The count is carried precisely so
// the fold reads the absence as unknown, not as "no files matched".
func TestPostApproveOnManualOverrideFiresOnUnknownScope(t *testing.T) {
	dir := t.TempDir()
	n := &stubNotifier{}
	inst := notifierInstance("id-pa", "go approval ping", hook.PostApprove, n)
	inst.Scope = hook.NewScope([]string{"*.go"})
	runner := hook.NewNotifierRunner(firesLedger(t, filepath.Join(dir, "hookfires.jsonl")), inst)
	pr := github.PR{
		Number: 12, Title: "docs: gate me", Author: "ann", URL: "u12", Checks: green(), HeadSHA: "h12",
		ChangedFiles: 3, Files: []string{"README.md", "docs/a.md", "docs/b.md"},
	}
	eng, _ := notifierEngine(t, runner, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.ApproveManually(context.Background(), 12))

	awaitNotified(t, n, 1)
	got := n.callContexts()[0]
	require.True(t, got.Manual)
	require.Equal(t, 3, got.ChangedFiles, "the count rides so the absent path list reads as unknown, not as no-match")
}

// EN4: a Notifier can never block, divert, or reorder an engine action (AC 4):
// the engine's outcome for every PR — approved, queued, staged, and the funnel
// partition — is identical with an absent, present, failing, or slow (never-
// returning) notifier on every point. The slow case also proves the cycle
// never waits on a side effect: the test would time out if it did.
func TestEngineOutcomeIsIdenticalWhateverNotifiersDo(t *testing.T) {
	build := func(name string) *hook.NotifierRunner {
		switch name {
		case "absent":
			return nil
		case "healthy":
			return hook.NewNotifierRunner(tempFiresEngine(t),
				notifierInstance("id-q", "q", hook.QueueEntered, &stubNotifier{}),
				notifierInstance("id-p", "p", hook.PostApprove, &stubNotifier{}))
		case "failing":
			return hook.NewNotifierRunner(tempFiresEngine(t),
				notifierInstance("id-q", "q", hook.QueueEntered, &stubNotifier{err: errors.New("boom")}),
				notifierInstance("id-p", "p", hook.PostApprove, &stubNotifier{err: errors.New("boom")}))
		case "slow":
			blocked := &stubNotifier{release: make(chan struct{})} // never released within the test
			t.Cleanup(func() { close(blocked.release) })
			return hook.NewNotifierRunner(tempFiresEngine(t),
				notifierInstance("id-q", "q", hook.QueueEntered, blocked),
				notifierInstance("id-p", "p", hook.PostApprove, blocked))
		}
		panic("unknown variant " + name)
	}

	for _, variant := range []string{"absent", "healthy", "failing", "slow"} {
		t.Run(variant, func(t *testing.T) {
			candidates := []github.PR{
				{Number: 1, Title: "chore: approve me", Author: "a", URL: "u1", Checks: green(), HeadSHA: "h1"},
				{Number: 2, Title: "docs: queue me", Author: "b", URL: "u2", Checks: green(), HeadSHA: "h2"},
				{Number: 3, Title: "feat: stage me", Author: "c", URL: "u3", Checks: green(), HeadSHA: "h3"},
			}
			eng, fake := notifierEngine(t, build(variant), candidates...)

			eng.RunCycleOnce(context.Background())

			require.Equal(t, []int{1}, fake.ApprovedCalls(), "the chore PR approves identically")
			queue := eng.Queue()
			require.Len(t, queue, 1)
			require.Equal(t, 2, queue[0].Number, "the docs PR queues identically")
			f := eng.Funnel()
			require.Len(t, f.Staging, 1, "the feat PR stages identically")
			require.Equal(t, 1, f.ApprovedThisCycle)
			sum := len(f.DroppedRed) + len(f.DroppedDraft) + len(f.Staging) + len(f.Screening) +
				f.NeedsHumanReview + f.ApprovedByTm3k + len(f.ApprovedElsewhere)
			require.Equal(t, f.Incoming, sum, "the partition stays whole in every variant")
		})
	}
}

// EN5: a PR that enters the queue, leaves it, and re-enters still fires each
// notifier at most once EVER — the ledger, not event topology, enforces
// "ever" (the decision the release/re-queue path exists to prove).
func TestQueueReentryAfterReleaseDoesNotRefire(t *testing.T) {
	n := &stubNotifier{}
	runner := hook.NewNotifierRunner(tempFiresEngine(t),
		notifierInstance("id-ra", "review assist", hook.QueueEntered, n))
	docs := github.PR{Number: 12, Title: "docs: gate me", Author: "ann", URL: "u12", Checks: green(), HeadSHA: "h1"}
	eng, fake := notifierEngine(t, runner, docs)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1)
	awaitNotified(t, n, 1)

	// Released: a retitle stops the Review-Rule match, the PR leaves the queue.
	retitled := docs
	retitled.Title = "feat: no longer gated"
	fake.Candidates = []github.PR{retitled}
	eng.RunCycleOnce(context.Background())
	require.Empty(t, eng.Queue(), "the released PR left the queue")

	// Re-entered: the docs title is back, the rules route it again.
	fake.Candidates = []github.PR{docs}
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "the PR re-entered the queue")
	neverMoreCalls(t, n, 1, "re-entry is not a fresh fire: once per PR means ever")
}

// tempFiresEngine builds a FiredLedger on a fresh temp path.
func tempFiresEngine(t *testing.T) *hook.FiredLedger {
	t.Helper()
	return firesLedger(t, filepath.Join(t.TempDir(), "hookfires.jsonl"))
}
