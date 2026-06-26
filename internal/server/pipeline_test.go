package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
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
		github.PR{Number: 3, Title: "feat(ui): panel", Author: "ca", URL: "u3", Checks: greenChecks()},
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
	for _, k := range []string{"dropped_red", "dropped_draft", "staging", "approved_elsewhere"} {
		require.NotNil(t, raw[k], "%s renders [] not null", k)
		require.Empty(t, raw[k], "%s is empty before the first cycle", k)
	}
}
