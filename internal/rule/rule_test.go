package rule_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// parse is a small helper mirroring how the engine feeds Matches.
func parse(title string) (conventionalcommit.Commit, bool) {
	return conventionalcommit.Parse(title)
}

// TestMatches is the correctness-critical matcher matrix: author include/
// exclude (incl. @me), per-part include/exclude regex, case-insensitivity, and
// the non-parse -> non-match gate. matched_rule first-match attribution is
// proved through the HTTP seam (server_test); here we prove the predicate.
func TestMatches(t *testing.T) {
	tests := []struct {
		name      string
		rule      rule.Rule
		title     string
		author    string
		selfLogin string
		diffSize  int
		want      bool
	}{
		{
			name:   "unconstrained-part rule matches any parsing title",
			rule:   rule.Rule{Name: "anything"},
			title:  "chore: x",
			author: "alice",
			want:   true,
		},
		{
			name:   "non-conventional title never matches",
			rule:   rule.Rule{Name: "anything"},
			title:  "just some words, no type",
			author: "alice",
			want:   false,
		},
		{
			name:   "type include matches anchored",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$"},
			title:  "chore: tidy",
			author: "alice",
			want:   true,
		},
		{
			name:   "type include rejects non-matching type",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$"},
			title:  "fix: bug",
			author: "alice",
			want:   false,
		},
		{
			name:   "type include anchored rejects superstring",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$"},
			title:  "chored: tidy",
			author: "alice",
			want:   false,
		},
		{
			name:   "type match is case-insensitive",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$"},
			title:  "CHORE: tidy",
			author: "alice",
			want:   true,
		},
		{
			name:   "scope exclude blocks renovate scope",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$", ScopeExclude: "renovate"},
			title:  "chore(renovate): bump",
			author: "alice",
			want:   false,
		},
		{
			name:   "scope exclude is case-insensitive",
			rule:   rule.Rule{Name: "chores", ScopeExclude: "renovate"},
			title:  "chore(Renovate): bump",
			author: "alice",
			want:   false,
		},
		{
			name:   "scope exclude passes when scope differs",
			rule:   rule.Rule{Name: "chores", TypeInclude: "^chore$", ScopeExclude: "renovate"},
			title:  "chore(deps): bump",
			author: "alice",
			want:   true,
		},
		{
			name:   "scope include substring matches service-a inside slashed scope",
			rule:   rule.Rule{Name: "service-a", ScopeInclude: "service-a"},
			title:  "feat(team/service-a): add",
			author: "alice",
			want:   true,
		},
		{
			name:   "scope include rejects when scope absent",
			rule:   rule.Rule{Name: "service-a", ScopeInclude: "service-a"},
			title:  "feat: add",
			author: "alice",
			want:   false,
		},
		{
			name:   "description include matches case-insensitively",
			rule:   rule.Rule{Name: "desc", DescriptionInclude: "add hooks"},
			title:  "chore(o): Add Hooks",
			author: "alice",
			want:   true,
		},
		{
			name:   "description exclude blocks WIP",
			rule:   rule.Rule{Name: "desc", DescriptionExclude: "wip"},
			title:  "chore: WIP do not merge",
			author: "alice",
			want:   false,
		},
		{
			name:   "authors include passes for listed author",
			rule:   rule.Rule{Name: "by author", AuthorsInclude: []string{"teammate_a"}},
			title:  "feat: x",
			author: "teammate_a",
			want:   true,
		},
		{
			name:   "authors include rejects unlisted author",
			rule:   rule.Rule{Name: "by author", AuthorsInclude: []string{"teammate_a"}},
			title:  "feat: x",
			author: "someone_else",
			want:   false,
		},
		{
			name:   "authors include is case-insensitive",
			rule:   rule.Rule{Name: "by author", AuthorsInclude: []string{"TEAMMATE_A"}},
			title:  "feat: x",
			author: "teammate_a",
			want:   true,
		},
		{
			name:      "authors exclude @me blocks the resolved self login",
			rule:      rule.Rule{Name: "not-me", AuthorsExclude: []string{"@me"}},
			title:     "chore: x",
			author:    "me-login",
			selfLogin: "me-login",
			want:      false,
		},
		{
			name:      "authors exclude @me passes other authors",
			rule:      rule.Rule{Name: "not-me", AuthorsExclude: []string{"@me"}},
			title:     "chore: x",
			author:    "alice",
			selfLogin: "me-login",
			want:      true,
		},
		{
			name:      "authors include @me matches the resolved self login",
			rule:      rule.Rule{Name: "me", AuthorsInclude: []string{"@me"}},
			title:     "chore: x",
			author:    "me-login",
			selfLogin: "me-login",
			want:      true,
		},
		{
			name:      "@me with empty self login never matches that entry",
			rule:      rule.Rule{Name: "me", AuthorsInclude: []string{"@me"}},
			title:     "chore: x",
			author:    "",
			selfLogin: "",
			want:      false,
		},
		{
			name:      "team-chores default rule: chore by other author, non-renovate scope",
			rule:      rule.Rule{Name: "team chores", AuthorsExclude: []string{"@me"}, TypeInclude: "^chore$", ScopeExclude: "renovate"},
			title:     "chore(deps): bump x",
			author:    "alice",
			selfLogin: "me-login",
			want:      true,
		},
		{
			name:      "team-chores default rule excludes self-authored chore",
			rule:      rule.Rule{Name: "team chores", AuthorsExclude: []string{"@me"}, TypeInclude: "^chore$", ScopeExclude: "renovate"},
			title:     "chore: bump x",
			author:    "me-login",
			selfLogin: "me-login",
			want:      false,
		},
		// Diff-size predicate (shared by both Rule classes). 0 => unconstrained
		// on each bound; matches when DiffMin <= diffSize <= DiffMax. diffSize is
		// the sum (additions + deletions) the engine passes.
		{
			name:     "no diff bounds is unconstrained for any size",
			rule:     rule.Rule{Name: "anything"},
			title:    "chore: x",
			author:   "alice",
			diffSize: 9999,
			want:     true,
		},
		{
			name:     "DiffMax alone rejects an over-large diff",
			rule:     rule.Rule{Name: "small", DiffMax: 50},
			title:    "chore: x",
			author:   "alice",
			diffSize: 51,
			want:     false,
		},
		{
			name:     "DiffMax alone accepts a diff at the bound",
			rule:     rule.Rule{Name: "small", DiffMax: 50},
			title:    "chore: x",
			author:   "alice",
			diffSize: 50,
			want:     true,
		},
		{
			name:     "DiffMax alone accepts a diff below the bound",
			rule:     rule.Rule{Name: "small", DiffMax: 50},
			title:    "chore: x",
			author:   "alice",
			diffSize: 10,
			want:     true,
		},
		{
			name:     "DiffMin alone rejects a diff below the bound",
			rule:     rule.Rule{Name: "big", DiffMin: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 99,
			want:     false,
		},
		{
			name:     "DiffMin alone accepts a diff at the bound",
			rule:     rule.Rule{Name: "big", DiffMin: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 100,
			want:     true,
		},
		{
			name:     "DiffMin alone accepts a diff above the bound",
			rule:     rule.Rule{Name: "big", DiffMin: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 500,
			want:     true,
		},
		{
			name:     "both bounds: inside the window matches",
			rule:     rule.Rule{Name: "window", DiffMin: 10, DiffMax: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 50,
			want:     true,
		},
		{
			name:     "both bounds: below the window does not match",
			rule:     rule.Rule{Name: "window", DiffMin: 10, DiffMax: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 5,
			want:     false,
		},
		{
			name:     "both bounds: above the window does not match",
			rule:     rule.Rule{Name: "window", DiffMin: 10, DiffMax: 100},
			title:    "chore: x",
			author:   "alice",
			diffSize: 200,
			want:     false,
		},
		{
			name:     "diff bound still requires the title predicates to pass",
			rule:     rule.Rule{Name: "typed-window", TypeInclude: "^chore$", DiffMax: 100},
			title:    "feat: x",
			author:   "alice",
			diffSize: 10,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := parse(tt.title)
			got, err := tt.rule.Matches(c, ok, tt.author, tt.selfLogin, tt.diffSize)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestMatchesInvalidRegexErrors proves an unparseable pattern surfaces as an
// error rather than silently matching or panicking.
func TestMatchesInvalidRegexErrors(t *testing.T) {
	r := rule.Rule{Name: "bad", TypeInclude: "("}
	c, ok := parse("chore: x")
	_, err := r.Matches(c, ok, "alice", "", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "type")
}

// TestIsReview proves the Class discriminator: only an explicit (case-folded)
// "review" is a Review Rule; empty/absent and "approve" read as Approve Rules.
// Class does not affect whether a match is computed — only the engine's routing.
func TestIsReview(t *testing.T) {
	tests := []struct {
		class string
		want  bool
	}{
		{"", false},
		{"approve", false},
		{"review", true},
		{"Review", true},
		{"REVIEW", true},
		{"anything-else", false},
	}
	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			require.Equal(t, tt.want, rule.Rule{Class: tt.class}.IsReview())
		})
	}
}
