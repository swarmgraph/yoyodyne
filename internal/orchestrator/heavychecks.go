package orchestrator

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// HeavyCheckPermits is the check runner's way to the record of places for the
// machine's heavy checks (runstate's heavycheck.go): a command the project's
// execution.heavy_checks names asks the record for a place and waits for it,
// and any other command is launched at once. It is what the runner is wired
// with only where the project names a heavy check, so a project that names
// none never opens the record.
type HeavyCheckPermits struct {
	Store     *runstate.HeavyCheckStore
	Product   domain.ProductID
	Execution config.Execution
}

// Acquire asks for a place for a heavy command, waiting for it.
func (h HeavyCheckPermits) Acquire(ctx context.Context, ask checks.PermitAsk, waiting func(check string, since time.Time)) (checks.Permit, error) {
	name, limit, heavy := h.Execution.HeavyCheck(ask.Command)
	if !heavy || h.Store == nil {
		return nil, nil
	}
	priority := ask.Priority
	if priority == "" {
		priority = domain.HeavyCheckOrdinary
	}
	grant, err := h.Store.Acquire(ctx, runstate.HeavyCheckAsk{
		Product: string(h.Product), RunID: ask.RunID, WorkItemID: ask.WorkItemID, Commit: ask.Commit,
		Check: name, Command: ask.Command, Limit: limit, Priority: priority,
	}, func(permit runstate.HeavyCheckPermit) {
		if waiting != nil {
			waiting(permit.Check, permit.AskedAt)
		}
	})
	if err != nil {
		return nil, err
	}
	return heavyCheckPermit{grant: grant}, nil
}

// heavyCheckPermit is one granted place, as the runner holds it.
type heavyCheckPermit struct {
	grant *runstate.HeavyCheckGrant
}

func (p heavyCheckPermit) Check() string         { return p.grant.Permit.Check }
func (p heavyCheckPermit) Waited() time.Duration { return p.grant.Waited }
func (p heavyCheckPermit) Hold() *os.File        { return p.grant.Hold() }
func (p heavyCheckPermit) Register(process execution.StartedProcess) error {
	return p.grant.Register(process)
}

// Finish records how the check ended, in the words a process result is read
// in, and gives its place back once its processes are gone.
func (p heavyCheckPermit) Finish(result execution.ProcessResult) error {
	ended := "not started"
	if result.Status != "" {
		ended = fmt.Sprintf("%s, exit code %d", result.Status, result.ExitCode)
	}
	_, err := p.grant.Finish(ended)
	return err
}

// heavyCheckPriority is the order this run's gate checks wait for places in: a
// replay onto a target branch that moved is checked for a promotion and goes
// first; a run continuing work an earlier attempt preserved — a decided
// repair, or a re-run holding that work — goes next; anything else goes last.
func heavyCheckPriority(state runstate.State) domain.HeavyCheckPriority {
	switch {
	case state.IntegrationRetries > 0:
		return domain.HeavyCheckForLanding
	case state.LiftedCommit != "" || len(state.RepairContinuations) > 0:
		return domain.HeavyCheckForPreservedWork
	}
	return domain.HeavyCheckOrdinary
}
