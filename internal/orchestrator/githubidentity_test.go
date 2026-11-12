package orchestrator

// Taking up runs GitHub's refusal of the harness's identity stopped: only once
// GitHub answers again, each at the step it stopped at, and nothing written
// while it does not.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

const identityRefusalDetail = "publish the change: git push: fatal: No user exists for uid 501"

// landingRefusedState is an approved change whose promotion GitHub refused the
// harness's key for.
func landingRefusedState() runstate.State {
	state := approvedStoppedState()
	state.Environmental = &runstate.EnvironmentalRefusal{Cause: runstate.CauseRemoteAuthRefused, RecordedAt: *state.CompletedAt, Settled: true}
	state.IntegrationStop.Cause = runstate.CauseRemoteAuthRefused
	return state
}

// publishRefusedState is a finished change that stopped while publishing its
// branch, before its checks, for the same reason.
func publishRefusedState() runstate.State {
	state := approvedStoppedState()
	state.Phase = runstate.PhaseDeveloping
	state.Environmental = nil
	state.IntegrationStop = nil
	state.ReviewDecision = ""
	state.ReviewApproves = ""
	state.ReviewSessionID = ""
	state.ReviewModel = ""
	state.ReviewSummary = ""
	state.ReviewRounds = 0
	state.StopClass = runstate.StopPublish
	state.Failure = "publish the change: " + identityRefusalDetail
	state.IdentityStop = &runstate.IdentityStop{Detail: identityRefusalDetail, RecordedAt: *state.CompletedAt}
	return state
}

type identityProbe struct {
	err   error
	asked []string
}

func (p *identityProbe) answers(_ context.Context, target string) error {
	p.asked = append(p.asked, target)
	return p.err
}

type landingResumes struct {
	reasons []string
	result  IntegrationResumeResult
	err     error
}

func (l *landingResumes) resume(_ context.Context, _, reason string) (IntegrationResumeResult, error) {
	l.reasons = append(l.reasons, reason)
	return l.result, l.err
}

func (h *resumeHarness) identityStops(probe *identityProbe, landing *landingResumes) GitHubIdentityStops {
	resumer := h.resumer()
	return GitHubIdentityStops{
		Docket: h.docket, Runs: h.runs, Intake: h.intake, Items: h.tracker, Worktrees: h.ownership,
		Capacity: h.capacity, Clock: docketClock{}, Answers: probe.answers, ResumeLanding: landing.resume, Start: resumer.Start,
	}
}

func TestAnApprovedChangeStoppedAtLandingIsResumedOnceGitHubAnswers(t *testing.T) {
	t.Parallel()

	h := newResumeHarness(t, landingRefusedState())
	probe := &identityProbe{}
	landing := &landingResumes{result: IntegrationResumeResult{Resumed: true}}
	pass := h.identityStops(probe, landing).Take(context.Background())

	if pass.Stopped != 1 || pass.TakenUp != 1 || len(landing.reasons) != 1 {
		t.Fatalf("pass = %#v, resumptions = %v; want the one stopped change resumed", pass, landing.reasons)
	}
	if len(probe.asked) != 1 || probe.asked[0] != "main" {
		t.Fatalf("GitHub was asked about %v, want the change's target once", probe.asked)
	}
	for _, want := range []string{runstate.LocalStamp(docketedNow), "answered again with the same identity", "approval, passed checks and repair count kept", "no repair attempt or re-run spent"} {
		if !strings.Contains(landing.reasons[0], want) {
			t.Fatalf("reason = %q, want it to say %q", landing.reasons[0], want)
		}
	}
}

func TestAChangeStoppedWhilePublishingIsTakenUpAtItsChecksOnceGitHubAnswers(t *testing.T) {
	t.Parallel()

	h := newResumeHarness(t, publishRefusedState())
	probe := &identityProbe{}
	pass := h.identityStops(probe, &landingResumes{}).Take(context.Background())

	if pass.Stopped != 1 || pass.TakenUp != 1 {
		t.Fatalf("pass = %#v (%s), want the one stopped change taken up", pass, pass.Says)
	}
	if len(h.started) != 1 || h.started[0] != (continuedRun{workItemID: docketedItem, runID: docketedRunID}) {
		t.Fatalf("started = %#v, want the run continued once", h.started)
	}
	state := h.reload(t)
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseChecking || state.CompletedAt != nil || !state.PublishOwed {
		t.Fatalf("taken-up run = %s/%s owing a push %t, want it running at its checks owing the push", state.Status, state.Phase, state.PublishOwed)
	}
	if state.IdentityStop != nil || state.Failure != "" || state.StopClass != "" {
		t.Fatalf("taken-up run still carries its stop: %#v, failure %q, class %q", state.IdentityStop, state.Failure, state.StopClass)
	}
	if len(state.IdentityTakeUps) != 1 || state.IdentityTakeUps[0].SupersededFailure != publishRefusedState().Failure || !strings.Contains(state.IdentityTakeUps[0].Reason, runstate.LocalStamp(docketedNow)) {
		t.Fatalf("take-ups = %#v, want the one this made, carrying what it superseded and the local time", state.IdentityTakeUps)
	}
	if state.RepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want the count kept at 1", state.RepairAttempts)
	}
	counters, err := h.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatal(err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 || counters.Reruns != 0 {
		t.Fatalf("the item's triage record was spent: %#v", counters)
	}
	if !strings.Contains(h.tracker.Notes, "Taken up again by the harness") || !strings.Contains(h.tracker.Notes, "No user exists for uid 501") || !h.tracker.Claimed {
		t.Fatalf("item notes = %q, claimed = %t; want the take-up recorded and the item put back", h.tracker.Notes, h.tracker.Claimed)
	}
	closure, closed := h.closure(t)
	if !closed || closure.Decision != triage.ContinuedChecksDecision || !strings.Contains(closure.DecidedBy, "the harness") {
		t.Fatalf("docket closure = %#v, want the stoppage closed in the harness's name", closure)
	}
}

func TestNothingIsTouchedWhileGitHubStillDoesNotAnswer(t *testing.T) {
	t.Parallel()

	for name, state := range map[string]runstate.State{"landing": landingRefusedState(), "publishing": publishRefusedState()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newResumeHarness(t, state)
			probe := &identityProbe{err: errors.New("list main on origin: git@github.com: Permission denied (publickey)")}
			landing := &landingResumes{result: IntegrationResumeResult{Resumed: true}}
			pass := h.identityStops(probe, landing).Take(context.Background())

			if pass.Stopped != 1 || pass.TakenUp != 0 || len(landing.reasons) != 0 {
				t.Fatalf("pass = %#v, resumptions = %v; want the change left stopped", pass, landing.reasons)
			}
			for _, want := range []string{"stays stopped at " + name + " for the development manager", "GitHub still does not accept the harness's identity", "Permission denied (publickey)"} {
				if !strings.Contains(pass.Says, want) {
					t.Fatalf("says = %q, want %q", pass.Says, want)
				}
			}
			h.assertNothingWritten(t)
			after := h.reload(t)
			if (after.IdentityStop == nil) != (state.IdentityStop == nil) || after.PublishOwed || len(after.IdentityTakeUps) != 0 || after.UpdatedAt != state.UpdatedAt {
				t.Fatalf("run changed while GitHub did not answer: %#v", after)
			}
		})
	}
}

func TestAPublishingStopWhoseBranchOrCheckoutIsGoneIsNotTakenUp(t *testing.T) {
	t.Parallel()

	for name, checkout := range map[string]*orchestratortest.RecoveryCheckout{
		"checkout gone": {Ownership: &orchestratortest.Ownership{}, Present: false, Branch: true},
		"branch gone":   {Ownership: &orchestratortest.Ownership{}, Present: true, Branch: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newResumeHarness(t, publishRefusedState())
			stops := h.identityStops(&identityProbe{}, &landingResumes{})
			stops.Worktrees = checkout
			pass := stops.Take(context.Background())

			if pass.TakenUp != 0 || !strings.Contains(pass.Says, "branch or checkout is gone") || !strings.Contains(pass.Says, "for the development manager") {
				t.Fatalf("pass = %#v, want it left for the development manager with the gone branch or checkout said", pass)
			}
			if checkout.Restores != 0 {
				t.Fatalf("a checkout was restored %d times for a push the run owes", checkout.Restores)
			}
			h.assertNothingWritten(t)
		})
	}
}

func TestTakeHeldAlsoTakesUpTheRunsGitHubRefusedAndFoldsTheirCounts(t *testing.T) {
	t.Parallel()

	h := newResumeHarness(t, landingRefusedState())
	landing := &landingResumes{result: IntegrationResumeResult{Resumed: true}}
	stops := h.identityStops(&identityProbe{}, landing)
	driver := MergeQueueDriver{Identity: &stops}

	pass := driver.TakeHeld(context.Background())
	if pass.Held != 1 || pass.Resumed != 1 || len(landing.reasons) != 1 || !strings.Contains(pass.Says, "taken up again at its landing") {
		t.Fatalf("pass = %#v, resumptions = %v; want the stopped change counted and taken up", pass, landing.reasons)
	}
	if empty := (MergeQueueDriver{}).TakeHeld(context.Background()); empty != (MergeQueueHeldPass{}) {
		t.Fatalf("a driver with nothing to look at found %#v", empty)
	}
}
