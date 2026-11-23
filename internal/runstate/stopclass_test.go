package runstate

import "testing"

// The reason a surface prints leads with the class, and each half survives the
// other's absence: a record written before the class existed prints its reason
// as it was written, and a succeeded run whose only account is what it stopped
// short of prints that account under its class.
func TestTheReasonLeadsWithTheClassThatStoppedTheRun(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		summary RunSummary
		want    string
	}{
		{
			name:    "a failure under its class",
			summary: RunSummary{StopClass: StopChecks, Failure: "verification failed after 2 of 2 permitted attempt(s): make test exited with 1"},
			want:    "checks: verification failed after 2 of 2 permitted attempt(s): make test exited with 1",
		},
		{
			name:    "a record written before the class existed",
			summary: RunSummary{Failure: "developer backend failed: signal: killed"},
			want:    "developer backend failed: signal: killed",
		},
		{
			name:    "a late completion record on a succeeded run",
			summary: RunSummary{StopClass: StopRecording, CompletionRecordingFailure: "save completed run state after cleanup: disk full"},
			want:    "recording: save completed run state after cleanup: disk full",
		},
		{
			name:    "an outstanding publication on a succeeded run",
			summary: RunSummary{StopClass: StopPublish, PublishFailure: "confirm the pull request merged: the forge is unreachable"},
			want:    "publish: confirm the pull request merged: the forge is unreachable",
		},
		{
			name:    "an outstanding cleanup on a succeeded run",
			summary: RunSummary{StopClass: StopCleanup, CleanupFailure: "remove worktree: directory is busy"},
			want:    "cleanup: remove worktree: directory is busy",
		},
		{
			// The run's own failure is its reason whatever else the record holds.
			name:    "a failure beside a bookkeeping account",
			summary: RunSummary{StopClass: StopRecording, Failure: "close integrated work item: bd close failed", CompletionRecordingFailure: "disk full"},
			want:    "recording: close integrated work item: bd close failed",
		},
		{
			name:    "a class with nothing beside it",
			summary: RunSummary{StopClass: StopCancelled},
			want:    "cancelled",
		},
		{
			name:    "a run nothing stopped",
			summary: RunSummary{},
			want:    "",
		},
		{
			// A stoppage settled onto a record a killed process had already left
			// terminal carries the blocker and no failure.
			name:    "a blocker and no failure, under its class",
			summary: RunSummary{Status: StatusCancelled, Outcome: OutcomeStopped, StopClass: StopOutside, Blocker: "no attempt of the harness can finish it"},
			want:    "outside: no attempt of the harness can finish it",
		},
		{
			name:    "a blocker and no failure, before the class existed",
			summary: RunSummary{Status: StatusCancelled, Outcome: OutcomeStopped, Blocker: "no attempt of the harness can finish it"},
			want:    "no attempt of the harness can finish it",
		},
		{
			name:    "a run that ended badly and gives nothing",
			summary: RunSummary{Status: StatusFailed, Outcome: OutcomeFailed},
			want:    NoReasonSays,
		},
		{
			// An outstanding publication is said under its own label, not as the
			// reason the work ended.
			name:    "a run that ended badly with only a publication to account for",
			summary: RunSummary{Status: StatusFailed, Outcome: OutcomeFailed, PublishFailure: "push refused"},
			want:    "",
		},
		{
			name:    "a run still going",
			summary: RunSummary{Status: StatusRunning},
			want:    "",
		},
	} {
		if got := test.summary.Reason(); got != test.want {
			t.Errorf("%s: Reason() = %q, want %q", test.name, got, test.want)
		}
	}
}

// The record and its summary give one reason, which is what keeps the channel
// line, read off the record, and `yoyo status`, read off the summary, from
// saying two things about one run.
func TestTheRecordAndItsSummaryGiveOneReason(t *testing.T) {
	t.Parallel()

	for _, state := range []State{
		{Status: StatusCancelled, StopClass: StopOutside, Blocker: "no attempt of the harness can finish it"},
		{Status: StatusFailed, Failure: "developer backend failed: signal: killed", Blocker: "handed back"},
		{Status: StatusFailed},
		{Status: StatusSucceeded, StopClass: StopCleanup, CleanupFailure: "remove worktree: directory is busy"},
	} {
		summary := RunSummary{
			Status: state.Status, Outcome: state.Outcome(), StopClass: state.StopClass,
			Failure: state.Failure, Blocker: state.Blocker, PublishFailure: state.PublishFailure,
			CleanupFailure: state.CleanupFailure, CompletionRecordingFailure: state.CompletionRecordingFailure,
		}
		if got, want := state.Reason(), summary.Reason(); got != want {
			t.Errorf("State.Reason() = %q, RunSummary.Reason() = %q, want one reason", got, want)
		}
	}
}

func TestOldRunSummariesKeepTheReasonWhileTheirCauseReadsAsUnknown(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	for _, state := range []State{
		{Status: StatusFailed},
		{Status: StatusFailed, Blocker: "the check kept failing"},
		{Status: StatusFailed, PublishFailure: "the push was refused"},
		{Status: StatusSucceeded},
	} {
		summary := store.summarize(state)
		if summary.StopClass != StopUnknown || state.RecordedStopClass() != StopUnknown || summary.Reason() != state.Reason() {
			t.Fatalf("old record cause/reason differs: state %s/%q, summary %s/%q", state.RecordedStopClass(), state.Reason(), summary.StopClass, summary.Reason())
		}
	}
}

// A run that succeeded by handing its change to the merge queue, which has not
// landed it or handed it back, has not stopped: neither its record nor its
// listing names a stop cause, and no stop is written for it. The same run with a
// blocker, or failed once the queue lost its entry, has stopped.
func TestARunWaitingInTheMergeQueueRecordsNoStop(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	queued := State{Status: StatusSucceeded, Phase: PhaseCleaningUp, MergeQueue: &MergeQueueAdmissionRecord{EntryID: "mqe-1", Order: 34, TargetBranch: "main"}}
	summary := store.summarize(queued)
	if queued.RecordedStopClass() != "" || summary.StopClass != "" || summary.RecordedStopClass() != "" || queued.stoppedNow() {
		t.Fatalf("a queued run reads as stopped: record %q, listing %q/%q, stopped %t", queued.RecordedStopClass(), summary.StopClass, summary.RecordedStopClass(), queued.stoppedNow())
	}
	blocked := queued
	blocked.Blocker = "handed to the development manager"
	if blocked.RecordedStopClass() != StopUnknown {
		t.Fatalf("a queued run with a blocker records %q, want %q", blocked.RecordedStopClass(), StopUnknown)
	}
	lost := queued
	lost.Status, lost.StopClass = StatusFailed, StopIntegration
	if lost.RecordedStopClass() != StopIntegration {
		t.Fatalf("a run whose queue entry was lost records %q, want %q", lost.RecordedStopClass(), StopIntegration)
	}
}
