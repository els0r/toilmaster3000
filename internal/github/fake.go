package github

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"
)

// Fake is a test GitHubClient. It serves canned candidates, records every
// Approve call, and lets a test mark specific PR numbers to fail their approval
// (to prove the engine's per-PR failure handling and retry-next-cycle path).
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

	// Login is the login CurrentUser returns (the resolved @me token).
	Login string
	// CurrentUserErr, when set, makes CurrentUser fail (to prove preflight
	// fails fast when @me cannot be resolved).
	CurrentUserErr error
	// RepoVisibleErr, when set, makes CheckRepoVisible fail (to prove
	// preflight fails fast when the configured repo is invisible to the
	// active gh identity).
	RepoVisibleErr error

	failNumbers map[int]bool
	approved    []int

	// states are canned PR lifecycles keyed by number, served WHOLE by
	// PRStatesSince (the engine's tail-of-cycle batched refresh) as a superset of
	// today's feed — the engine intersects against today's numbers. A number with
	// no entry is absent from the result (reads as last-known, or unknown).
	states map[int]RawPRState
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
	// wholesale (to prove the endpoint surfaces a gh failure).
	diffs     map[int][]FileDiff
	DiffErr   error
	diffCalls []int

	// mergeInfos are canned live merge-time details served by MergeInfo (the
	// per-merge gh pr view), keyed by PR number. MergeInfoErr, when set, makes
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

// ListCandidates returns the canned candidate set (or ListErr).
func (f *Fake) ListCandidates(_ context.Context) ([]PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
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

// CurrentUser returns the configured Login (or CurrentUserErr), standing in for
// `gh api user` so preflight is provable without a real gh.
func (f *Fake) CurrentUser(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CurrentUserErr != nil {
		return "", f.CurrentUserErr
	}
	return f.Login, nil
}

// CheckRepoVisible returns the configured RepoVisibleErr (nil by default),
// standing in for `gh repo view` so the repo-visibility preflight is provable
// without a real gh.
func (f *Fake) CheckRepoVisible(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.RepoVisibleErr
}

// SetState canns the raw lifecycle PRStatesSince returns for a PR number.
func (f *Fake) SetState(number int, raw RawPRState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[int]RawPRState{}
	}
	f.states[number] = raw
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
// StateErr), standing in for the batched `gh pr list`. It returns a superset of
// today's feed — the engine intersects against today's numbers — so it ignores
// since (the canned states are pre-scoped by the test).
func (f *Fake) PRStatesSince(_ context.Context, since time.Time) (map[int]RawPRState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateSinceCalls = append(f.stateSinceCalls, since)
	if f.StateErr != nil {
		return nil, f.StateErr
	}
	out := make(map[int]RawPRState, len(f.states))
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
// assert an unqueued number never reaches the gh diff call.
func (f *Fake) DiffCalls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.diffCalls))
	copy(out, f.diffCalls)
	return out
}

// Diff returns the canned changed-file set for the number (or DiffErr), standing
// in for the on-demand `gh api .../files` call.
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
// standing in for the per-merge `gh pr view` call. A number with no canned
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
