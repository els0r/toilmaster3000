package github

import "github.com/els0r/toilmaster3000/internal/forge"

// ghListItem mirrors the JSON gh emits for one `gh pr list --json ...` entry.
// Author is nested under author.login; additions/deletions/changedFiles are
// top-level diff counts. It is decode-only and package-private: nothing above
// the seam may ever hold one (ADR 0030 Layout).
type ghListItem struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Additions    int  `json:"additions"`
	Deletions    int  `json:"deletions"`
	ChangedFiles int  `json:"changedFiles"`
	IsDraft      bool `json:"isDraft"`
	// StatusCheckRollup is gh's heterogeneous array of check entries for the PR,
	// pulled in the same single list call (no per-PR N+1).
	StatusCheckRollup []ghCheck `json:"statusCheckRollup"`
	// ReviewDecision is gh's coarse review-state signal, pulled in the same single
	// list call (no per-PR N+1) to drive the approved-elsewhere soft dedup.
	ReviewDecision string `json:"reviewDecision"`
	// Mergeable is gh's mergeability signal, requested only by the authored
	// (outbound) list call; the inbound call leaves it empty.
	Mergeable string `json:"mergeable"`
	// HeadRefOid is the PR head's commit SHA, pulled in the same single list
	// call — the per-head key of the Screen verdict store (ADR 0022).
	HeadRefOid string `json:"headRefOid"`
	// Files is gh's changed-file array, requested only by the inbound candidate
	// call. Each entry's path spells `path` here — NOT the `filename` of the
	// REST files API behind Diff. Only the path is decoded: the batch field
	// carries no patch, so it can never stand in for the on-demand diff fetch
	// (ADR 0008 stays as it is).
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

// normalizeReviewDecision maps gh's reviewDecision to the neutral rollup.
// REVIEW_REQUIRED, the empty string, and anything GitHub adds later all
// collapse to "undecided" — the same branch they always shared, now named.
// Nothing degrades to approved.
//
// The empty string here means GitHub reported NO decision, not a field nobody
// asked for: reviewDecision rides listJSONFields, so BOTH per-cycle pulls
// request it. That matters — ADR 0013's soft dedup reads this field on the
// INBOUND pull to leave an already-approved PR alone, and it would silently
// stop working if a pull ever dropped the field.
func normalizeReviewDecision(raw string) forge.ReviewDecision {
	switch raw {
	case rawApproved:
		return forge.ReviewApproved
	case rawChangesRequested:
		return forge.ReviewChangesRequested
	default:
		return forge.ReviewNone
	}
}

// gh mergeable values. UNKNOWN (GitHub still computing) is deliberately absent:
// it takes the same default branch as an unrequested or unrecognised value.
const (
	mergeableMergeable   = "MERGEABLE"
	mergeableConflicting = "CONFLICTING"
)

// normalizeMergeable maps gh's mergeable to the neutral mergeability. UNKNOWN,
// the empty string the inbound pull leaves behind (it never requests the
// field), and anything GitHub adds later all collapse to unknown — which
// blocks a merge without moving a stage, and is retried next cycle.
func normalizeMergeable(raw string) forge.Mergeability {
	switch raw {
	case mergeableMergeable:
		return forge.MergeableMergeable
	case mergeableConflicting:
		return forge.MergeableConflicting
	default:
		return forge.MergeableUnknown
	}
}

// normalizePR maps one decoded list item into a neutral PR. It is the whole of
// the inbound and outbound pulls' normalisation: both calls decode into the
// same shape and differ only in which fields they asked gh for, so the decode
// never forks and neither does this.
func normalizePR(it ghListItem) forge.PR {
	checks, failing := normalizeChecks(it.StatusCheckRollup)
	var files []string
	for _, f := range it.Files {
		files = append(files, f.Path)
	}
	return forge.PR{
		Number:         it.Number,
		Title:          it.Title,
		Author:         it.Author.Login,
		URL:            it.URL,
		Additions:      it.Additions,
		Deletions:      it.Deletions,
		ChangedFiles:   it.ChangedFiles,
		IsDraft:        it.IsDraft,
		Checks:         checks,
		FailingChecks:  failing,
		ReviewDecision: normalizeReviewDecision(it.ReviewDecision),
		Mergeable:      normalizeMergeable(it.Mergeable),
		Files:          files,
		HeadSHA:        it.HeadRefOid,
	}
}

// normalizePRs maps a whole decoded list response into neutral PRs.
func normalizePRs(items []ghListItem) []forge.PR {
	prs := make([]forge.PR, 0, len(items))
	for _, it := range items {
		prs = append(prs, normalizePR(it))
	}
	return prs
}
