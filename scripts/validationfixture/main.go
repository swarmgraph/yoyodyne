// Command validationfixture writes the run records the validation project
// (scripts/validate-machine-home.sh) needs and cannot get from a provider: a
// run in flight, the same run ended, and a run whose worktree an earlier build
// kept. It writes them through the harness's own run store, so a record it
// makes is one the harness reads exactly as it reads its own, and it reads them
// back the same way.
//
//	go run ./scripts/validationfixture run <home> <product> [<worktree path> <branch> <base commit>]
//	go run ./scripts/validationfixture end <home> <product> <run id>
//	go run ./scripts/validationfixture hold <home> <product> <run id> <stop file> <seconds>
//	go run ./scripts/validationfixture show <home> <product> <run id>
//
// run records a run in flight and prints its id; a worktree is recorded with
// the branch and commit it was cut at, as a run's own record carries them. end records it cancelled.
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

const usage = "usage: validationfixture run|end|hold|show <home> <product> ..."

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
		switch len(args) {
		case 3:
			return startRun(store, product, "", "", "")
		case 6:
			return startRun(store, product, args[3], args[4], args[5])
		default:
			return errors.New(usage)
		}
	case "end":
		if len(args) != 4 {
			return errors.New(usage)
		}
		return endRun(store, args[3])
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

func startRun(store *runstate.Store, product, worktree, branch, base string) error {
	runID, err := runstate.NewRunID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: domain.ProductID(product),
		RepositoryID: product, WorkItemID: product + "-validation", Backend: domain.BackendClaudeCode,
		Status: runstate.StatusRunning, StartedAt: now, UpdatedAt: now, WorktreePath: worktree, Branch: branch, BaseCommit: base,
	}); err != nil {
		return err
	}
	fmt.Println(runID)
	return nil
}

func endRun(store *runstate.Store, runID string) error {
	state, err := store.Load(runID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	state.Status = runstate.StatusCancelled
	state.UpdatedAt = now
	state.CompletedAt = &now
	return store.Save(state)
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
