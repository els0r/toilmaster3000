// Package forge is the neutral vocabulary every layer above the seam speaks.
// It owns the domain types the engine passes around, the pure folds that judge
// them, the Client interface each forge adapter implements, and the Fake that
// backs the engine tests. A forge's own raw shapes are decoded and NORMALISED
// into this vocabulary inside its adapter (internal/forge/github) and never
// leave it — decode vs judge, tightened (ADR 0030 §3).
package forge

// ReviewDecision is the NEUTRAL, PR-level review rollup: has the PR been
// approved, has a reviewer asked for changes, or neither yet. Each adapter
// normalises its forge's own token set into these three values — GitHub's
// APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED / empty collapses here, and
// nothing above the seam ever sees the raw string.
type ReviewDecision string

const (
	// ReviewNone is "no decision yet" — nobody has approved and nobody has asked
	// for changes. It is the zero value, so an unrecognised token reads as
	// undecided rather than approved.
	//
	// It is NOT "nobody asked the forge". Every adapter MUST populate
	// ReviewDecision on the INBOUND pull: ADR 0013's soft dedup reads it to
	// leave a PR someone else already approved alone, and an adapter that
	// skipped the field would report every such PR as undecided and re-approve
	// it — corrupting saved-switches analytics across instances with no error
	// anywhere. Outbound needs it too, for the stage fold.
	ReviewNone ReviewDecision = ""
	// ReviewApproved is a PR the forge reports as approved.
	ReviewApproved ReviewDecision = "approved"
	// ReviewChangesRequested is a PR with an open request for changes. It is the
	// ONLY signal that withdraws outbound consent (ADR 0016).
	ReviewChangesRequested ReviewDecision = "changes_requested"
)

// Mergeability is the NEUTRAL answer to "will the forge accept a merge on this
// PR right now". It is a MERGE precondition, never a stage boundary:
// ClassifyOutboundStage never reads it, and a conflicted Ready PR stays in
// Ready carrying its mergeability.
//
// Only MergeableMergeable clears a merge; every other value blocks.
type Mergeability string

const (
	// MergeableUnknown is "not answered": the forge is still computing, reported
	// something unrecognised, or — for the inbound pull, which does not request
	// mergeability at all — was never asked. It blocks a merge without moving a
	// stage; the next cycle retries naturally.
	//
	// It is deliberately the ZERO VALUE, so a PR built by hand (forge.Fake's
	// canned candidates, the engine's synthetic manual-approve PR) is
	// indistinguishable from one an adapter reported unknown for. A separate
	// zero would give the Fake a branch production never produces.
	MergeableUnknown Mergeability = ""
	// MergeableMergeable is a PR the forge will accept a merge on — the merge
	// step's final precondition (ADR 0016).
	MergeableMergeable Mergeability = "mergeable"
	// MergeableConflicting is a PR whose branch conflicts with its base. The wire
	// layer derives the Ready row's conflict marker from it.
	MergeableConflicting Mergeability = "conflicting"
)

// PR is one candidate pull request from the candidate set, in the neutral
// vocabulary. GitLab's merge request is a PR here too, and its project-scoped
// iid is the Number (ADR 0030 §2). Additions and Deletions are the changed-line
// counts the adapter pulls in its single batched list call; they are carried
// separately and the diff-size rule predicate sums them (additions + deletions).
type PR struct {
	Number    int
	Title     string
	Author    string
	URL       string
	Additions int
	Deletions int
	// ChangedFiles is the count of files the PR touches, from the same single
	// batched list call. It is carried for human triage in the queue (how many
	// files a change spans), not for matching — the diff-size rule predicate uses
	// only additions+deletions.
	ChangedFiles int
	// IsDraft is the draft flag from the same single batched list call. A draft PR
	// is dropped by the engine's eligibility gate before it is ever parsed or
	// matched (CONTEXT "Eligibility gates").
	IsDraft bool
	// Checks is the PR's NORMALISED check rollup: one entry per check, each
	// already folded to pass/fail/pending by the adapter. The all-green
	// eligibility gate folds these via AllGreen before the PR is ever parsed or
	// matched. Cardinality is a forge fact — GitHub reports N rollup entries,
	// GitLab one pipeline verdict — so nothing above the seam may read a count
	// off this slice (ADR 0030 §6).
	Checks []Check
	// FailingChecks is the count of non-passing checks the ADAPTER supplies — the
	// dropped-red station's "N checks failing" signal. It is NOT a fold over
	// Checks: cardinality is a forge fact, so each adapter computes the count in
	// its own terms (GitHub over its rollup entries, GitLab from a failed-job
	// count) while emitting whatever entry count its check model has (ADR 0030 §6).
	FailingChecks int
	// ReviewDecision is the PR's normalised review rollup. An approved candidate
	// whose number is absent from approvals.jsonl was approved elsewhere — tm3k
	// leaves it alone as a soft dedup (ADR 0013), so saved-switches analytics
	// never double-counts across a team running multiple tm3k instances.
	ReviewDecision ReviewDecision
	// Mergeable is the PR's normalised mergeability, populated only by the
	// authored (outbound) pull — the inbound candidate pull does not request it,
	// and so leaves it MergeableUnknown.
	Mergeable Mergeability
	// Files are the paths of the PR's changed files, from the same single batched
	// list call (inbound only — the outbound pull requests no files). They are the
	// scope axis of Notifier firing discipline: a Notifier's Paths globs match
	// against them (ADR 0026). A forge may cap the field, so this list is
	// TRUNCATED whenever len(Files) < ChangedFiles — the adapter only reports that
	// fact; the pure Scope fold judges what to do about it.
	Files []string
	// HeadSHA is the commit the PR's head currently points at. Screen verdicts key
	// on it (hook_id, number, head — ADR 0022), so a new push re-screens and an
	// unchanged head reuses its stored verdict. Riding the batched call keeps
	// screening free of per-PR fetches.
	HeadSHA string
}

// FileDiff is one changed file of a PR: the path, its status
// (added|modified|removed|renamed), the per-file changed-line counts, and the
// unified-diff Patch. A forge omits the patch for binary and over-large files,
// so Patch is empty for those — the Diff card renders them as "no preview"
// rather than a blank diff. This is the on-demand seam behind the queue's Diff
// pill; it never rides the cycle (ADR 0008).
type FileDiff struct {
	Filename  string
	Status    string
	Additions int
	Deletions int
	Patch     string
}
