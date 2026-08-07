package server

import (
	"testing"

	"github.com/els0r/toilmaster3000/internal/engine"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// TestOutboundToBodyCoversEveryStage is the guard on the one place stages are
// still named one by one: the frozen wire mapping (ADR 0002, kept explicit by
// ADR 0025). Driven by github.OutboundStages(), it puts one uniquely-numbered
// item in every stage of the engine partition and asserts each stage reaches
// BOTH its named list field and its named distribution count.
//
// A stage added to the classifier but forgotten in outboundToBody fails here.
// Nothing else catches it: openapi.json stays byte-identical whether or not a
// DTO field is missing, so `make check` reports clean drift on the exact
// mistake this test exists to find.
func TestOutboundToBodyCoversEveryStage(t *testing.T) {
	stages := github.OutboundStages()

	ob := engine.Outbound{}
	wantNumber := map[github.OutboundStage]int{}
	for i, stage := range stages {
		number := 100 + i
		wantNumber[stage] = number
		ob[stage] = []engine.OutboundItem{{
			Number: number, Title: "feat(x): a row", Author: "me", URL: "u",
		}}
	}

	body := outboundToBody(ob, map[int]bool{})

	// The named wire fields, bound to the stage each is contracted to carry.
	lists := map[github.OutboundStage][]OutboundItem{
		github.OutboundStageDraft:            body.Draft,
		github.OutboundStageRed:              body.Red,
		github.OutboundStageRunning:          body.Running,
		github.OutboundStageChangesRequested: body.ChangesRequested,
		github.OutboundStageAwaitingApproval: body.AwaitingApproval,
		github.OutboundStageInDiscussion:     body.InDiscussion,
		github.OutboundStageReady:            body.Ready,
	}
	counts := map[github.OutboundStage]int{
		github.OutboundStageDraft:            body.Distribution.Draft,
		github.OutboundStageRed:              body.Distribution.Red,
		github.OutboundStageRunning:          body.Distribution.Running,
		github.OutboundStageChangesRequested: body.Distribution.ChangesRequested,
		github.OutboundStageAwaitingApproval: body.Distribution.AwaitingApproval,
		github.OutboundStageInDiscussion:     body.Distribution.InDiscussion,
		github.OutboundStageReady:            body.Distribution.Ready,
	}

	for _, stage := range stages {
		items, mapped := lists[stage]
		require.True(t, mapped, "stage %q reaches no wire list field", stage)
		require.Len(t, items, 1, "stage %q's wire list holds its one item", stage)
		require.Equal(t, wantNumber[stage], items[0].Number,
			"stage %q's wire list holds ITS item, not another stage's", stage)

		count, counted := counts[stage]
		require.True(t, counted, "stage %q reaches no distribution count", stage)
		require.Equal(t, 1, count, "stage %q's distribution count is its list length", stage)
	}

	require.Equal(t, len(stages), body.Outgoing,
		"outgoing is the derived partition sum, so every stage's item is counted exactly once")
}
