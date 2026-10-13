package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func TestSweepsSayWhenTheProviderCompactedAPass(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 12, 15, 40, 0, 0, time.UTC)
	rendered := renderSweep(runstate.Sweep{
		Task: "architect-pass", Role: domain.RoleArchitect, StartedAt: at, Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "Took the addendum."},
		Compactions: []backend.ProviderCompaction{
			{At: at.Add(5 * time.Minute), Trigger: "auto", BeforeTokens: 250_000, AfterTokens: 80_000},
			{At: at.Add(9 * time.Minute), BeforeTokens: 240_000},
			{At: at.Add(12 * time.Minute)},
		},
	})
	for _, want := range []string{
		"the provider compacted the session at 2026-10-12T15:45:00Z, from 250000 tokens to 80000 (its reason: auto)",
		"the provider compacted the session at 2026-10-12T15:49:00Z, from 240000 tokens; it did not say how large the session was after",
		"the provider compacted the session at 2026-10-12T15:52:00Z; it reported no sizes",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered = %q, want %q", rendered, want)
		}
	}
	if quiet := renderSweep(runstate.Sweep{Task: "architect-pass", Role: domain.RoleArchitect, Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "x"}}); strings.Contains(quiet, "compacted") {
		t.Errorf("a pass nothing compacted says %q", quiet)
	}
}
