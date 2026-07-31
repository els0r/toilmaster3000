package github

// OutboundStage is the terminal bucket of one authored PR in the outbound
// funnel partition (CONTEXT "Outbound"): every authored PR lands in EXACTLY one
// stage, with precedence draft > not-green (red | running) > changes-requested
// > awaiting-approval > ready.
type OutboundStage string

const (
	// OutboundStageDraft is a draft PR — the author's action is to finish it.
	// Unlike inbound, draft is an outbound STAGE, not an eligibility gate.
	OutboundStageDraft OutboundStage = "draft"
	// OutboundStageRed is a not-green pipeline with at least one failed check —
	// the author's action is to go fix CI.
	OutboundStageRed OutboundStage = "red"
	// OutboundStageRunning is a not-green pipeline with no failed check (at
	// least one still pending, or none registered yet) — the author's action is
	// to wait. An empty rollup lands here, not in red: checks that have not
	// registered are a wait signal, not a go-fix one.
	OutboundStageRunning OutboundStage = "running"
	// OutboundStageChangesRequested is a green pipeline with reviewDecision
	// CHANGES_REQUESTED — the author's action is to address the feedback. Its
	// own stage, not a badge: the wait is on the author, not a reviewer.
	OutboundStageChangesRequested OutboundStage = "changes_requested"
	// OutboundStageAwaitingApproval is a green pipeline with no approval yet —
	// waiting on a reviewer.
	OutboundStageAwaitingApproval OutboundStage = "awaiting_approval"
	// OutboundStageReady is a green pipeline with reviewDecision APPROVED —
	// waiting only on the author (or, in a later slice, the armed merge). A
	// conflicted Ready PR STAYS in Ready carrying its conflict state: mergeable
	// is a merge precondition, never a stage boundary.
	OutboundStageReady OutboundStage = "ready"
)

// gh reviewDecision values the stage fold judges.
const (
	reviewDecisionApproved         = "APPROVED"
	reviewDecisionChangesRequested = "CHANGES_REQUESTED"
)

// MergeableConflicting is gh's mergeable value for a PR whose branch conflicts
// with its base. The wire layer derives the Ready row's conflict marker from
// it; the stage fold never reads mergeable.
const MergeableConflicting = "CONFLICTING"

// StatusContext pending states (the legacy commit-status analog of a CheckRun
// that has not completed).
const (
	statePending  = "PENDING"
	stateExpected = "EXPECTED"
)

// ClassifyOutboundStage folds one authored PR into its outbound stage. It is
// the pure decision — no I/O — sibling to AllGreen and CollapsePRState: the gh
// seam only decodes the PR, this function judges it. Precedence top-down:
// draft > not-green (red | running) > changes-requested > awaiting-approval >
// ready. mergeable is deliberately not consulted — it is a merge precondition,
// not a stage boundary (a conflicted Ready PR stays in Ready).
func ClassifyOutboundStage(pr PR) OutboundStage {
	if pr.IsDraft {
		return OutboundStageDraft
	}
	if !AllGreen(pr.Checks) {
		if hasFailingCheck(pr.Checks) {
			return OutboundStageRed
		}
		return OutboundStageRunning
	}
	switch pr.ReviewDecision {
	case reviewDecisionChangesRequested:
		return OutboundStageChangesRequested
	case reviewDecisionApproved:
		return OutboundStageReady
	default:
		// REVIEW_REQUIRED, empty, or anything unrecognised: no approval yet.
		return OutboundStageAwaitingApproval
	}
}

// hasFailingCheck reports whether any rollup entry has FAILED — a terminal
// non-pass, as opposed to a still-pending entry. It is the red-vs-running
// discriminator: AllGreen collapses fail and pending (both block approval), but
// an outbound author must distinguish "go fix CI" from "wait".
func hasFailingCheck(checks []Check) bool {
	for _, c := range checks {
		if isFail(c) {
			return true
		}
	}
	return false
}

// isFail reports whether one rollup entry is in the fail bucket: a COMPLETED
// CheckRun with a non-pass conclusion (FAILURE, CANCELLED, TIMED_OUT, ...), or
// a StatusContext in a terminal non-success state (FAILURE/ERROR). A pending
// entry (CheckRun not yet COMPLETED, StatusContext PENDING/EXPECTED) is not a
// fail. An unrecognised entry counts as fail — defensively drawing the author's
// eye rather than reading as a harmless wait.
func isFail(c Check) bool {
	switch c.Typename {
	case typeCheckRun:
		if c.Status != statusCompleted {
			return false // pending, not failed
		}
		return !isPass(c)
	case typeStatusContext:
		switch c.State {
		case stateSuccess, statePending, stateExpected:
			return false
		default:
			return true // FAILURE, ERROR, or anything unrecognised
		}
	default:
		return true
	}
}
