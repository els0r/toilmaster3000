package server

import (
	"strings"

	"github.com/els0r/toilmaster3000/internal/engine"
)

// Analytics is the wire shape of the Analytics tab's look-back dashboard for a
// range: the auto-vs-human headline split and the context-switches-saved
// headline. Snake_case on the wire per the project's single-convention rule. It
// is computed exclusively from approvals.jsonl (ADR 0009) — no new persistence.
// Slice 1 carries the stats row only; ranges, deltas, the by-type cohort, and
// the scope filter join it in later slices.
type Analytics struct {
	// AutoApproved and HumanReview partition the range's recorded approvals by the
	// matched_rule prefix (ADR 0009): a "human approval: " prefix is Human Review, a
	// human stepping in via the queue; everything else is Auto-approved. The two
	// cover every approval, so their shares sum to 1.
	AutoApproved Stat `json:"auto_approved"`
	HumanReview  Stat `json:"human_review"`
	// SwitchesSaved is the context-switches-saved headline: the raw count of
	// interruptions the robot spared the human, which equals the Auto-approved count
	// (a Human Review approval is a switch the human DID take, so it is not saved).
	// Slice 4 adds the derived time/money figures from the settings constants.
	SwitchesSaved int `json:"switches_saved"`
}

// Stat is one headline stat: a count and its share of the range total, a 0..1
// fraction (the frontend formats it as a percentage). An empty range yields a 0
// share with no divide-by-zero.
type Stat struct {
	Count int     `json:"count"`
	Share float64 `json:"share"`
}

// aggregateAnalytics computes the slice-1 stats row from the range's approvals:
// it partitions them into Auto-approved vs Human Review by the matched_rule
// prefix (ADR 0009), derives each side's share of the total, and reports
// switches-saved as the auto count. The caller scopes the slice to the range
// (today, for slice 1) before calling; this function is the pure, table-tested
// partition.
func aggregateAnalytics(approvals []engine.Approval) Analytics {
	var autoCount, humanCount int
	for _, a := range approvals {
		if strings.HasPrefix(a.MatchedRule, engine.ManualApprovalPrefix) {
			humanCount++
		} else {
			autoCount++
		}
	}
	total := autoCount + humanCount
	return Analytics{
		AutoApproved:  Stat{Count: autoCount, Share: share(autoCount, total)},
		HumanReview:   Stat{Count: humanCount, Share: share(humanCount, total)},
		SwitchesSaved: autoCount,
	}
}

// share returns n's fraction of total as a 0..1 value, guarding the empty range
// (total 0) against a divide-by-zero by reporting 0.
func share(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}
