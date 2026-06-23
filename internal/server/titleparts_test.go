package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTitleParts covers the parse-on-read converter helper: the scope-split
// matrix (comma, slash, mixed, trimmed, empty) and the non-conventional
// failed-parse fallback (the zero value, which the frontend renders raw).
func TestTitleParts(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  TitleParts
	}{
		{
			name:  "no scope",
			title: "chore: bump deps",
			want:  TitleParts{Type: "chore", Scopes: []string{}, Description: "bump deps"},
		},
		{
			name:  "single scope",
			title: "fix(api): handle nil",
			want:  TitleParts{Type: "fix", Scopes: []string{"api"}, Description: "handle nil"},
		},
		{
			name:  "comma-separated scopes are split and trimmed",
			title: "chore(deps, ci): bump",
			want:  TitleParts{Type: "chore", Scopes: []string{"deps", "ci"}, Description: "bump"},
		},
		{
			name:  "slash-separated scopes are split",
			title: "feat(team/service-a): add panel",
			want:  TitleParts{Type: "feat", Scopes: []string{"team", "service-a"}, Description: "add panel"},
		},
		{
			name:  "mixed case scope is preserved verbatim",
			title: "fix(API): handle nil",
			want:  TitleParts{Type: "fix", Scopes: []string{"API"}, Description: "handle nil"},
		},
		{
			name:  "breaking marker is parsed",
			title: "feat(api)!: rename field",
			want:  TitleParts{Type: "feat", Scopes: []string{"api"}, Breaking: true, Description: "rename field"},
		},
		{
			name:  "breaking marker without scope",
			title: "chore!: drop legacy flag",
			want:  TitleParts{Type: "chore", Scopes: []string{}, Breaking: true, Description: "drop legacy flag"},
		},
		{
			name:  "non-conventional title is the zero value (failed-parse fallback)",
			title: "totally not a conventional title",
			want:  TitleParts{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, titleParts(tc.title))
		})
	}
}
