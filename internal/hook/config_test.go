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

	cfg, err := hook.Load(path, hook.GitHub)
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

	cfg, err := hook.Load(path, hook.GitHub)
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

func TestLoadAcceptsOpenCodeHarness(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security vet
    Harness: opencode
    Prompt: Vet this diff for malicious changes.
    Enabled: true
`)

	cfg, err := hook.Load(path, hook.GitHub)

	require.NoError(t, err)
	require.Equal(t, "opencode", cfg.Screens[0].Harness)
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

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)

	require.Len(t, cfg.Screens, 1)
	generated := cfg.Screens[0].ID
	require.NotEmpty(t, generated, "a missing Id is generated at boot")
	require.Len(t, cfg.Notifiers, 1)
	require.Equal(t, "keepme01", cfg.Notifiers[0].ID, "a present Id is kept verbatim")

	// The heal was persisted: a reload finds the same Ids in the file.
	reloaded, err := hook.Load(path, hook.GitHub)
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

	_, err = hook.Load(path, hook.GitHub)
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

	cfg, err := hook.Load(path, hook.GitHub)
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

	cfg, err := hook.Load(path, hook.GitHub)
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

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)
	require.Len(t, cfg.Notifiers, 2)
	require.Equal(t, []string{"*.go", "services/api/**"}, cfg.Notifiers[0].Paths)
	require.Empty(t, cfg.Notifiers[1].Paths, "a Notifier without Paths is unscoped, not scoped to nothing")
}

// TestLoadDecodesNotifierWorkDir proves a Notifier's optional harness anchor
// decodes from hooks.yaml (ADR 0027): WorkDir is the absolute directory the
// harness process runs in — the lever that makes that directory's ambient
// skills discoverable, and the run's read ceiling. A Notifier without it is
// unanchored and runs in tm3k's own cwd, exactly as every Notifier did before
// WorkDir existed.
func TestLoadDecodesNotifierWorkDir(t *testing.T) {
	dir := t.TempDir()
	path := writeHooks(t, `
Notifiers:
  - Id: aaaa1111
    Name: go review assist
    Harness: copilot
    Prompt: /golang-pr-review
    Point: queue_entered
    WorkDir: `+dir+`
    Enabled: true
  - Id: bbbb2222
    Name: bash review assist
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Enabled: true
`)

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)
	require.Len(t, cfg.Notifiers, 2)
	require.Equal(t, dir, cfg.Notifiers[0].WorkDir)
	require.Empty(t, cfg.Notifiers[1].WorkDir, "a Notifier without WorkDir is unanchored, not anchored to nothing")
}

// TestLoadRejectsBadWorkDir is the WorkDir preflight table (ADR 0027 decision
// 5), joining the refuse-at-boot family: a WorkDir that is not an absolute path
// to an existing directory refuses startup, named with the offending hook.
//
// The three refusals share one motive. WorkDir is a read grant handed to an
// agent that holds a gh publishing channel, so "which directory" must never be
// resolved by accident: a relative path would silently depend on where the
// binary happened to be started, and a missing or non-directory path would run
// the agent in tm3k's own cwd — no skills found, one generic review posted
// once, forever.
//
// The $VAR and ~ rows are the same rule read forward: NO expansion is
// performed. Both spellings are simply not absolute, so they are refused —
// which is the point, because an unset variable expands to "" and "" silently
// inherits tm3k's cwd. The row proves the refusal survives even when the
// variable IS set to a perfectly good directory.
func TestLoadRejectsBadWorkDir(t *testing.T) {
	realDir := t.TempDir()
	notADir := filepath.Join(realDir, "SKILL.md")
	require.NoError(t, os.WriteFile(notADir, []byte("# skill"), 0o644))

	tests := []struct {
		name    string
		workDir string
		wantErr error
		wantMsg []string
	}{
		{
			name:    "relative path",
			workDir: "skills",
			wantErr: hook.ErrBadWorkDir,
			wantMsg: []string{"go review assist", "skills"},
		},
		{
			name:    "absolute path that does not exist",
			workDir: filepath.Join(realDir, "nope"),
			wantErr: hook.ErrBadWorkDir,
			wantMsg: []string{"go review assist", "nope"},
		},
		{
			name:    "absolute path to a file, not a directory",
			workDir: notADir,
			wantErr: hook.ErrBadWorkDir,
			wantMsg: []string{"go review assist", "SKILL.md"},
		},
		{
			name:    "an environment variable is never expanded",
			workDir: "$TM3K_TEST_SKILLS",
			wantErr: hook.ErrBadWorkDir,
			wantMsg: []string{"go review assist", "$TM3K_TEST_SKILLS"},
		},
		{
			name:    "a tilde is never expanded",
			workDir: "~/skills",
			wantErr: hook.ErrBadWorkDir,
			wantMsg: []string{"go review assist", "~/skills"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set for real: expansion would turn this into a valid directory,
			// so the refusal below is proof that no expansion happened.
			t.Setenv("TM3K_TEST_SKILLS", realDir)
			path := writeHooks(t, `
Notifiers:
  - Name: go review assist
    Harness: copilot
    Prompt: /golang-pr-review
    Point: queue_entered
    WorkDir: "`+tt.workDir+`"
    Enabled: true
`)
			_, err := hook.Load(path, hook.GitHub)
			require.ErrorIs(t, err, tt.wantErr)
			for _, msg := range tt.wantMsg {
				require.ErrorContains(t, err, msg)
			}
		})
	}
}

// TestLoadRejectsMissingPromptFile closes the pre-existing hole ADR 0027
// decision 5 names, on BOTH kinds. A PromptFile is resolved at *fire* time —
// for a Notifier that is after ledger.Mark has already appended the row, so a
// mistyped path is a logged miss with the fire already spent: that PR loses
// its review permanently, with no retry and no 3-strikes path (which is
// Screens-only anyway). Boot is the only moment the typo can still be cheap.
func TestLoadRejectsMissingPromptFile(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "review.md")
	require.NoError(t, os.WriteFile(present, []byte("review it"), 0o644))
	missing := filepath.Join(dir, "typo.md")

	t.Run("screen naming a missing prompt file", func(t *testing.T) {
		_, err := hook.Load(writeHooks(t, `
Screens:
  - Name: security screen
    Harness: claude
    PromptFile: `+missing+`
    Enabled: true
`), hook.GitHub)
		require.ErrorIs(t, err, hook.ErrBadPromptFile)
		require.ErrorContains(t, err, "security screen")
		require.ErrorContains(t, err, "typo.md")
	})

	t.Run("notifier naming a missing prompt file", func(t *testing.T) {
		_, err := hook.Load(writeHooks(t, `
Notifiers:
  - Name: go review assist
    Harness: claude
    PromptFile: `+missing+`
    Point: queue_entered
    Enabled: true
`), hook.GitHub)
		require.ErrorIs(t, err, hook.ErrBadPromptFile)
		require.ErrorContains(t, err, "go review assist")
		require.ErrorContains(t, err, "typo.md")
	})

	t.Run("a prompt file that exists boots", func(t *testing.T) {
		cfg, err := hook.Load(writeHooks(t, `
Screens:
  - Name: security screen
    Harness: claude
    PromptFile: `+present+`
    Enabled: true
Notifiers:
  - Name: go review assist
    Harness: claude
    PromptFile: `+present+`
    Point: queue_entered
    Enabled: true
`), hook.GitHub)
		require.NoError(t, err)
		require.Len(t, cfg.Screens, 1)
		require.Len(t, cfg.Notifiers, 1)
	})
}

// TestDisabledHooksDeferExistenceChecks draws the line the two existence
// preflights (PromptFile, WorkDir) share with the eligibility mechanism
// (ADR 0031, hook.Spec.Classify): existence on disk is checked for the hooks
// that can actually RUN, because a disabled hook can neither spend a fire nor
// read a prompt. That keeps the shipped examples/hooks.yaml bootable — it
// carries disabled entries naming a prompt file you have not copied yet and a
// skills checkout only you can create — while the check still fires at the
// exact moment it matters: the boot after Enabled is flipped to true.
//
// Well-formedness is NOT deferred. A relative WorkDir is refused on a disabled
// Notifier too: "absolute path" is a property of what the operator wrote, not
// of the filesystem, and catching it early costs nothing.
func TestDisabledHooksDeferExistenceChecks(t *testing.T) {
	dir := t.TempDir()
	missingFile := filepath.Join(dir, "not-copied-yet.md")
	missingDir := filepath.Join(dir, "skills-worktree")

	cfg, err := hook.Load(writeHooks(t, `
Screens:
  - Name: security screen
    Harness: claude
    PromptFile: `+missingFile+`
    Enabled: false
Notifiers:
  - Name: go review assist
    Harness: copilot
    Prompt: /golang-pr-review
    Point: queue_entered
    WorkDir: `+missingDir+`
    Enabled: false
`), hook.GitHub)
	require.NoError(t, err, "a disabled hook names resources it does not need yet")
	require.Len(t, cfg.Screens, 1)
	require.Len(t, cfg.Notifiers, 1)

	_, err = hook.Load(writeHooks(t, `
Notifiers:
  - Name: go review assist
    Harness: copilot
    Prompt: /golang-pr-review
    Point: queue_entered
    WorkDir: skills
    Enabled: false
`), hook.GitHub)
	require.ErrorIs(t, err, hook.ErrBadWorkDir, "a relative WorkDir is malformed whether or not the hook runs")
}

// TestScopeIsNotifierOnly pins decision 1 of ADR 0026 structurally: Paths
// exists on NotifierConfig and NOWHERE on the shared Spec, so no Screen can
// carry it. A scoped Screen would have to resolve to proceed where it does not
// apply — handing hooks.yaml a way to silently un-gate whole file classes (a
// security screen scoped to **/*.go would auto-approve a malicious Makefile-only
// PR with zero screening). The type-level hazard stays UNREPRESENTABLE, the
// same technique as ScreenConfig carrying no Point — but since Paths cannot
// decode into ScreenConfig, a Screen writing it anyway is now a strict-decode
// unknown field (ADR 0032, superseding the old silent-acquire-nothing
// pinning): the mis-scoping refuses the boot BY NAME instead of vanishing.
func TestScopeIsNotifierOnly(t *testing.T) {
	_, onNotifier := reflect.TypeOf(hook.NotifierConfig{}).FieldByName("Paths")
	require.True(t, onNotifier, "scope is declared on the Notifier kind")

	_, onScreen := reflect.TypeOf(hook.ScreenConfig{}).FieldByName("Paths")
	require.False(t, onScreen, "a Screen — directly or through the shared Spec — can never carry scope")

	_, onSpec := reflect.TypeOf(hook.Spec{}).FieldByName("Paths")
	require.False(t, onSpec, "the shared Spec must not grow scope: both kinds would inherit it")

	// A hooks.yaml Screen that writes Paths anyway now refuses the boot: the
	// field is not there to decode into, so strict decoding catches it as an
	// unknown key rather than silently dropping it.
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
	_, err := hook.Load(path, hook.GitHub)
	require.ErrorIs(t, err, hook.ErrUnknownField)
	require.ErrorContains(t, err, "Paths")
}

// TestWorkDirIsNotifierOnly pins decision 2 of ADR 0027 structurally: WorkDir
// exists on NotifierConfig and NOWHERE on the shared Spec, so no Screen can
// carry it. A working tree is mutable, unversioned input — anchor a Screen to
// one and its verdict depends on whatever branch or half-finished rebase the
// operator last left there, so two runs over the same PR head can disagree for
// reasons no ledger records. A gate whose input is not reproducible is not a
// gate. As with Paths, the type-level hazard stays UNREPRESENTABLE, but
// writing it on a Screen anyway is now a strict-decode unknown field
// (ADR 0032, superseding the old silent-acquire-nothing pinning).
func TestWorkDirIsNotifierOnly(t *testing.T) {
	_, onNotifier := reflect.TypeOf(hook.NotifierConfig{}).FieldByName("WorkDir")
	require.True(t, onNotifier, "the harness anchor is declared on the Notifier kind")

	_, onScreen := reflect.TypeOf(hook.ScreenConfig{}).FieldByName("WorkDir")
	require.False(t, onScreen, "a Screen — directly or through the shared Spec — can never carry an anchor")

	_, onSpec := reflect.TypeOf(hook.Spec{}).FieldByName("WorkDir")
	require.False(t, onSpec, "the shared Spec must not grow an anchor: both kinds would inherit it")

	// A hooks.yaml Screen that writes WorkDir anyway now refuses the boot: the
	// field is not there to decode into, so strict decoding catches it as an
	// unknown key rather than silently dropping it.
	path := writeHooks(t, `
Screens:
  - Id: aaaa1111
    Name: security screen
    Harness: claude
    Prompt: vet it
    WorkDir: skills
    Enabled: true
`)
	_, err := hook.Load(path, hook.GitHub)
	require.ErrorIs(t, err, hook.ErrUnknownField)
	require.ErrorContains(t, err, "WorkDir")
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
    Harness: gemini
    Prompt: review it
    Point: queue_entered
`,
			wantErr: hook.ErrUnknownHarness,
			wantMsg: []string{"review assist", "gemini", "opencode"},
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
		{
			name: "unknown field on a screen",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    Foo: bar
`,
			wantErr: hook.ErrUnknownField,
			wantMsg: []string{"Foo"},
		},
		{
			name: "miscased key is unknown, not case-insensitively matched",
			doc: `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    requires:
      Forge: github
`,
			wantErr: hook.ErrUnknownField,
			wantMsg: []string{"requires"},
		},
		{
			name: "unknown top-level key",
			doc: `
Blah: true
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
`,
			wantErr: hook.ErrUnknownField,
			wantMsg: []string{"Blah"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeHooks(t, tt.doc)
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			_, err = hook.Load(path, hook.GitHub)
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

// TestLoadDecodesRequires proves a hook's optional Requires block parses off
// hooks.yaml — Forge and Tools both optional, hand-edited like the rest of the
// file (ADR 0031). Both hook kinds carry it, since Spec is shared.
func TestLoadDecodesRequires(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: gitlab-only screen
    Harness: claude
    Prompt: vet it
    Requires:
      Forge: gitlab
      Tools:
        - jq
    Enabled: true
Notifiers:
  - Name: slack notifier
    Harness: claude
    Prompt: notify
    Point: queue_entered
    Requires:
      Tools:
        - slack-cli
    Enabled: false
`)

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)

	require.Equal(t, hook.Forge("gitlab"), cfg.Screens[0].Requires.Forge)
	require.Equal(t, []string{"jq"}, cfg.Screens[0].Requires.Tools)

	require.Equal(t, hook.Forge(""), cfg.Notifiers[0].Requires.Forge, "Forge is optional")
	require.Equal(t, []string{"slack-cli"}, cfg.Notifiers[0].Requires.Tools)
}

// TestLoadAbsentRequiresIsZeroValue proves a hook with no Requires block loads
// exactly as it did before the field existed — absent Requires changes no
// existing behaviour (ADR 0031).
func TestLoadAbsentRequiresIsZeroValue(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: security vet
    Harness: claude
    Prompt: vet it
    Enabled: true
`)

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)

	require.Equal(t, hook.Requires{}, cfg.Screens[0].Requires)
}

// TestLoadSelfHealPreservesRequires proves the Id self-heal rewrite (which
// re-marshals the whole file) does not drop a hook's Requires block: a reload
// after the heal still carries it verbatim.
func TestLoadSelfHealPreservesRequires(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: gitlab-only screen
    Harness: claude
    Prompt: vet it
    Requires:
      Forge: gitlab
      Tools:
        - jq
    Enabled: true
`)

	_, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)

	reloaded, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)
	require.Equal(t, hook.Forge("gitlab"), reloaded.Screens[0].Requires.Forge)
	require.Equal(t, []string{"jq"}, reloaded.Screens[0].Requires.Tools)
}

// TestLoadSelfHealRoundTripsUnderStrictDecoding proves strict decoding
// (ADR 0032) does not collide with the self-heal write path: the persisted
// file (Id healed in, every field yaml.Marshal renders from the SAME struct
// tags KnownFields checks against) still decodes cleanly on reload — the
// marshal/unmarshal tag symmetry that makes this automatic, not something
// that needed hand-tuning per field.
func TestLoadSelfHealRoundTripsUnderStrictDecoding(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: security vet
    Harness: claude
    Model: opus
    Prompt: vet it
    Timeout: 90s
    Enabled: true
    Requires:
      Forge: github
      Tools:
        - jq
Notifiers:
  - Name: go review assist
    Harness: claude
    PromptFile: prompts/go-review.md
    Point: queue_entered
    Paths:
      - "*.go"
    Enabled: false
`)

	healed, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err, "the first load heals Ids and persists")
	require.NotEmpty(t, healed.Screens[0].ID)
	require.NotEmpty(t, healed.Notifiers[0].ID)

	reloaded, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err, "the persisted, healed file must still strictly decode")
	require.Equal(t, healed, reloaded, "the round trip is lossless")
}

// TestLoadRejectsUnknownForge proves a typo'd or unknown Requires.Forge value
// ("Github", "guthub") refuses the boot naming the hook, exactly like
// Harness/Point/Paths — a mis-scoping via typo must not silently resolve to a
// permanent, unexplained skip (ADR 0031 consequence 3). A legitimate OTHER
// forge value ("gitlab" while this instance runs github) stays valid — it is
// merely ineligible here, never a config error.
func TestLoadRejectsUnknownForge(t *testing.T) {
	tests := []struct {
		name  string
		forge string
	}{
		{name: "wrong case", forge: "Github"},
		{name: "misspelled", forge: "guthub"},
		{name: "unsupported forge", forge: "bitbucket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeHooks(t, `
Screens:
  - Name: security screen
    Harness: claude
    Prompt: vet it
    Requires:
      Forge: `+tt.forge+`
    Enabled: true
`)
			_, err := hook.Load(path, hook.GitHub)
			require.ErrorIs(t, err, hook.ErrUnknownForge)
			require.ErrorContains(t, err, "security screen")
			require.ErrorContains(t, err, tt.forge)
		})
	}
}

// TestLoadAcceptsLegitimateOtherForgeValue proves a KNOWN other-forge value is
// not a validation error: "gitlab" on a github-active Load boots clean, valid
// and merely ineligible here (ADR 0031's whole mixed-portfolio point).
func TestLoadAcceptsLegitimateOtherForgeValue(t *testing.T) {
	path := writeHooks(t, `
Screens:
  - Name: gitlab screen
    Harness: claude
    Prompt: vet it
    Requires:
      Forge: gitlab
    Enabled: true
`)
	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err)
	require.Equal(t, hook.Forge("gitlab"), cfg.Screens[0].Requires.Forge)
}

// TestLoadRejectsBlankToolEntry proves an empty or whitespace-only Tools entry
// refuses the boot naming the hook — the same typo-must-surface-at-boot
// doctrine as ErrBadPattern.
func TestLoadRejectsBlankToolEntry(t *testing.T) {
	path := writeHooks(t, `
Notifiers:
  - Name: review assist
    Harness: claude
    Prompt: review it
    Point: queue_entered
    Requires:
      Tools:
        - jq
        - "  "
    Enabled: true
`)
	_, err := hook.Load(path, hook.GitHub)
	require.ErrorIs(t, err, hook.ErrBadTool)
	require.ErrorContains(t, err, "review assist")
}

// TestLoadIneligibleHookSkipsExistenceChecks proves the failure mode ADR 0031
// exists to remove: a hook scoped to the OTHER forge, naming a PromptFile or
// WorkDir that exists only on that other machine, must not kill boot on THIS
// instance — under uniform hard-fail, neither instance could ever boot a
// shared hooks.yaml. Narrow fix: only existence stats are skipped; the hook
// is still parsed and still ineligible, never silently mutated.
func TestLoadIneligibleHookSkipsExistenceChecks(t *testing.T) {
	missingFile := filepath.Join(t.TempDir(), "gitlab-only-prompt.md")
	missingDir := filepath.Join(t.TempDir(), "gitlab-only-skills")

	path := writeHooks(t, `
Screens:
  - Name: gitlab screen
    Harness: claude
    PromptFile: `+missingFile+`
    Requires:
      Forge: gitlab
    Enabled: true
Notifiers:
  - Name: gitlab notifier
    Harness: claude
    Prompt: review it
    Point: queue_entered
    WorkDir: `+missingDir+`
    Requires:
      Forge: gitlab
    Enabled: true
`)

	cfg, err := hook.Load(path, hook.GitHub)
	require.NoError(t, err, "an ineligible hook's unreachable local paths must never refuse this instance's boot")
	require.Len(t, cfg.Screens, 1)
	require.Len(t, cfg.Notifiers, 1)
}

// TestLoadEligibleHookStillStatsExistence pins the narrow scope of the fix
// above: a hook actually IN SCOPE for this instance keeps today's hard-fail
// behaviour bit-for-bit — only ineligible hooks get the pass.
func TestLoadEligibleHookStillStatsExistence(t *testing.T) {
	missingFile := filepath.Join(t.TempDir(), "missing-prompt.md")

	path := writeHooks(t, `
Screens:
  - Name: security screen
    Harness: claude
    PromptFile: `+missingFile+`
    Requires:
      Forge: github
    Enabled: true
`)

	_, err := hook.Load(path, hook.GitHub)
	require.ErrorIs(t, err, hook.ErrBadPromptFile, "an eligible hook's missing prompt file must still refuse the boot")
}
