// Package rule models a named, enable/disable-able PR matching condition and
// the pure matcher that decides whether a rule matches a parsed PR title. A PR
// is auto-approved if ANY enabled rule matches it (rules are OR'd); on multiple
// matches the engine attributes the approval to the first matching enabled rule
// in file order.
//
// Matching decomposes into two predicates:
//   - Author — include/exclude lists of literal GitHub logins, compared
//     case-insensitively, with the magic token @me resolved to the engine's
//     resolved self-login before comparison.
//   - Conventional-commit title parts (type, scope, description) — each with an
//     optional Include regex and optional Exclude regex, matched
//     case-insensitively. A part with neither pattern is unconstrained.
//
// A title that does not parse as a conventional commit is a non-match and can
// never be auto-approved; that gate lives in the engine, which passes parsedOK
// into Matches.
package rule

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
)

// selfToken is the magic author token that resolves to the engine's resolved
// @me login before author comparison.
const selfToken = "@me"

// Rule is a named matching condition for PRs. Its on-disk form is PascalCase
// YAML (.config/rules.yaml); its wire form (Slice 5 HTTP CRUD) is snake_case
// JSON. Both tag sets are present so the two representations stay decoupled.
//
// ID is a stable, generated identifier independent of the user-editable Name,
// so renaming a rule does not change its identity.
type Rule struct {
	ID   string `yaml:"ID" json:"id"`
	Name string `yaml:"Name" json:"name"`
	// Class discriminates the two Rule classes: "approve" (auto-approve on match)
	// or "review" (route to Needs-Human-Review, never approve). An empty/absent
	// Class reads as "approve" (see IsReview), so existing files and the two seeds
	// need no migration; the next mutation rewrites them with an explicit Class.
	// Class changes only a match's OUTCOME (engine routing), never whether a match
	// is computed — Matches is class-agnostic.
	Class              string   `yaml:"Class" json:"class"`
	Enabled            bool     `yaml:"Enabled" json:"enabled"`
	AuthorsInclude     []string `yaml:"AuthorsInclude" json:"authors_include"`
	AuthorsExclude     []string `yaml:"AuthorsExclude" json:"authors_exclude"`
	TypeInclude        string   `yaml:"TypeInclude" json:"type_include"`
	TypeExclude        string   `yaml:"TypeExclude" json:"type_exclude"`
	ScopeInclude       string   `yaml:"ScopeInclude" json:"scope_include"`
	ScopeExclude       string   `yaml:"ScopeExclude" json:"scope_exclude"`
	DescriptionInclude string   `yaml:"DescriptionInclude" json:"description_include"`
	DescriptionExclude string   `yaml:"DescriptionExclude" json:"description_exclude"`
	// DiffMin/DiffMax bound a PR's total changed lines (additions + deletions).
	// Both are 0 => unconstrained (mirroring the "" => unconstrained string
	// idiom); the predicate matches when DiffMin <= size <= DiffMax. This
	// predicate is shared by both Rule classes — only a match's outcome differs.
	DiffMin int `yaml:"DiffMin" json:"diff_min"`
	DiffMax int `yaml:"DiffMax" json:"diff_max"`
}

// classReview is the canonical Class value for a Review Rule. The Approve class
// is the zero/absent value, so it has no constant — IsReview is the only test.
const classReview = "review"

// IsReview reports whether this is a Review Rule (Class == "review",
// case-insensitively). Every other Class value — including the empty/absent one
// — reads as an Approve Rule, which is why an unmigrated file needs no Class.
func (r Rule) IsReview() bool {
	return strings.EqualFold(r.Class, classReview)
}

// Matches reports whether the rule matches a PR. c and parsedOK come from
// conventionalcommit.Parse(title); a title that did not parse (parsedOK ==
// false) is a non-match and can never be approved. author is the PR author's
// login; selfLogin is the resolved @me login used to expand the @me token in
// the author lists.
//
// Matches is pure (no I/O). It returns an error only when one of the rule's
// regex patterns is invalid; seeded and validated rules are always compilable,
// so callers may treat an error as a configuration fault.
//
// A rule matches when the author include/exclude predicate passes AND, for each
// title part (type, scope, description), the Include (if set) matches and the
// Exclude (if set) does not, AND the diff-size predicate passes. diffSize is the
// PR's total changed lines (additions + deletions); the engine sums the two
// separate PR fields before calling Matches.
func (r Rule) Matches(c conventionalcommit.Commit, parsedOK bool, author, selfLogin string, diffSize int) (bool, error) {
	// A non-conventional title can never be auto-approved.
	if !parsedOK {
		return false, nil
	}

	if !r.authorMatches(author, selfLogin) {
		return false, nil
	}

	for _, part := range []struct {
		name             string
		value            string
		include, exclude string
	}{
		{"type", c.Type, r.TypeInclude, r.TypeExclude},
		{"scope", c.Scope, r.ScopeInclude, r.ScopeExclude},
		{"description", c.Description, r.DescriptionInclude, r.DescriptionExclude},
	} {
		ok, err := partMatches(part.value, part.include, part.exclude)
		if err != nil {
			return false, fmt.Errorf("rule %q %s: %w", r.Name, part.name, err)
		}
		if !ok {
			return false, nil
		}
	}

	if !r.diffMatches(diffSize) {
		return false, nil
	}

	return true, nil
}

// diffMatches applies the diff-size predicate: with 0 => unconstrained on each
// bound, it matches when DiffMin <= diffSize <= DiffMax. A zero DiffMin imposes
// no lower bound; a zero DiffMax imposes no upper bound.
func (r Rule) diffMatches(diffSize int) bool {
	if r.DiffMin != 0 && diffSize < r.DiffMin {
		return false
	}
	if r.DiffMax != 0 && diffSize > r.DiffMax {
		return false
	}
	return true
}

// authorMatches applies the author include/exclude predicate. Logins are
// compared case-insensitively as literals (not regex); the @me token expands to
// selfLogin first. If AuthorsInclude is non-empty the author must match one
// entry; if AuthorsExclude is non-empty and the author matches one entry the
// rule fails.
func (r Rule) authorMatches(author, selfLogin string) bool {
	if len(r.AuthorsInclude) > 0 && !loginIn(r.AuthorsInclude, author, selfLogin) {
		return false
	}
	if len(r.AuthorsExclude) > 0 && loginIn(r.AuthorsExclude, author, selfLogin) {
		return false
	}
	return true
}

// loginIn reports whether author equals (case-insensitively) any entry in the
// list, expanding the @me token to selfLogin. An @me entry with no resolved
// selfLogin never matches.
func loginIn(list []string, author, selfLogin string) bool {
	for _, entry := range list {
		if entry == selfToken {
			if selfLogin == "" {
				continue
			}
			entry = selfLogin
		}
		if strings.EqualFold(entry, author) {
			return true
		}
	}
	return false
}

// partMatches applies a part's optional Include/Exclude regexes (both
// case-insensitive). An empty pattern is unconstrained. It returns an error
// only for an invalid pattern.
func partMatches(value, include, exclude string) (bool, error) {
	if include != "" {
		re, err := compileCI(include)
		if err != nil {
			return false, fmt.Errorf("include %q: %w", include, err)
		}
		if !re.MatchString(value) {
			return false, nil
		}
	}
	if exclude != "" {
		re, err := compileCI(exclude)
		if err != nil {
			return false, fmt.Errorf("exclude %q: %w", exclude, err)
		}
		if re.MatchString(value) {
			return false, nil
		}
	}
	return true, nil
}

// compileCI compiles a pattern as a case-insensitive regex.
func compileCI(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + pattern)
}
