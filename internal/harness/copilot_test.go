package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// scriptedCopilot returns a Copilot adapter whose process seams are scripted:
// the gh diff fetch and the copilot invocation are plain functions, so the
// real gh/copilot CLIs never run in tests (the claude adapter's precedent).
func scriptedCopilot(fetchDiff func(ctx context.Context, repo string, number int) (string, error),
	invoke func(ctx context.Context, model, prompt string) ([]byte, error)) *Copilot {
	return &Copilot{fetchDiff: fetchDiff, invoke: invoke}
}

func TestCopilotScreenFetchesComposesInvokesExtracts(t *testing.T) {
	req := composeReq()
	var gotRepo string
	var gotNumber int
	var gotModel, gotPrompt string
	c := scriptedCopilot(
		func(_ context.Context, repo string, number int) (string, error) {
			gotRepo, gotNumber = repo, number
			return "+harmless line", nil
		},
		func(_ context.Context, model, prompt string) ([]byte, error) {
			gotModel, gotPrompt = model, prompt
			// Silent-mode stdout IS the response text — no envelope (ADR 0024).
			return []byte("Reviewed.\n\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n"), nil
		},
	)

	v, err := c.Screen(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, v)
	// The diff was fetched for the request's PR, and the invoked prompt is
	// exactly the composition over that fetched diff.
	require.Equal(t, "acme/widgets", gotRepo)
	require.Equal(t, 42, gotNumber)
	require.Equal(t, "sonnet", gotModel)
	require.Equal(t, ComposePrompt(req, "+harmless line"), gotPrompt)
}

func TestCopilotScreenDiffFetchFailureIsAFailedAttempt(t *testing.T) {
	invoked := false
	c := scriptedCopilot(
		func(context.Context, string, int) (string, error) {
			return "", errors.New("gh pr diff: exit status 1")
		},
		func(context.Context, string, string) ([]byte, error) {
			invoked = true
			return nil, nil
		},
	)

	_, err := c.Screen(context.Background(), composeReq())

	require.ErrorContains(t, err, "fetch diff")
	require.False(t, invoked, "a run without a diff must never invoke the harness")
}

func TestCopilotScreenInvokeFailureIsAFailedAttempt(t *testing.T) {
	c := scriptedCopilot(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string) ([]byte, error) {
			return nil, errors.New("copilot -p: signal: killed")
		},
	)

	_, err := c.Screen(context.Background(), composeReq())
	require.ErrorContains(t, err, "copilot -p")
}

func TestCopilotScreenUnparseableOutputIsAFailedAttempt(t *testing.T) {
	c := scriptedCopilot(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string) ([]byte, error) {
			return []byte("I looked at it. CAN PROCEED!"), nil
		},
	)

	v, err := c.Screen(context.Background(), composeReq())

	require.Error(t, err)
	require.Equal(t, hook.Verdict{}, v, "an unparseable run must never fabricate a verdict")
}

// scriptedCopilotAgent returns a Copilot adapter whose side-effect seam is
// scripted — the real copilot CLI never runs in tests.
func scriptedCopilotAgent(act func(ctx context.Context, model, prompt string) ([]byte, error)) *Copilot {
	return &Copilot{act: act}
}

func TestCopilotActComposesInvokesAndReturnsTheTranscript(t *testing.T) {
	req := composeReq()
	var gotModel, gotPrompt string
	c := scriptedCopilotAgent(func(_ context.Context, model, prompt string) ([]byte, error) {
		gotModel, gotPrompt = model, prompt
		return []byte("Posted a review comment on #42.\n"), nil
	})

	transcript, err := c.Act(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Posted a review comment on #42.\n", transcript)
	require.Equal(t, "sonnet", gotModel)
	require.Equal(t, ComposeNotifyPrompt(req), gotPrompt,
		"the invoked prompt is exactly the notify composition — the ceiling rides every run")
}

func TestCopilotActFailuresSurfaceAsErrors(t *testing.T) {
	// A crashed CLI surfaces its error.
	c := scriptedCopilotAgent(func(context.Context, string, string) ([]byte, error) {
		return nil, errors.New("copilot -p: signal: killed")
	})
	_, err := c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "copilot -p")

	// Blank silent-mode output is an error, not a transcript: the run said
	// nothing, so there is nothing to log as the agent's account of itself.
	c = scriptedCopilotAgent(func(context.Context, string, string) ([]byte, error) {
		return []byte("  \n"), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "empty copilot output")
}
