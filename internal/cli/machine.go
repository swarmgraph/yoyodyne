package cli

import (
	"context"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/supervise"
)

func machineAvailability(parts components) func(time.Time, time.Time, string) readmodel.GapCause {
	store, err := runstate.NewSupervisionStore(parts.stateRoot, parts.config.Product.ID)
	return func(from, to time.Time, task string) readmodel.GapCause {
		if err != nil {
			return readmodel.GapCause{Problem: "machine observations could not be opened: " + err.Error(), Checked: true}
		}
		availability := readmodel.ReadWatchAvailability(readmodel.Sources{
			Machine: freshMachine{SupervisionStore: store, runner: parts.runner}, Sessions: parts.watch, Sweeps: parts.store.Sweeps(), Now: func() time.Time { return to },
			RecurringPassesAtOnce: orchestrator.MaxConcurrentFirings,
		})
		return availability.Cause(from, to, task)
	}
}

// A diagnosis reads fresh OS evidence because a resumed scheduler may reach
// a miss before the supervisor's next minute has imported the wake.
type freshMachine struct {
	*runstate.SupervisionStore
	runner execution.ProcessRunner
}

func (m freshMachine) PowerHistory() ([]runstate.PowerEvent, error) {
	return supervise.ReadPowerHistory(context.Background(), m.runner)
}
