package testharness

import (
	"context"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
)

// Recover is the sweep that looks a promotion's request up by branch, records
// it, and arms the merge.
func (f QueuedFixture) Recover(t *testing.T) []orchestrator.PublicationRecovery {
	t.Helper()
	recoveries, err := f.Reconciler(t).RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	return recoveries
}
