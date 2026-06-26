package github

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

// StatusContext states.
const (
	stateSuccess = "SUCCESS"
)

// AllGreen reports whether a PR's check rollup is all-green: true IFF there is
// at least one entry AND every entry passes (zero fails, zero pendings). It is
// the all-green eligibility gate's pure decision — no I/O.
//
// An EMPTY rollup is NOT green: a pipeline that never ran is no signal, and an
// auto-approver must never fire on no signal (this closes the new-PR window).
//
// Each entry folds into one of three buckets:
//   - pass: a COMPLETED CheckRun concluding SUCCESS/SKIPPED/NEUTRAL, or a
//     StatusContext in state SUCCESS.
//   - fail: any other CheckRun conclusion (FAILURE, CANCELLED, TIMED_OUT,
//     ACTION_REQUIRED, STARTUP_FAILURE, STALE, ...), or a StatusContext not in
//     state SUCCESS that has reached a terminal non-success (FAILURE/ERROR).
//   - pending: a CheckRun not yet COMPLETED, or a StatusContext still PENDING/
//     EXPECTED.
//
// fails and pendings are treated identically here — both make the PR
// not-all-green — so the fold only needs to recognise pass and reject the rest.
func AllGreen(checks []Check) bool {
	if len(checks) == 0 {
		return false
	}
	for _, c := range checks {
		if !isPass(c) {
			return false
		}
	}
	return true
}

// FailingChecks counts a rollup's non-passing entries — the dropped-red
// station's "N checks failing" signal. It reuses the SAME classification as
// AllGreen (a non-pass entry is any fail OR pending), so the two never drift: an
// all-green rollup folds to 0, and an empty rollup folds to 0 too (a pipeline
// that never ran has nothing failing — emptiness is AllGreen's concern, not a
// failing check). It is a pure fold — no I/O.
func FailingChecks(checks []Check) int {
	n := 0
	for _, c := range checks {
		if !isPass(c) {
			n++
		}
	}
	return n
}

// isPass reports whether one rollup entry is in the pass bucket.
func isPass(c Check) bool {
	switch c.Typename {
	case typeCheckRun:
		if c.Status != statusCompleted {
			return false // pending: not yet completed
		}
		switch c.Conclusion {
		case conclusionSuccess, conclusionSkipped, conclusionNeutral:
			return true
		default:
			return false // fail
		}
	case typeStatusContext:
		return c.State == stateSuccess
	default:
		return false
	}
}
