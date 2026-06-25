package server

import (
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/stretchr/testify/require"
)

// auto builds an auto-approved record: its matched_rule is a rule name (no
// human-approval prefix), so the analytics partition counts it as Auto-approved.
func auto(number int) engine.Approval {
	return engine.Approval{Number: number, MatchedRule: "team chores", ApprovedAt: time.Now()}
}

// human builds a human-review record: its matched_rule carries the
// ManualApprovalPrefix, so the partition counts it as Human Review (ADR 0009).
func human(number int) engine.Approval {
	return engine.Approval{Number: number, MatchedRule: engine.ManualApprovalPrefix + "breaking_change", ApprovedAt: time.Now()}
}

// TestAggregateAnalytics covers the slice-1 aggregation: the auto-vs-human
// partition by the matched_rule prefix (ADR 0009), each side's share of the
// range total, and switches-saved = the auto count. The empty range yields all
// zeros with no divide-by-zero, and complementary shares sum to exactly 1.
func TestAggregateAnalytics(t *testing.T) {
	tests := []struct {
		name      string
		approvals []engine.Approval
		want      Analytics
	}{
		{
			name:      "empty range is all zeros, no divide-by-zero",
			approvals: nil,
			want: Analytics{
				AutoApproved:  Stat{Count: 0, Share: 0},
				HumanReview:   Stat{Count: 0, Share: 0},
				SwitchesSaved: 0,
			},
		},
		{
			name:      "all auto-approved: full share, switches saved = count",
			approvals: []engine.Approval{auto(1), auto(2), auto(3)},
			want: Analytics{
				AutoApproved:  Stat{Count: 3, Share: 1},
				HumanReview:   Stat{Count: 0, Share: 0},
				SwitchesSaved: 3,
			},
		},
		{
			name:      "mixed: shares sum to 1, switches saved = auto count only",
			approvals: []engine.Approval{auto(1), auto(2), auto(3), human(4)},
			want: Analytics{
				AutoApproved:  Stat{Count: 3, Share: 0.75},
				HumanReview:   Stat{Count: 1, Share: 0.25},
				SwitchesSaved: 3,
			},
		},
		{
			name:      "all human-review: a manual approval is a switch the human took, so none saved",
			approvals: []engine.Approval{human(1), human(2)},
			want: Analytics{
				AutoApproved:  Stat{Count: 0, Share: 0},
				HumanReview:   Stat{Count: 2, Share: 1},
				SwitchesSaved: 0,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateAnalytics(tc.approvals)
			require.Equal(t, tc.want, got)
			// The two sides partition the range total, so their shares always sum to
			// exactly 1 — except an empty range, where both are 0.
			if len(tc.approvals) > 0 {
				require.InDelta(t, 1.0, got.AutoApproved.Share+got.HumanReview.Share, 1e-9)
			}
		})
	}
}
