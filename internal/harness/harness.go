package harness

import (
	"context"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// Request is one screen run's input: which model to run, the operator's
// instructions (the hook's resolved Prompt), and the PR under judgement. The
// adapter fetches the diff itself from Repo/Number (ADR 0023: configuring a
// hook is the consent for the per-PR call).
type Request struct {
	Model        string
	Instructions string

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
// direction. Claude-only in MVP; each further adapter (Copilot, OpenCode) is
// one implementation here plus its allowlist entry in the hook validator.
type Adapter interface {
	Screen(ctx context.Context, req Request) (hook.Verdict, error)
}
