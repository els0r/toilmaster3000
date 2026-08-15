package harness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Claude is the claude harness adapter (ADR 0023): it runs the claude CLI
// headless, reusing the operator's existing auth exactly like the gh seam
// does. One Screen call is one full run — fetch the PR's diff via
// `gh pr diff` (no checkout; the sanctioned per-PR call — configuring a hook
// is the consent), compose the prompt, invoke `claude -p` with the
// machine-readable output mode, and unwrap the result envelope. Both legs end
// at the run's result text; judging it is the species' job (ADR 0028). Every
// failure surfaces as an error — a failed attempt for the caller's 3-strikes
// path — never a fabricated verdict.
type Claude struct {
	// fetchDiff, invoke, and act are the process seams, injectable in tests so
	// the real gh/claude CLIs never execute there (the internal/forge fake
	// precedent). Production wiring uses ghPRDiff, claudeInvoke, and
	// claudeActInvoke. act is separate from invoke because the two runs carry
	// different authority: a screen run answers over a diff tm3k fetched, an
	// act run holds the gh tool authority to post its own side effects
	// (ADR 0023). Both carry workDir — the process's working directory, empty
	// for every unanchored run (ADR 0027) — so the wiring is assertable at this
	// seam rather than only inside an exec call no test may make.
	fetchDiff func(ctx context.Context, repo string, number int) (string, error)
	invoke    func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
	act       func(ctx context.Context, model, prompt, workDir string, tools []string) ([]byte, error)
}

// NewClaude returns the production claude adapter, shelling out to the real
// gh and claude CLIs.
func NewClaude() *Claude {
	return &Claude{fetchDiff: ghPRDiff, invoke: claudeInvoke, act: claudeActInvoke}
}

// Screen runs one screen pass for the PR: diff -> prompt -> claude -> the run's
// result text. The caller's context bounds the whole run (the Screener applies
// the hook's Timeout); both child processes are killed on cancellation.
//
// The adapter stops at the text and does not extract a verdict — AIScreen does
// that, so a run whose text yields no verdict still has its transcript recorded
// (ADR 0028). Decoding the JSON envelope stays here: that part is claude's, not
// the harness-neutral half's.
func (c *Claude) Screen(ctx context.Context, req Request) (string, error) {
	diff, err := c.fetchDiff(ctx, req.Repo, req.Number)
	if err != nil {
		return "", fmt.Errorf("fetch diff for %s#%d: %w", req.Repo, req.Number, err)
	}
	out, err := c.invoke(ctx, req.Model, ComposePrompt(req, diff), req.WorkDir)
	if err != nil {
		return salvage(resultText(out)), err
	}
	return resultText(out)
}

// Act runs one side-effecting agent pass for the PR (the Agent seam): compose
// the notify prompt — carrying the never-approve/never-merge ceiling — and
// run the claude CLI WITH the gh tool authority, so the agent fetches the
// diff and posts its review itself as the runtime identity (ADR 0023). The
// decoded result text is returned as the transcript for the species to record;
// nothing is extracted from it.
func (c *Claude) Act(ctx context.Context, req Request) (string, error) {
	out, err := c.act(ctx, req.Model, ComposeNotifyPrompt(req), req.WorkDir, req.Tools)
	if err != nil {
		return salvage(resultText(out)), err
	}
	return resultText(out)
}

// ghPRDiff fetches one PR's unified diff via a single `gh pr diff` call —
// no checkout, reusing the gh auth (the internal/forge/github command style).
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
// production leg): the same headless run, plus the tool authority tools
// grants — one --allowedTools flag naming a Bash(<tool>:*) pattern per
// granted tool, so the agent can fetch the diff and post its review comment
// itself (ADR 0023), and use whatever else the hook's Requires.Tools declared
// (ADR 0031 decision 4). The authority is each tool's WHOLE CLI by design:
// tm3k cannot compel an agent holding auth anyway, so narrowing the allowlist
// within one tool would only feign a boundary the prompt ceiling actually
// carries (ADR 0023) — selecting WHICH tools are available is the part that
// is actually enforced (ADR 0031 decision 5).
func claudeActInvoke(ctx context.Context, model, prompt, workDir string, tools []string) ([]byte, error) {
	return runClaude(ctx, model, prompt, workDir, claudeAllowedToolsArgs(tools)...)
}

// claudeAllowedToolsArgs turns a hook's granted tools into the --allowedTools
// flag pair(s) claude's CLI expects: one flag occurrence naming one
// Bash(<tool>:*) pattern per tool. Absent tools (the pre-ADR-0031 case)
// produces nothing — the caller supplies ["gh"] via Requires.Grant when a
// hook declares nothing, so this is what makes "today's flags bit-for-bit"
// true without this function hard-coding "gh" itself.
func claudeAllowedToolsArgs(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	args := make([]string, 0, len(tools)+1)
	args = append(args, "--allowedTools")
	for _, tool := range tools {
		args = append(args, fmt.Sprintf("Bash(%s:*)", tool))
	}
	return args
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
//
// Stdout comes back even when the run FAILED. A CLI that printed its whole
// answer and then exited non-zero — or was killed by the hook's timeout mid-
// sentence — has already produced the evidence, and discarding it here is the
// evidence loss ADR 0028 exists to end, one layer below where it was fixed.
// The error still stands; the caller decides what, if anything, survives.
func runClaude(ctx context.Context, model, prompt, workDir string, extraArgs ...string) ([]byte, error) {
	cmd := claudeCmd(ctx, model, prompt, workDir, extraArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("claude -p: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
