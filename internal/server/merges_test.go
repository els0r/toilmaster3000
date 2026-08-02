package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/server"
	"github.com/stretchr/testify/require"
)

// seedMergesFile writes the given merge records to a merges.jsonl in the
// on-disk (oldest-first append) order, so a test can construct an engine over
// a ledger with controlled timestamps — the only way to exercise the
// today-scoped filter's boundary (the approvals-feed analog).
func seedMergesFile(t *testing.T, path string, recs ...engine.Merge) {
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

// mergesServer builds a server whose engine loads the given pre-seeded
// merges.jsonl and serves the given authored pull, returning the engine and
// the server URL.
func mergesServer(t *testing.T, mergesPath string, authored ...github.PR) (*engine.Engine, string) {
	t.Helper()
	fake := github.NewFake()
	fake.Authored = authored
	fake.SetMergeInfo(21, github.MergeDetails{
		Title: "feat(cli): live title", Body: "b",
		Reviews: []github.Review{{Author: "alice_osag", State: "APPROVED"}},
	})
	store := storeWith(t, matchAllChores())
	eng, err := engine.New(fake, filepath.Join(t.TempDir(), "approvals.jsonl"), mergesPath, store, testArms(t))
	require.NoError(t, err)
	h, err := server.New(testSPA(), eng, store, defaultSettings(t), "")
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return eng, srv.URL
}

// ME1 (tracer): a robot merge lands on GET /merges — the today-scoped wire
// ledger feeding the Merged station — with parse-on-read title parts (ADR
// 0006) and the normalized approver logins.
func TestMergesEndpointServesLedger(t *testing.T) {
	eng, url := mergesServer(t, tempMerges(t),
		github.PR{Number: 21, Title: "feat(cli): x", Author: "me", URL: "u21",
			Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "MERGEABLE"},
	)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background())

	var body []server.Merge
	getJSON(t, url+apiPrefix+"/merges", &body)

	require.Len(t, body, 1)
	require.Equal(t, 21, body[0].Number)
	require.Equal(t, "feat(cli): live title", body[0].Title, "the ledger records what landed")
	require.Equal(t, "feat", body[0].TitleParts.Type)
	require.Equal(t, "u21", body[0].URL)
	require.Equal(t, []string{"alice"}, body[0].ApprovedBy, "logins are normalized (_osag stripped)")
	require.False(t, body[0].MergedAt.IsZero())
}

// ME2: GET /merges is today-scoped — only records with merged_at >= local
// midnight cross the wire (inclusive boundary), newest-first; yesterday's
// merges live on only in the on-disk ledger. The exact analog of the
// approvals feed's today-scope.
func TestMergesEndpointTodayScoped(t *testing.T) {
	now := time.Now()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	mergesPath := tempMerges(t)
	seedMergesFile(t, mergesPath,
		engine.Merge{Number: 900, Title: "feat: yesterday", URL: "u900",
			MergedAt: midnight.Add(-time.Nanosecond), ApprovedBy: []string{"alice"}},
		engine.Merge{Number: 901, Title: "feat: at midnight", URL: "u901",
			MergedAt: midnight, ApprovedBy: []string{"alice"}},
		engine.Merge{Number: 902, Title: "feat: today", URL: "u902",
			MergedAt: now, ApprovedBy: []string{"alice"}},
	)
	_, url := mergesServer(t, mergesPath)

	var body []server.Merge
	getJSON(t, url+apiPrefix+"/merges", &body)

	require.Len(t, body, 2, "only today's merges are on the wire")
	require.Equal(t, []int{902, 901}, []int{body[0].Number, body[1].Number}, "newest-first, midnight inclusive")
}

// ME3: before any merge, GET /merges renders [] not null, so the frontend
// always maps over a real array.
func TestMergesEndpointEmptyIsArray(t *testing.T) {
	_, url := mergesServer(t, tempMerges(t))

	resp, err := http.Get(url + apiPrefix + "/merges")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.JSONEq(t, "[]", string(raw))
}

// ST1: GET /status widens with the outbound pair the heartbeat strip shows
// from every tab — ready_count (PRs waiting only on you, drawn from the live
// outbound snapshot like staging_count) and merged_count (today's ledger,
// the only durable merge signal: a merged PR leaves the is:open pull).
func TestStatusReportsReadyAndMergedCounts(t *testing.T) {
	now := time.Now()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	mergesPath := tempMerges(t)
	seedMergesFile(t, mergesPath,
		engine.Merge{Number: 900, Title: "feat: yesterday", URL: "u900",
			MergedAt: midnight.Add(-time.Nanosecond), ApprovedBy: []string{"alice"}},
		engine.Merge{Number: 902, Title: "feat: earlier today", URL: "u902",
			MergedAt: now, ApprovedBy: []string{"alice"}},
	)
	eng, url := mergesServer(t, mergesPath,
		// Two Ready PRs (one conflicted — still Ready, the stage ignores
		// mergeable) and one merely awaiting approval.
		github.PR{Number: 21, Title: "feat(cli): ready", Author: "me", URL: "u21",
			Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "MERGEABLE"},
		github.PR{Number: 22, Title: "feat(db): conflicted", Author: "me", URL: "u22",
			Checks: greenChecks(), ReviewDecision: "APPROVED", Mergeable: "CONFLICTING"},
		github.PR{Number: 23, Title: "feat(web): pending", Author: "me", URL: "u23",
			Checks: greenChecks(), ReviewDecision: "REVIEW_REQUIRED"},
	)

	var before server.CycleStatus
	getJSON(t, url+apiPrefix+"/status", &before)
	require.Zero(t, before.ReadyCount, "no snapshot before the first cycle")
	require.Equal(t, 1, before.MergedCount, "the seeded today merge counts even before a cycle")

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(21))
	eng.RunCycleOnce(context.Background()) // merges #21, appending a second today record

	var status server.CycleStatus
	getJSON(t, url+apiPrefix+"/status", &status)
	require.Equal(t, 1, status.ReadyCount, "the conflicted PR still counts — Ready ignores mergeable — but the just-merged one left Ready with the merge (ADR 0018)")
	require.Equal(t, 2, status.MergedCount, "today's ledger: the seeded record plus the fresh merge; yesterday's never counts")
}
