// Package hook is the hook subsystem: user-configured actions tm3k runs at
// named hook points (ADR 0021), declared in .config/hooks.yaml (ADR 0023).
package hook

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Spec is the declarative field set shared by both hook kinds — the AI-species
// declaration of ADR 0023 plus hook identity. Its on-disk form is PascalCase
// YAML (.config/hooks.yaml, matching rules.yaml/settings.yaml); there is
// deliberately no command string anywhere.
//
// ID is a stable, generated identifier independent of the user-editable Name
// (the rules-store idiom): all later hook state (verdict store, fired-ledger)
// keys on it, so renaming a hook never orphans its state. Exactly one of
// Prompt (inline instructions) and PromptFile (a path read at run time) must
// be set. Timeout bounds one harness run and is the only tunable (ADR 0022);
// absent means DefaultTimeout.
type Spec struct {
	ID         string   `yaml:"Id"`
	Name       string   `yaml:"Name"`
	Harness    string   `yaml:"Harness"`
	Model      string   `yaml:"Model,omitempty"`
	Prompt     string   `yaml:"Prompt,omitempty"`
	PromptFile string   `yaml:"PromptFile,omitempty"`
	Timeout    Duration `yaml:"Timeout,omitempty"`
	Enabled    bool     `yaml:"Enabled"`
	// Requires declares the preconditions this hook needs in order to run —
	// which Forge it targets and which extra Tools it needs on PATH — and is
	// hard-checked at boot (ADR 0031). Optional; an absent Requires changes no
	// existing behaviour: the hook applies to every instance and grants
	// nothing beyond the active forge's own CLI.
	Requires Requires `yaml:"Requires,omitempty"`
}

// Requires is a hook's declared preconditions (ADR 0031): tm3k cannot inspect
// what a hook actually does — with WorkDir the real behaviour lives in a
// skill file — so the operator asserts it and tm3k enforces the assertion.
// Both fields are optional and hand-edited like the rest of hooks.yaml; there
// is deliberately no predicate expression language, just these two concrete
// facts.
type Requires struct {
	// Forge names the forge this hook works against ("github", "gitlab").
	// Absent means forge-agnostic: the hook applies wherever it is enabled.
	Forge Forge `yaml:"Forge,omitempty"`
	// Tools lists additional binaries that must be on PATH, beyond the active
	// forge's own CLI. Additive to the tool grant, never a replacement — an
	// incomplete list can never strip authority a hook already had.
	Tools []string `yaml:"Tools,omitempty"`
}

// DefaultTimeout bounds a single harness run when a hook sets no Timeout —
// generous because an AI screen legitimately takes minutes (ADR 0022). It is
// the only hook tunable.
const DefaultTimeout = 10 * time.Minute

// TimeoutOrDefault returns the hook's run bound: its Timeout when set,
// DefaultTimeout otherwise. The Duration parser rejects non-positive values,
// so zero can only mean "absent".
func (s Spec) TimeoutOrDefault() time.Duration {
	if s.Timeout == 0 {
		return DefaultTimeout
	}
	return time.Duration(s.Timeout)
}

// ScreenConfig is a declarative Screen entry in hooks.yaml. It carries no
// Point field at all: pre_approve is the only pre-point, so Screens attach
// there structurally and the pre/post discipline cannot be violated in config
// (ADR 0023).
type ScreenConfig struct {
	Spec `yaml:",inline"`
}

// NotifierConfig is a declarative Notifier entry in hooks.yaml; Point names
// the post-point it fires at, Paths optionally scopes it, and WorkDir
// optionally anchors its harness run.
//
// Paths is firing discipline's second axis (ADR 0026): gitignore-style globs
// over the PR's changed-file paths deciding whether this Notifier applies to
// the PR at all. Absent Paths fires on every PR. It lives here and NOT on the
// shared Spec deliberately — a scoped Screen would have to resolve to proceed
// where it does not apply, handing hooks.yaml a way to silently un-gate whole
// file classes; the hazard stays unrepresentable rather than validated against
// (the same technique as ScreenConfig carrying no Point field).
//
// WorkDir is the harness process's working directory (ADR 0027): an absolute
// path, so the CLI discovers that directory's ambient skills. It is the read
// ceiling for Claude and Copilot; OpenCode treats a containing Git worktree as
// its direct-tool boundary (ADR 0029). Absent WorkDir is the unanchored
// Notifier, running in tm3k's own cwd exactly as before the field existed. It
// lives here and NOT on the shared Spec for the same unrepresentable-hazard reason as
// Paths: a working tree is mutable, unversioned input, so a Screen anchored to
// one could return different verdicts for the same PR head for reasons no
// ledger records — and a gate whose input is not reproducible is not a gate.
type NotifierConfig struct {
	Spec    `yaml:",inline"`
	Point   Point    `yaml:"Point"`
	Paths   []string `yaml:"Paths,omitempty"`
	WorkDir string   `yaml:"WorkDir,omitempty"`
}

// Duration is a time.Duration that round-trips YAML in Go duration syntax
// ("90s", "10m"). Only positive durations are representable: a hook timeout of
// zero or less is meaningless, and rejecting it at parse keeps "absent" the
// only spelling of "use the default".
type Duration time.Duration

// UnmarshalYAML parses a Go duration string, rejecting non-positive values.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("parse Timeout %q: %w", s, err)
	}
	if parsed <= 0 {
		return fmt.Errorf("parse Timeout %q: must be positive", s)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders the duration back in Go duration syntax.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// Config is the loaded hooks.yaml document: two lists, one per hook kind, so
// the pre/post discipline (ADR 0021) is unrepresentable to violate.
type Config struct {
	Screens   []ScreenConfig   `yaml:"Screens,omitempty"`
	Notifiers []NotifierConfig `yaml:"Notifiers,omitempty"`
}

// Load reads and validates hooks.yaml at the given path. An absent file is the
// no-hooks case: an empty Config, and — unlike rules.yaml/settings.yaml —
// nothing is seeded (hooks are opt-in, hand-edited config; ADR 0023).
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read hooks.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse hooks.yaml: %w", err)
	}

	// Preflight before healing: a refused config is never rewritten, so the
	// file the user must fix is exactly the file they wrote.
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("hooks.yaml: %w", err)
	}

	if cfg.healIDs() {
		if err := cfg.persist(path); err != nil {
			return Config{}, fmt.Errorf("write healed hooks.yaml: %w", err)
		}
	}
	return cfg, nil
}

// healIDs generates a stable ID for every hook missing one (the settings.yaml
// self-heal precedent, keeping the rules-store ID idiom without CRUD —
// ADR 0023) and reports whether anything was healed, so Load rewrites the file
// only when the heal changed it.
func (c *Config) healIDs() bool {
	healed := false
	for i := range c.Screens {
		if c.Screens[i].ID == "" {
			c.Screens[i].ID = newID()
			healed = true
		}
	}
	for i := range c.Notifiers {
		if c.Notifiers[i].ID == "" {
			c.Notifiers[i].ID = newID()
			healed = true
		}
	}
	return healed
}

// persist rewrites hooks.yaml with the current config. Only the self-heal
// calls it — hooks.yaml is otherwise hand-edited, never programmatically
// mutated (no CRUD, ADR 0023).
func (c Config) persist(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// newID returns a stable random hex identifier for a hook, independent of its
// editable name (the rules-store idiom).
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic and effectively never happens;
		// panicking here surfaces it at startup rather than minting a weak ID.
		panic(fmt.Sprintf("generate hook id: %v", err))
	}
	return hex.EncodeToString(b[:])
}
