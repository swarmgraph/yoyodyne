package orchestrator

// An entry a person hands back from the merge queue after merging its change by
// hand: where the target carries the change, the entry leaves the queue as
// landed and its run and item are settled; where it does not, the entry is
// handed back as before and the run's stop names why. A hand-back whose item is
// already closed leaves it closed.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runPullRequestForge answers about the run's own pull request as a test says,
// and asks the forge it wraps about every other branch.
type runPullRequestForge struct {
	PullRequests
	branch string
	answer publish.PullRequest
}

func (f *runPullRequestForge) State(ctx context.Context, head string) (publish.PullRequest, error) {
	if head == f.branch {
		return f.answer, nil
	}
	return f.PullRequests.State(ctx, head)
}

// runPullRequestReads gives a queue stopped on its entry a run whose own pull request
// the forge answers about as answer, and a repair that records what it is
// given.
func (f *promotionFixture) runPullRequestReads(answer publish.PullRequest) *recordingRepair {
	f.t.Helper()
	f.run.admittedRun.state.Branch = "change"
	f.run.admittedRun.state.PullRequest = &runstate.PullRequest{Branch: "change", Number: 1926, URL: "https://forge.example/pull/1926", HeadCommit: f.head}
	pipeline := *f.promoter.Pipeline
	pipeline.Publisher = &runPullRequestForge{PullRequests: pipeline.Publisher, branch: "change", answer: answer}
	f.promoter.Pipeline = &pipeline
	repair := &recordingRepair{}
	f.promoter.Repair = repair
	return repair
}

func (f *promotionFixture) handBackByHand(reason string) MergeQueuePromotion {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	handed, err := f.promoter.HandBack(ctx, queueKey, f.entry.EntryID, reason)
	if err != nil {
		f.t.Fatalf("HandBack() error = %v", err)
	}
	return handed
}

func TestAnEntryHandedBackAfterItsChangeWasMergedByHandEndsAsLanded(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		merge func(f *promotionFixture)
	}{
		{"merged with a merge commit", func(f *promotionFixture) {
			runPipelineGit(t, f.repository, "merge", "--no-ff", "-m", "Merge pull request #1926", f.head)
		}},
		{"squash-merged", func(f *promotionFixture) {
			runPipelineGit(t, f.repository, "merge", "--squash", f.head)
			runPipelineGit(t, f.repository, "commit", "-m", "the approved change (#1926)")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			f, _ := queueStoppedOnMerge(t)
			test.merge(f)
			merged := f.local("main")
			repair := f.runPullRequestReads(publish.PullRequest{Number: 1926, State: "MERGED", Merged: true, MergeCommit: merged, HeadCommit: f.head})

			handed := f.handBackByHand("merged by hand as #1926")
			handback := f.landing().Handback
			if handback == nil || handback.Continuation != runstate.MergeQueueLandedOutside || handback.TargetBase != merged || handback.HandedBackAt == nil {
				t.Fatalf("handback = %#v, want the entry landed at %s and recorded", handback, merged)
			}
			if handed.HandedBack == nil || handed.HandedBack.Continuation != runstate.MergeQueueLandedOutside {
				t.Fatalf("HandBack() = %#v, want it to say the entry left as landed", handed)
			}
			for _, want := range []string{"pull request #1926", "merged by hand as #1926"} {
				if !strings.Contains(handback.Reason, want) {
					t.Fatalf("reason = %q, want it to name %q", handback.Reason, want)
				}
			}
			saved := f.run.latest()
			if saved.Integration == nil || saved.Integration.TargetCommit != merged || saved.Status == runstate.StatusFailed {
				t.Fatalf("run status %s, integration %#v; want the run landed at %s, not failed", saved.Status, saved.Integration, merged)
			}
			if !f.tracker.Closed || f.tracker.Blocked || !strings.Contains(f.tracker.Notes, "outside the merge queue") {
				t.Fatalf("item closed %v, blocked %v, notes %q; want it settled as landed", f.tracker.Closed, f.tracker.Blocked, f.tracker.Notes)
			}
			if repair.count() != 0 || len(repair.reported) != 0 {
				t.Fatalf("repair given %#v and reported %#v, want nothing asked of the development manager", repair.given, repair.reported)
			}
			if pending := f.pending(); len(pending) != 0 {
				t.Fatalf("Pending() = %v, want nothing left in the queue", pending)
			}
		})
	}
}

func TestAnEntryHandedBackWhoseMergeTheTargetDoesNotShowIsHandedBackSayingWhy(t *testing.T) {
	t.Parallel()

	f, _ := queueStoppedOnMerge(t)
	// The forge says the request merged, but the target has nothing of it.
	repair := f.runPullRequestReads(publish.PullRequest{Number: 1926, State: "MERGED", Merged: true, MergeCommit: f.head, HeadCommit: f.head})
	base := f.local("main")

	f.handBackByHand("merged by hand as #1926")
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueReleased {
		t.Fatalf("handback = %#v, want the entry handed back as before", handback)
	}
	for _, want := range []string{"merged by hand as #1926", "could not confirm the change reached main", "main was brought up to date from the remote first", "does not carry the approved head " + f.head, "pull request #1926 reads as merged"} {
		if !strings.Contains(handback.Reason, want) {
			t.Fatalf("reason = %q, want it to say %q", handback.Reason, want)
		}
	}
	if repair.count() != 1 {
		t.Fatalf("repair given %#v, want the change handed back to its run once", repair.given)
	}
	if f.run.latest().Integration != nil || f.tracker.Closed {
		t.Fatal("a change the target does not carry was recorded as landed")
	}
	if f.local("main") != base {
		t.Fatal("the target moved")
	}
}

func TestAReleasedHandbackNamesWhatStoppedTheRun(t *testing.T) {
	t.Parallel()

	f, runs, handback := releasedRunHandback(t)
	f.tracker.Item.Status = "in_progress"
	if err := (MergeQueueRunHandback{Store: runs, Tracker: f.tracker}).HandBack(context.Background(), f.entry, handback); err != nil {
		t.Fatalf("HandBack() error = %v", err)
	}
	saved := f.run.latest()
	if saved.StopClass != runstate.StopIntegration || saved.RecordedStopClass() == runstate.StopUnknown {
		t.Fatalf("stop class = %q, want the run's stop named as the change not landing", saved.StopClass)
	}
	if !strings.Contains(saved.Failure, handback.Reason) {
		t.Fatalf("failure = %q, want it to carry the hand-back's reason", saved.Failure)
	}
}

func TestAHandbackOfAnEntryWhoseItemIsClosedLeavesItClosed(t *testing.T) {
	t.Parallel()

	f, runs, handback := releasedRunHandback(t)
	f.tracker.Item.Status = "closed"
	given := MergeQueueRunHandback{Store: runs, Tracker: f.tracker}
	for range 2 {
		if err := given.HandBack(context.Background(), f.entry, handback); err != nil {
			t.Fatalf("HandBack() error = %v", err)
		}
	}
	if f.tracker.Item.Status != "closed" || f.tracker.Blocked {
		t.Fatalf("item status %q, blocked %v; want it left closed", f.tracker.Item.Status, f.tracker.Blocked)
	}
	if len(f.tracker.NoteRecords) != 1 || !strings.Contains(f.tracker.NoteRecords[0], handback.Reason) || !strings.Contains(f.tracker.NoteRecords[0], "left closed") {
		t.Fatalf("notes recorded %v, want the hand-back said once as a note", f.tracker.NoteRecords)
	}
	if f.run.saveCount() != 0 {
		t.Fatalf("run saved %d times, want its record left as it is", f.run.saveCount())
	}
}

func TestAHandMergeOnTheRemoteButNotYetOnTheLocalTargetEndsAsLanded(t *testing.T) {
	t.Parallel()

	f, _ := queueStoppedOnMerge(t)
	base := f.local("main")
	runPipelineGit(t, f.repository, "merge", "--no-ff", "-m", "Merge pull request #1926", f.head)
	merged := f.local("main")
	// The merge is on the remote, as after a merge on the forge, and the local
	// copy of the target has not followed it.
	runPipelineGit(t, f.repository, "push", f.remote, "main")
	runPipelineGit(t, f.repository, "reset", "--hard", base)
	if f.local("main") != base {
		t.Fatalf("main = %s, want it put back at %s", f.local("main"), base)
	}
	repair := f.runPullRequestReads(publish.PullRequest{Number: 1926, State: "MERGED", Merged: true, MergeCommit: merged, HeadCommit: f.head})

	f.handBackByHand("merged by hand as #1926")
	handback := f.landing().Handback
	if handback == nil || handback.Continuation != runstate.MergeQueueLandedOutside || handback.TargetBase != merged {
		t.Fatalf("handback = %#v, want the entry landed at %s", handback, merged)
	}
	if f.local("main") != merged {
		t.Fatalf("main = %s, want it brought up to date to %s", f.local("main"), merged)
	}
	if !f.tracker.Closed || f.tracker.Blocked || repair.count() != 0 {
		t.Fatalf("item closed %v, blocked %v, repairs %d; want it settled as landed", f.tracker.Closed, f.tracker.Blocked, repair.count())
	}
}
