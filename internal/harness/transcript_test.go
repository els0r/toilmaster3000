package harness

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TS1 (tracer): a transcribed run lands on disk as one JSON line in
// transcripts.jsonl — an AI species accounts for itself in the sink, never in
// the log (ADR 0028). The on-disk key names are the record's contract: this
// file is read by a human with jq and by nothing else.
func TestTranscriptSinkAppendsOneRowPerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.jsonl")
	sink := NewTranscriptSink(path)

	rec := TranscriptRecord{
		Kind:     "notifier",
		HookID:   "n1",
		HookName: "go review assist",
		Number:   141872,
		Head:     "feedface",
		At:       time.Date(2026, 8, 10, 14, 1, 57, 0, time.UTC),
		Text:     "Review posted (one `COMMENTED` review, no approval, no merge).",
	}
	sink.Transcribe(rec)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 1, "one run, one row")

	var got TranscriptRecord
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &got))
	require.Equal(t, rec, got, "the row round-trips verbatim — a transcript is never abridged")

	var keys map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &keys))
	require.ElementsMatch(t,
		[]string{"kind", "hook_id", "hook_name", "number", "head", "at", "transcript"},
		mapKeys(keys))
}

// TS2: the sink is append-only across runs and across restarts — a second
// species writing, or a fresh process opening the same path, adds a row and
// never truncates the ones before it. The file is the standing history of
// everything the AI species have said.
func TestTranscriptSinkAppendsAcrossRunsAndRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.jsonl")

	sink := NewTranscriptSink(path)
	sink.Transcribe(TranscriptRecord{Kind: "screen", HookID: "s1", Number: 1, Text: "first"})
	sink.Transcribe(TranscriptRecord{Kind: "notifier", HookID: "n1", Number: 2, Text: "second"})

	// A fresh sink over the same path is the restart: it appends, never resets.
	NewTranscriptSink(path).Transcribe(TranscriptRecord{Kind: "screen", HookID: "s1", Number: 3, Text: "third"})

	require.Equal(t, []string{"first", "second", "third"}, transcriptTexts(t, path))
}

// TS3: a transcripts.jsonl under a directory that does not exist yet is the
// first-run case — the sink creates the parent rather than dropping the row.
// tm3k's .state/ is git-ignored, so a fresh checkout has no directory at all.
func TestTranscriptSinkCreatesItsParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".state", "transcripts.jsonl")

	NewTranscriptSink(path).Transcribe(TranscriptRecord{Kind: "screen", HookID: "s1", Number: 1, Text: "first run"})

	require.Equal(t, []string{"first run"}, transcriptTexts(t, path))
}

// TS4: concurrent species writes produce whole, separate rows. Up to four
// notifier runs plus the screen pool are in flight at once, and a transcript is
// far past the size a bare O_APPEND write keeps atomic — so an interleaved
// write would corrupt not just its own row but its neighbour's.
func TestTranscriptSinkKeepsConcurrentRowsWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.jsonl")
	sink := NewTranscriptSink(path)

	const runs = 8
	var wg sync.WaitGroup
	want := make([]string, 0, runs)
	for i := range runs {
		text := strings.Repeat(string(rune('a'+i)), 8*1024) // past any atomic-write window
		want = append(want, text)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.Transcribe(TranscriptRecord{Kind: "screen", HookID: "s1", Number: i, Text: text})
		}()
	}
	wg.Wait()

	require.ElementsMatch(t, want, transcriptTexts(t, path), "every row lands intact, none shredded by another")
}

// TS5: a sink that cannot write says so and returns normally. Transcription
// happens after the run has had its effect, so the write must never fail the
// caller (that is why Transcribe returns nothing) — but a transcript silently
// vanishing is the one way this design could mislead an operator, so the miss
// is logged, and the text goes with it as a last resort. The sink is the only
// copy: the fire is already marked in hookfires.jsonl, so at-most-once means no
// later cycle produces another, and an agent that posted a review as the
// operator's identity would otherwise leave no record of it anywhere.
func TestTranscriptSinkReportsAWriteItCannotMake(t *testing.T) {
	// A parent that is a regular file: neither MkdirAll nor OpenFile can win.
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	path := filepath.Join(blocked, "transcripts.jsonl")

	logs := captureLogs(t)
	NewTranscriptSink(path).Transcribe(TranscriptRecord{
		Kind: "notifier", HookID: "n1", HookName: "go review assist",
		Number: 7, Text: "posted a review comment",
	})

	require.Contains(t, logs.String(), "transcript")
	require.Contains(t, logs.String(), path, "the operator needs to know which sink is failing")
	require.Contains(t, logs.String(), "posted a review comment",
		"an escaped copy in the log beats no copy of what was said in the operator's name")
}

// TS5b: the fallback is the failure path's alone. A sink that wrote the row has
// put the prose where it belongs, and repeating it in the log is the exact
// thing ADR 0028 exists to end — the species tests assert the same from above.
func TestTranscriptSinkKeepsProseOutOfTheLogWhenItWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.jsonl")

	logs := captureLogs(t)
	NewTranscriptSink(path).Transcribe(TranscriptRecord{
		Kind: "notifier", HookID: "n1", HookName: "go review assist",
		Number: 7, Text: "posted a review comment",
	})

	require.Equal(t, []string{"posted a review comment"}, transcriptTexts(t, path))
	require.NotContains(t, logs.String(), "posted a review comment")
}

// recordingTranscriber is the in-memory sink of the species tests: it captures
// the rows a species writes, so a test asserts what was recorded without going
// near a file.
type recordingTranscriber struct{ rows []TranscriptRecord }

func (r *recordingTranscriber) Transcribe(rec TranscriptRecord) { r.rows = append(r.rows, rec) }

// captureLogs redirects the default logger for one test and returns the buffer
// it writes to.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// transcriptTexts reads a transcripts.jsonl and returns each row's transcript,
// in file order — the assertion the sink tests share.
func transcriptTexts(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var texts []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var rec TranscriptRecord
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		texts = append(texts, rec.Text)
	}
	return texts
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
