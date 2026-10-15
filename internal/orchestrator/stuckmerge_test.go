package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
		{status: "DIRTY", want: []string{"conflicts with main", "and the conflict resolved"}},
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
				"The change is not merged. The run's branch and worktree are kept, so a repair decided on this run brings it up to date without rebuilding it",
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

// A repair decided on a merge handed on this way brings the kept change up to
// date with the target: the run is put back at its merge, the change replayed
// onto where main went, checked and reviewed again, and its merge queued again,
// with no developer invoked and no repair attempt counted.
func TestARepairOfAStuckMergeBringsTheKeptChangeUpToDateAndQueuesItAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture, forge, reconciler := queuedFor(t, 3*time.Hour)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.Reading = publish.CheckReading{Files: []string{"feature.txt"}, Passing: 3, BehindBy: 1}
	forge.Removal = publish.QueueRemoval{MergeStatus: "BEHIND"}
	stopped, _ := settledStuck(t, fixture, reconciler)
	if !replaysDroppedMerge(stopped, triage.Found{BranchThere: true, WorktreeThere: true}) {
		t.Fatalf("a stuck merge with its branch, worktree, approval and session kept is not replayable: %s", dropReplayRefusal(stopped))
	}

	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error {
		t.Error("the developer was invoked for a replay that did not conflict")
		return nil
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, fixture.repository, fixture.worktreeRoot, fixture.store, fixture.tracker, provider, []string{"exit 0"}), provider), forge)
	pipeline.Publisher = forge
	continuer := RepairContinuer{
		Docket: fixture.docket, Runs: fixture.store, Intake: intake, Decisions: fixture.store.Triage(),
		Items: fixture.tracker, Worktrees: pipeline.Worktrees.(RepairWorktrees),
		ConfiguredAttempts: 2, Capacity: 1, Clock: fixedClock{at: stopped.UpdatedAt},
		Start: func(ctx context.Context, item, run string) (Outcome, error) {
			continued, err := fixture.store.Load(run)
			if err != nil {
				t.Fatal(err)
			}
			if !resumableIntegration(continued) || !continued.UpdatingQueuedHead() || continued.MergeDrop != nil || continued.PublishFailure != "" {
				t.Fatalf("continued record = status %q, phase %q, drop %#v; want the run back at its merge to be brought up to date", continued.Status, continued.Phase, continued.MergeDrop)
			}
			return pipeline.Continue(ctx, item, run)
		},
	}
	if _, err := fixture.store.Triage().GrantRepair(ctx, stopped.WorkItemID,
		triageDecided(runstate.TriageDecisionRepair, stopped.RunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatal(err)
	}
	carrying := CarryOut{
		Docket: fixture.docket, Decisions: fixture.store.Triage(), Reruns: fixture.store.Reruns(),
		Runs: fixture.store, Repairer: continuer, Clock: fixedClock{at: stopped.UpdatedAt},
	}
	carried, replayed, err := carrying.Carry(ctx, theOneOutstanding(t, carrying))
	if err != nil || !carried.Carried || replayed.RunID != stopped.RunID {
		t.Fatalf("carry-out = %#v, outcome = %#v, %v", carried, replayed, err)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) == 0 {
		t.Fatal("the replayed change was queued without a fresh independent review")
	}
	settled, err := fixture.store.Load(stopped.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.PullRequest == nil || !settled.PullRequest.MergeQueued || settled.Integration == nil || settled.MergeDrop != nil || settled.PublishFailure != "" || settled.Blocker != "" {
		t.Fatalf("record = %#v; want the replayed change's merge queued again with the drop gone", settled)
	}
	if settled.BaseCommit == stopped.BaseCommit || settled.PullRequest.Number != stopped.PullRequest.Number || settled.PullRequest.HeadCommit == stopped.PullRequest.HeadCommit {
		t.Errorf("base %s head %s; want the same pull request carrying the change replayed onto the target", settled.BaseCommit, settled.PullRequest.HeadCommit)
	}
	if settled.RepairAttempts != stopped.RepairAttempts || len(forge.MergeRequests()) != 2 {
		t.Errorf("repair attempts = %d (was %d), merges = %d; want no attempt counted and the merge asked for again", settled.RepairAttempts, stopped.RepairAttempts, len(forge.MergeRequests()))
	}
}

// Where the replay is impossible — here, the pull request was closed — a
// repair is refused once, on the item, saying why and naming the re-run.
func TestARepairOfAStuckMergeThatCannotBeReplayedIsRefusedNamingTheReRun(t *testing.T) {
	t.Parallel()
	fixture, forge, reconciler := queuedFor(t, 3*time.Hour)
	forge.Removal = publish.QueueRemoval{MergeStatus: "DIRTY"}
	settled, _ := settledStuck(t, fixture, reconciler)
	closed := *settled.PullRequest
	closed.State = "CLOSED"
	settled.PullRequest = &closed
	found := triage.Found{BranchThere: true, WorktreeThere: true}
	if replaysDroppedMerge(settled, found) {
		t.Fatal("a merge whose pull request is closed was taken as replayable")
	}
	err := continuableRepair(settled, found)
	if carryOutCause(err) != triage.CarryOutMergeDropped || !strings.Contains(err.Error(), "conflicts with main") || !strings.Contains(err.Error(), "pull request 1 is closed") {
		t.Fatalf("continuableRepair() = %v, want a refusal of cause %q carrying the forge's reason and why it cannot be replayed", err, triage.CarryOutMergeDropped)
	}
	if _, clears := carryOutGate(err); !strings.Contains(clears, "re-run") {
		t.Errorf("clears = %q, want the re-run the record supports named", clears)
	}
	if line := stuckMerge(closed, "main", time.Now(), 3*time.Hour, stuckLimit, false, "", dropReplayRefusal(settled), publish.QueueRemoval{MergeStatus: "DIRTY"}, ""); !strings.Contains(line, "a repair cannot bring it up to date because pull request 1 is closed, so a re-run of the item") {
		t.Errorf("the item line for an unreplayable merge does not name the re-run:\n%s", line)
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
