package orchestrator

// The three ways an ended run is put back to work, each asking the one decision
// on what may start under the named epics (internal/maystart) before it starts.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusedStart is a decision that refuses one item, what makes it let the item
// start, and a check of the record of refused starts: exactly one entry, for the
// item by the path given, and none for the empty path.
type refusedStart struct {
	decide StartDecision
	allow  func()
	kept   func(path maystart.Path)
}

// forEachRefusal runs a test against an item outside the named epics, and
// against a record of them that cannot be read.
func forEachRefusal(t *testing.T, item string, test func(t *testing.T, refusal refusedStart)) {
	t.Helper()
	t.Run("outside the named epics", func(t *testing.T) {
		t.Parallel()
		decide, add, refused := epicModeKept(t, blockedMember)
		test(t, refusedStart{decide: decide, allow: func() { add(item) }, kept: func(path maystart.Path) {
			t.Helper()
			refused(path, item)
		}})
	})
	t.Run("the decision could not be made", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		record := filepath.Join(home.ProductDirectory(root, "yoyodyne"), "epic-scope.json")
		if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		test(t, refusedStart{
			decide: func(start maystart.Start) maystart.Decision {
				return maystart.Ask(root, "yoyodyne", start, baseTime)
			},
			allow: func() {
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
			},
			kept: func(path maystart.Path) {
				t.Helper()
				refusals, err := maystart.ReadRefusals(root, "yoyodyne")
				if err != nil {
					t.Fatal(err)
				}
				if path == "" {
					if len(refusals.Items) != 0 {
						t.Fatalf("refused starts = %+v, want none", refusals.Items)
					}
					return
				}
				if len(refusals.Items) != 1 || refusals.Items[0].Path != path || refusals.Items[0].WorkItemID != item || !strings.Contains(refusals.Items[0].Why, "could not be read") {
					t.Fatalf("refused starts = %+v, want the one of %s by %s", refusals.Items, item, path)
				}
			},
		})
	})
}

// A change behind its target, queued with a failing check it does not touch or
// dropped by GitHub, keeps its merge, record and item, past the limit on a
// queued merge and on a pass that hosts no runs too, and is brought up to date
// once its item may start.
func TestAQueuedChangeThatMayNotStartIsNotBroughtUpToDate(t *testing.T) {
	t.Parallel()
	for name, dropped := range map[string]bool{"queued": false, "dropped": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachRefusal(t, "yoyodyne-task", func(t *testing.T, refusal refusedStart) {
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
				reconciler.Drops = forge
				reconciler.StuckMergeAge = stuckLimit
				reconciler.MayStart = refusal.decide
				load := func() runstate.State {
					t.Helper()
					state, err := fixture.store.Load(pipelineRunID)
					if err != nil {
						t.Fatal(err)
					}
					return state
				}
				// Every sweep writes the forge's checks onto the publication as it
				// reads them, with which process wrote it, whatever is then decided.
				unread := func(state runstate.State) string {
					published := *state.PullRequest
					published.Checks = nil
					state.PullRequest, state.UpdatedAt, state.Worker = &published, time.Time{}, nil
					encoded, err := json.Marshal(state)
					if err != nil {
						t.Fatal(err)
					}
					return string(encoded)
				}
				before, item := load(), fixture.tracker.Record()
				for _, pass := range []struct {
					waited time.Duration
					hosts  bool
				}{{time.Minute, true}, {13 * time.Hour, true}, {13 * time.Hour, false}} {
					reconciler.HostsRuns = pass.hosts
					reconciler.Clock = sweepClock{now: outcome.PullRequest.QueuedAt.Add(pass.waited)}
					results, err := reconciler.Reconcile(context.Background())
					if err != nil || len(results) != 1 || results[0].Action != ActionQueued || results[0].Failure != "" ||
						!strings.Contains(results[0].Detail, "yoyodyne-task was not started") {
						t.Fatalf("after %s: %#v, %v; want the merge left queued saying why", pass.waited, results, err)
					}
					if after := unread(load()); after != unread(before) {
						t.Fatalf("after %s: record = %s\nwant it as it was: %s", pass.waited, after, unread(before))
					}
					if len(forge.Withdrawn) != 0 || forge.HoldsQueuedMerge() == dropped || len(forge.MergeRequests()) != 1 {
						t.Fatalf("after %s: withdrawn %v, queued %t; want the pull request as it was", pass.waited, forge.Withdrawn, forge.HoldsQueuedMerge())
					}
					if now := fixture.tracker.Record(); now.Notes != item.Notes || now.Blocked || now.Closed || len(fixture.docket.entries) != 0 {
						t.Fatalf("after %s: the item was written to or handed on: %#v", pass.waited, now)
					}
					refusal.kept(maystart.UpdateQueued)
				}

				refusal.allow()
				reconciler.HostsRuns = true
				reconciler.Clock = sweepClock{now: outcome.PullRequest.QueuedAt.Add(time.Minute)}
				results, err := reconciler.Reconcile(context.Background())
				if err != nil || len(results) != 1 || results[0].Action != ActionUpdating || !updatingQueuedHead(load()) {
					t.Fatalf("Reconcile() once it may start = %#v, %v; want the change being brought up to date", results, err)
				}
				refusal.kept("")
			})
		})
	}
}

// A change waiting for a person's merge keeps its wait unchanged when its target
// moves. It is brought up to date once its item may start, and a merge by the
// person settles it while it may not.
func TestAChangeWaitingForAPersonThatMayNotStartKeepsItsWait(t *testing.T) {
	t.Parallel()
	forEachRefusal(t, "yoyodyne-task", func(t *testing.T, refusal refusedStart) {
		for _, merged := range []bool{true, false} {
			name := map[bool]string{true: "merged by a person", false: "allowed to start"}[merged]
			fixture := newPersonMergeUpdateFixture(t)
			driftRemoteTarget(t, fixture.remote, "main")
			reconciler := fixture.reconciler(t)
			reconciler.MayStart = refusal.decide
			before, notes := fixture.load(t), fixture.tracker.Notes
			check := func() PersonMergeSettlement {
				t.Helper()
				settled, err := reconciler.SettlePersonMerges(context.Background())
				if err != nil || len(settled) != 1 || settled[0].Failure != "" {
					t.Fatalf("%s: check = %#v, %v; want the one pull request read", name, settled, err)
				}
				return settled[0]
			}
			if settled := check(); settled.Updating || settled.BehindBy != 1 || !strings.Contains(settled.Kept, "yoyodyne-task was not started") {
				t.Fatalf("%s: check = %#v, want the wait kept saying why", name, settled)
			}
			if after := fixture.load(t); !reflect.DeepEqual(after, before) || fixture.tracker.Notes != notes || len(fixture.forge.Withdrawn) != 0 {
				t.Fatalf("%s: record = %#v, withdrawn %v; want everything as it was", name, after, fixture.forge.Withdrawn)
			}
			refusal.kept(maystart.UpdateForPerson)
			if merged {
				if err := fixture.forge.MergeByHand("main", fixture.run.Branch); err != nil {
					t.Fatal(err)
				}
				if settled := check(); !settled.Settled || settled.Found != PersonMergeMerged || !fixture.tracker.Closed {
					t.Fatalf("%s: check = %#v, closed %t; want the merge settled", name, settled, fixture.tracker.Closed)
				}
				continue
			}
			refusal.allow()
			if settled := check(); !settled.Updating || settled.Kept != "" {
				t.Fatalf("%s: check = %#v, want the change being brought up to date", name, settled)
			}
			refusal.kept("")
		}
	})
}

// itemsByID answers for the items of two runs.
type itemsByID map[string]*orchestratortest.Tracker

func (i itemsByID) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	return i[id].Show(ctx, id)
}

func (i itemsByID) RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error) {
	return i[id].RecordOutcome(ctx, id, notes)
}

func (i itemsByID) Claim(ctx context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	return i[id].Claim(ctx, id)
}

// A run GitHub's refusal stopped that may not start stays stopped, spends no
// attempt and keeps its entry, and a member's run listed after it is taken up at
// the same look.
func TestARunStoppedForGitHubsRefusalThatMayNotStartStaysStopped(t *testing.T) {
	t.Parallel()
	member := publishRefusedState()
	member.RunID = "run-fedcba9876543210fedcba9876543210"
	member.WorkItemID = blockedMember
	member.WorkItemTitle = "A member"
	member.WorktreePath = "/state/worktrees/member"
	member.Branch = "yoyodyne/member/abc"

	forEachRefusal(t, docketedItem, func(t *testing.T, refusal refusedStart) {
		h := newResumeHarness(t, publishRefusedState())
		if err := h.runs.Create(member); err != nil {
			t.Fatal(err)
		}
		docketer := docketerOverStore(h.docket, h.runs, docketConfig())
		docketer.Clock = docketClock{}
		if _, err := docketer.RecordStoppedRun(member); err != nil {
			t.Fatal(err)
		}
		entries, err := h.docket.List()
		if err != nil || len(entries) != 2 || entries[0].RunID != docketedRunID || entries[1].RunID != member.RunID {
			t.Fatalf("entries = %#v, %v; want the member's run listed second", entries, err)
		}
		stops := h.identityStops(&identityProbe{}, &landingResumes{})
		stops.Items = itemsByID{docketedItem: h.tracker, blockedMember: {Item: beads.WorkItem{ID: blockedMember, Title: "A member", Status: "in_progress"}}}
		// The member may start whatever the record says, so the look is seen to go
		// on where the record could not be read too.
		stops.MayStart = func(start maystart.Start) maystart.Decision {
			if reflect.DeepEqual(start, maystart.Run(maystart.AfterLogin, blockedMember)) {
				return maystart.Decision{Answer: maystart.May}
			}
			return refusal.decide(start)
		}
		before := h.reload(t)

		pass := stops.Take(context.Background())
		if pass.Stopped != 2 || pass.TakenUp != 1 || len(h.started) != 1 || h.started[0].runID != member.RunID {
			t.Fatalf("look = %#v, started %v; want only the member's run taken up", pass, h.started)
		}
		if !strings.Contains(pass.Says, docketedItem+" was not started") {
			t.Errorf("look says %q, want why the other stays stopped", pass.Says)
		}
		if after := h.reload(t); !reflect.DeepEqual(after, before) || len(after.IdentityTakeUps) != 0 {
			t.Fatalf("record = %#v, want the run as it stopped", after)
		}
		if _, closed := h.closure(t); closed || len(h.tracker.Calls) != 0 {
			t.Fatalf("closed = %t, item calls %v; want the entry open and the item untouched", closed, h.tracker.Calls)
		}
		refusal.kept(maystart.AfterLogin)

		// The member's run is live and holds one of the two slots.
		refusal.allow()
		if pass := stops.Take(context.Background()); pass.TakenUp != 1 || len(h.started) != 2 || h.started[1].runID != docketedRunID {
			t.Fatalf("look once it may start = %#v, started %v; want the run taken up", pass, h.started)
		}
		refusal.kept("")
	})
}
