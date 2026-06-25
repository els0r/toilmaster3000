// Package settings owns the analytics assumption constants — the first non-rule
// persisted state in tm3k (ADR 0010). It loads .config/settings.yaml at startup,
// seeding the documented defaults on first run, and exposes a locked get + a
// full-replace the assumption chip persists through. The constants are display
// assumptions for the Analytics "Context switches saved" headline (time/money),
// never engine behaviour.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Settings are the analytics assumption constants. Their on-disk form is
// PascalCase YAML (.config/settings.yaml, matching rules.yaml); the wire form is
// a separate snake_case DTO owned at the HTTP boundary (ADR 0002 / 0010). The
// whole file IS this struct — three top-level keys, no wrapper.
//
//   - MinutesPerSwitch — minutes a single context switch costs (seeded 23, Gloria
//     Mark's measured refocus-after-interruption figure).
//   - HourlyRate — the user's own hourly rate; the money headline only lands if it
//     is their number, not a hard-coded guess (seeded 100).
//   - Currency — the symbol prefixed onto the money figure (seeded "$").
type Settings struct {
	MinutesPerSwitch int    `yaml:"MinutesPerSwitch"`
	HourlyRate       int    `yaml:"HourlyRate"`
	Currency         string `yaml:"Currency"`
}

// Defaults are the constants seeded on first run (ADR 0010): 23 minutes per
// switch, $100/hr.
func Defaults() Settings {
	return Settings{MinutesPerSwitch: 23, HourlyRate: 100, Currency: "$"}
}

// Store owns the assumption constants in memory under a mutex and is the
// analytics handler's source for the time/money figures. It loads
// .config/settings.yaml at startup; on first run (file absent) it seeds the
// defaults and writes them to disk. Replace is the full-replace the PUT /settings
// path (the assumption chip) drives.
type Store struct {
	path string

	mu      sync.Mutex
	current Settings
}

// NewStore constructs a Store backed by the given settings.yaml path and loads
// (or seeds) it. A missing file is the first-run case: the defaults are seeded
// and persisted. Any other read/parse/write failure is returned.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns the current settings (locked read). Settings is a value type, so
// the caller receives a copy it cannot use to mutate the Store.
func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Replace full-replaces the constants (the PUT /settings path, like rules' PUT)
// and rewrites settings.yaml. On a persist failure it rolls back the in-memory
// value so the store stays consistent with the (unchanged) file.
func (s *Store) Replace(n Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.current
	s.current = n
	if err := s.persist(); err != nil {
		s.current = prev
		return err
	}
	return nil
}

// load reads settings.yaml into the Store, or seeds and persists the defaults
// when the file does not exist. Runs at construction, before the Store is shared.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.current = Defaults()
			if err := s.persist(); err != nil {
				return fmt.Errorf("seed settings.yaml: %w", err)
			}
			return nil
		}
		return fmt.Errorf("read settings.yaml: %w", err)
	}

	var doc Settings
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse settings.yaml: %w", err)
	}
	s.current = doc
	return nil
}

// persist writes the current settings to settings.yaml, creating the .config
// directory if needed. Callers hold s.mu (or run at construction).
func (s *Store) persist() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(s.current)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return err
	}
	return nil
}
