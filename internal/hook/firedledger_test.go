package hook_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// FL1 (tracer): a fire marked at dispatch persists to hookfires.jsonl and
// survives a reload — a Notifier fires once per PR EVER, across cycles AND
// restarts, so the row must outlive the process (ADR 0021).
func TestFiredLedgerRoundTripsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hookfires.jsonl")

	l, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	require.False(t, l.Fired("hook-1", 42), "nothing has fired yet")

	rec := hook.FireRecord{
		HookID: "hook-1", Number: 42, Point: hook.QueueEntered,
		At: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	}
	marked, err := l.Mark(rec)
	require.NoError(t, err)
	require.True(t, marked, "an unfired key marks as a fresh fire")
	require.True(t, l.Fired("hook-1", 42), "the key reads fired from now on")

	reloaded, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	require.True(t, reloaded.Fired("hook-1", 42), "the fire survives a restart")
}

// FL2: marking an already-fired key is the at-most-once no-op — false back,
// nothing appended — and keys are independent per hook AND per PR: several
// Notifiers on one point each keep their own once-per-PR entry (ADR 0021).
func TestFiredLedgerMarksAtMostOncePerHookAndPR(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hookfires.jsonl")
	l, err := hook.NewFiredLedger(path)
	require.NoError(t, err)

	rec := hook.FireRecord{HookID: "hook-1", Number: 42, Point: hook.QueueEntered, At: time.Now()}
	marked, err := l.Mark(rec)
	require.NoError(t, err)
	require.True(t, marked)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	marked, err = l.Mark(rec)
	require.NoError(t, err)
	require.False(t, marked, "a fired key never marks again")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the no-op appends nothing")

	// Independent keys: another hook on the same PR, and the same hook on
	// another PR, are both fresh fires.
	marked, err = l.Mark(hook.FireRecord{HookID: "hook-2", Number: 42, Point: hook.QueueEntered, At: time.Now()})
	require.NoError(t, err)
	require.True(t, marked, "each hook keeps its own once-per-PR entry")
	marked, err = l.Mark(hook.FireRecord{HookID: "hook-1", Number: 43, Point: hook.PostApprove, At: time.Now()})
	require.NoError(t, err)
	require.True(t, marked, "each PR is its own key for the same hook")
}

// FL3: first run (no hookfires.jsonl) is the empty ledger, and nothing is
// written until the first mark (the armed/verdict-store precedent — no seed).
func TestFiredLedgerEmptyOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hookfires.jsonl")

	l, err := hook.NewFiredLedger(path)
	require.NoError(t, err)
	require.False(t, l.Fired("hook-1", 1))
	require.NoFileExists(t, path, "nothing to persist until the first fire")
}
