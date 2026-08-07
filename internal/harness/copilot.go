package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// Copilot is the copilot harness adapter (ADR 0024): it runs the GitHub
// Copilot CLI headless, reusing the operator's existing Copilot auth exactly
// like the claude adapter reuses claude's. One Screen call is one full run —
// fetch the PR's diff via `gh pr diff` (the sanctioned per-PR call —
// configuring a hook is the consent), compose the prompt, invoke `copilot -p`
// in silent mode, and extract the verdict structurally from the response
// text. Every failure surfaces as an error — a failed attempt for the
// caller's 3-strikes path — never a fabricated verdict.
type Copilot struct {
	// fetchDiff, invoke, and act are the process seams, injectable in tests so
	// the real gh/copilot CLIs never execute there (the claude adapter's
	// precedent). Production wiring uses ghPRDiff, copilotInvoke, and
	// copilotActInvoke. act is separate from invoke because the two runs carry
	// different authority: a screen run answers over a diff tm3k fetched, an
	// act run holds the gh tool authority to post its own side effects
	// (ADR 0023). Both carry workDir — the process's working directory, empty
	// for every unanchored run (ADR 0027) — so the wiring is assertable at this
	// seam rather than only inside an exec call no test may make.
	fetchDiff func(ctx context.Context, repo string, number int) (string, error)
	invoke    func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
	act       func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
}

// NewCopilot returns the production copilot adapter, shelling out to the real
// gh and copilot CLIs.
func NewCopilot() *Copilot {
	return &Copilot{fetchDiff: ghPRDiff, invoke: copilotInvoke, act: copilotActInvoke}
}

// Screen runs one screen pass for the PR: diff -> prompt -> copilot ->
// verdict. The caller's context bounds the whole run (the Screener applies
// the hook's Timeout); both child processes are killed on cancellation.
func (c *Copilot) Screen(ctx context.Context, req Request) (hook.Verdict, error) {
	diff, err := c.fetchDiff(ctx, req.Repo, req.Number)
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("fetch diff for %s#%d: %w", req.Repo, req.Number, err)
	}
	out, err := c.invoke(ctx, req.Model, ComposePrompt(req, diff), req.WorkDir)
	if err != nil {
		return hook.Verdict{}, err
	}
	v, err := ExtractVerdictText(string(out))
	if err != nil {
		return hook.Verdict{}, fmt.Errorf("extract verdict: %w", err)
	}
	return v, nil
}

// Act runs one side-effecting agent pass for the PR (the Agent seam): compose
// the notify prompt — carrying the never-approve/never-merge ceiling — and
// run the copilot CLI WITH the gh shell authority, so the agent fetches the
// diff and posts its review itself as the runtime identity (ADR 0023). The
// silent-mode response text is returned as the transcript for the caller to
// log; nothing is extracted from it.
func (c *Copilot) Act(ctx context.Context, req Request) (string, error) {
	out, err := c.act(ctx, req.Model, ComposeNotifyPrompt(req), req.WorkDir)
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", errors.New("empty copilot output")
	}
	return string(out), nil
}

// copilotInvoke is the screen run's production leg: a silent headless pass
// with ZERO tools visible to the model — the judge parity of claude's
// toolless screen run. Bare `--available-tools` and `--available-tools=` are
// no-ops on CLI v1.0.78 (verified empirically); a match-nothing tool name is
// the reliable spelling of "no tools" (ADR 0024).
func copilotInvoke(ctx context.Context, model, prompt, workDir string) ([]byte, error) {
	return runCopilot(ctx, model, prompt, workDir, "--available-tools=__none__")
}

// copilotActInvoke is copilotInvoke's side-effecting sibling (the Agent
// seam's production leg): the same headless run plus the gh shell authority —
// --allow-tool "shell(gh:*)" — the exact analog of the claude leg's
// Bash(gh:*). The built-in GitHub MCP stays disabled (runCopilot's base
// flags): one action channel, matching the composed prompt's gh instructions
// and posting as the same runtime identity as every other leg. The authority
// is the WHOLE gh CLI by design: the prompt ceiling is the control
// (ADR 0023).
func copilotActInvoke(ctx context.Context, model, prompt, workDir string) ([]byte, error) {
	return runCopilot(ctx, model, prompt, workDir, "--allow-tool", "shell(gh:*)")
}

// copilotCmd builds one headless copilot CLI invocation without running it.
// Unlike claude, copilot does not attach piped stdin (verified on v1.0.78), so
// the composed prompt rides argv — a prompt beyond the OS argv limit fails
// exec, a failed attempt on the 3-strikes path, never a truncated judgement
// (ADR 0024). Silent mode makes stdout exactly the agent's response text, and
// the hermetic base flags keep the run free of ambient workstation state: no
// AGENTS.md custom instructions, no built-in GitHub MCP, no mid-run
// self-update.
//
// workDir becomes the process's working directory — cwd, not copilot's -C,
// which is only a spelling of the same lever and would cost internal/harness a
// per-CLI branch. Empty leaves cmd.Dir empty, the pre-existing behaviour
// bit-for-bit. --no-custom-instructions still suppresses AGENTS.md there: the
// hermetic flags are orthogonal to skill discovery (ADR 0027 amending
// ADR 0024). The anchor is a READ GRANT bounded by that directory, so nothing
// here may ever widen it — no --add-dir, no --allow-all-paths.
//
// CommandContext kills the CLI on cancellation (the hook's timeout), so no
// run outlives its bound.
func copilotCmd(ctx context.Context, model, prompt, workDir string, extraArgs ...string) *exec.Cmd {
	args := []string{"-p", prompt, "--silent", "--no-color",
		"--no-custom-instructions", "--disable-builtin-mcps", "--no-auto-update"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, "copilot", args...)
	cmd.Dir = workDir
	cmd.WaitDelay = cliWaitDelay
	return cmd
}

// runCopilot runs one headless copilot CLI pass and returns its stdout.
func runCopilot(ctx context.Context, model, prompt, workDir string, extraArgs ...string) ([]byte, error) {
	cmd := copilotCmd(ctx, model, prompt, workDir, extraArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("copilot -p: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
