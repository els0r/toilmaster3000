// Package conventionalcommit provides a tolerant parser for PR titles that
// follow the conventional-commit form "type(scope)!?: description".
//
// It is pure: no I/O, no logging. It feeds the rule matcher and the
// breaking-change invariant. Type, scope, and description are returned verbatim
// (not lower-cased): case-insensitive matching is the matcher's job, not the
// parser's.
package conventionalcommit

import "regexp"

// Commit is a parsed conventional-commit title.
type Commit struct {
	Type        string
	Scope       string
	Description string
	Breaking    bool
}

// titleRe captures the four parts of a conventional-commit title:
//
//	1: type        — one or more word characters before the scope/marker/colon.
//	3: scope       — the raw contents of the parentheses (group 2), preserved
//	                 verbatim including commas, slashes, and mixed case.
//	4: "!"         — the optional breaking-change marker.
//	5: description — everything after the colon.
//
// The type is intentionally \w+ (not \w+ with extra punctuation): a real
// conventional-commit type is a single bare word, so a leading garble that is
// not a bare word is correctly rejected as a non-match.
//
// The scope body is [^)]* — anything up to the first closing paren — which both
// keeps multi/slash/mixed-case scopes intact and, for the malformed
// doubled-prefix case, stops at the first ")" so the leftover second prefix
// falls into the description rather than corrupting the scope.
var titleRe = regexp.MustCompile(`^(\w+)(\(([^)]*)\))?(!)?:\s*(.+\S)\s*$`)

// Parse parses a PR title into its conventional-commit parts. ok is false when
// the title is not a conventional commit; callers must treat a non-match as
// never-auto-approvable. When ok is false the returned Commit is the zero value.
//
// Tolerance notes, locked by tests:
//   - Scopes are returned verbatim (commas, slashes, mixed case preserved).
//   - The breaking "!" is detected with or without a scope.
//   - Surrounding whitespace in the description is trimmed.
//   - The malformed doubled-prefix title (e.g.
//     "chore(scope): chore(scope): real description") PARSES rather than being
//     rejected: type and scope come from the first prefix and the entire
//     remainder — including the second, redundant prefix — lands verbatim in
//     the description. This is deliberate: it is a coherent, non-panicking
//     result, and the redundant text in the description is harmless to the
//     downstream matcher (which is what the acceptance criterion requires:
//     "a coherent result or ok=false, never panic").
func Parse(title string) (c Commit, ok bool) {
	m := titleRe.FindStringSubmatch(title)
	if m == nil {
		return Commit{}, false
	}
	return Commit{
		Type:        m[1],
		Scope:       m[3],
		Description: m[5],
		Breaking:    m[4] == "!",
	}, true
}
