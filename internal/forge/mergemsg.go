package forge

import "strings"

// ReviewState is the NEUTRAL state of ONE review on a PR, as the merge-info
// fetch returns it. It is deliberately separate from ReviewDecision: that one
// is the forge's PR-level rollup, this one is a single reviewer's verdict.
type ReviewState string

const (
	// ReviewStateCommented is a review that carries no verdict — a comment, a
	// dismissed review, a pending one. It is the zero value, so anything the
	// adapter did not recognise can never reach the approver trailer.
	ReviewStateCommented ReviewState = ""
	// ReviewStateApproved is an approving review — the only state ApprovedBy counts.
	ReviewStateApproved ReviewState = "approved"
	// ReviewStateChangesRequested is a review asking for changes.
	ReviewStateChangesRequested ReviewState = "changes_requested"
)

// Review is one normalised review on a PR: the reviewer's login and the neutral
// review state. ApprovedBy judges which reviews reach the commit trailer.
type Review struct {
	Author string
	State  ReviewState
}

// MergeDetails is the live merge-time view of a PR — title, body, and reviews
// fetched by ONE per-PR call at the moment of merge (the sanctioned per-PR
// call, ADR 0008/0016) — so the commit message is built from the PR
// description as it is NOW, never a stale arm-time copy.
type MergeDetails struct {
	Title   string
	Body    string
	Reviews []Review
}

// approvedByPrefix is the commit-body trailer marker gh-land stamps; parity
// means tm3k emits the identical bytes (ADR 0016).
const approvedByPrefix = "Approved by: "

// CommitMessage builds the gh-land-parity squash commit message from the live
// merge-time details (ADR 0016): subject = PR title verbatim; body = PR
// description + "\n" + "Approved by: <logins>" — the trailer built by
// ApprovedBy from the approving reviews. An empty description is the trailer
// alone; no approving reviews leaves the body untouched (no dangling trailer).
// It is the pure judgement sibling to AllGreen/ClassifyOutboundStage: the
// adapter only decodes and normalises the details, this function composes the
// message.
func CommitMessage(d MergeDetails) (subject, body string) {
	approvers := ApprovedBy(d.Reviews)
	if len(approvers) == 0 {
		return d.Title, d.Body
	}
	trailer := approvedByPrefix + strings.Join(approvers, ", ")
	if d.Body == "" {
		return d.Title, trailer
	}
	return d.Title, d.Body + "\n" + trailer
}

// ApprovedBy folds the reviews into the approver login list the trailer joins
// and the merge ledger persists as approved_by[]: only approving reviews
// count, an empty login is nobody, duplicates collapse, and first-seen order
// is preserved.
//
// The logins arrive already reduced to bare identities — an adapter maps its
// forge's own spellings (GitHub's "app/" bot prefix, the org's "_osag" EMU
// suffix) before they reach here, which is why the dedupe below is a plain
// comparison and still makes "alice" and "alice_osag" one approver. Knowing
// those spellings is decode and belongs to the adapter; deciding that two
// spellings are one person is judge and belongs here (ADR 0030 §3).
func ApprovedBy(reviews []Review) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range reviews {
		if r.State != ReviewStateApproved {
			continue
		}
		if r.Author == "" || seen[r.Author] {
			continue
		}
		seen[r.Author] = true
		out = append(out, r.Author)
	}
	return out
}
