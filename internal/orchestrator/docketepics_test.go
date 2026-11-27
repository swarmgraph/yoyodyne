package orchestrator

// The list of unpublished pull requests while the decision on what may start
// under the named epics refuses to bring a change up to date. A change is left
// off only while its update is due, which the refused start the check kept,
// naming the run, is what shows (naming-an-epic-again, points 4 to 6).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
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

// askingAt is the decision a check asks, keeping its answer as made at the time
// given.
func (e listingEpics) askingAt(at time.Time) StartDecision {
	return func(start maystart.Start) maystart.Decision {
		return maystart.Ask(e.root, "yoyodyne", start, at)
	}
}

// notDue is the one removal a check makes, over this record.
func (e listingEpics) notDue(t *testing.T) func(maystart.Path, string, string) {
	return func(path maystart.Path, item, run string) {
		if err := maystart.UpdateNotDue(e.root, "yoyodyne", path, item, run); err != nil {
			t.Errorf("UpdateNotDue() = %v", err)
		}
	}
}

func (e listingEpics) readRefusals() (maystart.Refusals, error) {
	return maystart.ReadRefusals(e.root, "yoyodyne")
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

func (e listingEpics) corrupt(t *testing.T, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home.ProductDirectory(e.root, "yoyodyne"), file), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unpublishedChange is a change past the listing age whose update is due, as
// its run and a check that met the refusal left it.
type unpublishedChange struct {
	state runstate.State
	path  maystart.Path
	// at is when the check was made.
	at time.Time
	// check makes the check again, at the same time, with GitHub's answer
	// changed first, and returns the run as it left it.
	check func(t *testing.T, answer func(*orchestratortest.CheckedForge)) runstate.State
}

var unpublishedKinds = []string{"queued on GitHub", "dropped by GitHub", "waiting for a person"}

// unpublishedChangeOf drives one kind of change to the check that finds its
// update due, asking the decision of epics and taking refusals away from it.
func unpublishedChangeOf(t *testing.T, kind string, epics listingEpics, hosts bool) unpublishedChange {
	t.Helper()
	if kind == "waiting for a person" {
		fixture := newPersonMergeUpdateFixture(t)
		driftRemoteTarget(t, fixture.remote, "main")
		at := fixture.run.CompletedAt.Add(13 * time.Hour)
		check := func(t *testing.T, answer func(*orchestratortest.CheckedForge)) runstate.State {
			t.Helper()
			answer(fixture.forge)
			reconciler := fixture.reconciler(t)
			reconciler.HostsRuns = hosts
			reconciler.MayStart, reconciler.UpdateNotDue = epics.askingAt(at), epics.notDue(t)
			reconciler.Clock = sweepClock{now: at}
			if _, err := reconciler.SettlePersonMerges(context.Background()); err != nil {
				t.Fatal(err)
			}
			return fixture.load(t)
		}
		return unpublishedChange{check(t, func(*orchestratortest.CheckedForge) {}), maystart.UpdateForPerson, at, check}
	}
	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1}
	if kind == "dropped by GitHub" {
		forge.DropQueuedMerge()
	} else {
		forge.Reading.Failing = []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}}
	}
	fixture.docket = &memoryDocket{}
	at := outcome.PullRequest.QueuedAt.Add(13 * time.Hour)
	check := func(t *testing.T, answer func(*orchestratortest.CheckedForge)) runstate.State {
		t.Helper()
		answer(forge)
		reconciler := fixture.sweep(t, forge, hosts)
		reconciler.Drops, reconciler.MayStart, reconciler.UpdateNotDue = forge, epics.askingAt(at), epics.notDue(t)
		reconciler.Clock = sweepClock{now: at}
		if _, err := reconciler.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		state, err := fixture.store.Load(pipelineRunID)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	return unpublishedChange{check(t, func(*orchestratortest.CheckedForge) {}), maystart.UpdateQueued, at, check}
}

// listed is whether the run's publication is on the list the docket builds at
// the time given.
func listed(t *testing.T, docket *memoryDocket, state runstate.State, epics *listingEpics, at time.Time) bool {
	t.Helper()
	docketer := docketerOver([]runstate.State{state}, docket)
	if epics != nil {
		docketer.WouldStart, docketer.NotStarted = epics.would, epics.readRefusals
	}
	docketer.Clock = docketClockAt{at: at}
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

// refusedFor is the refused update kept for the change, naming its run.
func refusedFor(t *testing.T, epics listingEpics, change unpublishedChange) bool {
	t.Helper()
	for _, item := range epics.refusals(t).Items {
		if item.WorkItemID == change.state.WorkItemID && item.Path == change.path && item.RunID == change.state.RunID {
			return true
		}
	}
	return false
}

// A change whose update is due and refused is left off the list, a merge GitHub
// dropped among them; building the list writes nothing; and a later check that
// finds the head level with its target, or cannot read GitHub's answer, takes
// the refusal away, so the change is listed.
func TestAChangeIsLeftOffOnlyWhileItsUpdateIsDueAndRefused(t *testing.T) {
	t.Parallel()
	answers := map[string]func(*orchestratortest.CheckedForge){
		"its head is level with its target": func(forge *orchestratortest.CheckedForge) {
			forge.Reading.BehindBy, forge.Reading.Failing = 0, nil
		},
		"GitHub's checks could not be read": func(forge *orchestratortest.CheckedForge) {
			forge.ReadError = errors.New("GitHub did not answer")
		},
	}
	for _, kind := range unpublishedKinds {
		for how, answer := range answers {
			t.Run(kind+", then "+how, func(t *testing.T) {
				t.Parallel()
				epics := newListingEpics(t)
				change := unpublishedChangeOf(t, kind, epics, true)
				if !refusedFor(t, epics, change) {
					t.Fatalf("refused starts = %+v, want the update refused naming run %s", epics.refusals(t).Items, change.state.RunID)
				}
				before := epics.refusals(t)
				docket := &memoryDocket{}
				if listed(t, docket, change.state, &epics, change.at) || len(docket.entries) != 0 {
					t.Fatal("listed while its update is due and refused")
				}
				if !reflect.DeepEqual(epics.refusals(t), before) {
					t.Fatalf("building the list changed the refused starts to %+v", epics.refusals(t))
				}
				state := change.check(t, answer)
				if refusedFor(t, epics, change) {
					t.Fatalf("the refused update was kept: %+v", epics.refusals(t).Items)
				}
				if !listed(t, &memoryDocket{}, state, &epics, change.at) {
					t.Fatal("not listed")
				}
			})
		}
	}
}

// Only the refused start naming this run, refused within the hour, under the
// path that fits, read from a file that can be read, leaves a change off; and
// one left off is listed once its item is added or epic mode is turned off.
func TestWhatShowsAnUpdateDue(t *testing.T) {
	t.Parallel()
	for _, kind := range unpublishedKinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			change := unpublishedChangeOf(t, kind, newListingEpics(t), true)
			item, run := change.state.WorkItemID, change.state.RunID
			other := maystart.UpdateQueued
			if change.path == maystart.UpdateQueued {
				other = maystart.UpdateForPerson
			}
			refused := func(start maystart.Start, at time.Time) func(listingEpics, *testing.T) {
				return func(epics listingEpics, t *testing.T) { epics.askingAt(at)(start) }
			}
			for why, keep := range map[string]func(listingEpics, *testing.T){
				"no entry":                          func(listingEpics, *testing.T) {},
				"an entry naming no run":            refused(maystart.Run(change.path, item), change.at),
				"an entry naming another run":       refused(maystart.Run(change.path, item).OfRun("run-another"), change.at),
				"an entry under the other path":     refused(maystart.Run(other, item).OfRun(run), change.at),
				"an entry refused over an hour ago": refused(maystart.Run(change.path, item).OfRun(run), change.at.Add(-61*time.Minute)),
				"a file that cannot be read": func(epics listingEpics, t *testing.T) {
					refused(maystart.Run(change.path, item).OfRun(run), change.at)(epics, t)
					epics.corrupt(t, "not-started.json")
				},
			} {
				epics := newListingEpics(t)
				keep(epics, t)
				if !listed(t, &memoryDocket{}, change.state, &epics, change.at) {
					t.Errorf("%s: not listed", why)
				}
			}
			for how, allow := range map[string]func(listingEpics, *testing.T){"added": listingEpics.add, "epic mode off": listingEpics.turnOff} {
				epics := newListingEpics(t)
				refused(maystart.Run(change.path, item).OfRun(run), change.at)(epics, t)
				if listed(t, &memoryDocket{}, change.state, &epics, change.at) {
					t.Fatalf("%s: listed before, while its update is due and refused", how)
				}
				allow(epics, t)
				if !listed(t, &memoryDocket{}, change.state, &epics, change.at) {
					t.Errorf("%s: not listed once it may be brought up to date", how)
				}
			}
			epics := newListingEpics(t)
			refused(maystart.Run(change.path, item).OfRun(run), change.at)(epics, t)
			epics.corrupt(t, "epic-scope.json")
			if !listed(t, &memoryDocket{}, change.state, &epics, change.at) {
				t.Error("not listed where the decision could not be made")
			}
		})
	}
}

// An entry listed before its update was refused comes off once it is, unless it
// is back because a decision of hers could not be carried out; and a change
// stuck for another reason is listed as ever.
func TestAChangeListedBeforeOrStuckForAnotherReason(t *testing.T) {
	t.Parallel()
	for _, kind := range unpublishedKinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			epics := newListingEpics(t)
			change := unpublishedChangeOf(t, kind, epics, true)
			docket := &memoryDocket{}
			if !listed(t, docket, change.state, nil, change.at) {
				t.Fatal("not listed with no epic named")
			}
			docketer := Docketer{WouldStart: epics.would, NotStarted: epics.readRefusals}
			runs := map[string]runstate.State{change.state.RunID: change.state}
			if kept := docketer.withoutRefusedUpdates([]triage.Entry{docket.entries[0]}, runs, change.at); len(kept) != 0 {
				t.Fatal("an entry listed before its update was refused stays")
			}
			entry := docket.entries[0]
			entry.Closed = &triage.Closure{Decision: runstate.TriageDecisionRerun, ClosedAt: baseTime}
			entry.CarryOut = &triage.CarryOut{RefusedAt: baseTime.Add(time.Minute)}
			if kept := docketer.withoutRefusedUpdates([]triage.Entry{entry}, runs, change.at); len(kept) != 1 {
				t.Fatal("an entry back because its decision could not be carried out was left out")
			}
			epics.add(t)
			if kept := docketer.withoutRefusedUpdates([]triage.Entry{docket.entries[0]}, runs, change.at); len(kept) != 1 {
				t.Fatal("an entry listed before epic mode does not come back once its item is added")
			}
			stuck := newListingEpics(t)
			stuck.askingAt(change.at)(maystart.Run(change.path, change.state.WorkItemID).OfRun(change.state.RunID))
			for why, alter := range map[string]func(*runstate.State){
				"closed":                                 func(s *runstate.State) { s.PullRequest.State = "CLOSED" },
				"its merge failed":                       func(s *runstate.State) { s.PublishFailure = "the merge was refused" },
				"the harness cannot bring it up to date": func(s *runstate.State) { s.WorktreeRemoved = true },
			} {
				state := change.state
				published := *state.PullRequest
				state.PullRequest = &published
				alter(&state)
				if !listed(t, &memoryDocket{}, state, &stuck, change.at) {
					t.Errorf("%s: not listed", why)
				}
			}
		})
	}
}

// In a process that hosts no runs a queued check still asks and keeps its
// refusal, and a check of a person's merge stops before it asks and changes no
// entry; either, finding no update due, takes away the entry naming its run and
// no entry naming another.
func TestAChecksThatHostsNoRuns(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"queued on GitHub", "waiting for a person"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			epics := newListingEpics(t)
			change := unpublishedChangeOf(t, kind, epics, false)
			if refused := refusedFor(t, epics, change); refused != (kind == "queued on GitHub") {
				t.Fatalf("refused starts = %+v, want one only for the queued check", epics.refusals(t).Items)
			}
			epics.askingAt(change.at)(maystart.Run(change.path, change.state.WorkItemID).OfRun("run-another"))
			before := epics.refusals(t)
			change.check(t, func(forge *orchestratortest.CheckedForge) { forge.Reading.BehindBy, forge.Reading.Failing = 0, nil })
			if after := epics.refusals(t); !reflect.DeepEqual(after, before) {
				t.Fatalf("a check of one run changed another's entry: %+v, was %+v", after.Items, before.Items)
			}
			epics.askingAt(change.at)(maystart.Run(change.path, change.state.WorkItemID).OfRun(change.state.RunID))
			if !refusedFor(t, epics, change) {
				t.Fatal("the entry did not take the run of the latest refusal")
			}
			change.check(t, func(*orchestratortest.CheckedForge) {})
			if refusedFor(t, epics, change) {
				t.Fatalf("a check finding no update due kept the entry naming its run: %+v", epics.refusals(t).Items)
			}
		})
	}
}
