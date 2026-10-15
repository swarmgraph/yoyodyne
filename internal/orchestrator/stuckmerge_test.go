package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// These drive the limit on a queued merge's wait (stuckmerge.go): a merge the
// forge has held, or dropped and nothing has replayed, for longer than
// triage.stuck_merge_age is asked about, recorded as not merged with the
// forge's reason, and put on the development manager's docket.

const stuckLimit = 2 * time.Hour

// sweepClock is a sweep's clock standing at one moment.
type sweepClock struct{ now time.Time }

func (c sweepClock) Now() time.Time { return c.now }

// queuedFor is a finished run whose merge the forge took, swept by a reconciler
// whose clock stands the given time after the merge was queued.
func queuedFor(t *testing.T, waited time.Duration) (queuedFixture, *orchestratortest.CheckedForge, Reconciler) {
	t.Helper()
	fixture, forge, outcome := queuedOnProtectedTarget(t)
	if outcome.PullRequest.QueuedAt == nil {
		t.Fatalf("pull request = %#v, want the moment the merge was queued recorded", outcome.PullRequest)
	}
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3}
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, false)
	reconciler.Drops = forge
	reconciler.StuckMergeAge = stuckLimit
	reconciler.Clock = sweepClock{now: outcome.PullRequest.QueuedAt.Add(waited)}
	return fixture, forge, reconciler
}

// settledStuck sweeps once and returns the run's record and the item's blocker,
// failing unless the merge was handed on as not merged.
func settledStuck(t *testing.T, fixture queuedFixture, reconciler Reconciler) (runstate.State, string) {
	t.Helper()
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the stuck merge settled on a blocker", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	published := settled.PullRequest
	if published.MergeQueued || published.Merged || settled.MergeDrop == nil || settled.MergeDrop.Reason != settled.PublishFailure {
		t.Fatalf("publication = %#v, drop = %#v; want it recorded as not merged, with the reason beside the moment", published, settled.MergeDrop)
	}
	if settled.Outstanding() || !settled.Status.Terminal() || settled.Blocker == "" {
		t.Fatalf("record = status %q, outstanding %t, blocker %q; want a stopped run, not one still waiting on the forge", settled.Status, settled.Outstanding(), settled.Blocker)
	}
	if settled.BranchRemoved || settled.WorktreeRemoved {
		t.Fatal("the stuck merge's branch or worktree was removed; the change has to be kept for a repair")
	}
	record := fixture.tracker.Record()
	if !record.Blocked || record.Closed {
		t.Fatalf("item blocked = %t, closed = %t; want it handed back unclosed", record.Blocked, record.Closed)
	}
	entries, err := fixture.docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Class != triage.ClassStoppedRun || entries[0].RunID != pipelineRunID {
		t.Fatalf("docket = %#v, want the run on the development manager's list of stopped runs", entries)
	}
	if !strings.Contains(record.BlockReason, "the next move is the development manager's") {
		t.Errorf("blocker does not name the development manager as moving next:\n%s", record.BlockReason)
	}
	return settled, record.BlockReason
}

// Every merge state the forge can report is said in words, with what would let
// the merge land; only the two a role cannot clear name the step somebody with
// access to the repository takes, and an account the forge would not give says
// that it was not read.
func TestAQueuedMergePastItsLimitIsWithdrawnAndHandedOnWithTheForgesReason(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status     string
		unread     error
		want       []string
		personStep bool
	}{
		{status: "DIRTY", want: []string{"conflicts with main", "a repair of this run can do from its kept branch"}},
		{status: "BEHIND", want: []string{"main has moved ahead of the pull request", "brought up to date with main"}},
		{status: "UNSTABLE", want: []string{"a check the forge runs on the pull request has not succeeded"}},
		{status: "BLOCKED", want: []string{"a protection rule of main is not satisfied", "approve pull request 1 on GitHub"}, personStep: true},
		{status: "DRAFT", want: []string{"still a draft", "mark pull request 1 ready for review"}, personStep: true},
		{status: "CLEAN", want: []string{"nothing the forge reports", "arming the merge again is the likely remedy"}},
		{status: "UNKNOWN", want: []string{"the forge has not decided whether the pull request can merge"}},
		{unread: errors.New("HTTP 502 from the forge"), want: []string{"merge state for it could not be read", "HTTP 502 from the forge"}},
	} {
		name := test.status
		if name == "" {
			name = "unread"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture, forge, reconciler := queuedFor(t, 13*time.Hour+31*time.Minute)
			forge.Removal = publish.QueueRemoval{MergeStatus: test.status}
			forge.RemovalErr = test.unread
			settled, blocker := settledStuck(t, fixture, reconciler)
			if len(forge.Withdrawn) != 1 || forge.HoldsQueuedMerge() {
				t.Fatalf("withdrawn = %v; want the merge the forge still held withdrawn before it was handed on", forge.Withdrawn)
			}
			want := append([]string{
				"has not merged after 13 hours 31 minutes, which is past the 2 hours a queued merge may wait",
				"the harness withdrew it",
				"The change is not merged; the run's branch and worktree are kept",
				"checks passing",
			}, test.want...)
			for _, phrase := range want {
				for label, text := range map[string]string{"publication failure": settled.PublishFailure, "blocker": blocker} {
					if !strings.Contains(text, phrase) {
						t.Errorf("%s does not say %q:\n%s", label, phrase, text)
					}
				}
			}
			if names := strings.Contains(strings.ToLower(settled.PublishFailure), "somebody with access to the repository on github"); names != test.personStep {
				t.Errorf("names a step for somebody on GitHub = %t, want %t:\n%s", names, test.personStep, settled.PublishFailure)
			}
		})
	}
}

// A repair decided on a merge handed on this way has nothing to hand the
// developer, and is refused once, on the item, naming what the record supports.
func TestARepairOfAStuckMergeIsRefusedOnceNamingTheReRun(t *testing.T) {
	t.Parallel()
	fixture, forge, reconciler := queuedFor(t, 3*time.Hour)
	forge.Removal = publish.QueueRemoval{MergeStatus: "DIRTY"}
	settled, _ := settledStuck(t, fixture, reconciler)
	err := continuableRepair(settled, triage.Found{BranchThere: true, WorktreeThere: true})
	if carryOutCause(err) != triage.CarryOutMergeDropped || !strings.Contains(err.Error(), "conflicts with main") {
		t.Fatalf("continuableRepair() = %v, want a refusal of cause %q carrying the forge's reason", err, triage.CarryOutMergeDropped)
	}
	if _, clears := carryOutGate(err); !strings.Contains(clears, "re-run") {
		t.Errorf("clears = %q, want the re-run the record supports named", clears)
	}
}

// A merge the forge dropped behind its target is one the sweep brings up to
// date, and it waits for a moment to do so; past the limit it stops waiting and
// records the drop, so the record never goes on saying "queued".
func TestADroppedMergeWaitingToBeBroughtUpToDatePastItsLimitIsRecordedAsNotMerged(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, waited time.Duration) (queuedFixture, *orchestratortest.CheckedForge, Reconciler) {
		fixture, forge, reconciler := queuedFor(t, waited)
		forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 4}
		forge.Removal = publish.QueueRemoval{MergeStatus: "DIRTY", Events: []publish.QueueEvent{{Kind: "AutoMergeDisabledEvent", Reason: "Merge conflict"}}}
		forge.DropQueuedMerge()
		return fixture, forge, reconciler
	}

	// Inside the limit the wait is what it was: left for the next sweep.
	fixture, forge, reconciler := setup(t, time.Hour)
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, %v; want the drop left for a sweep that hosts the update", results, err)
	}
	if recorded, _ := fixture.store.Load(pipelineRunID); !recorded.PullRequest.MergeQueued || fixture.tracker.Record().Blocked {
		t.Fatal("a drop inside the limit was handed on")
	}

	fixture, forge, reconciler = setup(t, 3*time.Hour)
	settled, blocker := settledStuck(t, fixture, reconciler)
	if len(forge.Withdrawn) != 0 {
		t.Errorf("withdrawn = %v; a merge the forge dropped has nothing to withdraw", forge.Withdrawn)
	}
	for _, phrase := range []string{
		"The forge had already dropped the merge.",
		"Its head had fallen behind main, and bringing it up to date waited because this pass hosts no runs",
		"conflicts with main",
		`auto-merge turned off (the forge's reason: "Merge conflict")`,
	} {
		if !strings.Contains(settled.PublishFailure, phrase) || !strings.Contains(blocker, phrase) {
			t.Errorf("record does not say %q:\n%s\n---\n%s", phrase, settled.PublishFailure, blocker)
		}
	}
}

// A forge that cannot be asked decides nothing, however long the merge has
// waited: the merge stays queued and the failed read is what the sweep reports.
func TestAQueuedMergePastItsLimitIsLeftQueuedWhenTheForgeCannotBeRead(t *testing.T) {
	t.Parallel()
	fixture, forge, reconciler := queuedFor(t, 5*time.Hour)
	forge.StateErr = errors.New("HTTP 500: 500 Internal Server Error")
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionUnsettled || !strings.Contains(results[0].Failure, "HTTP 500") {
		t.Fatalf("reconciliation = %#v, want the failed read reported", results)
	}
	recorded, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.PullRequest.MergeQueued || recorded.MergeDrop != nil || len(forge.Withdrawn) != 0 || fixture.tracker.Record().Blocked {
		t.Fatal("a merge nobody could ask about was withdrawn or handed on")
	}

	// Once the forge answers, the merge is handed on as any other past its limit.
	forge.StateErr = nil
	settledStuck(t, fixture, reconciler)
}

// A merge inside its limit is left as it was, and one the forge lands is
// settled as landed whenever the sweep comes to it.
func TestAMergeThatLandsBeforeItsLimitIsUnaffected(t *testing.T) {
	t.Parallel()
	fixture, forge, reconciler := queuedFor(t, time.Hour)
	results, err := reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionQueued {
		t.Fatalf("Reconcile() = %#v, %v; want the merge left queued inside its limit", results, err)
	}
	if len(forge.Withdrawn) != 0 || fixture.tracker.Record().Blocked {
		t.Fatal("a merge inside its limit was withdrawn or handed on")
	}

	forge.PerformQueuedMerge(t)
	reconciler.Clock = sweepClock{now: reconciler.clock().Now().Add(stuckLimit)}
	results, err = reconciler.Reconcile(context.Background())
	if err != nil || len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("Reconcile() = %#v, %v; want the landed merge settled", results, err)
	}
	if len(forge.Withdrawn) != 0 || fixture.tracker.Record().Blocked || !fixture.tracker.Record().Closed {
		t.Fatalf("withdrawn = %v, blocked = %t, closed = %t; want the item closed on the merge", forge.Withdrawn, fixture.tracker.Record().Blocked, fixture.tracker.Record().Closed)
	}
}

func TestWaitedForSaysAWaitInWords(t *testing.T) {
	t.Parallel()
	for span, want := range map[time.Duration]string{
		30 * time.Second:              "less than a minute",
		time.Minute:                   "1 minute",
		2 * time.Hour:                 "2 hours",
		13*time.Hour + 31*time.Minute: "13 hours 31 minutes",
		50*time.Hour + 40*time.Minute: "2 days 2 hours",
		24*time.Hour + 5*time.Minute:  "1 day",
	} {
		if got := waitedFor(span); got != want {
			t.Errorf("waitedFor(%s) = %q, want %q", span, got, want)
		}
	}
}
