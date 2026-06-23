package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// SetSelfLogin stores the resolved @me token and SelfLogin reads it back, so
// the matcher (Slice 4) can expand @me. Defaults to empty before preflight
// resolves it.
func TestSelfLoginRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := rule.NewStore(filepath.Join(t.TempDir(), "rules.yaml"))
	require.NoError(t, err)
	eng, err := engine.New(github.NewFake(), statePath, store)
	require.NoError(t, err)

	require.Empty(t, eng.SelfLogin(), "no self login before preflight resolves it")

	eng.SetSelfLogin("octocat")
	require.Equal(t, "octocat", eng.SelfLogin())
}
