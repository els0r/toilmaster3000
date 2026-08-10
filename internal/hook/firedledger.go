package hook

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/els0r/toilmaster3000/internal/jsonl"
)

// FireKey identifies one Notifier fire's subject: which hook fired for which
// PR. Deliberately head-free (unlike VerdictKey): a Notifier fires once per PR
// EVER — across cycles, restarts, and fix-up pushes — so a new push must NOT
// mint a fresh key (ADR 0021). Keyed on the hook's stable ID, never its
// editable Name, so a rename never re-fires anything.
type FireKey struct {
	HookID string
	Number int
}

// FireRecord is one fire row: the in-memory read-model and the on-disk shape
// of one line in hookfires.jsonl (the json tags serve that disk format, the
// verdicts.jsonl idiom). The row is appended AT DISPATCH, before the side
// effect runs — at-most-once, the mirror image of the approvals ledger's
// act-first-append-on-success: for outward side effects a logged miss beats a
// double-post (ADR 0021). Point records which event fired the hook — context
// for a human reading the ledger; the key is (hook_id, number) alone.
type FireRecord struct {
	HookID string    `json:"hook_id"`
	Number int       `json:"number"`
	Point  Point     `json:"point"`
	At     time.Time `json:"at"`
}

// key projects a record onto its ledger key.
func (r FireRecord) key() FireKey {
	return FireKey{HookID: r.HookID, Number: r.Number}
}

// FiredLedger owns the persisted Notifier fires: append-only hookfires.jsonl
// on disk, the fired key set in memory (mutex-guarded maps, loaded at
// construction, appended under lock — the verdict-store idiom). It is the
// transition memory that makes "once per PR ever" hold against level-triggered
// events: the queue is rebuilt every cycle, so the same PR re-presents its
// queue_entered moment each pass and only the ledger says "already announced".
type FiredLedger struct {
	path string

	// openRead is the load-path seam, injectable so the close path is
	// assertable: a read that fails only at close cannot be provoked in a temp
	// directory. The append path needs no twin — internal/jsonl owns it and
	// carries its own seam.
	openRead func(name string) (io.ReadCloser, error)

	mu    sync.Mutex
	fired map[FireKey]bool
}

// NewFiredLedger constructs a FiredLedger backed by the given hookfires.jsonl
// path and loads it. A missing file is the first-run case: the empty ledger,
// nothing written until the first fire.
func NewFiredLedger(path string) (*FiredLedger, error) {
	l := &FiredLedger{
		path:     path,
		fired:    map[FireKey]bool{},
		openRead: openFiredLedgerRead,
	}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

func openFiredLedgerRead(name string) (io.ReadCloser, error) {
	return os.Open(name)
}

// Fired reports whether the (hookID, number) key has ever fired (locked read).
func (l *FiredLedger) Fired(hookID string, number int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fired[FireKey{HookID: hookID, Number: number}]
}

// Mark records one fire at dispatch: it appends the row to hookfires.jsonl and
// marks the key fired, returning (true, nil) for a fresh fire and (false, nil)
// when the key had already fired — nothing appended, the at-most-once no-op.
// The in-memory set is updated only after the write succeeds, so memory never
// claims a fire the disk lost: on a failed append the caller must NOT run the
// side effect (no row ⇒ it must look never-dispatched), and the next pass of
// the level-triggered event retries the whole fire.
func (l *FiredLedger) Mark(rec FireRecord) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := rec.key()
	if l.fired[key] {
		return false, nil
	}
	if err := l.appendLine(rec); err != nil {
		return false, fmt.Errorf("persist fire %s #%d: %w", rec.HookID, rec.Number, err)
	}
	l.fired[key] = true
	return true, nil
}

// load reads hookfires.jsonl into the fired set; a missing file is the empty
// ledger. Runs at construction, before the ledger is shared.
func (l *FiredLedger) load() (err error) {
	f, err := l.openRead(l.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read hookfires.jsonl: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close hookfires.jsonl: %w", closeErr))
		}
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec FireRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("parse hookfires.jsonl line: %w", err)
		}
		l.fired[rec.key()] = true
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read hookfires.jsonl: %w", err)
	}
	return nil
}

// appendLine writes one record as a JSON line through the shared .state append
// idiom (internal/jsonl), which checks the close: a row that fails at flush is
// a failed append. Callers hold l.mu.
func (l *FiredLedger) appendLine(rec FireRecord) error {
	return jsonl.Append(l.path, rec)
}
