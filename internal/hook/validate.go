package hook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// The preflight failure classes (ADR 0023): each refuses startup, wrapped with
// the offending hook's label so the user can fix hooks.yaml without
// spelunking. Boot-time only — there is no hot-reload, so boot is the single
// moment bad config can be caught.
var (
	// ErrUnknownHarness rejects a Harness outside the adapter allowlist —
	// claude, copilot, and opencode (ADR 0023/0024/0029); a later adapter is one
	// allowlist entry plus its internal/harness implementation.
	ErrUnknownHarness = errors.New("unknown harness")
	// ErrMissingPrompt rejects a hook with neither Prompt nor PromptFile: an
	// AI hook without instructions can do nothing.
	ErrMissingPrompt = errors.New("missing prompt: set Prompt or PromptFile")
	// ErrAmbiguousPrompt rejects a hook setting both Prompt and PromptFile —
	// the spec is Prompt|PromptFile, exactly one; silently preferring either
	// would run instructions the user did not intend.
	ErrAmbiguousPrompt = errors.New("prompt and PromptFile are mutually exclusive")
	// ErrBadPromptFile rejects a PromptFile that does not resolve to a readable
	// file, on either kind (ADR 0027). Instructions are resolved at RUN time,
	// and for a Notifier that is after the fired-ledger row is appended — so a
	// typo there is a logged miss with the once-per-PR fire already spent, and
	// that PR loses its review permanently. Boot is the only moment the typo is
	// still cheap.
	ErrBadPromptFile = errors.New("unreadable prompt file")
	// ErrBadPoint rejects a Notifier whose Point is not a post-point: absent,
	// unknown, or a pre-point (pre-points carry Screens only — ADR 0021).
	ErrBadPoint = errors.New("invalid hook point")
	// ErrBadPattern rejects a Notifier Paths entry that is not a valid glob.
	// Scope is a silent gate — a pattern that never matches simply never fires
	// — so a typo that makes the pattern unparseable must surface at boot, not
	// as a review-assist that mysteriously went quiet (ADR 0026).
	ErrBadPattern = errors.New("invalid path pattern")
	// ErrBadWorkDir rejects a Notifier WorkDir that is not an absolute path to
	// an existing directory (ADR 0027). WorkDir is a read grant handed to an
	// agent holding a gh publishing channel, so which directory it names must
	// never be resolved by accident. NOTHING is expanded: no $VAR, no ~ — an
	// unset variable expands to "", which silently inherits tm3k's own cwd, and
	// the agent would then find no skills and post one generic review forever.
	ErrBadWorkDir = errors.New("invalid work directory")
	// ErrDuplicateName rejects a Name appearing twice across Screens and
	// Notifiers together: names label hooks in errors, logs, and queue
	// reasons (screen:<name>), so they must be unambiguous.
	ErrDuplicateName = errors.New("duplicate hook name")
	// ErrMissingName rejects a hook without a Name — every later surface
	// (queue reasons, logs, these very errors) needs the human handle.
	ErrMissingName = errors.New("missing hook name")
)

// knownHarnesses is the harness allowlist ErrUnknownHarness checks against
// (ADR 0023/0024/0028); adding a harness is one entry here.
var knownHarnesses = map[string]bool{"claude": true, "copilot": true, "opencode": true}

// validate runs the boot preflight over the whole config and returns the first
// failure. The pre/post discipline itself needs no checking on the Screen side
// — a ScreenConfig has no Point field to get wrong; only the Notifier's named
// post-point can be misconfigured.
func (c Config) validate() error {
	names := map[string]bool{}
	for i, s := range c.Screens {
		l := label("screen", i, s.Name)
		if err := s.validate(l, names); err != nil {
			return err
		}
	}
	for i, n := range c.Notifiers {
		l := label("notifier", i, n.Name)
		if err := n.validate(l, names); err != nil {
			return err
		}
		if !postPoints[n.Point] {
			return fmt.Errorf("%s: %w: %q — Notifiers attach to post-points only (%s)",
				l, ErrBadPoint, n.Point, strings.Join(sortedNames(postPoints), ", "))
		}
		for _, p := range n.Paths {
			if !doublestar.ValidatePattern(p) {
				return fmt.Errorf("%s: %w: %q", l, ErrBadPattern, p)
			}
		}
		if err := validateWorkDir(l, n.WorkDir, n.Enabled); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkDir checks a Notifier's optional harness anchor: absent is the
// unanchored Notifier, and anything else must be an absolute path to an
// existing directory. The path is taken literally — no $VAR, no ~, no cleanup
// pass — so the directory the operator wrote is the directory the agent reads
// from (ADR 0027).
//
// Well-formedness is checked always; existence only for a hook that can run,
// the checkHarnessBinaries line — a disabled Notifier may name the skills
// checkout you have not made yet, and the check lands on the boot after you
// flip Enabled.
func validateWorkDir(label, workDir string, enabled bool) error {
	if workDir == "" {
		return nil
	}
	if !filepath.IsAbs(workDir) {
		return fmt.Errorf("%s: %w: %q — WorkDir must be an absolute path (no $VAR or ~ expansion is performed)",
			label, ErrBadWorkDir, workDir)
	}
	if !enabled {
		return nil
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return fmt.Errorf("%s: %w: %q: %v", label, ErrBadWorkDir, workDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: %w: %q — not a directory", label, ErrBadWorkDir, workDir)
	}
	return nil
}

// validate checks the declarative fields shared by both kinds, recording the
// hook's Name in names to catch duplicates across the whole file.
func (s Spec) validate(label string, names map[string]bool) error {
	if s.Name == "" {
		return fmt.Errorf("%s: %w", label, ErrMissingName)
	}
	if names[s.Name] {
		return fmt.Errorf("%s: %w: %q", label, ErrDuplicateName, s.Name)
	}
	names[s.Name] = true

	if !knownHarnesses[s.Harness] {
		return fmt.Errorf("%s: %w: %q (known: %s)",
			label, ErrUnknownHarness, s.Harness, strings.Join(sortedNames(knownHarnesses), ", "))
	}

	if s.Prompt == "" && s.PromptFile == "" {
		return fmt.Errorf("%s: %w", label, ErrMissingPrompt)
	}
	if s.Prompt != "" && s.PromptFile != "" {
		return fmt.Errorf("%s: %w", label, ErrAmbiguousPrompt)
	}
	if s.PromptFile != "" && s.Enabled {
		if _, err := os.Stat(s.PromptFile); err != nil {
			return fmt.Errorf("%s: %w: %q: %v", label, ErrBadPromptFile, s.PromptFile, err)
		}
	}
	return nil
}

// label names a hook in a preflight error: by Name when set, by list position
// otherwise (a nameless hook still gets pointed at).
func label(kind string, idx int, name string) string {
	if name != "" {
		return fmt.Sprintf("%s %q", kind, name)
	}
	return fmt.Sprintf("%s #%d", kind, idx+1)
}

// sortedNames lists a registry's keys sorted, so error messages enumerating
// the valid values (points, harnesses) are deterministic.
func sortedNames[K ~string](m map[K]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}
