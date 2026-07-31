package armed_test

import (
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/armed"
	"github.com/stretchr/testify/require"
)

// A1 (tracer): arming persists to armed.json and survives a reload — the arm
// is an explicit human decision, so it must outlive a tm3k restart.
func TestArmPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "armed.json")

	s, err := armed.NewStore(path)
	require.NoError(t, err)
	require.NoError(t, s.Arm(42))
	require.NoError(t, s.Arm(57))
	require.True(t, s.IsArmed(42))
	require.True(t, s.IsArmed(57))

	reloaded, err := armed.NewStore(path)
	require.NoError(t, err)
	require.True(t, reloaded.IsArmed(42), "arm #42 survives a restart")
	require.True(t, reloaded.IsArmed(57), "arm #57 survives a restart")
	require.False(t, reloaded.IsArmed(99), "an un-armed number stays Withheld")
}

// A2: first run (no armed.json) is the empty set — every PR defaults to
// Withheld — and nothing is written until the first arm (unlike settings,
// there are no defaults to seed).
func TestEmptySetOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "armed.json")

	s, err := armed.NewStore(path)
	require.NoError(t, err)
	require.False(t, s.IsArmed(1), "every PR defaults to Withheld")
	require.Empty(t, s.Set())
	require.NoFileExists(t, path, "nothing to persist until the first arm")
}

// A3: disarming removes the number, rewrites the file, and the removal
// survives a reload — fresh consent is required after a disarm.
func TestDisarmPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "armed.json")
	s, err := armed.NewStore(path)
	require.NoError(t, err)
	require.NoError(t, s.Arm(42))
	require.NoError(t, s.Arm(57))

	require.NoError(t, s.Disarm(42))
	require.False(t, s.IsArmed(42))
	require.True(t, s.IsArmed(57), "disarming one PR leaves the others armed")

	reloaded, err := armed.NewStore(path)
	require.NoError(t, err)
	require.False(t, reloaded.IsArmed(42), "the disarm survives a restart")
	require.True(t, reloaded.IsArmed(57))
}

// A4: Retain removes every armed number not kept — the engine's per-cycle
// cleanup (PR left the pull, or level-triggered disarm) — reporting the
// removals sorted, persisting in one rewrite, and leaving kept arms alone.
func TestRetainRemovesStaleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "armed.json")
	s, err := armed.NewStore(path)
	require.NoError(t, err)
	for _, n := range []int{7, 3, 5} {
		require.NoError(t, s.Arm(n))
	}

	removed, err := s.Retain(map[int]bool{5: true})
	require.NoError(t, err)
	require.Equal(t, []int{3, 7}, removed, "removals are reported sorted")
	require.True(t, s.IsArmed(5), "a kept arm is untouched")

	reloaded, err := armed.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, map[int]bool{5: true}, reloaded.Set(), "the cleanup persisted")

	// Nothing stale: a no-op reconciliation removes (and rewrites) nothing.
	removed, err = s.Retain(map[int]bool{5: true})
	require.NoError(t, err)
	require.Empty(t, removed)
}
