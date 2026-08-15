package github

import (
	"strings"

	"github.com/els0r/toilmaster3000/internal/forge"
)

// ghViewItem mirrors the JSON gh emits for `gh pr view --json
// title,body,reviews`. Reviewer logins nest under author.login, like the list
// call's author. Decode-only and package-private.
type ghViewItem struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Reviews []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		State string `json:"state"`
	} `json:"reviews"`
}

// gh review states. Everything else GitHub emits (COMMENTED, DISMISSED,
// PENDING, and whatever comes next) carries no verdict and takes the default.
const (
	reviewStateApproved         = "APPROVED"
	reviewStateChangesRequested = "CHANGES_REQUESTED"
)

// normalizeReviewState maps gh's per-review state to the neutral one. Only an
// explicit APPROVED becomes approved: a state the adapter does not recognise
// can never reach the commit trailer.
func normalizeReviewState(raw string) forge.ReviewState {
	switch raw {
	case reviewStateApproved:
		return forge.ReviewStateApproved
	case reviewStateChangesRequested:
		return forge.ReviewStateChangesRequested
	default:
		return forge.ReviewStateCommented
	}
}

// botLoginPrefix is GitHub's marker on a GitHub App's reviewer login; emuSuffix
// is the org's Enterprise Managed User suffix on a human's. Both are GitHub
// spellings of an identity, not part of the identity, and gh-land strips both
// when it builds the "Approved by:" trailer — parity means tm3k strips exactly
// these two and nothing else (ADR 0016).
const (
	botLoginPrefix = "app/"
	emuSuffix      = "_osag"
)

// normalizeLogin maps a raw GitHub reviewer login to the bare identity. It is
// decode, not judge: which spellings GitHub uses is this adapter's knowledge,
// while deciding that two spellings are ONE approver stays with the shared
// fold (ADR 0030 §3). A login that is nothing but a prefix normalises to the
// empty string, which the fold then skips.
func normalizeLogin(raw string) string {
	return strings.TrimSuffix(strings.TrimPrefix(raw, botLoginPrefix), emuSuffix)
}

// normalizeMergeDetails maps the decoded merge-time view into the neutral
// details CommitMessage composes from, with each reviewer login reduced to its
// bare identity.
func normalizeMergeDetails(item ghViewItem) forge.MergeDetails {
	details := forge.MergeDetails{Title: item.Title, Body: item.Body}
	for _, r := range item.Reviews {
		details.Reviews = append(details.Reviews, forge.Review{
			Author: normalizeLogin(r.Author.Login),
			State:  normalizeReviewState(r.State),
		})
	}
	return details
}

// ghFileDiff is one changed file as the GitHub REST files API emits it. Note
// the field spelling: `filename` here, where gh's list `files` objects carry
// `path`. GitHub omits patch for binary and over-large files, so Patch decodes
// empty for those. Decode-only and package-private.
type ghFileDiff struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

// normalizeFileDiffs maps the decoded files response into neutral FileDiffs.
// The status token crosses verbatim — added|modified|removed|renamed is a
// value the Diff card renders, not a vocabulary any fold judges.
func normalizeFileDiffs(raw []ghFileDiff) []forge.FileDiff {
	files := make([]forge.FileDiff, 0, len(raw))
	for _, f := range raw {
		files = append(files, forge.FileDiff{
			Filename:  f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Patch:     f.Patch,
		})
	}
	return files
}
