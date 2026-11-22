package cli

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every path that takes a run up again outside a pipeline is handed the
// developer slots' endpoint pairs, so a run whose own slot is held takes a free
// one ahead of fresh work rather than wait for its own
// (orchestrator.SlotPairsFor). A path built without them leaves such a run
// waiting while the free slot goes to something new.
func TestEveryTakeUpOfAKeptRunIsGivenTheSlotsPairs(t *testing.T) {
	t.Parallel()

	// The constructors read nothing of the configuration that this check needs,
	// so it is built here rather than read from this checkout.
	cfg := config.Config{Product: config.Product{ID: "yoyodyne"}}
	cfg.Execution.MaxConcurrentDevelopers = 2
	store, err := runstate.NewStore(t.TempDir(), cfg.Product.ID)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	parts := components{config: cfg, store: store}
	handback, ok := mergeQueueDriverFrom(parts).Promoter.Repair.(orchestrator.MergeQueueRunHandback)
	if !ok {
		t.Fatal("the merge queue's hand-back is not a MergeQueueRunHandback")
	}
	for name, pairs := range map[string]runstate.SlotPairs{
		"decided repair":                 repairContinuerFrom(parts).SlotPairs,
		"continuation after a stall":     stallContinuerFrom(parts).SlotPairs,
		"continuation at the checks":     checkStageContinuerFrom(parts).SlotPairs,
		"integration resumption":         integrationResumerFrom(parts).SlotPairs,
		"take-up after GitHub came back": gitHubIdentityStopsFrom(parts).SlotPairs,
		"reconcile sweep's put-backs":    reconcilerFrom(parts).SlotPairs,
		"merge queue's catch-up":         handback.SlotPairs,
	} {
		if pairs == nil {
			t.Errorf("the %s is built without the developer slots' pairs", name)
		}
	}
}
