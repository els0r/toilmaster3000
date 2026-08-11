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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(line)
	return err
}

// marshalLine encodes one record as its on-disk line, newline included.
func marshalLine(rec any) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(rec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
