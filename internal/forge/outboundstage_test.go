package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestClassifyOutboundStage pins the outbound funnel partition: every authored
// PR folds into EXACTLY one stage, with the documented precedence
// draft > not-green (red | running) > changes-requested > awaiting-approval >
// in-discussion > ready (CONTEXT "Outbound", ADR 0016/0019). Mergeable is a
// merge precondition, not a stage boundary — it never moves a PR between
// stages. The unresolved-thread count is the fold's second input (ADR 0019):
// it splits Ready from In Discussion and is inert below awaiting-approval —
// precedence names the primary blocker.
//
// Every input is the NEUTRAL vocabulary: the fold never sees a raw forge
// string (ADR 0030 §3).
func TestClassifyOutboundStage(t *testing.T) {
	green := []forge.Check{{State: forge.CheckPass}}
	red := []forge.Check{{State: forge.CheckFail}}
	running := []forge.Check{{State: forge.CheckPending}}

	tests := []struct {
		name       string
		pr         forge.PR
		unresolved int
		want       forge.OutboundStage
	}{
		{
			name: "draft wins over everything (draft + red + changes-requested)",
			pr:   forge.PR{IsDraft: true, Checks: red, ReviewDecision: forge.ReviewChangesRequested},
			want: forge.OutboundStageDraft,
		},
		{
			name: "not-green wins over changes-requested (red + changes-requested is red)",
			pr:   forge.PR{Checks: red, ReviewDecision: forge.ReviewChangesRequested},
			want: forge.OutboundStageRed,
		},
		{
			name: "one failed check makes the PR red even while others still run",
			pr:   forge.PR{Checks: []forge.Check{{State: forge.CheckFail}, {State: forge.CheckPending}}},
			want: forge.OutboundStageRed,
		},
		{
			name: "no fail but a pending check is running (wait, not go-fix)",
			pr:   forge.PR{Checks: running},
			want: forge.OutboundStageRunning,
		},
		{
			name: "empty rollup is not green and not failed: running (checks not registered yet)",
			pr:   forge.PR{Checks: nil},
			want: forge.OutboundStageRunning,
		},
		{
			// Fail closed: an entry the adapter left unset is a bug, and a bug
			// must draw the author's eye rather than read as a harmless wait.
			// This is the pre-seam behaviour — the old zero Check carried no
			// __typename and the old isFail defaulted it to failing.
			name: "a verdictless entry is red, not running — an unset verdict fails closed",
			pr:   forge.PR{Checks: []forge.Check{{}}},
			want: forge.OutboundStageRed,
		},
		{
			name: "a CheckState the folds do not recognise is red too",
			pr:   forge.PR{Checks: []forge.Check{{State: forge.CheckState("something_new")}}},
			want: forge.OutboundStageRed,
		},
		{
			name: "a verdictless entry beside a pass is still red",
			pr:   forge.PR{Checks: []forge.Check{{State: forge.CheckPass}, {}}},
			want: forge.OutboundStageRed,
		},
		{
			name: "green + changes-requested is changes-requested (the wait is on the author)",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewChangesRequested},
			want: forge.OutboundStageChangesRequested,
		},
		{
			name: "green + approved is ready (waiting only on the author / the merge)",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved},
			want: forge.OutboundStageReady,
		},
		{
			name: "green + no review decision is awaiting approval (no approval yet)",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewNone},
			want: forge.OutboundStageAwaitingApproval,
		},
		{
			name: "a conflicted Ready PR stays in Ready — mergeable is a merge precondition, not a stage boundary",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved, Mergeable: forge.MergeableConflicting},
			want: forge.OutboundStageReady,
		},
		{
			name: "unknown mergeability does not change the stage",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved, Mergeable: forge.MergeableUnknown},
			want: forge.OutboundStageReady,
		},
		{
			name: "unknown mergeability on an awaiting PR stays awaiting",
			pr:   forge.PR{Checks: green, ReviewDecision: forge.ReviewNone, Mergeable: forge.MergeableUnknown},
			want: forge.OutboundStageAwaitingApproval,
		},
		{
			name:       "green + approved + an unresolved thread is in discussion — the Discussion gate's stage (ADR 0019)",
			pr:         forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved},
			unresolved: 1,
			want:       forge.OutboundStageInDiscussion,
		},
		{
			name:       "green + approved + zero unresolved threads is ready — the gate is open",
			pr:         forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved},
			unresolved: 0,
			want:       forge.OutboundStageReady,
		},
		{
			name:       "green + unapproved + unresolved threads sits in awaiting approval — precedence names the primary blocker",
			pr:         forge.PR{Checks: green, ReviewDecision: forge.ReviewNone},
			unresolved: 2,
			want:       forge.OutboundStageAwaitingApproval,
		},
		{
			name:       "draft wins over unresolved threads",
			pr:         forge.PR{IsDraft: true, Checks: green, ReviewDecision: forge.ReviewApproved},
			unresolved: 3,
			want:       forge.OutboundStageDraft,
		},
		{
			name:       "red wins over unresolved threads",
			pr:         forge.PR{Checks: red, ReviewDecision: forge.ReviewApproved},
			unresolved: 3,
			want:       forge.OutboundStageRed,
		},
		{
			name:       "running wins over unresolved threads",
			pr:         forge.PR{Checks: running, ReviewDecision: forge.ReviewApproved},
			unresolved: 3,
			want:       forge.OutboundStageRunning,
		},
		{
			name:       "changes-requested wins over unresolved threads — the disarm stage, not the hold stage",
			pr:         forge.PR{Checks: green, ReviewDecision: forge.ReviewChangesRequested},
			unresolved: 3,
			want:       forge.OutboundStageChangesRequested,
		},
		{
			name:       "a conflicted in-discussion PR stays in discussion — mergeable never moves a stage",
			pr:         forge.PR{Checks: green, ReviewDecision: forge.ReviewApproved, Mergeable: forge.MergeableConflicting},
			unresolved: 1,
			want:       forge.OutboundStageInDiscussion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, forge.ClassifyOutboundStage(tt.pr, tt.unresolved))
		})
	}
}

// TestOutboundStagesIsTheClassifierCodomain closes the drift between the
// ordered stage list and the fold that produces stages — the price of an open
// set (ADR 0025). It drives ClassifyOutboundStage over its FULL finite input
// space (isDraft x rollup shape x review decision, unrecognised values
// included, x unresolved count), collects the results into a set, and asserts
// set-equality with OutboundStages() in both directions at once: nothing the
// fold returns is missing from the list, and no listed stage is unreachable.
func TestOutboundStagesIsTheClassifierCodomain(t *testing.T) {
	pass := forge.Check{State: forge.CheckPass}
	fail := forge.Check{State: forge.CheckFail}
	pending := forge.Check{State: forge.CheckPending}

	// Every rollup shape the fold discriminates on: green, failed, pending, the
	// mixed fail+pending case, the empty rollup, and a verdictless entry.
	rollups := [][]forge.Check{
		nil,
		{},
		{pass},
		{fail},
		{pending},
		{fail, pending},
		{pass, fail},
		{pass, pending},
		{{}},
	}
	// Both decided review values, the undecided one, plus one the fold does not
	// recognise — the default branch is reachable either way.
	decisions := []forge.ReviewDecision{
		forge.ReviewNone,
		forge.ReviewApproved,
		forge.ReviewChangesRequested,
		forge.ReviewDecision("something_new"),
	}
	// Zero, one, and many unresolved threads: the fold only tests > 0, so three
	// points cover the axis.
	unresolveds := []int{0, 1, 7}

	var collected []forge.OutboundStage
	seen := map[forge.OutboundStage]bool{}
	for _, isDraft := range []bool{false, true} {
		for _, checks := range rollups {
			for _, decision := range decisions {
				for _, unresolved := range unresolveds {
					pr := forge.PR{IsDraft: isDraft, Checks: checks, ReviewDecision: decision}
					stage := forge.ClassifyOutboundStage(pr, unresolved)
					if !seen[stage] {
						seen[stage] = true
						collected = append(collected, stage)
					}
				}
			}
		}
	}

	require.ElementsMatch(t, forge.OutboundStages(), collected,
		"OutboundStages() must be exactly the classifier's codomain — no unlisted stage, no unreachable one")
}

// TestOutboundStagesReturnsACopy pins the reason the stage set is exported as a
// function rather than a slice var: an importer that scribbles on the result
// cannot corrupt the declaration every other caller reads.
func TestOutboundStagesReturnsACopy(t *testing.T) {
	stages := forge.OutboundStages()
	require.NotEmpty(t, stages)
	stages[0] = forge.OutboundStage("vandalised")

	require.NotEqual(t, forge.OutboundStage("vandalised"), forge.OutboundStages()[0],
		"OutboundStages returns a copy, so a mutating caller is isolated")
}
