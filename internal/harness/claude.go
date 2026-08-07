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
	// fetchDiff, invoke, and act are the process seams, injectable in tests so
	// the real gh/claude CLIs never execute there (the internal/github fake
	// precedent). Production wiring uses ghPRDiff, claudeInvoke, and
	// claudeActInvoke. act is separate from invoke because the two runs carry
	// different authority: a screen run answers over a diff tm3k fetched, an
	// act run holds the gh tool authority to post its own side effects
	// (ADR 0023). Both carry workDir — the process's working directory, empty
	// for every unanchored run (ADR 0027) — so the wiring is assertable at this
	// seam rather than only inside an exec call no test may make.
	fetchDiff func(ctx context.Context, repo string, number int) (string, error)
	invoke    func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
	act       func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
}

// NewClaude returns the production claude adapter, shelling out to the real
// gh and claude CLIs.
func NewClaude() *Claude {
	return &Claude{fetchDiff: ghPRDiff, invoke: claudeInvoke, act: claudeActInvoke}
}

// Screen runs one screen pass for the PR: diff -> prompt -> claude -> verdict.
// The caller's context bounds the whole run (the Screener applies the hook's
// Timeout); both child processes are killed on cancellation.
func (c *Claude) Screen(ctx context.Context, req Request) (hook.Verdict, error) {
	diff, err := c.fetchDiff(ctx, req.Repo, req.Number)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("fetch diff for %s#%d: %w", req.Repo, req.Number, err)
	}
	out, err := c.invoke(ctx, req.Model, ComposePrompt(req, diff), req.WorkDir)
	if err != nil {
		return hook.Verdict{}, err
	}
	v, err := ExtractVerdict(out)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("extract verdict: %w", err)
	}
	return v, nil
}

// Act runs one side-effecting agent pass for the PR (the Agent seam): compose
// the notify prompt — carrying the never-approve/never-merge ceiling — and
// run the claude CLI WITH the gh tool authority, so the agent fetches the
// diff and posts its review itself as the runtime identity (ADR 0023). The
// decoded result text is returned as the transcript for the caller to log;
// nothing is extracted from it.
func (c *Claude) Act(ctx context.Context, req Request) (string, error) {
	out, err := c.act(ctx, req.Model, ComposeNotifyPrompt(req), req.WorkDir)
	if err != nil {
		return "", err
	}
	return resultText(out)
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

// cliWaitDelay bounds how long a harness invocation waits for output pipes
// after the context kills the CLI: both claude and copilot may spawn
// subprocesses that inherit the pipes, and without the delay a kill could
// leave Run blocked on them.
const cliWaitDelay = 10 * time.Second

// claudeInvoke is the screen run's production leg: a plain headless pass with
// NO tool authority — the screen's diff is already in the prompt, and a judge
// that can act would be more than a judge.
func claudeInvoke(ctx context.Context, model, prompt, workDir string) ([]byte, error) {
	return runClaude(ctx, model, prompt, workDir)
}

// claudeActInvoke is claudeInvoke's side-effecting sibling (the Agent seam's
// production leg): the same headless run, plus the gh tool authority —
// --allowedTools "Bash(gh:*)" — so the agent can fetch the diff and post its
// review comment itself. The authority is the WHOLE gh CLI by design: tm3k
// cannot compel an agent holding gh auth anyway, so narrowing the allowlist
// would only feign a boundary the prompt ceiling actually carries (ADR 0023).
func claudeActInvoke(ctx context.Context, model, prompt, workDir string) ([]byte, error) {
	return runClaude(ctx, model, prompt, workDir, "--allowedTools", "Bash(gh:*)")
}

// claudeCmd builds one headless claude CLI invocation without running it: the
// prompt on stdin (a composed prompt can carry a whole diff — far beyond argv
// limits), the machine-readable JSON envelope on stdout, and workDir as the
// process's working directory. An empty workDir leaves cmd.Dir empty, which
// inherits tm3k's own cwd — the pre-existing behaviour, bit-for-bit (ADR 0027).
//
// The anchor is a READ GRANT: the CLI resolves skills and file reads from the
// same root and denies reads outside it, so this directory is the run's
// ceiling. Nothing here may ever widen that — no --add-dir, no
// --allow-all-paths — and the hermetic stance of ADR 0024 is untouched:
// ambient instructions stay off, only ambient skills come on.
//
// CommandContext kills the CLI on cancellation (the hook's timeout), so no run
// outlives its bound.
func claudeCmd(ctx context.Context, model, prompt, workDir string, extraArgs ...string) *exec.Cmd {
	args := []string{"-p", "--output-format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.WaitDelay = cliWaitDelay
	return cmd
}

// runClaude runs one headless claude CLI pass and returns its stdout.
func runClaude(ctx context.Context, model, prompt, workDir string, extraArgs ...string) ([]byte, error) {
	cmd := claudeCmd(ctx, model, prompt, workDir, extraArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
