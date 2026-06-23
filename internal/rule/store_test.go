package rule_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// TestSeedsDefaultsOnFirstRun proves first run (no rules.yaml) seeds the two
// default rules — both enabled, with stable IDs — and persists them to disk.
func TestSeedsDefaultsOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")

	s, err := rule.NewStore(path)
	require.NoError(t, err)

	rules := s.List()
	require.Len(t, rules, 2)

	chores := rules[0]
	require.Equal(t, "team chores", chores.Name)
	require.True(t, chores.Enabled)
	require.Equal(t, []string{"@me"}, chores.AuthorsExclude)
	require.Equal(t, "^chore$", chores.TypeInclude)
	require.Equal(t, "renovate", chores.ScopeExclude)
	require.NotEmpty(t, chores.ID, "seeded rule has a stable generated ID")

	svc := rules[1]
	require.Equal(t, "service-a — teammate_a", svc.Name)
	require.True(t, svc.Enabled)
	require.Equal(t, []string{"teammate_a"}, svc.AuthorsInclude)
	require.Equal(t, "service-a", svc.ScopeInclude)
	require.NotEmpty(t, svc.ID)

	require.NotEqual(t, chores.ID, svc.ID, "IDs are distinct")

	// The defaults were written to disk and reload with the same IDs and order.
	require.FileExists(t, path)
	s2, err := rule.NewStore(path)
	require.NoError(t, err)
	reloaded := s2.List()
	require.Len(t, reloaded, 2)
	require.Equal(t, chores.ID, reloaded[0].ID, "reload preserves seeded ID and order")
	require.Equal(t, "team chores", reloaded[0].Name)
	require.Equal(t, svc.ID, reloaded[1].ID)
	require.Equal(t, "service-a — teammate_a", reloaded[1].Name)
}

// TestLoadsExistingFileVerbatim proves an existing rules.yaml is loaded in file
// order without re-seeding.
func TestLoadsExistingFileVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	yaml := "" +
		"Rules:\n" +
		"  - ID: aaa\n" +
		"    Name: only one\n" +
		"    Enabled: false\n" +
		"    TypeInclude: ^feat$\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	s, err := rule.NewStore(path)
	require.NoError(t, err)

	rules := s.List()
	require.Len(t, rules, 1)
	require.Equal(t, "only one", rules[0].Name)
	require.False(t, rules[0].Enabled)
	require.Equal(t, "^feat$", rules[0].TypeInclude)
	require.Empty(t, rules[0].Class, "absent Class loads empty (read as approve), needing no migration")
}

// TestClassRoundTripsThroughYAML proves the Class discriminator survives a
// persist+reload: a review rule keeps its Class on disk, an explicit approve
// rule keeps "approve", and an absent Class loads empty (read as approve).
func TestClassRoundTripsThroughYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	yaml := "" +
		"Rules:\n" +
		"  - ID: aaa\n" +
		"    Name: review one\n" +
		"    Enabled: true\n" +
		"    Class: review\n" +
		"    ScopeInclude: osixpatch\n" +
		"  - ID: bbb\n" +
		"    Name: approve one\n" +
		"    Enabled: true\n" +
		"    Class: approve\n" +
		"    TypeInclude: ^chore$\n" +
		"  - ID: ccc\n" +
		"    Name: legacy no class\n" +
		"    Enabled: true\n" +
		"    TypeInclude: ^fix$\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	s, err := rule.NewStore(path)
	require.NoError(t, err)

	rules := s.List()
	require.Len(t, rules, 3)
	require.Equal(t, "review", rules[0].Class)
	require.True(t, rules[0].IsReview(), "Class: review reads as a Review Rule")
	require.Equal(t, "approve", rules[1].Class)
	require.False(t, rules[1].IsReview())
	require.Empty(t, rules[2].Class, "absent Class loads empty (read as approve)")
	require.False(t, rules[2].IsReview())

	// A mutation rewrites the file; reload and confirm Class persisted to disk.
	updated := rules[0]
	updated.Name = "review renamed"
	_, err = s.Update(updated.ID, updated)
	require.NoError(t, err)

	reloaded, err := rule.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, "review", reloaded.List()[0].Class, "Class survives the YAML write")
}
