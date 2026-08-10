package harness

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	json "github.com/goccy/go-json"
	"github.com/stretchr/testify/require"
)

// scriptedOpenCode returns an OpenCode adapter whose process seams are
// scripted, so real gh/opencode CLIs never execute in tests.
func scriptedOpenCode(fetchDiff func(ctx context.Context, repo string, number int) (string, error),
	invoke func(ctx context.Context, model, prompt, workDir string) ([]byte, error)) *OpenCode {
	return &OpenCode{fetchDiff: fetchDiff, invoke: invoke}
}

func TestOpenCodeScreenFetchesComposesInvokesAndReturnsResultText(t *testing.T) {
	req := composeReq()
	var gotRepo string
	var gotNumber int
	var gotModel, gotPrompt string
	o := scriptedOpenCode(
		func(_ context.Context, repo string, number int) (string, error) {
			gotRepo, gotNumber = repo, number
			return "+harmless line", nil
		},
		func(_ context.Context, model, prompt, _ string) ([]byte, error) {
			gotModel, gotPrompt = model, prompt
			// Text-mode stdout IS the response text — no envelope.
			return []byte("Reviewed.\n\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n"), nil
		},
	)

	result, err := o.Screen(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Reviewed.\n\n```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```\n", result,
		"the whole result text comes back, not a distillate of it")
	require.Equal(t, "acme/widgets", gotRepo)
	require.Equal(t, 42, gotNumber)
	require.Equal(t, "sonnet", gotModel)
	require.Equal(t, ComposePrompt(req, "+harmless line"), gotPrompt)
}

// Prose with no verdict document in it is no longer an adapter failure: the
// adapter stops at the text, and AIScreen fails the extraction after the run
// has been transcribed (ADR 0028). Silence remains the one thing this layer
// still judges — there is no text to hand up.
func TestOpenCodeScreenFailuresAreFailedAttempts(t *testing.T) {
	tests := []struct {
		name      string
		fetchDiff func(context.Context, string, int) (string, error)
		invoke    func(context.Context, string, string, string) ([]byte, error)
		wantErr   string
	}{
		{
			name: "diff fetch fails",
			fetchDiff: func(context.Context, string, int) (string, error) {
				return "", errors.New("gh pr diff: exit status 1")
			},
			invoke: func(context.Context, string, string, string) ([]byte, error) {
				t.Fatal("harness must not run without a diff")
				return nil, nil
			},
			wantErr: "fetch diff",
		},
		{
			name:      "invoke fails",
			fetchDiff: func(context.Context, string, int) (string, error) { return "+x", nil },
			invoke: func(context.Context, string, string, string) ([]byte, error) {
				return nil, errors.New("opencode run: signal: killed")
			},
			wantErr: "opencode run",
		},
		{
			name:      "output is silent",
			fetchDiff: func(context.Context, string, int) (string, error) { return "+x", nil },
			invoke: func(context.Context, string, string, string) ([]byte, error) {
				return []byte("   \n\t "), nil
			},
			wantErr: "empty opencode output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := scriptedOpenCode(tt.fetchDiff, tt.invoke)

			result, err := o.Screen(context.Background(), composeReq())

			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, result)
		})
	}
}

// A failed run that already SPOKE keeps its text — the claude and copilot legs'
// rule, and the whole point of ADR 0028's move: the text is the only account of
// the burnt strike the operator is about to see.
func TestOpenCodeScreenKeepsTheTextOfARunThatFailedAfterSpeaking(t *testing.T) {
	o := scriptedOpenCode(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(context.Context, string, string, string) ([]byte, error) {
			return []byte("Reviewed. One concern: the retry loop is unbounded."),
				errors.New("opencode run: exit status 1")
		},
	)

	result, err := o.Screen(context.Background(), composeReq())

	require.ErrorContains(t, err, "opencode run", "the failure is still a failed attempt")
	require.Equal(t, "Reviewed. One concern: the retry loop is unbounded.", result,
		"what the run said outlives how it ended")
}

func TestOpenCodeActKeepsTheTextOfARunThatFailedAfterSpeaking(t *testing.T) {
	o := &OpenCode{act: func(context.Context, string, string, string) ([]byte, error) {
		return []byte("Posted a review comment on #42."), errors.New("opencode run: exit status 1")
	}}

	transcript, err := o.Act(context.Background(), composeReq())

	require.ErrorContains(t, err, "opencode run")
	require.Equal(t, "Posted a review comment on #42.", transcript,
		"the agent holds gh authority: a run that failed AFTER posting is the one worth recording")
}

func TestOpenCodeActComposesInvokesAndReturnsTranscript(t *testing.T) {
	req := composeReq()
	var gotModel, gotPrompt string
	o := &OpenCode{act: func(_ context.Context, model, prompt, _ string) ([]byte, error) {
		gotModel, gotPrompt = model, prompt
		return []byte("Posted a review comment on #42.\n"), nil
	}}

	transcript, err := o.Act(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, "Posted a review comment on #42.\n", transcript)
	require.Equal(t, "sonnet", gotModel)
	require.Equal(t, ComposeNotifyPrompt(req), gotPrompt)
}

func TestOpenCodeActFailuresSurfaceAsErrors(t *testing.T) {
	o := &OpenCode{act: func(context.Context, string, string, string) ([]byte, error) {
		return nil, errors.New("opencode run: signal: killed")
	}}
	_, err := o.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "opencode run")

	o = &OpenCode{act: func(context.Context, string, string, string) ([]byte, error) {
		return []byte("  \n"), nil
	}}
	_, err = o.Act(context.Background(), composeReq())
	require.ErrorContains(t, err, "empty opencode output")
}

func TestOpenCodeCarriesWorkDirToProcessSeam(t *testing.T) {
	req := composeReq()
	req.WorkDir = "/srv/skills-worktree"

	var actWorkDir string
	agent := &OpenCode{act: func(_ context.Context, _, _, workDir string) ([]byte, error) {
		actWorkDir = workDir
		return []byte("Posted a review comment on #42."), nil
	}}
	_, err := agent.Act(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "/srv/skills-worktree", actWorkDir)

	var screenWorkDir string
	screen := scriptedOpenCode(
		func(context.Context, string, int) (string, error) { return "+x", nil },
		func(_ context.Context, _, _, workDir string) ([]byte, error) {
			screenWorkDir = workDir
			return []byte("```json\n{\"verdict\": \"proceed\", \"reason\": \"clean\"}\n```"), nil
		},
	)
	_, err = screen.Screen(context.Background(), composeReq())
	require.NoError(t, err)
	require.Empty(t, screenWorkDir, "a Screen run is never anchored")
}

func TestOpenCodeCmdUsesStdinAndRunLocalProfile(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"test":{"options":{"baseURL":"http://example.test"}}},"agent":{"other":{"mode":"primary"}}}`)
	cmd, err := openCodeCmd(context.Background(), "test/model", "prompt text", "/srv/skills-worktree", openCodeNotifierAgent)

	require.NoError(t, err)
	require.Equal(t, "/srv/skills-worktree", cmd.Dir)
	require.Equal(t, []string{"opencode", "run", "--agent", openCodeNotifierAgent, "--model", "test/model"}, cmd.Args)
	require.NotContains(t, cmd.Args, "--auto")
	require.NotContains(t, cmd.Args, "--continue")
	require.NotContains(t, cmd.Args, "--session")
	require.NotContains(t, cmd.Args, "--attach")

	stdin, err := io.ReadAll(cmd.Stdin)
	require.NoError(t, err)
	require.Equal(t, "prompt text", string(stdin))

	env := envMap(cmd.Env)
	require.Equal(t, "false", env["OPENCODE_AUTO_SHARE"])
	require.Equal(t, "true", env["OPENCODE_DISABLE_AUTOUPDATE"])
	require.Equal(t, "true", env["OPENCODE_DISABLE_LSP_DOWNLOAD"])
	require.NotEmpty(t, env["OPENCODE_CONFIG_CONTENT"])

	defaultModelCmd, err := openCodeCmd(context.Background(), "", "prompt text", "", openCodeScreenAgent)
	require.NoError(t, err)
	require.NotContains(t, defaultModelCmd.Args, "--model", "an empty hook Model uses the operator default")
}

func TestOpenCodeConfigPreservesInheritedSettingsAndReplacesReservedAgent(t *testing.T) {
	const agent = "tm3k-screen-0123456789abcdef"
	inherited := `{
		"provider":{"local":{"options":{"baseURL":"http://127.0.0.1:1234/v1"}}},
		"agent":{"other":{"mode":"primary"},"tm3k-screen":{"mode":"subagent","steps":9}},
		"mcp":{"trusted":{"enabled":true}}
	}`
	configured, err := openCodeConfig(inherited, agent)
	require.NoError(t, err)

	var config map[string]any
	require.NoError(t, json.Unmarshal([]byte(configured), &config))
	require.Equal(t, "disabled", config["share"])
	require.Equal(t, false, config["snapshot"])
	require.Equal(t, agent, config["default_agent"])
	require.Equal(t, "http://127.0.0.1:1234/v1", config["provider"].(map[string]any)["local"].(map[string]any)["options"].(map[string]any)["baseURL"])
	require.Equal(t, true, config["mcp"].(map[string]any)["trusted"].(map[string]any)["enabled"])

	agents := config["agent"].(map[string]any)
	require.Equal(t, map[string]any{"mode": "primary"}, agents["other"])
	require.Equal(t, map[string]any{"mode": "subagent", "steps": float64(9)}, agents[openCodeScreenAgent])
	screen := agents[agent].(map[string]any)
	require.Equal(t, "primary", screen["mode"])
	require.Equal(t, map[string]any{"*": "deny"}, screen["permission"])
}

func TestOpenCodeConfigNotifierAllowsOnlyGhActionChannel(t *testing.T) {
	configured, err := openCodeConfig("", openCodeNotifierAgent)
	require.NoError(t, err)

	var config map[string]any
	require.NoError(t, json.Unmarshal([]byte(configured), &config))
	permission := config["agent"].(map[string]any)[openCodeNotifierAgent].(map[string]any)["permission"].(map[string]any)
	require.Equal(t, "deny", permission["*"])
	require.Equal(t, "allow", permission["read"])
	require.Equal(t, "allow", permission["skill"])
	require.Equal(t, map[string]any{"*": "deny", "gh *": "allow"}, permission["bash"])
	require.NotContains(t, permission, "edit")
	require.NotContains(t, permission, "webfetch")
	require.NotContains(t, permission, "external_directory")
	require.Less(t, strings.Index(configured, `"*":"deny"`),
		strings.Index(configured, `"bash":{"*":"deny","gh *":"allow"}`),
		"the broad deny must precede every notifier exception")
}

func TestNewOpenCodeAgentNameIsRunPrivate(t *testing.T) {
	screen, err := newOpenCodeAgentName(openCodeScreenAgent)
	require.NoError(t, err)
	notifier, err := newOpenCodeAgentName(openCodeNotifierAgent)
	require.NoError(t, err)

	require.Regexp(t, `^tm3k-screen-[0-9a-f]{16}$`, screen)
	require.Regexp(t, `^tm3k-notifier-[0-9a-f]{16}$`, notifier)
	require.NotEqual(t, screen, notifier)
}

func TestOpenCodeConfigRejectsInvalidInheritedConfig(t *testing.T) {
	_, err := openCodeConfig("{", openCodeScreenAgent)
	require.ErrorContains(t, err, "decode inherited OPENCODE_CONFIG_CONTENT")

	_, err = openCodeConfig(`{"agent":"build"}`, openCodeScreenAgent)
	require.ErrorContains(t, err, "agent must be an object")

	_, err = openCodeConfig("null", openCodeScreenAgent)
	require.ErrorContains(t, err, "must be an object")
}

func TestSetEnvReplacesOneEntryCaseInsensitively(t *testing.T) {
	env := setEnv([]string{"A=one", "opencode_auto_share=true"}, "OPENCODE_AUTO_SHARE", "false")

	require.Equal(t, []string{"A=one", "OPENCODE_AUTO_SHARE=false"}, env)
}

func envMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}
