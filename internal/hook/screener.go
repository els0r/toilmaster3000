package hook

// ScreenInstance pairs a Screen's declarative Spec with the species
// implementation realizing it — the unit the screener consults and
// dispatches. Nothing instantiates species in this slice: production wiring
// passes none until the harness adapters land (ADR 0023); tests fake the
// Screen interface.
type ScreenInstance struct {
	Spec   Spec
	Screen Screen
}

// HoldDetail is one holding screen on a diverted PR: the screen's
// user-facing Name (queue reasons render screen:<name>) and the verdict's
// prose reason (the screen_holds field). Name, not ID — the human reads it.
type HoldDetail struct {
	Screen string
	Reason string
}

// Disposition is the level-triggered screening read for one PR at one head:
// which enabled screens are still awaiting a verdict and which hold. Both
// facts are reported — the caller's branch precedence decides (any hold
// diverts; else any pending parks the PR in Screening; else all proceed and
// the gated action may fire). Empty on both counts means proceed.
type Disposition struct {
	// Pending are the enabled screens with no verdict for the key — no row,
	// or an error latest row (a failed attempt is no verdict; the consult
	// re-dispatches it). A missing verdict is never proceed (ADR 0021).
	Pending []ScreenInstance
	// Holds carries EVERY screen whose latest row is a hold, in config order
	// (the reasons-list doctrine, ADR 0022).
	Holds []HoldDetail
}

// FoldVerdicts is the pure conjunctive fold of ADR 0022 — the screening
// sibling of AllGreen: it only judges, over the configured screens and a
// lookup of each screen's latest stored row for the PR-head under consult.
// Disabled screens never gate: they need no verdict and any stale hold of
// theirs is ignored.
func FoldVerdicts(screens []ScreenInstance, latest func(screenID string) (VerdictRecord, bool)) Disposition {
	var d Disposition
	for _, inst := range screens {
		if !inst.Spec.Enabled {
			continue
		}
		rec, ok := latest(inst.Spec.ID)
		if !ok || rec.Outcome == Errored {
			d.Pending = append(d.Pending, inst)
			continue
		}
		if rec.Outcome == Hold {
			d.Holds = append(d.Holds, HoldDetail{Screen: inst.Spec.Name, Reason: rec.Reason})
		}
	}
	return d
}
