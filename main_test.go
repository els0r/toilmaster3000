package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// resolveSelfLogin resolves the @me token via the gh seam; the Fake stands in
// for `gh api user` so this is provable without a real gh or network.
func TestResolveSelfLoginSuccess(t *testing.T) {
	fake := github.NewFake()
	fake.Login = "octocat"

	login, err := resolveSelfLogin(context.Background(), fake)
	require.NoError(t, err)
	require.Equal(t, "octocat", login)
}

// A failure resolving @me is a hard preflight error (never proceed without it).
func TestResolveSelfLoginError(t *testing.T) {
	fake := github.NewFake()
	fake.CurrentUserErr = errors.New("not authenticated")

	_, err := resolveSelfLogin(context.Background(), fake)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gh api user")
}

// An empty login is rejected rather than silently accepted.
func TestResolveSelfLoginEmpty(t *testing.T) {
	fake := github.NewFake()
	fake.Login = ""

	_, err := resolveSelfLogin(context.Background(), fake)
	require.Error(t, err)
}

// An invisible repo is a hard preflight error, and the message names BOTH the
// repo and the active gh account: the per-cycle pulls go through GitHub's
// search API, which returns empty (not an error) for a repo the identity
// cannot see — without this gate a wrong active account boots fine and reports
// `ok` with zero counts forever (the silent-blindness failure mode).
func TestCheckRepoVisibleFailsWhenRepoInvisible(t *testing.T) {
	fake := github.NewFake()
	fake.RepoVisibleErr = errors.New("GraphQL: Could not resolve to a Repository")

	err := checkRepoVisible(context.Background(), fake, "acme/private", "els0r")
	require.Error(t, err)
	require.Contains(t, err.Error(), "acme/private")
	require.Contains(t, err.Error(), "els0r")
}

// A visible repo passes the gate silently — the happy path adds no friction.
func TestCheckRepoVisiblePasses(t *testing.T) {
	fake := github.NewFake()

	err := checkRepoVisible(context.Background(), fake, "acme/public", "els0r")
	require.NoError(t, err)
}

// listen binds a free port and returns a usable listener.
func TestListenBindsFreePort(t *testing.T) {
	ln, err := listen("localhost:0")
	require.NoError(t, err)
	defer ln.Close()
	require.NotNil(t, ln)
}

// A port already in use causes a clear startup failure instead of a silent one.
func TestListenPortInUse(t *testing.T) {
	// Bind a port first, then assert a second listen on the same address fails.
	first, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	defer first.Close()

	_, err = listen(first.Addr().String())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot bind")
}

// checkGhAuth surfaces a failing `gh auth status` as a clear error (the auth
// check is injected, so no real gh is needed).
func TestCheckGhAuthFailsWhenUnauthenticated(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH; LookPath gate would fire before the auth check")
	}
	err := checkGhAuth(context.Background(), func(context.Context) error {
		return errors.New("not logged in")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "gh auth login")
}

// checkGhAuth passes when gh is present and the auth status check succeeds.
func TestCheckGhAuthPasses(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH")
	}
	err := checkGhAuth(context.Background(), func(context.Context) error { return nil })
	require.NoError(t, err)
}

// checkHarnessBinaries refuses startup when an enabled hook names a harness
// whose CLI is not installed (ADR 0024): a missing binary must fail boot with
// the harness's name, not burn failed attempts one screened PR at a time.
func TestCheckHarnessBinariesMissingBinaryRefusesStartup(t *testing.T) {
	cfg := hook.Config{Screens: []hook.ScreenConfig{
		{Spec: hook.Spec{Name: "security vet", Harness: "copilot", Enabled: true}},
	}}

	err := checkHarnessBinaries(cfg, func(string) (string, error) {
		return "", exec.ErrNotFound
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "copilot")
}

// Only harnesses named by ENABLED hooks are looked up — a disabled hook's
// missing CLI must not block boot, and each distinct harness is checked once.
func TestCheckHarnessBinariesChecksOnlyEnabledHooks(t *testing.T) {
	var looked []string
	cfg := hook.Config{
		Screens: []hook.ScreenConfig{
			{Spec: hook.Spec{Name: "vet", Harness: "claude", Enabled: true}},
			{Spec: hook.Spec{Name: "vet again", Harness: "claude", Enabled: true}},
		},
		Notifiers: []hook.NotifierConfig{
			{Spec: hook.Spec{Name: "assist", Harness: "copilot", Enabled: false}},
		},
	}

	err := checkHarnessBinaries(cfg, func(name string) (string, error) {
		looked = append(looked, name)
		return "/usr/local/bin/" + name, nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, looked)
}

// No hooks, no lookups: the no-hooks boot stays bit-for-bit as before.
func TestCheckHarnessBinariesNoHooksNoLookups(t *testing.T) {
	err := checkHarnessBinaries(hook.Config{}, func(string) (string, error) {
		t.Fatal("no lookup expected")
		return "", nil
	})
	require.NoError(t, err)
}

// buildScreener wires the AI Screen species from validated hook config: zero
// configured screens must yield a nil screener — bit-for-bit today's engine
// behavior — and configured screens must yield a consulting screener.
func TestBuildScreenerNilWithoutScreens(t *testing.T) {
	s, err := buildScreener(hook.Config{}, "acme/widgets", filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	require.Nil(t, s)
}

func TestBuildScreenerNilWithNotifiersOnly(t *testing.T) {
	cfg := hook.Config{Notifiers: []hook.NotifierConfig{{
		Spec:  hook.Spec{ID: "n1", Name: "ping", Harness: "claude", Prompt: "p", Enabled: true},
		Point: hook.PostApprove,
	}}}
	s, err := buildScreener(cfg, "acme/widgets", filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	require.Nil(t, s, "notifiers alone gate nothing — screens are the only screener input")
}

func TestBuildScreenerConstructsScreensOverTheClaudeAdapter(t *testing.T) {
	cfg := hook.Config{Screens: []hook.ScreenConfig{{
		Spec: hook.Spec{ID: "s1", Name: "security", Harness: "claude", Prompt: "vet it", Enabled: true},
	}}}
	s, err := buildScreener(cfg, "acme/widgets", filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	require.NotNil(t, s)
}

// buildNotifierRunner wires the AI Notifier species from validated hook
// config (buildScreener's sibling): zero configured Notifiers must yield a
// nil runner — bit-for-bit today's engine behavior, the fired-ledger not even
// opened — and configured Notifiers must yield a firing runner.
func TestBuildNotifierRunnerNilWithoutNotifiers(t *testing.T) {
	firesPath := filepath.Join(t.TempDir(), "hookfires.jsonl")
	r, err := buildNotifierRunner(hook.Config{}, "acme/widgets", firesPath)
	require.NoError(t, err)
	require.Nil(t, r)
	require.NoFileExists(t, firesPath, "zero notifiers: the ledger is not opened, let alone written")
}

func TestBuildNotifierRunnerNilWithScreensOnly(t *testing.T) {
	cfg := hook.Config{Screens: []hook.ScreenConfig{{
		Spec: hook.Spec{ID: "s1", Name: "security", Harness: "claude", Prompt: "vet it", Enabled: true},
	}}}
	r, err := buildNotifierRunner(cfg, "acme/widgets", filepath.Join(t.TempDir(), "hookfires.jsonl"))
	require.NoError(t, err)
	require.Nil(t, r, "screens alone announce nothing — notifiers are the only runner input")
}

func TestBuildNotifierRunnerConstructsNotifiersOverTheClaudeAdapter(t *testing.T) {
	cfg := hook.Config{Notifiers: []hook.NotifierConfig{{
		Spec:  hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Prompt: "review it", Enabled: true},
		Point: hook.QueueEntered,
	}}}
	r, err := buildNotifierRunner(cfg, "acme/widgets", filepath.Join(t.TempDir(), "hookfires.jsonl"))
	require.NoError(t, err)
	require.NotNil(t, r)
}

// A Notifier's configured Paths must reach the instance the runner fires, and
// be compiled into its Scope ONCE here at boot — not re-normalised per match
// (ADR 0026). Scope lives on the instance rather than the Spec because it
// exists on this kind only. The nested-file assertion is the proof the
// gitignore normalisation ran: an unnormalised "*.go" matches main.go but not
// internal/hook/scope.go.
func TestNotifierInstancesCompilePathsIntoScope(t *testing.T) {
	cfg := hook.Config{Notifiers: []hook.NotifierConfig{
		{
			Spec:  hook.Spec{ID: "n1", Name: "go review", Harness: "claude", Prompt: "review it", Enabled: true},
			Point: hook.QueueEntered,
			Paths: []string{"*.go"},
		},
		{
			Spec:  hook.Spec{ID: "n2", Name: "cross-cutting review", Harness: "claude", Prompt: "review it", Enabled: true},
			Point: hook.QueueEntered,
		},
	}}

	instances, err := notifierInstances(cfg, "acme/widgets")
	require.NoError(t, err)
	require.Len(t, instances, 2)

	require.True(t, instances[0].Scope.Matches([]string{"internal/hook/scope.go"}, 1),
		"the configured Paths reach the instance, gitignore-normalised at load")
	require.False(t, instances[0].Scope.Matches([]string{"README.md"}, 1),
		"and gate it: a docs-only PR is out of scope")
	require.True(t, instances[1].Scope.Matches([]string{"README.md"}, 1),
		"a Notifier without Paths stays unscoped and fires on every PR")
}

// The shipped example hook config must load through the real hook loader
// (drift guard for field renames), and the security-screen prompt it
// references must exist, signed off by the operator (#40).
func TestExampleHooksConfigLoads(t *testing.T) {
	data, err := os.ReadFile("examples/hooks.yaml")
	require.NoError(t, err)

	// Load the example from the layout its own header tells the operator to
	// create: the file next to the binary, with the prompts it references
	// copied into .config/. The boot preflight now stats every ENABLED hook's
	// PromptFile (ADR 0027), so this is also the assertion that an operator who
	// followed the shipped instructions boots — and that one who skipped the
	// copy is told which hook is missing what, instead of discovering it as a
	// screen that holds every PR.
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".config", "security-screen-prompt.md"),
		[]byte("vet it"), 0o644))

	// Load self-heals missing Ids into the file, so load a copy — the
	// committed example must stay Id-less (the intended first-run UX).
	path := filepath.Join(dir, "hooks.yaml")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Screens, 1)
	require.Equal(t, "claude", cfg.Screens[0].Harness)
	require.Equal(t, ".config/security-screen-prompt.md", cfg.Screens[0].PromptFile)
	require.NotEmpty(t, cfg.Screens[0].ID, "boot must self-heal an Id into the entry")

	// The example Notifier entries: two language-keyed review-assists sharing
	// queue_entered (the review-assist's home, ADR 0021), each scoped to its
	// own language by Paths — the polyglot case scope exists for (ADR 0026),
	// demonstrated as N flat siblings rather than one composing hook. Both ship
	// disabled: enabling is the operator's explicit act, after reviewing the
	// prompt each references.
	require.Len(t, cfg.Notifiers, 2)
	for _, n := range cfg.Notifiers {
		require.Equal(t, "claude", n.Harness)
		require.Equal(t, hook.QueueEntered, n.Point)
		require.False(t, n.Enabled, "the example ships opt-in: disabled until the operator reviews and flips it")
		require.NotEmpty(t, n.ID, "boot must self-heal an Id into the entry")
		require.NotEmpty(t, n.Paths, "each language reviewer declares the scope it applies to")
	}
	// The two Notifiers also demonstrate both harness-anchoring shapes
	// (ADR 0027). The Go one is ANCHORED: an absolute WorkDir plus a one-line
	// Prompt naming the skill that lives in it — harness coupling belongs in
	// Prompt, the escape hatch, and PromptFile is not re-based against WorkDir.
	// The bash one is UNANCHORED, keeping the PromptFile shape every hooks.yaml
	// written before WorkDir existed still has.
	require.NotEmpty(t, cfg.Notifiers[0].WorkDir, "the Go review-assist demonstrates the anchored shape")
	require.True(t, filepath.IsAbs(cfg.Notifiers[0].WorkDir), "WorkDir is absolute — nothing is expanded")
	require.NotEmpty(t, cfg.Notifiers[0].Prompt, "an anchored Notifier names its skill inline")
	require.Empty(t, cfg.Notifiers[0].PromptFile)

	require.Empty(t, cfg.Notifiers[1].WorkDir, "the bash review-assist stays unanchored — both shapes are shown")
	require.Equal(t, ".config/bash-review-prompt.md", cfg.Notifiers[1].PromptFile)

	// The comment must carry what an operator cannot infer from the field name:
	// that WorkDir is a READ GRANT over that subtree, that it must point at a
	// purpose-built skills-only checkout rather than a working clone, and the
	// sparse-worktree recipe that produces one.
	doc := string(data)
	require.Contains(t, doc, "read access")
	require.Contains(t, doc, "never a working clone")
	require.Contains(t, doc, "git worktree add --detach")
	require.Contains(t, doc, "git sparse-checkout set")

	// The scopes select their own language and decline the other's PR — the
	// example must demonstrate working selection, not just carry the field.
	goScope := hook.NewScope(cfg.Notifiers[0].Paths)
	shScope := hook.NewScope(cfg.Notifiers[1].Paths)
	require.True(t, goScope.Matches([]string{"internal/hook/scope.go"}, 1))
	require.False(t, goScope.Matches([]string{"scripts/release.sh"}, 1))
	require.True(t, shScope.Matches([]string{"scripts/release.sh"}, 1))
	require.False(t, shScope.Matches([]string{"internal/hook/scope.go"}, 1))
}

// The shipped Go review-assist prompt must carry the authority ceiling and
// the untrusted-data reminder in the operator-visible instructions too (the
// composition appends tm3k's contract regardless, but the example the
// operator copies and edits must model it), and must tell the operator to
// review it before enabling.
func TestGoReviewPromptCarriesCeilingAndUntrustedReminder(t *testing.T) {
	data, err := os.ReadFile("examples/go-review-prompt.md")
	require.NoError(t, err)
	prompt := string(data)
	require.Contains(t, prompt, "review before enabling", "the example is explicit opt-in: the operator reviews it first")
	require.Contains(t, prompt, "never approve", "the ceiling is stated where the operator will edit")
	require.Contains(t, prompt, "never merge")
	require.Contains(t, prompt, "untrusted", "the prompt must remind the assist the PR content is untrusted data")
}

// The bash review-assist prompt is the Go one's sibling — the second half of
// the polyglot example — and carries the same operator-visible contract.
func TestBashReviewPromptCarriesCeilingAndUntrustedReminder(t *testing.T) {
	data, err := os.ReadFile("examples/bash-review-prompt.md")
	require.NoError(t, err)
	prompt := string(data)
	require.Contains(t, prompt, "review before enabling", "the example is explicit opt-in: the operator reviews it first")
	require.Contains(t, prompt, "never approve", "the ceiling is stated where the operator will edit")
	require.Contains(t, prompt, "never merge")
	require.Contains(t, prompt, "untrusted", "the prompt must remind the assist the PR content is untrusted data")
}

// Both review-assist prompts may delegate their criteria to Skills instead of
// restating them — one source of truth for review standards (ADR 0026). What
// the delegation depends on is the ANCHOR, not the harness: both CLIs discover
// skills from the directory their run is anchored in, so the note must send the
// operator to WorkDir (ADR 0027). It stays in the prompt file rather than
// becoming a Spec field because only the spelling is harness-specific, and the
// prompt is where harness coupling belongs.
func TestReviewPromptsSendSkillDelegationToWorkDir(t *testing.T) {
	for _, path := range []string{"examples/go-review-prompt.md", "examples/bash-review-prompt.md"} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		prompt := string(data)
		require.Contains(t, prompt, "Skill", path+": the delegation option is documented where the operator edits")
		require.Contains(t, prompt, "WorkDir", path+": and names the anchor it actually depends on")
		// The corrected claim, pinned: delegation is not a claude-only property.
		// ADR 0027 records that copilot v1.0.78 resolves skills too; this test
		// previously enforced the false wording it replaced.
		require.NotContains(t, prompt, "claude-harness-specific",
			path+": skill delegation is not harness-specific — only its spelling is (ADR 0027)")
	}
}

func TestSecurityScreenPromptIsSignedOff(t *testing.T) {
	data, err := os.ReadFile("examples/security-screen-prompt.md")
	require.NoError(t, err)
	require.NotContains(t, string(data), "DRAFT", "the operator signed off in #40 — the pending-review marker is gone")
	require.Contains(t, string(data), "signed off", "the sign-off provenance stays recorded in the header")
	require.Contains(t, string(data), "untrusted", "the prompt must remind the screen the diff is untrusted data")
}
