package server

import (
	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
)

// OutboundItem is the wire shape of one authored PR itemized in an outbound
// stage list. Snake_case mirrors the engine's OutboundItem, plus the derived
// TitleParts (computed parse-on-read in outboundItemToBody, ADR 0006) and the
// derived Conflict flag. The raw title stays as source of truth.
type OutboundItem struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	TitleParts TitleParts `json:"title_parts"`
	Author     string     `json:"author"`
	URL        string     `json:"url"`
	// Additions, Deletions, and ChangedFiles are the PR's diff magnitude,
	// mirroring QueueItem's — from the same single authored list call.
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changed_files"`
	// Conflict marks a PR whose branch conflicts with its base (gh mergeable
	// CONFLICTING) — the Ready station's conflict marker. Derived on the wire
	// from the raw mergeable signal; mergeable is a merge precondition, not a
	// stage boundary, so a conflicted Ready row stays in Ready carrying this
	// flag. UNKNOWN (GitHub still computing) derives false, not a conflict.
	Conflict bool `json:"conflict"`
	// UnresolvedThreads is the PR's unresolved-review-thread count from the
	// cycle's batched threads call — the Discussion-gate predicate (ADR
	// 0019). It rides every row, whatever the stage (a draft with open nits
	// carries its real count); >=1 on an in_discussion row is what put it
	// there, and 0 on a ready row is what Ready means.
	UnresolvedThreads int `json:"unresolved_threads"`
	// Armed is the row's Armed/Withheld consent state (ADR 0016) — the badge
	// riding the row in whatever stage it is in, orthogonal to the partition.
	// Like PR State on the feed it is NOT snapshot data: the handler zips it in
	// from the engine's live armed set at response time, so a toggle reflects
	// on the very next fetch, not the next cycle. Default false — Withheld.
	Armed bool `json:"armed"`
}

// OutboundDistribution is the per-stage count set of the outbound partition —
// the Outgoing station's distribution bar. The counts are the stage-list
// lengths, so they sum to Outgoing by construction.
type OutboundDistribution struct {
	Draft            int `json:"draft"`
	Red              int `json:"red"`
	Running          int `json:"running"`
	ChangesRequested int `json:"changes_requested"`
	AwaitingApproval int `json:"awaiting_approval"`
	InDiscussion     int `json:"in_discussion"`
	Ready            int `json:"ready"`
}

// Outbound is the wire shape of the live outbound funnel snapshot served at
// /outbound: the seven itemized stage lists — in_discussion between
// awaiting_approval and ready (ADR 0019) — plus the distribution counts that
// partition Outgoing. Snake_case server-owned DTO (ADR 0002); the engine's
// Outbound never crosses the boundary directly. Like /pipeline it is derived
// live each cycle, never persisted — the zero snapshot (fresh restart, or a
// failed outbound or threads fetch) renders as empty lists + zero counts.
type Outbound struct {
	Outgoing         int                  `json:"outgoing"`
	Draft            []OutboundItem       `json:"draft"`
	Red              []OutboundItem       `json:"red"`
	Running          []OutboundItem       `json:"running"`
	ChangesRequested []OutboundItem       `json:"changes_requested"`
	AwaitingApproval []OutboundItem       `json:"awaiting_approval"`
	InDiscussion     []OutboundItem       `json:"in_discussion"`
	Ready            []OutboundItem       `json:"ready"`
	Distribution     OutboundDistribution `json:"distribution"`
	// Search is the fixed derived authored search (github.AuthoredSearch),
	// surfaced so the Outgoing station can show WHICH search produced this set
	// as a code chip — parallel to Pipeline.Search. It is seam config, not
	// snapshot data, so the handler sets it on the body directly.
	Search string `json:"search"`
}

// outboundItemToBody converts an engine OutboundItem to its wire DTO,
// computing the TitleParts parse-on-read (ADR 0006), deriving the Conflict
// flag from the raw mergeable signal, and zipping in the row's live armed
// state. See ADR 0002 for why the engine type does not cross the boundary
// directly.
func outboundItemToBody(it engine.OutboundItem, armed bool) OutboundItem {
	return OutboundItem{
		Number:            it.Number,
		Title:             it.Title,
		TitleParts:        titleParts(it.Title),
		Author:            it.Author,
		URL:               it.URL,
		Additions:         it.Additions,
		Deletions:         it.Deletions,
		ChangedFiles:      it.ChangedFiles,
		Conflict:          it.Mergeable == github.MergeableConflicting,
		UnresolvedThreads: it.UnresolvedThreads,
		Armed:             armed,
	}
}

// outboundItemsToBody maps a stage list to wire DTOs, returning a non-nil
// empty slice for an empty list so the JSON renders [] not null.
func outboundItemsToBody(items []engine.OutboundItem, armed map[int]bool) []OutboundItem {
	out := make([]OutboundItem, 0, len(items))
	for _, it := range items {
		out = append(out, outboundItemToBody(it, armed[it.Number]))
	}
	return out
}

// outboundToBody converts the engine's live outbound snapshot to its wire DTO
// (ADR 0002). The seven lists are itemized with parse-on-read title parts; the
// distribution counts are the list lengths, so they sum to Outgoing by
// construction. armed is the engine's LIVE armed set, zipped onto each row
// (the PR-State precedent) so a fresh toggle shows without waiting a cycle.
func outboundToBody(ob engine.Outbound, armed map[int]bool) Outbound {
	return Outbound{
		Outgoing:         ob.Outgoing,
		Draft:            outboundItemsToBody(ob.Draft, armed),
		Red:              outboundItemsToBody(ob.Red, armed),
		Running:          outboundItemsToBody(ob.Running, armed),
		ChangesRequested: outboundItemsToBody(ob.ChangesRequested, armed),
		AwaitingApproval: outboundItemsToBody(ob.AwaitingApproval, armed),
		InDiscussion:     outboundItemsToBody(ob.InDiscussion, armed),
		Ready:            outboundItemsToBody(ob.Ready, armed),
		Distribution: OutboundDistribution{
			Draft:            len(ob.Draft),
			Red:              len(ob.Red),
			Running:          len(ob.Running),
			ChangesRequested: len(ob.ChangesRequested),
			AwaitingApproval: len(ob.AwaitingApproval),
			InDiscussion:     len(ob.InDiscussion),
			Ready:            len(ob.Ready),
		},
	}
}

type outboundOutput struct {
	Body Outbound
}

// armOutput is the success body of an arm/disarm toggle: an ok marker, the PR
// number, and the resulting consent state (true after POST, false after
// DELETE) so the client can update the toggle without a second fetch.
type armOutput struct {
	Body struct {
		OK     bool `json:"ok"`
		Number int  `json:"number"`
		Armed  bool `json:"armed"`
	}
}
