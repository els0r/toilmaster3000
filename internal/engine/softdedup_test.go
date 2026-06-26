package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// softDedupEngine builds an engine whose single permissive rule matches any
// chore-typed candidate, over a fresh approvals.jsonl. It returns the engine,
// the fake client, and the ledger path so a test can assert both the approve
// call count and what was (not) written to approvals.jsonl.
func softDedupEngine(t *testing.T, candidates ...github.PR) (*engine.Engine, *github.Fake, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	_, err = store.Create(rule.Rule{Name: "any chore", Enabled: true, TypeInclude: "^chore$"})
	require.NoError(t, err)

	fake := github.NewFake(candidates...)
	eng, err := engine.New(fake, statePath, store)
	require.NoError(t, err)
	return eng, fake, statePath
}

func greenCheck() []github.Check {
	return []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"}}
}

// SD1 (tracer): a candidate GitHub already reports as APPROVED — but whose
// number is absent from approvals.jsonl — was approved elsewhere. tm3k leaves
// it alone: it never calls Approve and writes nothing to the ledger (proven at
// the RunCycleOnce cycle seam via the approve-call count and the ledger file).
func TestApprovedElsewhereIsNotReapprovedAndWritesNothing(t *testing.T) {
	pr := github.PR{
		Number: 7, Title: "chore: tidy", Author: "teammate", URL: "u7",
		Checks: greenCheck(), ReviewDecision: "APPROVED",
	}
	eng, fake, statePath := softDedupEngine(t, pr)

	eng.RunCycleOnce(context.Background())

	require.Empty(t, fake.ApprovedCalls(), "an approved-elsewhere PR is never approved by tm3k")
	require.Empty(t, eng.Approvals(), "an approved-elsewhere PR is recorded nowhere in the feed")
	_, err := os.Stat(statePath)
	require.True(t, os.IsNotExist(err), "an approved-elsewhere PR writes nothing to the ledger")
}

// SD2: a candidate that is APPROVED and already ours (its own prior approval
// sits in approvals.jsonl) stays a quiet no-op across cycles — never re-approved,
// the feed never grows. The already-deduped guard owns this case; reporting
// APPROVED does not turn it into a new approval nor a second ledger line.
func TestApprovedAndOursStaysQuietNoOp(t *testing.T) {
	pr := github.PR{
		Number: 9, Title: "chore: ours", Author: "me", URL: "u9",
		Checks: greenCheck(),
	}
	eng, fake, _ := softDedupEngine(t, pr)

	// First cycle: tm3k approves it (no reviewDecision yet — fresh candidate).
	eng.RunCycleOnce(context.Background())
	require.Equal(t, []int{9}, fake.ApprovedCalls(), "tm3k approves the matching PR on the first cycle")
	require.Len(t, eng.Approvals(), 1)

	// GitHub now reports our own approval back as APPROVED on the next fetch.
	fake.Candidates = []github.PR{{
		Number: 9, Title: "chore: ours", Author: "me", URL: "u9",
		Checks: greenCheck(), ReviewDecision: "APPROVED",
	}}
	eng.RunCycleOnce(context.Background())

	require.Equal(t, []int{9}, fake.ApprovedCalls(), "an approved-and-ours PR is a quiet no-op: never re-approved")
	require.Len(t, eng.Approvals(), 1, "the feed does not grow for an approved-and-ours PR")
}

// SD3: the soft dedup is precise to APPROVED. A matching candidate whose
// reviewDecision is anything else (REVIEW_REQUIRED, CHANGES_REQUESTED, or empty)
// is NOT approved-elsewhere — tm3k approves it normally and records the ledger
// line, exactly as before this narrowing.
func TestNonApprovedReviewDecisionStillApprovesNormally(t *testing.T) {
	prs := []github.PR{
		{Number: 11, Title: "chore: pending review", Author: "x", URL: "u11", Checks: greenCheck(), ReviewDecision: "REVIEW_REQUIRED"},
		{Number: 12, Title: "chore: changes asked", Author: "y", URL: "u12", Checks: greenCheck(), ReviewDecision: "CHANGES_REQUESTED"},
		{Number: 13, Title: "chore: no decision", Author: "z", URL: "u13", Checks: greenCheck(), ReviewDecision: ""},
	}
	eng, fake, _ := softDedupEngine(t, prs...)

	eng.RunCycleOnce(context.Background())

	require.Equal(t, []int{11, 12, 13}, fake.ApprovedCalls(), "only APPROVED is approved-elsewhere; every other review decision approves normally")
	require.Len(t, eng.Approvals(), 3, "each non-APPROVED candidate writes its ledger line")
}
