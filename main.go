// Command toilmaster3000 serves the SPA and JSON API for the PR auto-approver
// on localhost:8666. The built frontend is embedded so the tool ships as a
// single binary. It also runs the find->approve engine loop in a single
// background goroutine.
//
// Boot order (fail fast, clear message, never silently approve nothing):
//  1. preflight: gh installed+authenticated, resolve @me, bind the listen port.
//  2. SetSelfLogin on the engine so the matcher can expand @me.
//  3. serve the SPA + API on the bound listener.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/els0r/toilmaster3000/internal/settings"
)

//go:embed all:frontend/dist
var embeddedFrontend embed.FS

const (
	addr         = "localhost:8666"
	statePath    = ".state/approvals.jsonl"
	rulesPath    = ".config/rules.yaml"
	settingsPath = ".config/settings.yaml"
)

// config is the resolved startup configuration: the candidate set (repo +
// search) and the engine poll interval. It is populated from flags and the
// TM3K_* environment via viper, so a deployment can wire its own repo without
// recompiling.
type config struct {
	repo         string
	search       string
	pollInterval time.Duration
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// cobra has already printed the error; exit non-zero.
		os.Exit(1)
	}
}

// newRootCmd builds the toilmaster3000 command. Flags and the TM3K_* env share
// one viper instance, so each setting can come from either source — a flag, if
// given, overrides its env var. repo and search have no default and are
// required: a public build wired to no repo must fail fast, never silently
// approve nothing.
func newRootCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "toilmaster3000",
		Short: "PR auto-approver: serves the SPA + API and runs the find->approve engine loop",
		Args:  cobra.NoArgs,
		// On error, print the message (not the full usage wall) and exit non-zero.
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			v.SetEnvPrefix("TM3K")
			// poll-interval -> TM3K_POLL_INTERVAL (dashes are not valid in env names).
			v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
			v.AutomaticEnv()
			return v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), config{
				repo:         v.GetString("repo"),
				search:       v.GetString("search"),
				pollInterval: v.GetDuration("poll-interval"),
			})
		},
	}

	cmd.Flags().String("repo", "", "owner/name of the repository whose PRs are candidates (env TM3K_REPO) [required]")
	cmd.Flags().String("search", "", "`gh pr list --search` query selecting candidate PRs, e.g. \"is:open team-review-requested:owner/team\" (env TM3K_SEARCH) [required]")
	cmd.Flags().Duration("poll-interval", engine.DefaultPollInterval, "wait between find->approve cycles (e.g. 5m, 90s); must be at least 1m")
	return cmd
}

// run is the entrypoint body: validate config, wire the engine and server, run
// the preflight gates, and serve. It returns an error rather than exiting so
// cobra prints a clean message and main controls the exit code.
//
// Boot order (fail fast, clear message, never silently approve nothing):
//  1. validate required config (repo, search, poll interval).
//  2. preflight: gh installed+authenticated, resolve @me, bind the listen port.
//  3. SetSelfLogin on the engine so the matcher can expand @me.
//  4. serve the SPA + API on the bound listener.
func run(ctx context.Context, cfg config) error {
	// Configure the process-wide structured logger first, so every component
	// (including the engine, which captures slog.Default() at construction)
	// logs through the same handler.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// repo and search are the candidate set; without them the tool has nothing to
	// approve. Reject empties up front with a message that names both sources.
	if cfg.repo == "" {
		return fmt.Errorf("repo is required: set --repo or TM3K_REPO (e.g. owner/name)")
	}
	if cfg.search == "" {
		return fmt.Errorf("search is required: set --search or TM3K_SEARCH (e.g. \"is:open team-review-requested:owner/team\")")
	}
	// Reject a sub-minute poll interval up front: anything under a minute hammers
	// the GitHub API for no benefit. Fail fast with a clear message.
	if cfg.pollInterval < engine.MinPollInterval {
		return fmt.Errorf("invalid --poll-interval: %s is too aggressive; must be at least %s", cfg.pollInterval, engine.MinPollInterval)
	}

	spa, err := fs.Sub(embeddedFrontend, "frontend/dist")
	if err != nil {
		return fmt.Errorf("locate embedded frontend: %w", err)
	}

	client := github.NewCLI(cfg.repo, cfg.search)

	// Load (or seed on first run) the rule set the engine matches each cycle.
	rules, err := rule.NewStore(rulesPath)
	if err != nil {
		return fmt.Errorf("build rule store: %w", err)
	}

	// Load (or seed on first run) the analytics assumption constants (ADR 0010).
	set, err := settings.NewStore(settingsPath)
	if err != nil {
		return fmt.Errorf("build settings store: %w", err)
	}

	eng, err := engine.New(client, statePath, rules)
	if err != nil {
		return fmt.Errorf("build engine: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Preflight: fail fast with a clear message before serving or approving.
	if err := checkGhAuth(ctx, execGhAuthStatus); err != nil {
		return fmt.Errorf("preflight: gh auth: %w", err)
	}
	selfLogin, err := resolveSelfLogin(ctx, client)
	if err != nil {
		return fmt.Errorf("preflight: resolve @me: %w", err)
	}
	ln, err := listen(addr)
	if err != nil {
		return fmt.Errorf("preflight: bind listen port: %w", err)
	}
	defer ln.Close()

	// The matcher (Slice 4) reads the resolved @me token off the engine.
	eng.SetSelfLogin(selfLogin)
	eng.SetPollInterval(cfg.pollInterval)

	go eng.Run(ctx)

	handler, err := server.New(spa, eng, rules, set, cfg.search)
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	slog.Info("toilmaster3000 listening", "addr", addr, "url", "http://"+addr, "repo", cfg.repo, "self_login", selfLogin, "poll_interval", eng.PollInterval())
	if err := http.Serve(ln, handler); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// checkGhAuth verifies the gh CLI is installed and authenticated. It is the
// first preflight gate: a missing or unauthenticated gh must hard-exit rather
// than let the tool run and silently approve nothing. authStatus is injected so
// the check is testable without a real gh.
func checkGhAuth(ctx context.Context, authStatus func(context.Context) error) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found on PATH: install it from https://cli.github.com and run `gh auth login`")
	}
	if err := authStatus(ctx); err != nil {
		return fmt.Errorf("gh is not authenticated (`gh auth status` failed): run `gh auth login`: %w", err)
	}
	return nil
}

// execGhAuthStatus runs `gh auth status` and reports a non-zero exit as an
// error. It is the production authStatus passed to checkGhAuth.
func execGhAuthStatus(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resolveSelfLogin resolves the @me author token once via the gh seam
// (`gh api user`). A failure here is a hard preflight error: without @me the
// matcher cannot evaluate author rules. It is testable via the Fake client.
func resolveSelfLogin(ctx context.Context, client github.GitHubClient) (string, error) {
	login, err := client.CurrentUser(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve @me via `gh api user`: %w", err)
	}
	if login == "" {
		return "", fmt.Errorf("resolve @me via `gh api user`: empty login")
	}
	return login, nil
}

// listen binds the listen port up front so a port already in use causes a clear
// startup failure instead of a silent one. Returning the listener (rather than
// using http.ListenAndServe) makes the port check deterministic and testable.
func listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind %s (is another toilmaster3000 already running?): %w", addr, err)
	}
	return ln, nil
}
