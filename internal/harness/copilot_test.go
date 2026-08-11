package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// scriptedCopilot returns a Copilot adapter whose process seams are scripted:
// the gh diff fetch and the copilot invocation are plain functions, so the
// real gh/copilot CLIs never run in tests (the claude adapter's precedent).
func scriptedCopilot(fetchDiff func(ctx context.Context, repo string, number int) (string, error),
	invoke func(ctx context.Context, model, prompt, workDir string) ([]byte, error)) *Copilot {
	return &Copilot{fetchDiff: fetchDiff, invoke: invoke}
}

// Copilot's screen leg is claude's minus the envelope: silent-mode stdout
// already IS the result text, so the adapter hands it over whole and the
// species extracts from it (ADR 0028).
func TestCopilotScreenFetchesComposesInvokesAndReturnsResultText(t *testing.T) {
	req := composeReq()
	var gotRepo string
	var gotNumber int
	var gotModel, gotPrompt string
	c := scriptedCopilot(
		func(_ context.Context, repo string, number int) (string, error) {
			gotRepo, gotNumber = repo, number
			return "+harmless line", nil
		},
		func(_ context.Context, model, prompt, _ string) ([]byte, error) {
			gotModel, gotPrompt = model, prompt
			// Silent-mode stdout IS the response text — no envelope (ADR 0024).
			return []byte("Reviewed.\n\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n"), nil
		},
	)

	result, err := c.Screen(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Reviewed.\n\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n", result,
		"the whole result text comes back, not a distillate of it")
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
		func(context.Context, string, string, string) ([]byte, error) {
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
		func(context.Context, string, string, string) ([]byte, error) {
			return nil, errors.New("copilot -p: signal: killed")
		},
	)

	_, err := c.Screen(context.Background(), composeReq())
	require.ErrorContains(t, err, "copilot -p")
}

// A failed run that already SPOKE keeps its text. Silent-mode stdout is the
// answer itself, so a copilot pass that reviewed the diff and then exited
// non-zero on the way out has produced the whole evidence — and returning nil
// here is the evidence loss ADR 0028 exists to end, one layer below where it
// was fixed. The error stands; the species transcribes what came back before it
// judges the error.
func TestCopilotScreenKeepsTheTextOfARunThatFailedAfterSpeaking(t *testing.T) {
	c := scriptedCopilot(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string, string) ([]byte, error) {
			return []byte("Reviewed. One concern: the retry loop is unbounded."),
				errors.New("copilot -p: exit status 1: telemetry flush failed")
		},
	)

	result, err := c.Screen(context.Background(), composeReq())

	require.ErrorContains(t, err, "copilot -p", "the failure is still a failed attempt")
	require.Equal(t, "Reviewed. One concern: the retry loop is unbounded.", result,
		"what the run said outlives how it ended")
}

func TestCopilotActKeepsTheTextOfARunThatFailedAfterSpeaking(t *testing.T) {
	c := scriptedCopilotAgent(func(context.Context, string, string, string) ([]byte, error) {
		return []byte("Posted a review comment on #42."),
			errors.New("copilot -p: exit status 1")
	})

	transcript, err := c.Act(context.Background(), composeReq())

	require.ErrorContains(t, err, "copilot -p")
	require.Equal(t, "Posted a review comment on #42.", transcript,
		"the agent holds gh authority: a run that failed AFTER posting is the one worth recording")
}

// Silence is copilot's only adapter-level failure. With no envelope to decode,
// the CLI's stdout either is the result text or there is none — a run that said
// nothing is a failed attempt, and every OTHER disappointment (prose with no
// verdict document in it) now belongs to the species, which keeps the text as
// its transcript before failing. This is the one adapter check that survives
// the move; there is nothing else at this layer left to judge.
func TestCopilotScreenSilentOutputIsAFailedAttempt(t *testing.T) {
	c := scriptedCopilot(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string, string) ([]byte, error) {
			return []byte("   \n\t "), nil
		},
	)

	result, err := c.Screen(context.Background(), composeReq())

	require.ErrorContains(t, err, "empty copilot output")
	require.Empty(t, result)
}

// scriptedCopilotAgent returns a Copilot adapter whose side-effect seam is
// scripted — the real copilot CLI never runs in tests.
func scriptedCopilotAgent(act func(ctx context.Context, model, prompt, workDir string) ([]byte, error)) *Copilot {
	return &Copilot{act: act}
}

func TestCopilotActComposesInvokesAndReturnsTheTranscript(t *testing.T) {
	req := composeReq()
	var gotModel, gotPrompt string
	c := scriptedCopilotAgent(func(_ context.Context, model, prompt, _ string) ([]byte, error) {
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

// One field serves both adapters, which is the whole reason WorkDir is the
// process's cwd and not copilot's -C: the Request's anchor reaches the process
// seam verbatim here exactly as it does on claude, and internal/harness grows
// no per-CLI branch. The screen leg stays unanchored — AIScreen never populates
// the field, and copilot's toolless screen run has no skill-resolution
// mechanism to anchor anyway.
func TestCopilotCarriesWorkDirToTheProcessSeam(t *testing.T) {
	req := composeReq()
	req.WorkDir = "/srv/skills-worktree"

	var actWorkDir string
	agent := &Copilot{act: func(_ context.Context, model, prompt, workDir string) ([]byte, error) {
		actWorkDir = workDir
		return []byte("Posted a review comment on #42.\n"), nil
	}}
	_, err := agent.Act(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "/srv/skills-worktree", actWorkDir)

	var screenWorkDir string
	screen := scriptedCopilot(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(_ context.Context, model, prompt, workDir string) ([]byte, error) {
			screenWorkDir = workDir
			return []byte("```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n"), nil
		},
	)
	_, err = screen.Screen(context.Background(), composeReq())
	require.NoError(t, err)
	require.Empty(t, screenWorkDir, "a screen run is never anchored: the species leaves WorkDir empty")
}

// copilotCmd's twin of the claude assertion: the anchor is the process's cwd,
// not copilot's -C, and the read grant is never widened. The hermetic base
// flags of ADR 0024 ride every leg unchanged — they suppress AGENTS.md, which
// is orthogonal to the skills the anchored directory makes discoverable.
func TestCopilotCmdAnchorsTheRunAndNeverWidensTheGrant(t *testing.T) {
	// The extra arguments are the act leg's, verbatim from copilotActInvoke —
	// the only leg that is ever anchored.
	cmd := copilotCmd(context.Background(), "sonnet", "prompt text", "/srv/skills-worktree",
		"--allow-tool", "shell(gh:*)")

	require.Equal(t, "/srv/skills-worktree", cmd.Dir)
	for _, arg := range cmd.Args {
		require.NotContains(t, arg, "--add-dir", "the grant is bounded by WorkDir and never widened")
		require.NotContains(t, arg, "--allow-all-paths", "the grant is bounded by WorkDir and never widened")
	}
	require.Contains(t, cmd.Args, "--no-custom-instructions",
		"hermetic-by-default stands: ambient instructions stay off while ambient skills come on")

	// An unanchored run leaves cmd.Dir empty, which inherits tm3k's own cwd —
	// the behaviour every leg had before WorkDir existed, bit-for-bit. The
	// toolless screen leg is always in this shape.
	require.Empty(t, copilotCmd(context.Background(), "", "prompt text", "", "--available-tools=__none__").Dir)
}

func TestCopilotActFailuresSurfaceAsErrors(t *testing.T) {
	// A crashed CLI surfaces its error.
	c := scriptedCopilotAgent(func(context.Context, string, string, string) ([]byte, error) {
		return nil, errors.New("copilot -p: signal: killed")
	})
	_, err := c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "copilot -p")

	// Blank silent-mode output is an error, not a transcript: the run said
	// nothing, so there is nothing to log as the agent's account of itself.
	c = scriptedCopilotAgent(func(context.Context, string, string, string) ([]byte, error) {
		return []byte("  \n"), nil
	})
	_, err = c.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "empty copilot output")
}
