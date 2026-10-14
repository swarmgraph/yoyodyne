package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// countingProcess is who a round these tests seed was charged by. A round is
// only ever given back to the process that charged it, and the tests that seed
// one here only ever spend it; what they need is somebody to have charged it.
const countingProcess = "pid-1-000000000000000a"

// memoryDocket is the durable docket without the disk: it enforces the two
// properties the store guarantees, which are that a key is recorded once and
// that a decision recorded against a key is joined onto its entry.
type memoryDocket struct {
	entries []triage.Entry
	closed  map[string]triage.Closure
	failOn  string
}

// close settles one entry the way the store does: the entry stays on the log
// and the decision is recorded beside it, at a moment the caller names because
// what a build does about a settled key turns on when it was settled.
func (d *memoryDocket) close(key, decision string, at time.Time) {
	if d.closed == nil {
		d.closed = make(map[string]triage.Closure)
	}
	d.closed[key] = triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           key,
		ProductID:     "yoyodyne",
		RunID:         docketedRunID,
		WorkItemID:    docketedItem,
		Decision:      decision,
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      at,
	}
}

// waitOn settles one entry with a decision that holds only until the moment it
// names, which is what waiting on a forge is.
func (d *memoryDocket) waitOn(key string, at, revisitAfter time.Time) {
	d.close(key, "wait", at)
	settled := d.closed[key]
	settled.RevisitAfter = revisitAfter
	d.closed[key] = settled
}

// Close is the store's closure, reduced to what these tests read of it: the
// latest decision about a key is what stands, and one recorded while an earlier
// one still holds changes nothing.
func (d *memoryDocket) Close(closure triage.Closure) (bool, error) {
	if err := closure.Validate(); err != nil {
		return false, err
	}
	if d.closed == nil {
		d.closed = make(map[string]triage.Closure)
	}
	if standing, decided := d.closed[closure.Key]; decided && standing.Holds(closure.ClosedAt) {
		return false, nil
	}
	for _, entry := range d.entries {
		if entry.Key == closure.Key {
			d.closed[closure.Key] = closure
			return true, nil
		}
	}
	return false, fmt.Errorf("no docket entry keyed %s is recorded", closure.Key)
}

func (d *memoryDocket) RecordOnce(entry triage.Entry) (bool, error) {
	if d.failOn != "" && entry.Class == triage.Class(d.failOn) {
		return false, errors.New("the docket is unwritable")
	}
	if err := entry.Validate(); err != nil {
		return false, err
	}
	for index, existing := range d.entries {
		if existing.Key != entry.Key {
			continue
		}
		// A key whose entry a decision settled carries the stoppage that happened
		// after it, exactly as the durable store does: the same run can stop twice,
		// and the second time is not the first arriving again.
		closure, decided := d.closed[entry.Key]
		if !decided || !entry.RecordedAt.After(closure.ClosedAt) {
			return false, nil
		}
		d.entries[index] = entry
		return true, nil
	}
	d.entries = append(d.entries, entry)
	return true, nil
}

// List hands back a copy, as the durable store does by decoding the log afresh:
// what a reader joins onto the entries it was given must not travel back into
// the log, which is written once and never revised.
func (d *memoryDocket) List() ([]triage.Entry, error) {
	listed := slices.Clone(d.entries)
	for index := range listed {
		closure, found := d.closed[listed[index].Key]
		// A decision settles the stoppage it was made about, and not one docketed
		// after it.
		if !found || listed[index].RecordedAt.After(closure.ClosedAt) {
			continue
		}
		settled := closure
		listed[index].Closed = &settled
	}
	return listed, nil
}

func (d *memoryDocket) keys() []string {
	keys := make([]string, 0, len(d.entries))
	for _, entry := range d.entries {
		keys = append(keys, entry.Key)
	}
	return keys
}

// recordedRuns is the run evidence a docket is built from.
type recordedRuns struct {
	states []runstate.State
}

func (r recordedRuns) Recorded() ([]runstate.State, error) { return r.states, nil }

// recordedDecisions is the durable triage record the guards spend and refuse
// against, without the disk: what triage has decided about a work item, and what
// the harness has claimed against that item's stoppages.
type recordedDecisions struct {
	counters map[string]runstate.TriageCounters
	claimed  map[string][]runstate.Rerun
	// unreadable is the work item whose record cannot be read, which is the case
	// an entry must never render as an item nothing was ever decided about.
	unreadable string
}

func (d *recordedDecisions) Counters(workItemID string) (runstate.TriageCounters, error) {
	if workItemID == d.unreadable {
		return runstate.TriageCounters{}, errors.New("the triage record is unreadable")
	}
	return d.counters[workItemID], nil
}

func (d *recordedDecisions) Claimed(workItemID string) ([]runstate.Rerun, error) {
	if workItemID == d.unreadable {
		return nil, errors.New("the re-run records are unreadable")
	}
	return d.claimed[workItemID], nil
}

const (
	docketedRunID = "run-0123456789abcdef0123456789abcdef"
	docketedItem  = "yoyodyne-task"
	docketedTitle = "Slack thread headers carry the item's title"
)

var docketedNow = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

// docketClock keeps the moment a docket is built fixed, so what a test is
// measuring is the age of the work rather than the age of the test.
type docketClock struct{}

func (docketClock) Now() time.Time { return docketedNow }

// docketClockAt is the same for a build that happens later than the one before
// it, which is what a test about a decision and a stoppage on either side of it
// needs: the order of those moments is the whole of what it is measuring.
type docketClockAt struct{ at time.Time }

func (c docketClockAt) Now() time.Time { return c.at }

func docketerOver(states []runstate.State, docket *memoryDocket) Docketer {
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {ReviewRounds: 3}},
	}
	return docketerDeciding(states, docket, recorded, recorded)
}

// docketedTriage is the configuration a docket is built against in these tests,
// and docketedCaps the ceilings assembled from it — the same ones the guards are
// refused against, which is the whole point of the docket reporting them.
var (
	docketedTriage = config.Triage{
		StuckMergeAge:       config.Duration(2 * time.Hour),
		ReviewRoundsCap:     4,
		RepairGrantAttempts: 2,
	}
	docketedCaps = TriageCaps(config.Execution{IntegrationRetriesBeforeReconciliation: 1}, docketedTriage)
)

// docketerDeciding is a docket built over one triage record, for the tests that
// are about what the record says rather than about what stopped.
func docketerDeciding(states []runstate.State, docket *memoryDocket, decisions DocketDecisions, claimed DocketReruns) Docketer {
	return Docketer{
		Docket:    docket,
		Runs:      recordedRuns{states: states},
		Decisions: decisions,
		Reruns:    claimed,
		// The caps are assembled the way every other reader of the same record
		// assembles them, so the docket cannot report an item against a ceiling
		// nothing enforces.
		Caps:   docketedCaps,
		Triage: docketedTriage,
		Clock:  docketClock{},
	}
}

// docketerOverStore is a docket over a real run store, reading the same two
// durable records the triage guards spend: the item's counters and the re-runs
// claimed against its stoppages.
func docketerOverStore(docket Docket, store *runstate.Store, cfg config.Config) *Docketer {
	return &Docketer{
		Docket:    docket,
		Runs:      store,
		Decisions: store.Triage(),
		Reruns:    store.Reruns(),
		Stops:     store,
		Caps:      TriageCaps(cfg.Execution, cfg.Triage),
		Triage:    cfg.Triage,
	}
}

// stoppedState is a run that ended on a durable blocker with its work
// preserved, which is what every blocked run leaves behind.
func stoppedState() runstate.State {
	completed := docketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:        runstate.StateSchemaVersion,
		RunID:                docketedRunID,
		ProductID:            "yoyodyne",
		RepositoryID:         "yoyodyne",
		WorkItemID:           docketedItem,
		WorkItemTitle:        docketedTitle,
		Backend:              "claude-code",
		Status:               runstate.StatusFailed,
		Phase:                runstate.PhaseReviewing,
		StartedAt:            completed.Add(-time.Hour),
		UpdatedAt:            completed,
		CompletedAt:          &completed,
		WorktreePath:         "/state/worktrees/task",
		Branch:               "yoyodyne/task/abc",
		BaseCommit:           strings.Repeat("a", 40),
		TargetBranch:         "main",
		RepairAttempts:       2,
		ReviewRounds:         3,
		ReviewSummary:        "the change misses the acceptance criteria",
		ReviewFindings:       1,
		ReviewFindingDetails: []runstate.Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}},
		CheckFailure:         &runstate.CheckFailure{Command: "make test", ExitCode: 1, Output: "FAIL"},
		Blocker:              "Yoyodyne stopped this item: the repair budget was spent.",
	}
}

// publishedState is a run whose approved change was published and whose
// publication the forge has not merged.
func publishedState(approvedAgo time.Duration) runstate.State {
	completed := docketedNow.Add(-approvedAgo)
	commit := strings.Repeat("b", 40)
	return runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          docketedRunID,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		WorkItemID:     docketedItem,
		Backend:        "claude-code",
		Status:         runstate.StatusSucceeded,
		Phase:          runstate.PhaseComplete,
		StartedAt:      completed.Add(-time.Hour),
		UpdatedAt:      completed,
		CompletedAt:    &completed,
		Branch:         "yoyodyne/task/abc",
		ReviewDecision: runstate.ReviewApprove,
		ReviewRounds:   1,
		PullRequest: &runstate.PullRequest{
			Remote: "origin", Branch: "yoyodyne/task/abc", Number: 42,
			URL: "https://forge.invalid/pull/42", HeadCommit: commit, State: "OPEN",
		},
	}
}

// A run that ends on a durable blocker is the event the docket exists for, and
// what it carries is the evidence somebody decides on rather than a summary of
// it.
func TestARunThatStoppedOnABlockerIsDocketedWithItsEvidence(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	created, err := docketerOver(nil, docket).RecordStoppedRun(stoppedState())
	if err != nil || !created {
		t.Fatalf("RecordStoppedRun() = %t, error = %v", created, err)
	}
	entry := docket.entries[0]
	if entry.Class != triage.ClassStoppedRun || entry.WorkItemID != docketedItem {
		t.Fatalf("entry = %#v", entry)
	}
	// An entry outlives the run it came from, so what the item is called travels
	// with it: a development manager reading the docket afterwards has the entry
	// and not the run record it was built from.
	if entry.WorkItemTitle != docketedTitle {
		t.Fatalf("title = %q, want what the run recorded the item as", entry.WorkItemTitle)
	}
	if entry.Blocker != stoppedState().Blocker {
		t.Fatalf("blocker = %q, want the words it was recorded in", entry.Blocker)
	}
	if len(entry.Findings) != 1 || entry.Findings[0].Message != "add the missing file" || entry.Findings[0].File != "feature.txt" {
		t.Fatalf("findings = %#v, want the reviewer's own words", entry.Findings)
	}
	if entry.Check == nil || entry.Check.Command != "make test" || entry.Check.ExitCode != 1 {
		t.Fatalf("check = %#v", entry.Check)
	}
	if entry.Artifacts.Branch != "yoyodyne/task/abc" || entry.Artifacts.WorktreePath != "/state/worktrees/task" {
		t.Fatalf("artifacts = %#v, want the preserved branch and worktree", entry.Artifacts)
	}
	// The counters are the half of an entry a decision is measured against: the
	// rounds and the decisions are the item's durable triage record, and the caps
	// beside each are what will refuse the next decision.
	want := triage.Counters{
		ReviewRounds: 3, ReviewRoundsCap: 4, RepairAttempts: 2, RepairGrantAttempts: 2,
		RepairGrantsCap: 1, RerunsCap: 1, MergeRearmsCap: 1,
		CrossingsBound: runstate.MaxDelegatedCapCrossings,
	}
	if entry.Counters != want {
		t.Fatalf("counters = %#v, want %#v", entry.Counters, want)
	}
}

// A parked run is owed a continuation. Docketing one would put work in front of
// a development manager that nobody has to decide anything about, and every one
// of these is recorded on a run that is still running.
func TestWorkOwedAContinuationIsNeverDocketed(t *testing.T) {
	t.Parallel()

	resetsAt := docketedNow.Add(time.Hour)
	heldSince := docketedNow.Add(-time.Hour)
	for _, test := range []struct {
		name  string
		state func(runstate.State) runstate.State
	}{
		{
			name: "waiting out an exhausted usage limit",
			state: func(state runstate.State) runstate.State {
				state.UsageLimitResetsAt = &resetsAt
				state.PauseCause = runstate.PauseUsageLimit
				return state
			},
		},
		{
			name: "owed the rest of an attempt the harness stopped on time",
			state: func(state runstate.State) runstate.State {
				state.ProviderStop = runstate.ProviderStopStalled
				return state
			},
		},
		{
			name: "held up by an unresolved directive",
			state: func(state runstate.State) runstate.State {
				state.DirectivePause = &runstate.DirectivePause{DirectiveID: "d1", Kind: "question", Unresolved: "the operator has not answered"}
				return state
			},
		},
		{
			name: "waiting on unfinished work its item depends on",
			state: func(state runstate.State) runstate.State {
				state.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
				return state
			},
		},
		{
			name: "parked because the operator paused the harness",
			state: func(state runstate.State) runstate.State {
				state.OperatorHeldSince = &heldSince
				return state
			},
		},
		{
			name:  "still working",
			state: func(state runstate.State) runstate.State { return state },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// A parked run is a running run, so it carries no terminal record and
			// no blocker: the pauses above are exactly what a run stops short for.
			parked := stoppedState()
			parked.Status = runstate.StatusRunning
			parked.CompletedAt = nil
			parked.Blocker = ""
			parked = test.state(parked)
			if err := parked.Validate(); err != nil {
				t.Fatalf("the parked run is not a state the harness could record: %v", err)
			}

			docket := &memoryDocket{}
			built, err := docketerOver([]runstate.State{parked}, docket).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if built.Added != 0 || len(docket.entries) != 0 {
				t.Fatalf("a run owed a continuation was docketed: %#v", docket.entries)
			}
		})
	}
}

// Most runs end for a reason nobody has to decide about. Docketing those would
// make the docket a run log, which is a channel nobody reads.
func TestARunThatEndedWithNothingToDecideIsNotDocketed(t *testing.T) {
	t.Parallel()

	// Neither of the two things that make a stoppage: no blocker anybody recorded,
	// and no change left behind for anybody to decide about.
	ended := stoppedState()
	ended.Blocker = ""
	ended.Branch = ""
	ended.WorktreePath = ""
	ended.Failure = "the harness could not reach the tracker to claim the item"
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{ended}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Fatalf("a run with no blocker and nothing preserved was docketed: %#v", docket.entries)
	}
}

// diedHoldingItsChange is the shape all three of yoyodyne-ifd.268's incidents
// left behind: a run made terminal by its own process, with no blocker anybody
// recorded, the reason it gave for dying, and the change still on its branch.
// Nothing resumes it, because it is terminal, and until it was docketed no
// recorded decision could reach it either.
func diedHoldingItsChange() runstate.State {
	died := stoppedState()
	died.Phase = runstate.PhaseDeveloping
	died.Blocker = ""
	died.ReviewSummary = ""
	died.ReviewFindings = 0
	died.ReviewFindingDetails = nil
	died.ReviewRounds = 0
	died.CheckFailure = nil
	died.RepairAttempts = 0
	died.Failure = "publish the developer branch: remote rejected the push: Connection reset by 140.82.121.36 port 443"
	return died
}

// The seam yoyodyne-ifd.268 is about: a run that dies holding its change reaches
// the development manager, so the decision she records about it is one the
// harness can carry out. Every one of these was found by a person noticing the
// item had gone quiet, and got out of it by dispatching the item by name — which
// starts a run and records no decision at all.
func TestARunThatDiedHoldingItsChangeIsDocketed(t *testing.T) {
	t.Parallel()

	swept := docketedNow.Add(-30 * time.Minute)
	for _, test := range []struct {
		name  string
		state func(runstate.State) runstate.State
	}{
		{
			// run-0cfda20a of yoyodyne-ifd.267.
			name:  "the remote reset the developer branch push, with the branch and worktree both still there",
			state: func(state runstate.State) runstate.State { return state },
		},
		{
			// run-ae343453 of yoyodyne-ifd.242: the same death, whose worktree the
			// idle sweep took afterwards. The branch is what still holds the change.
			name: "the same death after the idle sweep took the worktree, leaving the branch",
			state: func(state runstate.State) runstate.State {
				state.WorktreeRemoved = true
				state.WorktreeSweptAt = &swept
				return state
			},
		},
		{
			// run-32e3f059 of yoyodyne-ifd.209.6.
			name: "the developer backend broke mid-attempt on oversized output",
			state: func(state runstate.State) runstate.State {
				state.Failure = "developer backend failed: run Claude Code: process output exceeded 8388608 bytes"
				state.WorktreeRemoved = true
				state.WorktreeSweptAt = &swept
				state.PreservedWorkRef = "refs/yoyodyne/preserved-work/" + docketedRunID
				return state
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			died := test.state(diedHoldingItsChange())
			if err := died.Validate(); err != nil {
				t.Fatalf("the death is not a state the harness could record: %v", err)
			}

			docket := &memoryDocket{}
			created, err := docketerOver(nil, docket).RecordStoppedRun(died)
			if err != nil || !created {
				t.Fatalf("a death holding the change reached nobody: created = %t, error = %v", created, err)
			}
			entry := docket.entries[0]
			if entry.Class != triage.ClassStoppedRun || entry.Key != triage.Key(triage.ClassStoppedRun, docketedRunID) {
				t.Fatalf("entry = %#v, want the stoppage of the run keyed to it", entry)
			}
			// The item carries no blocker for this, so the entry says what stopped
			// the run in its own field rather than claiming the item holds words
			// nobody wrote there.
			if entry.Blocker != "" {
				t.Fatalf("blocker = %q, want none: nothing recorded one on the item", entry.Blocker)
			}
			if entry.Failure != died.Failure {
				t.Fatalf("failure = %q, want the reason the run gave %q", entry.Failure, died.Failure)
			}
			if !strings.Contains(entry.Render(), died.Failure) {
				t.Fatalf("the rendered entry does not say what stopped the run:\n%s", entry.Render())
			}
			// And what still holds the change is named, because that is the whole of
			// what makes the entry actionable.
			if entry.Artifacts.Branch != died.Branch || entry.Artifacts.BranchRemoved {
				t.Fatalf("artifacts = %#v, want the branch the change is on", entry.Artifacts)
			}
		})
	}
}

// The deaths this must not docket. Each is terminal and each leaves something
// behind, and none of them is work waiting on a person's decision.
func TestADeathThatIsNobodysDecisionIsNotDocketed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		state func(runstate.State) runstate.State
	}{
		{
			// An operator's stop and a deadline are both deliberate. Neither hands
			// anybody a decision, and both read as themselves rather than as a
			// stoppage everywhere else the harness says what became of a run.
			name: "the operator stopped it",
			state: func(state runstate.State) runstate.State {
				state.Status = runstate.StatusCancelled
				return state
			},
		},
		{
			name: "the harness stopped it on time",
			state: func(state runstate.State) runstate.State {
				state.Status = runstate.StatusTimedOut
				return state
			},
		},
		{
			// The work landed. Finishing what is left of the run is reconciliation's,
			// and an unfinished publication of it is docketed as the publication it is.
			name: "it failed after promoting its work",
			state: func(state runstate.State) runstate.State {
				state.Phase = runstate.PhaseIntegrating
				state.ReviewDecision = runstate.ReviewApprove
				state.ReviewRounds = 1
				state.ProviderSessionID = "developer-session"
				state.ProviderModel = "opus"
				state.ReviewSessionID = "reviewer-session"
				state.ReviewModel = "opus"
				state.Integration = &runstate.Integration{
					TargetBranch:         "main",
					SourceCommit:         strings.Repeat("c", 40),
					TargetCommit:         strings.Repeat("c", 40),
					PreviousTargetCommit: strings.Repeat("a", 40),
				}
				state.HarnessCommit = strings.Repeat("c", 40)
				return state
			},
		},
		{
			name: "it died with nothing to show for it",
			state: func(state runstate.State) runstate.State {
				state.Branch = ""
				state.WorktreePath = ""
				state.BaseCommit = ""
				state.TargetBranch = ""
				return state
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			died := test.state(diedHoldingItsChange())
			if err := died.Validate(); err != nil {
				t.Fatalf("the death is not a state the harness could record: %v", err)
			}

			docket := &memoryDocket{}
			created, err := docketerOver(nil, docket).RecordStoppedRun(died)
			if err != nil {
				t.Fatalf("RecordStoppedRun() error = %v", err)
			}
			if created || len(docket.entries) != 0 {
				t.Fatalf("a run nobody has to decide about was docketed: %#v", docket.entries)
			}
		})
	}
}

// And the scan over the recorded history does not go looking for them, which is
// the one asymmetry in this file that looks like an oversight. Every terminal
// failed run with a surviving branch has this shape, including every record
// written before the harness carried a blocker at all; re-deriving it would put
// months of settled work on the docket in one build and push the stoppages that
// need deciding off the end of what the development manager's context can carry.
func TestTheScanDoesNotBackfillDeathsFromTheRecordedHistory(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{diedHoldingItsChange()}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Fatalf("the scan backfilled a death from the history: %#v", docket.entries)
	}
	// A blocker is re-derived as it always was: it is a durable classification
	// that stood on the record from the moment it was written.
	blocked := &memoryDocket{}
	rebuilt, err := docketerOver([]runstate.State{stoppedState()}, blocked).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rebuilt.Added != 1 {
		t.Fatalf("the scan stopped re-deriving a blocker: added = %d", rebuilt.Added)
	}
}

// A failure long enough to exceed what an entry may carry is cut rather than
// refused. An entry refused for its length is a stoppage that reaches nobody,
// which is exactly the silence this docketing exists to end.
//
// The run record is now bounded at the write that makes it, so nothing the
// harness records reaches here oversized. The cut stays, and so does this test:
// the entry must not depend on a bound somebody else was supposed to have
// applied, and a record written before that bound existed still dockets.
func TestAnOversizedFailureIsCutRatherThanLosingTheEntry(t *testing.T) {
	t.Parallel()

	died := diedHoldingItsChange()
	died.Failure = strings.Repeat("x", triage.MaxBlockerBytes*2)
	docket := &memoryDocket{}
	created, err := docketerOver(nil, docket).RecordStoppedRun(died)
	if err != nil || !created {
		t.Fatalf("a verbose death reached nobody: created = %t, error = %v", created, err)
	}
	entry := docket.entries[0]
	if len(entry.Failure) > triage.MaxBlockerBytes {
		t.Fatalf("failure is %d bytes, which the entry's own bound refuses", len(entry.Failure))
	}
	if !strings.Contains(entry.Failure, "the rest of this failure was not recorded") {
		t.Fatalf("a cut failure did not say it was cut: %q", entry.Failure[len(entry.Failure)-120:])
	}
}

// A reviewer summary long enough to exceed what an entry may carry is cut rather
// than refused, for the reason the failure beside it is: the entry is the whole
// of what the development manager is told about a stopped run, and one refused
// for its length is a stoppage she never hears about. It was the summary the
// entry refused on rather than the failure, because the record bounded the one
// and not the other.
//
// The record now bounds this too, at the write that makes it, so nothing the
// harness records reaches here oversized. The cut stays all the same: the entry
// must not depend on a bound somebody else was supposed to have applied, and a
// record written before that bound existed still dockets.
func TestAnOversizedReviewSummaryIsCutRatherThanLosingTheEntry(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	stopped.ReviewSummary = "the change misses the acceptance criteria: " + strings.Repeat("x", triage.MaxMessageBytes*2)
	docket := &memoryDocket{}
	created, err := docketerOver(nil, docket).RecordStoppedRun(stopped)
	if err != nil || !created {
		t.Fatalf("a verbose review reached nobody: created = %t, error = %v", created, err)
	}
	entry := docket.entries[0]
	if len(entry.Summary) > triage.MaxMessageBytes {
		t.Fatalf("summary is %d bytes, which the entry's own bound refuses", len(entry.Summary))
	}
	if !strings.HasPrefix(entry.Summary, "the change misses the acceptance criteria: ") ||
		!strings.Contains(entry.Summary, "the rest of this summary was not recorded") {
		t.Fatalf("a cut summary lost its head or did not say it was cut: %q", entry.Summary[len(entry.Summary)-120:])
	}
}

func TestAPublicationIsDocketedOnceItIsStuckAndNotBefore(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		state      func() runstate.State
		wantEntry  bool
		wantInText string
	}{
		{
			// Nothing has happened to it, which is what makes it stuck, so its
			// age is the only thing there is to decide on.
			name:       "approved and unmerged past the configured age",
			state:      func() runstate.State { return publishedState(3 * time.Hour) },
			wantEntry:  true,
			wantInText: "OPEN",
		},
		{
			name:      "approved and unmerged inside the configured age",
			state:     func() runstate.State { return publishedState(time.Hour) },
			wantEntry: false,
		},
		{
			// A merge the forge dropped is not going to happen with time, so it
			// is docketed the moment the harness records it rather than aged.
			name: "an outstanding publication the harness already recorded",
			state: func() runstate.State {
				state := publishedState(time.Minute)
				state.PublishFailure = "the forge dropped the queued merge of pull request 42"
				return state
			},
			wantEntry:  true,
			wantInText: "dropped the queued merge",
		},
		{
			name: "a publication the forge merged",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.PullRequest.Merged = false
				state.PullRequest.State = "MERGED"
				merged := *state.PullRequest
				merged.Merged = true
				state.PullRequest = &merged
				state.Integration = &runstate.Integration{
					TargetBranch: "main", SourceCommit: strings.Repeat("c", 40),
					TargetCommit: strings.Repeat("c", 40), PreviousTargetCommit: strings.Repeat("d", 40),
				}
				return state
			},
			wantEntry: false,
		},
		{
			// The merge happened and the publication still did not finish, which
			// is the one merged publication somebody has to look at.
			name: "a merge the harness could not confirm",
			state: func() runstate.State {
				state := publishedState(time.Minute)
				merged := *state.PullRequest
				merged.Merged = true
				merged.State = "MERGED"
				state.PullRequest = &merged
				state.Integration = &runstate.Integration{
					TargetBranch: "main", SourceCommit: strings.Repeat("c", 40),
					TargetCommit: strings.Repeat("c", 40), PreviousTargetCommit: strings.Repeat("d", 40),
				}
				state.PublishFailure = "confirm the queued merge reached main: the remote does not carry it"
				return state
			},
			wantEntry:  true,
			wantInText: "the harness could not finish the publication",
		},
		{
			// A branch nobody authorized merging is not a stuck publication; the
			// run's own blocker is what says what happened to it.
			name: "a publication no review approved",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.ReviewDecision = runstate.ReviewRepair
				return state
			},
			wantEntry: false,
		},
		{
			// A publication the harness closed as superseded is dealt with. It is
			// unmerged and always will be, so without this it would age into the
			// docket forever and send somebody to a closed pull request.
			name: "a publication retired as superseded",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				retired := *state.PullRequest
				retired.State = "CLOSED"
				retired.Superseded = "pull request #46, which the forge merged for run run-fedcba9876543210fedcba9876543210"
				state.PullRequest = &retired
				return state
			},
			wantEntry: false,
		},
		{
			name: "a run that is still working on its own publication",
			state: func() runstate.State {
				state := publishedState(3 * time.Hour)
				state.Status = runstate.StatusRunning
				state.Phase = runstate.PhaseIntegrating
				state.CompletedAt = nil
				return state
			},
			wantEntry: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			docket := &memoryDocket{}
			built, err := docketerOver([]runstate.State{test.state()}, docket).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if !test.wantEntry {
				if built.Added != 0 {
					t.Fatalf("docketed a publication that is not stuck: %#v", docket.entries)
				}
				return
			}
			if built.Added != 1 || len(built.Entries) != 1 {
				t.Fatalf("build = %#v, want one publication entry", built)
			}
			entry := built.Entries[0]
			if entry.Class != triage.ClassPublication || entry.Publication == nil || entry.Publication.Number != 42 {
				t.Fatalf("entry = %#v", entry)
			}
			if entry.Counters.ReviewRoundsCap != 4 || entry.Counters.RepairGrantAttempts != 2 {
				t.Fatalf("counters = %#v, want the configured budgets beside the publication", entry.Counters)
			}
			if rendered := entry.Render(); !strings.Contains(rendered, test.wantInText) {
				t.Fatalf("entry is missing %q:\n%s", test.wantInText, rendered)
			}
		})
	}
}

// The age is measured from when the run ended, so a sweep that walks past a
// stuck publication and writes to the record cannot reset the clock on it.
func TestAPublicationsAgeIsMeasuredFromWhenItsRunEnded(t *testing.T) {
	t.Parallel()

	state := publishedState(3 * time.Hour)
	state.UpdatedAt = docketedNow.Add(-time.Minute)
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{state}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 1 {
		t.Fatalf("a touched record hid a stuck publication: %#v", built)
	}
	if !built.Entries[0].Publication.ApprovedAt.Equal(state.CompletedAt.UTC()) {
		t.Fatalf("approved at = %s, want when the run ended", built.Entries[0].Publication.ApprovedAt)
	}
}

// Scanning twice must docket once. This is what makes it safe for the sweep and
// every conversation open to build the docket, and what stops a reconcile after
// a crash from docketing a stoppage the run already docketed.
func TestBuildingTheDocketTwiceDocketsEachEventOnce(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	published := publishedState(3 * time.Hour)
	published.RunID = "run-fedcba9876543210fedcba9876543210"
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stopped, published}, docket)

	first, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if first.Added != 2 || len(first.Entries) != 2 {
		t.Fatalf("first build = %#v, want both stoppages docketed", first)
	}
	second, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if second.Added != 0 || len(second.Entries) != 2 {
		t.Fatalf("second build = %#v, want the same docket", second)
	}
	want := []string{
		triage.Key(triage.ClassStoppedRun, stopped.RunID),
		// A publication is keyed to the run and the pull request together, so what
		// the entry is about is two facts a reader can check against the record.
		triage.PublicationKey(published.RunID, published.PullRequest.Number),
	}
	if strings.Join(docket.keys(), ",") != strings.Join(want, ",") {
		t.Fatalf("docket keys = %v, want %v", docket.keys(), want)
	}
}

// The regression case the twelve phantoms of the 250 sweep left behind: a
// stoppage the development manager decided about must not come back on the next
// docket. The scan goes on finding the same run in the same durable records, so
// what settles it has to be the decision rather than the evidence changing.
func TestAStoppageDecidedOnceDoesNotComeBackOnTheNextDocket(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	published := publishedState(3 * time.Hour)
	published.RunID = "run-fedcba9876543210fedcba9876543210"
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stopped, published}, docket)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	// An escalation spends no counter at all, which is precisely the decision
	// nothing else the harness reads can see.
	docket.close(triage.PublicationKey(published.RunID, published.PullRequest.Number), "escalate", docketedNow)

	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 0 {
		t.Fatalf("second build added %d entry(s), want the settled stoppage left alone", rebuilt.Added)
	}
	if rebuilt.Closed != 1 {
		t.Fatalf("second build closed = %d, want the decided entry counted", rebuilt.Closed)
	}
	if len(rebuilt.Entries) != 1 || rebuilt.Entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("second build = %#v, want the undecided stoppage and nothing else", rebuilt.Entries)
	}
	// The entry is still on the log. That is what stops the scan docketing the
	// same publication again the next time it walks the same records.
	if len(docket.entries) != 2 {
		t.Fatalf("docket entries = %#v, want both stoppages still recorded", docket.keys())
	}
}

// A docket every entry of which has been decided is a docket with nothing on
// it, rather than one that could not be read: what a reader must be told is that
// nothing is waiting on them.
func TestADocketWhoseEntriesAreAllDecidedListsNothing(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{stopped}, docket)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	docket.close(triage.Key(triage.ClassStoppedRun, stopped.RunID), "escalate", docketedNow)

	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if len(rebuilt.Entries) != 0 || rebuilt.Closed != 1 {
		t.Fatalf("second build = %#v, want nothing listed and the decided entry counted", rebuilt)
	}
}

// The other half of that rule, and the one a closed entry could hide: a repair
// continues the run that stopped, so a repaired run that dies again derives the
// key its settled entry carries. What decides whether it is a fresh stoppage is
// the run's own ending, not the build's clock — every scan re-derives the same
// stoppages, and the settled one must stay settled.
func TestARunThatStoppedAgainAfterItsDecisionIsDocketedAgain(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	docket := &memoryDocket{}
	if _, err := docketerOver([]runstate.State{stopped}, docket).Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	decided := docketedNow.Add(30 * time.Minute)
	docket.close(triage.Key(triage.ClassStoppedRun, stopped.RunID), "repair", decided)

	// A build between the decision and anything happening leaves the settled
	// stoppage alone, which is the phantom this must not regrow.
	quiet := docketerOver([]runstate.State{stopped}, docket)
	quiet.Clock = docketClockAt{at: decided.Add(time.Minute)}
	unchanged, err := quiet.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if unchanged.Added != 0 || len(unchanged.Entries) != 0 || unchanged.Closed != 1 {
		t.Fatalf("build over an unchanged run = %#v, want the settled stoppage left alone", unchanged)
	}

	// The repair was carried out in the run that stopped, and the run died again.
	died := decided.Add(15 * time.Minute)
	repaired := stoppedState()
	repaired.CompletedAt = &died
	repaired.UpdatedAt = died
	repaired.Blocker = "Yoyodyne stopped this item: the push was refused by the remote."
	after := docketerOver([]runstate.State{repaired}, docket)
	after.Clock = docketClockAt{at: died.Add(time.Minute)}
	rebuilt, err := after.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 1 || len(rebuilt.Entries) != 1 || rebuilt.Closed != 0 {
		t.Fatalf("second build = %#v, want the fresh stoppage docketed and listed", rebuilt)
	}
	if rebuilt.Entries[0].Blocker != repaired.Blocker {
		t.Fatalf("entry = %#v, want the blocker the run stopped on this time", rebuilt.Entries[0])
	}
}

// Waiting says the forge still has the merge, which is "not yet" rather than a
// decision about it. So the entry comes back once it has been sitting there as
// long again — nothing about a merge that is not happening ever changes, so
// without that a stuck publication disappears on the strength of a decision to
// look at it later.
func TestAPublicationWaitedOnComesBackOnceTheWaitHasRunOut(t *testing.T) {
	t.Parallel()

	published := publishedState(3 * time.Hour)
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{published}, docket)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	key := triage.PublicationKey(published.RunID, published.PullRequest.Number)

	// Decided as the entry was made, to be looked at again two hours later.
	decided := docketedNow.Add(time.Minute)
	docket.waitOn(key, decided, decided.Add(2*time.Hour))

	waiting := docketerOver([]runstate.State{published}, docket)
	waiting.Clock = docketClockAt{at: decided.Add(time.Hour)}
	held, err := waiting.Build()
	if err != nil {
		t.Fatalf("Build() while waiting error = %v", err)
	}
	if len(held.Entries) != 0 || held.Closed != 1 {
		t.Fatalf("build while the wait holds = %#v, want the entry left alone", held)
	}
	// It is not a question while the wait holds, and it is still carried, apart,
	// for the docket to list after everything that is.
	if len(held.Waiting) != 1 || held.Waiting[0].Key != key || len(held.Listed()) != 1 {
		t.Fatalf("waiting = %#v, want the waited publication carried apart", held.Waiting)
	}

	// The same decision, once the moment it named has passed.
	docketer.Clock = docketClockAt{at: decided.Add(3 * time.Hour)}
	lapsed, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() after the wait error = %v", err)
	}
	if len(lapsed.Entries) != 1 || lapsed.Closed != 0 || len(lapsed.Waiting) != 0 {
		t.Fatalf("build after the wait ran out = %#v, want the publication back", lapsed)
	}
	// It comes back as the entry it was, carrying what was decided about it, so
	// whoever gets it is told they have seen it before.
	if lapsed.Entries[0].Closed == nil || lapsed.Entries[0].Closed.Decision != "wait" {
		t.Fatalf("entry = %#v, want the lapsed decision carried on it", lapsed.Entries[0])
	}
	// And nothing was docketed a second time for it.
	if lapsed.Added != 0 || len(docket.entries) != 1 {
		t.Fatalf("build = %#v, docket = %v, want the one entry", lapsed, docket.keys())
	}
}

// One run can have stopped and left a publication nobody merged. They are two
// records because they are two events, and one of them being docketed must not
// hide the other — but they are one live entry, with the earlier beneath.
func TestOneRunCanBeDocketedForBothWhatStoppedAndWhatWasNeverMerged(t *testing.T) {
	t.Parallel()

	state := publishedState(3 * time.Hour)
	state.Status = runstate.StatusFailed
	state.Blocker = "Yoyodyne stopped this item: the publication needs a person."
	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{state}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 2 {
		t.Fatalf("build = %#v, want the stoppage and the publication both docketed", built)
	}
	if len(built.Entries) != 1 || built.Folded != 1 || len(built.Entries[0].Earlier) != 1 {
		t.Fatalf("build = %#v, want one live entry with the other beneath it", built)
	}
}

// The case of 2026-09-25, when yoyodyne-ifd.362 stood on the docket six times:
// one run docketed three ways — its stoppage as it ended, the developer's
// escalation it ended on, and the publication a later scan found stuck behind it
// — is one live entry, with the earlier two beneath it whole rather than
// summarized. A wait decided about it lapses back as that same one entry,
// carrying the decision.
func TestOneStoppageDocketedThreeWaysIsOneLiveEntry(t *testing.T) {
	t.Parallel()

	state := stoppedState()
	state.Blocker = "Yoyodyne stopped this item: its target branch moved, and this change conflicts with what the branch now holds."
	state.LandingOutcome = runstate.LandingEscalate
	state.LandingReason = "the criteria ask for a file the design forbids"
	state.ReviewDecision = runstate.ReviewApprove
	state.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: state.Branch, Number: 42,
		URL: "https://forge.invalid/pull/42", HeadCommit: strings.Repeat("b", 40), State: "OPEN",
	}
	ended := *state.CompletedAt
	docket := &memoryDocket{}
	at := func(moment time.Time) Docketer {
		docketer := docketerOver([]runstate.State{state}, docket)
		docketer.Clock = docketClockAt{at: moment}
		return docketer
	}

	// The run dockets its own stoppage, and the escalation it ended on, as it ends.
	if created, err := at(ended).RecordStoppedRun(state); err != nil || !created {
		t.Fatalf("RecordStoppedRun() = %t, error = %v, want the stoppage docketed", created, err)
	}
	if created, err := at(ended.Add(time.Second)).RecordEscalation(state); err != nil || !created {
		t.Fatalf("RecordEscalation() = %t, error = %v, want the escalation docketed", created, err)
	}
	// A sweep re-derives both, and a later scan finds the publication stuck.
	if swept, err := at(ended.Add(30 * time.Minute)).Build(); err != nil || swept.Added != 0 {
		t.Fatalf("sweep build = %#v, error = %v, want nothing docketed twice", swept, err)
	}
	later, err := at(ended.Add(3 * time.Hour)).Build()
	if err != nil || later.Added != 1 {
		t.Fatalf("later build = %#v, error = %v, want the stuck publication recorded", later, err)
	}
	if len(docket.entries) != 3 {
		t.Fatalf("docket log = %v, want three records kept for the run", docket.keys())
	}

	if len(later.Entries) != 1 || later.Folded != 2 {
		t.Fatalf("docket = %#v, want one live entry for the run", later.Entries)
	}
	live := later.Entries[0]
	if live.Class != triage.ClassPublication || len(live.Earlier) != 2 ||
		live.Earlier[0].Class != triage.ClassStoppedRun || live.Earlier[1].Class != triage.ClassEscalation {
		t.Fatalf("live entry = %#v, want the publication on top and the stoppage then the escalation beneath", live)
	}
	// Beneath means whole: the stoppage keeps its findings, its failing check, and
	// its preserved branch, which is what separates a repair from a re-run.
	stoppage := live.Earlier[0]
	if len(stoppage.Findings) != 1 || stoppage.Check == nil || stoppage.Artifacts.Branch != state.Branch {
		t.Fatalf("folded stoppage = %#v, want its whole evidence kept", stoppage)
	}
	rendered := live.Render()
	for _, want := range []string{
		"Docketed 2 time(s) before for this run",
		"its target branch moved",
		"add the missing file",
		"Failing check: make test (exit 1)",
		"the criteria ask for a file the design forbids",
		"yoyodyne/task/abc",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered entry is missing %q:\n%s", want, rendered)
		}
	}

	// A wait decided about the one entry settles all three, and when it lapses it
	// is the same one entry that comes back, carrying the decision.
	decided := ended.Add(3*time.Hour + time.Minute)
	for _, key := range docket.keys() {
		docket.waitOn(key, decided, decided.Add(2*time.Hour))
	}
	if held, err := at(decided.Add(time.Hour)).Build(); err != nil || len(held.Entries) != 0 {
		t.Fatalf("build while waiting = %#v, error = %v, want nothing listed", held, err)
	}
	lapsed, err := at(decided.Add(3 * time.Hour)).Build()
	if err != nil || lapsed.Added != 0 || len(lapsed.Entries) != 1 || lapsed.Folded != 2 {
		t.Fatalf("build after the wait = %#v, error = %v, want the one entry back and nothing docketed again", lapsed, err)
	}
	if back := lapsed.Entries[0]; back.Closed == nil || back.Closed.Decision != "wait" ||
		back.Earlier[0].Closed == nil || back.Earlier[0].Closed.Decision != "wait" {
		t.Fatalf("entry = %#v, want the lapsed decision carried on it and beneath it", back)
	}
}

// A separate run of the same item is separate stopped work, and is not folded:
// the fold is one entry per stopped run, not per item.
func TestAnotherRunOfTheSameItemIsItsOwnEntry(t *testing.T) {
	t.Parallel()

	first := stoppedState()
	second := stoppedState()
	second.RunID = "run-fedcba9876543210fedcba9876543210"
	built, err := docketerOver([]runstate.State{first, second}, &memoryDocket{}).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 2 || built.Folded != 0 {
		t.Fatalf("build = %#v, want one entry per stopped run", built)
	}
}

// The development manager's evidence from run-effbd604, replayed: a re-run
// decided and durably recorded where the counters read, a resubmission refused
// as one of one re-runs spent, and a docket that showed no recorded decision at
// all — which is how one authorized recovery nearly got spent twice.
//
// The decision is made after the stoppage is docketed, because that is the only
// order there is: the entry is what the decision is made against.
func TestARecordedDecisionTheGuardWouldRefuseAgainShowsOnTheDocket(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	decisions, err := runstate.NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	claimed, err := runstate.NewRerunStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	docket := &memoryDocket{}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, docket, decisions, claimed)

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 || built.Entries[0].Counters.Reruns != 0 {
		t.Fatalf("build = %#v, want the stoppage docketed with nothing decided about it yet", built)
	}

	// The development manager decides a re-run, which spends the item's re-run
	// budget as it is recorded and before anything acts on it.
	if _, err := decisions.RecordRerun(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRerun, decidedRunID), docketedNow, docketedCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	// The resubmission the development manager made is refused by the guard, and
	// the refusal is the proof the decision is really recorded.
	var refused runstate.TriageCapError
	_, err = decisions.RecordRerun(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRerun, decidedRunID), docketedNow, docketedCaps)
	if !errors.As(err, &refused) {
		t.Fatalf("second RecordRerun() error = %v, want a cap refusal", err)
	}
	reruns, refusedByReruns := refused.RefusedBy(runstate.TriageRerunBudget)
	if !refusedByReruns || reruns.Spent != 1 || reruns.Cap != 1 {
		t.Fatalf("second RecordRerun() error = %v, want the re-run budget refusing it", err)
	}

	rebuilt, err := docketer.Build()
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if rebuilt.Added != 0 || len(rebuilt.Entries) != 1 {
		t.Fatalf("build = %#v, want the same one entry", rebuilt)
	}
	entry := rebuilt.Entries[0]
	if entry.Counters.Reruns != reruns.Spent || entry.Counters.RerunsCap != reruns.Cap {
		t.Fatalf("counters = %#v, want the %d of %d the guard refused against", entry.Counters, reruns.Spent, reruns.Cap)
	}
	if entry.Counters.RerunsCarriedOut != 0 || !entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want a decision recorded and not carried out", entry.Counters)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"1 of 1 re-run(s), 0 carried out",
		"already recorded and not yet carried out",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
}

// The third recurrence of the same divergence, replayed: a repair grant the
// round cap cut from two rounds to one, reported back with that detail as it was
// recorded, and a docket entry that carried a bare count of it — so the same
// decision was made again and refused by the budget rather than by the docket it
// was read off.
//
// What the entry has to carry is every figure the guard refused against, which
// is more than the count: what the grant came to, that the cap cut it, and what
// the item now stands committed to. The rounds counted alone say three of four
// used, which reads as room the guard does not have.
func TestAGrantTheRoundCapCutShowsOnTheDocketWithWhatARepeatWouldMeet(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	decisions, err := runstate.NewTriageStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	claimed, err := runstate.NewRerunStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, decisions, claimed)

	// The item has cost three of the four rounds its cap permits, one for each
	// developer attempt its runs were judged on.
	for _, attempt := range []string{"attempt-1", "attempt-2", "attempt-3"} {
		if _, err := decisions.RecordReviewRound(context.Background(), docketedItem, attempt, countingProcess, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound(%s) error = %v", attempt, err)
		}
	}
	// The development manager decides a repair. The configured grant is two
	// rounds and the cap has room for one, so the harness records the cut and
	// says so — which is the sentence the next docket contradicted.
	granted, err := decisions.GrantRepair(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRepair, decidedRunID),
		TriageRepairGrantRounds(docketedTriage), docketedNow, docketedCaps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if !granted.Truncated || granted.Requested != 2 || granted.Rounds != 1 {
		t.Fatalf("grant = %#v, want the round cap cutting two rounds to one", granted)
	}
	// The resubmission is refused by the guard, which is the proof the decision is
	// really recorded and the round-trip the docket is meant to save.
	var refused runstate.TriageCapError
	_, err = decisions.GrantRepair(context.Background(), docketedItem, triageDecided(runstate.TriageDecisionRepair, decidedRunID),
		TriageRepairGrantRounds(docketedTriage), docketedNow, docketedCaps)
	if !errors.As(err, &refused) {
		t.Fatalf("second GrantRepair() error = %v, want a cap refusal", err)
	}
	grants, refusedByGrants := refused.RefusedBy(runstate.TriageRepairGrantBudget)
	if !refusedByGrants || grants.Spent != 1 || grants.Cap != 1 {
		t.Fatalf("second GrantRepair() error = %v, want the repair grant budget refusing it", err)
	}
	// This item is at the end of both budgets, and the refusal says both at once
	// rather than sending the operator back for a second override ceremony.
	if _, refusedByRounds := refused.RefusedBy(runstate.TriageReviewRoundBudget); !refusedByRounds {
		t.Fatalf("second GrantRepair() error = %v, want the round budget named in the same refusal", err)
	}

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the one stoppage docketed", built)
	}
	entry := built.Entries[0]
	if entry.Counters.RepairGrants != grants.Spent || entry.Counters.RepairGrantsCap != grants.Cap {
		t.Fatalf("counters = %#v, want the %d of %d the guard refused against", entry.Counters, grants.Spent, grants.Cap)
	}
	if entry.Counters.GrantedRounds != 1 || entry.Counters.TruncatedGrants != 1 {
		t.Fatalf("counters = %#v, want the one round the cap cut the grant down to", entry.Counters)
	}
	if entry.Counters.CommittedRounds != 4 || entry.Counters.RoundsUncommitted() != 0 || !entry.Counters.Exhausted() {
		t.Fatalf("counters = %#v, want the whole cap committed, as the guard reads it", entry.Counters)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"1 of 1 repair grant(s) worth 1 review round(s), 1 of them cut down to the room the cap still had",
		"4 of 4 are committed by a grant not yet spent",
		// Both budgets are spent here, and the entry says both: crossing one and
		// meeting the other is the round-trip the docket exists to save.
		"A further repair grant for " + docketedItem + " is refused by both of its budgets: 1 of 1 permitted grant(s) are already recorded, and 4 of 4 round(s) are spent or committed",
		"Crossing either one alone leaves the other refusing it",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
}

// A stoppage the harness has already run again is the other half of the same
// gate: the decision is spent and so is the one re-run this entry gets, and an
// entry that still read as re-runnable is an entry somebody acts on and the
// harness refuses.
func TestAStoppageAlreadyReRunSaysSoRatherThanReadingAsAvailable(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	key := triage.Key(triage.ClassStoppedRun, stopped.RunID)
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 1}},
		claimed: map[string][]runstate.Rerun{docketedItem: {{
			DocketKey:  key,
			PriorRunID: stopped.RunID,
			WorkItemID: docketedItem,
			ClaimedAt:  docketedNow.Add(-time.Minute),
			RunID:      "run-fedcba9876543210fedcba9876543210",
		}}},
	}
	built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entry := built.Entries[0]
	if entry.Rerun == nil || entry.Rerun.RunID != "run-fedcba9876543210fedcba9876543210" {
		t.Fatalf("rerun = %#v, want the fresh run the claim started", entry.Rerun)
	}
	if entry.Counters.RerunsCarriedOut != 1 || entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want the decision recorded as carried out", entry.Counters)
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "already re-run as run run-fedcba9876543210fedcba9876543210") {
		t.Fatalf("the entry does not say this stoppage was re-run:\n%s", rendered)
	}
}

// A claim taken against another stoppage of the same item is not this entry's.
// The docket key is what the guard refuses on, so it is what the view reads:
// counting the item's claims alone would show a stoppage as spent that the
// harness would run again, and matching on the item would hide the one that is.
func TestAClaimAgainstAnotherStoppageIsNotReadAsThisOnes(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 2}},
		claimed: map[string][]runstate.Rerun{docketedItem: {{
			DocketKey:  triage.Key(triage.ClassStoppedRun, "run-99999999999999999999999999999999"),
			PriorRunID: "run-99999999999999999999999999999999",
			WorkItemID: docketedItem,
			ClaimedAt:  docketedNow.Add(-time.Hour),
			RunID:      "run-fedcba9876543210fedcba9876543210",
		}}},
	}
	built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entry := built.Entries[0]
	if entry.Rerun != nil {
		t.Fatalf("rerun = %#v, want no claim against this stoppage", entry.Rerun)
	}
	if entry.Counters.RerunsCarriedOut != 1 || !entry.Counters.Decided() {
		t.Fatalf("counters = %#v, want one of the two decisions carried out", entry.Counters)
	}
}

// A triage record that cannot be read is said out loud on the entry. Rendering
// it as zeros would describe an item nobody has decided anything about, which is
// the one reading that turns an unreadable record into a decision made twice.
func TestATriageRecordThatCannotBeReadIsStatedRatherThanShownAsNothingDecided(t *testing.T) {
	t.Parallel()

	recorded := &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 1}},
	}
	docketer := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, recorded, recorded)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	recorded.unreadable = docketedItem
	built, err := docketer.Build()
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("Build() error = %v, want the unreadable record reported", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the entry it had docketed", built)
	}
	entry := built.Entries[0]
	if entry.CountersProblem == "" {
		t.Fatalf("entry = %#v, want the unreadable record named on it", entry)
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "Triage decisions could not be read") ||
		!strings.Contains(rendered, "read the record before deciding") {
		t.Fatalf("the entry does not say the record could not be read:\n%s", rendered)
	}
	if strings.Contains(rendered, "Triage decisions recorded") {
		t.Fatalf("an unreadable record was rendered as decisions:\n%s", rendered)
	}
}

// Reading a docket without what has been carried out would report every
// decision as still standing, which is exactly the state a reader would spend
// twice. It refuses instead.
func TestReadingTheDocketWithoutWhatWasCarriedOutRefuses(t *testing.T) {
	t.Parallel()

	docketer := docketerOver([]runstate.State{stoppedState()}, &memoryDocket{})
	docketer.Reruns = nil
	if _, err := docketer.Build(); err == nil {
		t.Fatalf("Build() read a docket without the re-runs already carried out")
	}
}

// A publication docketed before the pull request joined the key is still on the
// docket, and a build that read only the current key would docket it a second
// time. The log is append-only, so both keys are what a reader has to answer to.
func TestAPublicationDocketedUnderTheOlderKeyIsNotDocketedAgain(t *testing.T) {
	t.Parallel()

	published := publishedState(3 * time.Hour)
	docket := &memoryDocket{}
	docketer := docketerOver([]runstate.State{published}, docket)
	entry, err := docketer.publicationEntry(published, docketedNow)
	if err != nil {
		t.Fatalf("publicationEntry() error = %v", err)
	}
	// The key an entry made before this change carries: the run alone.
	entry.Key = triage.Key(triage.ClassPublication, published.RunID)
	if _, err := docket.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}

	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 || len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the publication left as the one entry it already is", built)
	}
}

// A docket that cannot be built in full still delivers what it found: the
// entries beside the broken one are exactly the ones somebody needs.
func TestABuildThatCannotDocketEverythingStillReturnsWhatItFound(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	published := publishedState(3 * time.Hour)
	published.RunID = "run-fedcba9876543210fedcba9876543210"
	docket := &memoryDocket{failOn: string(triage.ClassPublication)}
	built, err := docketerOver([]runstate.State{stopped, published}, docket).Build()
	// The run that could not be docketed is named rather than failing the build,
	// so the sweep that built it finishes; a reader that says the docket is
	// partial still has it as one error.
	if err != nil {
		t.Fatalf("Build() error = %v, want the one run's refusal named rather than failing the build", err)
	}
	if len(built.NotDocketed) != 1 || built.NotDocketed[0].RunID != published.RunID || !strings.Contains(built.NotDocketed[0].Problem, "unwritable") {
		t.Fatalf("not docketed = %#v, want the publication's run and the refusal named", built.NotDocketed)
	}
	if incomplete := built.Incomplete(); incomplete == nil || !strings.Contains(incomplete.Error(), "unwritable") {
		t.Fatalf("Incomplete() = %v, want the refusal", incomplete)
	}
	if built.Added != 1 || len(built.Entries) != 1 || built.Entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("build = %#v, want the stoppage it could docket", built)
	}
}

func TestADocketerWithNothingWiredRefusesRatherThanReportingAnEmptyDocket(t *testing.T) {
	t.Parallel()

	if _, err := (Docketer{}).Build(); err == nil {
		t.Fatalf("Build() reported an empty docket without one being wired")
	}
	if _, err := (Docketer{}).RecordStoppedRun(stoppedState()); err == nil {
		t.Fatalf("RecordStoppedRun() reported a docketed run without a docket")
	}
}

// The whole point of the docket is that it arrives without an operator
// carrying it: the run that stops on a blocker is what puts itself on it,
// carrying the evidence it recorded on the way.
func TestARunThatSpendsItsRepairBudgetDocketsItselfAsItStops(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
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

	outcome, runErr := pipeline.Run(context.Background(), tracker.Item.ID)
	if runErr == nil || !outcome.Blocked {
		t.Fatalf("Run() error = %v, blocked = %t, want a run that spent its budget", runErr, outcome.Blocked)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the stopped run", entries)
	}
	entry := entries[0]
	if entry.Class != triage.ClassStoppedRun || entry.RunID != outcome.RunID || entry.WorkItemID != tracker.Item.ID {
		t.Fatalf("entry = %#v", entry)
	}
	// The blocker on the entry is the blocker on the item, in the same words:
	// an entry that paraphrased it would be a second account of one stoppage.
	if entry.Blocker != strings.TrimRight(tracker.BlockReason, "\n") {
		t.Fatalf("docketed blocker is not the one recorded on the item:\n%s\n---\n%s", entry.Blocker, tracker.BlockReason)
	}
	if len(entry.Findings) != 1 || entry.Findings[0].Message != "add the missing file" {
		t.Fatalf("findings = %#v, want the reviewer's own words", entry.Findings)
	}
	if entry.Artifacts.WorktreePath != outcome.WorktreePath || entry.Artifacts.Branch != outcome.Branch {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entry.Artifacts)
	}
	// Two verdicts were obtained — the first one and the one after the repair —
	// against a cap of four, and that is what a grant would be decided against.
	// The rounds are the item's own triage record rather than a second count made
	// from the runs, because that record is what the grant is truncated against.
	want := triage.Counters{
		ReviewRounds: 2, ReviewRoundsCap: 4, RepairAttempts: 1, RepairGrantAttempts: 2,
		RepairGrantsCap: 1, RerunsCap: 1, MergeRearmsCap: 1,
		CrossingsBound: runstate.MaxDelegatedCapCrossings,
	}
	if entry.Counters != want {
		t.Fatalf("counters = %#v, want %#v", entry.Counters, want)
	}
	// The rounds are durable, so the next run of this item is measured against
	// what the last one already spent rather than starting the argument over.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.ReviewRounds != 2 || state.Blocker == "" {
		t.Fatalf("state = %#v, want the rounds and the blocker recorded", state)
	}
}

// A run whose process died never docketed itself. The sweep that settles it is
// what dockets the stoppage, and doing that twice — the run and then the sweep,
// or two sweeps — must leave one entry.
func TestASweepDocketsARunItStopsAndNeverDocketsItTwice(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseReviewing}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}

	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	docketer := docketerOverStore(docket, store, pipeline.Config)
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketer,
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked", results)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != pipelineRunID {
		t.Fatalf("docket = %#v, want the run the sweep stopped", entries)
	}
	if !strings.Contains(entries[0].Blocker, "No developer was restarted") {
		t.Fatalf("docketed blocker is not the one the sweep recorded:\n%s", entries[0].Blocker)
	}
	if entries[0].Artifacts.WorktreePath == "" || entries[0].Artifacts.Branch == "" {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entries[0].Artifacts)
	}

	// A later scan finds the same stoppage on the same run and dockets nothing:
	// the entry is keyed to the stoppage rather than to the noticing.
	built, err := docketer.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 || len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the one entry already docketed", built)
	}
}

// A stoppage that could not be docketed is a delivery that did not happen, not
// a run that has to be settled again. Reporting it as a failed settlement would
// describe a settled run as outstanding, and nothing would ever correct that.
func TestASweepThatCannotDocketStillSettlesTheRun(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseReviewing}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}

	docket := &memoryDocket{failOn: string(triage.ClassStoppedRun)}
	results, err := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
	}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the run settled as blocked", results)
	}
	if !strings.Contains(results[0].DocketProblem, "unwritable") {
		t.Fatalf("docket problem = %q, want the refusal named", results[0].DocketProblem)
	}
	// The blocker is durable on the run, so the delivery that failed here is
	// made by the next build rather than lost with the sweep.
	settled, err := store.Load(results[0].RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Blocker == "" {
		t.Fatalf("settled run = %#v, want the blocker recorded on it", settled)
	}
}

// The entry made about work that never started. It is the whole of what the
// development manager gets — there is no branch to look at, no reviewer's words,
// and no failing check — so what it must carry is what the item asks for, the
// read that says the tree does not have it, and who releases it.
func TestAnUnreadyItemIsDocketedWithWhatIsMissingAndWhoDecides(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerDeciding(nil, docket, &recordedDecisions{}, &recordedDecisions{})
	docketer.ProductID = "yoyodyne"
	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.100.1",
		Title: "Commit and publish an approved artifact write",
	}
	unmet := []readiness.Unmet{{
		Kind:     readiness.KindForbiddenByRuling,
		Missing:  `it says of itself: "Blocked until the architect's answer exists"`,
		Evidence: "the sentence is in the item's own statement",
		Decides:  "the product manager, or the development manager who records the dependency",
	}}

	created, err := docketer.RecordUnreadyItem(item, unmet)
	if err != nil {
		t.Fatalf("RecordUnreadyItem() error = %v", err)
	}
	if !created {
		t.Fatal("RecordUnreadyItem() created nothing, want the finding docketed")
	}
	entry := docket.entries[0]
	if entry.Class != triage.ClassUnreadyItem || entry.RunID != "" {
		t.Fatalf("entry = %+v, want an unready item with no run behind it", entry)
	}
	if entry.WorkItemTitle != item.Title || entry.ProductID != "yoyodyne" {
		t.Fatalf("entry = %+v, want the item and its product named", entry)
	}
	if entry.Unready == nil || len(entry.Unready.Prerequisites) != 1 {
		t.Fatalf("entry.Unready = %+v, want the one unmet prerequisite carried", entry.Unready)
	}
	carried := entry.Unready.Prerequisites[0]
	if carried.Kind != string(readiness.KindForbiddenByRuling) || carried.Missing != unmet[0].Missing ||
		carried.Evidence != unmet[0].Evidence || carried.Decides != unmet[0].Decides {
		t.Fatalf("prerequisite = %+v, want the reading carried whole", carried)
	}
	// The budgets travel with it for the reason they travel with every entry: what
	// may still be decided about this item is the same question whether it stopped
	// or never started.
	if entry.Counters.ReviewRoundsCap != docketedTriage.ReviewRoundsCap {
		t.Fatalf("counters = %+v, want the configured ceilings beside the finding", entry.Counters)
	}

	// A watching session meets the same unready item at every poll. It is one
	// finding, so it is one entry.
	createdAgain, err := docketer.RecordUnreadyItem(item, unmet)
	if err != nil {
		t.Fatalf("RecordUnreadyItem() error = %v", err)
	}
	if createdAgain || len(docket.entries) != 1 {
		t.Fatalf("docket = %v, want the same finding docketed once", docket.keys())
	}
}

// An item that meets everything it states dockets nothing and is not an error,
// which is nearly every item.
func TestAnItemWithNothingUnmetIsNotDocketed(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerDeciding(nil, docket, &recordedDecisions{}, &recordedDecisions{})

	created, err := docketer.RecordUnreadyItem(beads.WorkItem{ID: "yoyodyne-ifd.304"}, nil)
	if err != nil {
		t.Fatalf("RecordUnreadyItem() error = %v", err)
	}
	if created || len(docket.entries) != 0 {
		t.Fatalf("docket = %v, want nothing said about a ready item", docket.keys())
	}
	if _, err := (Docketer{}).RecordUnreadyItem(beads.WorkItem{ID: "x"}, []readiness.Unmet{{Kind: "k", Missing: "m"}}); err == nil {
		t.Fatal("RecordUnreadyItem() routed a finding with no docket wired")
	}
}

// diedBeforeItStarted is the yoyodyne-ifd.285 shape: a dispatch that died at the
// claim. Nothing was taken, nothing was cut, and the item is exactly as the
// scheduler found it — which is why every other rule on this docket reads it as
// nothing having happened.
func diedBeforeItStarted() runstate.State {
	completed := docketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         docketedRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    docketedItem,
		WorkItemTitle: docketedTitle,
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		StartedAt:     completed.Add(-time.Second),
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		Failure:       "claim work item: bd update failed with status failed and exit code 1: Error claiming yoyodyne-task: issue not claimable: status blocked",
	}
}

// The half of yoyodyne-ifd.338 the operator asked about directly: a run that
// fails before claiming left no docket entry, so it existed in no surface the
// development manager's sweep reads. Every other stoppage class dockets and this
// one evaporated — yoyodyne-ifd.285 was dispatched twenty-nine times in twenty
// hours and the harness's own next-mover line said only that nothing was
// recorded for anybody to decide.
func TestARunThatDiedBeforeItClaimedItsItemIsDocketed(t *testing.T) {
	t.Parallel()

	died := diedBeforeItStarted()
	if err := died.Validate(); err != nil {
		t.Fatalf("the death is not a state the harness could record: %v", err)
	}
	// The rules that carried every other stoppage say nothing about this one,
	// which is the whole of why it needed a class.
	if stoppedRun(died) || diedHolding(died, triage.Found{}) {
		t.Fatal("a pre-claim death now reads as a stoppage the older rules catch; this test no longer measures the gap")
	}

	docket := &memoryDocket{}
	created, err := docketerOver(nil, docket).RecordUnstartedRun(died)
	if err != nil || !created {
		t.Fatalf("a death before the claim reached nobody: created = %t, error = %v", created, err)
	}
	entry := docket.entries[0]
	if entry.Class != triage.ClassUnstartedRun || entry.Key != triage.Key(triage.ClassUnstartedRun, docketedRunID) {
		t.Fatalf("entry = %#v, want the unstarted run keyed to it", entry)
	}
	// Keyed to the run and naming the item it tried to claim, which is the whole
	// of what somebody has to go and look at.
	if entry.RunID != docketedRunID || entry.WorkItemID != docketedItem || entry.WorkItemTitle != docketedTitle {
		t.Fatalf("entry names %s/%s (%q), want the run and the item it tried to claim", entry.RunID, entry.WorkItemID, entry.WorkItemTitle)
	}
	if entry.Failure != died.Failure {
		t.Fatalf("failure = %q, want the reason the run gave %q", entry.Failure, died.Failure)
	}
	if entry.Blocker != "" || entry.Check != nil || len(entry.Findings) != 0 {
		t.Fatalf("entry = %#v, want nothing about a change that was never made", entry)
	}
	// The counters travel for the reason every other entry's do: what the item can
	// still afford is the same question whether it stopped or never started.
	if entry.Counters.ReviewRounds != 3 || entry.Counters.ReviewRoundsCap != docketedCaps.ReviewRounds {
		t.Fatalf("counters = %#v, want the item's own record beside the caps", entry.Counters)
	}
	rendered := entry.Render()
	if !strings.Contains(rendered, "died before it claimed") || !strings.Contains(rendered, died.Failure) {
		t.Fatalf("the rendered entry does not say what happened:\n%s", rendered)
	}
	// And it must not read as the stoppage it is not. "Died holding its change" is
	// exactly what did not happen here, and a development manager who read it
	// would go looking for a branch nobody made.
	if strings.Contains(rendered, "Died holding its change") {
		t.Fatalf("the entry claims a change that was never made:\n%s", rendered)
	}
	// Docketing is one event however many times it is recorded.
	again, err := docketerOver(nil, docket).RecordUnstartedRun(died)
	if err != nil || again {
		t.Fatalf("the same death was docketed twice: created = %t, error = %v", again, err)
	}
}

// The runs this must not docket. A run that got as far as claiming is described
// by the rules that were already there, and a run that never started must not be
// re-derived from a history where the claim was not recorded at all.
func TestADeathAfterTheClaimIsNotDocketedAsUnstarted(t *testing.T) {
	t.Parallel()

	claimed := docketedNow.Add(-2 * time.Hour)
	for _, test := range []struct {
		name  string
		state runstate.State
	}{
		{
			// It cut a worktree, so it claimed, whatever its record says about when.
			name:  "it died holding its change",
			state: diedHoldingItsChange(),
		},
		{
			name: "it recorded the claim and then failed",
			state: func() runstate.State {
				died := diedBeforeItStarted()
				died.WorkItemClaimedAt = &claimed
				return died
			}(),
		},
		{
			// A deliberate cancellation is not a death, and nothing about it is a
			// decision somebody has to take.
			name: "the operator cancelled it before it claimed",
			state: func() runstate.State {
				died := diedBeforeItStarted()
				died.Status = runstate.StatusCancelled
				return died
			}(),
		},
		{
			// A death that said nothing about why is an entry nobody could act on.
			name: "it recorded no reason for dying",
			state: func() runstate.State {
				died := diedBeforeItStarted()
				died.Failure = ""
				return died
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			docket := &memoryDocket{}
			created, err := docketerOver(nil, docket).RecordUnstartedRun(test.state)
			if err != nil {
				t.Fatalf("RecordUnstartedRun() error = %v", err)
			}
			if created || len(docket.entries) != 0 {
				t.Fatalf("it was docketed as an unstarted run: %#v", docket.entries)
			}
		})
	}
}

// The scan must not backfill these, for preservedDeath's reason and a sharper one
// of its own: the claim time is a field yoyodyne-ifd.338 added, so every run
// recorded before it reads as unclaimed however far it actually got. A build that
// re-derived this would bury the entries the development manager is there to
// decide about under a history of settled failures.
func TestTheScanDoesNotBackfillPreClaimDeathsFromTheRecordedHistory(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	built, err := docketerOver([]runstate.State{diedBeforeItStarted()}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if built.Added != 0 {
		t.Fatalf("the scan backfilled a pre-claim death from the history: %#v", docket.entries)
	}
}

// The yoyodyne-ifd.285 shape replayed end to end: the harness dispatches an item
// whose claim the tracker refuses, and what the development manager's docket
// holds afterwards is a record of it.
//
// This is the acceptance yoyodyne-ifd.338 was admitted for. Before it, the run
// recorded the failure and stopped there: no blocker was written, because the
// item was never claimed, and no worktree was cut, so both rules that put a
// stoppage on the docket read the run as nothing having happened. The scheduler
// then pulled the same item again twenty-two minutes later, twenty-nine times
// over twenty hours, and no surface said a word.
func TestADispatchTheTrackerRefusesReachesTheDocket(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	refusal := errors.New("bd update failed with status failed and exit code 1: Error claiming yoyodyne-task: issue not claimable: status blocked")
	tracker := &orchestratortest.Tracker{
		Item:    beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "blocked"},
		OnClaim: func() error { return refusal },
	}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); !errors.Is(err, refusal) {
		t.Fatalf("Run() error = %v, want the tracker's refusal", err)
	}

	// The durable record first: the run says it never claimed, which is what
	// separates this from every stoppage that left work behind.
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.WorkItemClaimedAt != nil || recorded.WorktreePath != "" {
		t.Fatalf("run = %#v, want a run that took nothing and cut nothing", recorded)
	}

	// And the docket, which is what the development manager's sweep reads. It is
	// built from the store rather than taken from the write above, because what is
	// being claimed is that somebody eventually looks.
	built, err := docketerOverStore(docket, store, pipeline.Config).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("docket = %#v, want the refused dispatch on it", built.Entries)
	}
	entry := built.Entries[0]
	if entry.Class != triage.ClassUnstartedRun || entry.RunID != pipelineRunID || entry.WorkItemID != tracker.Item.ID {
		t.Fatalf("entry = %#v, want the unstarted run keyed to the item it tried to claim", entry)
	}
	if !strings.Contains(entry.Failure, "not claimable: status blocked") {
		t.Fatalf("entry failure = %q, want what the tracker refused with", entry.Failure)
	}
	if !strings.Contains(entry.Render(), "died before it claimed") {
		t.Fatalf("the rendered entry does not say the dispatch never started:\n%s", entry.Render())
	}
}

// The docket's own half of the 2026-09-07 conflation. Every entry described a
// stoppage and none of them said whether what it was waiting for was a decision
// or the carrying out of one, so a docket of already-decided work read as work
// the development manager owed decisions on. Each entry now names its next mover.
func TestADocketEntrySaysWhetherItWaitsOnHerOrOnTheHarness(t *testing.T) {
	t.Parallel()

	stopped := stoppedState()
	undecided := &recordedDecisions{}
	built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, undecided, undecided).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rendered := built.Entries[0].Render(); !strings.Contains(rendered, "Next mover: you — nothing the harness has still to carry out is recorded about this stoppage") {
		t.Fatalf("an undecided stoppage does not name her as the next mover:\n%s", rendered)
	}

	// A re-run recorded against this stoppage and not yet claimed. The decision is
	// named rather than only the counter, because the counter is the item's total
	// and a total cannot say which of an item's runs was decided about.
	decided := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: {
		Reruns:    1,
		Decisions: []runstate.TriageDecision{triageDecided(runstate.TriageDecisionRerun, stopped.RunID)},
	}}}
	built, err = docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, decided, decided).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rendered := built.Entries[0].Render(); !strings.Contains(rendered, "Next mover: the harness — a decision about this stoppage is already recorded and has not been carried out") {
		t.Fatalf("a decided stoppage does not name the harness as the next mover:\n%s", rendered)
	}

	// And a decision recorded about some other run of the same item is not this
	// stoppage's. The item's re-run counter says a decision was made; the docket
	// entry is about a run nobody decided anything about, and naming the harness
	// over it sends an operator to watch for a run nothing is going to start.
	elsewhere := &recordedDecisions{counters: map[string]runstate.TriageCounters{docketedItem: {
		Reruns:    1,
		Decisions: []runstate.TriageDecision{triageDecided(runstate.TriageDecisionRerun, "run-fedcba9876543210fedcba9876543210")},
	}}}
	built, err = docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, elsewhere, elsewhere).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if rendered := built.Entries[0].Render(); !strings.Contains(rendered, "Next mover: you — nothing the harness has still to carry out is recorded about this stoppage") {
		t.Fatalf("a decision about another run of the item was read as this stoppage's:\n%s", rendered)
	}

	// And a record nobody could open says that rather than guessing, for the
	// reason the decisions below it do: an unreadable record read as an item
	// nobody has decided about is how one authorized recovery is nearly spent
	// twice.
	unreadable := &recordedDecisions{}
	docketer := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, unreadable, unreadable)
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	unreadable.unreadable = docketedItem
	built, _ = docketer.Build()
	if len(built.Entries) != 1 {
		t.Fatalf("built = %#v, want the entry the docket already held", built)
	}
	if rendered := built.Entries[0].Render(); !strings.Contains(rendered, "Next mover: unknown") {
		t.Fatalf("an unreadable record was given a next mover:\n%s", rendered)
	}
}

// A repair grant recorded and unspent is the other decision the harness has
// still to carry out, and the counters say so without a re-run among them. It is
// the grant that answers rather than the decision, because a repair continues the
// run it was granted for: the same run stops again carrying the same decision, so
// the decision alone would go on claiming a carry-out that has already happened.
func TestAnOutstandingGrantIsADecisionTheHarnessHasStillToCarryOut(t *testing.T) {
	t.Parallel()

	granted := runstate.TriageCounters{
		RepairGrants:    1,
		CommittedRounds: 3,
		ReviewRounds:    2,
		Decisions:       []runstate.TriageDecision{triageDecided(runstate.TriageDecisionRepair, docketedRunID)},
	}
	outstanding := triage.Counters{Standing: granted.Standing(docketedRunID)}
	if !outstanding.AwaitingCarryOut() {
		t.Fatalf("counters = %#v, want the unspent grant read as a carry-out outstanding", outstanding)
	}
	granted.ReviewRounds = 3
	spent := triage.Counters{Standing: granted.Standing(docketedRunID)}
	if spent.AwaitingCarryOut() {
		t.Fatalf("counters = %#v, want a grant whose rounds are spent read as carried out", spent)
	}
}

// The entry made about a dispatch that produced no run record at all. It is the
// only thing that will ever say the attempt happened: no run was reserved, so no
// sweep walks past it and nothing downstream of the run store can re-derive it.
func TestAnAttemptThatNeverBecameARunIsDocketedWithWhatWasTriedAndWhyItFailed(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerDeciding(nil, docket, &recordedDecisions{}, &recordedDecisions{})
	docketer.ProductID = "yoyodyne"
	attempt := UnstartedAttempt{
		WorkItemID:            "yoyodyne-ifd.353",
		WorkItemTitle:         "A stall the watchdog can see is one the operator is told about",
		SelectedBecause:       "first in the product manager's order of 74 admitted items, 12 of them pullable",
		Failure:               "repository is not ready for an isolated run: the primary checkout has uncommitted changes",
		ExcludedForTheSession: true,
	}

	created, err := docketer.RecordUnstartedAttempt(attempt)
	if err != nil {
		t.Fatalf("RecordUnstartedAttempt() error = %v", err)
	}
	if !created {
		t.Fatal("RecordUnstartedAttempt() created nothing, want the attempt docketed")
	}
	entry := docket.entries[0]
	if entry.Class != triage.ClassUnstartedAttempt || entry.RunID != "" {
		t.Fatalf("entry = %+v, want an attempt with no run behind it", entry)
	}
	if entry.WorkItemID != attempt.WorkItemID || entry.WorkItemTitle != attempt.WorkItemTitle || entry.ProductID != "yoyodyne" {
		t.Fatalf("entry = %+v, want the item and its product named", entry)
	}
	if entry.Failure != attempt.Failure {
		t.Fatalf("entry.Failure = %q, want the failure that stopped the dispatch", entry.Failure)
	}
	if entry.Attempt == nil || entry.Attempt.SelectedBecause != attempt.SelectedBecause || !entry.Attempt.ExcludedForTheSession {
		t.Fatalf("entry.Attempt = %+v, want the selection and the exclusion carried", entry.Attempt)
	}
	// The budgets travel with it for the reason they travel with every entry: what
	// may still be decided about this item is the same question whether it stopped,
	// never started, or never got as far as a run.
	if entry.Counters.ReviewRoundsCap != docketedTriage.ReviewRoundsCap {
		t.Fatalf("counters = %+v, want the configured ceilings beside the entry", entry.Counters)
	}

	// A session that meets the same failure about the same item again has met one
	// standing fact, so it is one entry.
	createdAgain, err := docketer.RecordUnstartedAttempt(attempt)
	if err != nil {
		t.Fatalf("RecordUnstartedAttempt() error = %v", err)
	}
	if createdAgain || len(docket.entries) != 1 {
		t.Fatalf("docket = %v, want the same dead dispatch docketed once", docket.keys())
	}

	// A different failure about the same item is a different fact, and one the
	// development manager has not been told.
	attempt.Failure = "the claude-code backend is not authenticated"
	createdAgain, err = docketer.RecordUnstartedAttempt(attempt)
	if err != nil {
		t.Fatalf("RecordUnstartedAttempt() error = %v", err)
	}
	if !createdAgain || len(docket.entries) != 2 {
		t.Fatalf("docket = %v, want a dispatch failing a new way recorded", docket.keys())
	}
}

// An attempt with nothing wrong with it dockets nothing and is not an error,
// which is nearly every attempt; and one with nowhere to be recorded says so
// rather than reporting a record that was never made.
func TestAnAttemptWithNoFailureIsNotDocketed(t *testing.T) {
	t.Parallel()

	docket := &memoryDocket{}
	docketer := docketerDeciding(nil, docket, &recordedDecisions{}, &recordedDecisions{})

	created, err := docketer.RecordUnstartedAttempt(UnstartedAttempt{
		WorkItemID:      "yoyodyne-ifd.353",
		SelectedBecause: "first in the order",
	})
	if err != nil {
		t.Fatalf("RecordUnstartedAttempt() error = %v", err)
	}
	if created || len(docket.entries) != 0 {
		t.Fatalf("docket = %v, want nothing said about a dispatch that did not fail", docket.keys())
	}
	if _, err := (Docketer{}).RecordUnstartedAttempt(UnstartedAttempt{
		WorkItemID:      "x",
		SelectedBecause: "first in the order",
		Failure:         "the dispatch died",
	}); err == nil {
		t.Fatal("RecordUnstartedAttempt() recorded an attempt with no docket wired")
	}
}

// A waited run is carried apart from the open entries, one per run, and a run
// docketed again since the wait is not: its open entry speaks for it.
func TestWaitingDocketFoldsTheWaitsAndLeavesOutARunAskedAgain(t *testing.T) {
	t.Parallel()

	now := docketedNow
	waitedEntry := func(class triage.Class, run string, recorded time.Time) triage.Entry {
		return triage.Entry{SchemaVersion: triage.SchemaVersion, Key: triage.Key(class, run), Class: class, RunID: run, WorkItemID: "item-" + run,
			RecordedAt: recorded, Closed: &triage.Closure{Decision: "wait", ClosedAt: now.Add(-time.Hour), RevisitAfter: now.Add(time.Hour)}}
	}
	settled := waitedEntry(triage.ClassStoppedRun, "run-settled", now.Add(-5*time.Hour))
	settled.Closed.RevisitAfter = time.Time{}
	settled.Closed.Decision = "rescope"
	entries := []triage.Entry{
		waitedEntry(triage.ClassStoppedRun, "run-waited", now.Add(-4*time.Hour)),
		waitedEntry(triage.ClassPublication, "run-waited", now.Add(-3*time.Hour)),
		waitedEntry(triage.ClassStoppedRun, "run-asked-again", now.Add(-2*time.Hour)),
		settled,
	}
	open := []triage.Entry{{Key: triage.Key(triage.ClassStoppedRun, "run-asked-again-later"), RunID: "run-asked-again", RecordedAt: now}}

	listable, waiting, unlisted := listableDocket(entries, now)
	if len(listable) != 0 || unlisted != 4 || len(waiting) != 3 {
		t.Fatalf("listable %d, waiting %d, unlisted %d, want 0, 3, 4", len(listable), len(waiting), unlisted)
	}
	kept := waitingDocket(waiting, open)
	if len(kept) != 1 || kept[0].RunID != "run-waited" || len(kept[0].Earlier) != 1 {
		t.Fatalf("waiting docket = %#v, want the waited run folded once and the run asked again left out", kept)
	}
}
