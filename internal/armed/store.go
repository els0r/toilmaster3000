// Package armed owns the persisted armed set — the per-PR Armed/Withheld
// consent flag of the Outbound direction (CONTEXT "Outbound", ADR 0016) and
// tm3k's first mutable per-PR state. Armed is the ONLY thing that will ever
// authorize an outbound merge, so it is a durable human decision: loaded from
// .state/armed.json at startup and rewritten on every toggle or cleanup, it
// survives restarts AND new pushes to the PR (arm-while-red is the core use
// case). Every PR defaults to Withheld — a number absent from the set.
package armed

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Store owns the armed set in memory under a mutex, backed by armed.json (a
// sorted JSON array of PR numbers). A missing file is the first-run case: the
// empty set, nothing written until the first arm. The engine consults and
// reconciles it each cycle; the arm/disarm endpoints toggle it.
type Store struct {
	path string

	mu  sync.Mutex
	set map[int]bool
}

// NewStore constructs a Store backed by the given armed.json path and loads
// it. A missing file is the first-run case (empty set, no seed write); any
// other read/parse failure is returned.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, set: map[int]bool{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// IsArmed reports whether the PR number is armed (locked read). A number not
// in the set is Withheld — the default for every authored PR.
func (s *Store) IsArmed(number int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set[number]
}

// Set returns a copy of the armed set keyed by PR number (locked read), for
// the wire layer to zip armed flags onto outbound rows.
func (s *Store) Set() map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]bool, len(s.set))
	for n := range s.set {
		out[n] = true
	}
	return out
}

// Arm adds the PR number to the armed set and rewrites armed.json. Arming an
// already-armed number is an idempotent no-op (no rewrite). On a persist
// failure the in-memory set is rolled back so it stays consistent with the
// (unchanged) file.
func (s *Store) Arm(number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set[number] {
		return nil
	}
	s.set[number] = true
	if err := s.persist(); err != nil {
		delete(s.set, number)
		return fmt.Errorf("persist arm #%d: %w", number, err)
	}
	return nil
}

// Disarm removes the PR number from the armed set and rewrites armed.json.
// Disarming a number that is not armed is an idempotent no-op (no rewrite).
// On a persist failure the in-memory set is rolled back.
func (s *Store) Disarm(number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set[number] {
		return nil
	}
	delete(s.set, number)
	if err := s.persist(); err != nil {
		s.set[number] = true
		return fmt.Errorf("persist disarm #%d: %w", number, err)
	}
	return nil
}

// Retain removes every armed number NOT in keep — the engine's per-cycle
// reconciliation (cleanup of PRs that left the pull as merged/closed, plus the
// level-triggered changes-requested disarm) — in ONE rewrite. It returns the
// removed numbers, sorted. An empty removal set is a no-op (no rewrite); on a
// persist failure the in-memory set is rolled back.
func (s *Store) Retain(keep map[int]bool) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removed []int
	for n := range s.set {
		if !keep[n] {
			removed = append(removed, n)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	slices.Sort(removed)

	for _, n := range removed {
		delete(s.set, n)
	}
	if err := s.persist(); err != nil {
		for _, n := range removed {
			s.set[n] = true
		}
		return nil, fmt.Errorf("persist armed-set cleanup: %w", err)
	}
	return removed, nil
}

// load reads armed.json into the Store; a missing file is the empty set. Runs
// at construction, before the Store is shared.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read armed.json: %w", err)
	}
	var numbers []int
	if err := json.Unmarshal(data, &numbers); err != nil {
		return fmt.Errorf("parse armed.json: %w", err)
	}
	for _, n := range numbers {
		s.set[n] = true
	}
	return nil
}

// persist writes the armed set to armed.json as a sorted JSON array, creating
// the .state directory if needed. Callers hold s.mu.
func (s *Store) persist() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	numbers := make([]int, 0, len(s.set))
	for n := range s.set {
		numbers = append(numbers, n)
	}
	slices.Sort(numbers)
	data, err := json.Marshal(numbers)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
