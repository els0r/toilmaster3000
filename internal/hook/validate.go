package hook

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The preflight failure classes (ADR 0023): each refuses startup, wrapped with
// the offending hook's label so the user can fix hooks.yaml without
// spelunking. Boot-time only — there is no hot-reload, so boot is the single
// moment bad config can be caught.
var (
	// ErrUnknownHarness rejects a Harness outside the adapter allowlist —
	// claude-only in MVP; a later adapter (Copilot, OpenCode) is one allowlist
	// entry plus its internal/harness implementation.
	ErrUnknownHarness = errors.New("unknown harness")
	// ErrMissingPrompt rejects a hook with neither Prompt nor PromptFile: an
	// AI hook without instructions can do nothing.
	ErrMissingPrompt = errors.New("missing prompt: set Prompt or PromptFile")
	// ErrAmbiguousPrompt rejects a hook setting both Prompt and PromptFile —
	// the spec is Prompt|PromptFile, exactly one; silently preferring either
	// would run instructions the user did not intend.
	ErrAmbiguousPrompt = errors.New("Prompt and PromptFile are mutually exclusive")
	// ErrBadPoint rejects a Notifier whose Point is not a post-point: absent,
	// unknown, or a pre-point (pre-points carry Screens only — ADR 0021).
	ErrBadPoint = errors.New("invalid hook point")
	// ErrDuplicateName rejects a Name appearing twice across Screens and
	// Notifiers together: names label hooks in errors, logs, and queue
	// reasons (screen:<name>), so they must be unambiguous.
	ErrDuplicateName = errors.New("duplicate hook name")
	// ErrMissingName rejects a hook without a Name — every later surface
	// (queue reasons, logs, these very errors) needs the human handle.
	ErrMissingName = errors.New("missing hook name")
)

// knownHarnesses is the harness allowlist ErrUnknownHarness checks against.
// Claude-only in MVP (ADR 0023); adding a harness is one entry here.
var knownHarnesses = map[string]bool{"claude": true}

// validate runs the boot preflight over the whole config and returns the first
// failure. The pre/post discipline itself needs no checking on the Screen side
// — a ScreenConfig has no Point field to get wrong; only the Notifier's named
// post-point can be misconfigured.
func (c Config) validate() error {
	names := map[string]bool{}
	for i, s := range c.Screens {
		l := label("screen", i, s.Name)
		if err := s.Spec.validate(l, names); err != nil {
			return err
		}
	}
	for i, n := range c.Notifiers {
		l := label("notifier", i, n.Name)
		if err := n.Spec.validate(l, names); err != nil {
			return err
		}
		if !postPoints[n.Point] {
			return fmt.Errorf("%s: %w: %q — Notifiers attach to post-points only (%s)",
				l, ErrBadPoint, n.Point, strings.Join(sortedNames(postPoints), ", "))
		}
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
