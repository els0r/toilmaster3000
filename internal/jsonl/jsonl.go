// Package jsonl owns the append-only JSONL idiom every .state writer in tm3k
// shares: approvals.jsonl and merges.jsonl (the engine's ledgers),
// verdicts.jsonl, hookfires.jsonl, and transcripts.jsonl. Each of those grew
// its own byte-identical copy of the same twelve lines — create the parent,
// open O_APPEND|O_CREATE, marshal, write the line — and four copies of a
// routine must be kept in step by hand: the day the idiom changes, three sites
// get the fix and the fourth silently keeps the old behaviour.
//
// Only the APPEND half lives here. Reading is deliberately not shared: each
// caller folds its file into a different read-model, and transcripts.jsonl is
// never read at all — a shared reader would hand it a line-length limit that
// ADR 0028 explicitly forbids inheriting.
package jsonl

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// Append writes one record as a JSON line, creating the parent directory and
// the file if needed — .state/ is git-ignored, so a fresh checkout has neither.
//
// The record is marshalled BEFORE anything is opened, so a record that cannot
// be encoded never creates a file or a truncated line. The whole line, newline
// included, goes out in one Write: callers that hold a lock get atomicity from
// the lock, and the single write is what keeps a partial line from reaching
// disk ahead of an error.
//
// Close is checked, not deferred. On a quota'd or network-backed filesystem a
// write can succeed into the page cache and fail at flush, and every caller
// here acts on a failed append — the verdict store refuses to update memory,
// the transcript sink logs a miss. Discarding the close error would hand all
// of them a success for a row that never reached disk. The write error wins
// when both fail: it names the cause, and the close error is its echo.
func Append(path string, rec any) error {
	line, err := marshalLine(rec)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := openAppend(path)
	if err != nil {
		return err
	}

	_, writeErr := f.Write(line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// openAppend opens the ledger for appending. It is a package var so the close
// path is assertable: a write that fails only at flush is real on a quota'd or
// network-backed filesystem and cannot be provoked in a temp directory.
var openAppend = func(path string) (io.WriteCloser, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// marshalLine encodes one record as its on-disk line, newline included.
//
// HTML escaping is OFF. encoding/json escapes <, > and & by default — a
// browser-safety default for JSON embedded in a page, which no .state file
// ever is. Left on, it mangles the prose these files are read for: a
// transcript quoting `if a < b && c > d` lands as < and &, so a
// `grep` over transcripts.jsonl misses text that is demonstrably in the file
// and `less` shows escapes where the operator expected code. Verdict reasons
// and PR titles carry the same characters. Both spellings decode identically,
// so old rows keep reading and new rows read to a human too.
func marshalLine(rec any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
