package hook

import "context"

// Point is a named hook point: a moment the engine passes through, never a
// stage a PR sits in (ADR 0021). The registry below is the full point set;
// adding a point is one entry here plus its pre/post classification.
type Point string

// The hook-point registry, under the strict pre/post discipline: pre-points
// carry Screens only (they gate the upcoming action), post-points carry
// Notifiers only (they announce a completed fact). Outbound points
// (pre_merge/post_merge) are deliberately absent — deferred, not designed
// (ADR 0021).
const (
	// PreApprove is the moment before approve(): an Approve Rule matched and
	// no Review Rule or breaking title gated. The only pre-point; Screens
	// attach here structurally, without naming it.
	PreApprove Point = "pre_approve"
	// PostApprove fires after an approval succeeded, auto and manual override
	// alike (the context carries the manual flag).
	PostApprove Point = "post_approve"
	// QueueEntered fires when rules routed the PR to Needs-Human-Review.
	QueueEntered Point = "queue_entered"
	// ScreenHeld fires when a Screen's hold diverted the PR to the queue.
	// Separate from QueueEntered so a just-screened PR never auto-receives a
	// second AI pass (ADR 0021).
	ScreenHeld Point = "screen_held"
)

// postPoints is the post side of the registry — the points a Notifier may
// name. Screens never name a point at all, so the pre side needs no set.
var postPoints = map[Point]bool{
	PostApprove:  true,
	QueueEntered: true,
	ScreenHeld:   true,
}

// PRContext is the one payload every hook receives (ADR 0023): the point that
// fired, the repo, the PR identity the hook needs to fetch anything further
// itself (per-PR fetches are the hook's business — the one-batched-call
// doctrine, amended), and the point extras.
type PRContext struct {
	Point   Point
	Repo    string // owner/name
	Number  int
	Title   string
	Author  string
	URL     string
	HeadSHA string // the head the PR carried when the point fired; Screen verdicts key on it

	// Point extras — zero-valued where the point carries no such fact.
	Reasons []string // queue_entered / screen_held: why the PR sits in the queue
	Manual  bool     // post_approve: manual override vs auto-approval
}

// Outcome is a Verdict's decision. The zero value is deliberately neither
// Proceed nor Hold: a missing verdict is never proceed
// (never-fire-on-no-signal extends to hooks — ADR 0021).
type Outcome string

const (
	// Proceed lets the gated engine action go ahead.
	Proceed Outcome = "proceed"
	// Hold diverts the PR to Needs-Human-Review (divert, never drop —
	// Invariant-family; ADR 0022).
	Hold Outcome = "hold"
	// Errored is a recorded failed attempt (crash, timeout, no extractable
	// verdict) — a verdict-store row outcome, never a Screen's verdict. The
	// level-triggered consult reads it as "still no verdict" and re-dispatches;
	// the persisted rows are the attempt count the 3-strikes fold reads to
	// bound the no-verdict path (ADR 0022 decision 5).
	Errored Outcome = "error"
)

// Verdict is a Screen's structured judgement: the outcome plus the reason the
// human will read (queue reason chips, the screen_holds prose).
type Verdict struct {
	Outcome Outcome
	Reason  string
}

// Screen is the pre-point hook kind: anything that yields a Verdict gating the
// engine action at its point (ADR 0021). An error — crash, timeout, nothing
// structurally extractable — means NO verdict, never a fabricated one in
// either direction; the caller records it as a failed attempt (ADR 0022's
// 3-strikes path). The AI species realizing this seam arrives in a later
// slice; nothing implements it yet.
type Screen interface {
	Screen(ctx context.Context, pr PRContext) (Verdict, error)
}

// Notifier is the post-point hook kind: it fires a side effect announcing a
// completed fact. Output is ignored and failure can never block, divert, or
// reorder an engine action — the caller logs the miss and moves on, and
// at-most-once firing means a failure is never retried (ADR 0021). The AI
// species realizing this seam arrives in a later slice; nothing implements it
// yet.
type Notifier interface {
	Notify(ctx context.Context, pr PRContext) error
}
