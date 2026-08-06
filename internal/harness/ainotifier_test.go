package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// agentFunc adapts a bare function to the Agent seam — the scripted fake of
// the notifier-species tests.
type agentFunc func(ctx context.Context, req Request) (string, error)

func (f agentFunc) Act(ctx context.Context, req Request) (string, error) {
	return f(ctx, req)
}

// queueCtx is the queue_entered PRContext the notifier-species tests share.
func queueCtx() hook.PRContext {
	return hook.PRContext{
		Point:   hook.QueueEntered,
		Number:  7,
		Title:   "feat(api): add endpoint",
		Author:  "alice",
		URL:     "https://github.com/acme/widgets/pull/7",
		HeadSHA: "feedface",
		Reasons: []string{"docs gate"},
	}
}

// AN1 (tracer): AINotifier realizes the hook.Notifier kind from its Spec —
// one Act per Notify, carrying the resolved instructions, the model, and the
// PR identity; the agent's transcript is logged and otherwise ignored (no
// verdict extraction, ADR 0023).
func TestAINotifierRealizesTheNotifierKindFromItsSpec(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Model: "sonnet", Prompt: "review Go code"}
	var got Request
	notifier := NewAINotifier(spec, "acme/widgets", agentFunc(func(_ context.Context, req Request) (string, error) {
		got = req
		return "posted a review comment", nil
	}))

	err := notifier.Notify(context.Background(), queueCtx())

	require.NoError(t, err)
	require.Equal(t, Request{
		Model:        "sonnet",
		Instructions: "review Go code",
		Repo:         "acme/widgets", // the construction-time repo, the AIScreen precedent
		Number:       7,
		Title:        "feat(api): add endpoint",
		Author:       "alice",
		URL:          "https://github.com/acme/widgets/pull/7",
		HeadSHA:      "feedface",
	}, got)
}

// AN2: a failing agent run surfaces as the Notify error — the runner logs the
// miss and never retries (ADR 0021); the species fabricates nothing.
func TestAINotifierAgentFailureSurfacesAsError(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Prompt: "review it"}
	notifier := NewAINotifier(spec, "acme/widgets", agentFunc(func(context.Context, Request) (string, error) {
		return "", errors.New("claude -p: signal: killed")
	}))

	err := notifier.Notify(context.Background(), queueCtx())
	require.ErrorContains(t, err, "claude -p")
}

// AN3: PromptFile is read at run time (an edit takes effect without a
// restart) and an unreadable file is an error that never invokes the harness
// — the AIScreen contract, shared by the sibling species.
func TestAINotifierReadsPromptFileAtRunTimeAndFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	require.NoError(t, os.WriteFile(path, []byte("go instructions"), 0o644))
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "claude", PromptFile: path}
	var instructions string
	notifier := NewAINotifier(spec, "acme/widgets", agentFunc(func(_ context.Context, req Request) (string, error) {
		instructions = req.Instructions
		return "", nil
	}))

	require.NoError(t, notifier.Notify(context.Background(), queueCtx()))
	require.Equal(t, "go instructions", instructions)

	invoked := false
	missing := hook.Spec{ID: "n2", Name: "go review 2", Harness: "claude", PromptFile: filepath.Join(t.TempDir(), "missing.md")}
	broken := NewAINotifier(missing, "acme/widgets", agentFunc(func(context.Context, Request) (string, error) {
		invoked = true
		return "", nil
	}))
	err := broken.Notify(context.Background(), queueCtx())
	require.ErrorContains(t, err, "prompt")
	require.False(t, invoked, "a notifier without instructions must never invoke the harness")
}
