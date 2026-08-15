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
	if len(raw) > 0 {
		// Pre-sized, but only when there is something to size for: an empty
		// rollup must yield a NIL slice, not an empty one. "No rollup" and "a
		// rollup that came back empty" are the same thing to every caller, and
		// the recorded-response tests pin nil.
		checks = make([]forge.Check, 0, len(raw))
	}
	for _, c := range raw {
		state := normalizeCheckState(c)
		if state != forge.CheckPass {
			failing++
		}
		checks = append(checks, forge.Check{State: state})
	}
	return checks, failing
}

// GitHub spells an approval and a request for changes the same way wherever it
// reports one — the PR-level reviewDecision rollup and an individual review's
// state — so the two tokens are declared once here. The MAPPINGS stay separate
// (normalizeReviewDecision, normalizeReviewState): their raw domains differ
// (only the rollup has REVIEW_REQUIRED; only a review has COMMENTED and
// DISMISSED) and they produce different neutral types.
const (
	rawApproved         = "APPROVED"
	rawChangesRequested = "CHANGES_REQUESTED"
)
