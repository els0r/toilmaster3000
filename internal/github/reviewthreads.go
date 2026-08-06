package github

// ReviewThread is one review-thread node of a PR — the only resolvable comment
// species GitHub has, and therefore the only species the Discussion gate reads
// (ADR 0019). isResolved exists solely in GraphQL's reviewThreads connection,
// which is why the threads call is GraphQL. isOutdated is deliberately not
// decoded: outdated ≠ resolved — only the explicit resolve click closes a
// conversation.
type ReviewThread struct {
	IsResolved bool `json:"isResolved"`
}

// RawReviewThreads is one PR's reviewThreads connection as the GraphQL search
// emits it: the fetched page of thread nodes plus whether more pages exist.
// The seam only decodes this; UnresolvedCount does the judging. The zero value
// (no threads, no further pages) is a PR with nothing to resolve.
type RawReviewThreads struct {
	// Nodes is the fetched page of review threads.
	Nodes []ReviewThread
	// HasMorePages is the connection's pageInfo.hasNextPage: the PR carries
	// more threads than the fetched page.
	HasMorePages bool
}

// UnresolvedCount folds one PR's decoded reviewThreads connection into its
// unresolved-thread count — the Discussion-gate predicate (ADR 0019): zero
// means the gate is open, ≥1 holds the merge. It is the pure decision — no
// I/O — sibling to AllGreen and CollapsePRState.
//
// Truncation is conservative: a connection reporting more pages than fetched
// is treated as HAVING unresolved threads (the count reads at least 1), never
// as resolved — an unfetched page may hold an open conversation, and the robot
// must hold rather than merge on a guess.
func UnresolvedCount(raw RawReviewThreads) int {
	n := 0
	for _, th := range raw.Nodes {
		if !th.IsResolved {
			n++
		}
	}
	if n == 0 && raw.HasMorePages {
		return 1
	}
	return n
}
