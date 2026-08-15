package hook_test

import (
	"errors"
	"testing"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// errNotFound stands in for exec.ErrNotFound — the fake lookPath's report for
// a binary that is not on PATH, kept local so these tests need no real
// binaries (ADR 0031's injected-lookPath seam).
var errNotFound = errors.New("executable file not found in $PATH")

// TestRequiresGrant proves the tool grant formula (ADR 0031 decision 4): the
// active forge's own CLI plus whatever Tools declares, additive — absent
// Tools grants exactly what it grants today (the forge's CLI alone), never
// less, and Tools is never a replacement.
func TestRequiresGrant(t *testing.T) {
	tests := []struct {
		name  string
		r     hook.Requires
		forge hook.Forge
		want  []string
	}{
		{
			name:  "absent Tools grants exactly the active forge's CLI — today's behaviour unchanged",
			r:     hook.Requires{},
			forge: hook.GitHub,
			want:  []string{"gh"},
		},
		{
			name:  "declared Tools is additive to the forge's CLI, never a replacement",
			r:     hook.Requires{Tools: []string{"jq"}},
			forge: hook.GitHub,
			want:  []string{"gh", "jq"},
		},
		{
			// Requires.Forge names GitHub, but the ACTIVE forge passed to Grant is
			// GitLab — proving the grant follows the active instance, never the
			// hook's own Forge declaration.
			name:  "the grant follows the active forge, not the Requires.Forge value",
			r:     hook.Requires{Forge: hook.GitHub},
			forge: hook.GitLab,
			want:  []string{"glab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.r.Grant(tt.forge)
			require.ElementsMatch(t, tt.want, got, "the grant is a set, not an order guarantee")
		})
	}
}

// alwaysFound is a lookPath stub that reports every binary present — the
// baseline for classification tests that are not exercising the Broken path.
func alwaysFound(name string) (string, error) { return "/usr/local/bin/" + name, nil }

// TestSpecClassifyForgeMismatchIsIneligible proves a hook scoped to a
// different forge than the active instance is Ineligible — never Broken,
// never a boot refusal (ADR 0031 decision 2): it was never a gate on this
// instance at all.
func TestSpecClassifyForgeMismatchIsIneligible(t *testing.T) {
	s := hook.Spec{Name: "gitlab screen", Harness: "claude", Requires: hook.Requires{Forge: hook.GitLab}}

	elig, reason := s.Classify(hook.GitHub, alwaysFound)

	require.Equal(t, hook.Ineligible, elig)
	require.Contains(t, reason, "gitlab")
}

// TestSpecClassifyOtherForgeCLIInToolsIsIneligible proves a hook naming the
// OTHER forge's CLI in Tools is ineligible on its own, without Forge being
// spelled out (ADR 0031 decision 4): declaring "glab" is self-describing on a
// GitHub instance.
func TestSpecClassifyOtherForgeCLIInToolsIsIneligible(t *testing.T) {
	s := hook.Spec{Name: "glab notifier", Harness: "claude", Requires: hook.Requires{Tools: []string{"glab"}}}

	elig, reason := s.Classify(hook.GitHub, alwaysFound)

	require.Equal(t, hook.Ineligible, elig)
	require.Contains(t, reason, "glab")
}

// TestSpecClassifyEligibleWithNoRequires proves a hook declaring nothing is
// eligible everywhere and needs only its harness binary present — absent
// Requires changes no existing behaviour (ADR 0031).
func TestSpecClassifyEligibleWithNoRequires(t *testing.T) {
	s := hook.Spec{Name: "security vet", Harness: "claude"}

	elig, reason := s.Classify(hook.GitHub, alwaysFound)

	require.Equal(t, hook.Eligible, elig)
	require.Empty(t, reason)
}

// TestSpecClassifyMissingHarnessIsBroken proves the harness-binary preflight
// (formerly checkHarnessBinaries, ADR 0024) is served by this mechanism: a
// hook in scope whose harness CLI is missing is Broken, exactly the fact the
// old dedicated check caught.
func TestSpecClassifyMissingHarnessIsBroken(t *testing.T) {
	s := hook.Spec{Name: "security vet", Harness: "copilot"}

	elig, reason := s.Classify(hook.GitHub, func(name string) (string, error) {
		if name == "copilot" {
			return "", errNotFound
		}
		return "/usr/local/bin/" + name, nil
	})

	require.Equal(t, hook.Broken, elig)
	require.Contains(t, reason, "copilot")
}

// TestSpecClassifyMissingDeclaredToolIsBroken proves a hook in scope whose
// declared Requires.Tools binary is missing is Broken — the same fact as a
// missing harness, caught by the same mechanism.
func TestSpecClassifyMissingDeclaredToolIsBroken(t *testing.T) {
	s := hook.Spec{Name: "jq notifier", Harness: "claude", Requires: hook.Requires{Tools: []string{"jq"}}}

	elig, reason := s.Classify(hook.GitHub, func(name string) (string, error) {
		if name == "jq" {
			return "", errNotFound
		}
		return "/usr/local/bin/" + name, nil
	})

	require.Equal(t, hook.Broken, elig)
	require.Contains(t, reason, "jq")
}

// TestSpecClassifyExplicitForgeWinsOverToolsInference proves the other-forge-
// CLI-in-Tools inference is a FALLBACK, not a veto (ADR 0031 decision 4): when
// Forge is spelled out and matches the active instance, naming the other
// forge's CLI in Tools is a legitimate mirror hook, eligible, with that CLI in
// its grant — the inference applies only when Forge is absent.
func TestSpecClassifyExplicitForgeWinsOverToolsInference(t *testing.T) {
	s := hook.Spec{
		Name:     "mirror notifier",
		Harness:  "claude",
		Requires: hook.Requires{Forge: hook.GitHub, Tools: []string{"glab"}},
	}

	elig, reason := s.Classify(hook.GitHub, alwaysFound)

	require.Equal(t, hook.Eligible, elig, "explicit Forge match must not be vetoed by the Tools inference")
	require.Empty(t, reason)
	require.ElementsMatch(t, []string{"gh", "glab"}, s.Requires.Grant(hook.GitHub))
}

// TestSpecClassifyUnmappedActiveForgeIsBroken proves Classify never calls
// lookPath("") when the active forge itself has no known CLI: it is a clear
// Broken classification instead (ADR 0031's mechanism guarding its own
// forgeCLI lookup).
func TestSpecClassifyUnmappedActiveForgeIsBroken(t *testing.T) {
	s := hook.Spec{Name: "security vet", Harness: "claude"}

	elig, reason := s.Classify(hook.Forge("bitbucket"), func(name string) (string, error) {
		require.NotEmpty(t, name, "lookPath must never be called with an empty binary name")
		return "/usr/local/bin/" + name, nil
	})

	require.Equal(t, hook.Broken, elig)
	require.Contains(t, reason, "bitbucket")
}
