package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// scriptedClaude returns a Claude adapter whose process seams are scripted:
// the gh diff fetch and the claude invocation are plain functions, so the
// real gh/claude CLIs never run in tests (the internal/github fake precedent).
func scriptedClaude(fetchDiff func(ctx context.Context, repo string, number int) (string, error),
	invoke func(ctx context.Context, model, prompt string) ([]byte, error)) *Claude {
	return &Claude{fetchDiff: fetchDiff, invoke: invoke}
}

func TestClaudeScreenFetchesComposesInvokesExtracts(t *testing.T) {
	req := composeReq()
	var gotRepo string
	var gotNumber int
	var gotModel, gotPrompt string
	c := scriptedClaude(
		func(_ context.Context, repo string, number int) (string, error) {
			gotRepo, gotNumber = repo, number
			return "+harmless line", nil
		},
		func(_ context.Context, model, prompt string) ([]byte, error) {
			gotModel, gotPrompt = model, prompt
			return envelope(t, "```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```"), nil
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

func TestClaudeScreenDiffFetchFailureIsAFailedAttempt(t *testing.T) {
	invoked := false
	c := scriptedClaude(
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

func TestClaudeScreenInvokeFailureIsAFailedAttempt(t *testing.T) {
	c := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string) ([]byte, error) {
			return nil, errors.New("claude -p: signal: killed")
		},
	)

	_, err := c.Screen(context.Background(), composeReq())
	require.ErrorContains(t, err, "claude -p")
}

// scriptedClaudeAgent returns a Claude adapter whose side-effect seam is
// scripted — the real claude CLI never runs in tests.
func scriptedClaudeAgent(act func(ctx context.Context, model, prompt string) ([]byte, error)) *Claude {
	return &Claude{act: act}
}

func TestClaudeActComposesInvokesAndReturnsTheTranscript(t *testing.T) {
	req := composeReq()
	var gotModel, gotPrompt string
	c := scriptedClaudeAgent(func(_ context.Context, model, prompt string) ([]byte, error) {
		gotModel, gotPrompt = model, prompt
		return envelope(t, "Posted a review comment on #42."), nil
	})

	transcript, err := c.Act(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Posted a review comment on #42.", transcript)
	require.Equal(t, "sonnet", gotModel)
	require.Equal(t, ComposeNotifyPrompt(req), gotPrompt,
		"the invoked prompt is exactly the notify composition — the ceiling rides every run")
}

func TestClaudeActFailuresSurfaceAsErrors(t *testing.T) {
	// A crashed CLI surfaces its error.
	c := scriptedClaudeAgent(func(context.Context, string, string) ([]byte, error) {
		return nil, errors.New("claude -p: signal: killed")
	})
	_, err := c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "claude -p")

	// An errored run envelope is an error, not a transcript.
	c = scriptedClaudeAgent(func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": ""}`), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "errored")

	// Non-envelope stdout (crash text) fails the decode.
	c = scriptedClaudeAgent(func(context.Context, string, string) ([]byte, error) {
		return []byte("panic: something broke"), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.Error(t, err)
}

func TestClaudeScreenUnparseableOutputIsAFailedAttempt(t *testing.T) {
	c := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string) ([]byte, error) {
			return []byte("I looked at it. CAN PROCEED!"), nil
		},
	)

	v, err := c.Screen(context.Background(), composeReq())

	require.Error(t, err)
	require.Equal(t, hook.Verdict{}, v, "an unparseable run must never fabricate a verdict")
}
