package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	json "github.com/goccy/go-json"
)

const (
	openCodeScreenAgent   = "tm3k-screen"
	openCodeNotifierAgent = "tm3k-notifier"
)

// openCodePermission preserves JSON member order. OpenCode applies its
// wildcard rules in source order, so the broad deny must precede the notifier's
// gh exception instead of relying on map iteration order.
type openCodePermission struct {
	All   string                  `json:"*"`
	Glob  string                  `json:"glob,omitempty"`
	Grep  string                  `json:"grep,omitempty"`
	List  string                  `json:"list,omitempty"`
	Read  string                  `json:"read,omitempty"`
	Skill string                  `json:"skill,omitempty"`
	Bash  *openCodeBashPermission `json:"bash,omitempty"`
}

// openCodeBashPermission is the notifier's bash permission list: the broad
// deny always comes first (the type's own doc above), then one
// "<tool> *": "allow" entry per granted tool — ADR 0031 decision 4's
// additive grant (the active forge's CLI plus declared Requires.Tools),
// delivered here as the actual tool authority rather than merely boot-
// checked. A dedicated MarshalJSON keeps the order explicit: a plain
// map[string]any would let Go's (or goccy's) key-sorting decide it, and nothing
// here should depend on tool names sorting after "*".
type openCodeBashPermission struct {
	tools []string
}

// MarshalJSON writes {"*":"deny","<tool> *":"allow",...} in tools order.
// Absent tools (nil/empty) still yields the deny-only object as valid JSON.
func (p openCodeBashPermission) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`{"*":"deny"`)
	for _, tool := range p.tools {
		key, err := json.Marshal(tool + " *")
		if err != nil {
			return nil, fmt.Errorf("marshal bash permission key %q: %w", tool, err)
		}
		buf.WriteByte(',')
		buf.Write(key)
		buf.WriteString(`:"allow"`)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// OpenCode is the opencode harness adapter. It reuses the operator's normal
// OpenCode provider and authentication setup, but each run overlays a tm3k
// agent profile so a Screen is tool-free and a Notifier has only the gh action
// channel (ADR 0029).
type OpenCode struct {
	// fetchDiff, invoke, and act are the process seams, injectable in tests so
	// the real gh/opencode CLIs never execute there. Production wiring uses
	// ghPRDiff, openCodeInvoke, and openCodeActInvoke. The two invocation legs
	// carry distinct OpenCode agent profiles: a Screen judges tm3k's supplied
	// diff, while an Act run holds the gh action channel to publish its review.
	fetchDiff func(ctx context.Context, repo string, number int) (string, error)
	invoke    func(ctx context.Context, model, prompt, workDir string) ([]byte, error)
	act       func(ctx context.Context, model, prompt, workDir string, tools []string) ([]byte, error)
}

// NewOpenCode returns the production OpenCode adapter, shelling out to the
// real gh and opencode CLIs.
func NewOpenCode() *OpenCode {
	return &OpenCode{fetchDiff: ghPRDiff, invoke: openCodeInvoke, act: openCodeActInvoke}
}

// Screen runs one screen pass for the PR: diff -> prompt -> opencode -> the
// run's result text. The caller's context bounds the whole run (the Screener
// applies the hook's Timeout); both child processes are killed on cancellation.
//
// The adapter stops at the text and does not extract a verdict — AIScreen does
// that, so a run whose text yields no verdict still has its transcript recorded
// (ADR 0028). Default output mode writes only completed response text to
// non-terminal stdout, so like copilot there is no envelope to unwrap first.
func (o *OpenCode) Screen(ctx context.Context, req Request) (string, error) {
	diff, err := o.fetchDiff(ctx, req.Repo, req.Number)
	if err != nil {
		return "", fmt.Errorf("fetch diff for %s#%d: %w", req.Repo, req.Number, err)
	}
	out, err := o.invoke(ctx, req.Model, ComposePrompt(req, diff), req.WorkDir)
	if err != nil {
		return salvage(openCodeText(out)), err
	}
	return openCodeText(out)
}

// Act runs one side-effecting agent pass for the PR. The OpenCode notifier
// profile permits the gh CLI as its one action channel; restricting individual
// gh verbs is not sound because OpenCode's bash permissions match shell text,
// not parsed argv. The composed prompt therefore retains the authority ceiling
// (never approve, never merge) as the control, as it does for every adapter.
// The response text comes back as the transcript for the species to record;
// nothing is extracted from it.
func (o *OpenCode) Act(ctx context.Context, req Request) (string, error) {
	out, err := o.act(ctx, req.Model, ComposeNotifyPrompt(req), req.WorkDir, req.Tools)
	if err != nil {
		return salvage(openCodeText(out)), err
	}
	return openCodeText(out)
}

// openCodeText normalises one opencode run's stdout into the run's result
// text — the opencode twin of copilotText, shared by both legs for the reason
// one is (ADR 0014): a normalisation living in two places grows a second
// failure shape in only one of them. Default non-terminal output is exactly the
// completed response, so there is nothing to unwrap and one thing to check:
// that the run said anything at all. A silent run is a failed attempt, never an
// empty transcript.
func openCodeText(out []byte) (string, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return "", errors.New("empty opencode output")
	}
	return string(out), nil
}

// openCodeInvoke is the Screen production leg. The tm3k-screen agent denies
// every OpenCode tool, so it can obtain PR content only from the diff carried
// in its prompt. Trusted inherited instructions still shape the model run.
// tools is always nil here: a Screen run never carries one (Request.Tools is
// never populated for the Screen leg, the same exclusion AIScreen enforces
// on WorkDir).
func openCodeInvoke(ctx context.Context, model, prompt, workDir string) ([]byte, error) {
	agent, err := newOpenCodeAgentName(openCodeScreenAgent)
	if err != nil {
		return nil, err
	}
	return runOpenCode(ctx, model, prompt, workDir, agent, nil)
}

// openCodeActInvoke is openCodeInvoke's side-effecting sibling. The
// tm3k-notifier agent permits tools' granted binaries and nothing else that
// can act — the active forge's CLI plus whatever Requires.Tools declares
// (ADR 0031 decision 4), delivered to the actual bash permission rather than
// merely boot-checked.
func openCodeActInvoke(ctx context.Context, model, prompt, workDir string, tools []string) ([]byte, error) {
	agent, err := newOpenCodeAgentName(openCodeNotifierAgent)
	if err != nil {
		return nil, err
	}
	return runOpenCode(ctx, model, prompt, workDir, agent, tools)
}

// newOpenCodeAgentName returns a run-private agent name. OpenCode deep-merges
// agent definitions from configuration layers, so a fixed reserved name could
// retain fields an operator had configured under the same name. The random
// suffix makes the tm3k profile a complete fresh definition while preserving
// every unrelated trusted configuration entry.
func newOpenCodeAgentName(kind string) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate OpenCode agent name: %w", err)
	}
	return kind + "-" + hex.EncodeToString(suffix[:]), nil
}

// openCodeCmd builds one headless OpenCode invocation without running it. The
// composed prompt rides stdin, which OpenCode appends to the run message and
// keeps a full PR diff out of argv. Default output mode writes only completed
// response text to non-terminal stdout, the stable surface passed to the
// structural verdict extractor.
//
// The inherited inline configuration is trusted operator setup. tm3k preserves
// it, replacing only its reserved agent and the run-local no-side-effect
// settings. WorkDir is the process cwd and direct-tool workspace, not a general
// sandbox: trusted global configuration and instructions can still influence
// the run.
func openCodeCmd(ctx context.Context, model, prompt, workDir, agent string, tools []string) (*exec.Cmd, error) {
	config, err := openCodeConfig(os.Getenv("OPENCODE_CONFIG_CONTENT"), agent, tools)
	if err != nil {
		return nil, err
	}
	pwd, err := openCodePWD(workDir)
	if err != nil {
		return nil, err
	}

	args := []string{"run", "--agent", agent}
	if model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, "opencode", args...)
	cmd.Dir = workDir
	cmd.Env = setEnv(os.Environ(), "OPENCODE_CONFIG_CONTENT", config)
	cmd.Env = setEnv(cmd.Env, "OPENCODE_AUTO_SHARE", "false")
	cmd.Env = setEnv(cmd.Env, "OPENCODE_DISABLE_AUTOUPDATE", "true")
	cmd.Env = setEnv(cmd.Env, "OPENCODE_DISABLE_LSP_DOWNLOAD", "true")
	cmd.Env = setEnv(cmd.Env, "PWD", pwd)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.WaitDelay = cliWaitDelay
	configureOpenCodeProcess(cmd)
	return cmd, nil
}

// openCodePWD returns the directory OpenCode must treat as its project root.
// OpenCode prefers PWD over process.cwd(), so retaining tm3k's parent PWD would
// bypass an anchored Notifier's WorkDir.
func openCodePWD(workDir string) (string, error) {
	if workDir != "" {
		return workDir, nil
	}
	pwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve OpenCode working directory: %w", err)
	}
	return pwd, nil
}

// openCodeConfig overlays one run-private tm3k agent onto the inherited inline
// config. OpenCode merges agent definitions deeply across layers, so a unique
// name avoids retaining an operator-defined field from an identically named
// agent while preserving provider and authentication configuration.
func openCodeConfig(inherited, agent string, tools []string) (string, error) {
	config := map[string]any{}
	if inherited != "" {
		if err := json.Unmarshal([]byte(inherited), &config); err != nil {
			return "", fmt.Errorf("decode inherited OPENCODE_CONFIG_CONTENT: %w", err)
		}
		if config == nil {
			return "", errors.New("inherited OPENCODE_CONFIG_CONTENT must be an object")
		}
	}

	agents, err := openCodeAgents(config)
	if err != nil {
		return "", err
	}
	agents[agent] = openCodeAgent(agent, tools)
	config["agent"] = agents
	config["autoupdate"] = false
	config["compaction"] = map[string]any{"auto": false}
	config["default_agent"] = agent
	config["formatter"] = false
	config["lsp"] = false
	config["share"] = "disabled"
	config["snapshot"] = false

	out, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode tm3k OpenCode profile: %w", err)
	}
	return string(out), nil
}

// openCodeAgents returns the inherited agent registry, refusing an invalid
// shape rather than silently dropping the operator's configuration.
func openCodeAgents(config map[string]any) (map[string]any, error) {
	raw, ok := config["agent"]
	if !ok {
		return map[string]any{}, nil
	}
	agents, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("inherited OPENCODE_CONFIG_CONTENT agent must be an object")
	}
	return agents, nil
}

// openCodeAgent returns the full replacement definition for one tm3k-reserved
// agent. The wildcard denies custom/MCP tools too. Notifier bash permission is
// deliberately the tools' full CLIs: OpenCode's shell-text matcher cannot
// enforce a safe subset of any one tool's verbs (ADR 0029), and WHICH tools
// are available at all is the part ADR 0031 decision 5 actually enforces.
// tools is unused on the Screen branch (openCodeInvoke always passes nil):
// a Screen carries no Bash permission regardless.
func openCodeAgent(name string, tools []string) map[string]any {
	permission := openCodePermission{All: "deny"}
	if strings.HasPrefix(name, openCodeNotifierAgent+"-") || name == openCodeNotifierAgent {
		permission = openCodePermission{
			All:   "deny",
			Glob:  "allow",
			Grep:  "allow",
			List:  "allow",
			Read:  "allow",
			Skill: "allow",
			Bash:  &openCodeBashPermission{tools: tools},
		}
	}
	return map[string]any{
		"description": "toilmaster3000 harness run",
		"mode":        "primary",
		"permission":  permission,
	}
}

// setEnv replaces one inherited environment entry while leaving every other
// operator-provided setting intact. OpenCode flag names are uppercase, so the
// case-insensitive comparison also behaves correctly on Windows.
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, key+"="+value)
}

// runOpenCode runs one OpenCode CLI pass and returns its text-mode stdout.
//
// Stdout comes back even when the run FAILED — the claude and copilot legs'
// rule: text-mode stdout IS the answer, so a run that reviewed the diff and
// then exited non-zero loses everything it said if this returns nil. The error
// still stands; the caller decides what survives.
func runOpenCode(ctx context.Context, model, prompt, workDir, agent string, tools []string) ([]byte, error) {
	cmd, err := openCodeCmd(ctx, model, prompt, workDir, agent, tools)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("opencode run: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
