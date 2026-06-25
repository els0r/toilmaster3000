package settings_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/settings"
	"github.com/stretchr/testify/require"
)

// TestSeedsDefaultsOnFirstRun proves first run (no settings.yaml) seeds the
// analytics assumption constants with their documented defaults (ADR 0010) and
// persists them, so the figures land on a known basis from the very first boot.
func TestSeedsDefaultsOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")

	s, err := settings.NewStore(path)
	require.NoError(t, err)

	got := s.Get()
	require.Equal(t, 23, got.MinutesPerSwitch, "Gloria Mark's measured refocus figure")
	require.Equal(t, 100, got.HourlyRate)
	require.Equal(t, "$", got.Currency)

	// The defaults were written to disk and reload identically (no re-seed).
	require.FileExists(t, path)
	s2, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, got, s2.Get(), "seeded settings reload verbatim")
}

// TestLoadsExistingFileVerbatim proves an existing settings.yaml is loaded as
// written (PascalCase keys, like rules.yaml) without re-seeding.
func TestLoadsExistingFileVerbatim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	yaml := "" +
		"MinutesPerSwitch: 30\n" +
		"HourlyRate: 150\n" +
		"Currency: \"€\"\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	s, err := settings.NewStore(path)
	require.NoError(t, err)

	got := s.Get()
	require.Equal(t, 30, got.MinutesPerSwitch)
	require.Equal(t, 150, got.HourlyRate)
	require.Equal(t, "€", got.Currency)
}

// TestReplacePersistsAndReloads proves a full-replace (the PUT /settings path)
// overwrites every constant in memory and on disk, surviving a reload — the
// settings round-trip the assumption chip relies on.
func TestReplacePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	s, err := settings.NewStore(path)
	require.NoError(t, err)

	next := settings.Settings{MinutesPerSwitch: 45, HourlyRate: 200, Currency: "£"}
	require.NoError(t, s.Replace(next))
	require.Equal(t, next, s.Get(), "replace updates the in-memory settings")

	reloaded, err := settings.NewStore(path)
	require.NoError(t, err)
	require.Equal(t, next, reloaded.Get(), "replace persisted to disk")
}
