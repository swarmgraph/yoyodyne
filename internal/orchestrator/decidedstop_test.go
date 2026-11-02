package orchestrator

// A stop the development manager decided about a run in flight. It is the
// operator's stop made on her behalf, so the run stops exactly as it does for
// theirs; what differs is who the record names and the docket entry it leaves.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decision is recorded while the developer is working, in the shape her
// conversation records it: the stop on the item's triage record, and the request
// beside the run naming her. The run stops at its next boundary with its change
// preserved, frees its slot, says who stopped it and why, and is docketed as a
// stoppage already decided rather than one waiting on her.
func TestAStopTheDevelopmentManagerDecidedEndsTheRunInFlightAsDecided(t *testing.T) {
	t.Parallel()

	const decidedBy = "the development manager in conversation chat-0123456789abcdef"
	const reason = "superseded: the dashboard rewrite does this work (superseded by yoyodyne-ifd.398)"
	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: docketedItem, Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if _, err := store.Triage().RecordDecision(context.Background(), docketedItem, runstate.TriageDecision{
			Decision:     runstate.TriageDecisionStop,
			RunID:        request.RunID,
			Reason:       "superseded: the dashboard rewrite does this work",
			SupersededBy: "yoyodyne-ifd.398",
			DecidedBy:    "development manager",
			Conversation: "chat-0123456789abcdef",
			Turn:         7,
		}, baseTime); err != nil {
			return err
		}
		if err := store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    docketedItem,
			RequestedAt:   baseTime,
			Reason:        reason,
			RequestedBy:   decidedBy,
			Decision:      runstate.TriageDecisionStop,
		}); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the stop reported as what ended the run")
	}
	// The stop: the run ended cancelled at the review boundary, before a verdict
	// was bought on a change nobody is going to take, and promoted nothing.
	if !strings.Contains(err.Error(), decidedBy+" stopped this run") || !strings.Contains(err.Error(), reason) {
		t.Fatalf("Run() error = %v, want it to name who stopped the run and why", err)
	}
	if outcome.Status != runstate.StatusCancelled || outcome.StopClass != runstate.StopManager {
		t.Fatalf("status = %q, want the stopped run recorded as cancelled", outcome.Status)
	}
	if reviews := countRoleRequests(provider.Requests, "reviewer"); reviews != 0 {
		t.Fatalf("reviewer invocations = %d, want the stop to have landed before the review", reviews)
	}
	if outcome.Integration != nil || tracker.Closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.Closed)
	}

	// The preserved change: the branch and the worktree are where an operator's
	// stop leaves them, and the branch carries what the developer wrote.
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.StopClass != runstate.StopManager {
		t.Fatalf("saved stop cause = %q, want manager-stop", stopped.StopClass)
	}
	if stopped.WorktreeRemoved || stopped.BranchRemoved {
		t.Fatalf("stopped state = %#v, want the artifacts preserved", stopped)
	}
	if _, err := os.Stat(filepath.Join(stopped.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the stopped run's change did not survive in its worktree: %v", err)
	}
	if listed := gitOutput(t, repository, "branch", "--list", stopped.Branch); !strings.Contains(listed, stopped.Branch) {
		t.Fatalf("branch %s is gone, want it preserved", stopped.Branch)
	}
	if shown := gitOutput(t, repository, "show", stopped.Branch+":feature.txt"); !strings.Contains(shown, "implemented") {
		t.Fatalf("branch %s does not carry the change: %q", stopped.Branch, shown)
	}

	// The freed slot: a terminal record is not in flight, and nothing is left
	// incomplete to hold a developer slot or the race guard.
	if stopped.Status.InFlight() || stopped.CompletedAt == nil {
		t.Fatalf("stopped state = %#v, want a terminal run holding no slot", stopped)
	}
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("runs in flight = %d, want the stopped run's slot free", len(incomplete))
	}

	// The record: the run and the item both say who stopped it and why.
	if !strings.Contains(stopped.Failure, decidedBy+" stopped this run") || !strings.Contains(stopped.Failure, "yoyodyne-ifd.398") {
		t.Fatalf("run failure = %q, want who stopped it and the superseding item", stopped.Failure)
	}
	if !strings.Contains(tracker.Notes, decidedBy+" stopped this run") || !strings.Contains(tracker.Notes, "superseded by yoyodyne-ifd.398") {
		t.Fatalf("the work item was not told who stopped it and why: %q", tracker.Notes)
	}
	counters, err := store.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	decision, decided := counters.DecisionOf(outcome.RunID)
	if !decided || decision.Decision != runstate.TriageDecisionStop || decision.SupersededBy != "yoyodyne-ifd.398" {
		t.Fatalf("decision = %#v (decided=%t), want the stop on the item's triage record", decision, decided)
	}
	if counters.AwaitingCarryOut(outcome.RunID) {
		t.Fatal("the stop reads as a decision the harness has still to carry out")
	}

	// The docket: one entry for the stopped run, closed by her decision, so the
	// docket reads it as decided and puts nothing to her.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Class != triage.ClassStoppedRun || entries[0].RunID != outcome.RunID || entries[0].StopClass != runstate.StopManager.Name() {
		t.Fatalf("docket = %#v, want the stopped run docketed once", entries)
	}
	closure := entries[0].Closed
	if closure == nil || closure.Decision != runstate.TriageDecisionStop || closure.DecidedBy != decidedBy || !strings.Contains(closure.Reason, "yoyodyne-ifd.398") {
		t.Fatalf("closure = %#v, want the entry settled by her stop", closure)
	}
	if !strings.Contains(entries[0].Failure, decidedBy+" stopped this run") {
		t.Fatalf("entry failure = %q, want the entry to say who stopped the run", entries[0].Failure)
	}
	built, err := pipeline.Docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 0 || built.Closed != 1 {
		t.Fatalf("docket build = %d open, %d closed; want the stop read as decided and nothing awaiting her", len(built.Entries), built.Closed)
	}
	// The entry says the item is superseded and by what, and the stop is what
	// ended the run rather than a death nobody classified.
	stop := entries[0].StopRequested
	if stop == nil || !stop.Landed || stop.SupersededBy != "yoyodyne-ifd.398" || stop.By != decidedBy {
		t.Fatalf("stop on the entry = %#v, want the stop that landed, naming the superseding item", stop)
	}
	rendered := entries[0].Render()
	if !strings.Contains(rendered, "superseded by yoyodyne-ifd.398") || strings.Contains(rendered, "Died holding its change") {
		t.Fatalf("rendered entry does not say the item is superseded and by what:\n%s", rendered)
	}

	// The hold: the item is not pulled while the stopped run's change stands,
	// though the run ended cancelled with no blocker, and the hold says why.
	held, err := readmodel.HeldForAPerson(context.Background(), store, store.Triage(), nil, nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	holdReason, holding := held.Reason(docketedItem)
	if !holding || !strings.Contains(holdReason, "superseded by yoyodyne-ifd.398") || !strings.Contains(holdReason, outcome.RunID) {
		t.Fatalf("hold = %q (held=%t), want the superseded item held while its preserved change stands", holdReason, holding)
	}
	if held.Decided(docketedItem) {
		t.Fatal("the hold names the harness as the next mover on a stop that leaves it nothing to carry out")
	}
}

// A stop asked of a run that passed its last boundary before reading it decides
// nothing about the stoppage the run reached instead. The request is written
// while the reviewer is giving its last verdict, the verdict fails the run on
// its repair budget, and what that leaves is a fresh, undecided entry naming both
// the stop that was asked and the review that actually stopped the run.
func TestAStopTheRunNeverReachedLeavesItsLaterStoppageUndecided(t *testing.T) {
	t.Parallel()

	const decidedBy = "the development manager in conversation chat-0123456789abcdef"
	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: docketedItem, Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	// The second review is the run's last provider call: the stop is decided and
	// requested while it is streaming, so no boundary is left to read it at.
	reviews := 0
	respond := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviews++
			if reviews == 2 {
				if _, err := store.Triage().RecordDecision(context.Background(), docketedItem, runstate.TriageDecision{
					Decision:     runstate.TriageDecisionStop,
					RunID:        request.RunID,
					Reason:       "superseded: the dashboard rewrite does this work",
					SupersededBy: "yoyodyne-ifd.398",
					DecidedBy:    "development manager",
					Conversation: "chat-0123456789abcdef",
					Turn:         7,
				}, baseTime); err != nil {
					return backend.RunResult{}, err
				}
				if err := store.RecordStop(runstate.StopRequest{
					SchemaVersion: runstate.StopSchemaVersion,
					ProductID:     "yoyodyne",
					RunID:         request.RunID,
					WorkItemID:    docketedItem,
					RequestedAt:   baseTime,
					Reason:        "superseded: the dashboard rewrite does this work (superseded by yoyodyne-ifd.398)",
					RequestedBy:   decidedBy,
					Decision:      runstate.TriageDecisionStop,
				}); err != nil {
					return backend.RunResult{}, err
				}
			}
		}
		return respond(request)
	}

	outcome, runErr := pipeline.Run(context.Background(), tracker.Item.ID)
	if runErr == nil || !outcome.Blocked || outcome.Status == runstate.StatusCancelled {
		t.Fatalf("Run() error = %v, blocked = %t, status = %q; want the failed review to have stopped the run", runErr, outcome.Blocked, outcome.Status)
	}
	if reviews != 2 {
		t.Fatalf("reviews = %d, want the stop requested during the last one", reviews)
	}

	// The record: the stop stands on the item's triage record, and is not read
	// as a decision about the stoppage the run reached.
	counters, err := store.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if decided, found := counters.DecisionOf(outcome.RunID); !found || decided.Decision != runstate.TriageDecisionStop {
		t.Fatalf("decision = %#v (found=%t), want the stop still on the record", decided, found)
	}
	if standing := counters.Standing(outcome.RunID); standing.Decided {
		t.Fatalf("standing = %#v, want the stoppage read as undecided", standing)
	}

	// The docket: one fresh entry, open, naming the review that stopped the run
	// and the stop that was asked and never reached.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != outcome.RunID || entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("docket = %#v, want the stopped run docketed once", entries)
	}
	entry := entries[0]
	if entry.Closed != nil {
		t.Fatalf("closure = %#v, want the stoppage undecided", entry.Closed)
	}
	if strings.TrimSpace(entry.Blocker) == "" || len(entry.Findings) == 0 {
		t.Fatalf("entry = %#v, want the blocker and the reviewer's findings that stopped the run", entry)
	}
	stop := entry.StopRequested
	if stop == nil || stop.Landed || stop.By != decidedBy || stop.Decision != runstate.TriageDecisionStop {
		t.Fatalf("stop on the entry = %#v, want the stop that was asked and never reached", stop)
	}
	built, err := pipeline.Docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Closed != 0 {
		t.Fatalf("docket build = %d open, %d closed; want the later stoppage undecided", len(built.Entries), built.Closed)
	}
	listed := built.Entries[0]
	if listed.Counters.Standing.Decided || listed.Counters.AwaitingCarryOut() {
		t.Fatalf("standing = %#v, want nothing decided about the stoppage", listed.Counters.Standing)
	}
	rendered := listed.Render()
	for _, want := range []string{"Blocker", "A stop was asked for and never reached", decidedBy, "decides nothing about this stoppage", "add the missing file"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry does not say %q:\n%s", want, rendered)
		}
	}

	// The hold: the item is held for her decision about the stoppage rather than
	// read as superseded or as a decision awaiting its carry-out.
	held, err := readmodel.HeldForAPerson(context.Background(), store, store.Triage(), nil, nil)
	if err != nil {
		t.Fatalf("HeldForAPerson() error = %v", err)
	}
	reason, holding := held.Reason(docketedItem)
	if !holding || strings.Contains(reason, "superseded") || !strings.Contains(reason, "the development manager decides what happens to it") {
		t.Fatalf("hold = %q (held=%t), want the stoppage held for her decision", reason, holding)
	}
	if held.Decided(docketedItem) {
		t.Fatal("the hold reads the later stoppage as already decided")
	}
}

// The operator's own stop is unchanged by any of this: it names the operator and
// leaves nothing on the docket, since it hands nobody a decision.
func TestAnOperatorStopIsStillDocketedNowhere(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: docketedItem, Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    docketedItem,
			RequestedAt:   baseTime,
		})
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	_, err = pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "the operator stopped this run") {
		t.Fatalf("Run() error = %v, want the operator named", err)
	}
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("docket = %#v, want an operator's stop docketed nowhere", entries)
	}
}

// fixedStops is a stop request on record for every run it is asked about.
type fixedStops struct{ request runstate.StopRequest }

func (f fixedStops) StopRequested(string) (runstate.StopRequest, bool, error) {
	return f.request, true, nil
}

// A stop that landed is not a stop the run never reached. An operator's stop
// ends the run cancelled and is settled into a durable blocker, which is docketed
// on the same path as a stoppage the stop never reached; its entry says the stop
// is what ended it. A run that failed on its own account with the same request
// on record says the stop never reached it.
func TestAStoppedRunEntrySaysWhetherTheStopItCarriesLanded(t *testing.T) {
	t.Parallel()

	request := runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         docketedRunID,
		WorkItemID:    docketedItem,
		RequestedAt:   baseTime,
		Reason:        "wrong item",
	}
	for _, tc := range []struct {
		name   string
		status runstate.Status
		landed bool
	}{
		{name: "an operator's stop that landed", status: runstate.StatusCancelled, landed: true},
		{name: "a failed review the stop never reached", status: runstate.StatusFailed, landed: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			state := stoppedState()
			state.Status = tc.status
			docket := &memoryDocket{}
			docketer := docketerOver([]runstate.State{state}, docket)
			docketer.Stops = fixedStops{request: request}
			if _, err := docketer.RecordStoppedRun(state); err != nil {
				t.Fatalf("RecordStoppedRun() error = %v", err)
			}
			entries, err := docket.List()
			if err != nil || len(entries) != 1 {
				t.Fatalf("docket = %#v, %v; want the stopped run", entries, err)
			}
			stop := entries[0].StopRequested
			if stop == nil || stop.Landed != tc.landed || stop.By != "the operator" {
				t.Fatalf("stop on the entry = %#v, want landed=%t", stop, tc.landed)
			}
			rendered := entries[0].Render()
			if never := strings.Contains(rendered, "never reached"); never == tc.landed {
				t.Fatalf("rendered entry says never reached = %t, want %t:\n%s", never, !tc.landed, rendered)
			}
			if tc.landed && !strings.Contains(rendered, "Stopped in flight") {
				t.Fatalf("rendered entry does not say the stop ended the run:\n%s", rendered)
			}
		})
	}
}
