package conventionalcommit_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		want   conventionalcommit.Commit
		wantOK bool
	}{
		{
			name:   "no scope",
			title:  "chore: bump deps",
			want:   conventionalcommit.Commit{Type: "chore", Description: "bump deps"},
			wantOK: true,
		},
		{
			name:   "simple scope",
			title:  "feat(api): add endpoint",
			want:   conventionalcommit.Commit{Type: "feat", Scope: "api", Description: "add endpoint"},
			wantOK: true,
		},
		{
			name:   "slash scope from CONTEXT example",
			title:  "chore(team/service-b): Add hooks",
			want:   conventionalcommit.Commit{Type: "chore", Scope: "team/service-b", Description: "Add hooks"},
			wantOK: true,
		},
		{
			// Multi/slash/mixed-case scope must be preserved verbatim — the
			// parser does not normalise case or split on commas/slashes.
			name:   "multi slash mixed-case scope preserved verbatim",
			title:  "chore(Team,networking,routing/foo): wire it up",
			want:   conventionalcommit.Commit{Type: "chore", Scope: "Team,networking,routing/foo", Description: "wire it up"},
			wantOK: true,
		},
		{
			name:   "breaking with scope",
			title:  "feat(service-a)!: rework auth",
			want:   conventionalcommit.Commit{Type: "feat", Scope: "service-a", Description: "rework auth", Breaking: true},
			wantOK: true,
		},
		{
			name:   "breaking without scope",
			title:  "chore!: drop legacy flag",
			want:   conventionalcommit.Commit{Type: "chore", Description: "drop legacy flag", Breaking: true},
			wantOK: true,
		},
		{
			// Description case is preserved (matcher is case-insensitive, not
			// the parser).
			name:   "type and description case preserved",
			title:  "Fix: Handle Edge Case",
			want:   conventionalcommit.Commit{Type: "Fix", Description: "Handle Edge Case"},
			wantOK: true,
		},
		{
			name:   "leading and trailing whitespace in description trimmed",
			title:  "chore:    padded description   ",
			want:   conventionalcommit.Commit{Type: "chore", Description: "padded description"},
			wantOK: true,
		},
		{
			// DECISION (locked): the PRD's real-world malformed doubled-prefix
			// title PARSES. Type and scope come from the first prefix; the
			// entire remainder — including the redundant second prefix — lands
			// verbatim in the description. It is a coherent, non-panicking
			// result; the redundant text is harmless to the downstream matcher.
			name:   "malformed doubled prefix parses with remainder in description",
			title:  "chore(deps): chore(deps): bump library to v2",
			want:   conventionalcommit.Commit{Type: "chore", Scope: "deps", Description: "chore(deps): bump library to v2"},
			wantOK: true,
		},
		{
			// Same decision holds for a no-scope doubled prefix.
			name:   "malformed doubled prefix no scope",
			title:  "chore: chore: bump",
			want:   conventionalcommit.Commit{Type: "chore", Description: "chore: bump"},
			wantOK: true,
		},
		{
			name:   "empty scope parens",
			title:  "chore(): tidy",
			want:   conventionalcommit.Commit{Type: "chore", Scope: "", Description: "tidy"},
			wantOK: true,
		},
		// --- non-matches: ok must be false and Commit must be the zero value ---
		{
			name:   "plain prose is not conventional",
			title:  "just some text",
			wantOK: false,
		},
		{
			name:   "empty string",
			title:  "",
			wantOK: false,
		},
		{
			name:   "missing colon",
			title:  "chore bump deps",
			wantOK: false,
		},
		{
			name:   "colon but no description",
			title:  "chore:",
			wantOK: false,
		},
		{
			name:   "colon then only whitespace is not a description",
			title:  "chore:    ",
			wantOK: false,
		},
		{
			name:   "type with space before colon is not a bare type",
			title:  "chore : bump",
			wantOK: false,
		},
		{
			name:   "leading garble before type is rejected",
			title:  "!chore: bump",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := conventionalcommit.Parse(tt.title)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.want, got)
			} else {
				// On a non-match the parser must not leak a spurious result.
				require.Equal(t, conventionalcommit.Commit{}, got)
			}
		})
	}
}

// TestParseNeverPanics is a belt-and-braces guard for the "never panic"
// acceptance criterion against assorted hostile inputs.
func TestParseNeverPanics(t *testing.T) {
	hostile := []string{
		"",
		"(",
		")",
		"():",
		"!:",
		"chore((((((((:",
		"chore)))))): x",
		"chore(deps): chore(deps): chore(deps): deep",
		"::::::",
		"\t\n",
	}
	for _, in := range hostile {
		require.NotPanics(t, func() {
			_, _ = conventionalcommit.Parse(in)
		}, "input %q", in)
	}
}
