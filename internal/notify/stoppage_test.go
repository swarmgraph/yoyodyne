package notify

// How loudly a stoppage is said, and where.
//
// Critical is what reaches the operator wherever he is. On 2026-09-25 he was
// paged as critical for yoyodyne-ifd.435.3 (run-59207a66): an approved change
// that lost its race for main twice and stopped for the development manager to
// re-run. Nothing about it was his, and nothing about it was wrong. So a
// stoppage's severity follows who moves next and what stopped it: critical only
// where that is the operator, a note in the item's thread where the environment
// stopped it and a role or the harness moves next, and a warning where the work
// stopped it and a role decides.

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// stoppedRun is a run that ended on a blocker, with whatever stopped it applied
// by the caller.
func stoppedRun(apply func(*runstate.State)) runstate.State {
	state := endedRun(running(), runstate.StatusFailed)
	state.Failure = "what the record gave as the reason"
	state.Blocker = runstate.RecordBlocker("a person has to decide what happens to this item")
	apply(&state)
	return state
}

// approvedAndStoppedBy is an approved change the environment stopped on its way
// to the target, by the cause named.
func approvedAndStoppedBy(cause runstate.EnvironmentalCause) func(*runstate.State) {
	return func(state *runstate.State) {
		state.Phase = runstate.PhaseIntegrating
		state.ReviewDecision = runstate.ReviewApprove
		state.IntegrationStop = &runstate.IntegrationStop{
			Cause:      cause,
			Phase:      runstate.PhaseIntegrating,
			RecordedAt: moment,
		}
	}
}

// refusedBy is a round the settle classified as refused by the cause named.
func refusedBy(cause runstate.EnvironmentalCause) func(*runstate.State) {
	return func(state *runstate.State) {
		refusal := &runstate.EnvironmentalRefusal{Cause: cause, RecordedAt: moment, Settled: true, Refused: true}
		if cause == runstate.CauseUsageWindow {
			resets := moment.Add(96 * time.Hour)
			refusal.ResetsAt = &resets
		}
		state.Environmental = refusal
	}
}

func TestAStoppageIsSaidAtTheSeverityItsNextMoverAndItsCauseWarrant(t *testing.T) {
	for _, stoppage := range []struct {
		name     string
		apply    func(*runstate.State)
		severity report.Severity
		reach    Reach
	}{
		// The operator's: a cause only a person clears on the machine. A provider
		// nobody can reach is the third cause the item names, and it has no case
		// here because it never ends a run as a stoppage: a run the provider stops
		// answering parks on runstate.PauseProviderUnauthenticated or
		// PauseProviderUnreachable with its claim and worktree kept, spends no
		// budget, and is never blocked (awaitProviderOutage in
		// internal/orchestrator). The operators hear it from the provider-outage
		// message, which is theirs directly.
		{"a target that diverged from the remote's", approvedAndStoppedBy(runstate.CauseDivergedTarget), report.SeverityCritical, ReachChannel},
		{"a credential the remote refused", approvedAndStoppedBy(runstate.CauseRemoteAuthRefused), report.SeverityCritical, ReachChannel},
		{"a primary checkout carrying state the harness does not own", approvedAndStoppedBy(runstate.CauseDirtyPrimary), report.SeverityCritical, ReachChannel},

		// A role's or the harness's, on an environmental cause: routine, and said in
		// the item's thread.
		{"a lost race, as run-59207a66 recorded it", func(state *runstate.State) {
			state.Phase = runstate.PhaseIntegrating
			state.ReviewDecision = runstate.ReviewApprove
			state.IntegrationRetries = 2
			state.Failure = runstate.ContendedIntegrationFailure + " after 2 of 2 permitted retry(s): integrate approved change: target branch moved away from the recorded base commit"
		}, report.SeverityNote, ReachThread},
		{"a replay the harness's budget killed", approvedAndStoppedBy(runstate.CauseReplayKilled), report.SeverityNote, ReachThread},
		{"a tracker read that timed out", approvedAndStoppedBy(runstate.CauseTransportFailure), report.SeverityNote, ReachThread},
		{"a usage window past the longest wait", refusedBy(runstate.CauseUsageWindow), report.SeverityNote, ReachThread},
		{"a handback that carried none of the change", refusedBy(runstate.CauseHandbackMissingChange), report.SeverityNote, ReachThread},

		// A role's, on a verdict about the change: a real decision, at the channel.
		{"findings nobody repaired", func(state *runstate.State) {
			state.ReviewDecision = runstate.ReviewRepair
			state.ReviewFindings = 2
		}, report.SeverityWarning, ReachChannel},
		{"a check that kept failing", func(state *runstate.State) {
			state.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 2}
		}, report.SeverityWarning, ReachChannel},
		{"paths the item never granted", func(state *runstate.State) {
			state.PathRefusal = &runstate.PathRefusal{Paths: []string{"docs/product/brief.md"}}
		}, report.SeverityWarning, ReachChannel},
	} {
		after := stoppedRun(stoppage.apply)
		kinds, notifications := crossed(t, running(), after)
		said := only(t, notifications, KindBlockerRecorded)
		if said.Event.Severity != stoppage.severity {
			t.Errorf("a stoppage on %s is said as %s among %v, want %s", stoppage.name, said.Event.Severity, kinds, stoppage.severity)
		}
		if reach := said.Reach(); reach != stoppage.reach {
			t.Errorf("a stoppage on %s reaches the %s, want the %s", stoppage.name, reach, stoppage.reach)
		}
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render a stoppage on %s: %v", stoppage.name, err)
		}
		critical := strings.Contains(message.Body, "Critical")
		if critical != (stoppage.severity == report.SeverityCritical) {
			t.Errorf("a stoppage on %s at %s reads %q", stoppage.name, stoppage.severity, message.Body)
		}
	}
}

// The mover is the docket's reading of the same record: an approved change the
// environment stopped whose branch is there is the harness's to resume, one whose
// branch is gone is the development manager's like any other undecided
// stoppage. A cause only a person clears does not change the mover — it is the
// same harness's resume the docket names — and it is the severity alone that
// makes such a stoppage the operator's; the message's Next clause still names
// the harness, as the docket does.
func TestAStoppagesNextMoverIsTheDocketsReadingOfIt(t *testing.T) {
	for _, reading := range []struct {
		name  string
		apply func(*runstate.State)
		want  readmodel.Mover
	}{
		{"a resumable stop on a transport failure", approvedAndStoppedBy(runstate.CauseTransportFailure), readmodel.MoverHarness},
		{"a stop on a transport failure whose branch is gone", func(state *runstate.State) {
			approvedAndStoppedBy(runstate.CauseTransportFailure)(state)
			state.BranchRemoved = true
		}, readmodel.MoverDevelopmentManager},
		{"a resumable stop on a diverged target", approvedAndStoppedBy(runstate.CauseDivergedTarget), readmodel.MoverHarness},
		{"a lost race", func(state *runstate.State) {
			state.ReviewDecision = runstate.ReviewApprove
			state.Failure = runstate.ContendedIntegrationFailure + " after 2 of 2 permitted retry(s)"
		}, readmodel.MoverDevelopmentManager},
		{"a check that kept failing", func(state *runstate.State) {
			state.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 2}
		}, readmodel.MoverDevelopmentManager},
	} {
		after := stoppedRun(reading.apply)
		if got := readmodel.StoppageMover(after, nil, false); got != reading.want {
			t.Errorf("%s moves next by %s, want %s", reading.name, got, reading.want)
		}
	}
}

// The message that pages the operator for a cause only he clears names the same
// next move the docket does, rather than a mover of its own: the harness's
// resume, once he has cleared it.
func TestACriticalStoppageNamesTheDocketsNextMove(t *testing.T) {
	after := stoppedRun(approvedAndStoppedBy(runstate.CauseDivergedTarget))
	_, notifications := crossed(t, running(), after)
	said := only(t, notifications, KindBlockerRecorded)
	if said.Event.Severity != report.SeverityCritical {
		t.Fatalf("a stoppage on a diverged target is said as %s, want critical", said.Event.Severity)
	}
	if want := "the harness's — " + after.IntegrationStop.ResumeSays(after.RunID); said.Event.Detail.Mover != want {
		t.Fatalf("the critical stoppage names %q as its next move, want the docket's %q", said.Event.Detail.Mover, want)
	}
}

// A cause recorded on a round that delivered a change anyway is not what stopped
// the run: the round spent as any round does, and the stoppage is the work's.
func TestACauseTheSettleDidNotClassifyLeavesTheStoppageTheWorks(t *testing.T) {
	after := stoppedRun(func(state *runstate.State) {
		refusedBy(runstate.CauseTransportFailure)(state)
		state.Environmental.Refused = false
	})
	_, notifications := crossed(t, running(), after)
	if said := only(t, notifications, KindBlockerRecorded); said.Event.Severity != report.SeverityWarning {
		t.Fatalf("a stoppage whose recorded cause delivered a change anyway is said as %s, want a warning", said.Event.Severity)
	}
}
