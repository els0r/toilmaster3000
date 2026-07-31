package server

import (
	"time"

	"github.com/els0r/toilmaster3000/internal/engine"
)

// Merge is the wire shape of one merge-ledger entry (the Merged station's
// row). Snake_case mirrors the on-disk merges.jsonl record exactly, plus the
// derived TitleParts (computed parse-on-read in mergeToBody, ADR 0006 — never
// persisted). ApprovedBy carries the normalized approver logins the commit's
// "Approved by:" trailer named.
type Merge struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	TitleParts TitleParts `json:"title_parts"`
	URL        string     `json:"url"`
	MergedAt   time.Time  `json:"merged_at"`
	ApprovedBy []string   `json:"approved_by"`
}

// mergeToBody converts an engine Merge (whose json tags serve the
// merges.jsonl disk format) to its wire DTO. See ADR 0002 for why the engine
// type does not cross the boundary directly. A nil ApprovedBy renders [] not
// null, so the frontend always joins a real array.
func mergeToBody(m engine.Merge) Merge {
	approvedBy := m.ApprovedBy
	if approvedBy == nil {
		approvedBy = []string{}
	}
	return Merge{
		Number:     m.Number,
		Title:      m.Title,
		TitleParts: titleParts(m.Title),
		URL:        m.URL,
		MergedAt:   m.MergedAt,
		ApprovedBy: approvedBy,
	}
}

// todaysMerges filters the engine's newest-first ledger down to today's
// records (merged_at >= local midnight, workstation tz) — the same
// read-boundary scoping the approvals feed applies. Both the /merges body and
// /status's merged_count read through here, so the two can never disagree.
func todaysMerges(merges []engine.Merge) []engine.Merge {
	cutoff := startOfLocalDay(time.Now())
	out := make([]engine.Merge, 0, len(merges))
	for _, m := range merges {
		if m.MergedAt.Before(cutoff) {
			continue
		}
		out = append(out, m)
	}
	return out
}

type mergesOutput struct {
	Body []Merge
}
