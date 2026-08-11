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

// adapterFunc adapts a bare function to the Adapter seam — the scripted fake
// of the species tests. It returns result TEXT, as the seam now does: what a
// screen run yields is what the harness said, and turning that into a verdict
// is the species' job (ADR 0028).
type adapterFunc func(ctx context.Context, req Request) (string, error)

func (f adapterFunc) Screen(ctx context.Context, req Request) (string, error) {
	return f(ctx, req)
}

// verdictText is a result text carrying exactly the instructed verdict document
// — what an adapter hands a species on a well-behaved run.
func verdictText(outcome, reason string) string {
	return "I reviewed the diff.\n\n```json\n{\"verdict\": \"" + outcome + "\", \"reason\": \"" + reason + "\"}\n```\n"
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

// AS5 is why extraction moved up out of the adapter (ADR 0028). A run whose
// text carries no verdict document is a failed attempt on the 3-strikes path,
// and its text is the ONLY evidence of why the agent answered the way it did.
// The species transcribes before it extracts, so the evidence outlives the
// failure — before this, "no verdict document in harness result" was the most
// opaque error in tm3k and the harness threw away the one thing that explained
// it.
func TestAIScreenTranscribesTheTextThatFailedExtraction(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", Prompt: "look closely"}
	sink := &recordingTranscriber{}
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(context.Context, Request) (string, error) {
		return "I looked at the diff and it seems fine to me.", nil // prose, no fenced document
	}), sink)

	v, err := screen.Screen(context.Background(), prCtx())

	require.ErrorContains(t, err, "verdict")
	require.Equal(t, hook.Verdict{}, v, "prose is never scanned for keywords (ADR 0023)")
	require.Len(t, sink.rows, 1, "the text that failed extraction is exactly the evidence worth keeping")
	require.Equal(t, "I looked at the diff and it seems fine to me.", sink.rows[0].Text)
	require.Equal(t, "screen", sink.rows[0].Kind)
	require.Equal(t, "feedface", sink.rows[0].Head, "per-head keying is the screen's whole invalidation story")
}

// AS5b is the case AS5 leaves uncovered and the overwhelmingly common one: a
// screen run that DID yield a verdict is transcribed too. Without it the suite
// stays green if transcribe is moved down into the extraction-failure branch —
// the natural "only keep the evidence when it failed" simplification, which
// AS5's own name invites — and screen transcription silently vanishes for every
// successful run while CONTEXT.md still claims every AI run that produced text
// is recorded. The verdict is the outcome; the transcript is what was said, and
// the sink holds one for each.
func TestAIScreenTranscribesTheRunThatYieldedItsVerdict(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", Prompt: "look closely"}
	sink := &recordingTranscriber{}
	result := verdictText("proceed", "clean")
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(context.Context, Request) (string, error) {
		return result, nil
	}), sink)

	v, err := screen.Screen(context.Background(), prCtx())

	require.NoError(t, err)
	require.Equal(t, hook.Verdict{Outcome: hook.Proceed, Reason: "clean"}, v)
	require.Len(t, sink.rows, 1, "a successful run accounts for itself like any other")
	require.Equal(t, result, sink.rows[0].Text, "the whole text, verdict document and prose alike")
	require.Equal(t, "screen", sink.rows[0].Kind)
	require.Equal(t, "s1", sink.rows[0].HookID)
	require.Equal(t, "feedface", sink.rows[0].Head, "per-head keying is the screen's whole invalidation story")
}

// AS6 is AS5 one layer down: the adapter itself failed — a non-zero exit, a
// timeout kill — after the CLI had already printed its answer. That run burns
// one of three strikes and re-spends a paid harness call, so the operator
// deciding whether to keep the screen configured needs to see what it said. The
// error is unchanged: still a failed attempt, never a fabricated verdict.
func TestAIScreenTranscribesARunThatFailedAfterSpeaking(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "copilot", Prompt: "look closely"}
	sink := &recordingTranscriber{}
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(context.Context, Request) (string, error) {
		return verdictText("hold", "unbounded retry loop"), errors.New("copilot -p: exit status 1")
	}), sink)

	v, err := screen.Screen(context.Background(), prCtx())

	require.ErrorContains(t, err, "copilot -p")
	require.Equal(t, hook.Verdict{}, v, "a failed run yields no verdict, however well-formed its text")
	require.Len(t, sink.rows, 1, "a failed attempt that spoke still accounts for itself")
	require.Equal(t, verdictText("hold", "unbounded retry loop"), sink.rows[0].Text)
}

func TestAIScreenRealizesTheScreenKindFromItsSpec(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", Model: "sonnet", Prompt: "look closely"}
	var got Request
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(_ context.Context, req Request) (string, error) {
		got = req
		return verdictText("proceed", "clean"), nil
	}), &recordingTranscriber{})

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

// TestAIScreenNeverAnchorsItsRun is the second of decision 2's two
// enforcements (ADR 0027): ScreenConfig has no field to declare an anchor, AND
// the Screen species has no code path that would carry one — NewAIScreen takes
// no working directory, so every Request it issues leaves WorkDir empty and
// every screen run keeps cmd.Dir = "". A Screen judged against a mutable,
// unversioned tree could disagree with itself over the same PR head for
// reasons no ledger records, and a gate whose input is not reproducible is not
// a gate.
func TestAIScreenNeverAnchorsItsRun(t *testing.T) {
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "copilot", Prompt: "look closely"}
	var got Request
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(_ context.Context, req Request) (string, error) {
		got = req
		return verdictText("proceed", "clean"), nil
	}), &recordingTranscriber{})

	_, err := screen.Screen(context.Background(), prCtx())
	require.NoError(t, err)
	require.Empty(t, got.WorkDir)
}

func TestAIScreenReadsPromptFileAtRunTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	require.NoError(t, os.WriteFile(path, []byte("first version"), 0o644))
	spec := hook.Spec{ID: "s1", Name: "security", Harness: "claude", PromptFile: path}
	var instructions []string
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(_ context.Context, req Request) (string, error) {
		instructions = append(instructions, req.Instructions)
		return verdictText("proceed", "clean"), nil
	}), &recordingTranscriber{})

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
	screen := NewAIScreen(spec, "acme/widgets", adapterFunc(func(context.Context, Request) (string, error) {
		invoked = true
		return "", nil
	}), &recordingTranscriber{})

	v, err := screen.Screen(context.Background(), prCtx())

	require.ErrorContains(t, err, "prompt")
	require.Equal(t, hook.Verdict{}, v)
	require.False(t, invoked, "a screen without instructions must never invoke the harness")
}
