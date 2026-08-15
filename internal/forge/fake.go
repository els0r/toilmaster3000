package forge

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"
)

// The Fake is only worth anything if it stays interchangeable with a real
// adapter, so the seam it stands in for is asserted at compile time.
var _ Client = (*Fake)(nil)

// Fake is a test forge Client. It serves canned candidates, records every
// Approve call, and lets a test mark specific PR numbers to fail their approval
// (to prove the engine's per-PR failure handling and retry-next-cycle path).
//
// It lives beside the neutral vocabulary rather than in an adapter on purpose:
// the engine's behaviour is forge-independent, so the seam it is tested over
// must be too.
type Fake struct {
	mu sync.Mutex

	// Candidates is the canned candidate set returned by ListCandidates.
	Candidates []PR
	// ListErr, when set, makes ListCandidates fail (to prove a failed fetch
	// skips the whole cycle).
	ListErr error

	// Authored is the canned authored (outbound) set returned by ListAuthored.
	Authored []PR
	// AuthoredErr, when set, makes ListAuthored fail (to prove a failed
	// outbound fetch clears the outbound snapshot, independent of the inbound
	// pull).
	AuthoredErr error
	// authoredCalls counts ListAuthored invocations, so a test can assert one
	// additional list call per cycle (no N+1).
	authoredCalls int

	// threads are canned per-PR reviewThreads connections keyed by number,
	// served WHOLE by UnresolvedThreads (the cycle's third batched call). A
	// number with no entry reads as a PR with no review threads (zero
	// unresolved). ThreadsErr, when set, makes UnresolvedThreads fail
	// wholesale (to prove a failed threads call clears the outbound snapshot
	// and skips ALL merging that cycle — fail closed, ADR 0019).
	threads      map[int]ReviewThreads
	ThreadsErr   error
	threadsCalls int

	// Login is the login CurrentUser returns (the resolved @me token).
	Login string
	// CurrentUserErr, when set, makes CurrentUser fail (to prove preflight
	// fails fast when @me cannot be resolved).
	CurrentUserErr error
	// RepoVisibleErr, when set, makes CheckRepoVisible fail (to prove
	// preflight fails fast when the configured repo is invisible to the
	// active forge identity).
	RepoVisibleErr error

	failNumbers map[int]bool
	approved    []int

	// states are canned PR lifecycles keyed by number, served WHOLE by
	// PRStatesSince (the engine's tail-of-cycle batched refresh) as a superset of
	// today's feed — the engine intersects against today's numbers. A number with
	// no entry is absent from the result (reads as last-known, or unknown).
	states map[int]Lifecycle
	// StateErr, when set, makes PRStatesSince fail wholesale — the batched refresh
	// is all-or-nothing (ADR 0007), proving a failed refresh keeps ALL last-known
	// state and never aborts the cycle.
	StateErr error
	// stateSinceCalls records the since floor of every PRStatesSince call, so a
	// test can assert the refresh ran once (not per-PR) and was skipped entirely
	// for an empty feed.
	stateSinceCalls []time.Time

	// diffs are canned per-PR changed-file sets served by Diff (the on-demand
	// Diff-pill fetch), keyed by PR number. DiffErr, when set, makes Diff fail
	// wholesale (to prove the endpoint surfaces an adapter failure).
	diffs     map[int][]FileDiff
	DiffErr   error
	diffCalls []int

	// mergeInfos are canned live merge-time details served by MergeInfo (the
	// per-merge merge-info fetch), keyed by PR number. MergeInfoErr, when set, makes
	// MergeInfo fail wholesale (to prove a merge without live details is
	// skipped).
	mergeInfos   map[int]MergeDetails
	MergeInfoErr error

	// mergeCalls records every Merge invocation in order (including failed
	// attempts, so a test can count the one immediate retry); failMerges maps a
	// PR number to how many of its next Merge calls fail.
	mergeCalls []MergeCall
	failMerges map[int]int
}

// MergeCall is one recorded Merge invocation: the PR number and the commit
// subject/body the engine constructed — what a test asserts gh-land parity
// against.
type MergeCall struct {
	Number  int
	Subject string
	Body    string
}

// NewFake returns a Fake seeded with the given canned candidates.
func NewFake(candidates ...PR) *Fake {
	return &Fake{
		Candidates:  candidates,
		Login:       "me-login",
		failNumbers: map[int]bool{},
	}
}

// FailApprove marks a PR number so its Approve call returns an error.
func (f *Fake) FailApprove(number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNumbers == nil {
		f.failNumbers = map[int]bool{}
	}
	f.failNumbers[number] = true
}

// HealApprove clears a previously-set failure so a later cycle can succeed.
func (f *Fake) HealApprove(number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failNumbers, number)
}

// ApprovedCalls returns the PR numbers Approve was called with, in order
// (including failed attempts), so a test can assert call counts.
func (f *Fake) ApprovedCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.approved))
	copy(out, f.approved)
	return out
}

// requireCoherentChecks panics if a canned PR's rollup contradicts its
// FailingChecks count. A real adapter produces the two together and cannot
// disagree with itself; a hand-built PR sets two fields and can, which is
// exactly the bug that hid when FailingChecks stopped being a fold and became
// adapter-supplied (ADR 0030 §6) — a red PR silently claiming nothing failing,
// rendering "0 checks failing" on the dropped-red station.
//
// The rule is DIRECTIONAL on purpose. Entry count and failing count are not
// the same number on every forge: GitHub counts non-passing rollup entries,
// GitLab counts failed jobs behind ONE pipeline entry. Requiring equality here
// would bake GitHub's cardinality into the shared Fake and reject a correct
// GitLab fixture. What every forge owes is the implication a failure makes: if
// something failed, the count is not zero.
//
// It panics rather than returning an error because the Fake has no *testing.T
// and this is never a runtime condition — it is a malformed fixture, and a
// malformed fixture must stop the test rather than quietly weaken it.
func requireCoherentChecks(prs []PR) {
	for _, pr := range prs {
		if hasFailingCheck(pr.Checks) && pr.FailingChecks == 0 {
			panic(fmt.Errorf(
				"forge.Fake: PR #%d has a failing check but FailingChecks is 0 — "+
					"a canned PR must carry the count its adapter would supply (ADR 0030 §6)",
				pr.Number))
		}
	}
}

// ListCandidates returns the canned candidate set (or ListErr).
func (f *Fake) ListCandidates(_ context.Context) ([]PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	requireCoherentChecks(f.Candidates)
	out := make([]PR, len(f.Candidates))
	copy(out, f.Candidates)
	return out, nil
}

// ListAuthored returns the canned authored set (or AuthoredErr), recording the
// call so a test can assert exactly one outbound list call per cycle.
func (f *Fake) ListAuthored(_ context.Context) ([]PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authoredCalls++
	if f.AuthoredErr != nil {
		return nil, f.AuthoredErr
	}
	requireCoherentChecks(f.Authored)
	out := make([]PR, len(f.Authored))
	copy(out, f.Authored)
	return out, nil
}

// AuthoredCallCount returns how many times ListAuthored was invoked.
func (f *Fake) AuthoredCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authoredCalls
}

// SetThreads canns the review-thread connection UnresolvedThreads returns
// for a PR number. Setting the zero ReviewThreads (or never setting one)
// reads as a PR with nothing unresolved — the two cases are one condition by
// construction (ADR 0019).
func (f *Fake) SetThreads(number int, threads ReviewThreads) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.threads == nil {
		f.threads = map[int]ReviewThreads{}
	}
	f.threads[number] = threads
}

// ThreadsCallCount returns how many times UnresolvedThreads was invoked, so a
// test can assert the threads ride one batched call per cycle (no N+1).
func (f *Fake) ThreadsCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threadsCalls
}

// UnresolvedThreads records the call and returns the WHOLE canned thread map
// (or ThreadsErr), standing in for the adapter's batched threads call.
func (f *Fake) UnresolvedThreads(_ context.Context) (map[int]ReviewThreads, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadsCalls++
	if f.ThreadsErr != nil {
		return nil, f.ThreadsErr
	}
	out := make(map[int]ReviewThreads, len(f.threads))
	maps.Copy(out, f.threads)
	return out, nil
}

// CurrentUser returns the configured Login (or CurrentUserErr), standing in for
// the adapter's identity call so preflight is provable without a real CLI.
func (f *Fake) CurrentUser(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CurrentUserErr != nil {
		return "", f.CurrentUserErr
	}
	return f.Login, nil
}

// CheckRepoVisible returns the configured RepoVisibleErr (nil by default),
// standing in for the adapter's repo probe so the repo-visibility preflight is provable
// without a real CLI.
func (f *Fake) CheckRepoVisible(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.RepoVisibleErr
}

// SetState canns the lifecycle PRStatesSince returns for a PR number.
func (f *Fake) SetState(number int, lifecycle Lifecycle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[int]Lifecycle{}
	}
	f.states[number] = lifecycle
}

// DropState removes a canned lifecycle so a later PRStatesSince result no longer
// carries that number — standing in for a PR that has aged out of the
// `updated:>=today` window or lags the search index. A test uses it to prove the
// engine keeps the last-known state in place rather than resetting it to unknown.
func (f *Fake) DropState(number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, number)
}

// StateCallCount returns how many times PRStatesSince was invoked, so a test can
// assert the refresh ran once per cycle (not per-PR) and was skipped entirely for
// an empty feed.
func (f *Fake) StateCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stateSinceCalls)
}

// PRStatesSince records the call and returns the WHOLE canned state set (or
// StateErr), standing in for the adapter's batched PR-State call. It returns a superset of
// today's feed — the engine intersects against today's numbers — so it ignores
// since (the canned states are pre-scoped by the test).
func (f *Fake) PRStatesSince(_ context.Context, since time.Time) (map[int]Lifecycle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateSinceCalls = append(f.stateSinceCalls, since)
	if f.StateErr != nil {
		return nil, f.StateErr
	}
	out := make(map[int]Lifecycle, len(f.states))
	maps.Copy(out, f.states)
	return out, nil
}

// SetDiff canns the changed-file set Diff returns for a PR number.
func (f *Fake) SetDiff(number int, files []FileDiff) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.diffs == nil {
		f.diffs = map[int][]FileDiff{}
	}
	f.diffs[number] = files
}

// DiffCalls returns the PR numbers Diff was called with, in order, so a test can
// assert an unqueued number never reaches the adapter's diff call.
func (f *Fake) DiffCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.diffCalls))
	copy(out, f.diffCalls)
	return out
}

// Diff returns the canned changed-file set for the number (or DiffErr), standing
// in for the adapter's on-demand diff fetch.
func (f *Fake) Diff(_ context.Context, number int) ([]FileDiff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diffCalls = append(f.diffCalls, number)
	if f.DiffErr != nil {
		return nil, f.DiffErr
	}
	out := make([]FileDiff, len(f.diffs[number]))
	copy(out, f.diffs[number])
	return out, nil
}

// SetMergeInfo canns the live merge-time details MergeInfo returns for a PR
// number.
func (f *Fake) SetMergeInfo(number int, details MergeDetails) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mergeInfos == nil {
		f.mergeInfos = map[int]MergeDetails{}
	}
	f.mergeInfos[number] = details
}

// MergeInfo returns the canned details for the number (or MergeInfoErr),
// standing in for the per-merge merge-info fetch. A number with no canned
// entry returns the zero details — an engine test that does not assert the
// message need not cann one.
func (f *Fake) MergeInfo(_ context.Context, number int) (MergeDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MergeInfoErr != nil {
		return MergeDetails{}, f.MergeInfoErr
	}
	return f.mergeInfos[number], nil
}

// FailMerge marks a PR number so its next `times` Merge calls fail — one
// failure proves the immediate retry lands, two prove the cycle gives up and
// the next cycle retries.
func (f *Fake) FailMerge(number, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failMerges == nil {
		f.failMerges = map[int]int{}
	}
	f.failMerges[number] = times
}

// MergeCalls returns every Merge invocation in order (including failed
// attempts), so a test can assert both the retry count and the constructed
// commit message.
func (f *Fake) MergeCalls() []MergeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]MergeCall, len(f.mergeCalls))
	copy(out, f.mergeCalls)
	return out
}

// Merge records the call and fails while the number has FailMerge budget left.
func (f *Fake) Merge(_ context.Context, number int, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mergeCalls = append(f.mergeCalls, MergeCall{Number: number, Subject: subject, Body: body})
	if f.failMerges[number] > 0 {
		f.failMerges[number]--
		return fmt.Errorf("fake: merge %d failed", number)
	}
	return nil
}

// Approve records the call and fails if the number was marked via FailApprove.
func (f *Fake) Approve(_ context.Context, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved = append(f.approved, number)
	if f.failNumbers[number] {
		return fmt.Errorf("fake: approve %d failed", number)
	}
	return nil
}
