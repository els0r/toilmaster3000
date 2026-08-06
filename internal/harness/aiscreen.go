package harness

import (
	"context"
	"fmt"
	"os"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// AIScreen is the AI Screen species (ADR 0023): it realizes the hook.Screen
// kind from a validated declarative Spec by delegating one Request per run to
// a harness Adapter. It owns exactly the Spec->Request translation: resolving
// the instructions (inline Prompt, or PromptFile read at run time so an edit
// takes effect without a restart) and naming the model.
type AIScreen struct {
	spec    hook.Spec
	repo    string
	adapter Adapter
}

// NewAIScreen constructs the species over an already-validated Spec. repo is
// the configured candidate-set slug ("owner/name"), taken at construction:
// hook.PRContext carries a Repo field, but the engine passes it empty today —
// the candidate set is single-repo by construction, so main wires the one
// configured repo in here. Should the engine ever populate PRContext.Repo,
// this seam is the place to prefer it.
func NewAIScreen(spec hook.Spec, repo string, adapter Adapter) *AIScreen {
	return &AIScreen{spec: spec, repo: repo, adapter: adapter}
}

// Screen runs one screen pass. An error — unreadable prompt file, or any
// adapter failure — is a failed attempt for the Screener's 3-strikes path
// (ADR 0022), never a verdict.
func (s *AIScreen) Screen(ctx context.Context, pr hook.PRContext) (hook.Verdict, error) {
	instructions, err := resolveInstructions(s.spec)
	if err != nil {
		return hook.Verdict{}, err
	}
	return s.adapter.Screen(ctx, Request{
		Model:        s.spec.Model,
		Instructions: instructions,
		Repo:         s.repo,
		Number:       pr.Number,
		Title:        pr.Title,
		Author:       pr.Author,
		URL:          pr.URL,
		HeadSHA:      pr.HeadSHA,
	})
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
