package github

// PRState is the live GitHub lifecycle of an already-approved PR, surfaced on
// the Approval Feed so the user can see what became of the robot's approval. It
// collapses GitHub's raw (state, mergedAt) pair into one of four buckets.
type PRState string

const (
	// PRStateOpen is an approved PR not yet merged (GitHub state OPEN).
	PRStateOpen PRState = "open"
	// PRStateMerged is the happy outcome (state MERGED, or CLOSED with a mergedAt).
	PRStateMerged PRState = "merged"
	// PRStateClosed is a PR closed WITHOUT merging — a PR the robot approved that a
	// human then rejected/abandoned. A deliberately-surfaced false-positive signal.
	PRStateClosed PRState = "closed"
	// PRStateUnknown is "not checked yet": the default before the first successful
	// refresh (e.g. just after a restart) and the defensive fallback for an
	// unrecognised state. Rendered neutrally — never guessed as open.
	PRStateUnknown PRState = "unknown"
)

// RawPRState is the decode-only result of a per-PR `gh pr view` call: GitHub's
// raw state and mergedAt as gh emits them. The seam returns this verbatim;
// CollapsePRState does the judging into a PRState bucket.
type RawPRState struct {
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
}

// CollapsePRState folds GitHub's raw (state, mergedAt) pair into the display
// bucket. It is the pure decision — no I/O — mirroring AllGreen: the gh seam
// only decodes the pair, this function judges it.
//
// merged is recognised both ways: GitHub's own MERGED state, and the defensive
// CLOSED-with-a-mergedAt (a merged PR's underlying state is CLOSED). A CLOSED PR
// with no mergedAt is closed-without-merging. Anything unrecognised is unknown —
// we never guess open.
func CollapsePRState(state, mergedAt string) PRState {
	switch state {
	case "OPEN":
		return PRStateOpen
	case "MERGED":
		return PRStateMerged
	case "CLOSED":
		if mergedAt != "" {
			return PRStateMerged
		}
		return PRStateClosed
	default:
		return PRStateUnknown
	}
}
