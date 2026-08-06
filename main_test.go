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

// The shipped example hook config must load through the real hook loader
// (drift guard for field renames), and the security-screen prompt it
// references must exist, signed off by the operator (#40).
func TestExampleHooksConfigLoads(t *testing.T) {
	data, err := os.ReadFile("examples/hooks.yaml")
	require.NoError(t, err)
	// Load self-heals missing Ids into the file, so load a copy — the
	// committed example must stay Id-less (the intended first-run UX).
	path := filepath.Join(t.TempDir(), "hooks.yaml")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Screens, 1)
	require.Equal(t, "claude", cfg.Screens[0].Harness)
	require.Equal(t, ".config/security-screen-prompt.md", cfg.Screens[0].PromptFile)
	require.NotEmpty(t, cfg.Screens[0].ID, "boot must self-heal an Id into the entry")

	// The example Notifier entry: the Go review-assist, attached to
	// queue_entered (the review-assist's home, ADR 0021) and shipped disabled
	// — enabling it is the operator's explicit act, after reviewing the
	// prompt it references.
	require.Len(t, cfg.Notifiers, 1)
	n := cfg.Notifiers[0]
	require.Equal(t, "claude", n.Harness)
	require.Equal(t, hook.QueueEntered, n.Point)
	require.Equal(t, ".config/go-review-prompt.md", n.PromptFile)
	require.False(t, n.Enabled, "the example ships opt-in: disabled until the operator reviews and flips it")
	require.NotEmpty(t, n.ID, "boot must self-heal an Id into the entry")
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

func TestSecurityScreenPromptIsSignedOff(t *testing.T) {
	data, err := os.ReadFile("examples/security-screen-prompt.md")
	require.NoError(t, err)
	require.NotContains(t, string(data), "DRAFT", "the operator signed off in #40 — the pending-review marker is gone")
	require.Contains(t, string(data), "signed off", "the sign-off provenance stays recorded in the header")
	require.Contains(t, string(data), "untrusted", "the prompt must remind the screen the diff is untrusted data")
}
