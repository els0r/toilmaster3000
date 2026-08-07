package harness

import (
	"context"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// Request is one harness run's input: which model to run, the operator's
// instructions (the hook's resolved Prompt), and the PR under judgement. The
// adapter fetches the diff itself from Repo/Number (ADR 0023: configuring a
// hook is the consent for the per-PR call).
//
// WorkDir is the harness process's working directory, and the empty string is
// the unanchored run every leg made before it existed (ADR 0027). It is set by
// AINotifier and by nothing else: AIScreen never populates it, so a Screen has
// neither a config field to declare an anchor nor a code path that would carry
// one. Because discovery and reads resolve from the same root on both CLIs, it
// is also the run's read ceiling — which is why tm3k never widens it with
// --add-dir or --allow-all-paths.
type Request struct {
	Model        string
	Instructions string
	WorkDir      string

	Repo    string // owner/name
	Number  int
	Title   string
	Author  string
	URL     string
	HeadSHA string
}

// Adapter is the harness seam (ADR 0023): one headless AI invocation per
// call — fetch the PR's diff, compose the prompt, run the harness, extract
// the verdict structurally. An error is a failed attempt (the caller records
// it on the 3-strikes path, ADR 0022), never a fabricated verdict in either
// direction. Two MVP adapters, claude and copilot (ADR 0024); each further
// adapter (OpenCode) is one implementation here plus its allowlist entry in
// the hook validator.
type Adapter interface {
	Screen(ctx context.Context, req Request) (hook.Verdict, error)
}

// Agent is the harness seam's side-effect half — the Notifier sibling of
// Adapter (ADR 0023): one headless AI invocation run for its ACTIONS, not its
// answer. Unlike Screen, nothing is extracted and nothing comes back to act
// on: the agent itself holds the gh authority — it fetches the PR's diff and
// posts its review comment / requests changes as the runtime identity — and
// its authority ceiling (never approve, never merge) is prompt-enforced,
// because tm3k cannot compel an agent holding gh auth (ADR 0023). The
// returned transcript is logged by the caller and otherwise ignored. A
// separate interface, not a second Adapter method: a screening-only adapter
// stays a one-method implementation, and fakes script exactly the half they
// exercise.
type Agent interface {
	Act(ctx context.Context, req Request) (transcript string, err error)
}
