package rule_test

import (
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// TestValidate covers the two semantic guards (per ADR 0001): reject a rule
// that constrains nothing, and reject a non-empty regex that fails to compile,
// naming the offending field. Both surface as sentinel errors the HTTP layer
// maps to 4xx.
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		rule    rule.Rule
		wantErr error
		// field, when set, must appear in the error message (offending field).
		field string
	}{
		{
			name:    "constrains nothing is rejected",
			rule:    rule.Rule{Name: "approve everything", Enabled: true},
			wantErr: rule.ErrEmptyRule,
		},
		{
			name: "author include alone is enough",
			rule: rule.Rule{Name: "by author", AuthorsInclude: []string{"alice"}},
		},
		{
			name: "author exclude alone is enough",
			rule: rule.Rule{Name: "not me", AuthorsExclude: []string{"@me"}},
		},
		{
			name: "a single title-part pattern is enough",
			rule: rule.Rule{Name: "chores", TypeInclude: "^chore$"},
		},
		{
			name:    "invalid type_include names the field",
			rule:    rule.Rule{Name: "bad", TypeInclude: "([a-z"},
			wantErr: rule.ErrInvalidRegex,
			field:   "type_include",
		},
		{
			name:    "invalid scope_exclude names the field",
			rule:    rule.Rule{Name: "bad", ScopeExclude: "(unclosed"},
			wantErr: rule.ErrInvalidRegex,
			field:   "scope_exclude",
		},
		{
			name:    "invalid description_include names the field",
			rule:    rule.Rule{Name: "bad", DescriptionInclude: "*"},
			wantErr: rule.ErrInvalidRegex,
			field:   "description_include",
		},
		{
			name: "author entries are literal, not regex-compiled",
			rule: rule.Rule{Name: "literal", AuthorsInclude: []string{"([not-a-regex"}},
		},
		// Diff-size constraints count toward "non-empty": a diff-only rule is
		// valid, and the empty-rule message is class-neutral and mentions diff.
		{
			name: "diff_min alone is enough (diff-only rule is not empty)",
			rule: rule.Rule{Name: "big diffs", DiffMin: 100},
		},
		{
			name: "diff_max alone is enough (diff-only rule is not empty)",
			rule: rule.Rule{Name: "small diffs", DiffMax: 50},
		},
		{
			name:    "constrains nothing message mentions diff size",
			rule:    rule.Rule{Name: "approve everything", Enabled: true},
			wantErr: rule.ErrEmptyRule,
			field:   "diff",
		},
		{
			name:    "DiffMin greater than DiffMax is rejected",
			rule:    rule.Rule{Name: "inverted", DiffMin: 100, DiffMax: 50},
			wantErr: rule.ErrInvalidDiffRange,
		},
		{
			name: "DiffMin equal to DiffMax is accepted",
			rule: rule.Rule{Name: "exact", DiffMin: 50, DiffMax: 50},
		},
		{
			name: "one-sided DiffMin (DiffMax zero) is accepted",
			rule: rule.Rule{Name: "lower bound only", DiffMin: 100},
		},
		{
			name: "one-sided DiffMax (DiffMin zero) is accepted",
			rule: rule.Rule{Name: "upper bound only", DiffMax: 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rule.Validate(tt.rule)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			if tt.field != "" {
				require.Contains(t, err.Error(), tt.field)
			}
		})
	}
}

// TestStoreCreateGeneratesIDAndPersists proves Create ignores any incoming id,
// generates a stable one, and rewrites rules.yaml so the new rule survives a
// reload.
func TestStoreCreateGeneratesIDAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	s, err := rule.NewStore(path) // seeds two defaults
	require.NoError(t, err)

	created, err := s.Create(rule.Rule{ID: "client-supplied-ignored", Name: "new one", Enabled: true, TypeInclude: "^fix$"})
	require.NoError(t, err)
	require.NotEqual(t, "client-supplied-ignored", created.ID, "incoming id is ignored")
	require.NotEmpty(t, created.ID)

	reloaded, err := rule.NewStore(path)
	require.NoError(t, err)
	list := reloaded.List()
	require.Len(t, list, 3)
	require.Equal(t, created.ID, list[2].ID, "created rule appended and persisted")
}

// TestStoreCreateRejectsInvalid proves Create runs validation before
// persisting: an empty rule and a bad-regex rule are both rejected without
// mutating the store or disk.
func TestStoreCreateRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	s, err := rule.NewStore(path)
	require.NoError(t, err)
	before := len(s.List())

	_, err = s.Create(rule.Rule{Name: "empty"})
	require.ErrorIs(t, err, rule.ErrEmptyRule)

	_, err = s.Create(rule.Rule{Name: "bad", ScopeInclude: "([a-z"})
	require.ErrorIs(t, err, rule.ErrInvalidRegex)

	require.Len(t, s.List(), before, "rejected creates do not mutate the store")
}

// TestStoreUpdateReplacesAndPreservesID proves Update is a full replace keyed by
// path id (the body id is irrelevant), and is how enable/disable round-trips.
func TestStoreUpdateReplacesAndPreservesID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	s, err := rule.NewStore(path)
	require.NoError(t, err)
	id := s.List()[0].ID
	require.True(t, s.List()[0].Enabled)

	updated, err := s.Update(id, rule.Rule{Name: "team chores", Enabled: false, TypeInclude: "^chore$"})
	require.NoError(t, err)
	require.Equal(t, id, updated.ID, "id preserved from the path on a full replace")
	require.False(t, updated.Enabled)

	reloaded, err := rule.NewStore(path)
	require.NoError(t, err)
	require.False(t, reloaded.List()[0].Enabled, "disabled flag persisted (enable/disable via PUT)")
}

// TestStoreUpdateDeleteUnknownID proves both report ErrRuleNotFound for an id
// not in the store.
func TestStoreUpdateDeleteUnknownID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	s, err := rule.NewStore(path)
	require.NoError(t, err)

	_, err = s.Update("nope", rule.Rule{Name: "x", TypeInclude: "^chore$"})
	require.ErrorIs(t, err, rule.ErrRuleNotFound)

	require.ErrorIs(t, s.Delete("nope"), rule.ErrRuleNotFound)
}

// TestStoreDeletePersists proves Delete removes the rule and rewrites disk.
func TestStoreDeletePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	s, err := rule.NewStore(path)
	require.NoError(t, err)
	id := s.List()[0].ID

	require.NoError(t, s.Delete(id))
	require.Len(t, s.List(), 1)

	reloaded, err := rule.NewStore(path)
	require.NoError(t, err)
	require.Len(t, reloaded.List(), 1, "delete persisted to disk")
	require.NotEqual(t, id, reloaded.List()[0].ID)
}
