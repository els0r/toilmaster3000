// Package github is the GitHub forge adapter. It owns GitHub's raw decode
// shapes and its transport — `gh pr list --json` plus the one `gh api graphql`
// for review threads — and NORMALISES everything it decodes into the neutral
// vocabulary of internal/forge before it returns. GitHub's own words
// (__typename, COMPLETED, APPROVED, MERGEABLE, ...) exist nowhere outside this
// package: the pure folds judge the neutral values, never these (ADR 0030 §3).
package github

import "github.com/els0r/toilmaster3000/internal/forge"

// ghCheck is one entry of a PR's statusCheckRollup, exactly as gh emits it. The
// rollup is heterogeneous: GitHub Checks API runs decode as __typename
// "CheckRun" (carrying status/conclusion), legacy commit statuses as
// "StatusContext" (carrying state). This struct only DECODES one entry;
// normalizeCheckState maps it to the neutral verdict.
type ghCheck struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// Rollup entry discriminators (gh's __typename).
const (
	typeCheckRun      = "CheckRun"
	typeStatusContext = "StatusContext"
)

// CheckRun statuses and conclusions.
const (
	statusCompleted = "COMPLETED"

	conclusionSuccess = "SUCCESS"
	conclusionSkipped = "SKIPPED"
	conclusionNeutral = "NEUTRAL"
)

// StatusContext states — the legacy commit-status analogs of a CheckRun's
// success and of one that has not completed.
const (
	stateSuccess  = "SUCCESS"
	statePending  = "PENDING"
	stateExpected = "EXPECTED"
)

// normalizeCheckState maps ONE raw rollup entry to its neutral verdict. It is
// pure, and it is the whole of GitHub's check opinion:
//
//   - pass: a COMPLETED CheckRun concluding SUCCESS/SKIPPED/NEUTRAL, or a
//     StatusContext in state SUCCESS.
//   - pending: a CheckRun that has not reached COMPLETED (its conclusion is not
//     yet meaningful, whatever it holds), or a StatusContext still
//     PENDING/EXPECTED.
//   - fail: everything else — any other CheckRun conclusion (FAILURE,
//     CANCELLED, TIMED_OUT, ACTION_REQUIRED, STARTUP_FAILURE, STALE, ...), a
//     StatusContext in a terminal non-success state (FAILURE/ERROR), and any
//     entry type or state GitHub adds later. An unrecognised entry counting as
//     fail is deliberate: it draws the author's eye rather than reading as a
//     harmless wait.
func normalizeCheckState(c ghCheck) forge.CheckState {
	switch c.Typename {
	case typeCheckRun:
		if c.Status != statusCompleted {
			return forge.CheckPending
		}
		switch c.Conclusion {
		case conclusionSuccess, conclusionSkipped, conclusionNeutral:
			return forge.CheckPass
		default:
			return forge.CheckFail
		}
	case typeStatusContext:
		switch c.State {
		case stateSuccess:
			return forge.CheckPass
		case statePending, stateExpected:
			return forge.CheckPending
		default:
			return forge.CheckFail
		}
	default:
		return forge.CheckFail
	}
}

// normalizeChecks maps a whole raw rollup to its neutral entries and, beside
// them, the count of non-passing ones. The count rides out of the adapter
// rather than being folded later because cardinality is a forge fact (ADR 0030
// §6): GitHub reports N rollup entries per PR where GitLab reports one
// pipeline verdict, so only the adapter can say what "N checks failing" means
// on its own forge. GitHub's answer is unchanged from when it was a fold —
// every entry that is not a pass, fails and pendings alike.
//
// An empty rollup yields no entries and a count of zero: nothing ran, so
// nothing is failing. Emptiness is the all-green gate's concern.
func normalizeChecks(raw []ghCheck) (checks []forge.Check, failing int) {
	for _, c := range raw {
		state := normalizeCheckState(c)
		if state != forge.CheckPass {
			failing++
		}
		checks = append(checks, forge.Check{State: state})
	}
	return checks, failing
}

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

// gh reviewDecision values.
const (
	reviewDecisionApproved         = "APPROVED"
	reviewDecisionChangesRequested = "CHANGES_REQUESTED"
)

// normalizeReviewDecision maps gh's reviewDecision to the neutral rollup.
// REVIEW_REQUIRED, the empty string the inbound pull leaves behind, and
// anything GitHub adds later all collapse to "undecided" — the same branch
// they always shared, now named. Nothing degrades to approved.
func normalizeReviewDecision(raw string) forge.ReviewDecision {
	switch raw {
	case reviewDecisionApproved:
		return forge.ReviewApproved
	case reviewDecisionChangesRequested:
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
