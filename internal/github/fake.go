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

	// Login is the login CurrentUser returns (the resolved @me token).
	Login string
	// CurrentUserErr, when set, makes CurrentUser fail (to prove preflight
	// fails fast when @me cannot be resolved).
	CurrentUserErr error

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
