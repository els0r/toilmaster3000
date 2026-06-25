package settings_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/settings"
	"github.com/stretchr/testify/require"
)

// TestSeedsDefaultsOnFirstRun proves first run (no settings.yaml) seeds the
// per-switch cost band with its documented defaults (ADR 0012) and persists them,
// so the money range lands on a known basis from the very first boot.
func TestSeedsDefaultsOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")

	s, err := settings.NewStore(path)
	require.NoError(t, err)

	got := s.Get()
	require.Equal(t, 10, got.CostLow, "10 min × CHF1.00/min gross")
	require.Equal(t, 26, got.CostHigh, "23 min × CHF1.15/min loaded")
	require.Equal(t, "CHF", got.Currency)

	// The defaults were written to disk and reload identically (no re-seed).
	require.FileExists(t, path)
	s2, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, got, s2.Get(), "seeded settings reload verbatim")
}

// TestLoadsExistingFileVerbatim proves an existing, valid settings.yaml is loaded
// as written (PascalCase keys, like rules.yaml) without re-seeding.
func TestLoadsExistingFileVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	yaml := "" +
		"CostLow: 7\n" +
		"CostHigh: 30\n" +
		"Currency: \"€\"\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	s, err := settings.NewStore(path)
	require.NoError(t, err)

	got := s.Get()
	require.Equal(t, 7, got.CostLow)
	require.Equal(t, 30, got.CostHigh)
	require.Equal(t, "€", got.Currency)
}

// TestSelfHealsPreRangeFile proves a settings.yaml written under the old
// minutes×rate schema (ADR 0010) — which has no Cost keys — is reseeded to the
// full new defaults on load and rewritten, so the schema change is invisible and
// the money headline can never come back CHF0 – CHF0 (ADR 0012).
func TestSelfHealsPreRangeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	oldSchema := "" +
		"MinutesPerSwitch: 23\n" +
		"HourlyRate: 100\n" +
		"Currency: \"$\"\n"
	require.NoError(t, os.WriteFile(path, []byte(oldSchema), 0o644))

	s, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, settings.Defaults(), s.Get(), "missing cost keys reseed to defaults")

	// The healed defaults were rewritten, so a reload finds a valid file (no re-heal).
	reloaded, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, settings.Defaults(), reloaded.Get())
}

// TestSelfHealsZeroCost proves a file present but with a non-positive bound (a
// hand-edit that would zero or invert the band) is treated as unset and reseeded,
// the same guard the PUT path enforces structurally (ADR 0012).
func TestSelfHealsZeroCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	zeroed := "" +
		"CostLow: 0\n" +
		"CostHigh: 26\n" +
		"Currency: \"CHF\"\n"
	require.NoError(t, os.WriteFile(path, []byte(zeroed), 0o644))

	s, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, settings.Defaults(), s.Get(), "a non-positive bound reseeds the band")
}

// TestReplacePersistsAndReloads proves a full-replace (the PUT /settings path)
// overwrites every constant in memory and on disk, surviving a reload — the
// settings round-trip the editor relies on.
func TestReplacePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, err := settings.NewStore(path)
	require.NoError(t, err)

	next := settings.Settings{CostLow: 12, CostHigh: 40, Currency: "£"}
	require.NoError(t, s.Replace(next))
	require.Equal(t, next, s.Get(), "replace updates the in-memory settings")

	reloaded, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, next, reloaded.Get(), "replace persisted to disk")
}
