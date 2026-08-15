package forge

import (
	"context"
	"time"
)

// Client is the forge seam the engine drives. Exactly one implementation is
// live per instance — one instance serves one forge (ADR 0030 §1) — and every
// value crossing this interface is already NORMALISED into the neutral
// vocabulary above: a forge's raw shapes are decoded inside its adapter and
// never reach a caller.
//
// Adapters deliberately do not share a transport (ADR 0030 §4); each shells out
// to the operator's own CLI, reusing its auth. tm3k holds no token.
type Client interface {
	// ListCandidates pulls the inbound candidate set once per cycle.
	ListCandidates(ctx context.Context) ([]PR, error)
	// ListAuthored pulls the outbound candidate set — every open PR the
	// operator authors — once per cycle via a second batched list against the
	// same repo. Drafts are included (draft is an outbound STAGE, not a gate),
	// and mergeability + the review decision ride the single call (no N+1).
	// Normalised, not judged: ClassifyOutboundStage judges each PR into its stage.
	ListAuthored(ctx context.Context) ([]PR, error)
	// UnresolvedThreads pulls per-PR review-thread resolution for the whole
	// authored pull — the cycle's third batched call (ADR 0019). Normalised, not
	// judged: each PR maps to its review-thread connection and the pure
	// UnresolvedCount judges the fold. A PR absent from the map carries no
	// review threads. The result is load-bearing for the outbound partition: a
	// failed call makes the engine fail closed (clear the outbound snapshot,
	// merge nothing), exactly like a failed ListAuthored.
	UnresolvedThreads(ctx context.Context) (map[int]ReviewThreads, error)
	// Approve records an approving review on one PR.
	Approve(ctx context.Context, number int) error
	// CurrentUser resolves the authenticated login once at startup so the matcher
	// can expand the @me author token.
	CurrentUser(ctx context.Context) (string, error)
	// CheckRepoVisible reports whether the configured repo is visible to the
	// active identity — a boot-time preflight gate. It exists because the
	// per-cycle pulls can return an EMPTY result (not an error) for a repo the
	// identity cannot see: without this check a wrong active account boots fine
	// and reports `ok` with zero counts forever, the silent-blindness failure
	// mode the preflight is there to prevent.
	CheckRepoVisible(ctx context.Context) error
	// PRStatesSince fetches the live lifecycle of every PR the bot has reviewed —
	// it only ever approves, so reviewed-by-me == approved-by-me — that was
	// updated at or after since, in ONE batched call, for the engine's
	// tail-of-cycle Approval-Feed refresh. It returns a number->Lifecycle map (a
	// superset of today's feed; the engine intersects it against today's
	// numbers). Normalised, not judged: CollapsePRState judges each bucket.
	// Replaces the per-PR view N+1 that did not survive a higher cycle cadence
	// (ADR 0007).
	PRStatesSince(ctx context.Context, since time.Time) (map[int]Lifecycle, error)
	// Diff fetches one PR's changed files on demand (the queue's Diff pill), in a
	// single call bounded to one page. User-triggered, never on the cycle path —
	// the sanctioned exception to the no-per-PR-call rule (ADR 0008). Files past
	// the page cap are simply not returned; the caller compares the count against
	// the PR's ChangedFiles to render a "first N of M" banner.
	Diff(ctx context.Context, number int) ([]FileDiff, error)
	// MergeInfo fetches one PR's live title, body, and reviews in a single call
	// at the moment of merge — a sanctioned per-PR call in the ADR 0008 sense
	// (rare, consented via the Arm; fires only on an actual merge), which
	// guarantees the commit message is built from the PR description as it is
	// NOW, never a stale arm-time copy (ADR 0016). Normalised, not judged:
	// CommitMessage/ApprovedBy judge the details.
	MergeInfo(ctx context.Context, number int) (MergeDetails, error)
	// Merge squash-merges one PR with the given commit subject and body,
	// deleting the branch (ADR 0016 always squashes). The engine owns the
	// preconditions and the one immediate retry; this call only executes.
	Merge(ctx context.Context, number int, subject, body string) error
}
