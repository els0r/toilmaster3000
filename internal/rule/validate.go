package rule

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrEmptyRule is returned by Validate when a rule constrains nothing: no
// author include/exclude entries AND all six title-part regexes empty AND no
// diff-size bound. Such a rule would match every PR (the match-everything
// footgun), so it is rejected before persisting. The message is class-neutral
// (it speaks of "match", not "approve") because the predicate is shared by both
// Rule classes. The HTTP layer maps this to a 4xx.
var ErrEmptyRule = errors.New("rule must constrain at least one of author, type, scope, description, or diff size")

// ErrInvalidRegex is the sentinel wrapped by Validate when one of a rule's
// title-part regexes fails to compile. The wrapping error names the offending
// field (e.g. "invalid regex in scope_include: ..."). The HTTP layer maps this
// to a 4xx and surfaces the message. Author entries are literal logins, not
// regex, and are never compiled.
var ErrInvalidRegex = errors.New("invalid regex")

// ErrInvalidDiffRange is returned by Validate when a rule's diff bounds are
// inverted — DiffMin > DiffMax with both non-zero. (A zero bound is
// unconstrained, so a one-sided bound is always valid.) The HTTP layer maps
// this to a 4xx like the other semantic-validation failures.
var ErrInvalidDiffRange = errors.New("diff_min must not exceed diff_max")

// Validate runs the semantic guards that huma's structural schema cannot
// express, before a rule is persisted on Create or Update:
//
//   - reject a rule that constrains nothing (ErrEmptyRule) — a diff-size bound
//     counts, so a diff-only rule is valid;
//   - reject any non-empty title-part regex that fails to compile, naming the
//     offending field (ErrInvalidRegex);
//   - reject inverted diff bounds, DiffMin > DiffMax with both non-zero
//     (ErrInvalidDiffRange).
//
// It returns the first failure it finds. ID and Name are not validated here;
// the empty-rule check ignores both because a name alone constrains no PR.
func Validate(r Rule) error {
	if len(r.AuthorsInclude) == 0 && len(r.AuthorsExclude) == 0 &&
		r.TypeInclude == "" && r.TypeExclude == "" &&
		r.ScopeInclude == "" && r.ScopeExclude == "" &&
		r.DescriptionInclude == "" && r.DescriptionExclude == "" &&
		r.DiffMin == 0 && r.DiffMax == 0 {
		return ErrEmptyRule
	}

	if r.DiffMin != 0 && r.DiffMax != 0 && r.DiffMin > r.DiffMax {
		return ErrInvalidDiffRange
	}

	for _, f := range []struct {
		field   string
		pattern string
	}{
		{"type_include", r.TypeInclude},
		{"type_exclude", r.TypeExclude},
		{"scope_include", r.ScopeInclude},
		{"scope_exclude", r.ScopeExclude},
		{"description_include", r.DescriptionInclude},
		{"description_exclude", r.DescriptionExclude},
	} {
		if f.pattern == "" {
			continue
		}
		if _, err := regexp.Compile(f.pattern); err != nil {
			return fmt.Errorf("%w in %s: %s", ErrInvalidRegex, f.field, err)
		}
	}

	return nil
}
