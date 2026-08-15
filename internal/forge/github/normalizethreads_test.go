package github

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestNormalizeReviewThreads runs the threads decode+normalise path over a
// RECORDED `gh api graphql` search response — the cycle's third batched call
// (ADR 0019). GraphQL is forced, not chosen: isResolved exists only in the
// reviewThreads connection.
//
// Normalisation here is thin by design: the resolution flag is already a
// boolean, so what the adapter really carries across is the connection's
// pageInfo.hasNextPage, which UnresolvedCount reads as "hold". isOutdated is
// deliberately never requested nor decoded — outdated is not resolved.
func TestNormalizeReviewThreads(t *testing.T) {
	out := decodeFixture[ghThreadsResponse](t, "review_threads.json")
	require.Len(t, out.Data.Search.Nodes, 3, "the fixture is the recorded response, not a subset")

	require.Equal(t, map[int]forge.ReviewThreads{
		21: {Nodes: []forge.ReviewThread{{IsResolved: true}, {IsResolved: false}}},
		22: {Nodes: []forge.ReviewThread{}, HasMorePages: true},
		23: {Nodes: []forge.ReviewThread{{IsResolved: true}, {IsResolved: true}}},
	}, normalizeReviewThreads(out.Data.Search.Nodes))
}

// TestNormalizeReviewThreadsFeedsUnresolvedCount closes the loop between the
// adapter's mapping and the Discussion gate's shared fold: a truncated page
// must reach the gate as a HOLD, and a fully-resolved one must open it.
func TestNormalizeReviewThreadsFeedsUnresolvedCount(t *testing.T) {
	out := decodeFixture[ghThreadsResponse](t, "review_threads.json")
	threads := normalizeReviewThreads(out.Data.Search.Nodes)

	require.Equal(t, 1, forge.UnresolvedCount(threads[21]), "one open conversation holds")
	require.Equal(t, 1, forge.UnresolvedCount(threads[22]),
		"an empty fetched page reporting more pages holds — never resolve on a truncated fetch")
	require.Equal(t, 0, forge.UnresolvedCount(threads[23]), "all resolved opens the gate")
	require.Equal(t, 0, forge.UnresolvedCount(threads[999]),
		"a PR absent from the search carries no threads")
}
