package forge_test

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/forge"
	"github.com/stretchr/testify/require"
)

// TestAllGreen folds NORMALISED check entries into the All-Green invariant:
// true IFF there is at least one entry AND every entry is pass. An empty
// rollup is NOT green — a pipeline that never ran is no signal, and an
// auto-approver must never fire on no signal.
//
// The fold sees only the neutral pass/fail/pending vocabulary; which raw forge
// status maps to which state is the adapter's normalisation, table-tested
// there (ADR 0030 §3, §10).
func TestAllGreen(t *testing.T) {
	pass := forge.Check{State: forge.CheckPass}
	fail := forge.Check{State: forge.CheckFail}
	pending := forge.Check{State: forge.CheckPending}

	tests := []struct {
		name   string
		checks []forge.Check
		want   bool
	}{
		{
			name:   "empty rollup is not green (no signal)",
			checks: nil,
			want:   false,
		},
		{
			name:   "zero-length rollup is not green either",
			checks: []forge.Check{},
			want:   false,
		},
		{
			name:   "a single passing entry is green",
			checks: []forge.Check{pass},
			want:   true,
		},
		{
			name:   "all-pass is green",
			checks: []forge.Check{pass, pass, pass},
			want:   true,
		},
		{
			name:   "a single failing entry blocks an otherwise-green rollup",
			checks: []forge.Check{pass, fail},
			want:   false,
		},
		{
			name:   "a single pending entry blocks an otherwise-green rollup",
			checks: []forge.Check{pass, pending},
			want:   false,
		},
		{
			name:   "a lone failing entry is not green",
			checks: []forge.Check{fail},
			want:   false,
		},
		{
			name:   "a lone pending entry is not green",
			checks: []forge.Check{pending},
			want:   false,
		},
		{
			name:   "mixed pass, fail and pending is not green",
			checks: []forge.Check{pass, fail, pending},
			want:   false,
		},
		{
			name:   "the zero Check is a verdictless entry and blocks",
			checks: []forge.Check{pass, {}},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, forge.AllGreen(tt.checks))
		})
	}
}
