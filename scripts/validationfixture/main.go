// Command validationfixture writes the run records the validation project
// (scripts/validate-machine-home.sh) needs and cannot get from a provider: a
// run in flight, and a run that had already ended, whose worktree an earlier
// build kept. It creates them through the harness's own run store, so a record
// it makes is one the harness reads exactly as it reads its own, and it reads
// them back the same way. It never changes a record once made: a run moves
// between states only through the run store's own steps.
//
//	go run ./scripts/validationfixture run <home> <product>
//	go run ./scripts/validationfixture ended <home> <product> <worktree path> <branch> <base commit>
//	go run ./scripts/validationfixture hold <home> <product> <run id> <stop file> <seconds>
//	go run ./scripts/validationfixture show <home> <product> <run id>
//
// run records a run in flight and prints its id. ended records a run that had
// already ended, cancelled, with the worktree it kept and the branch and commit
// that worktree was cut at, as a run's own record carries them, and prints its
// id.
// hold takes the run's lease, the one `yoyo home migrate` reads to tell a run
// in flight, prints "held", and keeps it until the stop file exists or the
// seconds have passed, whichever comes first, so a script that dies before it
// creates the stop file still leaves nothing running. show prints the run's
// worktree path and the path a migration note kept beside it, one per line.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const usage = "usage: validationfixture run|ended|hold|show <home> <product> ..."

func main() {
	if err := fixture(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "validationfixture:", err)
		os.Exit(1)
	}
}

func fixture(args []string) error {
	if len(args) < 3 {
		return errors.New(usage)
	}
	verb, root, product := args[0], args[1], args[2]
	store, err := runstate.NewStore(root, domain.ProductID(product))
	if err != nil {
		return err
	}
	switch verb {
	case "run":
		if len(args) != 3 {
			return errors.New(usage)
		}
		return createRun(store, product, runstate.StatusRunning, "", "", "")
	case "ended":
		if len(args) != 6 {
			return errors.New(usage)
		}
		return createRun(store, product, runstate.StatusCancelled, args[3], args[4], args[5])
	case "hold":
		if len(args) != 6 {
			return errors.New(usage)
		}
		seconds, err := strconv.Atoi(args[5])
		if err != nil || seconds <= 0 {
			return fmt.Errorf("hold takes a whole number of seconds, not %q", args[5])
		}
		return holdRun(root, product, args[3], args[4], time.Duration(seconds)*time.Second)
	case "show":
		if len(args) != 4 {
			return errors.New(usage)
		}
		state, err := store.Load(args[3])
		if err != nil {
			return err
		}
		kept := ""
		if state.HomeMigration != nil {
			kept = state.HomeMigration.FromWorktreePath
		}
		fmt.Println(state.WorktreePath)
		fmt.Println(kept)
		return nil
	default:
		return errors.New(usage)
	}
}

func createRun(store *runstate.Store, product string, status runstate.Status, worktree, branch, base string) error {
	runID, err := runstate.NewRunID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: domain.ProductID(product),
		RepositoryID: product, WorkItemID: product + "-validation", Backend: domain.BackendClaudeCode,
		Status: status, StartedAt: now, UpdatedAt: now, WorktreePath: worktree, Branch: branch, BaseCommit: base,
	}
	if status.Terminal() {
		state.CompletedAt = &now
	}
	if err := store.Create(state); err != nil {
		return err
	}
	fmt.Println(runID)
	return nil
}

// holdRun keeps the run's lease, which is what a run's own process holds for as
// long as it runs, until the stop file appears or the bound passes.
func holdRun(root, product, runID, stop string, bound time.Duration) error {
	target := filepath.Join(home.ProductDirectory(root, product), "runs", runID+".lease")
	lease, taken, err := runstate.TryLeasePath(target, "the validation project's run in flight")
	if err != nil {
		return err
	}
	if !taken {
		return fmt.Errorf("the lease %s is already held", target)
	}
	defer lease.Release()
	fmt.Println("held")
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stop); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
