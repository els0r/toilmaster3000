package forge

// ReviewThread is one NORMALISED review thread of a PR — a resolvable
// conversation, and therefore the only species the Discussion gate reads (ADR
// 0019). Only the resolution flag survives normalisation: whether a thread is
// merely outdated is deliberately not carried, because outdated is not
// resolved — only the explicit resolve click closes a conversation.
type ReviewThread struct {
	IsResolved bool
}

// ReviewThreads is one PR's normalised review-thread connection: the fetched
// page of threads plus whether the forge reported more pages than were
// fetched. The adapter normalises into this; UnresolvedCount does the judging.
// The zero value (no threads, no further pages) is a PR with nothing to
// resolve.
type ReviewThreads struct {
	// Nodes is the fetched page of review threads.
	Nodes []ReviewThread
	// HasMorePages reports that the PR carries more threads than the fetched
	// page holds.
	HasMorePages bool
}

// UnresolvedCount folds one PR's normalised review-thread connection into its
// unresolved-thread count — the Discussion-gate predicate (ADR 0019): zero
// means the gate is open, >=1 holds the merge. It is the pure decision — no
// I/O — sibling to AllGreen and CollapsePRState.
//
// Truncation is conservative: a connection reporting more pages than fetched
// is treated as HAVING unresolved threads (the count reads at least 1), never
// as resolved — an unfetched page may hold an open conversation, and the robot
// must hold rather than merge on a guess.
func UnresolvedCount(threads ReviewThreads) int {
	n := 0
	for _, th := range threads.Nodes {
		if !th.IsResolved {
			n++
		}
	}
	if n == 0 && threads.HasMorePages {
		return 1
	}
	return n
}
