package harness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// Claude is the claude harness adapter (ADR 0023): it runs the claude CLI
// headless, reusing the operator's existing auth exactly like the gh seam
// does. One Screen call is one full run — fetch the PR's diff via
// `gh pr diff` (no checkout; the sanctioned per-PR call — configuring a hook
// is the consent), compose the prompt, invoke `claude -p` with the
// machine-readable output mode, and extract the verdict structurally. Every
// failure surfaces as an error — a failed attempt for the caller's 3-strikes
// path — never a fabricated verdict.
type Claude struct {
	// fetchDiff and invoke are the two process seams, injectable in tests so
	// the real gh/claude CLIs never execute there (the internal/github fake
	// precedent). Production wiring uses ghPRDiff and claudeInvoke.
	fetchDiff func(ctx context.Context, repo string, number int) (string, error)
	invoke    func(ctx context.Context, model, prompt string) ([]byte, error)
}

// NewClaude returns the production claude adapter, shelling out to the real
// gh and claude CLIs.
func NewClaude() *Claude {
	return &Claude{fetchDiff: ghPRDiff, invoke: claudeInvoke}
}

// Screen runs one screen pass for the PR: diff -> prompt -> claude -> verdict.
// The caller's context bounds the whole run (the Screener applies the hook's
// Timeout); both child processes are killed on cancellation.
func (c *Claude) Screen(ctx context.Context, req Request) (hook.Verdict, error) {
	diff, err := c.fetchDiff(ctx, req.Repo, req.Number)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("fetch diff for %s#%d: %w", req.Repo, req.Number, err)
	}
	out, err := c.invoke(ctx, req.Model, ComposePrompt(req, diff))
	if err != nil {
		return hook.Verdict{}, err
	}
	v, err := ExtractVerdict(out)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("extract verdict: %w", err)
	}
	return v, nil
}

// ghPRDiff fetches one PR's unified diff via a single `gh pr diff` call —
// no checkout, reusing the gh auth (the internal/github command style).
// CommandContext kills gh when the run's context ends, so a timed-out screen
// leaks no process.
func ghPRDiff(ctx context.Context, repo string, number int) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "diff", strconv.Itoa(number), "--repo", repo)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr diff %d: %w: %s", number, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// claudeWaitDelay bounds how long claudeInvoke waits for output pipes after
// the context kills the CLI: claude may spawn subprocesses that inherit the
// pipes, and without the delay a kill could leave Run blocked on them.
const claudeWaitDelay = 10 * time.Second

// claudeInvoke runs one headless claude CLI pass: the prompt on stdin (a
// composed prompt carries a whole diff — far beyond argv limits), the result
// as the machine-readable JSON envelope on stdout that ExtractVerdict
// decodes. CommandContext kills the CLI on cancellation (the screen's
// timeout), so no run outlives its bound.
func claudeInvoke(ctx context.Context, model, prompt string) ([]byte, error) {
	args := []string{"-p", "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = claudeWaitDelay
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
