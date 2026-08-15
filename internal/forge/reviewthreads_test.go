package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestUnresolvedCount pins the Discussion gate's pure judge (ADR 0019): the
// fold from one PR's normalised review-thread connection into its
// unresolved-thread count. Truncation is conservative — a connection reporting
// more thread pages than fetched is treated as HAVING unresolved threads,
// never as resolved: the gate holds until a fetch proves closure.
func TestUnresolvedCount(t *testing.T) {
	resolved := forge.ReviewThread{IsResolved: true}
	unresolved := forge.ReviewThread{IsResolved: false}

	tests := []struct {
		name    string
		threads forge.ReviewThreads
		want    int
	}{
		{
			name:    "no threads folds to zero — nothing to resolve, the gate is open",
			threads: forge.ReviewThreads{},
			want:    0,
		},
		{
			name:    "all fetched threads resolved folds to zero",
			threads: forge.ReviewThreads{Nodes: []forge.ReviewThread{resolved, resolved}},
			want:    0,
		},
		{
			name:    "unresolved threads are counted",
			threads: forge.ReviewThreads{Nodes: []forge.ReviewThread{resolved, unresolved, unresolved}},
			want:    2,
		},
		{
			name:    "further pages with every fetched thread resolved still holds — at least one unresolved, never resolved",
			threads: forge.ReviewThreads{Nodes: []forge.ReviewThread{resolved}, HasMorePages: true},
			want:    1,
		},
		{
			name:    "further pages on an empty fetched page holds too (defensive)",
			threads: forge.ReviewThreads{HasMorePages: true},
			want:    1,
		},
		{
			name:    "further pages never shrink an already-unresolved count",
			threads: forge.ReviewThreads{Nodes: []forge.ReviewThread{unresolved, unresolved}, HasMorePages: true},
			want:    2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, forge.UnresolvedCount(tt.threads))
		})
	}
}
