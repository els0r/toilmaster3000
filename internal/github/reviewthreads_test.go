package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// TestUnresolvedCount pins the Discussion gate's pure judge (ADR 0019): the
// fold from one PR's decoded reviewThreads connection into its
// unresolved-thread count. Truncation is conservative — a connection reporting
// more thread pages than fetched is treated as HAVING unresolved threads,
// never as resolved: the gate holds until a fetch proves closure.
func TestUnresolvedCount(t *testing.T) {
	resolved := github.ReviewThread{IsResolved: true}
	unresolved := github.ReviewThread{IsResolved: false}

	tests := []struct {
		name string
		raw  github.RawReviewThreads
		want int
	}{
		{
			name: "no threads folds to zero — nothing to resolve, the gate is open",
			raw:  github.RawReviewThreads{},
			want: 0,
		},
		{
			name: "all fetched threads resolved folds to zero",
			raw:  github.RawReviewThreads{Nodes: []github.ReviewThread{resolved, resolved}},
			want: 0,
		},
		{
			name: "unresolved threads are counted",
			raw:  github.RawReviewThreads{Nodes: []github.ReviewThread{resolved, unresolved, unresolved}},
			want: 2,
		},
		{
			name: "further pages with every fetched thread resolved still holds — at least one unresolved, never resolved",
			raw:  github.RawReviewThreads{Nodes: []github.ReviewThread{resolved}, HasMorePages: true},
			want: 1,
		},
		{
			name: "further pages on an empty fetched page holds too (defensive)",
			raw:  github.RawReviewThreads{HasMorePages: true},
			want: 1,
		},
		{
			name: "further pages never shrink an already-unresolved count",
			raw:  github.RawReviewThreads{Nodes: []github.ReviewThread{unresolved, unresolved}, HasMorePages: true},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, github.UnresolvedCount(tt.raw))
		})
	}
}
