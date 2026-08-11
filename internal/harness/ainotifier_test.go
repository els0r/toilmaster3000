package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	notifier := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(_ context.Context, req Request) (string, error) {
		got = req
		return "posted a review comment", nil
	}), &recordingTranscriber{})

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

// AN5: a completed agent run is transcribed — the species' account of itself
// goes to the sink, identified so a reader joins it to the hookfires.jsonl row
// the fire wrote (hook_id + number) and reads it without joining anything
// (hook_name + head). And the transcript is GONE from the log: prose in a log
// line is what ADR 0028 exists to end.
func TestAINotifierTranscribesItsRun(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review assist", Harness: "claude", Prompt: "review it"}
	sink := &recordingTranscriber{}
	logs := captureLogs(t)
	notifier := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(context.Context, Request) (string, error) {
		return "Review posted (one `COMMENTED` review, no approval, no merge).", nil
	}), sink)

	require.NoError(t, notifier.Notify(context.Background(), queueCtx()))

	require.Len(t, sink.rows, 1)
	got := sink.rows[0]
	require.False(t, got.At.IsZero(), "the species stamps when the run finished")
	got.At = time.Time{}
	require.Equal(t, TranscriptRecord{
		Kind:     "notifier",
		HookID:   "n1",
		HookName: "go review assist",
		Number:   7,
		Head:     "feedface", // which commit the agent actually reviewed
		Text:     "Review posted (one `COMMENTED` review, no approval, no merge).",
	}, got)
	require.NotContains(t, logs.String(), "COMMENTED", "the transcript never reaches the log")
}

// AN6: nothing is transcribed when there is nothing the agent said. A failed
// run has no account to give — the fire is already recorded in hookfires.jsonl
// and the runner logs the miss — and an empty row would only be noise in a file
// whose entire purpose is prose.
func TestAINotifierTranscribesNothingWithoutText(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review assist", Harness: "claude", Prompt: "review it"}

	failed := &recordingTranscriber{}
	broken := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(context.Context, Request) (string, error) {
		return "", errors.New("claude -p: signal: killed")
	}), failed)
	require.Error(t, broken.Notify(context.Background(), queueCtx()))
	require.Empty(t, failed.rows, "a run that produced no text has no account to give")

	silent := &recordingTranscriber{}
	quiet := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(context.Context, Request) (string, error) {
		return "", nil
	}), silent)
	require.NoError(t, quiet.Notify(context.Background(), queueCtx()))
	require.Empty(t, silent.rows, "no text, no row — even on the success path")
}

// AN4: the configured WorkDir lands on every Request the species issues, taken
// at construction exactly as repo is (ADR 0027). It arrives as a constructor
// argument rather than off the Spec because WorkDir lives on NotifierConfig
// and nowhere on the shared Spec — the same shape Scope's compile takes. An
// unanchored Notifier leaves it empty and runs in tm3k's own cwd, bit-for-bit
// as before the field existed.
func TestAINotifierCarriesItsConfiguredWorkDir(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "copilot", Prompt: "/golang-pr-review"}

	var anchored Request
	notifier := NewAINotifier(spec, "acme/widgets", "/srv/skills-worktree",
		agentFunc(func(_ context.Context, req Request) (string, error) {
			anchored = req
			return "posted a review comment", nil
		}), &recordingTranscriber{})
	require.NoError(t, notifier.Notify(context.Background(), queueCtx()))
	require.Equal(t, "/srv/skills-worktree", anchored.WorkDir)
	require.Equal(t, "acme/widgets", anchored.Repo, "the anchor rides alongside repo, replacing nothing")

	var unanchored Request
	plain := NewAINotifier(spec, "acme/widgets", "",
		agentFunc(func(_ context.Context, req Request) (string, error) {
			unanchored = req
			return "posted a review comment", nil
		}), &recordingTranscriber{})
	require.NoError(t, plain.Notify(context.Background(), queueCtx()))
	require.Empty(t, unanchored.WorkDir)
}

// AN2: a failing agent run surfaces as the Notify error — the runner logs the
// miss and never retries (ADR 0021); the species fabricates nothing.
func TestAINotifierAgentFailureSurfacesAsError(t *testing.T) {
	spec := hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Prompt: "review it"}
	notifier := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(context.Context, Request) (string, error) {
		return "", errors.New("claude -p: signal: killed")
	}), &recordingTranscriber{})

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
	notifier := NewAINotifier(spec, "acme/widgets", "", agentFunc(func(_ context.Context, req Request) (string, error) {
		instructions = req.Instructions
		return "", nil
	}), &recordingTranscriber{})

	require.NoError(t, notifier.Notify(context.Background(), queueCtx()))
	require.Equal(t, "go instructions", instructions)

	invoked := false
	missing := hook.Spec{ID: "n2", Name: "go review 2", Harness: "claude", PromptFile: filepath.Join(t.TempDir(), "missing.md")}
	broken := NewAINotifier(missing, "acme/widgets", "", agentFunc(func(context.Context, Request) (string, error) {
		invoked = true
		return "", nil
	}), &recordingTranscriber{})
	err := broken.Notify(context.Background(), queueCtx())
	require.ErrorContains(t, err, "prompt")
	require.False(t, invoked, "a notifier without instructions must never invoke the harness")
}
