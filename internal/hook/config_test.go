package hook_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/stretchr/testify/require"
)

// writeHooks writes a hooks.yaml document into a temp dir and returns its path.
func writeHooks(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks.yaml")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o644))
	return path
}

// TestLoadAbsentFileMeansNoHooks proves an absent hooks.yaml is the no-hooks
// case: the binary boots clean with an empty config, and — unlike rules.yaml
// and settings.yaml — nothing is seeded to disk. Hooks are opt-in, hand-edited
// config (ADR 0023); a file the user never wrote must never appear.
func TestLoadAbsentFileMeansNoHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.yaml")

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Empty(t, cfg.Screens)
	require.Empty(t, cfg.Notifiers)
	require.NoFileExists(t, path, "absent hooks.yaml is never seeded")
}

// TestLoadValidFile proves a hand-written hooks.yaml (PascalCase, two lists —
// ADR 0023) loads with every declarative field populated: the AI-species
// fields (Harness/Model/Prompt|PromptFile/Timeout/Enabled), the Notifier's
// post-point, and Timeout parsed from Go duration syntax.
func TestLoadValidFile(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security vet
    Harness: claude
    Model: opus
    Prompt: "Vet this diff for malicious changes."
    Timeout: 90s
    Enabled: true
Notifiers:
  - Id: bbbb2222
    Name: golang review assist
    Harness: claude
    PromptFile: prompts/go-review.md
    Point: queue_entered
    Enabled: false
`)

	cfg, err := hook.Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Screens, 1)
	s := cfg.Screens[0]
	require.Equal(t, "aaaa1111", s.ID)
	require.Equal(t, "security vet", s.Name)
	require.Equal(t, "claude", s.Harness)
	require.Equal(t, "opus", s.Model)
	require.Equal(t, "Vet this diff for malicious changes.", s.Prompt)
	require.Equal(t, 90*time.Second, time.Duration(s.Timeout))
	require.True(t, s.Enabled)

	require.Len(t, cfg.Notifiers, 1)
	n := cfg.Notifiers[0]
	require.Equal(t, "bbbb2222", n.ID)
	require.Equal(t, "golang review assist", n.Name)
	require.Equal(t, "claude", n.Harness)
	require.Equal(t, "prompts/go-review.md", n.PromptFile)
	require.Equal(t, hook.QueueEntered, n.Point)
	require.False(t, n.Enabled)
}

// TestLoadSelfHealsMissingIds proves the settings.yaml self-heal precedent
// applied to hook identity (ADR 0023): a hook missing Id gets a stable one
// generated at boot and written back into the file, while a hook that already
// carries an Id keeps it verbatim. A reload returns the healed Ids unchanged —
// the stability the verdict store and fired-ledger key on.
func TestLoadSelfHealsMissingIds(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    Enabled: true
Notifiers:
  - Id: keepme01
    Name: review assist
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Enabled: true
`)

	cfg, err := hook.Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Screens, 1)
	generated := cfg.Screens[0].ID
	require.NotEmpty(t, generated, "a missing Id is generated at boot")
	require.Len(t, cfg.Notifiers, 1)
	require.Equal(t, "keepme01", cfg.Notifiers[0].ID, "a present Id is kept verbatim")

	// The heal was persisted: a reload finds the same Ids in the file.
	reloaded, err := hook.Load(path)
	require.NoError(t, err)
	require.Equal(t, generated, reloaded.Screens[0].ID, "healed Id survives reload")
	require.Equal(t, "keepme01", reloaded.Notifiers[0].ID)
}

// TestLoadLeavesFullyIdentifiedFileUntouched proves a file whose hooks all
// carry Ids is loaded verbatim and never rewritten — the self-heal writes only
// when it healed something, so a hand-edited file is not churned on every boot.
func TestLoadLeavesFullyIdentifiedFileUntouched(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security vet
    Harness: claude
    Prompt: vet it
    Enabled: true
`)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	_, err = hook.Load(path)
	require.NoError(t, err)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "no heal needed, no rewrite")
}

// TestTimeoutOrDefault proves the per-hook run bound (the only tunable,
// ADR 0022): an absent Timeout reads as the 10m default; a set one reads as
// written. No runtime consumes it yet — the harness runner (a later slice)
// will.
func TestTimeoutOrDefault(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: default timeout
    Harness: claude
    Prompt: vet it
    Enabled: true
  - Id: bbbb2222
    Name: custom timeout
    Harness: claude
    Prompt: vet it
    Timeout: 2m30s
    Enabled: true
`)

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, cfg.Screens[0].TimeoutOrDefault(), "absent Timeout defaults to 10m")
	require.Equal(t, 2*time.Minute+30*time.Second, cfg.Screens[1].TimeoutOrDefault())
}

// TestLoadAcceptsCopilotHarness proves copilot is an allowlisted harness on
// both hook kinds (ADR 0024) — the second adapter is one allowlist entry.
func TestLoadAcceptsCopilotHarness(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security vet
    Harness: copilot
    Prompt: vet it
    Enabled: true
Notifiers:
  - Id: bbbb2222
    Name: review assist
    Harness: copilot
    Prompt: review it
    Point: queue_entered
    Enabled: true
`)

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Equal(t, "copilot", cfg.Screens[0].Harness)
	require.Equal(t, "copilot", cfg.Notifiers[0].Harness)
}

// TestLoadDecodesNotifierPaths proves a Notifier's optional scope decodes from
// hooks.yaml (ADR 0026): Paths is the glob list gating whether the Notifier
// applies to a PR at all, and a Notifier without it stays unscoped — absent
// Paths fires on every PR, so every hooks.yaml written before scope existed
// keeps its meaning.
func TestLoadDecodesNotifierPaths(t *testing.T) {
	path := writeHooks(t, `
Notifiers:
  - Id: aaaa1111
    Name: go review assist
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Paths:
      - "*.go"
      - "services/api/**"
    Enabled: true
  - Id: bbbb2222
    Name: cross-cutting review
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Enabled: true
`)

	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Notifiers, 2)
	require.Equal(t, []string{"*.go", "services/api/**"}, cfg.Notifiers[0].Paths)
	require.Empty(t, cfg.Notifiers[1].Paths, "a Notifier without Paths is unscoped, not scoped to nothing")
}

// TestScopeIsNotifierOnly pins decision 1 of ADR 0026 structurally: Paths
// exists on NotifierConfig and NOWHERE on the shared Spec, so no Screen can
// carry it. A scoped Screen would have to resolve to proceed where it does not
// apply — handing hooks.yaml a way to silently un-gate whole file classes (a
// security screen scoped to **/*.go would auto-approve a malicious Makefile-only
// PR with zero screening). The hazard stays UNREPRESENTABLE rather than
// validated against, the same technique as ScreenConfig carrying no Point.
func TestScopeIsNotifierOnly(t *testing.T) {
	_, onNotifier := reflect.TypeOf(hook.NotifierConfig{}).FieldByName("Paths")
	require.True(t, onNotifier, "scope is declared on the Notifier kind")

	_, onScreen := reflect.TypeOf(hook.ScreenConfig{}).FieldByName("Paths")
	require.False(t, onScreen, "a Screen — directly or through the shared Spec — can never carry scope")

	_, onSpec := reflect.TypeOf(hook.Spec{}).FieldByName("Paths")
	require.False(t, onSpec, "the shared Spec must not grow scope: both kinds would inherit it")

	// And a hooks.yaml Screen that writes Paths anyway acquires nothing: the
	// field is not there to decode into, so the Screen gates every PR as always.
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security screen
    Harness: claude
    Prompt: vet it
    Paths:
      - "*.go"
    Enabled: true
`)
	cfg, err := hook.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Screens, 1)
	require.True(t, cfg.Screens[0].Enabled, "the Screen loads; it simply has no scope to acquire")
}

// TestLoadRejectsBadConfig is the preflight table (the boot gate of ADR 0023):
// each misconfiguration class refuses startup with a sentinel error whose
// message names the offending hook, so the user can fix hooks.yaml without
// spelunking. Structural parse failures (broken YAML, bad Timeout) refuse
// startup too, through the parse path rather than a sentinel.
func TestLoadRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr error    // sentinel checked with ErrorIs; nil for parse failures
		wantMsg []string // substrings the error must carry (the offending hook)
	}{
		{
			name: "unknown harness on a screen",
			doc: `
Screens:
  - Name: security vet
    Harness: gemini
    Prompt: vet it
`,
			wantErr: hook.ErrUnknownHarness,
			wantMsg: []string{"security vet", "gemini", "claude", "copilot"},
		},
		{
			name: "unknown harness on a notifier",
			doc: `
Notifiers:
  - Name: review assist
    Harness: opencode
    Prompt: review it
    Point: queue_entered
`,
			wantErr: hook.ErrUnknownHarness,
			wantMsg: []string{"review assist", "opencode"},
		},
		{
			name: "empty harness",
			doc: `
Screens:
  - Name: security vet
    Prompt: vet it
`,
			wantErr: hook.ErrUnknownHarness,
			wantMsg: []string{"security vet"},
		},
		{
			name: "missing prompt",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
`,
			wantErr: hook.ErrMissingPrompt,
			wantMsg: []string{"security vet"},
		},
		{
			name: "both prompt and prompt file",
			doc: `
Notifiers:
  - Name: review assist
    Harness: claude
    Prompt: review it
    PromptFile: prompts/review.md
    Point: queue_entered
`,
			wantErr: hook.ErrAmbiguousPrompt,
			wantMsg: []string{"review assist"},
		},
		{
			name: "notifier at a pre-point",
			doc: `
Notifiers:
  - Name: review assist
    Harness: claude
    Prompt: review it
    Point: pre_approve
`,
			wantErr: hook.ErrBadPoint,
			wantMsg: []string{"review assist", "pre_approve"},
		},
		{
			name: "notifier at an unknown point",
			doc: `
Notifiers:
  - Name: review assist
    Harness: claude
    Prompt: review it
    Point: on_merge
`,
			wantErr: hook.ErrBadPoint,
			wantMsg: []string{"review assist", "on_merge"},
		},
		{
			name: "notifier without a point",
			doc: `
Notifiers:
  - Name: review assist
    Harness: claude
    Prompt: review it
`,
			wantErr: hook.ErrBadPoint,
			wantMsg: []string{"review assist"},
		},
		{
			name: "malformed path pattern on a notifier",
			doc: `
Notifiers:
  - Name: go review assist
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Paths:
      - "*.go"
      - "[bad"
`,
			wantErr: hook.ErrBadPattern,
			wantMsg: []string{"go review assist", "[bad"},
		},
		{
			name: "duplicate name within a kind",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
  - Name: security vet
    Harness: claude
    Prompt: vet it differently
`,
			wantErr: hook.ErrDuplicateName,
			wantMsg: []string{"security vet"},
		},
		{
			name: "duplicate name across kinds",
			doc: `
Screens:
  - Name: vet
    Harness: claude
    Prompt: vet it
Notifiers:
  - Name: vet
    Harness: claude
    Prompt: review it
    Point: queue_entered
`,
			wantErr: hook.ErrDuplicateName,
			wantMsg: []string{"vet"},
		},
		{
			name: "missing name",
			doc: `
Screens:
  - Harness: claude
    Prompt: vet it
`,
			wantErr: hook.ErrMissingName,
			wantMsg: []string{"screen #1"},
		},
		{
			name: "malformed yaml",
			doc: `
Screens:
  - Name: [broken
`,
			wantMsg: []string{"parse hooks.yaml"},
		},
		{
			name: "unparseable timeout",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    Timeout: ten minutes
`,
			wantMsg: []string{"Timeout"},
		},
		{
			name: "non-positive timeout",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    Timeout: -5m
`,
			wantMsg: []string{"Timeout", "positive"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHooks(t, tt.doc)
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			_, err = hook.Load(path)
			require.Error(t, err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			for _, msg := range tt.wantMsg {
				require.ErrorContains(t, err, msg)
			}

			// A refused config is never self-heal-rewritten: the file the user
			// must fix is exactly the file they wrote.
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "invalid file left untouched")
		})
	}
}
