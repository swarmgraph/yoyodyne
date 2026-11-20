package orchestrator

// A run is attributable to the account that served it.
//
// The pieces of this are tested where they live — the pool's rotation and
// budgets in internal/config, the spend and the cursor in internal/runstate, the
// environment on the command in internal/backend/claudecode — and none of that
// says the wiring holds. What makes a run attributable is that one account is
// chosen once, written onto the record, and then read back off that record by
// every invocation the run goes on to make: the first developer attempt, each
// repair, and the review of the change. A pipeline that chose again per
// invocation would pass every one of those other tests and still split one piece
// of work across two subscriptions.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// testStateRoot is where a pooled account's provider home would be, for a
// pipeline that runs nothing. Nothing is written there: what is under test is
// which directory reaches the provider, and the fake provider is what receives
// it. A pipeline that runs a developer keeps the state directory its test
// fixture made, where the run's scratch directory is cut, and its accounts'
// homes are named under that one instead.
const testStateRoot = "/state"

func TestAPooledRunIsAttributableToTheAccountThatServedIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)
	// The pool is asked once and answers with the second account, so a run that
	// fell back to the configuration's own answer would be visibly the wrong one:
	// a pooled configuration names no single account.
	served := config.AccountEndpoint{Alias: "two", Directory: filepath.Join(pipeline.StateRoot, "accounts", "two")}
	pipeline.Accounts = fixedAccountChooser{endpoint: served}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != served.Alias {
		t.Fatalf("recorded account = %q, want the %q the pool chose", state.AccountAlias, served.Alias)
	}

	// The first verdict sends the change back, so this run made two developer
	// attempts and obtained two verdicts. Both halves matter: a repair is where a
	// second choice would be taken, and the review is the invocation belonging to
	// a different role.
	if attempts := provider.RequestsForRole(domain.RoleDeveloper); len(attempts) < 2 {
		t.Fatalf("the run made %d developer attempt(s), want the first and its repair", len(attempts))
	}
	if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) < 2 {
		t.Fatalf("the run obtained %d verdict(s), want one per attempt", len(reviews))
	}
	for index, request := range provider.Requests {
		if request.AccountAlias != served.Alias || request.AccountConfigDir != served.Directory {
			t.Fatalf("invocation %d (%s) ran under %q at %q, want the run's own %q at %q",
				index, request.Role, request.AccountAlias, request.AccountConfigDir, served.Alias, served.Directory)
		}
	}
}

// Where the alias authenticates is resolved from the configuration rather than
// carried on the record, so the record stays a name and the directory stays this
// machine's business.
func TestTheHomeAnInvocationRunsInIsResolvedFromTheRecordedAlias(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)
	pipeline.Accounts = fixedAccountChooser{endpoint: config.AccountEndpoint{Alias: "one"}}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != "one" {
		t.Fatalf("recorded account = %q, want one", state.AccountAlias)
	}
	// The chooser named no directory at all, and every invocation still ran in the
	// home that alias authenticates in — which is the whole of what "resolved from
	// the alias" means.
	want := filepath.Join(pipeline.StateRoot, "accounts", "one")
	if len(provider.Requests) == 0 {
		t.Fatal("the run made no provider invocation")
	}
	for _, request := range provider.Requests {
		if request.AccountConfigDir != want {
			t.Fatalf("invocation ran in %q, want the home %q authenticates in", request.AccountConfigDir, want)
		}
	}
}

// A pooled configuration names no single account, so a pipeline with no pool
// wired has nothing to attribute a run to. It refuses before the work item is
// claimed and before a provider is paid, rather than starting a run nothing
// could bill afterwards.
func TestAPooledConfigurationWithNoPoolRefusesBeforeAnythingIsClaimed(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = pooled(pipeline)

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("Run() error = nil, want a pooled configuration with nothing to choose with refused")
	}
	if tracker.Claimed {
		t.Fatal("the work item was claimed by a run that could not say which account would serve it")
	}
	if len(provider.Requests) != 0 {
		t.Fatalf("a run that could not choose an account still made %d provider invocation(s)", len(provider.Requests))
	}
}

// A project with one account has no pool wired and needs none: the configuration
// itself answers, and every invocation runs where the machine is already signed
// in. This is the case every existing installation is in, and nothing about it
// changes because pooling exists.
func TestAnUnpooledRunNamesItsAccountAndImposesNoProviderHome(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Work", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Accounts = map[string]config.Account{"work": {}}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.AccountAlias != "work" {
		t.Fatalf("recorded account = %q, want the project's single account", state.AccountAlias)
	}
	for _, request := range provider.Requests {
		if request.AccountConfigDir != "" {
			t.Fatalf("a single-account run was pointed at %q, want the machine's own provider home", request.AccountConfigDir)
		}
	}
}

// Simultaneous starts are the arrangement pooling exists for, so the pool must
// not double-serve under it. The cursor is read from the run records and moved
// by the record a start reserves, and until those were one step two runs
// beginning in the same moment read the same cursor and were both sent to the
// same account.
//
// Three starts rather than two, because the third is what catches a rotation
// that serializes the choosing and then dates the records in a different order:
// with the cursor on a run somebody has already rotated past, the account behind
// it is handed out twice.
func TestSimultaneousStartsAreServedByDistinctAccounts(t *testing.T) {
	t.Parallel()

	const starts = 3
	stateRoot := t.TempDir()
	cfg := config.Config{
		Accounts: map[string]config.Account{"one": {}, "two": {}, "three": {}},
		// Every one of these starts reserves a run and none of them ends, so the
		// capacity has to admit all of them or the pool would be exonerated by a
		// refusal rather than by choosing well.
		Execution: config.Execution{MaxConcurrentDevelopers: starts},
	}

	// Each start gets a store of its own over the same durable records, which is
	// what a second process holds.
	pipelines := make([]Pipeline, starts)
	for index := range pipelines {
		store, err := runstate.NewStore(stateRoot, "yoyodyne")
		if err != nil {
			t.Fatalf("runstate.NewStore() error = %v", err)
		}
		pipelines[index] = Pipeline{Store: store, Config: cfg, StateRoot: testStateRoot,
			Accounts: rotatingPool{config: cfg, stateRoot: testStateRoot, runs: store}}
	}

	aliases := make([]string, starts)
	leases := make([]*runstate.Lease, starts)
	failures := make([]error, starts)
	t.Cleanup(func() {
		for _, lease := range leases {
			lease.Release()
		}
	})
	// The starts are released together so they are contending rather than merely
	// consecutive: what is under test is what happens when the cursor is read by
	// more than one of them before any of them has written it.
	begin := make(chan struct{})
	var done sync.WaitGroup
	done.Add(starts)
	for index := range starts {
		go func(index int) {
			defer done.Done()
			<-begin
			recorded, lease, err := pipelines[index].reserveRun(context.Background(), pendingRun(index))
			if err != nil {
				failures[index] = err
				return
			}
			aliases[index], leases[index] = recorded.AccountAlias, lease
		}(index)
	}
	close(begin)
	done.Wait()
	for index, err := range failures {
		if err != nil {
			t.Fatalf("start %d: %v", index, err)
		}
	}

	served := make(map[string]int, starts)
	for _, alias := range aliases {
		served[alias]++
	}
	if len(served) != starts {
		t.Fatalf("%d simultaneous starts were served by %v, want one account each from a pool that has %d", starts, served, starts)
	}
	// And the record each of them reserved names the account it was served by,
	// because the cursor the next start reads is that record and nothing else.
	records, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	for index, alias := range aliases {
		recorded, err := records.Load(runIDFor(index))
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if recorded.AccountAlias != alias {
			t.Fatalf("start %d was served by %q and its record says %q", index, alias, recorded.AccountAlias)
		}
	}
}

// pendingRun is one start's run as it has been built and before the reservation
// dates it and names the account it spends, which is exactly the state
// reserveRun is handed. Each start is a different work item, because two runs of
// one item is what a reservation refuses whatever the pool decided.
func pendingRun(index int) runstate.State {
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		// The run ids are derived from the index rather than generated so a record
		// can be read back by the start that reserved it; what they are is
		// otherwise immaterial.
		RunID:         runIDFor(index),
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    fmt.Sprintf("yoyodyne-task-%d", index),
		WorkItemTitle: "Work",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusPending,
	}
}

func runIDFor(index int) string { return fmt.Sprintf("run-%032x", index+1) }

// rotatingPool is the join the harness wires: the configuration says which
// accounts there are and the run records say which one was served last. What the
// real pool adds to it — the weekly budgets, and the week of spend they are read
// against — is tested where it is implemented, and adds nothing to the cursor
// this is about.
type rotatingPool struct {
	config    config.Config
	stateRoot string
	runs      *runstate.Store
}

func (p rotatingPool) ChooseAccount() (config.AccountEndpoint, error) {
	lastServed, err := p.runs.LastAccountAlias()
	if err != nil {
		return config.AccountEndpoint{}, err
	}
	return p.config.ChooseAccount(p.stateRoot, lastServed, nil)
}

// pooled turns a test pipeline into one whose project runs across two accounts,
// which is the arrangement in which the configuration itself no longer answers
// which account a run spent.
func pooled(pipeline Pipeline) Pipeline {
	pipeline.Config.Accounts = map[string]config.Account{"one": {}, "two": {}}
	return pipeline
}

// fixedAccountChooser is a pool that has already decided. What a real one weighs
// — the rotation, the budgets, the run records behind both — is tested where it
// is implemented; what is under test here is that the pipeline asks once and
// then stops asking.
type fixedAccountChooser struct {
	endpoint config.AccountEndpoint
}

func (c fixedAccountChooser) ChooseAccount() (config.AccountEndpoint, error) {
	return c.endpoint, nil
}
