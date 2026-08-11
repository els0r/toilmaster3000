package jsonl

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type row struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// J1 (tracer): appending is append-only across calls and across processes — a
// fresh call over the same path adds a line and never truncates the ones
// before it. Every .state ledger in tm3k depends on exactly this.
func TestAppendIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")

	require.NoError(t, Append(path, row{ID: "a", Text: "first"}))
	require.NoError(t, Append(path, row{ID: "b", Text: "second"}))

	require.Equal(t, []string{
		`{"id":"a","text":"first"}`,
		`{"id":"b","text":"second"}`,
	}, lines(t, path))
}

// J2: a ledger under a directory that does not exist yet is the first-run case
// — .state/ is git-ignored, so a fresh checkout has none and the writer creates
// it rather than dropping the row.
func TestAppendCreatesItsParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".state", "rows.jsonl")

	require.NoError(t, Append(path, row{ID: "a", Text: "first run"}))

	require.Equal(t, []string{`{"id":"a","text":"first run"}`}, lines(t, path))
}

// J3: a record that cannot be marshalled fails before anything is opened, so a
// bad record never leaves a file behind — nor a half-written line in one that
// already exists.
func TestAppendRejectsAnUnmarshallableRecordWithoutTouchingDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")

	require.Error(t, Append(path, map[string]any{"fn": func() {}}))
	require.NoFileExists(t, path, "nothing opened, nothing created")
}

// J3b: code text survives the round trip to disk unescaped. encoding/json
// escapes <, > and & by default for JSON embedded in a page; no .state file is
// ever that, and left on it makes a transcript quoting Go code ungreppable in
// the one file whose entire purpose is being read with grep and jq.
func TestAppendDoesNotEscapeCodeText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")
	const code = "if a < b && c > d { <-ctx.Done() }"

	require.NoError(t, Append(path, row{ID: "a", Text: code}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), code, "grep over the file finds what the file says")
	for _, escape := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		require.NotContains(t, string(data), escape, "no \\uXXXX escapes where the operator expects code")
	}
}

// J4: a write that cannot be made is an error, not a silent no-op — every
// caller decides for itself what a failed append means (the verdict store
// refuses to update memory, the sink logs a miss), and none of them can decide
// anything if the failure never surfaces.
func TestAppendSurfacesAWriteItCannotMake(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))

	require.Error(t, Append(filepath.Join(blocked, "rows.jsonl"), row{ID: "a"}))
}

// J5: concurrent appends produce whole, separate lines. The transcript sink
// writes rows far past any atomic-write window, so an interleaved write would
// corrupt not just its own line but its neighbour's.
func TestAppendKeepsConcurrentLinesWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows.jsonl")

	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, Append(path, row{Text: strings.Repeat(string(rune('a'+i)), 8*1024)}))
		}()
	}
	wg.Wait()

	got := lines(t, path)
	require.Len(t, got, writers)
	for _, line := range got {
		require.True(t, strings.HasPrefix(line, `{"id":"","text":"`), "each line is a whole record: %.40s", line)
		require.True(t, strings.HasSuffix(line, `"}`), "each line is a whole record: %.40s", line)
	}
}

// lines returns the file's non-empty lines in on-disk order.
func lines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}
