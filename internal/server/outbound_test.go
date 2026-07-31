package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/stretchr/testify/require"
)

// outboundServer builds a server whose engine has run one cycle over an
// authored pull hitting every outbound stage, and returns the running server
// URL.
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
	}
	eng, store := newEngine(t, fake)
	eng.RunCycleOnce(context.Background())
	srv := newTestServerFor(t, eng, store)
	return srv.URL
}

// OB1 (tracer): GET /outbound returns the per-stage lists, the distribution
// counts (summing to outgoing), the conflict flag on Ready rows, and
// parse-on-read title parts — the outbound wire shape (snake_case DTOs, ADR
// 0002/0006).
func TestOutboundSnapshotMapping(t *testing.T) {
	url := outboundServer(t)

	var body server.Outbound
	getJSON(t, url+apiPrefix+"/outbound", &body)

	require.Equal(t, 7, body.Outgoing, "outgoing is the raw authored pull")

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
	require.Len(t, body.Ready, 2)

	// Distribution counts partition Outgoing by construction.
	require.Equal(t, 1, body.Distribution.Draft)
	require.Equal(t, 1, body.Distribution.Red)
	require.Equal(t, 1, body.Distribution.Running)
	require.Equal(t, 1, body.Distribution.ChangesRequested)
	require.Equal(t, 1, body.Distribution.AwaitingApproval)
	require.Equal(t, 2, body.Distribution.Ready)
	sum := body.Distribution.Draft + body.Distribution.Red + body.Distribution.Running +
		body.Distribution.ChangesRequested + body.Distribution.AwaitingApproval + body.Distribution.Ready
	require.Equal(t, body.Outgoing, sum)

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
	for _, k := range []string{"draft", "red", "running", "changes_requested", "awaiting_approval", "ready"} {
		require.NotNil(t, raw[k], "%s renders [] not null", k)
		require.Empty(t, raw[k], "%s is empty before the first cycle", k)
	}
}
