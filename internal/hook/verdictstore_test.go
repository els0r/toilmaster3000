package hook_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// V1 (tracer): an appended verdict persists to verdicts.jsonl and survives a
// reload — an unchanged head reuses its verdict across restarts, so the row
// must outlive the process (ADR 0022).
func TestVerdictPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts.jsonl")

	s, err := hook.NewVerdictStore(path)
	require.NoError(t, err)
	rec := hook.VerdictRecord{
		ScreenID: "scr-1", Number: 42, Head: "abc123",
		Outcome: hook.Proceed, Reason: "trivial dep bump",
		At: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	}
	require.NoError(t, s.Append(rec))

	got, ok := s.Latest("scr-1", 42, "abc123")
	require.True(t, ok)
	require.Equal(t, rec, got)

	reloaded, err := hook.NewVerdictStore(path)
	require.NoError(t, err)
	got, ok = reloaded.Latest("scr-1", 42, "abc123")
	require.True(t, ok, "the verdict survives a restart")
	require.Equal(t, rec, got)
}

// V2: the latest row per key wins — an error attempt followed by a proceed
// reads as proceed (the append-only file is the history; the store serves
// only the newest row) — and a different head is a different key entirely: a
// new push misses the store, which IS the re-screen trigger (no invalidation
// logic exists or is needed, ADR 0022).
func TestLatestRowPerKeyWinsAndHeadsAreIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts.jsonl")
	s, err := hook.NewVerdictStore(path)
	require.NoError(t, err)

	early := hook.VerdictRecord{ScreenID: "scr-1", Number: 7, Head: "old-head",
		Outcome: hook.Errored, Reason: "harness timeout", At: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)}
	late := hook.VerdictRecord{ScreenID: "scr-1", Number: 7, Head: "old-head",
		Outcome: hook.Hold, Reason: "touches auth", At: time.Date(2026, 8, 6, 9, 5, 0, 0, time.UTC)}
	require.NoError(t, s.Append(early))
	require.NoError(t, s.Append(late))

	got, ok := s.Latest("scr-1", 7, "old-head")
	require.True(t, ok)
	require.Equal(t, late, got, "the newest row wins for the key")

	_, ok = s.Latest("scr-1", 7, "new-head")
	require.False(t, ok, "a new push is a fresh key: the store misses, so the PR re-screens")
	_, ok = s.Latest("scr-2", 7, "old-head")
	require.False(t, ok, "verdicts are per-screen: another screen has no verdict for the same head")

	// Latest-wins also holds through a reload — the on-disk order is the truth.
	reloaded, err := hook.NewVerdictStore(path)
	require.NoError(t, err)
	got, ok = reloaded.Latest("scr-1", 7, "old-head")
	require.True(t, ok)
	require.Equal(t, late, got)
}

// V4: ErrorStreak counts the consecutive TRAILING error rows per key — the
// failed-attempt count the 3-strikes fold consumes (ADR 0022 decision 5). A
// real verdict resets its key to zero, other keys are untouched, and the
// streak is rebuilt from disk order on reload so the attempt cap survives a
// restart.
func TestErrorStreakCountsTrailingErrorsPerKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts.jsonl")
	s, err := hook.NewVerdictStore(path)
	require.NoError(t, err)

	require.Zero(t, s.ErrorStreak("scr-1", 7, "h1"), "an unknown key has no failed attempts")

	row := func(head string, outcome hook.Outcome, reason string) hook.VerdictRecord {
		return hook.VerdictRecord{ScreenID: "scr-1", Number: 7, Head: head,
			Outcome: outcome, Reason: reason, At: time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)}
	}

	require.NoError(t, s.Append(row("h1", hook.Errored, "timeout")))
	require.NoError(t, s.Append(row("h1", hook.Errored, "nonzero exit")))
	require.Equal(t, 2, s.ErrorStreak("scr-1", 7, "h1"), "each error row extends its key's streak")

	require.NoError(t, s.Append(row("h2", hook.Errored, "timeout")))
	require.Equal(t, 1, s.ErrorStreak("scr-1", 7, "h2"), "a new push is a fresh key: its streak starts over")
	require.Equal(t, 2, s.ErrorStreak("scr-1", 7, "h1"), "other keys' streaks are untouched")

	require.NoError(t, s.Append(row("h1", hook.Hold, "touches auth")))
	require.Zero(t, s.ErrorStreak("scr-1", 7, "h1"), "a real verdict ends the failure streak")

	require.NoError(t, s.Append(row("h1", hook.Errored, "timeout")))
	require.Equal(t, 1, s.ErrorStreak("scr-1", 7, "h1"), "only TRAILING errors count: the streak restarts after a verdict")

	reloaded, err := hook.NewVerdictStore(path)
	require.NoError(t, err)
	require.Equal(t, 1, reloaded.ErrorStreak("scr-1", 7, "h1"), "the streak is rebuilt from disk order")
	require.Equal(t, 1, reloaded.ErrorStreak("scr-1", 7, "h2"))
}

// V3: first run (no verdicts.jsonl) is the empty store, and nothing is
// written until the first append (the armed-store precedent — no seed).
func TestEmptyStoreOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdicts.jsonl")

	s, err := hook.NewVerdictStore(path)
	require.NoError(t, err)
	_, ok := s.Latest("scr-1", 1, "h")
	require.False(t, ok, "no key has a verdict on first run")
	require.NoFileExists(t, path, "nothing to persist until the first verdict")
}
