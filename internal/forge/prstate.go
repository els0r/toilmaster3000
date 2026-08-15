package forge

// PRState is the live forge lifecycle of an already-approved PR, surfaced on
// the Approval Feed so the user can see what became of the robot's approval. It
// collapses the normalised (lifecycle, mergedAt) pair into one of four buckets.
type PRState string

const (
	// PRStateOpen is an approved PR not yet merged.
	PRStateOpen PRState = "open"
	// PRStateMerged is the happy outcome (lifecycle merged, or closed with a mergedAt).
	PRStateMerged PRState = "merged"
	// PRStateClosed is a PR closed WITHOUT merging — a PR the robot approved that a
	// human then rejected/abandoned. A deliberately-surfaced false-positive signal.
	PRStateClosed PRState = "closed"
	// PRStateUnknown is "not checked yet": the default before the first successful
	// refresh (e.g. just after a restart) and the defensive fallback for an
	// unrecognised lifecycle. Rendered neutrally — never guessed as open.
	PRStateUnknown PRState = "unknown"
)

// LifecycleState is the NEUTRAL lifecycle token an adapter normalises its
// forge's raw PR-state vocabulary into (GitHub's OPEN|MERGED|CLOSED, GitLab's
// opened|merged|closed). The zero value is LifecycleUnknown, so a value the
// adapter did not recognise can never read as open.
type LifecycleState string

const (
	// LifecycleUnknown is a state the adapter did not recognise. It is the zero
	// value on purpose: an unmapped token must degrade to unknown, never to a guess.
	LifecycleUnknown LifecycleState = ""
	// LifecycleOpen is a PR still open on the forge.
	LifecycleOpen LifecycleState = "open"
	// LifecycleMerged is a PR the forge reports as merged.
	LifecycleMerged LifecycleState = "merged"
	// LifecycleClosed is a PR the forge reports as closed. Whether that means
	// merged is CollapsePRState's judgement, not the adapter's: some forges
	// report a merged PR's underlying state as closed with a mergedAt.
	LifecycleClosed LifecycleState = "closed"
)

// Lifecycle is one PR's normalised lifecycle as the batched PR-State refresh
// returns it: the neutral state token plus the forge's mergedAt timestamp
// verbatim. The adapter normalises into this; CollapsePRState does the judging
// into a PRState bucket.
type Lifecycle struct {
	State    LifecycleState
	MergedAt string
}

// CollapsePRState folds the normalised (lifecycle, mergedAt) pair into the
// display bucket. It is the pure decision — no I/O — mirroring AllGreen: the
// adapter only decodes and normalises the pair, this function judges it.
//
// merged is recognised both ways: the forge's own merged lifecycle, and the
// defensive closed-with-a-mergedAt (on GitHub a merged PR's underlying state is
// CLOSED). A closed PR with no mergedAt is closed-without-merging. Anything
// unrecognised is unknown — we never guess open.
func CollapsePRState(state LifecycleState, mergedAt string) PRState {
	switch state {
	case LifecycleOpen:
		return PRStateOpen
	case LifecycleMerged:
		return PRStateMerged
	case LifecycleClosed:
		if mergedAt != "" {
			return PRStateMerged
		}
		return PRStateClosed
	default:
		return PRStateUnknown
	}
}
