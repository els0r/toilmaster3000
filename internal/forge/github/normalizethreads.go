package github

import "github.com/els0r/toilmaster3000/internal/forge"

// ghThreadsResponse mirrors the envelope `gh api graphql` returns for the
// batched review-threads search: one search node per PR of the authored pull,
// each carrying its number and the first page of its reviewThreads connection.
// Decode-only and package-private.
type ghThreadsResponse struct {
	Data struct {
		Search struct {
			Nodes []ghThreadsNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

// ghThreadsNode is one PR's search node: its number and its reviewThreads page.
type ghThreadsNode struct {
	Number        int `json:"number"`
	ReviewThreads struct {
		Nodes    []ghReviewThread `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"reviewThreads"`
}

// ghReviewThread is one review-thread node — the only resolvable comment
// species GitHub has, and therefore the only species the Discussion gate reads
// (ADR 0019). isOutdated is deliberately neither requested nor decoded:
// outdated is not resolved — only the explicit resolve click closes a
// conversation.
type ghReviewThread struct {
	IsResolved bool `json:"isResolved"`
}

// normalizeReviewThreads maps the decoded search nodes into the number->
// ReviewThreads map the outbound partition reads. The connection's
// pageInfo.hasNextPage crosses as HasMorePages, which is the whole point of
// carrying a struct rather than a slice: UnresolvedCount reads a truncated
// fetch as a hold.
func normalizeReviewThreads(nodes []ghThreadsNode) map[int]forge.ReviewThreads {
	threads := make(map[int]forge.ReviewThreads, len(nodes))
	for _, n := range nodes {
		fetched := make([]forge.ReviewThread, 0, len(n.ReviewThreads.Nodes))
		for _, th := range n.ReviewThreads.Nodes {
			fetched = append(fetched, forge.ReviewThread{IsResolved: th.IsResolved})
		}
		threads[n.Number] = forge.ReviewThreads{
			Nodes:        fetched,
			HasMorePages: n.ReviewThreads.PageInfo.HasNextPage,
		}
	}
	return threads
}
