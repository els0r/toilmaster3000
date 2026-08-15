package hook

import (
	"fmt"
	"sort"
)

// Forge identifies the code-hosting platform a Requires block or the active
// instance targets (ADR 0030/0031). Only the two forges the product supports
// are named; a later forge is one constant plus one forgeCLI entry.
type Forge string

const (
	// GitHub is the "github" forge value, driven by the gh CLI.
	GitHub Forge = "github"
	// GitLab is the "gitlab" forge value, driven by the glab CLI.
	GitLab Forge = "gitlab"
)

// forgeCLI maps each known Forge to the CLI binary that drives it — the
// static fact ADR 0031 decision 5 enforces (which CLI a hook may invoke),
// as distinct from the verb ceilings that stay prompt-enforced prose.
var forgeCLI = map[Forge]string{
	GitHub: "gh",
	GitLab: "glab",
}

// knownForges lists every Forge this build recognises, sorted for
// deterministic iteration and error messages — driven off forgeCLI's own
// keys, so adding a forge is one constant plus one forgeCLI entry, nothing
// else (ADR 0031).
func knownForges() []Forge {
	forges := make([]Forge, 0, len(forgeCLI))
	for f := range forgeCLI {
		forges = append(forges, f)
	}
	sort.Slice(forges, func(i, j int) bool { return forges[i] < forges[j] })
	return forges
}

// Grant returns the tool authority this hook holds against the active forge:
// the forge's own CLI plus whatever Tools declares — additive, never a
// replacement (ADR 0031 decision 4). Absent Tools returns exactly the active
// forge's CLI, today's implicit grant unchanged. An active forge with no
// known CLI contributes nothing rather than an empty-string entry — Classify
// reports that case as Broken before any lookPath call is made.
func (r Requires) Grant(active Forge) []string {
	grant := make([]string, 0, len(r.Tools)+1)
	if cli, ok := forgeCLI[active]; ok {
		grant = append(grant, cli)
	}
	grant = append(grant, r.Tools...)
	return grant
}

// Eligibility is a hook's boot-time classification against the active
// instance's declared preconditions (ADR 0031).
type Eligibility int

const (
	// Eligible: in scope for this instance and every declared precondition
	// holds.
	Eligible Eligibility = iota
	// Ineligible: the hook declares it does not apply here — a different
	// Forge, or the other forge's CLI named in Tools. Never a gate on this
	// instance at all; skipped and logged, both kinds.
	Ineligible
	// Broken: in scope for this instance but cannot run — a declared Tools
	// binary (or the harness itself) is missing from PATH. A Notifier
	// declines with a warning; a Screen refuses the boot.
	Broken
)

// ineligible reports whether r declares this hook does not apply to the
// active instance: a different Forge, or — ONLY when Forge is absent — the
// other forge's CLI named explicitly in Tools, self-describing without Forge
// spelled out (ADR 0031 decision 4). An explicit Forge that matches active is
// not a candidate for the Tools inference at all: the inference is a
// fallback for when Forge was never spelled out, never a veto over an
// explicit match — Requires{Forge: github, Tools: [glab]} on a GitHub
// instance is a legitimate mirror hook, eligible with glab in its grant.
// Returns the human reason for the boot log.
func (r Requires) ineligible(active Forge) (string, bool) {
	if r.Forge != "" {
		if r.Forge != active {
			return fmt.Sprintf("scoped to forge %q, this instance runs %q", r.Forge, active), true
		}
		return "", false
	}
	for _, forge := range knownForges() {
		if forge == active {
			continue
		}
		cli, ok := forgeCLI[forge]
		if !ok {
			continue
		}
		for _, tool := range r.Tools {
			if tool == cli {
				return fmt.Sprintf("Tools names %q, the CLI of forge %q", cli, forge), true
			}
		}
	}
	return "", false
}

// Classify decides this hook's Eligibility against the active instance:
// whether Requires scopes it elsewhere (Ineligible), or it is in scope but a
// needed binary is missing (Broken) — folding the harness-binary preflight
// (ADR 0024's checkHarnessBinaries) into the same mechanism, since a missing
// harness CLI is exactly the same fact as a missing Requires.Tools binary.
// lookPath is the injected PATH seam (the exec.LookPath precedent) so this is
// testable without real binaries. Returns the human reason for
// Ineligible/Broken; empty for Eligible.
//
// An active forge with no known CLI is itself Broken — checked first, so
// Grant's guarded lookup is never asked to resolve an empty binary name via
// lookPath.
func (s Spec) Classify(active Forge, lookPath func(string) (string, error)) (Eligibility, string) {
	if _, ok := forgeCLI[active]; !ok {
		return Broken, fmt.Sprintf("active forge %q has no known CLI", active)
	}
	if reason, ineligible := s.Requires.ineligible(active); ineligible {
		return Ineligible, reason
	}
	for _, tool := range s.Requires.Grant(active) {
		if _, err := lookPath(tool); err != nil {
			return Broken, fmt.Sprintf("required tool %q not on PATH: %v", tool, err)
		}
	}
	if _, err := lookPath(s.Harness); err != nil {
		return Broken, fmt.Sprintf("harness %q not on PATH: %v", s.Harness, err)
	}
	return Eligible, ""
}
