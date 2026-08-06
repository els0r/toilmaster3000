package harness

import (
	"context"
	"log/slog"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// AINotifier is the AI Notifier species (ADR 0023), AIScreen's sibling: it
// realizes the hook.Notifier kind from a validated declarative Spec by
// delegating one Request per fire to a harness Agent. It owns exactly the
// Spec->Request translation (resolving the instructions, naming the model);
// the agent does the rest itself — fetches the diff, posts the review comment
// or requests changes as the runtime identity. No verdict comes back: the
// transcript is logged here and otherwise ignored.
type AINotifier struct {
	spec   hook.Spec
	repo   string
	agent  Agent
	logger *slog.Logger
}

// NewAINotifier constructs the species over an already-validated Spec. repo is
// the configured candidate-set slug ("owner/name"), taken at construction
// exactly like AIScreen's (the engine leaves PRContext.Repo empty today).
func NewAINotifier(spec hook.Spec, repo string, agent Agent) *AINotifier {
	return &AINotifier{spec: spec, repo: repo, agent: agent, logger: slog.Default()}
}

// Notify runs one side-effecting agent pass for the PR. An error — unreadable
// prompt file, or any agent failure — is a logged miss for the runner
// (ADR 0021: never retried, never able to block an engine action).
func (n *AINotifier) Notify(ctx context.Context, pr hook.PRContext) error {
	instructions, err := resolveInstructions(n.spec)
	if err != nil {
		return err
	}
	transcript, err := n.agent.Act(ctx, Request{
		Model:        n.spec.Model,
		Instructions: instructions,
		Repo:         n.repo,
		Number:       pr.Number,
		Title:        pr.Title,
		Author:       pr.Author,
		URL:          pr.URL,
		HeadSHA:      pr.HeadSHA,
	})
	if err != nil {
		return err
	}
	// The transcript is the agent's account of what it did — logged for the
	// operator, never parsed, never acted on (ADR 0023: no verdict extraction
	// on the Notifier side).
	n.logger.Info("notifier: agent run transcript",
		"notifier", n.spec.Name, "pr", pr.Number, "transcript", transcript)
	return nil
}
