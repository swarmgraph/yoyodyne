package orchestrator

// The list of unpublished pull requests while the decision on what may start
// under the named epics refuses to bring a change up to date.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// listingEpics is epic mode on over a real record, with the decision the list
// reads and the one a start asks.
type listingEpics struct {
	root     string
	operator *epicscope.Operator
}

func newListingEpics(t *testing.T) listingEpics {
	t.Helper()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: []string{blockedMember}}, baseTime); err != nil {
		t.Fatal(err)
	}
	return listingEpics{root: root, operator: operator}
}

func (e listingEpics) would(start maystart.Start) maystart.Decision {
	return maystart.Decide(e.root, "yoyodyne", start)
}

func (e listingEpics) ask(start maystart.Start) maystart.Decision {
	return maystart.Ask(e.root, "yoyodyne", start, baseTime)
}

// refusals is every refused start kept.
func (e listingEpics) refusals(t *testing.T) maystart.Refusals {
	t.Helper()
	kept, err := readmodel.NewEpicRecords(e.root, domain.ProductID("yoyodyne")).NotStarted()
	if err != nil {
		t.Fatal(err)
	}
	return kept
}

func (e listingEpics) add(t *testing.T) {
	t.Helper()
	if _, err := e.operator.Add(namedEpic, "yoyodyne-task", baseTime); err != nil {
		t.Fatal(err)
	}
}

func (e listingEpics) turnOff(t *testing.T) {
	t.Helper()
	if _, err := e.operator.TurnOff(); err != nil {
		t.Fatal(err)
	}
}

func (e listingEpics) corrupt(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(e.root, "yoyodyne"), "epic-scope.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unpublishedChange is a change past the listing age whose update the named
// epics refuse, as its run and the check that met the refusal left it.
type unpublishedChange struct {
	state runstate.State
	path  maystart.Path
}

// unpublishedChanges drives each update to its refusal, over the decision given.
func unpublishedChanges(t *testing.T, ask StartDecision) map[string]unpublishedChange {
	t.Helper()
	changes := map[string]unpublishedChange{}
	for name, dropped := range map[string]bool{"queued on GitHub": false, "dropped by GitHub": true} {
		fixture, forge, outcome := queuedOnProtectedTarget(t)
		driftRemoteTarget(t, fixture.remote, "main")
		forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1}
		if dropped {
			forge.DropQueuedMerge()
		} else {
			forge.Reading.Failing = []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}}
		}
		fixture.docket = &memoryDocket{}
		reconciler := fixture.sweep(t, forge, true)
		reconciler.Drops, reconciler.StuckMergeAge, reconciler.MayStart = forge, stuckLimit, ask
		reconciler.Clock = sweepClock{now: outcome.PullRequest.QueuedAt.Add(13 * time.Hour)}
		if _, err := reconciler.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		state, err := fixture.store.Load(pipelineRunID)
		if err != nil {
			t.Fatal(err)
		}
		changes[name] = unpublishedChange{state, maystart.UpdateQueued}
	}
	fixture := newPersonMergeUpdateFixture(t)
	driftRemoteTarget(t, fixture.remote, "main")
	reconciler := fixture.reconciler(t)
	reconciler.MayStart = ask
	if _, err := reconciler.SettlePersonMerges(context.Background()); err != nil {
		t.Fatal(err)
	}
	changes["waiting for a person"] = unpublishedChange{fixture.load(t), maystart.UpdateForPerson}
	return changes
}

// listed is whether the run's publication is on the list the docket builds.
func listed(t *testing.T, docket *memoryDocket, state runstate.State, would StartDecision) bool {
	t.Helper()
	docketer := docketerOver([]runstate.State{state}, docket)
	docketer.WouldStart = would
	docketer.Clock = docketClockAt{at: state.CompletedAt.Add(13 * time.Hour)}
	build, err := docketer.Build()
	if err != nil || len(build.NotDocketed) != 0 {
		t.Fatalf("Build() = %+v, %v", build, err)
	}
	if build.Closed != 0 {
		t.Fatalf("Build() counts %d entries as settled, want none", build.Closed)
	}
	for _, entry := range build.Entries {
		if entry.Class == triage.ClassPublication && entry.RunID == state.RunID {
			return true
		}
	}
	return false
}

// A change the named epics keep from being brought up to date is not listed, is
// listed once its item is added or epic mode is off, and the reading is kept
// nowhere, so the refused start already recorded stands exactly as it was.
func TestAChangeTheNamedEpicsKeepFromItsUpdateIsNotListed(t *testing.T) {
	t.Parallel()
	setup := newListingEpics(t)
	for name, change := range unpublishedChanges(t, setup.would) {
		for how, allow := range map[string]func(listingEpics, *testing.T){"added": listingEpics.add, "epic mode off": listingEpics.turnOff} {
			t.Run(name+", "+how, func(t *testing.T) {
				t.Parallel()
				epics, docket := newListingEpics(t), &memoryDocket{}
				if listed(t, docket, change.state, epics.would) || len(docket.entries) != 0 {
					t.Fatal("listed while its update is refused")
				}
				if kept := epics.refusals(t); !reflect.DeepEqual(kept.Items, []maystart.RefusedItem(nil)) {
					t.Fatalf("building the list kept %+v", kept.Items)
				}
				epics.ask(maystart.Run(change.path, change.state.WorkItemID))
				before := epics.refusals(t)
				if len(before.Items) != 1 {
					t.Fatalf("refused starts = %+v, want the update's", before.Items)
				}
				if listed(t, docket, change.state, epics.would) || !reflect.DeepEqual(epics.refusals(t), before) {
					t.Fatalf("refused starts = %+v, want them as they were and the change unlisted", epics.refusals(t))
				}
				allow(epics, t)
				if !listed(t, docket, change.state, epics.would) {
					t.Fatal("not listed once it may be brought up to date and is still unpublished")
				}
			})
		}
		t.Run(name+", listed before epic mode", func(t *testing.T) {
			t.Parallel()
			epics, docket := newListingEpics(t), &memoryDocket{}
			if !listed(t, docket, change.state, nil) || listed(t, docket, change.state, epics.would) {
				t.Fatal("want it listed with no epic named and left out while its update is refused")
			}
			// A decision of hers the harness could not carry out is still put to her.
			entry := docket.entries[0]
			entry.Closed = &triage.Closure{Decision: runstate.TriageDecisionRerun, ClosedAt: baseTime}
			entry.CarryOut = &triage.CarryOut{RefusedAt: baseTime.Add(time.Minute)}
			docketer := Docketer{WouldStart: epics.would}
			runs := map[string]runstate.State{change.state.RunID: change.state}
			if kept := docketer.withoutRefusedUpdates([]triage.Entry{entry}, runs, baseTime); len(kept) != 1 {
				t.Fatal("an entry back because its decision could not be carried out was left out")
			}
			epics.add(t)
			if kept := docketer.withoutRefusedUpdates([]triage.Entry{docket.entries[0]}, runs, baseTime); len(kept) != 1 {
				t.Fatal("an entry listed before epic mode does not come back once its item is added")
			}
		})
		t.Run(name+", listed as ever", func(t *testing.T) {
			t.Parallel()
			epics := newListingEpics(t)
			for why, alter := range map[string]func(*runstate.State){
				"closed":                                 func(s *runstate.State) { s.PullRequest.State = "CLOSED" },
				"its merge failed":                       func(s *runstate.State) { s.PublishFailure = "the merge was refused" },
				"the harness cannot bring it up to date": func(s *runstate.State) { s.WorktreeRemoved = true },
			} {
				state := change.state
				published := *state.PullRequest
				state.PullRequest = &published
				alter(&state)
				if !listed(t, &memoryDocket{}, state, epics.would) {
					t.Errorf("%s: not listed", why)
				}
			}
			epics.corrupt(t)
			if !listed(t, &memoryDocket{}, change.state, epics.would) {
				t.Error("not listed where the decision could not be made")
			}
		})
	}
}
