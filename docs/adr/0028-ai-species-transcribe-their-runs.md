# AI species transcribe their runs

## Context

The first live Notifier run posted a real review, and the operator's only record
of what it did looked like this:

```
level=INFO msg="notifier: agent run transcript" notifier="go review assist" pr=141872
  transcript="I'll start by invoking the review skill...\n\n**Findings (4 Friction, 1 Nit)**\n1. ..."
```

A multi-paragraph account of an agent's reasoning, escaped onto one line of a
structured log. It is the most valuable artifact a Notifier produces — the
agent posts as the operator's own identity, so this is the record of what was
said in their name — and it was the least usable thing in the system: unfiltered
by PR, unsearchable, interleaved with cycle noise, and gone as soon as the log
rotated.

The Screen leg was worse. `Adapter.Screen` returned `(hook.Verdict, error)`, so
the run's result text died inside the adapter the moment extraction succeeded —
or failed. `extract verdict: no verdict document in harness result` is the most
opaque failure tm3k can produce: the agent said *something*, that something is
the only evidence of why it did not answer in the instructed shape, and the
harness discarded it before anyone could look.

Both legs already had the text in hand. Neither had anywhere to put it.

## Decision

1. **`.state/transcripts.jsonl` is a write-only sink.** Appended after each AI
   run; never loaded at boot, never consulted by the engine, never on the wire.
   Every other `.state` file is loaded because the engine *consults it to decide
   something* — the fired set gates a fire, the verdict fold gates an action. A
   transcript gates nothing, so there is nothing to hold in memory. The type is
   named `TranscriptSink`, deliberately neither `Store` nor `Ledger`: in this
   codebase both of those words mean "loaded at boot and consulted", and a
   reader who expects a `load()` here should be told otherwise by the name.

2. **The species writes it, not the runner — and the obligation binds AI
   species alone.** `Transcriber` lives in `internal/harness` next to the
   species, and `NewAIScreen`/`NewAINotifier` both require one: an AI species
   cannot be constructed with nowhere to put its account of itself. The
   alternative — `hook.Notifier.Notify` returning `(transcript, error)` for the
   `NotifierRunner` to persist — was rejected because it puts an AI-shaped
   artifact on the *kind's* contract, leaving a future `SlackNotifier` owing a
   permanent `""`. `hook` stays innocent of transcripts entirely; a non-AI
   species has no import path to the concept.

3. **Extraction moves out of the adapters and into `AIScreen`.** `Adapter.Screen`
   returns result text, converging with `Agent.Act`: a harness run *is* a
   transcript on both legs, and what a species does with it afterwards — extract
   a verdict, or nothing at all — is species policy. This amends ADR 0023's
   description of the seam. It exists to serve decision 1: `AIScreen` transcribes
   and *then* extracts, so the run that failed to yield a verdict keeps the text
   that explains why. `ExtractVerdict` (the claude-only envelope wrapper) is
   deleted; `ExtractVerdictText` — always documented as "the harness-neutral
   half" — becomes the one extractor with one caller. Each adapter still
   normalises its own CLI's output (claude unwraps its JSON envelope, copilot's
   silent-mode stdout already is the text) and stops there.

   The seams stay two interfaces, not one: the authority differs — a screen run
   is toolless, an act run carries `gh` — so ADR 0023's "a screening-only adapter
   stays a one-method implementation" is untouched.

4. **`Transcribe` returns nothing.** Transcription happens *after* the run has
   had its effect: the Notifier's review is already posted, the Screen's text is
   already in hand. A returned error would let the Notifier leg log a miss for a
   review that succeeded — uncorrectable, since at-most-once means no later cycle
   revisits it — and let the Screen leg turn a valid verdict into a failed
   attempt, burning one of three strikes and re-spending a paid harness call
   because a disk write hiccuped. Every other `.state` writer returns an error
   *because the error changes behaviour*; here it must change nothing, ever, so
   the invariant lives in the type rather than in two callers' discipline. The
   sink logs its own write failures — naming the sink and the run, **and the
   transcript, as a last resort**. Deleting the old log line made the sink the
   only copy; the fire is already marked in `hookfires.jsonl`, so at-most-once
   means no later cycle produces another. Dropping the text on a failed write
   would leave an agent having spoken as the operator's identity on GitHub with
   no record anywhere. An escaped, hard-to-read copy beats none, and the
   ugliness is what makes the operator notice. On the success path the prose is
   gone from the log, which is the whole point.

5. **A row iff there is text, and it carries no outcome.** A run that produced
   nothing has no account to give, and the failure is already recorded by
   whoever owns it (an `error` row in `verdicts.jsonl`, the runner's logged miss
   for a fire). The row deliberately does *not* carry the run's verdict: the
   species writes the transcript, the runner writes the verdict — two writers,
   two files — so an `outcome` field would be a second recording of a fact
   someone else owns, written at a different moment with an independent failure
   mode. A partial failure would leave the two files disagreeing about one run.
   `verdicts.jsonl` says what was decided; `transcripts.jsonl` says what was
   said.

6. **The link is `hook_id` + `number`; `head` and `hook_name` are carried for
   legibility, not linkage.** For a Notifier the pair is unique by construction
   (a fire happens once per PR ever, ADR 0021), so it joins one `hookfires.jsonl`
   row exactly. For a Screen it selects the key's one-to-three attempts in
   `verdicts.jsonl`, which `At` orders — and those are rows a reader opens
   together anyway. Minting a run ID for an exact 1:1 was rejected: it costs a
   field on two committed on-disk formats plus a `RunID` smuggled through
   `hook.Verdict`, to disambiguate what is already legible. `hook_name` is
   user-editable, so an old row may name a hook since renamed — that is history,
   not staleness; the key never moves.

   "Already legible" only holds if the key has ONE name, and it did not:
   `verdicts.jsonl` spelled it `screen_id`, so `jq -s ... .hook_id` over the
   three files silently dropped every outcome row and returned the transcripts
   alone — a reader had to open Go source to discover the rename, which is the
   one thing a write-only, jq-only file exists to avoid. `verdicts.jsonl` now
   writes `hook_id` too (a Screen is a hook; this is its stable hook `Id`,
   ADR 0023). Rows written under the old name still load, forever: an
   append-only ledger that rewrites itself on upgrade is a worse trade than one
   dead field name. This amends ADR 0022's row format.

   `head` is the head tm3k **observed** when the point fired, not a pin on what
   the agent read. Neither species pins its fetch to it — `gh pr diff` takes a
   number, and a Notifier's agent fetches the PR itself under its own `gh`
   authority — so a push between dispatch and run leaves the row naming the
   older commit. Pinning is a change to what a run *is*, not to how it is
   recorded; it is out of scope here and would be its own decision. The field
   is recorded for what it truly is: the SHA a Screen's verdict rows key on
   (ADR 0022), and the commit the run was dispatched for.

7. **Unbounded; no rotation, no per-row cap.** A few KB per run on a
   single-workstation tool is low single-digit MB a year. Truncation would cut
   exactly the part worth having on the day an agent posts something wrong.

   **Because nothing reads the file, no line-length limit applies.** If a reader
   is ever added it must NOT inherit `FiredLedger.load`'s 1 MB `scanner.Buffer` —
   a long transcript would break it. This is the trap this decision sets.

## Consequences

- The `notifier: agent run transcript` log line is deleted and `AINotifier` no
  longer holds a logger. `NotifierRunner`'s existing `notifier: fired` line is
  the operator's signal that a transcript exists.
- Screen runs are now transcribed too — they never were before, since the text
  never left the adapter.
- `TranscriptSink` holds a mutex, like every sibling in `.state/`. The first
  draft went without one, resting wholeness on O_APPEND plus **one** `Write` of
  the fully marshalled row — but `os.File.Write` loops on a short write, so a
  40 KB row split at a quota boundary can have a concurrent species' row land in
  the gap, and nothing ever reads the file to notice. The single write stays;
  the lock is what makes a row whole.
- Rows are written with HTML escaping OFF (`internal/jsonl`). `encoding/json`
  escapes `<`, `>` and `&` by default — safe for JSON embedded in a page, which
  no `.state` file is — and left on it would make a transcript quoting Go code
  ungreppable in the one file that exists to be read with `grep` and `jq`.
- Adapter-level failure narrows. Copilot's screen leg now has exactly one check
  of its own — empty stdout; prose carrying no verdict document is the species'
  failure, and it keeps its transcript.
- Text and error are not exclusive at the harness seams. A CLI that printed its
  whole answer and then exited non-zero — or was killed mid-sentence by the
  hook's timeout — returns both, and both species transcribe what came back
  *before* they judge the error. The failure is untouched: still a failed
  attempt on the 3-strikes path, still a logged miss for a fire. The
  row-iff-text rule suppresses the runs that genuinely said nothing.
- An errored claude envelope (`is_error: true`) returns its `result` alongside
  the error. The flag says the run *ended* badly — max turns, a tool failure —
  not that it said nothing on the way, and that text is the only thing
  explaining the burnt strike the operator sees in `verdicts.jsonl`. The other
  envelope failures still return `""`, because there genuinely is no text:
  nothing was printed, or what was printed did not decode.
