package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// adapterFunc adapts a bare function to the Adapter seam — the scripted fake
// of the species tests.
type adapterFunc func(ctx context.Context, req Request) (hook.Verdict, error)

func (f adapterFunc) Screen(ctx context.Context, req Request) (hook.Verdict, error) {
	return f(ctx, req)
}

// prCtx is the PRContext the species tests share — Repo deliberately empty,
// exactly as the engine passes it today.
func prCtx() hook.PRContext {
	return hook.PRContext{
		Point:   hook.PreApprove,
		Number:  7,
		Title:   "chore: tidy",
		Author:  "alice",
		URL:     "https://github.com/acme/widgets/pull/7",
		HeadSHA: "feedface",
	}
}

func TestAIScreenRealizesTheScreenKindFromItsSpec(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", Model: "sonnet", Prompt: "look closely"}
	var got Request
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(_ context.Context, req Request) (hook.Verdict, error) {
		got = req
		return hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, nil
	}))

	v, err := screen.Screen(context.Background(), prCtx())

	require.NoError(t, err)
	require.Equal(t, hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, v)
	require.Equal(t, Request{
		Model:        "sonnet",
		Instructions: "look closely",
		Repo:         "acme/widgets", // the construction-time repo, not PRContext's (empty today)
		Number:       7,
		Title:        "chore: tidy",
		Author:       "alice",
		URL:          "https://github.com/acme/widgets/pull/7",
		HeadSHA:      "feedface",
	}, got)
}

func TestAIScreenReadsPromptFileAtRunTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	require.NoError(t, os.WriteFile(path, []byte("first version"), 0o644))
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", PromptFile: path}
	var instructions []string
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(_ context.Context, req Request) (hook.Verdict, error) {
		instructions = append(instructions, req.Instructions)
		return hook.Verdict{Outcome: hook.Proceed}, nil
	}))

	_, err := screen.Screen(context.Background(), prCtx())
	require.NoError(t, err)

	// An edit between runs takes effect without a restart: the file is read
	// per run, not at construction.
	require.NoError(t, os.WriteFile(path, []byte("second version"), 0o644))
	_, err = screen.Screen(context.Background(), prCtx())
	require.NoError(t, err)

	require.Equal(t, []string{"first version", "second version"}, instructions)
}

func TestAIScreenUnreadablePromptFileIsAFailedAttempt(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", PromptFile: filepath.Join(t.TempDir(), "missing.md")}
	invoked := false
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(context.Context, Request) (hook.Verdict, error) {
		invoked = true
		return hook.Verdict{}, nil
	}))

	v, err := screen.Screen(context.Background(), prCtx())

	require.ErrorContains(t, err, "prompt")
	require.Equal(t, hook.Verdict{}, v)
	require.False(t, invoked, "a screen without instructions must never invoke the harness")
}
