package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// load reads the existing approvals.jsonl (if any) into the dedup set and the
// newest-first feed, so approvals survive a restart and are not re-approved. A
// missing file is the first-run case and is not an error.
func (e *Engine) load() error {
	f, err := os.Open(e.statePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	// File is oldest-first (append order); collect then reverse for newest-first.
	var ordered []Approval
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Approval
		if err := json.Unmarshal(line, &rec); err != nil {
			return fmt.Errorf("parse approvals.jsonl line: %w", err)
		}
		e.dedup[rec.Number] = true
		ordered = append(ordered, rec)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	e.feed = make([]Approval, 0, len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		e.feed = append(e.feed, ordered[i])
	}
	return nil
}

// appendRecord appends one approval as a JSON line to approvals.jsonl, creating
// the .state directory and file if needed. Callers must hold e.mu.
func (e *Engine) appendRecord(rec Approval) error {
	if dir := filepath.Dir(e.statePath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(e.statePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}
