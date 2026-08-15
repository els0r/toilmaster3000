package forge

// OutboundStage is the terminal bucket of one authored PR in the outbound
// funnel partition (CONTEXT "Outbound"): every authored PR lands in EXACTLY one
// stage, with precedence draft > not-green (red | running) > changes-requested
// > awaiting-approval > in-discussion > ready.
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
	// OutboundStageChangesRequested is a green pipeline with a review decision of
	// changes-requested — the author's action is to address the feedback. Its
	// own stage, not a badge: the wait is on the author, not a reviewer.
	OutboundStageChangesRequested OutboundStage = "changes_requested"
	// OutboundStageAwaitingApproval is a green pipeline with no approval yet —
	// waiting on a reviewer. Unresolved threads are inert here: precedence
	// names the primary blocker, and that is the missing approval.
	OutboundStageAwaitingApproval OutboundStage = "awaiting_approval"
	// OutboundStageInDiscussion is a green, approved pipeline with at least one
	// UNRESOLVED review thread — approved with nits, waiting on the conversation
	// to close (ADR 0019). It sits between Awaiting Approval and Ready, and the
	// merge step never walks it: the partition IS the Discussion gate.
	OutboundStageInDiscussion OutboundStage = "in_discussion"
	// OutboundStageReady is a green, approved pipeline with zero unresolved
	// review threads — waiting only on the author (or the armed merge). A
	// conflicted Ready PR STAYS in Ready carrying its mergeability: mergeable is
	// a merge precondition, never a stage boundary.
	OutboundStageReady OutboundStage = "ready"
)

// outboundStages is the stage set in funnel order — which is also
// ClassifyOutboundStage's precedence order. It is the ONE declaration of the
// set (ADR 0025); everything downstream keys off the classifier's tag instead
// of re-enumerating it.
var outboundStages = []OutboundStage{
	OutboundStageDraft,
	OutboundStageRed,
	OutboundStageRunning,
	OutboundStageChangesRequested,
	OutboundStageAwaitingApproval,
	OutboundStageInDiscussion,
	OutboundStageReady,
}

// OutboundStages returns the outbound stage set in funnel order. It is a
// function returning a COPY, not an exported slice var, for the reason
// engine.Outbound() copies its slices: an exported slice is mutable by any
// importer, and this one is the declaration of record.
//
// The set is open by design — the classifier may grow a stage — so the
// discipline is: loops that must be COMPLETE range the partition map; loops
// that must be ORDERED range this list. A forge test drives the fold over its
// full input space and asserts set-equality with this list in both directions,
// so neither an unlisted classifier branch nor an unreachable constant
// survives.
func OutboundStages() []OutboundStage {
	return append([]OutboundStage(nil), outboundStages...)
}

// ClassifyOutboundStage folds one authored PR plus its unresolved-review-
// thread count (judged by UnresolvedCount from the cycle's threads call, ADR
// 0019) into its outbound stage. It is the pure decision — no I/O — sibling to
// AllGreen and CollapsePRState: the adapter only decodes and normalises, this
// function judges. Precedence top-down: draft > not-green (red | running) >
// changes-requested > awaiting-approval > in-discussion > ready. The
// unresolved count only ever splits the approved tail — In Discussion vs
// Ready — and is inert above it: precedence names the primary blocker.
// Mergeability is deliberately not consulted — it is a merge precondition, not
// a stage boundary (a conflicted PR stays in its stage).
func ClassifyOutboundStage(pr PR, unresolvedThreads int) OutboundStage {
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
	case ReviewChangesRequested:
		return OutboundStageChangesRequested
	case ReviewApproved:
		if unresolvedThreads > 0 {
			return OutboundStageInDiscussion
		}
		return OutboundStageReady
	default:
		// Undecided, or anything unrecognised: no approval yet.
		return OutboundStageAwaitingApproval
	}
}

// hasFailingCheck reports whether any rollup entry FAILED — a terminal
// non-pass, as opposed to a still-pending entry. It is the red-vs-running
// discriminator: AllGreen collapses fail and pending (both block approval), but
// an outbound author must distinguish "go fix CI" from "wait".
func hasFailingCheck(checks []Check) bool {
	for _, c := range checks {
		if c.State == CheckFail {
			return true
		}
	}
	return false
}
