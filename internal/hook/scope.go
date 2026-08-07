package hook

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Scope is a Notifier's compiled path scope — firing discipline's second axis
// (ADR 0026). Cadence answers how often a Notifier may fire (once per PR ever);
// scope answers whether it applies to the PR at all.
//
// The zero Scope carries no patterns and matches every PR: absent Paths is the
// unscoped Notifier every hooks.yaml written before scope existed declares, and
// the cross-cutting reviewer that owns the seams between languages.
//
// Scope exists on the Notifier kind only. A scoped Screen would have to resolve
// to proceed where it does not apply (or the PR sits in Screening forever),
// handing hooks.yaml a way to silently un-gate whole file classes; the hazard
// stays unrepresentable rather than validated against.
type Scope struct {
	// patterns are gitignore-normalised at construction, never at match time.
	patterns []string
}

// NewScope compiles a Notifier's configured Paths into a Scope, normalising
// each pattern ONCE (the boot-load moment, not the per-match one). Patterns are
// validated by the boot preflight, so an unparseable one cannot reach here from
// a loaded config.
func NewScope(paths []string) Scope {
	if len(paths) == 0 {
		return Scope{}
	}
	patterns := make([]string, 0, len(paths))
	for _, p := range paths {
		patterns = append(patterns, normalizePattern(p))
	}
	return Scope{patterns: patterns}
}

// normalizePattern applies the gitignore convention to one pattern: a pattern
// naming no directory is anchored nowhere, so it is prefixed with **/ and
// matches at any depth (`*.go` covers main.go AND internal/hook/hook.go).
// doublestar's ** spans zero or more segments, which is what makes the prefixed
// form match a root-level file too. A pattern that contains / is left literal,
// keeping monorepo directory scoping (`services/api/**`) expressible.
func normalizePattern(p string) string {
	if strings.Contains(p, "/") {
		return p
	}
	return "**/" + p
}

// Matches reports whether the Notifier applies to a PR with these changed-file
// paths. An unscoped Notifier matches everything; otherwise any pattern
// matching any path is a match.
//
// files is what the cycle fetch could see and changedFiles is what the PR
// actually touches, so files is TRUNCATED (gh caps it at 100 per PR) exactly
// when len(files) < changedFiles. On a truncated list with no visible match the
// Notifier FIRES: never-fire-on-no-signal is an auto-approval rule (ADR 0005),
// where acting without evidence is the consequential move. A Notifier can
// neither block, divert, nor reorder anything, so DECLINING is the consequential
// choice here and firing is inert — and the agent then fetches the real,
// uncapped diff itself, seeing past the window tm3k could not. Uncertainty
// resolves toward the harmless side, and the sides differ by kind: a Screen
// with no verdict never proceeds; a Notifier with unknown scope fires.
func (s Scope) Matches(files []string, changedFiles int) bool {
	if len(s.patterns) == 0 {
		return true
	}
	for _, f := range files {
		for _, p := range s.patterns {
			if ok, err := doublestar.Match(p, f); err == nil && ok {
				return true
			}
		}
	}
	return len(files) < changedFiles
}
