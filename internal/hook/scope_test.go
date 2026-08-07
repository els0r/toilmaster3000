package hook_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// Scope is the pure core of firing discipline's second axis (ADR 0026): given
// a Notifier's configured Paths and the changed-file paths riding the cycle
// fetch, it judges whether the Notifier applies to the PR at all. Heavy table
// per the testing doctrine — this fold decides whether a once-per-PR fire is
// spent, so its edges are the correctness-critical ones.
func TestScopeMatches(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		files []string
		want  bool
	}{
		{
			name:  "absent Paths matches every PR — every existing hooks.yaml keeps its meaning",
			paths: nil,
			files: []string{"README.md"},
			want:  true,
		},
		{
			name:  "an empty Paths list is the absent case, not a scope nothing satisfies",
			paths: []string{},
			files: []string{"README.md"},
			want:  true,
		},
		{
			name:  "absent Paths matches even a PR whose file list is empty",
			paths: nil,
			want:  true,
		},
		{
			name:  "a pattern matching a changed file scopes the Notifier in",
			paths: []string{"**/*.go"},
			files: []string{"internal/hook/hook.go"},
			want:  true,
		},
		{
			name:  "no pattern matches any changed file: out of scope",
			paths: []string{"**/*.go"},
			files: []string{"README.md"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hook.NewScope(tt.paths).Matches(tt.files, len(tt.files))
			require.Equal(t, tt.want, got)
		})
	}
}

// Unknown scope FIRES. The cycle fetch caps files at 100 per PR, so a list
// shorter than the PR's changedFiles count is a window tm3k cannot see past —
// and on a truncated list with no visible match the Notifier fires anyway
// (ADR 0026 decision 5). Never-fire-on-no-signal (ADR 0005) does NOT transfer:
// it was written for auto-APPROVAL, where acting without evidence is the
// consequential move. A Notifier cannot block, divert, or reorder anything, so
// DECLINING is the consequential choice here and firing is inert — the agent
// then fetches the real, uncapped diff itself. Uncertainty resolves toward the
// harmless side, and the sides differ by kind.
func TestScopeFiresOnATruncatedFileList(t *testing.T) {
	tests := []struct {
		name         string
		paths        []string
		files        []string
		changedFiles int
		want         bool
	}{
		{
			name:         "truncated list, no visible match: fires — tm3k cannot know it does not apply",
			paths:        []string{"*.go"},
			files:        []string{"docs/a.md", "docs/b.md"},
			changedFiles: 120,
			want:         true,
		},
		{
			name:         "the SAME file set untruncated, no match: declines — the list is the whole truth",
			paths:        []string{"*.go"},
			files:        []string{"docs/a.md", "docs/b.md"},
			changedFiles: 2,
			want:         false,
		},
		{
			name:         "truncated list WITH a visible match: fires on the evidence, not the uncertainty",
			paths:        []string{"*.go"},
			files:        []string{"main.go", "docs/a.md"},
			changedFiles: 120,
			want:         true,
		},
		{
			name:         "an empty file list against a real changedFiles count is total truncation: fires",
			paths:        []string{"*.go"},
			changedFiles: 3,
			want:         true,
		},
		{
			name:         "no files and no count is no truncation: an unscoped-by-nothing Notifier declines",
			paths:        []string{"*.go"},
			changedFiles: 0,
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, hook.NewScope(tt.paths).Matches(tt.files, tt.changedFiles))
		})
	}
}

// Patterns match the file's FULL path under the gitignore convention: a
// pattern naming no directory is prefixed with **/ at load, so it matches at
// any depth, while a pattern containing / keeps its directory scoping (ADR
// 0026 decision 3). This is the table of the ADR, verbatim — it is also the
// case that disqualifies stdlib path.Match, which treats ** as two *s and so
// matches `**/*.go` at exactly one directory level.
func TestScopeNormalisesGitignoreStyle(t *testing.T) {
	files := []string{"main.go", "internal/hook/hook.go", "services/api/x/y.go"}

	tests := []struct {
		pattern string
		want    []bool // one per file above, in order
	}{
		{pattern: "*.go", want: []bool{true, true, true}},
		{pattern: "**/*.go", want: []bool{true, true, true}},
		{pattern: "services/api/**", want: []bool{false, false, true}},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()
			scope := hook.NewScope([]string{tt.pattern})
			for i, f := range files {
				require.Equal(t, tt.want[i], scope.Matches([]string{f}, 1), "pattern %q vs %q", tt.pattern, f)
			}
		})
	}
}

// Both sides of the fold are any-match: any configured pattern matching any
// changed file scopes the Notifier in. A polyglot PR is therefore in scope for
// every language reviewer whose files it touches, and each fires independently.
func TestScopeIsAnyPatternAgainstAnyFile(t *testing.T) {
	t.Parallel()
	scope := hook.NewScope([]string{"*.go", "scripts/**"})

	require.True(t, scope.Matches([]string{"README.md", "main.go"}, 2),
		"a later file matching an earlier pattern is a match")
	require.True(t, scope.Matches([]string{"scripts/release.sh"}, 1),
		"a later pattern matching the only file is a match")
	require.False(t, scope.Matches([]string{"README.md", "docs/adr/0026.md"}, 2),
		"no pattern against any file is out of scope")
}
