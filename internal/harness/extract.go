// Package harness owns AI harness invocation (ADR 0023): adapters behind a
// small interface — claude and copilot in MVP (ADR 0024) — that fetch a PR's
// diff, compose the screen prompt, run the harness headless, and return what it
// said. Above them sit the two AI species, which transcribe every run
// (ADR 0028) and then do their kind's work with the text: AIScreen extracts a
// verdict structurally, AINotifier ignores it. A run with no confident
// extractable verdict errors as a failed attempt (ADR 0022's 3-strikes path);
// nothing here ever fabricates a verdict in either direction.
package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/els0r/toilmaster3000/internal/hook"
)

// ExtractVerdictText extracts the Screen's verdict structurally from one
// harness run's result text. It is the whole of extraction and has exactly one
// caller, AIScreen — the adapters hand back result text and stop (ADR 0028),
// so the harness-neutral half is the only half there is. The designated
// location is a fenced code block carrying
// exactly the instructed JSON document {"verdict": "proceed"|"hold",
// "reason": "..."}. Exactly one well-formed document in the result is the
// verdict; anything else — none, several (e.g. a second one echoed out of the
// diff), malformed, or verdict-shaped text outside a fence — is an error, a
// failed attempt on the 3-strikes path (ADR 0022). Prose is never scanned for
// keywords: "CAN PROCEED" in agent chatter means nothing (ADR 0023).
func ExtractVerdictText(result string) (hook.Verdict, error) {
	var verdicts []hook.Verdict
	for _, block := range fencedBlocks(result) {
		if v, ok := decodeVerdictDocument(block); ok {
			verdicts = append(verdicts, v)
		}
	}
	switch len(verdicts) {
	case 1:
		return verdicts[0], nil
	case 0:
		return hook.Verdict{}, errors.New("no verdict document in harness result")
	default:
		return hook.Verdict{}, fmt.Errorf("ambiguous harness result: %d verdict documents", len(verdicts))
	}
}

// resultText decodes one claude CLI run's stdout (`claude -p --output-format
// json`, a single result envelope) and returns its result text. Everything
// else on stdout — crash text, partial output, a non-result envelope, an
// errored run — is an error. Shared by both claude legs, which now return the
// same thing: the run's result text, for the species to transcribe and then do
// with as its kind requires.
func resultText(output []byte) (string, error) {
	if len(bytes.TrimSpace(output)) == 0 {
		return "", errors.New("empty claude output")
	}

	var env struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(output, &env); err != nil {
		return "", fmt.Errorf("decode claude result envelope: %w", err)
	}
	if env.Type != "result" {
		return "", fmt.Errorf("unexpected claude envelope type %q", env.Type)
	}
	if env.IsError {
		return "", fmt.Errorf("claude run errored (subtype %q)", env.Subtype)
	}
	return env.Result, nil
}

// decodeVerdictDocument reports whether a fenced block is a well-formed
// verdict document, and decodes it. Well-formed means exactly the instructed
// shape: one JSON object, no unknown fields (an embellished or foreign
// document is not ours), verdict one of proceed|hold. Reason is carried for
// the human but not required — its absence leaves the verdict unambiguous.
func decodeVerdictDocument(block string) (hook.Verdict, bool) {
	dec := json.NewDecoder(strings.NewReader(block))
	dec.DisallowUnknownFields()
	var doc struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := dec.Decode(&doc); err != nil {
		return hook.Verdict{}, false
	}
	if dec.More() {
		// Trailing content in the same fence: not the single instructed
		// document.
		return hook.Verdict{}, false
	}
	outcome := hook.Outcome(doc.Verdict)
	if outcome != hook.Proceed && outcome != hook.Hold {
		return hook.Verdict{}, false
	}
	return hook.Verdict{Outcome: outcome, Reason: doc.Reason}, true
}

// fencedBlocks returns the contents of every closed markdown code fence
// (``` opener with optional info string, ``` closer) in the text, in order.
// An unclosed fence yields nothing — half a block is no block.
func fencedBlocks(text string) []string {
	var blocks []string
	var cur []string
	in := false
	for line := range strings.Lines(text) {
		trimmed := strings.TrimSpace(line)
		if !in {
			if strings.HasPrefix(trimmed, "```") {
				in = true
				cur = nil
			}
			continue
		}
		if trimmed == "```" {
			in = false
			blocks = append(blocks, strings.Join(cur, "\n"))
			continue
		}
		cur = append(cur, strings.TrimSuffix(line, "\n"))
	}
	return blocks
}
