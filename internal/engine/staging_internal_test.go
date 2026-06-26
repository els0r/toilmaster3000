package engine

import (
	"log/slog"
	"testing"

	"github.com/els0r/toilmaster3000/internal/conventionalcommit"
	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/els0r/toilmaster3000/internal/rule"
	"github.com/stretchr/testify/require"
)

// TestEvaluateRulesStaging proves the Staging classification at its source: an
// eligible, parseable candidate that matches NO enabled rule yields empty
// reasons AND no Approve match — the exact fall-through the cycle records as
// Staging. This is the pure predicate the funnel's Staging bucket reuses; no new
// matching logic. The companion non-staging cases (a Review match, an Approve
// match) confirm the classifier is the discriminator, not a constant.
func TestEvaluateRulesStaging(t *testing.T) {
	logger := slog.Default()
	c, parsedOK := conventionalcommit.Parse("chore(infra): rotate token")
	require.True(t, parsedOK)
	pr := github.PR{Number: 1, Title: "chore(infra): rotate token", Author: "carol"}

	t.Run("no rule matches an eligible parseable PR -> staging", func(t *testing.T) {
		rules := []rule.Rule{
			{Name: "feat approver", Class: "approve", Enabled: true, TypeInclude: "^feat$"},
			{Name: "big-diff review", Class: "review", Enabled: true, TypeInclude: "^docs$"},
		}
		reasons, _, approveMatched := evaluateRules(logger, rules, c, parsedOK, pr, "me")
		require.Empty(t, reasons, "no Review Rule matched -> no reasons")
		require.False(t, approveMatched, "no Approve Rule matched -> staging, not approved")
	})

	t.Run("a matching Approve Rule is not staging", func(t *testing.T) {
		rules := []rule.Rule{{Name: "chore approver", Class: "approve", Enabled: true, TypeInclude: "^chore$"}}
		reasons, _, approveMatched := evaluateRules(logger, rules, c, parsedOK, pr, "me")
		require.Empty(t, reasons)
		require.True(t, approveMatched, "an Approve match leaves Staging")
	})

	t.Run("a matching Review Rule is not staging", func(t *testing.T) {
		rules := []rule.Rule{{Name: "chore gate", Class: "review", Enabled: true, TypeInclude: "^chore$"}}
		reasons, _, _ := evaluateRules(logger, rules, c, parsedOK, pr, "me")
		require.Equal(t, []string{"chore gate"}, reasons, "a Review match queues, not staging")
	})
}
