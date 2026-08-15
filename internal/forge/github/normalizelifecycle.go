package github

import "github.com/els0r/toilmaster3000/internal/forge"

// ghPRStateItem is one entry of the batched PR-State refresh as gh emits it for
// `gh pr list --state all --json number,state,mergedAt`: the PR number
// alongside GitHub's raw lifecycle pair. Decode-only and package-private.
type ghPRStateItem struct {
	Number   int    `json:"number"`
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
}

// gh PR state values.
const (
	prStateOpen   = "OPEN"
	prStateMerged = "MERGED"
	prStateClosed = "CLOSED"
)

// normalizeLifecycleState maps gh's raw PR state to the neutral lifecycle
// token. It maps a NAME and nothing more: whether a closed PR carrying a
// mergedAt counts as merged is CollapsePRState's judgement, shared by every
// forge. Anything unrecognised is unknown — never guessed as open.
func normalizeLifecycleState(raw string) forge.LifecycleState {
	switch raw {
	case prStateOpen:
		return forge.LifecycleOpen
	case prStateMerged:
		return forge.LifecycleMerged
	case prStateClosed:
		return forge.LifecycleClosed
	default:
		return forge.LifecycleUnknown
	}
}

// normalizeLifecycles maps the whole decoded PR-State response into the
// number->Lifecycle map the engine intersects against today's feed. The
// mergedAt timestamp travels verbatim: it is data, not vocabulary.
func normalizeLifecycles(items []ghPRStateItem) map[int]forge.Lifecycle {
	states := make(map[int]forge.Lifecycle, len(items))
	for _, it := range items {
		states[it.Number] = forge.Lifecycle{
			State:    normalizeLifecycleState(it.State),
			MergedAt: it.MergedAt,
		}
	}
	return states
}
