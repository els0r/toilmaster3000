package engine_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// testArms builds an armed store over a temp-dir armed.json — the throwaway
// consent set every engine test that does not assert arming itself wires in.
func testArms(t *testing.T) *armed.Store {
	t.Helper()
	arms, err := armed.NewStore(filepath.Join(t.TempDir(), "armed.json"))
	require.NoError(t, err)
	return arms
}

// armedEngine builds an engine over a fake with NO inbound candidates, the
// given authored PRs, and an armed store over the given armed.json path (so a
// test can reload the file to prove persistence). It returns the engine and
// the fake.
func armedEngine(t *testing.T, armedPath string, authored ...github.PR) (*engine.Engine, *github.Fake) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	arms, err := armed.NewStore(armedPath)
	require.NoError(t, err)

	fake := github.NewFake()
	fake.Authored = authored
	eng, err := engine.New(fake, statePath, store, arms)
	require.NoError(t, err)
	return eng, fake
}

// AR1 (tracer): an armed PR observed with CHANGES_REQUESTED is disarmed that
// cycle — the level-triggered disarm that makes Armed ∧ Changes-Requested an
// impossible state. An open objection always requires fresh consent, so the
// disarm is persisted (a restart must not resurrect the stale arm).
func TestArmedPRDisarmedOnChangesRequested(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 5, Title: "feat(e): pending review", Author: "me", URL: "u5", Checks: green(), ReviewDecision: "REVIEW_REQUIRED"}
	eng, fake := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(5))
	require.True(t, eng.ArmedSet()[5], "the PR is armed while awaiting approval")

	// A reviewer objects: the next cycle observes CHANGES_REQUESTED.
	pr.ReviewDecision = "CHANGES_REQUESTED"
	fake.Authored = []github.PR{pr}
	eng.RunCycleOnce(context.Background())

	require.False(t, eng.ArmedSet()[5], "an armed PR observed with CHANGES_REQUESTED is disarmed that cycle")

	reloaded, err := armed.NewStore(armedPath)
	require.NoError(t, err)
	require.False(t, reloaded.IsArmed(5), "the level-triggered disarm is persisted")
}

// AR2: arms are sticky across new pushes — arm-while-red is the core use
// case: an armed red PR that turns green (a fix-up commit ran the pipeline)
// stays armed through the stage change, no re-arm needed.
func TestArmStickyAcrossPushes(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 8, Title: "fix(ci): red for now", Author: "me", URL: "u8",
		Checks: []github.Check{{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"}}}
	eng, fake := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(8), "a red PR can be armed (arm anywhere except Changes Requested)")

	// The fix-up lands: pipeline green, approval in — several cycles later.
	pr.Checks = green()
	pr.ReviewDecision = "APPROVED"
	fake.Authored = []github.PR{pr}
	eng.RunCycleOnce(context.Background())
	eng.RunCycleOnce(context.Background())

	require.True(t, eng.ArmedSet()[8], "the arm survives new pushes and stage changes")
}

// AR3: an armed entry is cleaned up when its PR leaves the pull (merged or
// closed — the pull is is:open, so leaving IS merged/closed). The removal is
// persisted so a restart does not resurrect a stale arm.
func TestArmedEntryCleanedUpWhenPRLeavesPull(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 9, Title: "feat(a): soon merged", Author: "me", URL: "u9", Checks: green(), ReviewDecision: "APPROVED"}
	eng, fake := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(9))

	// The PR merges (or is closed): it leaves the is:open authored pull.
	fake.Authored = nil
	eng.RunCycleOnce(context.Background())

	require.False(t, eng.ArmedSet()[9], "a PR that left the pull is removed from the armed set")

	reloaded, err := armed.NewStore(armedPath)
	require.NoError(t, err)
	require.False(t, reloaded.IsArmed(9), "the cleanup is persisted")
}

// AR4: a FAILED outbound fetch never touches the armed set — consent is only
// ever withdrawn on fresh authored data, so a gh hiccup cannot silently disarm
// (or clean up) anything.
func TestFailedOutboundFetchKeepsArms(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 10, Title: "feat(b): armed", Author: "me", URL: "u10", Checks: green()}
	eng, fake := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(10))

	fake.AuthoredErr = errors.New("gh exploded")
	eng.RunCycleOnce(context.Background())

	require.True(t, eng.ArmedSet()[10], "a failed fetch clears the snapshot but never the arms")
}

// AR5: arming a Changes-Requested PR is rejected — the endpoint-facing half of
// the impossible-state guarantee (the level-triggered disarm is the other
// half): an open objection requires the feedback addressed and re-approval
// before consent can be given.
func TestArmRejectsChangesRequested(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 11, Title: "feat(c): objected", Author: "me", URL: "u11", Checks: green(), ReviewDecision: "CHANGES_REQUESTED"}
	eng, _ := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())

	err := eng.Arm(11)
	require.ErrorIs(t, err, engine.ErrChangesRequested)
	require.False(t, eng.ArmedSet()[11], "the rejected arm leaves the PR Withheld")
}

// AR6: arming a PR absent from the outbound snapshot is rejected — consent is
// only given over a PR the operator can see; before the first cycle NOTHING is
// armable (the snapshot is empty).
func TestArmRejectsUnknownPR(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 12, Title: "feat(d): mine", Author: "me", URL: "u12", Checks: green()}
	eng, _ := armedEngine(t, armedPath, pr)

	require.ErrorIs(t, eng.Arm(12), engine.ErrNotInOutbound, "nothing is armable before the first cycle")

	eng.RunCycleOnce(context.Background())
	require.ErrorIs(t, eng.Arm(999), engine.ErrNotInOutbound, "a stranger number is rejected")
	require.NoError(t, eng.Arm(12), "the snapshot PR arms fine")
}

// AR7: Disarm withdraws consent from any stage — no snapshot check (always
// safe) — and is idempotent on a Withheld PR.
func TestDisarmWithdrawsConsent(t *testing.T) {
	armedPath := filepath.Join(t.TempDir(), "armed.json")
	pr := github.PR{Number: 13, Title: "feat(e): armed then not", Author: "me", URL: "u13", Checks: green()}
	eng, _ := armedEngine(t, armedPath, pr)

	eng.RunCycleOnce(context.Background())
	require.NoError(t, eng.Arm(13))
	require.NoError(t, eng.Disarm(13))
	require.False(t, eng.ArmedSet()[13])
	require.NoError(t, eng.Disarm(13), "disarming a Withheld PR is a quiet no-op")
}
