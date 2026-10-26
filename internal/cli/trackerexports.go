package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Maintenance runs through reconcile, which the supervisor already schedules.
// Removal failures and record failures are reported without skipping the rest
// of reconciliation. Partial removals are recorded even when a later one fails.
func maintainTrackerExports(ctx context.Context, parts components) (beads.ExportCleanup, error) {
	store, err := runstate.NewTrackerExportCleanupStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return beads.ExportCleanup{}, err
	}
	cleaned, cleanErr := beads.CleanExportTemporaries(ctx, parts.repository, parts.runner, time.Now())
	recordErr := store.Record(cleaned)
	if recordErr != nil {
		recordErr = fmt.Errorf("record removed tracker export temporaries: %w", recordErr)
	}
	refreshErr := refreshTrackerExport(parts.tracker().RefreshExportIfDue(ctx))
	return cleaned, errors.Join(cleanErr, recordErr, refreshErr)
}

// refreshTrackerExport is the clean-up's failure to refresh the tracker
// export, said with its cause first where the export ran out of time: whether
// it waited behind other processes or the tracker was slow on its own, and how
// big the store was. The failure stays a failure either way; what bd said and
// the whole account follow it.
func refreshTrackerExport(err error) error {
	if err == nil {
		return nil
	}
	if slow, ok := beads.SlowCallOf(err); ok {
		return fmt.Errorf("refresh the tracker export: %s: %w", slow.Cause(), err)
	}
	return fmt.Errorf("refresh the tracker export: %w", err)
}
