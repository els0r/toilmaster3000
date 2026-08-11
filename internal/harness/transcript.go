package harness

import (
	"log/slog"
	"strings"
	"time"

	"github.com/els0r/toilmaster3000/internal/hook"
	"github.com/els0r/toilmaster3000/internal/jsonl"
)

// TranscriptRecord is one AI run's account of itself: the on-disk shape of one
// line in transcripts.jsonl (ADR 0028). The json tags serve that disk format —
// the verdicts.jsonl idiom — and are the record's whole contract, because
// nothing in tm3k ever reads this file back: it exists for a human with jq.
//
// The link into the ledger that owns the run's OUTCOME is HookID + Number: for
// a Notifier that pair is unique by construction (a fire happens once per PR
// ever, ADR 0021), so it joins one hookfires.jsonl row exactly; for a Screen it
// selects the key's one-to-three attempts in verdicts.jsonl, which At orders.
// No run ID is minted for the exact 1:1 — it would cost a field on two
// committed on-disk formats to disambiguate rows a reader opens together anyway.
//
// Head and HookName are not part of that link. They are carried so a row reads
// without a join at all: which commit the run was dispatched for, and what the
// hook was called when it ran. HookName is user-editable, so an old row may
// name a hook that has since been renamed — that is history, not staleness; the
// key never moves.
//
// Head is the head tm3k OBSERVED when the point fired — the SHA a Screen's
// verdict rows key on (ADR 0022) — and deliberately not a claim about what the
// agent read. Neither species pins its fetch to it: the Screen's `gh pr diff`
// takes a number, not a SHA, and the Notifier's agent fetches the PR itself
// under its own gh authority. A push between dispatch and run means the agent
// saw a newer diff than this field names. Pinning would be a change to what a
// run IS, not to how it is recorded, so it does not belong here (ADR 0028).
type TranscriptRecord struct {
	Kind     string    `json:"kind"` // "screen" | "notifier"
	HookID   string    `json:"hook_id"`
	HookName string    `json:"hook_name"`
	Number   int       `json:"number"`
	Head     string    `json:"head"`
	At       time.Time `json:"at"`
	Text     string    `json:"transcript"`
}

// Transcriber is the sink seam both AI species hold (ADR 0028). It returns
// NOTHING on purpose: transcription happens AFTER the run has had its effect —
// the Notifier's review is posted, the Screen's text is in hand — so a failed
// write must never reach the caller. Were it an error, the Notifier leg would
// report a logged miss for a review that was actually posted (and at-most-once
// guarantees no later cycle corrects it), and the Screen leg would turn a valid
// verdict into a failed attempt, burning one of three strikes and re-spending a
// paid harness call because a disk write hiccuped. The invariant is in the type
// so no future caller can propagate what it must ignore.
//
// It lives here rather than in package hook because a transcript is a property
// of the AI SPECIES, not of the Notifier/Screen kinds: hook.Notifier stays a
// one-method interface, so a non-AI species — a Slack poster — owes no
// transcript and has no import path to the concept.
type Transcriber interface {
	Transcribe(rec TranscriptRecord)
}

// The two Kind values, the species' names for themselves in the sink.
const (
	kindScreen   = "screen"
	kindNotifier = "notifier"
)

// transcribe records one AI run, and is the ONE place the row-iff-text rule
// lives: a run that produced no text has no account to give — the failure is
// already recorded by whoever owns it (an error row in verdicts.jsonl, the
// runner's logged miss for a fire) — and an empty row would be noise in a file
// that exists for prose. Both species call it, so the rule cannot drift between
// them (ADR 0014).
//
// "No text" means nothing but whitespace, not the empty string. A CLI that
// answers with a bare newline said nothing, and the adapters already agree:
// copilot rejects whitespace-only stdout as `empty copilot output` before a
// species ever sees it. Testing the bare string here would have made the rule
// mean one thing through copilot and another through claude.
//
// The policy sits here rather than in the sink deliberately: the sink writes
// what it is given, and what is worth writing is the species layer's call.
func transcribe(sink Transcriber, kind string, spec hook.Spec, pr hook.PRContext, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	sink.Transcribe(TranscriptRecord{
		Kind:     kind,
		HookID:   spec.ID,
		HookName: spec.Name,
		Number:   pr.Number,
		Head:     pr.HeadSHA,
		At:       time.Now(),
		Text:     text,
	})
}

// TranscriptSink is the write-only transcripts.jsonl appender: the one
// implementation of Transcriber (ADR 0028). Deliberately neither a Store nor a
// Ledger — those names mean "loaded at boot and consulted" in this codebase
// (VerdictStore, FiredLedger), and this is loaded never and consulted never.
// It gates nothing, so there is nothing to hold in memory.
//
// It carries no mutex, unlike every sibling in .state/, because it has no
// shared state to guard: the path is immutable and each Transcribe opens its
// own descriptor. Concurrent runs — up to four notifiers plus the screen pool —
// stay whole on the strength of O_APPEND and ONE Write of the fully marshalled
// row. That last part is load-bearing: splitting the line and the newline into
// two writes would open the interleaving window this design closes.
type TranscriptSink struct {
	path   string
	logger *slog.Logger
}

// NewTranscriptSink constructs the sink over the given transcripts.jsonl path.
// It returns no error and touches no disk: with nothing to load there is
// nothing to fail, and the file appears on the first run's append. That is what
// lets main wire one unconditionally — an operator with zero hooks configured
// still gets no file, without a nil-sink branch anywhere.
func NewTranscriptSink(path string) *TranscriptSink {
	return &TranscriptSink{path: path, logger: slog.Default()}
}

// Transcribe appends one record as a JSON line, creating the parent directory
// if needed — .state/ is git-ignored, so a fresh checkout has none (the
// FiredLedger.appendLine idiom).
//
// Every failure ends here as a logged miss: the run whose account this is has
// already happened, and nothing upstream may be told otherwise. The log line
// names the sink and the run, never the transcript — putting the prose back in
// the log is the exact thing this type exists to stop.
func (s *TranscriptSink) Transcribe(rec TranscriptRecord) {
	if err := s.append(rec); err != nil {
		s.logger.Warn("transcript not recorded; the run itself stands",
			"path", s.path, "hook", rec.HookName, "pr", rec.Number, "error", err)
	}
}

// append writes the one line through the shared .state append idiom
// (internal/jsonl). Callers get the error; nobody above Transcribe ever does.
func (s *TranscriptSink) append(rec TranscriptRecord) error {
	return jsonl.Append(s.path, rec)
}
