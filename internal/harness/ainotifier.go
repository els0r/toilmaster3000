package harness

import (
	"context"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// AINotifier is the AI Notifier species (ADR 0023), AIScreen's sibling: it
// realizes the hook.Notifier kind from a validated declarative Spec by
// delegating one Request per fire to a harness Agent. It owns exactly the
// Spec->Request translation (resolving the instructions, naming the model);
// the agent does the rest itself — fetches the diff, posts the review comment
// or requests changes as the runtime identity. No verdict comes back: the
// transcript is recorded in the sink and otherwise ignored (ADR 0028).
type AINotifier struct {
	spec    hook.Spec
	repo    string
	workDir string
	tools   []string
	agent   Agent
	sink    Transcriber
}

// NewAINotifier constructs the species over an already-validated Spec. repo is
// the configured candidate-set slug ("owner/name"), taken at construction
// exactly like AIScreen's (the engine leaves PRContext.Repo empty today).
//
// workDir is the Notifier's optional harness anchor (ADR 0027), boot-validated
// as an absolute directory. It arrives as an argument rather than off the Spec
// because it exists on the Notifier kind alone — the shape Scope's compile
// already takes — and the empty string is the unanchored Notifier. AIScreen has
// no such parameter and never sets Request.WorkDir: the Screen exclusion is
// enforced twice, once by the absent config field and once by the absent code
// path.
//
// tools is the Act leg's tool authority — spec.Requires.Grant against the
// active forge (ADR 0031 decision 4), computed once at construction (the repo
// precedent: a pure function of config the caller already resolved, not
// re-derived per fire). It rides every Request this species issues. AIScreen
// again has no such parameter: a Screen run is always toolless.
//
// sink is required, not optional (ADR 0028): an AI species accounts for itself,
// so there is no way to construct one with nowhere to put its account. The
// obligation binds the SPECIES, never the kind — hook.Notifier is still one
// method, so a non-AI Notifier owes no transcript.
func NewAINotifier(spec hook.Spec, repo, workDir string, tools []string, agent Agent, sink Transcriber) *AINotifier {
	return &AINotifier{spec: spec, repo: repo, workDir: workDir, tools: tools, agent: agent, sink: sink}
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
		WorkDir:      n.workDir,
		Repo:         n.repo,
		Number:       pr.Number,
		Title:        pr.Title,
		Author:       pr.Author,
		URL:          pr.URL,
		HeadSHA:      pr.HeadSHA,
		Tools:        n.tools,
	})
	// The transcript is the agent's account of what it did — recorded for the
	// operator, never parsed, never acted on (ADR 0023: no verdict extraction
	// on the Notifier side). It goes to the sink and nowhere else: by the time
	// this runs the review is already posted, so a sink that fails changes
	// nothing here (ADR 0028).
	//
	// Recorded BEFORE the error is judged, because a failed run is the one most
	// worth having: an agent holding gh authority may have posted its review
	// and then exited non-zero, and at-most-once means no later cycle re-runs
	// it. The row-iff-text rule suppresses the runs that genuinely said nothing.
	transcribe(n.sink, kindNotifier, n.spec, pr, transcript)
	if err != nil {
		return err
	}
	return nil
}
