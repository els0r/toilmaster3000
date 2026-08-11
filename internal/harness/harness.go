package harness

import (
	"context"
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
// call — fetch the PR's diff, compose the prompt, run the harness, and return
// what the harness said. An error is a failed attempt (the caller records it on
// the 3-strikes path, ADR 0022). Two MVP adapters, claude and copilot (ADR
// 0024); each further adapter (OpenCode) is one implementation here plus its
// allowlist entry in the hook validator.
//
// It returns the run's result TEXT, not a verdict: extracting one is the Screen
// species' job, not the harness's (ADR 0028). Each adapter still normalises its
// own CLI's output into result text — claude decodes its JSON envelope, copilot
// hands back silent-mode stdout — and stops there. Pulling extraction up buys
// the transcript: a run that produced text and then failed to yield a verdict
// keeps its account of itself, and ExtractVerdictText, always the
// harness-neutral half, now has exactly one caller instead of one per adapter.
//
// Text and error are NOT exclusive. A CLI that printed its whole answer and
// then exited non-zero, or was killed mid-sentence by the hook's timeout,
// returns both: the text it managed, and the failure. Callers transcribe what
// came back before they judge the error — a failed attempt that spoke is
// exactly the run whose account is worth keeping. An empty transcript with an
// error is a run that said nothing.
type Adapter interface {
	Screen(ctx context.Context, req Request) (transcript string, err error)
}

// Agent is the harness seam's side-effect half — the Notifier sibling of
// Adapter (ADR 0023): one headless AI invocation run for its ACTIONS, not its
// answer. Unlike Screen, nothing is extracted from what comes back: the agent
// itself holds the gh authority — it fetches the PR's diff and posts its review
// comment / requests changes as the runtime identity — and its authority
// ceiling (never approve, never merge) is prompt-enforced, because tm3k cannot
// compel an agent holding gh auth (ADR 0023).
//
// Both halves return a transcript, and that convergence is the point: a harness
// run IS a transcript on either leg, and what a species does with it afterwards
// — extract a verdict, or nothing at all — is species policy (ADR 0028). They
// stay two interfaces regardless, because the authority differs: a screen run is
// toolless, an act run carries gh. A screening-only adapter stays a one-method
// implementation, and fakes script exactly the half they exercise.
//
// Text and error are not exclusive here either, and it matters more on this
// leg: an agent holding gh authority may have posted its review and THEN failed,
// so the run that errored is the run whose account the operator most needs.
type Agent interface {
	Act(ctx context.Context, req Request) (transcript string, err error)
}
