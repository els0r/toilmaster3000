package github_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// TestClassifyOutboundStage pins the outbound funnel partition: every authored
// PR folds into EXACTLY one stage, with the documented precedence
// draft > not-green (red | running) > changes-requested > awaiting-approval >
// in-discussion > ready (CONTEXT "Outbound", ADR 0016/0019). mergeable is a
// merge precondition, not a stage boundary — it never moves a PR between
// stages. The unresolved-thread count is the fold's second input (ADR 0019):
// it splits Ready from In Discussion and is inert below awaiting-approval —
// precedence names the primary blocker.
func TestClassifyOutboundStage(t *testing.T) {
	checkRun := func(status, conclusion string) github.Check {
		return github.Check{Typename: "CheckRun", Status: status, Conclusion: conclusion}
	}
	statusContext := func(state string) github.Check {
		return github.Check{Typename: "StatusContext", State: state}
	}
	green := []github.Check{checkRun("COMPLETED", "SUCCESS")}
	red := []github.Check{checkRun("COMPLETED", "FAILURE")}
	running := []github.Check{checkRun("IN_PROGRESS", "")}

	tests := []struct {
		name       string
		pr         github.PR
		unresolved int
		want       github.OutboundStage
	}{
		{
			name: "draft wins over everything (draft + red + changes-requested)",
			pr:   github.PR{IsDraft: true, Checks: red, ReviewDecision: "CHANGES_REQUESTED"},
			want: github.OutboundStageDraft,
		},
		{
			name: "not-green wins over changes-requested (red + changes-requested is red)",
			pr:   github.PR{Checks: red, ReviewDecision: "CHANGES_REQUESTED"},
			want: github.OutboundStageRed,
		},
		{
			name: "one failed check makes the PR red even while others still run",
			pr:   github.PR{Checks: []github.Check{checkRun("COMPLETED", "FAILURE"), checkRun("IN_PROGRESS", "")}},
			want: github.OutboundStageRed,
		},
		{
			name: "no fail but a pending check is running (wait, not go-fix)",
			pr:   github.PR{Checks: running},
			want: github.OutboundStageRunning,
		},
		{
			name: "a pending legacy StatusContext is running",
			pr:   github.PR{Checks: []github.Check{statusContext("PENDING")}},
			want: github.OutboundStageRunning,
		},
		{
			name: "an ERROR StatusContext is red",
			pr:   github.PR{Checks: []github.Check{statusContext("ERROR")}},
			want: github.OutboundStageRed,
		},
		{
			name: "empty rollup is not green and not failed: running (checks not registered yet)",
			pr:   github.PR{Checks: nil},
			want: github.OutboundStageRunning,
		},
		{
			name: "green + CHANGES_REQUESTED is changes-requested (the wait is on the author)",
			pr:   github.PR{Checks: green, ReviewDecision: "CHANGES_REQUESTED"},
			want: github.OutboundStageChangesRequested,
		},
		{
			name: "green + APPROVED is ready (waiting only on the author / the merge)",
			pr:   github.PR{Checks: green, ReviewDecision: "APPROVED"},
			want: github.OutboundStageReady,
		},
		{
			name: "green + REVIEW_REQUIRED is awaiting approval",
			pr:   github.PR{Checks: green, ReviewDecision: "REVIEW_REQUIRED"},
			want: github.OutboundStageAwaitingApproval,
		},
		{
			name: "green + empty reviewDecision is awaiting approval (no approval yet)",
			pr:   github.PR{Checks: green, ReviewDecision: ""},
			want: github.OutboundStageAwaitingApproval,
		},
		{
			name: "a conflicted Ready PR stays in Ready — mergeable is a merge precondition, not a stage boundary",
			pr:   github.PR{Checks: green, ReviewDecision: "APPROVED", Mergeable: "CONFLICTING"},
			want: github.OutboundStageReady,
		},
		{
			name: "UNKNOWN mergeable does not change the stage",
			pr:   github.PR{Checks: green, ReviewDecision: "APPROVED", Mergeable: "UNKNOWN"},
			want: github.OutboundStageReady,
		},
		{
			name: "UNKNOWN mergeable on an awaiting PR stays awaiting",
			pr:   github.PR{Checks: green, ReviewDecision: "REVIEW_REQUIRED", Mergeable: "UNKNOWN"},
			want: github.OutboundStageAwaitingApproval,
		},
		{
			name:       "green + APPROVED + an unresolved thread is in discussion — the Discussion gate's stage (ADR 0019)",
			pr:         github.PR{Checks: green, ReviewDecision: "APPROVED"},
			unresolved: 1,
			want:       github.OutboundStageInDiscussion,
		},
		{
			name:       "green + APPROVED + zero unresolved threads is ready — the gate is open",
			pr:         github.PR{Checks: green, ReviewDecision: "APPROVED"},
			unresolved: 0,
			want:       github.OutboundStageReady,
		},
		{
			name:       "green + unapproved + unresolved threads sits in awaiting approval — precedence names the primary blocker",
			pr:         github.PR{Checks: green, ReviewDecision: "REVIEW_REQUIRED"},
			unresolved: 2,
			want:       github.OutboundStageAwaitingApproval,
		},
		{
			name:       "draft wins over unresolved threads",
			pr:         github.PR{IsDraft: true, Checks: green, ReviewDecision: "APPROVED"},
			unresolved: 3,
			want:       github.OutboundStageDraft,
		},
		{
			name:       "red wins over unresolved threads",
			pr:         github.PR{Checks: red, ReviewDecision: "APPROVED"},
			unresolved: 3,
			want:       github.OutboundStageRed,
		},
		{
			name:       "running wins over unresolved threads",
			pr:         github.PR{Checks: running, ReviewDecision: "APPROVED"},
			unresolved: 3,
			want:       github.OutboundStageRunning,
		},
		{
			name:       "changes-requested wins over unresolved threads — the disarm stage, not the hold stage",
			pr:         github.PR{Checks: green, ReviewDecision: "CHANGES_REQUESTED"},
			unresolved: 3,
			want:       github.OutboundStageChangesRequested,
		},
		{
			name:       "a conflicted in-discussion PR stays in discussion — mergeable never moves a stage",
			pr:         github.PR{Checks: green, ReviewDecision: "APPROVED", Mergeable: "CONFLICTING"},
			unresolved: 1,
			want:       github.OutboundStageInDiscussion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, github.ClassifyOutboundStage(tt.pr, tt.unresolved))
		})
	}
}
