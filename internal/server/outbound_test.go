package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/stretchr/testify/require"
)

// outboundServer builds a server whose engine has run one cycle over an
// authored pull hitting every outbound stage — In Discussion included (PR 18
// is approved with two unresolved review threads) — and returns the running
// server URL.
func outboundServer(t *testing.T) string {
	t.Helper()
	fake := github.NewFake()
	fake.Authored = []github.PR{
		{Number: 11, Title: "feat(ui): wip panel", Author: "me", URL: "u11", IsDraft: true, Checks: greenChecks()},
		{Number: 12, Title: "fix(api): broken", Author: "me", URL: "u12", Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}},
		{Number: 13, Title: "fix(ci): building", Author: "me", URL: "u13", Checks: []github.Check{{Typename: "CheckRun", Status: "IN_PROGRESS"}}},
		{Number: 14, Title: "feat(db): objected", Author: "me", URL: "u14", Checks: greenChecks(), ReviewDecision: "CHANGES_REQUESTED"},
		{Number: 15, Title: "feat(web): pending", Author: "me", URL: "u15", Checks: greenChecks(), ReviewDecision: "REVIEW_REQUIRED", Additions: 40, Deletions: 2, ChangedFiles: 3},
		{Number: 16, Title: "feat(cli)!: approved", Author: "me", URL: "u16", Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "CONFLICTING"},
		{Number: 17, Title: "feat(sdk): approved too", Author: "me", URL: "u17", Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "MERGEABLE"},
		{Number: 18, Title: "feat(gw): approved with nits", Author: "me", URL: "u18", Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "MERGEABLE"},
	}
	fake.SetThreads(18, github.RawReviewThreads{
		Nodes: []github.ReviewThread{{IsResolved: false}, {IsResolved: false}, {IsResolved: true}},
	})
	eng, store := newEngine(t, fake)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)
	return srv.URL
}

// OB1 (tracer): GET /outbound returns the per-stage lists — in_discussion
// included (ADR 0019) — the distribution counts (summing to outgoing), the
// conflict flag on Ready rows, per-row unresolved-thread counts, and
// parse-on-read title parts — the outbound wire shape (snake_case DTOs, ADR
// 0002/0006).
func TestOutboundSnapshotMapping(t *testing.T) {
	url := outboundServer(t)

	var body server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &body)

	require.Equal(t, 8, body.Outgoing, "outgoing is the raw authored pull")

	require.Len(t, body.Draft, 1)
	require.Equal(t, 11, body.Draft[0].Number)
	require.Len(t, body.Red, 1)
	require.Equal(t, 12, body.Red[0].Number)
	require.Len(t, body.Running, 1)
	require.Equal(t, 13, body.Running[0].Number)
	require.Len(t, body.ChangesRequested, 1)
	require.Equal(t, 14, body.ChangesRequested[0].Number)
	require.Len(t, body.AwaitingApproval, 1)
	require.Equal(t, 15, body.AwaitingApproval[0].Number)
	require.Len(t, body.InDiscussion, 1)
	require.Equal(t, 18, body.InDiscussion[0].Number)
	require.Len(t, body.Ready, 2)

	// The unresolved-thread count rides each row: the judged count on the
	// in-discussion row, zero on a Ready row (that is what Ready means).
	require.Equal(t, 2, body.InDiscussion[0].UnresolvedThreads)
	require.Zero(t, body.Ready[0].UnresolvedThreads)

	// Distribution counts partition Outgoing by construction.
	require.Equal(t, 1, body.Distribution.Draft)
	require.Equal(t, 1, body.Distribution.Red)
	require.Equal(t, 1, body.Distribution.Running)
	require.Equal(t, 1, body.Distribution.ChangesRequested)
	require.Equal(t, 1, body.Distribution.AwaitingApproval)
	require.Equal(t, 1, body.Distribution.InDiscussion)
	require.Equal(t, 2, body.Distribution.Ready)
	sum := body.Distribution.Draft + body.Distribution.Red + body.Distribution.Running +
		body.Distribution.ChangesRequested + body.Distribution.AwaitingApproval +
		body.Distribution.InDiscussion + body.Distribution.Ready
	require.Equal(t, body.Outgoing, sum)

	// The heartbeat's ready keeps counting ONLY Ready — In Discussion has no
	// heartbeat count, so the strip stays actionable-signals-only (ADR 0019).
	var status server.CycleStatus
	getJSON(t, url+apiPrefix+"/status", &status)
	require.Equal(t, 2, status.ReadyCount, "ready excludes the in-discussion row")

	// A conflicted Ready row stays in Ready carrying the conflict flag; a
	// MERGEABLE one carries none. UNKNOWN (not conflicting) would carry none
	// either — conflict is derived from CONFLICTING alone.
	require.True(t, body.Ready[0].Conflict, "CONFLICTING derives conflict=true")
	require.False(t, body.Ready[1].Conflict, "MERGEABLE derives conflict=false")

	// Parse-on-read title parts ride each row (ADR 0006), including breaking.
	require.Equal(t, "feat", body.Ready[0].TitleParts.Type)
	require.True(t, body.Ready[0].TitleParts.Breaking)
	// Diff magnitude rides each row from the single authored list call.
	require.Equal(t, 40, body.AwaitingApproval[0].Additions)
	require.Equal(t, 2, body.AwaitingApproval[0].Deletions)
	require.Equal(t, 3, body.AwaitingApproval[0].ChangedFiles)

	// The Outgoing station shows the fixed derived search as a code chip.
	require.Equal(t, github.AuthoredSearch, body.Search)
}

// armedAuthored is a minimal authored pull spanning the armable stages plus a
// Changes-Requested PR, for the arm/disarm endpoint tests.
func armedAuthored() []github.PR {
	return []github.PR{
		{Number: 21, Title: "feat(a): red", Author: "me", URL: "u21",
			Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}},
		{Number: 22, Title: "feat(b): objected", Author: "me", URL: "u22", Checks: greenChecks(), ReviewDecision: "CHANGES_REQUESTED"},
		{Number: 23, Title: "feat(c): pending", Author: "me", URL: "u23", Checks: greenChecks(), ReviewDecision: "REVIEW_REQUIRED"},
	}
}

// newArmedOutboundServer builds a server whose engine has run one cycle over
// the given authored pull, with the armed store over an explicit armed.json
// path so a restart test can rebuild everything over the same file. It returns
// the server URL.
func newArmedOutboundServer(t *testing.T, armedPath string, authored []github.PR) string {
	t.Helper()
	fake := github.NewFake()
	fake.Authored = authored
	store := storeWith(t, matchAllChores())
	arms, err := armed.NewStore(armedPath)
	require.NoError(t, err)
	eng, err := engine.New(fake, filepath.Join(t.TempDir(), "approvals.jsonl"), tempMerges(t), store, arms)
	require.NoError(t, err)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)
	return srv.URL
}

// armedFlags returns each row's armed flag keyed by PR number across every
// stage list of an /outbound body.
func armedFlags(body server.Outbound) map[int]bool {
	out := map[int]bool{}
	for _, stage := range [][]server.OutboundItem{
		body.Draft, body.Red, body.Running,
		body.ChangesRequested, body.AwaitingApproval,
		body.InDiscussion, body.Ready,
	} {
		for _, it := range stage {
			out[it.Number] = it.Armed
		}
	}
	return out
}

// OB-A1 (tracer): POST /outbound/{number}/arm arms a PR, and every /outbound
// row carries an armed flag — true on the armed row (whatever its stage: here
// a RED one, arm-while-red being the core use case), false elsewhere (the
// Withheld default).
func TestArmEndpointArmsAndOutboundCarriesFlag(t *testing.T) {
	url := newArmedOutboundServer(t, filepath.Join(t.TempDir(), "armed.json"), armedAuthored())

	var before server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &before)
	require.Equal(t, map[int]bool{21: false, 22: false, 23: false}, armedFlags(before),
		"every authored PR defaults to Withheld")

	code := doJSON(t, http.MethodPost, url+apiPrefix+"/outbound/21/arm", nil, nil)
	require.Equal(t, http.StatusOK, code)

	var after server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &after)
	require.Equal(t, map[int]bool{21: true, 22: false, 23: false}, armedFlags(after),
		"the armed flag rides the row immediately (no cycle needed), others stay Withheld")
}

// OB-A2: arming a Changes-Requested PR is a 4xx — Armed ∧ Changes-Requested is
// an impossible state, and the endpoint is one of its two guards (the
// level-triggered disarm is the other).
func TestArmEndpointRejectsChangesRequested(t *testing.T) {
	url := newArmedOutboundServer(t, filepath.Join(t.TempDir(), "armed.json"), armedAuthored())

	code, msg := errorMessage(t, http.MethodPost, url+apiPrefix+"/outbound/22/arm", nil)
	require.Equal(t, http.StatusConflict, code)
	require.Contains(t, msg, "changes requested")

	var body server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &body)
	require.False(t, armedFlags(body)[22], "the rejected PR stays Withheld")
}

// OB-A3: arming a PR absent from the outbound snapshot is a 404 — consent is
// only ever given over a PR the operator can see.
func TestArmEndpointUnknownPRIs404(t *testing.T) {
	url := newArmedOutboundServer(t, filepath.Join(t.TempDir(), "armed.json"), armedAuthored())

	code, _ := errorMessage(t, http.MethodPost, url+apiPrefix+"/outbound/999/arm", nil)
	require.Equal(t, http.StatusNotFound, code)
}

// OB-A4: DELETE /outbound/{number}/arm disarms — the toggle's other half —
// and the row reads Withheld again on the next fetch.
func TestDisarmEndpointDisarms(t *testing.T) {
	url := newArmedOutboundServer(t, filepath.Join(t.TempDir(), "armed.json"), armedAuthored())

	require.Equal(t, http.StatusOK, doJSON(t, http.MethodPost, url+apiPrefix+"/outbound/23/arm", nil, nil))
	require.Equal(t, http.StatusOK, doJSON(t, http.MethodDelete, url+apiPrefix+"/outbound/23/arm", nil, nil))

	var body server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &body)
	require.False(t, armedFlags(body)[23], "the disarmed PR is Withheld again")
}

// OB-A5: the armed set survives a tm3k restart — a fresh engine + server over
// the same armed.json still reports the row armed after its first cycle.
func TestArmedSurvivesRestart(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")

	url1 := newArmedOutboundServer(t, armedPath, armedAuthored())
	require.Equal(t, http.StatusOK, doJSON(t, http.MethodPost, url1+apiPrefix+"/outbound/21/arm", nil, nil))

	// Restart: everything rebuilt over the same armed.json.
	url2 := newArmedOutboundServer(t, armedPath, armedAuthored())
	var body server.Outbound
	getJSON(t, url2+apiPrefix+"/outbound", &body)
	require.True(t, armedFlags(body)[21], "the arm survives the restart")
}

// OB2: before any cycle (a fresh restart), /outbound renders the empty
// snapshot — zero counts and empty (non-null) lists, so the frontend always
// maps over a real array.
func TestOutboundEmptyBeforeFirstCycle(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + apiPrefix + "/outbound")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raw map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.Equal(t, float64(0), raw["outgoing"])
	for _, k := range []string{"draft", "red", "running", "changes_requested", "awaiting_approval", "in_discussion", "ready"} {
		require.NotNil(t, raw[k], "%s renders [] not null", k)
		require.Empty(t, raw[k], "%s is empty before the first cycle", k)
	}
}
