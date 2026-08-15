package forge

// CheckState is the NEUTRAL verdict of one check entry — the whole vocabulary
// the all-green gate and the outbound stage fold judge. Each forge adapter
// normalises its own raw check shapes into these three values (ADR 0030 §3):
// GitHub's heterogeneous rollup of CheckRun status/conclusion pairs and legacy
// StatusContext states, GitLab's single pipeline status. The folds never see a
// forge's raw strings, so a mis-mapped status is the adapter's bug and is
// caught by the adapter's own normalisation table (ADR 0030 §10).
type CheckState string

const (
	// CheckPass is a finished entry the forge considers successful (or
	// deliberately inconsequential — GitHub's SKIPPED/NEUTRAL).
	CheckPass CheckState = "pass"
	// CheckFail is a finished entry that did not succeed. It is the
	// red-vs-running discriminator on the outbound side: the author's action
	// is to go fix it.
	CheckFail CheckState = "fail"
	// CheckPending is an entry that has not finished. It blocks green exactly
	// as a fail does, but the author's action is to wait, not to fix.
	CheckPending CheckState = "pending"
)

// Check is one NORMALISED entry of a PR's check rollup. It carries the neutral
// verdict and nothing else: the raw discriminators an adapter decoded to reach
// it never leave that adapter.
//
// The zero value carries no verdict. It is neither pass nor fail, so it blocks
// the all-green gate and reads as a wait rather than as red — the conservative
// reading for an auto-approver.
type Check struct {
	State CheckState
}

// AllGreen reports whether a PR's check rollup is all-green: true IFF there is
// at least one entry AND every entry passes (zero fails, zero pendings). It is
// the all-green eligibility gate's pure decision — no I/O.
//
// An EMPTY rollup is NOT green: a pipeline that never ran is no signal, and an
// auto-approver must never fire on no signal (this closes the new-PR window).
//
// Fails and pendings are treated identically here — both make the PR
// not-all-green — so the fold only needs to recognise pass and reject the rest.
func AllGreen(checks []Check) bool {
	if len(checks) == 0 {
		return false
	}
	for _, c := range checks {
		if c.State != CheckPass {
			return false
		}
	}
	return true
}
