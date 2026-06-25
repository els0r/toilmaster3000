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

// TestRangeStart covers the slice-2 range-boundary math: each of the four
// selectable ranges resolves to its inclusive start instant in now's location.
// The cases pin the correctness-critical edges the plan calls out — ISO-8601
// Monday week start (incl. when now IS Monday, when now is Sunday, and when the
// week straddles a month boundary), calendar-1st month start (incl. on the 1st),
// and the rolling X×24h window (incl. crossing a month boundary and days=1).
func TestRangeStart(t *testing.T) {
	// All times are built in UTC; rangeStart computes boundaries in now's own
	// location, so UTC keeps the expectations free of DST ambiguity. 2026-06-25 is
	// a Thursday (the seeded "today"), so its ISO week runs Mon 2026-06-22 → Sun
	// 2026-06-28.
	at := func(y int, m time.Month, d, hh, mm int) time.Time {
		return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
	}
	tests := []struct {
		name string
		rng  string
		days int
		now  time.Time
		want time.Time
	}{
		{"today is local midnight of now's day", "today", 0, at(2026, 6, 25, 14, 30), at(2026, 6, 25, 0, 0)},
		{"week starts Monday 00:00 (now mid-week Thu)", "week", 0, at(2026, 6, 25, 14, 30), at(2026, 6, 22, 0, 0)},
		{"week start when now IS Monday is the same day's midnight", "week", 0, at(2026, 6, 22, 9, 0), at(2026, 6, 22, 0, 0)},
		{"week start when now is Sunday is six days back", "week", 0, at(2026, 6, 28, 23, 0), at(2026, 6, 22, 0, 0)},
		{"week start straddling a month boundary", "week", 0, at(2026, 7, 1, 10, 0), at(2026, 6, 29, 0, 0)},
		{"month is the calendar 1st 00:00", "month", 0, at(2026, 6, 25, 14, 30), at(2026, 6, 1, 0, 0)},
		{"month start when now IS the 1st is that day's midnight", "month", 0, at(2026, 6, 1, 8, 0), at(2026, 6, 1, 0, 0)},
		{"days is a rolling X×24h window ending now", "days", 7, at(2026, 6, 25, 14, 30), at(2026, 6, 18, 14, 30)},
		{"days window crosses a month boundary", "days", 5, at(2026, 7, 2, 10, 0), at(2026, 6, 27, 10, 0)},
		{"days=1 is exactly 24h back, preserving the clock time", "days", 1, at(2026, 6, 25, 14, 30), at(2026, 6, 24, 14, 30)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, rangeStart(tc.rng, tc.days, tc.now).Equal(tc.want),
				"rangeStart(%q, %d, %s) = %s, want %s", tc.rng, tc.days, tc.now, rangeStart(tc.rng, tc.days, tc.now), tc.want)
		})
	}
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
