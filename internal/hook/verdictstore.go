package hook

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/els0r/toilmaster3000/internal/jsonl"
)

// VerdictKey identifies one screen run's subject: which Screen judged which
// PR at which head. Per-head keying is the whole invalidation story (ADR
// 0022): a new push changes the key, so the store misses and the PR
// re-screens; an unchanged head keeps hitting its stored verdict — across
// cycles and restarts alike.
type VerdictKey struct {
	ScreenID string
	Number   int
	Head     string
}

// VerdictRecord is one screen-run row: the engine's read-model and the
// on-disk shape of one line in verdicts.jsonl (the json tags serve that disk
// format, the approvals.jsonl idiom). Outcome is proceed|hold|error — error
// is a recorded failed attempt, never a Screen's verdict (ADR 0022).
//
// The key is spelled `hook_id` on disk, as it is in hookfires.jsonl and
// transcripts.jsonl: a Screen is a hook, and this is its stable hook Id
// (ADR 0023). It was `screen_id` until ADR 0028, which made the three files
// joinable by hand — a reader keying on `.hook_id` silently dropped every
// verdict row. The Go field keeps the ScreenID name because a verdict is a
// Screen's alone; only the disk spelling is shared.
type VerdictRecord struct {
	ScreenID string    `json:"hook_id"`
	Number   int       `json:"number"`
	Head     string    `json:"head"`
	Outcome  Outcome   `json:"outcome"`
	Reason   string    `json:"reason"`
	At       time.Time `json:"at"`
}

// UnmarshalJSON decodes one verdicts.jsonl row, accepting the legacy
// `screen_id` spelling of the key alongside today's `hook_id`. An operator's
// existing file predates the rename and must keep loading — the 3-strikes
// count and every stored verdict live in it — so the old name is read forever
// and written never. Rewriting the file at boot was rejected: an append-only
// ledger that mutates itself on upgrade is a worse trade than one dead field
// name.
func (r *VerdictRecord) UnmarshalJSON(data []byte) error {
	type row VerdictRecord // sheds this method, so no recursion
	aux := struct {
		*row
		LegacyScreenID string `json:"screen_id"`
	}{row: (*row)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if r.ScreenID == "" {
		r.ScreenID = aux.LegacyScreenID
	}
	return nil
}

// key projects a record onto its store key.
func (r VerdictRecord) key() VerdictKey {
	return VerdictKey{ScreenID: r.ScreenID, Number: r.Number, Head: r.Head}
}

// VerdictStore owns the persisted screen verdicts: append-only
// verdicts.jsonl on disk; in memory the latest row per key (latest wins —
// the level-triggered read the cycle consults, ADR 0022) plus each key's
// trailing error streak (the failed-attempt count the 3-strikes fold
// consumes). Mirrors the armed store: mutex-guarded maps, loaded at
// construction, appended under lock.
type VerdictStore struct {
	path string

	mu     sync.Mutex
	latest map[VerdictKey]VerdictRecord
	// errorStreak counts the consecutive TRAILING error rows per key — how
	// many failed attempts the key's history ends with. A non-error row resets
	// its key to zero: a real verdict ends the failure streak. Rebuilt from
	// disk order at load, so the 3-strikes cap survives a restart (ADR 0022).
	errorStreak map[VerdictKey]int
}

// NewVerdictStore constructs a VerdictStore backed by the given
// verdicts.jsonl path and loads it, folding the rows latest-wins. A missing
// file is the first-run case: the empty store, nothing written until the
// first verdict.
func NewVerdictStore(path string) (*VerdictStore, error) {
	s := &VerdictStore{path: path, latest: map[VerdictKey]VerdictRecord{}, errorStreak: map[VerdictKey]int{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Latest returns the latest row for the (screenID, number, head) key (locked
// read). A missing key means no run has completed for this head — never
// proceed (never-fire-on-no-signal, ADR 0021).
func (s *VerdictStore) Latest(screenID string, number int, head string) (VerdictRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.latest[VerdictKey{ScreenID: screenID, Number: number, Head: head}]
	return rec, ok
}

// ErrorStreak returns how many consecutive error rows the key's history ends
// with — the failed-attempt count since the last real verdict; 0 when the
// latest row is a verdict or the key is unknown (locked read). The 3-strikes
// fold reads it to bound the no-verdict path (ADR 0022 decision 5).
func (s *VerdictStore) ErrorStreak(screenID string, number int, head string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorStreak[VerdictKey{ScreenID: screenID, Number: number, Head: head}]
}

// Append appends one row to verdicts.jsonl and makes it the key's latest,
// creating the .state directory if needed. The in-memory map is updated only
// after the write succeeds, so memory never claims a verdict the disk lost
// (the next cycle re-dispatches on the miss).
func (s *VerdictStore) Append(rec VerdictRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendLine(rec); err != nil {
		return fmt.Errorf("persist verdict %s #%d@%s: %w", rec.ScreenID, rec.Number, rec.Head, err)
	}
	s.apply(rec)
	return nil
}

// apply folds one row into the in-memory views: the latest-wins map and the
// per-key trailing error streak (an error row extends its key's streak; a real
// verdict resets it). Callers hold s.mu, or run pre-share at load.
func (s *VerdictStore) apply(rec VerdictRecord) {
	key := rec.key()
	s.latest[key] = rec
	if rec.Outcome == Errored {
		s.errorStreak[key]++
	} else {
		delete(s.errorStreak, key)
	}
}

// load reads verdicts.jsonl into the latest-wins map; a missing file is the
// empty store. Runs at construction, before the store is shared.
func (s *VerdictStore) load() (err error) {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read verdicts.jsonl: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close verdicts.jsonl: %w", closeErr))
		}
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec VerdictRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("parse verdicts.jsonl line: %w", err)
		}
		// File order is append order, so replaying realizes both latest-wins
		// and the trailing error streak.
		s.apply(rec)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read verdicts.jsonl: %w", err)
	}
	return nil
}

// appendLine writes one record as a JSON line through the shared .state append
// idiom (internal/jsonl), which checks the close: a row that fails at flush is
// a failed append. Callers hold s.mu.
func (s *VerdictStore) appendLine(rec VerdictRecord) error {
	return jsonl.Append(s.path, rec)
}
