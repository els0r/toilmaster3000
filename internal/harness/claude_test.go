package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// scriptedClaude returns a Claude adapter whose process seams are scripted:
// the gh diff fetch and the claude invocation are plain functions, so the
// real gh/claude CLIs never run in tests (the internal/github fake precedent).
func scriptedClaude(fetchDiff func(ctx context.Context, repo string, number int) (string, error),
	invoke func(ctx context.Context, model, prompt, workDir string) ([]byte, error)) *Claude {
	return &Claude{fetchDiff: fetchDiff, invoke: invoke}
}

// The screen leg fetches, composes, invokes, and unwraps its CLI's envelope —
// and stops there. What comes back is the run's result text, verdict document
// and surrounding prose intact: extraction belongs to the species (ADR 0028),
// and the adapter handing over the whole text is what lets the species
// transcribe it before judging it.
func TestClaudeScreenFetchesComposesInvokesAndReturnsResultText(t *testing.T) {
	req := composeReq()
	var gotRepo string
	var gotNumber int
	var gotModel, gotPrompt string
	c := scriptedClaude(
		func(_ context.Context, repo string, number int) (string, error) {
			gotRepo, gotNumber = repo, number
			return "+harmless line", nil
		},
		func(_ context.Context, model, prompt, _ string) ([]byte, error) {
			gotModel, gotPrompt = model, prompt
			return envelope(t, "Looks routine.\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```"), nil
		},
	)

	result, err := c.Screen(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Looks routine.\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```", result,
		"the whole result text comes back, not a distillate of it")
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
		func(context.Context, string, string, string) ([]byte, error) {
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
		func(context.Context, string, string, string) ([]byte, error) {
			return nil, errors.New("claude -p: signal: killed")
		},
	)

	_, err := c.Screen(context.Background(), composeReq())
	require.ErrorContains(t, err, "claude -p")
}

// scriptedClaudeAgent returns a Claude adapter whose side-effect seam is
// scripted — the real claude CLI never runs in tests.
func scriptedClaudeAgent(act func(ctx context.Context, model, prompt, workDir string) ([]byte, error)) *Claude {
	return &Claude{act: act}
}

func TestClaudeActComposesInvokesAndReturnsTheTranscript(t *testing.T) {
	req := composeReq()
	var gotModel, gotPrompt string
	c := scriptedClaudeAgent(func(_ context.Context, model, prompt, _ string) ([]byte, error) {
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

// The Request's WorkDir is what anchors the harness process, and it reaches
// the process seam verbatim — that is the whole mechanism of ADR 0027. The
// screen leg gets no anchor: AIScreen never populates the field, so a screen
// run keeps the pre-existing unanchored behaviour bit-for-bit.
func TestClaudeCarriesWorkDirToTheProcessSeam(t *testing.T) {
	req := composeReq()
	req.WorkDir = "/srv/skills-worktree"

	var actWorkDir string
	agent := &Claude{act: func(_ context.Context, model, prompt, workDir string) ([]byte, error) {
		actWorkDir = workDir
		return envelope(t, "Posted a review comment on #42."), nil
	}}
	_, err := agent.Act(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "/srv/skills-worktree", actWorkDir)

	var screenWorkDir string
	screen := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(_ context.Context, model, prompt, workDir string) ([]byte, error) {
			screenWorkDir = workDir
			return envelope(t, "```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```"), nil
		},
	)
	_, err = screen.Screen(context.Background(), composeReq())
	require.NoError(t, err)
	require.Empty(t, screenWorkDir, "a screen run is never anchored: the species leaves WorkDir empty")
}

// claudeCmd is where the anchor becomes real: the built command is inspected,
// never run, so the assertion covers the production argv without the CLI ever
// executing. Two things are pinned. The anchor IS cmd.Dir — copilot's -C has no
// claude analogue, and cwd is the lever both CLIs share. And the run's read
// grant is never widened: because reads resolve from the same root as
// discovery, the named directory is the ceiling, so tm3k must pass neither
// --add-dir nor --allow-all-paths on any leg, ever (ADR 0027 decision 3).
func TestClaudeCmdAnchorsTheRunAndNeverWidensTheGrant(t *testing.T) {
	// The extra arguments are the act leg's, verbatim from claudeActInvoke —
	// the only leg that is ever anchored.
	cmd := claudeCmd(context.Background(), "sonnet", "prompt text", "/srv/skills-worktree",
		"--allowedTools", "Bash(gh:*)")

	require.Equal(t, "/srv/skills-worktree", cmd.Dir)
	for _, arg := range cmd.Args {
		require.NotContains(t, arg, "--add-dir", "the grant is bounded by WorkDir and never widened")
		require.NotContains(t, arg, "--allow-all-paths", "the grant is bounded by WorkDir and never widened")
	}

	// An unanchored run leaves cmd.Dir empty, which inherits tm3k's own cwd —
	// the behaviour every leg had before WorkDir existed, bit-for-bit.
	require.Empty(t, claudeCmd(context.Background(), "", "prompt text", "").Dir)
}

func TestClaudeActFailuresSurfaceAsErrors(t *testing.T) {
	// A crashed CLI surfaces its error.
	c := scriptedClaudeAgent(func(context.Context, string, string, string) ([]byte, error) {
		return nil, errors.New("claude -p: signal: killed")
	})
	_, err := c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "claude -p")

	// An errored run envelope is an error, not a transcript.
	c = scriptedClaudeAgent(func(context.Context, string, string, string) ([]byte, error) {
		return []byte(`{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": ""}`), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "errored")

	// Non-envelope stdout (crash text) fails the decode.
	c = scriptedClaudeAgent(func(context.Context, string, string, string) ([]byte, error) {
		return []byte("panic: something broke"), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.Error(t, err)
}

// Stdout that is not a result envelope never becomes text: the adapter's own
// contract is the envelope, so a crashed or half-written run fails HERE and the
// species is never handed something to transcribe as though it were an answer.
// (Prose that decodes fine but carries no verdict document is the species'
// problem, and it keeps its transcript — see AS5.)
func TestClaudeScreenUnparseableOutputIsAFailedAttempt(t *testing.T) {
	c := scriptedClaude(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string, string) ([]byte, error) {
			return []byte("I looked at it. CAN PROCEED!"), nil
		},
	)

	result, err := c.Screen(context.Background(), composeReq())

	require.Error(t, err)
	require.Empty(t, result, "a run whose envelope will not decode yields no text at all")
}
