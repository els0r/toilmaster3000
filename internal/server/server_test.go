package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"gopkg.in/yaml.v3"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/stretchr/testify/require"
)

const apiPrefix = "/api/toilmaster3000/v1"

// testSPA returns a minimal embedded-SPA filesystem standing in for
// frontend/dist, so the server has an index.html to serve at "/".
func testSPA() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>toilmaster3000</title>")},
	}
}

// storeWith builds a rule.Store over a temp-dir rules.yaml seeded with the
// given explicit rules (bypassing the default seeding by pre-writing the file),
// so a test can pin exactly which rules the engine matches.
func storeWith(t *testing.T, rules ...rule.Rule) *rule.Store {
	t.Helper()
	return storeAt(t, filepath.Join(t.TempDir(), "rules.yaml"), rules...)
}

// storeAt is storeWith over an explicit rules.yaml path, so a restart test can
// reuse the same file. The file is written before NewStore so the explicit
// rules load verbatim instead of triggering default seeding.
func storeAt(t *testing.T, rulesPath string, rules ...rule.Rule) *rule.Store {
	t.Helper()
	doc := map[string][]rule.Rule{"Rules": rules}
	data, err := yaml.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rulesPath, data, 0o644))
	s, err := rule.NewStore(rulesPath)
	require.NoError(t, err)
	return s
}

// matchAllChores is a permissive rule used by the slice-2 tracer tests: it
// matches any author whose title parses with type==chore (the canned
// candidates), standing in for "approve these candidates".
func matchAllChores() rule.Rule {
	return rule.Rule{Name: "test chores", Enabled: true, TypeInclude: "^chore$"}
}

// newEngine builds an Engine over the given fake client, a temp-dir state file,
// and a permissive chore-matching rule store. The fake GitHubClient is the only
// substitution; everything else is asserted through the HTTP API. The store is
// returned so the server can be wired with the same instance.
func newEngine(t *testing.T, fake *github.Fake) (*engine.Engine, *rule.Store) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store := storeWith(t, matchAllChores())
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng, store
}

// newEngineAt builds an Engine over an explicit state-file path (chore-matching
// rules), so a test can construct a second engine over the same file to prove
// restart.
func newEngineAt(t *testing.T, fake *github.Fake, statePath string) (*engine.Engine, *rule.Store) {
	t.Helper()
	store := storeWith(t, matchAllChores())
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng, store
}

// newEngineWith builds an Engine over an explicit rule store, for the slice-4
// matcher tests that pin specific rules and candidates.
func newEngineWith(t *testing.T, fake *github.Fake, store *rule.Store) *engine.Engine {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng
}

func newTestServerFor(t *testing.T, eng *engine.Engine, store *rule.Store) *httptest.Server {
	t.Helper()
	h, err := server.New(testSPA(), eng, store)
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	eng, store := newEngine(t, github.NewFake())
	return newTestServerFor(t, eng, store)
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(into))
}

// seedApprovalsFile writes the given approval records to an approvals.jsonl in
// the on-disk (oldest-first append) order, so a test can construct an engine
// over a feed with controlled timestamps — the only way to exercise the
// today-scoped filter's boundary and a legacy import's matched_rule.
func seedApprovalsFile(t *testing.T, path string, recs ...engine.Approval) {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range recs {
		line, err := json.Marshal(r)
		require.NoError(t, err)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
}

// greenChecks is a single completed, successful CheckRun — the minimal rollup
// that makes a PR all-green so the eligibility gate lets it through to
// evaluation. Shared fixtures whose PRs are expected to stay ELIGIBLE (approved
// or queued) carry it; the all-green gate (added in the eligibility slice) drops
// any PR with an empty rollup, so without it these PRs would be dropped.
func greenChecks() []github.Check {
	return []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}}
}

// cannedCandidates are three chore PRs that all match matchAllChores(), so the
// slice-2 tracer tests (approve, dedup, restart, retry) see all three approved.
// Each carries a passing rollup so the all-green gate lets them through.
func cannedCandidates() []github.PR {
	return []github.PR{
		{Number: 1, Title: "chore: bump deps", Author: "alice", URL: "https://github.com/o/r/pull/1", Checks: greenChecks()},
		{Number: 2, Title: "chore: tidy", Author: "bob", URL: "https://github.com/o/r/pull/2", Checks: greenChecks()},
		{Number: 3, Title: "chore: lint", Author: "carol", URL: "https://github.com/o/r/pull/3", Checks: greenChecks()},
	}
}

// B1 (tracer): GET /status returns the never-run Cycle status in snake_case.
func TestStatusNeverRun(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + apiPrefix + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))

	require.Nil(t, got["last_run"], "never-run cycle has no last_run timestamp")
	require.Equal(t, "never_run", got["outcome"])
	require.Equal(t, float64(0), got["approved_count"])
	require.Equal(t, float64(0), got["queue_count"])
}

// B2: huma generates an OpenAPI document and it is reachable, describing the
// status operation.
func TestOpenAPIReachable(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/openapi.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	require.Contains(t, doc, "openapi")

	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "OpenAPI doc has a paths object")
	require.Contains(t, paths, apiPrefix+"/status")
	require.Contains(t, paths, apiPrefix+"/approvals")
}

// TestOpenAPISpecMatchesCommitted asserts the committed openapi.json is exactly
// the document the API builds today. cmd/openapigen writes that file from the
// same server.Config + server.RegisterAPI used here and by the live server, so a
// DTO change that isn't followed by `make generate` (regenerating the spec and
// the frontend types) fails here. This is the spec half of the drift guard, and
// unlike `make check` it runs in `go test` with no regeneration step. The nil
// engine/rules are safe: spec generation never invokes the handler closures.
func TestOpenAPISpecMatchesCommitted(t *testing.T) {
	api := humago.New(http.NewServeMux(), server.Config())
	server.RegisterAPI(api, nil, nil)

	got, err := api.OpenAPI().MarshalJSON()
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("..", "..", "openapi.json"))
	require.NoError(t, err, "read committed openapi.json (did you run `make generate`?)")

	require.JSONEq(t, string(want), string(got),
		"committed openapi.json is stale; run `make generate` and commit the result")
}

// B3: the embedded SPA is served at "/" and unknown non-API routes fall back to
// index.html so client-side routing works; the API prefix is not shadowed.
func TestSPAServedWithFallback(t *testing.T) {
	srv := newTestServer(t)

	for _, path := range []string{"/", "/some/client/route"} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "path %s", path)
		require.Contains(t, string(body), "toilmaster3000", "path %s serves the SPA shell", path)
	}

	// The SPA fallback must not swallow the JSON API.
	resp, err := http.Get(srv.URL + apiPrefix + "/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// B4: a cycle approves every candidate exactly once; they appear newest-first
// in GET /approvals, and GET /status reflects the cycle's counts.
func TestCycleApprovesAndFeedNewestFirst(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)

	require.Len(t, feed, 3)
	// Newest-first: candidates approved in order 1,2,3 -> feed 3,2,1.
	require.Equal(t, []int{3, 2, 1}, []int{feed[0].Number, feed[1].Number, feed[2].Number})
	require.Equal(t, "test chores", feed[0].MatchedRule)
	require.Equal(t, "https://github.com/o/r/pull/3", feed[0].URL)
	require.False(t, feed[0].ApprovedAt.IsZero(), "approved_at is set")

	require.Equal(t, []int{1, 2, 3}, fake.ApprovedCalls(), "each candidate approved exactly once")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, "ok", status.Outcome)
	require.Equal(t, 3, status.ApprovedCount)
	require.Equal(t, 0, status.QueueCount)
	require.NotNil(t, status.LastRun)
}

// B5: an already-approved candidate is never re-approved across cycles
// (idempotent, quiet), and the feed does not grow.
func TestDedupAcrossCycles(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3, "second cycle re-approves nothing")
	require.Equal(t, []int{1, 2, 3}, fake.ApprovedCalls(), "Approve called once per PR despite two cycles")
}

// --- PR State (Approval Feed lifecycle bar) ---

// PS1 (tracer): the Approval Feed carries each entry's live PR State, fetched
// out-of-band at the tail of the cycle (one gh pr view per today's feed entry)
// and collapsed to the open|merged|closed bucket. A merged PR shows "merged".
func TestApprovalFeedCarriesPRState(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.SetState(3, github.RawPRState{State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"})
	fake.SetState(2, github.RawPRState{State: "CLOSED"}) // closed without merging
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3)

	byNum := map[int]server.Approval{}
	for _, a := range feed {
		byNum[a.Number] = a
	}
	require.Equal(t, "merged", byNum[3].State, "PR 3 merged")
	require.Equal(t, "closed", byNum[2].State, "PR 2 closed without merging (the false-positive signal)")
}

// PS2: a PR the engine has no lifecycle for yet is "unknown" on the wire — the
// neutral default rendered as no bar. PR State is never optimistically guessed
// as open. (gh returns an empty raw state here; CollapsePRState yields unknown.)
func TestApprovalFeedUnknownPRStateByDefault(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...) // no SetState for any candidate
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3)
	for _, a := range feed {
		require.Equal(t, "unknown", a.State, "PR #%d has no known lifecycle yet", a.Number)
	}
}

// PS3: a per-PR refresh failure is logged and skipped, keeping the last known
// state — it never resets the bar to unknown nor aborts the cycle. The batched
// refresh is all-or-nothing (ADR 0007): a failed call keeps ALL last-known state,
// the per-PR approve failure semantics applied wholesale.
func TestPRStateRefreshFailureKeepsLastKnown(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.SetState(3, github.RawPRState{State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"})
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())
	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Equal(t, "merged", stateOf(feed, 3), "first refresh records merged")

	// The batched gh call now fails wholesale; the last known state must survive.
	fake.StateErr = errors.New("gh boom")
	eng.RunCycleOnce(context.Background())
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Equal(t, "merged", stateOf(feed, 3), "a failed refresh keeps the last known state")
}

// stateOf returns the wire PR State of the feed entry with the given number.
func stateOf(feed []server.Approval, number int) string {
	for _, a := range feed {
		if a.Number == number {
			return a.State
		}
	}
	return ""
}

// PS4: PR State is refreshed in ONE batched call per cycle regardless of feed
// size — the per-PR gh-pr-view N+1 is gone (ADR 0007). The batch returns a
// SUPERSET of today's feed (here a stranger #999 the bot reviewed but that is not
// in the feed); the engine intersects against today's numbers, so every today
// entry gets its state and the stranger is dropped.
func TestPRStateRefreshIsOneBatchedCall(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...) // approves 1, 2, 3 today
	fake.SetState(1, github.RawPRState{State: "OPEN"})
	fake.SetState(2, github.RawPRState{State: "CLOSED"}) // closed without merging
	fake.SetState(3, github.RawPRState{State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"})
	fake.SetState(999, github.RawPRState{State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"}) // stranger, not in feed
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	require.Equal(t, 1, fake.StateCallCount(),
		"three today entries are refreshed in ONE batched call, not three")

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3, "the stranger #999 from the batch superset never enters the feed")
	require.Equal(t, "open", stateOf(feed, 1))
	require.Equal(t, "closed", stateOf(feed, 2))
	require.Equal(t, "merged", stateOf(feed, 3))
}

// PS5: a today entry ABSENT from a later batch result (search-index lag, or aged
// out of the updated:>= window) keeps its last-known state — the engine updates
// the map in place and never resets a known bar to unknown (ADR 0007). This is
// what makes the accepted one-cycle index lag a quiet "no bar yet" rather than a
// known->unknown->known flicker.
func TestPRStateAbsentFromBatchKeepsLastKnown(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.SetState(3, github.RawPRState{State: "MERGED", MergedAt: "2026-06-19T10:00:00Z"})
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())
	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Equal(t, "merged", stateOf(feed, 3), "first refresh records merged")

	// #3 drops out of the next batch result; its known state must persist.
	fake.DropState(3)
	eng.RunCycleOnce(context.Background())
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Equal(t, "merged", stateOf(feed, 3),
		"an entry absent from the batch keeps its last-known state, not reset to unknown")
}

// PS6: with no feed entries to refresh, the batched call is skipped entirely —
// no gh process is spawned for a set that would intersect to nothing.
func TestPRStateRefreshSkippedWhenFeedEmpty(t *testing.T) {
	fake := github.NewFake() // no candidates -> nothing approved -> empty feed
	eng, _ := newEngine(t, fake)

	eng.RunCycleOnce(context.Background())

	require.Equal(t, 0, fake.StateCallCount(), "empty feed: the batched refresh is skipped")
}

// B6: approvals persist to approvals.jsonl and survive a restart — a second
// engine over the same file loads them into the dedup set and does not
// re-approve them.
func TestApprovalsSurviveRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")

	fake1 := github.NewFake(cannedCandidates()...)
	eng1, _ := newEngineAt(t, fake1, statePath)
	eng1.RunCycleOnce(context.Background())
	require.Equal(t, []int{1, 2, 3}, fake1.ApprovedCalls())

	// Restart: a fresh engine + server over the same state file.
	fake2 := github.NewFake(cannedCandidates()...)
	eng2, store2 := newEngineAt(t, fake2, statePath)
	srv2 := newTestServerFor(t, eng2, store2)

	// Feed is restored newest-first immediately on load.
	var feed []server.Approval
	getJSON(t, srv2.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3)
	require.Equal(t, []int{3, 2, 1}, []int{feed[0].Number, feed[1].Number, feed[2].Number})

	// A cycle on the restarted engine re-approves nothing.
	eng2.RunCycleOnce(context.Background())
	require.Empty(t, fake2.ApprovedCalls(), "restarted engine does not re-approve loaded PRs")
}

// B7: one PR's Approve failure does not abort the cycle; the others are
// approved and the failed PR is retried (and succeeds) next cycle, because the
// record is written only on success.
func TestFailedApproveRetriesNextCycle(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.FailApprove(2)
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 2, "the failed PR is absent from the feed")
	require.Equal(t, []int{3, 1}, []int{feed[0].Number, feed[1].Number}, "only 1 and 3 approved")

	// Next cycle: heal #2; it is retried and now appears.
	fake.HealApprove(2)
	eng.RunCycleOnce(context.Background())

	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 3)
	require.Equal(t, 2, feed[0].Number, "retried PR is newest")
	// #1 and #3 are not re-approved; #2 attempted twice total.
	require.Equal(t, []int{1, 2, 3, 2}, fake.ApprovedCalls())
}

// B8: a failed ListCandidates skips the whole cycle (approves nothing) and is
// recorded as a gh error outcome in GET /status.
func TestListFailureSkipsCycle(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.ListErr = context.DeadlineExceeded
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "no approvals when the fetch fails")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Contains(t, status.Outcome, "gh error:")
	require.Equal(t, 0, status.ApprovedCount)
	require.NotNil(t, status.LastRun, "a failed cycle still records last_run")

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Empty(t, feed)
}

// --- Slice 1: draft eligibility gate (HTTP seam) ---

// E1 (tracer): a draft candidate that would otherwise match an enabled rule is
// dropped before evaluation — absent from /approvals and /queue, never
// approved — and GET /status reports it in dropped_count.
func TestDraftDroppedBeforeEvaluation(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		// Would match matchAllChores() (chore type) but is a draft -> dropped.
		github.PR{Number: 300, Title: "chore: still cooking", Author: "alice", URL: "https://github.com/o/r/pull/300", IsDraft: true},
		// A ready chore that DOES auto-approve, so the cycle is not a no-op. It
		// carries a passing rollup so the all-green gate lets it through (else it
		// too would be dropped, masking the draft-gate assertion).
		github.PR{Number: 301, Title: "chore: ready", Author: "bob", URL: "https://github.com/o/r/pull/301", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.False(t, got[300], "a draft PR is never approved even though it matches a rule")
	require.True(t, got[301], "a ready matching PR still auto-approves")
	require.Equal(t, []int{301}, fake.ApprovedCalls(), "the draft PR's approve is never called")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Empty(t, queueNumbers(queue)[300].Number, "a draft PR is absent from the queue")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.DroppedCount, "one draft dropped this cycle")
	require.Equal(t, 1, status.ApprovedCount)
}

// E2: a failed candidate fetch reports dropped 0 — nothing was evaluated, so
// nothing was dropped (parallel to approved/queue both being 0).
func TestListFailureReportsDroppedZero(t *testing.T) {
	fake := github.NewFake(cannedCandidates()...)
	fake.ListErr = context.DeadlineExceeded
	eng, store := newEngine(t, fake)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Contains(t, status.Outcome, "gh error:")
	require.Equal(t, 0, status.DroppedCount, "a failed fetch evaluated nothing -> dropped 0")
}

// --- Slice 2 (eligibility): all-green gate (HTTP seam) ---

// passingCheck is one completed, successful CheckRun — the minimal rollup that
// makes a PR all-green so the gate lets it through to evaluation.
func passingCheck() github.Check {
	return github.Check{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}
}

// EG1: a PR with a failing check is absent from /approvals and /queue and is
// never approved — even though it matches a rule. It is counted in dropped_count.
func TestFailingCheckDroppedBeforeEvaluation(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		// Matches matchAllChores() but a failing check drops it before evaluation.
		github.PR{Number: 400, Title: "chore: red pipeline", Author: "alice", URL: "u400",
			Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}},
		// A green chore that DOES auto-approve, so the cycle is not a no-op.
		github.PR{Number: 401, Title: "chore: green", Author: "bob", URL: "u401",
			Checks: []github.Check{passingCheck()}},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.False(t, got[400], "a failing-check PR is never approved even though it matches a rule")
	require.True(t, got[401], "a green matching PR still auto-approves")
	require.Equal(t, []int{401}, fake.ApprovedCalls(), "the failing PR's approve is never called")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	_, queued := queueNumbers(queue)[400]
	require.False(t, queued, "a failing-check PR is absent from the queue")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.DroppedCount, "one not-all-green PR dropped this cycle")
	require.Equal(t, 1, status.ApprovedCount)
}

// EG2: a PR with a pending check is dropped this cycle, then becomes eligible on
// a later cycle once the check heals to SUCCESS — no persistent waiting state.
func TestPendingCheckDroppedThenEligibleNextCycle(t *testing.T) {
	store := storeWith(t, matchAllChores())
	pending := github.PR{Number: 410, Title: "chore: ci running", Author: "alice", URL: "u410",
		Checks: []github.Check{{Typename: "CheckRun", Status: "IN_PROGRESS"}}}
	fake := github.NewFake(pending)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.DroppedCount, "pending pipeline is not a candidate this cycle")
	require.Equal(t, 0, status.ApprovedCount)
	require.Empty(t, fake.ApprovedCalls(), "a pending PR is never approved")

	// Heal the check to SUCCESS; the recomputed candidate set now passes the gate.
	pending.Checks = []github.Check{passingCheck()}
	fake.Candidates = []github.PR{pending}

	eng.RunCycleOnce(context.Background())

	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 0, status.DroppedCount, "healed PR is no longer dropped")
	require.Equal(t, 1, status.ApprovedCount, "healed PR is now eligible and approved")
	require.Equal(t, []int{410}, fake.ApprovedCalls(), "approved exactly once after healing")
}

// EG3: a PR with an EMPTY rollup is dropped (not vacuously approved) — an
// auto-approver must never fire on no signal.
func TestEmptyRollupDropped(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		// Matches the rule but has no checks at all -> not all-green -> dropped.
		github.PR{Number: 420, Title: "chore: no signal", Author: "alice", URL: "u420"},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Empty(t, feed, "an empty-rollup PR is never vacuously approved")
	require.Empty(t, fake.ApprovedCalls(), "gh approve never called for an empty-rollup PR")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.DroppedCount, "the empty-rollup PR is dropped")
}

// EG4: a PR whose checks are all SUCCESS/SKIPPED/NEUTRAL (>=1 entry) passes the
// gate and is evaluated normally (auto-approved here).
func TestAllPassingChecksEvaluatedNormally(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 430, Title: "chore: mixed green", Author: "alice", URL: "u430",
			Checks: []github.Check{
				{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SKIPPED"},
				{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "NEUTRAL"},
				{Typename: "StatusContext", State: "SUCCESS"},
			}},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.True(t, approvedNumbers(feed)[430], "an all-pass rollup passes the gate and auto-approves")
	require.Equal(t, []int{430}, fake.ApprovedCalls())

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 0, status.DroppedCount, "an all-green PR is not dropped")
	require.Equal(t, 1, status.ApprovedCount)
}

// --- Slice 4: rule-driven matching, asserted through the HTTP seam ---

// matchedRules returns matched_rule keyed by PR number from the feed.
func matchedRules(feed []server.Approval) map[int]string {
	out := map[int]string{}
	for _, a := range feed {
		out[a.Number] = a.MatchedRule
	}
	return out
}

// approvedNumbers returns the set of approved PR numbers from the feed.
func approvedNumbers(feed []server.Approval) map[int]bool {
	out := map[int]bool{}
	for _, a := range feed {
		out[a.Number] = true
	}
	return out
}

// mixedCandidates spans the matching matrix: a chore (matches "chores"), a
// renovate chore (excluded by scope), a service-a feat by teammate_a (matches the
// service-a rule), a non-conventional title (never matches), and a self-authored
// chore (excluded by @me).
// Every candidate carries a passing rollup so the all-green gate lets them
// through to rule evaluation — these tests assert RULE-driven matching, so the
// exclusions must come from the rules (scope/@me/non-conventional), not the gate.
func mixedCandidates(selfLogin string) []github.PR {
	return []github.PR{
		{Number: 10, Title: "chore(deps): bump x", Author: "alice", URL: "https://github.com/o/r/pull/10", Checks: greenChecks()},
		{Number: 11, Title: "chore(renovate): bump y", Author: "renovate-bot", URL: "https://github.com/o/r/pull/11", Checks: greenChecks()},
		{Number: 12, Title: "feat(team/service-a): add panel", Author: "teammate_a", URL: "https://github.com/o/r/pull/12", Checks: greenChecks()},
		{Number: 13, Title: "totally not a conventional title", Author: "bob", URL: "https://github.com/o/r/pull/13", Checks: greenChecks()},
		{Number: 14, Title: "chore: my own work", Author: selfLogin, URL: "https://github.com/o/r/pull/14", Checks: greenChecks()},
	}
}

// S4-A: a cycle approves only PRs matched by an enabled rule; non-matching
// candidates (renovate scope, non-conventional title, self-authored) are not
// approved. Uses the two default-shaped rules and exercises @me.
func TestRuleDrivenApprovesOnlyMatches(t *testing.T) {
	const self = "me-login"
	store := storeWith(t,
		rule.Rule{Name: "team chores", Enabled: true, AuthorsExclude: []string{"@me"}, TypeInclude: "^chore$", ScopeExclude: "renovate"},
		rule.Rule{Name: "service-a — teammate_a", Enabled: true, AuthorsInclude: []string{"teammate_a"}, ScopeInclude: "service-a"},
	)
	fake := github.NewFake(mixedCandidates(self)...)
	fake.Login = self
	eng := newEngineWith(t, fake, store)
	eng.SetSelfLogin(self)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)

	got := approvedNumbers(feed)
	require.True(t, got[10], "team chore approved")
	require.True(t, got[12], "service-a PR approved")
	require.False(t, got[11], "renovate-scoped chore excluded")
	require.False(t, got[13], "non-conventional title never approved")
	require.False(t, got[14], "self-authored chore excluded by @me")
	require.Len(t, feed, 2)

	// Attribution: each approved PR records the rule that matched it.
	rules := matchedRules(feed)
	require.Equal(t, "team chores", rules[10])
	require.Equal(t, "service-a — teammate_a", rules[12])

	require.ElementsMatch(t, []int{10, 12}, fake.ApprovedCalls(), "only matched PRs approved exactly once")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, "ok", status.Outcome)
	require.Equal(t, 2, status.ApprovedCount)
}

// S4-B: OR-of-rules + first-match attribution. A PR matched by several enabled
// rules is approved once, attributed to the first matching rule in file order.
func TestRuleOrAndFirstMatchAttribution(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "first chores", Enabled: true, TypeInclude: "^chore$"},
		rule.Rule{Name: "second deps", Enabled: true, ScopeInclude: "deps"},
	)
	// PR 20 matches BOTH rules; PR 21 matches only the second.
	fake := github.NewFake(
		github.PR{Number: 20, Title: "chore(deps): bump", Author: "alice", URL: "u20", Checks: greenChecks()},
		github.PR{Number: 21, Title: "fix(deps): bump", Author: "alice", URL: "u21", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 2)

	rules := matchedRules(feed)
	require.Equal(t, "first chores", rules[20], "doubly-matched PR attributed to first rule in file order")
	require.Equal(t, "second deps", rules[21])
	require.ElementsMatch(t, []int{20, 21}, fake.ApprovedCalls(), "doubly-matched PR approved exactly once")
}

// S4-C: a disabled rule does not contribute matches; with both rules disabled
// nothing is approved even for an otherwise-matching candidate.
func TestDisabledRulesDoNotMatch(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "disabled chores", Enabled: false, TypeInclude: "^chore$"},
		rule.Rule{Name: "enabled feats", Enabled: true, TypeInclude: "^feat$"},
	)
	fake := github.NewFake(
		github.PR{Number: 30, Title: "chore: x", Author: "alice", URL: "u30", Checks: greenChecks()},
		github.PR{Number: 31, Title: "feat: y", Author: "alice", URL: "u31", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1)
	require.Equal(t, 31, feed[0].Number, "only the enabled rule's match is approved")
	require.Equal(t, "enabled feats", feed[0].MatchedRule)
	require.Equal(t, []int{31}, fake.ApprovedCalls())
}

// S4-D: first run with no rules.yaml seeds the two defaults (both enabled) and
// the cycle reproduces the scripts' behaviour against a realistic candidate
// set, all asserted through the HTTP seam.
func TestSeededDefaultsDriveCycle(t *testing.T) {
	const self = "me-login"
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store, err := rule.NewStore(rulesPath) // no file -> seeds defaults
	require.NoError(t, err)
	require.FileExists(t, rulesPath)

	fake := github.NewFake(mixedCandidates(self)...)
	fake.Login = self
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	eng.SetSelfLogin(self)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)

	got := approvedNumbers(feed)
	require.True(t, got[10], "default team-chores rule approves the deps chore")
	require.True(t, got[12], "default service-a rule approves teammate_a's service-a PR")
	require.False(t, got[11], "renovate excluded")
	require.False(t, got[13], "non-conventional excluded")
	require.False(t, got[14], "@me self-authored excluded")

	rules := matchedRules(feed)
	require.Equal(t, "team chores", rules[10])
	require.Equal(t, "service-a — teammate_a", rules[12])
}

// --- Slice 5: Rules CRUD over the HTTP seam ---

// newRulesServer builds a server over an engine with a fresh fake client and
// the given rule store, returning both so a test can drive CRUD over HTTP and
// reload the store from disk to prove persistence.
func newRulesServer(t *testing.T, store *rule.Store) *httptest.Server {
	t.Helper()
	eng := newEngineWith(t, github.NewFake(), store)
	return newTestServerFor(t, eng, store)
}

// doJSON issues method+path with an optional JSON body and decodes the response
// into `into` (when non-nil). It returns the status code so callers assert it.
func doJSON(t *testing.T, method, url string, body any, into any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	if into != nil && resp.StatusCode/100 == 2 {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into))
	}
	return resp.StatusCode
}

// errorMessage reads a huma error response body and returns its detail string,
// so a test can assert the offending field surfaces to the client.
func errorMessage(t *testing.T, method, url string, body any) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(raw)
}

// S5-A: POST creates a rule (server-generated id), GET lists it, PUT
// full-replaces it (toggling Enabled), and DELETE removes it; every change
// persists to rules.yaml and survives a restart (a second Store over the same
// file).
func TestRulesCRUDPersistAndSurviveRestart(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath) // empty file -> no rules
	srv := newRulesServer(t, store)

	// Create.
	var created rule.Rule
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "team chores", "enabled": true, "type_include": "^chore$"},
		&created)
	require.Equal(t, http.StatusCreated, code)
	require.NotEmpty(t, created.ID, "server generates a stable id")
	require.Equal(t, "team chores", created.Name)
	require.True(t, created.Enabled)

	// List shows it.
	var listed []rule.Rule
	getJSON(t, srv.URL+apiPrefix+"/rules", &listed)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)

	// Persisted to disk: a fresh store over the same file sees it.
	reloaded, err := rule.NewStore(rulesPath)
	require.NoError(t, err)
	require.Len(t, reloaded.List(), 1)
	require.Equal(t, created.ID, reloaded.List()[0].ID)

	// Update: full-replace toggling Enabled off (this is enable/disable).
	var updated rule.Rule
	code = doJSON(t, http.MethodPut, srv.URL+apiPrefix+"/rules/"+created.ID,
		map[string]any{"name": "team chores", "enabled": false, "type_include": "^chore$"},
		&updated)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, created.ID, updated.ID, "id preserved from the path")
	require.False(t, updated.Enabled, "disabled via PUT")

	// The disabled flag round-trips through a restart.
	reloaded2, err := rule.NewStore(rulesPath)
	require.NoError(t, err)
	require.Len(t, reloaded2.List(), 1)
	require.False(t, reloaded2.List()[0].Enabled, "disabled rule persisted disabled")

	// Delete.
	code = doJSON(t, http.MethodDelete, srv.URL+apiPrefix+"/rules/"+created.ID, nil, nil)
	require.Equal(t, http.StatusNoContent, code)

	getJSON(t, srv.URL+apiPrefix+"/rules", &listed)
	require.Empty(t, listed)

	reloaded3, err := rule.NewStore(rulesPath)
	require.NoError(t, err)
	require.Empty(t, reloaded3.List(), "delete persisted to disk")
}

// S5-B: a rule that constrains nothing is rejected (4xx) and never persisted.
func TestCreateEmptyRuleRejected(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	code, msg := errorMessage(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "approve everything", "enabled": true})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Contains(t, msg, "must constrain at least one")

	require.Empty(t, store.List(), "empty rule never persisted")
}

// S5-C: a rule with an invalid regex is rejected (4xx) with a message naming
// the offending field, on both POST and PUT, and is never persisted.
func TestInvalidRegexRejectedNamingField(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t,
		rulesPath,
		rule.Rule{ID: "fixed-id", Name: "valid", Enabled: true, TypeInclude: "^chore$"},
	)
	srv := newRulesServer(t, store)

	// POST with a bad scope regex.
	code, msg := errorMessage(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "bad", "enabled": true, "scope_include": "([a-z"})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Contains(t, msg, "scope_include", "the offending field is named")

	require.Len(t, store.List(), 1, "bad-regex rule never persisted")

	// PUT the existing valid rule to a bad description regex is also rejected.
	code, msg = errorMessage(t, http.MethodPut, srv.URL+apiPrefix+"/rules/fixed-id",
		map[string]any{"name": "valid", "enabled": true, "description_exclude": "(unclosed"})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Contains(t, msg, "description_exclude")

	// The existing rule is untouched (still its valid form).
	require.Equal(t, "^chore$", store.List()[0].TypeInclude)
	require.Empty(t, store.List()[0].DescriptionExclude, "rejected PUT did not mutate the rule")
}

// S5-D: Update and Delete on an unknown id return a clear not-found (404).
func TestUpdateDeleteUnknownIDNotFound(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	code := doJSON(t, http.MethodPut, srv.URL+apiPrefix+"/rules/nope",
		map[string]any{"name": "x", "enabled": true, "type_include": "^chore$"}, nil)
	require.Equal(t, http.StatusNotFound, code)

	code = doJSON(t, http.MethodDelete, srv.URL+apiPrefix+"/rules/nope", nil, nil)
	require.Equal(t, http.StatusNotFound, code)
}

// --- Slice 6: breaking-change invariant + Needs-Human-Review queue + manual approve ---

// queueNumbers returns the set of PR numbers in the queue feed.
func queueNumbers(items []server.QueueItem) map[int]server.QueueItem {
	out := map[int]server.QueueItem{}
	for _, q := range items {
		out[q.Number] = q
	}
	return out
}

// S6-A: a breaking-! PR that matches a rule is absent from /approvals and
// present in /queue with reason breaking_change; non-breaking matches still
// auto-approve in the same cycle. Status queue_count reflects the live queue.
func TestBreakingChangeRoutedToQueueNotApproved(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 40, Title: "chore: tidy", Author: "alice", URL: "https://github.com/o/r/pull/40", Checks: greenChecks()},
		github.PR{Number: 41, Title: "chore!: drop legacy flag", Author: "bob", URL: "https://github.com/o/r/pull/41", Additions: 40, Deletions: 12, ChangedFiles: 3, Checks: greenChecks()},
		github.PR{Number: 42, Title: "chore(api)!: rename field", Author: "carol", URL: "https://github.com/o/r/pull/42", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.True(t, got[40], "non-breaking chore auto-approved")
	require.False(t, got[41], "breaking chore is NOT auto-approved")
	require.False(t, got[42], "breaking scoped chore is NOT auto-approved")
	require.Equal(t, []int{40}, fake.ApprovedCalls(), "only the non-breaking PR is approved")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	q := queueNumbers(queue)
	require.Len(t, queue, 2)
	require.Equal(t, []string{"breaking_change"}, q[41].Reasons)
	require.Equal(t, []string{"breaking_change"}, q[42].Reasons)
	require.Equal(t, "chore!: drop legacy flag", q[41].Title, "queue carries the title")
	require.Equal(t, "bob", q[41].Author, "queue carries the author")
	require.Equal(t, "https://github.com/o/r/pull/41", q[41].URL, "queue carries the GitHub url")
	// Diff magnitude is threaded from the gh PR through the engine onto the wire
	// queue item (slice 3); the feed deliberately omits it.
	require.Equal(t, 40, q[41].Additions, "queue carries additions")
	require.Equal(t, 12, q[41].Deletions, "queue carries deletions")
	require.Equal(t, 3, q[41].ChangedFiles, "queue carries changed_files")

	var status server.CycleStatus
	getJSON(t, srv.URL+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.ApprovedCount)
	require.Equal(t, 2, status.QueueCount, "queue_count is the live queue size")
}

// S6-B: no rule, however permissive, can cause a breaking change to be
// auto-approved — the invariant gates approval around the OR-of-rules.
func TestNoRuleCanAutoApproveBreaking(t *testing.T) {
	// A maximally-permissive rule: matches any conventional commit of any type.
	store := storeWith(t, rule.Rule{Name: "approve everything typed", Enabled: true, TypeInclude: ".*"})
	fake := github.NewFake(
		github.PR{Number: 50, Title: "feat(service-a)!: breaking thing", Author: "teammate_a", URL: "u50", Checks: greenChecks()},
		github.PR{Number: 51, Title: "fix!: also breaking", Author: "alice", URL: "u51", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "the permissive rule did not auto-approve any breaking PR")

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Empty(t, feed)

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Len(t, queue, 2, "both breaking PRs routed to the queue")
}

// S6-Diff-A: GET /queue/{number}/diff returns the queued PR's changed files
// (filename/status/+N/−M/patch) plus total_files — the queue item's
// changed_files — so the Diff card can render per-file and show "first N of M".
// A file GitHub omits the patch for (binary) crosses the wire with an empty patch.
func TestQueueDiffReturnsFilesAndTotal(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 60, Title: "chore!: breaking chore", Author: "alice", URL: "u60",
			Additions: 902, Deletions: 100, ChangedFiles: 142, Checks: greenChecks()},
	)
	fake.SetDiff(60, []github.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1 @@\n+a\n-b"},
		{Filename: "logo.png", Status: "added", Additions: 0, Deletions: 0, Patch: ""},
	})
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var body server.PRDiffBody
	getJSON(t, srv.URL+apiPrefix+"/queue/60/diff", &body)
	require.Equal(t, 142, body.TotalFiles, "total_files is the PR's changed_files (banner: first 2 of 142)")
	require.Equal(t, []server.FileDiff{
		{Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1 +1 @@\n+a\n-b"},
		{Filename: "logo.png", Status: "added", Additions: 0, Deletions: 0, Patch: ""},
	}, body.Files)
}

// S6-Diff-B: GET /queue/{number}/diff for a number not in the queue is a 404 —
// the Diff pill is queue-only, so the endpoint never fetches a diff for a PR
// the human isn't looking at.
func TestQueueDiffUnqueuedIs404(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + apiPrefix + "/queue/999/diff")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// S6-C: POST /queue/{number}/approve approves the PR, records it as
// "human approval: <reasons joined>" in the feed, and it leaves the queue next
// cycle.
func TestManualApproveMovesToFeedAndLeavesQueue(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 60, Title: "chore!: breaking chore", Author: "alice", URL: "https://github.com/o/r/pull/60", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Len(t, queue, 1)

	// Manual override approve.
	var ok struct {
		OK     bool `json:"ok"`
		Number int  `json:"number"`
	}
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/queue/60/approve", nil, &ok)
	require.Equal(t, http.StatusOK, code)
	require.True(t, ok.OK)
	require.Equal(t, 60, ok.Number)
	require.Equal(t, []int{60}, fake.ApprovedCalls(), "manual approve calls the gh seam once")

	// It appears in the feed, attributed as a manual breaking override.
	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1)
	require.Equal(t, 60, feed[0].Number)
	require.Equal(t, "human approval: breaking_change", feed[0].MatchedRule)

	// The next cycle drops it from the queue (now in the dedup set) and does not
	// re-approve it.
	eng.RunCycleOnce(context.Background())
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Empty(t, queue, "manually-approved PR leaves the queue next cycle")
	require.Equal(t, []int{60}, fake.ApprovedCalls(), "not re-approved on the next cycle")
}

// S6-D: POST /queue/{number}/approve on a number that is not in the queue
// returns a clear 404.
func TestManualApproveUnknownNumberNotFound(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 70, Title: "chore!: breaking", Author: "alice", URL: "u70", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	// 999 was never a candidate, so it is not in the queue.
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/queue/999/approve", nil, nil)
	require.Equal(t, http.StatusNotFound, code)
	require.Empty(t, fake.ApprovedCalls(), "nothing approved for an unknown number")
}

// S6-E: the queue reflects current truth each cycle. An item leaves when its PR
// is no longer a candidate (merged/closed) or stops matching, tested by changing
// the fake's candidates between cycles.
func TestQueueRecomputedEachCycle(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 80, Title: "chore!: breaking one", Author: "alice", URL: "u80", Checks: greenChecks()},
		github.PR{Number: 81, Title: "chore!: breaking two", Author: "bob", URL: "u81", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())
	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Len(t, queue, 2, "both breaking PRs queued")

	// #80 merges/closes (no longer a candidate); #81 retitled to a non-breaking
	// title that still matches the chore rule -> auto-approved, leaves the queue.
	fake.Candidates = []github.PR{
		{Number: 81, Title: "chore: no longer breaking", Author: "bob", URL: "u81", Checks: greenChecks()},
		{Number: 82, Title: "chore!: a new breaking", Author: "carol", URL: "u82", Checks: greenChecks()},
	}

	eng.RunCycleOnce(context.Background())
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	q := queueNumbers(queue)
	require.Len(t, queue, 1, "queue recomputed: only the new breaking PR remains")
	_, has82 := q[82]
	require.True(t, has82, "the newly-appearing breaking PR is queued")
	_, has80 := q[80]
	require.False(t, has80, "the merged/closed PR left the queue")
	_, has81 := q[81]
	require.False(t, has81, "the now-non-breaking PR left the queue (it auto-approved)")

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.True(t, approvedNumbers(feed)[81], "#81 auto-approved once non-breaking")
}

// S6-F: a manual approve racing a concurrent cycle is safe — both go through
// the single locked approve() funnel, so the PR is approved exactly once and the
// race detector stays quiet. Run with -race to exercise the data-race guarantee.
func TestManualApproveRacesCycleSafely(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 90, Title: "chore!: breaking", Author: "alice", URL: "u90", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	// Seed the queue so #90 is manually-approvable.
	eng.RunCycleOnce(context.Background())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// A cycle running concurrently with the manual approve. Even if the
		// manual approve lands first, the cycle's approve() is an idempotent
		// no-op (PR now in dedup) and recomputes the queue without #90.
		eng.RunCycleOnce(context.Background())
	}()
	go func() {
		defer wg.Done()
		// Raw POST (not the require-based helper, which must not FailNow off the
		// test goroutine); correctness is asserted on the test goroutine below.
		resp, err := http.Post(srv.URL+apiPrefix+"/queue/90/approve", "application/json", nil)
		if err == nil {
			resp.Body.Close()
		}
	}()
	wg.Wait()

	// Drain one more cycle so the queue is recomputed after both finished.
	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1, "#90 approved exactly once despite the race")
	require.Equal(t, 90, feed[0].Number)
	require.Equal(t, "human approval: breaking_change", feed[0].MatchedRule, "manual override wins attribution")
	require.Equal(t, []int{90}, fake.ApprovedCalls(), "gh seam called exactly once")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Empty(t, queue, "#90 has left the queue")
}

// --- Slice 3: diff-size predicate, both classes ---

// S3-A: an Approve Rule carrying a DiffMax auto-approves only PRs whose
// additions+deletions is at or below that size; a larger PR is ignored. Diff
// size is summed from the two separate PR fields. Asserted through the HTTP
// seam with the fake GitHubClient as the only substitution.
func TestDiffMaxApproveRuleBoundsBySize(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "small chores", Enabled: true, TypeInclude: "^chore$", DiffMax: 50},
	)
	fake := github.NewFake(
		github.PR{Number: 100, Title: "chore: small", Author: "alice", URL: "u100", Additions: 30, Deletions: 20, Checks: greenChecks()}, // 50, at bound
		github.PR{Number: 101, Title: "chore: tiny", Author: "alice", URL: "u101", Additions: 1, Deletions: 0, Checks: greenChecks()},    // 1, below
		github.PR{Number: 102, Title: "chore: huge", Author: "alice", URL: "u102", Additions: 40, Deletions: 11, Checks: greenChecks()},  // 51, over
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.True(t, got[100], "diff at the bound is approved")
	require.True(t, got[101], "diff below the bound is approved")
	require.False(t, got[102], "diff over the bound is not approved")
	require.ElementsMatch(t, []int{100, 101}, fake.ApprovedCalls(), "only in-bound PRs approved exactly once")
}

// S3-B: a DiffMin lower bound matches a PR whose summed diff falls in
// [DiffMin, DiffMax] and ignores one below it. (Without slice 2's Class this
// rides the Approve path: an in-window PR is approved, an under-window PR is
// not.)
func TestDiffMinApproveRuleBoundsBySize(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "big chores", Enabled: true, TypeInclude: "^chore$", DiffMin: 100, DiffMax: 1000},
	)
	fake := github.NewFake(
		github.PR{Number: 110, Title: "chore: in window", Author: "alice", URL: "u110", Additions: 120, Deletions: 80, Checks: greenChecks()}, // 200, inside
		github.PR{Number: 111, Title: "chore: too small", Author: "alice", URL: "u111", Additions: 50, Deletions: 49, Checks: greenChecks()},  // 99, below
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.True(t, got[110], "PR with summed diff inside [DiffMin,DiffMax] is approved")
	require.False(t, got[111], "PR with summed diff below DiffMin is ignored")
	require.Equal(t, []int{110}, fake.ApprovedCalls())
}

// S3-C: a rule constraining only diff size is accepted on POST (not rejected
// as empty), and the diff fields round-trip through the wire DTO on GET.
func TestDiffOnlyRuleAcceptedAndRoundTrips(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	var created rule.Rule
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "diff only", "enabled": true, "diff_min": 10, "diff_max": 500},
		&created)
	require.Equal(t, http.StatusCreated, code, "a diff-only rule is not rejected as empty")

	var listed []rule.Rule
	getJSON(t, srv.URL+apiPrefix+"/rules", &listed)
	require.Len(t, listed, 1)
	require.Equal(t, 10, listed[0].DiffMin, "diff_min round-trips through the wire")
	require.Equal(t, 500, listed[0].DiffMax, "diff_max round-trips through the wire")
}

// S3-D: the empty-rule rejection message is class-neutral and mentions diff
// size (so a diff-aware client understands diff counts toward "non-empty").
func TestEmptyRuleMessageMentionsDiff(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	code, msg := errorMessage(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "approve everything", "enabled": true})
	require.Equal(t, http.StatusUnprocessableEntity, code)
	require.Contains(t, msg, "diff", "empty-rule message mentions diff size")
}

// S3-E: an inverted diff range (DiffMin > DiffMax, both non-zero) is rejected
// with a 4xx (ErrInvalidDiffRange) and never persisted.
func TestInvertedDiffRangeRejected(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	code, _ := errorMessage(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "inverted", "enabled": true, "diff_min": 100, "diff_max": 50})
	require.Equal(t, http.StatusUnprocessableEntity, code, "DiffMin > DiffMax is a 4xx")
	require.Empty(t, store.List(), "inverted-range rule never persisted")
}

// S3-F: huma rejects a negative diff_min/diff_max structurally on the wire
// (before any service-layer validation), returning 422.
func TestNegativeDiffRejectedStructurally(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "negative", "enabled": true, "diff_max": -1}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, code, "huma rejects a negative diff_max structurally")
	require.Empty(t, store.List(), "structurally-invalid rule never reaches the store")
}

// --- Slice 2: Review Rule class + evaluation precedence (HTTP seam) ---

// reviewCandidates spans the precedence matrix: an osixpatch chore (matched by a
// Review Rule only), a plain chore (matched by the Approve Rule only), a chore
// matched by BOTH classes, a breaking osixpatch chore, a breaking plain chore,
// and a non-conventional title (invisible to both classes).
// Every candidate carries a passing rollup so the all-green gate lets them
// through: the precedence matrix is about Review-vs-Approve and queueing, which
// happen AFTER the gate, so each PR must clear the gate first.
func reviewCandidates() []github.PR {
	return []github.PR{
		{Number: 200, Title: "chore(osixpatch): patch one", Author: "alice", URL: "u200", Checks: greenChecks()},
		{Number: 201, Title: "chore: plain approve", Author: "bob", URL: "u201", Checks: greenChecks()},
		{Number: 202, Title: "chore(osixpatch/deps): both", Author: "carol", URL: "u202", Checks: greenChecks()},
		{Number: 203, Title: "chore(osixpatch)!: breaking patch", Author: "dave", URL: "u203", Checks: greenChecks()},
		{Number: 204, Title: "chore!: breaking plain", Author: "erin", URL: "u204", Checks: greenChecks()},
		{Number: 205, Title: "not a conventional title", Author: "frank", URL: "u205", Checks: greenChecks()},
	}
}

// approveAllChores + reviewOsixpatch are the two rules driving the slice-2 matrix.
func approveAllChores() rule.Rule {
	return rule.Rule{Name: "team chores", Enabled: true, Class: "approve", TypeInclude: "^chore$"}
}

func reviewOsixpatch() rule.Rule {
	return rule.Rule{Name: "osixpatch gate", Enabled: true, Class: "review", ScopeInclude: "osixpatch"}
}

// S2-A: a PR matched by a Review Rule is absent from /approvals and present in
// /queue with the Review Rule's name in reasons — even when no Approve Rule
// matches it at all.
func TestReviewRuleQueuesNotApproves(t *testing.T) {
	// Approve Rule matches only "fix"; Review Rule matches osixpatch scope.
	store := storeWith(t,
		rule.Rule{Name: "approve fixes", Enabled: true, Class: "approve", TypeInclude: "^fix$"},
		reviewOsixpatch(),
	)
	fake := github.NewFake(
		// Matches the Review Rule (osixpatch) but NOT the Approve Rule (chore != fix).
		github.PR{Number: 210, Title: "chore(osixpatch): patch", Author: "alice", URL: "u210", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Empty(t, feed, "a Review-Rule match is never auto-approved")
	require.Empty(t, fake.ApprovedCalls(), "gh approve never called for a queued PR")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	q := queueNumbers(queue)
	require.Len(t, queue, 1)
	require.Equal(t, []string{"osixpatch gate"}, q[210].Reasons,
		"a Review-Rule match queues with the Review Rule's name, even with no Approve Rule matching")
}

// S2-B: a PR matched by BOTH an Approve Rule and a Review Rule is queued (not
// approved); reasons lists the Review Rule's name (Review Rules win). A plain
// chore matched only by the Approve Rule still auto-approves.
func TestReviewWinsOverApprove(t *testing.T) {
	store := storeWith(t, approveAllChores(), reviewOsixpatch())
	fake := github.NewFake(reviewCandidates()...)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)

	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.False(t, got[200], "osixpatch chore matched by a Review Rule is NOT approved")
	require.True(t, got[201], "a plain chore matched only by the Approve Rule auto-approves")
	require.False(t, got[202], "a chore matched by both classes is NOT approved")
	require.Equal(t, "team chores", matchedRules(feed)[201], "approve attributed to the Approve Rule")
	require.ElementsMatch(t, []int{201}, fake.ApprovedCalls())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	q := queueNumbers(queue)
	require.Equal(t, []string{"osixpatch gate"}, q[200].Reasons)
	require.Equal(t, []string{"osixpatch gate"}, q[202].Reasons,
		"a both-classes match queues with the Review Rule's name only (not approved)")
}

// S2-C: a breaking PR matching ONLY a Review Rule lists just the Review Rule —
// breaking_change stays tied to an Approve match.
func TestBreakingReasonTiedToApproveMatch(t *testing.T) {
	// Approve Rule matches only "fix" so #220 (a breaking osixpatch chore) does
	// NOT match the Approve Rule -> Review name only, no breaking_change.
	store := storeWith(t,
		rule.Rule{Name: "approve fixes", Enabled: true, Class: "approve", TypeInclude: "^fix$"},
		reviewOsixpatch(),
	)
	fake := github.NewFake(
		github.PR{Number: 220, Title: "chore(osixpatch)!: breaking patch", Author: "dave", URL: "u220", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Equal(t, []string{"osixpatch gate"}, queueNumbers(queue)[220].Reasons,
		"breaking PR matching ONLY a Review Rule lists just the Review Rule (breaking tied to Approve)")
}

// S2-D: a breaking PR matching BOTH a Review Rule and an Approve Rule lists both
// reasons, Review name first then breaking_change; a breaking plain chore
// (Approve match only) lists just breaking_change.
func TestBreakingWithReviewListsBoth(t *testing.T) {
	store := storeWith(t, approveAllChores(), reviewOsixpatch())
	fake := github.NewFake(
		github.PR{Number: 230, Title: "chore(osixpatch)!: breaking patch", Author: "dave", URL: "u230", Checks: greenChecks()},
		github.PR{Number: 231, Title: "chore!: breaking plain", Author: "erin", URL: "u231", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	q := queueNumbers(queue)
	require.Equal(t, []string{"osixpatch gate", "breaking_change"}, q[230].Reasons,
		"Review name first, then breaking_change last")
	require.Equal(t, []string{"breaking_change"}, q[231].Reasons,
		"breaking plain chore (Approve match only) lists just breaking_change")

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Empty(t, feed, "neither breaking PR is auto-approved")
}

// S2-E: a multi-reason queue item approved via POST /queue/{number}/approve
// records matched_rule as "human approval: <reasons joined>".
func TestMultiReasonManualApprovalRecordsAllReasons(t *testing.T) {
	store := storeWith(t, approveAllChores(), reviewOsixpatch())
	fake := github.NewFake(
		github.PR{Number: 240, Title: "chore(osixpatch)!: breaking patch", Author: "dave", URL: "u240", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Equal(t, []string{"osixpatch gate", "breaking_change"}, queueNumbers(queue)[240].Reasons)

	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/queue/240/approve", nil, nil)
	require.Equal(t, http.StatusOK, code)

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1)
	require.Equal(t, "human approval: osixpatch gate, breaking_change", feed[0].MatchedRule)
}

// S2-F: an empty/absent Class behaves as an Approve Rule, and a round-trip
// through PUT persists an explicit class; GET /rules returns both classes each
// carrying its class.
func TestClassDefaultsAndRoundTrips(t *testing.T) {
	rulesPath := filepath.Join(t.TempDir(), "rules.yaml")
	store := storeAt(t, rulesPath)
	srv := newRulesServer(t, store)

	// Create a rule with NO class field: it must persist as approve.
	var created rule.Rule
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "no class", "enabled": true, "type_include": "^chore$"},
		&created)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "approve", created.Class, "absent class defaults to approve on persist")

	// Create an explicit review rule.
	var reviewRule rule.Rule
	code = doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/rules",
		map[string]any{"name": "osixpatch gate", "enabled": true, "class": "review", "scope_include": "osixpatch"},
		&reviewRule)
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, "review", reviewRule.Class)

	// GET /rules returns both classes, each carrying its class on the wire.
	var listed []map[string]any
	getJSON(t, srv.URL+apiPrefix+"/rules", &listed)
	require.Len(t, listed, 2)
	classByName := map[string]string{}
	for _, r := range listed {
		classByName[r["name"].(string)] = r["class"].(string)
	}
	require.Equal(t, "approve", classByName["no class"])
	require.Equal(t, "review", classByName["osixpatch gate"])

	// A PUT round-trip persists an explicit class on disk.
	code = doJSON(t, http.MethodPut, srv.URL+apiPrefix+"/rules/"+created.ID,
		map[string]any{"name": "no class", "enabled": true, "type_include": "^chore$"}, nil)
	require.Equal(t, http.StatusOK, code)

	reloaded, err := rule.NewStore(rulesPath)
	require.NoError(t, err)
	for _, r := range reloaded.List() {
		require.NotEmpty(t, r.Class, "every persisted rule has an explicit class: %q", r.Name)
		if r.Name == "no class" {
			require.Equal(t, "approve", r.Class)
		}
	}
}

// S2-G: a disabled Review Rule stops gating (the PR auto-approves via the Approve
// Rule); a non-conventional title remains invisible to both classes.
func TestDisabledReviewRuleStopsGating(t *testing.T) {
	store := storeWith(t,
		approveAllChores(),
		rule.Rule{Name: "osixpatch gate", Enabled: false, Class: "review", ScopeInclude: "osixpatch"},
	)
	fake := github.NewFake(
		github.PR{Number: 250, Title: "chore(osixpatch): patch", Author: "alice", URL: "u250", Checks: greenChecks()},
		github.PR{Number: 251, Title: "not conventional at all", Author: "bob", URL: "u251", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	got := approvedNumbers(feed)
	require.True(t, got[250], "with the Review Rule disabled, the osixpatch chore auto-approves")
	require.False(t, got[251], "a non-conventional title is invisible to both classes")

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Empty(t, queue, "nothing queued: review rule disabled, non-conventional title skipped")
}

// --- Slice 1 (frontend redesign): server-parsed title_parts on the wire ---

// FR1: an approved PR ships its parsed title_parts (type, scopes, description)
// alongside the unchanged raw title; the verbatim slash/comma scope is split into
// the wire's scope list.
func TestApprovalShipsTitleParts(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 500, Title: "chore(deps,ci): bump x", Author: "alice", URL: "u500", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1)
	a := feed[0]
	require.Equal(t, "chore(deps,ci): bump x", a.Title, "raw title retained verbatim")
	require.Equal(t, "chore", a.TitleParts.Type)
	require.Equal(t, []string{"deps", "ci"}, a.TitleParts.Scopes, "comma scope split into the wire list")
	require.Equal(t, "bump x", a.TitleParts.Description)
	require.False(t, a.TitleParts.Breaking)
}

// FR2: a queued breaking PR ships title_parts.breaking=true and the parsed parts,
// independent of the queueing reason.
func TestQueueItemShipsBreakingTitleParts(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		github.PR{Number: 510, Title: "chore(team/service-a)!: rename field", Author: "bob", URL: "u510", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Len(t, queue, 1)
	q := queue[0]
	require.Equal(t, "chore(team/service-a)!: rename field", q.Title, "raw title retained verbatim")
	require.Equal(t, "chore", q.TitleParts.Type)
	require.Equal(t, []string{"team", "service-a"}, q.TitleParts.Scopes, "slash scope split into the wire list")
	require.Equal(t, "rename field", q.TitleParts.Description)
	require.True(t, q.TitleParts.Breaking, "breaking is a parsed part, independent of the queue reason")
}

// --- Slice 4 (frontend redesign): server-derived manual flag + today-scoped feed ---

// FR4-A: the feed carries a server-derived `manual` flag computed parse-on-read
// from the matched_rule prefix — true for a manual queue override ("human
// approval: …"), false for an auto-approval. This replaces the client's brittle
// matchedRule.startsWith("manual") sniff.
func TestApprovalManualFlagDerived(t *testing.T) {
	store := storeWith(t, matchAllChores())
	fake := github.NewFake(
		// Auto-approves (non-breaking chore).
		github.PR{Number: 600, Title: "chore: auto", Author: "alice", URL: "u600", Checks: greenChecks()},
		// Breaking chore -> queued, then manually approved below.
		github.PR{Number: 601, Title: "chore!: needs a human", Author: "bob", URL: "u601", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	srv := newTestServerFor(t, eng, store)
	eng.RunCycleOnce(context.Background())

	// Manually approve the queued breaking PR.
	code := doJSON(t, http.MethodPost, srv.URL+apiPrefix+"/queue/601/approve", nil, nil)
	require.Equal(t, http.StatusOK, code)

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	manual := map[int]bool{}
	for _, a := range feed {
		manual[a.Number] = a.Manual
	}
	require.True(t, manual[601], "a manual override (human approval: …) is flagged manual")
	require.False(t, manual[600], "an auto-approval is not flagged manual")
}

// FR4-B: a legacy-imported record (matched_rule "legacy (imported)") is not a
// manual override — the prefix check is exact, so only "human approval: …" flips
// the flag.
func TestLegacyApprovalNotManual(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	seedApprovalsFile(t, statePath, engine.Approval{
		Number:      700,
		Title:       "chore(x): imported",
		URL:         "https://github.com/o/r/pull/700",
		MatchedRule: "legacy (imported)",
		ApprovedAt:  time.Now(),
	})
	store := storeWith(t, matchAllChores())
	eng, err := engine.New(github.NewFake(), statePath, store)
	require.NoError(t, err)
	srv := newTestServerFor(t, eng, store)

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)
	require.Len(t, feed, 1)
	require.False(t, feed[0].Manual, "a legacy import is not a manual override")
}

// FR4-C: GET /approvals is today-scoped — it returns only entries with
// approved_at >= local midnight (workstation tz). The boundary is inclusive at
// midnight; anything before it (yesterday, the seeded historical approvals) is
// filtered out even though it stays in the engine's feed (dedup truth). The
// filter is computed from the same local midnight the test derives, so it is
// deterministic regardless of wall-clock time of day.
func TestApprovalsTodayScopedAtLocalMidnight(t *testing.T) {
	now := time.Now()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	// On-disk order is oldest-first; seed yesterday -> boundary -> today.
	seedApprovalsFile(t, statePath,
		engine.Approval{Number: 800, Title: "chore: yesterday", URL: "u800",
			MatchedRule: "team chores", ApprovedAt: midnight.Add(-time.Nanosecond)},
		engine.Approval{Number: 801, Title: "chore: at midnight", URL: "u801",
			MatchedRule: "team chores", ApprovedAt: midnight},
		engine.Approval{Number: 802, Title: "chore: today", URL: "u802",
			MatchedRule: "team chores", ApprovedAt: now},
	)
	store := storeWith(t, matchAllChores())
	eng, err := engine.New(github.NewFake(), statePath, store)
	require.NoError(t, err)
	srv := newTestServerFor(t, eng, store)

	var feed []server.Approval
	getJSON(t, srv.URL+apiPrefix+"/approvals", &feed)

	got := approvedNumbers(feed)
	require.False(t, got[800], "an approval before local midnight is filtered out")
	require.True(t, got[801], "an approval exactly at local midnight is shown (inclusive boundary)")
	require.True(t, got[802], "an approval from today is shown")
	require.Len(t, feed, 2, "only today's approvals are on the wire")
}
