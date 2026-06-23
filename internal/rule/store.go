package rule

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Store owns the rule set in memory under a mutex and is the engine's source of
// matching rules. It loads .config/rules.yaml at startup; on first run (file
// absent) it seeds the two default rules — reproducing the legacy scripts'
// behaviour — and writes them to disk with stable generated IDs.
//
// Slice 5 extends this Store with Create/Update/Delete + disk rewrite +
// validation behind the HTTP CRUD surface; this slice only loads, seeds, and
// lists. The mutex and on-disk file already in place are what those mutations
// will build on.
type Store struct {
	path string

	mu    sync.Mutex
	rules []Rule
}

// rulesFile is the on-disk YAML document shape: a top-level Rules list of
// PascalCase rule entries.
type rulesFile struct {
	Rules []Rule `yaml:"Rules"`
}

// NewStore constructs a Store backed by the given rules.yaml path and loads (or
// seeds) it. A missing file is the first-run case: the two default rules are
// seeded and persisted. Any other read/parse/write failure is returned.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// ErrRuleNotFound is returned by Update and Delete when no rule has the given
// id. The HTTP layer maps it to a 404/4xx.
var ErrRuleNotFound = errors.New("rule not found")

// List returns the rules in file order (locked read). The slice is a copy so
// callers cannot mutate the Store's backing array.
func (s *Store) List() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Create validates and appends a new rule, generating its stable ID (any id on
// the incoming rule is ignored). On success it rewrites rules.yaml and returns
// the stored rule (with its generated id). A semantic-validation failure
// (ErrEmptyRule / ErrInvalidRegex) is returned without mutating the store.
func (s *Store) Create(r Rule) (Rule, error) {
	if err := Validate(r); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	r.ID = newID()
	s.rules = append(s.rules, r)
	if err := s.persist(); err != nil {
		// Roll back the in-memory append so a failed write leaves the store
		// consistent with the (unchanged) file.
		s.rules = s.rules[:len(s.rules)-1]
		return Rule{}, err
	}
	return r, nil
}

// Update full-replaces the rule with the given id (this is also how
// enable/disable works: a PUT with Enabled flipped). The id is preserved from
// the path, not the body. On success it rewrites rules.yaml and returns the
// stored rule. An unknown id returns ErrRuleNotFound; a validation failure is
// returned without mutating the store.
func (s *Store) Update(id string, r Rule) (Rule, error) {
	if err := Validate(r); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexOf(id)
	if idx < 0 {
		return Rule{}, fmt.Errorf("%w: %s", ErrRuleNotFound, id)
	}

	prev := s.rules[idx]
	r.ID = id
	s.rules[idx] = r
	if err := s.persist(); err != nil {
		s.rules[idx] = prev
		return Rule{}, err
	}
	return r, nil
}

// Delete removes the rule with the given id and rewrites rules.yaml. An unknown
// id returns ErrRuleNotFound.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexOf(id)
	if idx < 0 {
		return fmt.Errorf("%w: %s", ErrRuleNotFound, id)
	}

	prev := s.rules
	s.rules = append(append([]Rule{}, s.rules[:idx]...), s.rules[idx+1:]...)
	if err := s.persist(); err != nil {
		s.rules = prev
		return err
	}
	return nil
}

// indexOf returns the position of the rule with the given id, or -1 if absent.
// Callers hold s.mu.
func (s *Store) indexOf(id string) int {
	for i, r := range s.rules {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// load reads rules.yaml into the Store, or seeds and persists the defaults when
// the file does not exist. Callers (NewStore) hold no lock yet; this runs at
// construction before the Store is shared.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.rules = seedDefaults()
			if err := s.persist(); err != nil {
				return fmt.Errorf("seed rules.yaml: %w", err)
			}
			return nil
		}
		return fmt.Errorf("read rules.yaml: %w", err)
	}

	var doc rulesFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse rules.yaml: %w", err)
	}
	s.rules = doc.Rules
	return nil
}

// persist writes the current rule set to rules.yaml, creating the .config
// directory if needed. Slice 5 reuses this on every mutation; this slice calls
// it once at seed time.
func (s *Store) persist() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(rulesFile{Rules: s.rules})
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return err
	}
	return nil
}

// seedDefaults returns the two starter default rules written on first run, both
// enabled, each with a freshly generated stable ID. They are illustrative
// examples a user edits to fit their own repo.
//
//   - "team chores"             — exclude self, type == chore, scope not renovate.
//   - "service-a — teammate_a"  — author teammate_a, scope contains service-a.
func seedDefaults() []Rule {
	return []Rule{
		{
			ID:             newID(),
			Name:           "team chores",
			Enabled:        true,
			AuthorsExclude: []string{selfToken},
			TypeInclude:    "^chore$",
			ScopeExclude:   "renovate",
		},
		{
			ID:             newID(),
			Name:           "service-a — teammate_a",
			Enabled:        true,
			AuthorsInclude: []string{"teammate_a"},
			ScopeInclude:   "service-a",
		},
	}
}

// newID returns a stable random hex identifier for a rule, independent of its
// editable name.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is catastrophic and effectively never happens;
		// panicking here surfaces it at startup rather than minting a weak ID.
		panic(fmt.Sprintf("generate rule id: %v", err))
	}
	return hex.EncodeToString(b[:])
}
