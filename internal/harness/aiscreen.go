package harness

import (
	"context"
	"fmt"
	"os"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// AIScreen is the AI Screen species (ADR 0023): it realizes the hook.Screen
// kind from a validated declarative Spec by delegating one Request per run to
// a harness Adapter. It owns the Spec->Request translation — resolving the
// instructions (inline Prompt, or PromptFile read at run time so an edit takes
// effect without a restart) and naming the model — and, on the way back, both
// things an AI species does with what the harness said: transcribe it, then
// extract the verdict from it (ADR 0028).
type AIScreen struct {
	spec    hook.Spec
	repo    string
	adapter Adapter
	sink    Transcriber
}

// NewAIScreen constructs the species over an already-validated Spec. repo is
// the configured candidate-set slug ("owner/name"), taken at construction:
// hook.PRContext carries a Repo field, but the engine passes it empty today —
// the candidate set is single-repo by construction, so main wires the one
// configured repo in here. Should the engine ever populate PRContext.Repo,
// this seam is the place to prefer it.
// sink is required, not optional (ADR 0028): an AI species accounts for itself,
// so there is no way to construct one with nowhere to put its account. The
// obligation binds the SPECIES, never the kind.
func NewAIScreen(spec hook.Spec, repo string, adapter Adapter, sink Transcriber) *AIScreen {
	return &AIScreen{spec: spec, repo: repo, adapter: adapter, sink: sink}
}

// Screen runs one screen pass. An error — unreadable prompt file, any adapter
// failure, or a result with no extractable verdict — is a failed attempt for the
// Screener's 3-strikes path (ADR 0022), never a verdict.
//
// The order is deliberate: transcribe, THEN extract (ADR 0028). A run that
// produced text and then failed to yield a verdict is the case where the text
// matters most, and it is exactly the case an extract-first order would throw
// away. Extraction living here rather than in the adapter is what makes that
// order expressible at all.
func (s *AIScreen) Screen(ctx context.Context, pr hook.PRContext) (hook.Verdict, error) {
	instructions, err := resolveInstructions(s.spec)
	if err != nil {
		return hook.Verdict{}, err
	}
	result, err := s.adapter.Screen(ctx, Request{
		Model:        s.spec.Model,
		Instructions: instructions,
		Repo:         s.repo,
		Number:       pr.Number,
		Title:        pr.Title,
		Author:       pr.Author,
		URL:          pr.URL,
		HeadSHA:      pr.HeadSHA,
	})
	if err != nil {
		return hook.Verdict{}, err
	}
	transcribe(s.sink, kindScreen, s.spec, pr, result)

	v, err := ExtractVerdictText(result)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("extract verdict: %w", err)
	}
	return v, nil
}

// resolveInstructions resolves a hook's operator prompt: the inline Prompt,
// or the PromptFile read now — per run, so an edit takes effect without a
// restart (validation guarantees exactly one is set). Shared by both AI
// species.
func resolveInstructions(spec hook.Spec) (string, error) {
	if spec.PromptFile == "" {
		return spec.Prompt, nil
	}
	data, err := os.ReadFile(spec.PromptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file: %w", err)
	}
	return string(data), nil
}
