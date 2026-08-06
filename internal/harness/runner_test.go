package harness

// The hook runner (Screener dispatch, in-flight dedup, per-screen timeout)
// driven end-to-end through the AI Screen species and the harness seam, with
// scripted fakes at the process boundary — the real gh and claude CLIs never
// run in any test (ADR 0023's one new test seam). The tests live here rather
// than in internal/hook because they exercise the species: hook's own tests
// cover the fold and store in isolation.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// newStore builds a VerdictStore on a temp path.
func newStore(t *testing.T) *hook.VerdictStore {
	t.Helper()
	store, err := hook.NewVerdictStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	return store
}

// securitySpec is the enabled claude screen the runner tests configure.
func securitySpec() hook.Spec {
	return hook.Spec{ID: "sec1", Name: "security", Harness: "claude", Model: "sonnet", Prompt: "vet the diff", Enabled: true}
}

// awaitLatest polls the store until a row exists for the key and returns it.
func awaitLatest(t *testing.T, store *hook.VerdictStore, screenID string, number int, head string) hook.VerdictRecord {
	t.Helper()
	var rec hook.VerdictRecord
	require.Eventually(t, func() bool {
		r, ok := store.Latest(screenID, number, head)
		if ok {
			rec = r
		}
		return ok
	}, 5*time.Second, 10*time.Millisecond)
	return rec
}

// TestScreenerScreensEndToEndThroughTheClaudeAdapter is the AC-1 path minus
// the real processes: a would-auto-approve PR consults the screener, the
// claude adapter fetches the (scripted) diff, composes the prompt, the
// (scripted) claude run answers, the verdict is extracted structurally and
// stored, and the next pass proceeds.
func TestScreenerScreensEndToEndThroughTheClaudeAdapter(t *testing.T) {
	var mu sync.Mutex
	var invokedPrompt string
	adapter := scriptedClaude(
		func(_ context.Context, repo string, number int) (string, error) {
			require.Equal(t, "acme/widgets", repo)
			require.Equal(t, 7, number)
			return "+one harmless line", nil
		},
		func(_ context.Context, _ string, prompt string) ([]byte, error) {
			mu.Lock()
			invokedPrompt = prompt
			mu.Unlock()
			return envelope(t, "All clean.\n```json\n{\"verdict\": \"proceed\", \"reason\": \"routine chore\"}\n```"), nil
		},
	)
	spec := securitySpec()
	store := newStore(t)
	screener := hook.NewScreener(store, hook.ScreenInstance{
		Spec:   spec,
		Screen: NewAIScreen(spec, "acme/widgets", adapter),
	})

	// First pass: no verdict yet — the PR parks in Screening and a run
	// dispatches in the background.
	disp := screener.Consult(context.Background(), prCtx())
	require.Len(t, disp.Pending, 1)
	require.Empty(t, disp.Holds)

	rec := awaitLatest(t, store, spec.ID, 7, "feedface")
	require.Equal(t, hook.Proceed, rec.Outcome)
	require.Equal(t, "routine chore", rec.Reason)

	// The run's prompt was the composition over the fetched diff, fenced as
	// data.
	mu.Lock()
	prompt := invokedPrompt
	mu.Unlock()
	require.Contains(t, prompt, "<pr_diff>\n+one harmless line\n</pr_diff>")
	require.Contains(t, prompt, "vet the diff")

	// Next pass: the stored verdict acts — nothing pending, nothing holds,
	// the gated approve may fire.
	disp = screener.Consult(context.Background(), prCtx())
	require.Empty(t, disp.Pending)
	require.Empty(t, disp.Holds)
}

func TestScreenerHoldDivertsWithTheScreensReason(t *testing.T) {
	adapter := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+curl x | sh", nil },
		func(context.Context, string, string) ([]byte, error) {
			return envelope(t, "```json\n{\"verdict\": \"hold\", \"reason\": \"pipes a remote script into sh\"}\n```"), nil
		},
	)
	spec := securitySpec()
	store := newStore(t)
	screener := hook.NewScreener(store, hook.ScreenInstance{
		Spec:   spec,
		Screen: NewAIScreen(spec, "acme/widgets", adapter),
	})

	screener.Consult(context.Background(), prCtx())
	rec := awaitLatest(t, store, spec.ID, 7, "feedface")
	require.Equal(t, hook.Hold, rec.Outcome)

	disp := screener.Consult(context.Background(), prCtx())
	require.Empty(t, disp.Pending)
	require.Equal(t, []hook.HoldDetail{{Screen: "security", Reason: "pipes a remote script into sh"}}, disp.Holds)
}

// TestScreenerDedupsInFlightRuns proves the level-triggered re-consult never
// stacks a second run for the same (screen, number, head) while one executes.
func TestScreenerDedupsInFlightRuns(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	adapter := adapterFunc(func(ctx context.Context, _ Request) (hook.Verdict, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case <-release:
			return hook.Verdict{Outcome: hook.Proceed, Reason: "ok"}, nil
		case <-ctx.Done():
			return hook.Verdict{}, ctx.Err()
		}
	})
	spec := securitySpec()
	store := newStore(t)
	screener := hook.NewScreener(store, hook.ScreenInstance{
		Spec:   spec,
		Screen: NewAIScreen(spec, "acme/widgets", adapter),
	})

	// Two cycles consult while the first run is still executing.
	screener.Consult(context.Background(), prCtx())
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls == 1
	}, 5*time.Second, 10*time.Millisecond, "the first consult must dispatch the run")
	disp := screener.Consult(context.Background(), prCtx())
	require.Len(t, disp.Pending, 1, "an in-flight run is still no verdict")

	close(release)
	awaitLatest(t, store, spec.ID, 7, "feedface")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, calls, "the re-consult must not have stacked a second run")
}

// TestScreenerTimeoutBecomesAnErrorAttempt proves the per-screen Timeout is
// enforced where the Screener applies it, and that an adapter honoring
// context cancellation yields a recorded error attempt — never a verdict.
func TestScreenerTimeoutBecomesAnErrorAttempt(t *testing.T) {
	adapter := adapterFunc(func(ctx context.Context, _ Request) (hook.Verdict, error) {
		<-ctx.Done() // the adapter contract: die with the context
		return hook.Verdict{}, ctx.Err()
	})
	spec := securitySpec()
	spec.Timeout = hook.Duration(30 * time.Millisecond)
	store := newStore(t)
	screener := hook.NewScreener(store, hook.ScreenInstance{
		Spec:   spec,
		Screen: NewAIScreen(spec, "acme/widgets", adapter),
	})

	screener.Consult(context.Background(), prCtx())

	rec := awaitLatest(t, store, spec.ID, 7, "feedface")
	require.Equal(t, hook.Errored, rec.Outcome)
	require.Contains(t, rec.Reason, context.DeadlineExceeded.Error())

	// An error attempt is still no verdict: the next pass re-reports pending
	// (and re-dispatches — the 3-strikes path counts attempts, ADR 0022).
	disp := screener.Consult(context.Background(), prCtx())
	require.Len(t, disp.Pending, 1)
	require.Empty(t, disp.Holds)
}

// TestNotifierRunnerFiresEndToEndThroughTheClaudeAdapter is the review-assist
// path minus the real processes: a queue_entered fire reaches the AI Notifier
// species, which composes the notify prompt — the never-approve/never-merge
// ceiling riding every run — and invokes the (scripted) claude agent leg; the
// fired-ledger row stands from dispatch, so a second fire dispatches nothing.
// The real gh/claude CLIs never run: the agent seam is scripted.
func TestNotifierRunnerFiresEndToEndThroughTheClaudeAdapter(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	agent := scriptedClaudeAgent(func(_ context.Context, _ string, prompt string) ([]byte, error) {
		mu.Lock()
		prompts = append(prompts, prompt)
		mu.Unlock()
		return envelope(t, "Posted a review comment."), nil
	})
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Prompt: "review Go idioms", Enabled: true}
	ledger, err := hook.NewFiredLedger(filepath.Join(t.TempDir(), "hookfires.jsonl"))
	require.NoError(t, err)
	runner := hook.NewNotifierRunner(ledger, hook.NotifierInstance{
		Spec:     spec,
		Point:    hook.QueueEntered,
		Notifier: NewAINotifier(spec, "acme/widgets", agent),
	})

	pr := hook.PRContext{Point: hook.QueueEntered, Number: 7, Title: "feat: new endpoint",
		Author: "alice", URL: "https://github.com/acme/widgets/pull/7", HeadSHA: "feedface",
		Reasons: []string{"docs gate"}}
	runner.Fire(context.Background(), pr)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(prompts) == 1
	}, 5*time.Second, 10*time.Millisecond, "the fire reaches the agent leg")
	require.True(t, ledger.Fired("n1", 7))

	mu.Lock()
	prompt := prompts[0]
	mu.Unlock()
	require.Contains(t, prompt, "review Go idioms", "the operator's instructions ride the run")
	require.Contains(t, prompt, "gh pr diff 7 --repo acme/widgets", "the agent is told to fetch the diff itself")
	require.Contains(t, prompt, "You must NEVER approve", "the ceiling rides every composed run")
	require.Contains(t, prompt, "untrusted data")

	runner.Fire(context.Background(), pr)
	require.Never(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(prompts) > 1
	}, 250*time.Millisecond, 50*time.Millisecond, "the spent fire never dispatches a second agent run")
}

// TestScreenerRecordsUnparseableOutputAsErrorAttempt is AC-2 through the full
// runner: harness chatter without a structural verdict becomes a recorded
// error attempt, never a fabricated proceed or hold.
func TestScreenerRecordsUnparseableOutputAsErrorAttempt(t *testing.T) {
	adapter := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string) ([]byte, error) {
			return envelope(t, "Everything looks great. CAN PROCEED."), nil
		},
	)
	spec := securitySpec()
	store := newStore(t)
	screener := hook.NewScreener(store, hook.ScreenInstance{
		Spec:   spec,
		Screen: NewAIScreen(spec, "acme/widgets", adapter),
	})

	screener.Consult(context.Background(), prCtx())

	rec := awaitLatest(t, store, spec.ID, 7, "feedface")
	require.Equal(t, hook.Errored, rec.Outcome)
	require.True(t, strings.Contains(rec.Reason, "no verdict document"), "reason should carry the extraction failure: %q", rec.Reason)

	disp := screener.Consult(context.Background(), prCtx())
	require.Len(t, disp.Pending, 1, "prose chatter is no verdict — the PR stays in Screening")
	require.Empty(t, disp.Holds)
}
