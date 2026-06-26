package server

import (
	"github.com/els0r/toilmaster3000/internal/engine"
)

// FunnelItem is the wire shape of one PR itemized in a terminal funnel bucket
// (dropped_red, dropped_draft, staging, approved_elsewhere). Snake_case mirrors
// the engine's FunnelItem, plus the derived TitleParts (computed parse-on-read
// in funnelItemToBody, ADR 0006 — mirroring queueItemToBody). FailingChecks is
// the "N checks failing" signal, meaningful on dropped_red (0 on every other
// bucket). The raw title stays as source of truth.
type FunnelItem struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	TitleParts TitleParts `json:"title_parts"`
	Author     string     `json:"author"`
	URL        string     `json:"url"`
	// FailingChecks is the count of non-passing checks on a dropped_red row (its
	// "N checks failing" station signal); 0 on every other bucket.
	FailingChecks int `json:"failing_checks"`
}

// Pipeline is the wire shape of the live Cycle Funnel snapshot served at
// /pipeline: the FOUR terminal item lists plus the distribution counts that
// partition Incoming and approved_this_cycle. Snake_case server-owned DTO (ADR
// 0002); the engine's Funnel never crosses the boundary directly.
//
// Stations 4/5 (Needs-Human-Review queue, Approval Feed) reuse /queue and
// /approvals, so they are count-only here (needs_human_review) — the funnel does
// not re-itemize them. The raw Incoming PR set is not retained anywhere; Incoming
// is a count rendered as the distribution bar.
type Pipeline struct {
	Incoming          int          `json:"incoming"`
	DroppedRed        []FunnelItem `json:"dropped_red"`
	DroppedDraft      []FunnelItem `json:"dropped_draft"`
	Staging           []FunnelItem `json:"staging"`
	ApprovedElsewhere []FunnelItem `json:"approved_elsewhere"`
	// NeedsHumanReview and ApprovedByTm3k are count-only partition segments: the
	// queue is itemized via /queue, and Approved-by-tm3k (the STANDING dedup-member
	// segment, any day) is never itemized (it is done — in the ledger if today,
	// history otherwise). ApprovedThisCycle is the narrower this-cycle pulse,
	// distinct from the standing ApprovedByTm3k count (the cadence-seam doctrine).
	NeedsHumanReview  int `json:"needs_human_review"`
	ApprovedByTm3k    int `json:"approved_by_tm3k"`
	ApprovedThisCycle int `json:"approved_this_cycle"`
}

// funnelItemToBody converts an engine FunnelItem to its wire DTO, computing the
// TitleParts parse-on-read (ADR 0006), mirroring queueItemToBody. See ADR 0002
// for why the engine type does not cross the boundary directly.
func funnelItemToBody(it engine.FunnelItem) FunnelItem {
	return FunnelItem{
		Number:        it.Number,
		Title:         it.Title,
		TitleParts:    titleParts(it.Title),
		Author:        it.Author,
		URL:           it.URL,
		FailingChecks: it.FailingChecks,
	}
}

// funnelItemsToBody maps a list of engine FunnelItems to wire DTOs, returning a
// non-nil empty slice for an empty list so the JSON renders [] not null.
func funnelItemsToBody(items []engine.FunnelItem) []FunnelItem {
	out := make([]FunnelItem, 0, len(items))
	for _, it := range items {
		out = append(out, funnelItemToBody(it))
	}
	return out
}

// pipelineToBody converts the engine's live Funnel snapshot to its wire DTO
// (ADR 0002). The four lists are itemized with parse-on-read title parts; the
// remaining segments are counts.
func pipelineToBody(f engine.Funnel) Pipeline {
	return Pipeline{
		Incoming:          f.Incoming,
		DroppedRed:        funnelItemsToBody(f.DroppedRed),
		DroppedDraft:      funnelItemsToBody(f.DroppedDraft),
		Staging:           funnelItemsToBody(f.Staging),
		ApprovedElsewhere: funnelItemsToBody(f.ApprovedElsewhere),
		NeedsHumanReview:  f.NeedsHumanReview,
		ApprovedByTm3k:    f.ApprovedByTm3k,
		ApprovedThisCycle: f.ApprovedThisCycle,
	}
}

type pipelineOutput struct {
	Body Pipeline
}
