package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// scriptedScreen is the fake Screen species of this slice (nothing real
// implements the kind interface yet — ADR 0023): it blocks until released,
// then returns its scripted verdict/error, and counts its calls so a test can
// prove in-flight dedup and per-head re-screening at the kind seam.
type scriptedScreen struct {
	verdict hook.Verdict
	err     error
	// release gates the run: the screen blocks until the channel is closed (or
	// the run context dies), so a test can hold a verdict "in flight" across
	// cycles deterministically. A nil release returns immediately.
	release chan struct{}
	calls   atomic.Int32
}

func newScriptedScreen(verdict hook.Verdict, err error, blocked bool) *scriptedScreen {
	s := &scriptedScreen{verdict: verdict, err: err}
	if blocked {
		s.release = make(chan struct{})
	}
	return s
}

func (s *scriptedScreen) Screen(ctx context.Context, _ hook.PRContext) (hook.Verdict, error) {
	s.calls.Add(1)
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return hook.Verdict{}, ctx.Err()
		}
	}
	return s.verdict, s.err
}

// callCount returns how many Screen() calls have happened so far.
func (s *scriptedScreen) callCount() int { return int(s.calls.Load()) }

// enabledSpec builds an enabled screen spec (distinct ID vs Name, so tests
// prove verdicts key on ID while user-facing output carries Name).
func enabledSpec(id, name string) hook.Spec {
	return hook.Spec{ID: id, Name: name, Harness: "claude", Prompt: "p", Enabled: true}
}

// screeningEngine builds an engine over the candidates with one permissive
// Approve Rule (any chore) and the given screens consulting the given verdict
// store — the funnelEngine of the screening slice. The store is built by the
// caller so tests can await the async verdict write (the store is the real
// seam between runner and cycle; runner internals stay untested).
func screeningEngine(t *testing.T, store *hook.VerdictStore, screens []hook.ScreenInstance, candidates ...forge.PR) (*engine.Engine, *forge.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	rules, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)

	fake := forge.NewFake(candidates...)
	eng, err := engine.New(fake, statePath, tempMerges(t), rules, testArms(t), hook.NewScreener(store, screens...))
	require.NoError(t, err)
	return eng, fake
}

func tempVerdicts(t *testing.T) *hook.VerdictStore {
	t.Helper()
	store, err := hook.NewVerdictStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	return store
}

// partitionSum recomputes the funnel partition from its segments — the
// invariant every screening path must keep whole (CONTEXT "Cycle Funnel").
func partitionSum(f engine.Funnel) int {
	return len(f.DroppedRed) + len(f.DroppedDraft) + len(f.Staging) + len(f.Screening) +
		f.NeedsHumanReview + f.ApprovedByTm3k + len(f.ApprovedElsewhere)
}

// S1 (tracer): a would-auto-approve PR whose enabled Screen has no verdict for
// its head lands in the Screening segment this cycle — never approved, never
// queued — with the pending screen named on the row. The cycle does not block
// on the in-flight run (the screen is held open across the whole assertion),
// and the partition keeps summing to Incoming with the new segment in place.
func TestScreeningParksWouldApproveWithNoVerdict(t *testing.T) {
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, true) // blocked: no verdict can land
	defer close(scr.release)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}

	pr := forge.PR{Number: 21, Title: "chore: bump dep", Author: "ann", URL: "u21", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, tempVerdicts(t), screens, pr)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "no verdict is never proceed: the PR is not approved")
	f := eng.Funnel()
	require.Len(t, f.Screening, 1, "the PR parks in the Screening segment")
	require.Equal(t, 21, f.Screening[0].Number)
	require.Equal(t, []string{"security"}, f.Screening[0].PendingScreens, "the row names its pending screens")
	require.Empty(t, eng.Queue(), "pending is not held: nothing routes to the queue")
	require.Equal(t, f.Incoming, partitionSum(f), "the partition sums to Incoming with Screening in place")
}

// awaitVerdict blocks until the async runner has recorded a row for the key —
// the store is the seam between runner and cycle, so waiting on it is exactly
// what the next cycle's level-triggered consult does (runner internals stay
// untested).
func awaitVerdict(t *testing.T, store *hook.VerdictStore, screenID string, number int, head string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := store.Latest(screenID, number, head)
		return ok
	}, 10*time.Second, 5*time.Millisecond, "the dispatched run records its row")
}

// S2: every enabled screen says proceed -> the approval fires on the NEXT
// cycle pass (latency <= one cycle), completely silent — the feed entry
// carries the matched rule exactly as before screens existed, and the
// partition moves the PR from Screening to Approved-by-tm3k.
func TestAllProceedApprovesOnNextPassSilently(t *testing.T) {
	sec := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, nil, false)
	lic := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed, Reason: "MIT only"}, nil, false)
	screens := []hook.ScreenInstance{
		{Spec: enabledSpec("id-sec", "security"), Screen: sec},
		{Spec: enabledSpec("id-lic", "license"), Screen: lic},
	}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 22, Title: "chore: bump dep", Author: "ann", URL: "u22", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	// Cycle 1: no verdicts yet — the PR parks in Screening and both runs dispatch.
	eng.RunCycleOnce(context.Background())
	require.Empty(t, fake.ApprovedCalls(), "the dispatching cycle only parks the PR")
	require.Len(t, eng.Funnel().Screening, 1)

	// The runs complete off-cycle; their rows land in the store.
	awaitVerdict(t, store, "id-sec", 22, "head-1")
	awaitVerdict(t, store, "id-lic", 22, "head-1")

	// Cycle 2: the level-triggered consult reads all-proceed and approves.
	eng.RunCycleOnce(context.Background())
	require.Equal(t, []int{22}, fake.ApprovedCalls(), "approval fires on the next pass")

	f := eng.Funnel()
	require.Empty(t, f.Screening, "the PR left Screening")
	require.Equal(t, 1, f.ApprovedByTm3k)
	require.Equal(t, 1, f.ApprovedThisCycle)
	require.Equal(t, f.Incoming, partitionSum(f), "the partition stays whole through the proceed path")

	// Silence: the feed entry is indistinguishable from a pre-screening
	// approval — attributed to the first matching rule, no screen annotation
	// anywhere (a proceed leaves no trace).
	feed := eng.Approvals()
	require.Len(t, feed, 1)
	require.NotEmpty(t, feed[0].MatchedRule, "attributed to the matching Approve Rule, as before screens")
	require.NotContains(t, feed[0].MatchedRule, "screen", "a proceed is silent: no feed annotation")
}

// S3: any hold diverts the PR to Needs-Human-Review — divert, never drop —
// carrying EVERY holding screen as a screen:<name> reason plus the
// {screen, reason} prose in screen_holds. The PR is never approved, and the
// partition stays whole (the PR counts in NeedsHumanReview, not Screening).
func TestHoldDivertsToQueueCarryingEveryHoldingScreen(t *testing.T) {
	sec := newScriptedScreen(hook.Verdict{Outcome: hook.Hold, Reason: "touches auth code"}, nil, false)
	lic := newScriptedScreen(hook.Verdict{Outcome: hook.Hold, Reason: "GPL dependency"}, nil, false)
	screens := []hook.ScreenInstance{
		{Spec: enabledSpec("id-sec", "security"), Screen: sec},
		{Spec: enabledSpec("id-lic", "license"), Screen: lic},
	}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 23, Title: "chore: sneaky", Author: "mal", URL: "u23", Checks: green(), HeadSHA: "head-1", Additions: 9, Deletions: 2, ChangedFiles: 3}
	eng, fake := screeningEngine(t, store, screens, pr)

	// Cycle 1 dispatches; the holds land off-cycle.
	eng.RunCycleOnce(context.Background())
	awaitVerdict(t, store, "id-sec", 23, "head-1")
	awaitVerdict(t, store, "id-lic", 23, "head-1")

	// Cycle 2 reads the holds and diverts.
	eng.RunCycleOnce(context.Background())
	require.Empty(t, fake.ApprovedCalls(), "a held PR is never auto-approved")

	queue := eng.Queue()
	require.Len(t, queue, 1, "divert, never drop: the held PR is queued for a human")
	item := queue[0]
	require.Equal(t, 23, item.Number)
	require.Equal(t, []string{"screen:security", "screen:license"}, item.Reasons,
		"every holding screen contributes a screen:<name> reason (Name, not Id)")
	require.Equal(t, []engine.ScreenHold{
		{Screen: "security", Reason: "touches auth code"},
		{Screen: "license", Reason: "GPL dependency"},
	}, item.ScreenHolds, "the prose rides screen_holds, one pair per holding screen")
	require.Equal(t, 3, item.ChangedFiles, "the queue entry carries the diff magnitude like any other")

	f := eng.Funnel()
	require.Empty(t, f.Screening, "held is not pending")
	require.Equal(t, 1, f.NeedsHumanReview)
	require.Equal(t, f.Incoming, partitionSum(f), "the partition stays whole through the hold path")
}

// S4: dispatch is level-triggered but deduped — while a run is in flight, the
// consult of every later cycle sees the same missing verdict and must NOT
// stack a second run for the same (screen, number, head). The PR just keeps
// sitting in Screening; the cycle never waits.
func TestInFlightRunIsNotDispatchedTwice(t *testing.T) {
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, true)
	defer close(scr.release)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 24, Title: "chore: slow screen", Author: "ann", URL: "u24", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	eng.RunCycleOnce(context.Background())
	// The dispatch is async: wait for the run to actually start before cycling
	// again, so the second consult provably races a live in-flight run.
	require.Eventually(t, func() bool { return scr.callCount() == 1 }, 10*time.Second, 5*time.Millisecond)

	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())
	require.Equal(t, 1, scr.callCount(), "in-flight dedup: later cycles re-see the miss but never stack a second run")
	require.Len(t, eng.Funnel().Screening, 1, "the PR keeps sitting in Screening while the run is in flight")
	require.Empty(t, fake.ApprovedCalls())
}

// S5: a run that overruns its per-screen Timeout is cancelled and recorded as
// an error row — which is NO verdict: the PR stays in Screening and the next
// cycle's level-triggered consult re-dispatches (transient blips self-heal;
// three strikes synthesize a hold — S10). Timeout is the only tunable.
func TestTimedOutRunRecordsErrorAndRedispatches(t *testing.T) {
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, true) // never released: every run rides into its timeout
	defer close(scr.release)
	spec := enabledSpec("id-sec", "security")
	spec.Timeout = hook.Duration(20 * time.Millisecond)
	screens := []hook.ScreenInstance{{Spec: spec, Screen: scr}}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 25, Title: "chore: hung harness", Author: "ann", URL: "u25", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	eng.RunCycleOnce(context.Background())
	awaitVerdict(t, store, "id-sec", 25, "head-1")
	rec, ok := store.Latest("id-sec", 25, "head-1")
	require.True(t, ok)
	require.Equal(t, hook.Errored, rec.Outcome, "a timed-out run is a recorded failed attempt")
	require.NotEmpty(t, rec.Reason, "the error row carries the failure as its reason")

	// Next cycle: an error row is no verdict — still pending, and the consult
	// re-dispatches a fresh run.
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Funnel().Screening, 1, "an error row keeps the PR visibly in Screening")
	require.Empty(t, fake.ApprovedCalls(), "an error row is never proceed")
	require.Eventually(t, func() bool { return scr.callCount() >= 2 }, 10*time.Second, 5*time.Millisecond,
		"the level-triggered consult re-dispatches after a failed attempt")
}

// S6: verdicts are per-head — a new push is a fresh key, so the store misses
// and the PR re-screens. This is also how a hold clears by push: the held PR
// leaves the queue and parks in Screening for its new head (no invalidation
// logic exists or is needed, ADR 0022).
func TestNewPushRescreens(t *testing.T) {
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Hold, Reason: "touches auth code"}, nil, false)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 26, Title: "chore: evolving", Author: "ann", URL: "u26", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	// Head-1 screens and holds.
	eng.RunCycleOnce(context.Background())
	awaitVerdict(t, store, "id-sec", 26, "head-1")
	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "head-1 is held")

	// The author pushes: the same PR now carries head-2. The hold's key no
	// longer matches — the PR re-screens from scratch.
	prPushed := pr
	prPushed.HeadSHA = "head-2"
	fake.Candidates = []forge.PR{prPushed}

	eng.RunCycleOnce(context.Background())
	f := eng.Funnel()
	require.Empty(t, eng.Queue(), "the stale hold does not follow the new head")
	require.Len(t, f.Screening, 1, "the new head re-screens")
	require.Equal(t, f.Incoming, partitionSum(f))
	require.Eventually(t, func() bool { return scr.callCount() >= 2 }, 10*time.Second, 5*time.Millisecond,
		"a fresh run dispatches for the new head")
}

// S7: an unchanged head reuses its stored verdict across a restart — the
// verdict store is the durable truth, so the rebuilt engine approves on its
// first cycle without a single new screen run.
func TestUnchangedHeadReusesVerdictAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	verdictsPath := filepath.Join(dir, "verdicts.jsonl")
	statePath := filepath.Join(dir, "approvals.jsonl")
	rules, err := rule.NewStore(filepath.Join(dir, "rules.yaml"))
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)

	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, nil, false)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}
	pr := forge.PR{Number: 27, Title: "chore: stable head", Author: "ann", URL: "u27", Checks: green(), HeadSHA: "head-1"}

	// First process life: dispatch, verdict lands, process "dies" before the
	// next cycle can act on it.
	store1, err := hook.NewVerdictStore(verdictsPath)
	require.NoError(t, err)
	eng1, err := engine.New(forge.NewFake(pr), statePath, tempMerges(t), rules, testArms(t), hook.NewScreener(store1, screens...))
	require.NoError(t, err)
	eng1.RunCycleOnce(context.Background())
	awaitVerdict(t, store1, "id-sec", 27, "head-1")
	require.Equal(t, 1, scr.callCount())

	// Restart: fresh engine, fresh store instance, SAME files.
	store2, err := hook.NewVerdictStore(verdictsPath)
	require.NoError(t, err)
	fake2 := forge.NewFake(pr)
	eng2, err := engine.New(fake2, statePath, tempMerges(t), rules, testArms(t), hook.NewScreener(store2, screens...))
	require.NoError(t, err)
	eng2.RunCycleOnce(context.Background())

	require.Equal(t, []int{27}, fake2.ApprovedCalls(), "the persisted proceed acts on the rebuilt engine's first cycle")
	require.Equal(t, 1, scr.callCount(), "the unchanged head reuses its verdict: no new run after restart")
}

// S8: the SEVEN terminal segments partition Incoming — the F5 partition test
// with Screening in the formula. One cycle carries a candidate for every
// segment at once (screening-pending, screen-held, rule-queued, staged,
// draft, red, approved-elsewhere, standing-approved) and the counts must
// reconcile exactly.
func TestFunnelPartitionSumsToIncomingWithScreening(t *testing.T) {
	dir := t.TempDir()
	rules, err := rule.NewStore(filepath.Join(dir, "rules.yaml"))
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"})
	require.NoError(t, err)

	// One screen, scripted per-PR via the store: #2 will carry a recorded
	// hold, #1 and #8 no verdict (pending). #1's run stays blocked so #1 sits
	// in Screening deterministically across both cycles.
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, true)
	defer close(scr.release)
	spec := enabledSpec("id-sec", "security")
	store := tempVerdicts(t)
	require.NoError(t, store.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 2, Head: "h2", Outcome: hook.Hold, Reason: "touches auth", At: time.Now()}))
	// #7 (approved on cycle 1, standing on cycle 2) proceeds by stored verdict.
	require.NoError(t, store.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 7, Head: "h7", Outcome: hook.Proceed, Reason: "clean", At: time.Now()}))

	redChecks := []forge.Check{{State: forge.CheckFail}}
	candidates := []forge.PR{
		{Number: 1, Title: "chore: screening", Author: "a", URL: "u1", Checks: green(), HeadSHA: "h1"},                                       // pending verdict -> Screening
		{Number: 2, Title: "chore: held", Author: "b", URL: "u2", Checks: green(), HeadSHA: "h2"},                                            // stored hold -> NeedsHumanReview
		{Number: 3, Title: "docs: gate me", Author: "c", URL: "u3", Checks: green(), HeadSHA: "h3"},                                          // review rule -> NeedsHumanReview
		{Number: 4, Title: "feat: no rule", Author: "d", URL: "u4", Checks: green(), HeadSHA: "h4"},                                          // no match -> Staging
		{Number: 5, Title: "chore: draft", Author: "e", URL: "u5", IsDraft: true, Checks: green(), HeadSHA: "h5"},                            // draft -> DroppedDraft
		{Number: 6, Title: "chore: red", Author: "f", URL: "u6", Checks: redChecks, HeadSHA: "h6"},                                           // red -> DroppedRed
		{Number: 7, Title: "chore: pass", Author: "g", URL: "u7", Checks: green(), HeadSHA: "h7"},                                            // proceed -> approved (standing on cycle 2)
		{Number: 8, Title: "chore: elsewhere", Author: "h", URL: "u8", Checks: green(), ReviewDecision: forge.ReviewApproved, HeadSHA: "h8"}, // approved elsewhere
	}
	fake := forge.NewFake(candidates...)
	eng, err := engine.New(fake, filepath.Join(dir, "approvals.jsonl"), tempMerges(t), rules, testArms(t), hook.NewScreener(store, hook.ScreenInstance{Spec: spec, Screen: scr}))
	require.NoError(t, err)

	// Cycle 1 approves #7 (stored proceed); cycle 2 shows it standing.
	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())

	f := eng.Funnel()
	require.Equal(t, 8, f.Incoming)
	require.Equal(t, f.Incoming, partitionSum(f), "the seven segments partition Incoming")
	require.Len(t, f.Screening, 1)
	require.Len(t, f.DroppedRed, 1)
	require.Len(t, f.DroppedDraft, 1)
	require.Len(t, f.Staging, 1)
	require.Len(t, f.ApprovedElsewhere, 1)
	require.Equal(t, 2, f.NeedsHumanReview, "one screen-held + one rule-routed")
	require.Equal(t, 1, f.ApprovedByTm3k, "#7 stands as a dedup member")
	require.Zero(t, f.ApprovedThisCycle)
}

// S10: the no-verdict path is bounded and ends at a human (ADR 0022 decision
// 5). After one or two failed attempts the PR stays visibly in Screening and
// the level-triggered consult re-dispatches next cycle; the THIRD error row
// for the key synthesizes a hold carrying the last error, and the PR flows to
// the queue like any hold — never silent limbo, never a walk-through. Once
// struck out, no further paid run is dispatched for the key.
func TestThreeErrorAttemptsSynthesizeScreenUnavailableHold(t *testing.T) {
	scr := newScriptedScreen(hook.Verdict{}, errors.New("harness exploded"), false)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}
	store := tempVerdicts(t)

	pr := forge.PR{Number: 28, Title: "chore: flaky harness", Author: "ann", URL: "u28", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	// Cycles 1-3: each consult still has no verdict (an error row is no
	// verdict), keeps the PR visibly in Screening, and re-dispatches — the
	// streak growing to N proves attempt N actually ran and recorded.
	for attempt := 1; attempt <= 3; attempt++ {
		eng.RunCycleOnce(context.Background())
		f := eng.Funnel()
		require.Len(t, f.Screening, 1, "after %d failed attempts the PR stays visibly in Screening", attempt-1)
		require.Empty(t, eng.Queue(), "short of the cap nothing routes to the queue")
		require.Equal(t, f.Incoming, partitionSum(f))
		require.Eventually(t, func() bool {
			return store.ErrorStreak("id-sec", 28, "head-1") == attempt
		}, 10*time.Second, 5*time.Millisecond, "attempt %d records its error row", attempt)
	}

	// Cycle 4: three error rows synthesize a hold carrying the last error —
	// the PR reaches the queue like any hold, and is never approved.
	eng.RunCycleOnce(context.Background())
	require.Empty(t, fake.ApprovedCalls(), "a screen-unavailable PR is never walked through")
	queue := eng.Queue()
	require.Len(t, queue, 1, "the third strike summons a human")
	require.Equal(t, []string{"screen:security"}, queue[0].Reasons)
	require.Equal(t, []engine.ScreenHold{{Screen: "security", Reason: "screen unavailable: harness exploded"}},
		queue[0].ScreenHolds, "the synthesized hold carries the last error as its prose")
	f := eng.Funnel()
	require.Empty(t, f.Screening, "struck out is held, not pending")
	require.Equal(t, 1, f.NeedsHumanReview)
	require.Equal(t, f.Incoming, partitionSum(f), "the partition stays whole through the synthesized-hold path")

	// A holding disposition dispatches nothing: the cap also stops the paid
	// runs (retry-forever was explicitly rejected, ADR 0022).
	require.Never(t, func() bool { return scr.callCount() > 3 }, 250*time.Millisecond, 50*time.Millisecond,
		"a struck-out key never dispatches another run")
	require.Equal(t, 3, store.ErrorStreak("id-sec", 28, "head-1"), "no fourth attempt is ever recorded")
}

// S11: the queue's manual-override Approve outranks a screen hold — exactly as
// it outranks the breaking-change Invariant (ADR 0022 decision 4). The held PR
// approves on GitHub through the same override path, the published snapshots
// mutate in place (ADR 0018: queue removal + funnel count move, atomically with
// the ledger append), and the partition keeps summing to Incoming. The next
// cycle keeps it approved: the stored hold row is outranked for good.
func TestManualOverrideOutranksScreenHold(t *testing.T) {
	store := tempVerdicts(t)
	require.NoError(t, store.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 29, Head: "head-1", Outcome: hook.Hold, Reason: "touches auth code", At: time.Now()}))
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Hold, Reason: "touches auth code"}, nil, false)
	screens := []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}

	pr := forge.PR{Number: 29, Title: "chore: contested", Author: "ann", URL: "u29", Checks: green(), HeadSHA: "head-1"}
	eng, fake := screeningEngine(t, store, screens, pr)

	eng.RunCycleOnce(context.Background())
	require.Len(t, eng.Queue(), 1, "the stored hold diverts the PR to the queue")

	require.NoError(t, eng.ApproveManually(context.Background(), 29))

	require.Equal(t, []int{29}, fake.ApprovedCalls(), "the human outranks the robot's hold")
	require.Empty(t, eng.Queue(), "ADR 0018: the queue snapshot mutates in place, no waiting for a rebuild")
	f := eng.Funnel()
	require.Zero(t, f.NeedsHumanReview, "the funnel count left needs-human-review in the same mutation")
	require.Equal(t, 1, f.ApprovedByTm3k, "and became an approved-by-tm3k member")
	require.Zero(t, f.ApprovedThisCycle, "a manual override is not the cycle's pulse")
	require.Equal(t, f.Incoming, partitionSum(f), "the in-place mutation keeps the partition whole")
	require.Zero(t, eng.Status().QueueCount, "the heartbeat's queue count follows the queue")

	feed := eng.Approvals()
	require.Len(t, feed, 1)
	require.Equal(t, engine.ManualApprovalPrefix+"screen:security", feed[0].MatchedRule,
		"the override self-documents the outranked screen hold")

	// Next cycle: the dedup set outranks the still-stored hold row — the PR
	// stays approved, never re-queues, and no screen ever runs for it.
	eng.RunCycleOnce(context.Background())
	f = eng.Funnel()
	require.Empty(t, eng.Queue())
	require.Equal(t, 1, f.ApprovedByTm3k)
	require.Equal(t, f.Incoming, partitionSum(f))
	require.Zero(t, scr.callCount(), "an approved PR never reaches a screen again")
}

// S12: disabling a screen (disable + restart) releases its holds on the next
// cycle (ADR 0022 decision 4): the fold only consults ENABLED screens, so the
// stored hold row simply stops participating — level-triggered, no cleanup
// pass, no state-file surgery. The formerly-held PR approves on the rebuilt
// engine's first cycle.
func TestDisabledScreenReleasesItsHoldsAfterRestart(t *testing.T) {
	verdictsPath := filepath.Join(t.TempDir(), "verdicts.jsonl")
	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Hold, Reason: "touches auth code"}, nil, false)
	pr := forge.PR{Number: 30, Title: "chore: misjudged", Author: "ann", URL: "u30", Checks: green(), HeadSHA: "head-1"}

	// Life 1: the enabled screen's stored hold diverts the PR to the queue.
	store1, err := hook.NewVerdictStore(verdictsPath)
	require.NoError(t, err)
	require.NoError(t, store1.Append(hook.VerdictRecord{ScreenID: "id-sec", Number: 30, Head: "head-1", Outcome: hook.Hold, Reason: "touches auth code", At: time.Now()}))
	eng1, fake1 := screeningEngine(t, store1, []hook.ScreenInstance{{Spec: enabledSpec("id-sec", "security"), Screen: scr}}, pr)
	eng1.RunCycleOnce(context.Background())
	require.Len(t, eng1.Queue(), 1, "enabled, the stored hold diverts")
	require.Empty(t, fake1.ApprovedCalls())

	// Restart with the screen DISABLED: same verdicts file, fresh store, the
	// spec now carries Enabled=false — the backing-out story, no state-file
	// surgery involved.
	store2, err := hook.NewVerdictStore(verdictsPath)
	require.NoError(t, err)
	off := enabledSpec("id-sec", "security")
	off.Enabled = false
	eng2, fake2 := screeningEngine(t, store2, []hook.ScreenInstance{{Spec: off, Screen: scr}}, pr)

	eng2.RunCycleOnce(context.Background())
	require.Equal(t, []int{30}, fake2.ApprovedCalls(), "the released PR approves on the next cycle")
	require.Empty(t, eng2.Queue(), "the disabled screen's hold stopped participating")
	f := eng2.Funnel()
	require.Empty(t, f.Screening, "a disabled screen is never pending either")
	require.Equal(t, 1, f.ApprovedByTm3k)
	require.Equal(t, f.Incoming, partitionSum(f), "the partition stays whole through the release")
	require.Zero(t, scr.callCount(), "a disabled screen never runs")
}

// S9: screens run ONLY on the would-auto-approve subset — after the gates,
// after rule matching, after the breaking-! invariant (the pre_approve
// moment, ADR 0021). Candidates that drop, stage, or route by rule never
// reach a screen: no run is ever dispatched for them.
func TestScreensRunOnlyOnWouldApproveSubset(t *testing.T) {
	dir := t.TempDir()
	rules, err := rule.NewStore(filepath.Join(dir, "rules.yaml"))
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "any chore", Class: "approve", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)
	_, err = rules.Create(rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"})
	require.NoError(t, err)

	scr := newScriptedScreen(hook.Verdict{Outcome: hook.Proceed}, nil, false)
	redChecks := []forge.Check{{State: forge.CheckFail}}
	candidates := []forge.PR{
		{Number: 1, Title: "docs: routed by rule", Author: "a", URL: "u1", Checks: green(), HeadSHA: "h1"},
		{Number: 2, Title: "chore!: breaking", Author: "b", URL: "u2", Checks: green(), HeadSHA: "h2"},
		{Number: 3, Title: "feat: staged", Author: "c", URL: "u3", Checks: green(), HeadSHA: "h3"},
		{Number: 4, Title: "chore: draft", Author: "d", URL: "u4", IsDraft: true, Checks: green(), HeadSHA: "h4"},
		{Number: 5, Title: "chore: red", Author: "e", URL: "u5", Checks: redChecks, HeadSHA: "h5"},
	}
	fake := forge.NewFake(candidates...)
	eng, err := engine.New(fake, filepath.Join(dir, "approvals.jsonl"), tempMerges(t), rules, testArms(t),
		hook.NewScreener(tempVerdicts(t), hook.ScreenInstance{Spec: enabledSpec("id-sec", "security"), Screen: scr}))
	require.NoError(t, err)

	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())

	require.Zero(t, scr.callCount(), "no gate-dropped, staged, rule-routed, or breaking PR ever reaches a screen")
	require.Empty(t, eng.Funnel().Screening, "nothing on the would-approve path: the Screening segment stays empty")
	require.Empty(t, fake.ApprovedCalls())
	// The breaking chore PR queued via the invariant, not via any screen.
	queue := eng.Queue()
	require.Len(t, queue, 2)
	for _, q := range queue {
		require.Empty(t, q.ScreenHolds, "rule reasons XOR screen holds: rule-routed entries carry no screen prose")
	}
}
