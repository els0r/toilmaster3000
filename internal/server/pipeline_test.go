package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/stretchr/testify/require"
)

// pipelineServer builds a server whose engine has run one cycle over candidates
// hitting every terminal funnel bucket: one chore approved, one docs queued
// (Review Rule), one feat staged (no rule), one draft dropped, one red dropped,
// one approved elsewhere. It returns the running server URL.
func pipelineServer(t *testing.T) string {
	t.Helper()
	store := storeWith(t,
		rule.Rule{Name: "chore approve", Class: "approve", Enabled: true, TypeInclude: "^chore$"},
		rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"},
	)
	red := []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}, {Typename: "StatusContext", State: "PENDING"}}
	fake := github.NewFake(
		github.PR{Number: 1, Title: "chore(api): bump", Author: "al", URL: "u1", Checks: greenChecks()},
		github.PR{Number: 2, Title: "docs(team/web): readme", Author: "bo", URL: "u2", Checks: greenChecks()},
		github.PR{Number: 3, Title: "feat(ui): panel", Author: "ca", URL: "u3", Checks: greenChecks(), Additions: 120, Deletions: 8, ChangedFiles: 5},
		github.PR{Number: 4, Title: "chore: wip", Author: "de", URL: "u4", IsDraft: true, Checks: greenChecks()},
		github.PR{Number: 5, Title: "chore: flaky", Author: "ed", URL: "u5", Checks: red},
		github.PR{Number: 6, Title: "chore: theirs", Author: "fa", URL: "u6", Checks: greenChecks(), ReviewDecision: "APPROVED"},
	)
	eng := newEngineWith(t, fake, store)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)
	return srv.URL
}

// P1 (tracer): GET /pipeline returns the live snapshot's four lists, the
// distribution counts, and approved_this_cycle, with parse-on-read title parts
// on each itemized row — the funnel's wire shape (snake_case DTOs, ADR 0002/0006).
func TestPipelineSnapshotMapping(t *testing.T) {
	url := pipelineServer(t)

	var body server.Pipeline
	getJSON(t, url+apiPrefix+"/pipeline", &body)

	// Distribution counts partition Incoming.
	require.Equal(t, 6, body.Incoming)
	require.Equal(t, 1, body.NeedsHumanReview)
	require.Equal(t, 1, body.ApprovedByTm3k, "the chore approved this cycle is a standing dedup member")
	require.Equal(t, 1, body.ApprovedThisCycle)

	// The four itemized lists.
	require.Len(t, body.DroppedDraft, 1)
	require.Equal(t, 4, body.DroppedDraft[0].Number)

	require.Len(t, body.DroppedRed, 1)
	require.Equal(t, 5, body.DroppedRed[0].Number)
	require.Equal(t, 2, body.DroppedRed[0].FailingChecks, "one FAILURE + one PENDING are non-passing")

	require.Len(t, body.Staging, 1)
	require.Equal(t, 3, body.Staging[0].Number)
	// Parse-on-read title parts ride each row.
	require.Equal(t, "feat", body.Staging[0].TitleParts.Type)
	require.Equal(t, []string{"ui"}, body.Staging[0].TitleParts.Scopes)
	// Diff magnitude rides each row from the single list fetch (the Staging area
	// renders it; threaded from github.PR's Additions/Deletions/ChangedFiles).
	require.Equal(t, 120, body.Staging[0].Additions)
	require.Equal(t, 8, body.Staging[0].Deletions)
	require.Equal(t, 5, body.Staging[0].ChangedFiles)

	require.Len(t, body.ApprovedElsewhere, 1)
	require.Equal(t, 6, body.ApprovedElsewhere[0].Number)

	// The partition sums to Incoming on the wire.
	sum := len(body.DroppedRed) + len(body.DroppedDraft) + len(body.Staging) +
		body.NeedsHumanReview + body.ApprovedByTm3k + len(body.ApprovedElsewhere)
	require.Equal(t, body.Incoming, sum)
}

// P2: before any cycle (a fresh restart), /pipeline renders the empty snapshot —
// zero counts and empty (non-null) lists, so the frontend always maps over a
// real array.
func TestPipelineEmptyBeforeFirstCycle(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + apiPrefix + "/pipeline")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raw map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.Equal(t, float64(0), raw["incoming"])
	require.Equal(t, float64(0), raw["approved_this_cycle"])
	for _, k := range []string{"dropped_red", "dropped_draft", "staging", "screening", "approved_elsewhere"} {
		require.NotNil(t, raw[k], "%s renders [] not null", k)
		require.Empty(t, raw[k], "%s is empty before the first cycle", k)
	}
}

// P3: /pipeline itemizes the Screening segment — each awaiting-verdict PR with
// its pending_screens names (the station's "why hasn't #N gone through?"
// signal) — and /queue carries a screen-held entry's screen:<name> reasons plus
// the {screen, reason} screen_holds prose (ADR 0022's wire consequences).
func TestPipelineScreeningAndQueueScreenHolds(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "chore approve", Class: "approve", Enabled: true, TypeInclude: "^chore$"},
	)
	fake := github.NewFake(
		github.PR{Number: 1, Title: "chore(api): pending", Author: "al", URL: "u1", Checks: greenChecks(), HeadSHA: "h1"},
		github.PR{Number: 2, Title: "chore(auth): held", Author: "bo", URL: "u2", Checks: greenChecks(), HeadSHA: "h2"},
	)

	// The verdict store is pre-seeded: #2 carries a recorded hold; #1 has no
	// row and its screen never answers within the test, so it stays pending.
	verdicts, err := hook.NewVerdictStore(filepath.Join(t.TempDir(), "verdicts.jsonl"))
	require.NoError(t, err)
	require.NoError(t, verdicts.Append(hook.VerdictRecord{
		ScreenID: "id-sec", Number: 2, Head: "h2", Outcome: hook.Hold, Reason: "touches auth code", At: time.Now(),
	}))
	spec := hook.Spec{ID: "id-sec", Name: "security", Harness: "claude", Prompt: "p", Enabled: true}
	screener := hook.NewScreener(verdicts, hook.ScreenInstance{Spec: spec, Screen: blockedScreen{}})

	eng, err := engine.New(fake, filepath.Join(t.TempDir(), "approvals.jsonl"), tempMerges(t), store, testArms(t), screener)
	require.NoError(t, err)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)

	var body server.Pipeline
	getJSON(t, srv.URL+apiPrefix+"/pipeline", &body)
	require.Len(t, body.Screening, 1)
	require.Equal(t, 1, body.Screening[0].Number)
	require.Equal(t, []string{"security"}, body.Screening[0].PendingScreens)
	require.Equal(t, "chore", body.Screening[0].TitleParts.Type, "parse-on-read title parts ride screening rows too")
	require.Equal(t, 1, body.NeedsHumanReview, "the held PR counts in the queue segment")

	// The wire partition sums to Incoming with the screening list in place.
	sum := len(body.DroppedRed) + len(body.DroppedDraft) + len(body.Staging) + len(body.Screening) +
		body.NeedsHumanReview + body.ApprovedByTm3k + len(body.ApprovedElsewhere)
	require.Equal(t, body.Incoming, sum)

	var queue []server.QueueItem
	getJSON(t, srv.URL+apiPrefix+"/queue", &queue)
	require.Len(t, queue, 1)
	require.Equal(t, 2, queue[0].Number)
	require.Equal(t, []string{"screen:security"}, queue[0].Reasons)
	require.Equal(t, []server.ScreenHold{{Screen: "security", Reason: "touches auth code"}}, queue[0].ScreenHolds)
}

// P4: a rule-routed queue entry renders screen_holds as [] not null (rule
// reasons XOR screen holds — the empty side must still be a real array).
func TestQueueScreenHoldsEmptyOnRuleRoutedEntries(t *testing.T) {
	store := storeWith(t,
		rule.Rule{Name: "docs gate", Class: "review", Enabled: true, TypeInclude: "^docs$"},
	)
	fake := github.NewFake(
		github.PR{Number: 3, Title: "docs: gate me", Author: "ca", URL: "u3", Checks: greenChecks()},
	)
	eng := newEngineWith(t, fake, store)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)

	resp, err := http.Get(srv.URL + apiPrefix + "/queue")
	require.NoError(t, err)
	defer resp.Body.Close()
	var raw []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.Len(t, raw, 1)
	require.NotNil(t, raw[0]["screen_holds"], "screen_holds renders [] not null")
	require.Empty(t, raw[0]["screen_holds"])
}

// blockedScreen is a Screen whose run never returns within a test: the
// dispatched goroutine parks on the context, so the pending PR provably stays
// in Screening while the wire is asserted.
type blockedScreen struct{}

func (blockedScreen) Screen(ctx context.Context, _ hook.PRContext) (hook.Verdict, error) {
	<-ctx.Done()
	return hook.Verdict{}, ctx.Err()
}
