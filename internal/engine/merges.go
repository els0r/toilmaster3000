package engine

import "time"

// Merge is one merge-ledger record: the engine's internal read-model and the
// on-disk shape of one line in merges.jsonl — the exact outbound analog of
// Approval/approvals.jsonl (the json tags serve that disk format). It is NOT
// the wire shape; the /merges wire DTO is server.Merge, mapped via mergeToBody
// (ADR 0002). Written ONLY on a successful merge (see merge.go): a merged PR
// leaves the is:open pull immediately, so this ledger is the only durable
// signal the Merged station and the heartbeat's merged count can read.
type Merge struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	MergedAt time.Time `json:"merged_at"`
	// ApprovedBy is the deduped, normalized approver login list — the same
	// logins the commit's "Approved by:" trailer names (github.ApprovedBy).
	ApprovedBy []string `json:"approved_by"`
}

// Merges returns the merge ledger, newest-first (locked read). The wire layer
// applies the today-scope at the read boundary, like the approvals feed.
func (e *Engine) Merges() []Merge {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Merge, len(e.merges))
	copy(out, e.merges)
	return out
}

// loadMerges reads the existing merges.jsonl (if any) into the newest-first
// in-memory ledger, so the Merged station survives a restart. A missing file
// is the first-run case and is not an error. Runs at construction, before the
// Engine is shared.
func (e *Engine) loadMerges() error {
	ordered, err := readJSONL[Merge](e.mergesPath)
	if err != nil {
		return err
	}
	e.merges = newestFirst(ordered)
	return nil
}
