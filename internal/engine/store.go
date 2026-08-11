package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/els0r/toilmaster3000/internal/jsonl"
)

// load reads the existing approvals.jsonl (if any) into the dedup set and the
// newest-first feed, so approvals survive a restart and are not re-approved. A
// missing file is the first-run case and is not an error.
func (e *Engine) load() error {
	ordered, err := readJSONL[Approval](e.statePath)
	if err != nil {
		return err
	}
	for _, rec := range ordered {
		e.dedup[rec.Number] = true
	}
	e.feed = newestFirst(ordered)
	return nil
}

// appendRecord appends one approval as a JSON line to approvals.jsonl,
// creating the .state directory and file if needed. Callers must hold e.mu.
func (e *Engine) appendRecord(rec Approval) error {
	return appendJSONL(e.statePath, rec)
}

// readJSONL reads an append-only JSONL ledger into its records in on-disk
// (oldest-first append) order. A missing file is the first-run case: no
// records, no error. Both ledgers (approvals.jsonl, merges.jsonl) load through
// here so the line handling never forks.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var ordered []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec T
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("parse %s line: %w", filepath.Base(path), err)
		}
		ordered = append(ordered, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ordered, nil
}

// newestFirst reverses an on-disk (oldest-first) record slice into the
// newest-first order the in-memory read-models keep.
func newestFirst[T any](ordered []T) []T {
	out := make([]T, 0, len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		out = append(out, ordered[i])
	}
	return out
}

// appendJSONL appends one record as a JSON line to an append-only ledger.
// Both ledgers append through here, and the idiom itself lives in
// internal/jsonl — shared with every other .state writer. Callers must hold
// e.mu.
func appendJSONL(path string, rec any) error {
	return jsonl.Append(path, rec)
}
