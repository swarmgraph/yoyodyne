package notify

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func running() runstate.State {
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-4d1f",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.68.2",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     moment,
		UpdatedAt:     moment,
		Selection: &runstate.Selection{
			By:     runstate.SelectedByDevelopmentManager,
			Reason: "highest-priority item nothing is holding back",
			At:     moment,
		},
	}
}

func TestEveryStopCauseIsNamedInTheRunsThread(t *testing.T) {
	t.Parallel()
	for _, class := range runstate.StopClasses() {
		before := running()
		after := before
		after.Status = runstate.StatusFailed
		after.StopClass = class
		after.Failure = "the harness ended this run"
		after.CompletedAt = &moment
		notifications, err := FromRun(before, after, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, notification := range notifications {
			if notification.Event.Kind == KindRunEnded {
				rendered, err := Render(notification.Topic, notification.Speaker, notification.Event)
				if err != nil || !strings.Contains(rendered.Body, class.Name()+":") {
					t.Fatalf("%s thread rendering = %v, error %v", class, rendered, err)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("%s produced no run ending notification", class)
		}
	}
	old := running()
	old.Status = runstate.StatusFailed
	old.PublishFailure = "the push was refused"
	if got := endingReason(old); got != "unknown: the push was refused" {
		t.Fatalf("old run ending = %q", got)
	}
}

// crossed reports the kinds one reading to the next produced, in order, and
// fails the test if anything it produced could not be said.
func crossed(t *testing.T, before, after runstate.State) ([]Kind, []Notification) {
	t.Helper()
	notifications, err := FromRun(before, after, nil)
	if err != nil {
		t.Fatalf("select from run state: %v", err)
	}
	kinds := make([]Kind, 0, len(notifications))
	for _, notification := range notifications {
		if _, err := Render(notification.Topic, notification.Speaker, notification.Event); err != nil {
			t.Fatalf("a selected %s could not be said: %v", notification.Event.Kind, err)
		}
		kinds = append(kinds, notification.Event.Kind)
	}
	return kinds, notifications
}

func only(t *testing.T, notifications []Notification, kind Kind) Notification {
	t.Helper()
	for _, notification := range notifications {
		if notification.Event.Kind == kind {
			return notification
		}
	}
	t.Fatalf("nothing said %s", kind)
	return Notification{}
}

func TestARunStartingCarriesWhyItWasSelected(t *testing.T) {
	// The reason the invariant makes durable is the reason an operator reads.
	kinds, notifications := crossed(t, runstate.State{}, running())
	if len(kinds) != 1 || kinds[0] != KindRunStarted {
		t.Fatalf("a fresh run crossed %v", kinds)
	}
	started := notifications[0]
	if started.Topic.Key() != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("addressed to %q", started.Topic.Key())
	}
	if started.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", started.Speaker.Key())
	}
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "highest-priority item nothing is holding back") {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
}

// The record says what the item is called, so every message a run produces is
// addressed to a topic that can be named in words. The sink reads durable
// records and never the tracker, so a title the run did not write down is a
// title nothing downstream can recover.
func TestARunAddressesItsTopicByWhatTheRecordCallsTheItem(t *testing.T) {
	state := running()
	state.WorkItemTitle = "Slack thread headers carry the item's title, not just its slug"
	_, notifications := crossed(t, runstate.State{}, state)
	started := notifications[0]
	if started.Topic.Key() != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("addressed to %q, want the identifier to stay the key", started.Topic.Key())
	}
	if started.Topic.Title != state.WorkItemTitle {
		t.Fatalf("topic title = %q, want %q", started.Topic.Title, state.WorkItemTitle)
	}
	// A run recorded before titles were written is addressed exactly as it was.
	_, older := crossed(t, runstate.State{}, running())
	if older[0].Topic.Title != "" {
		t.Fatalf("topic title = %q, want nothing where the record carried nothing", older[0].Topic.Title)
	}
}

// A run's opening message says what it is running as, in every voice that can
// open one: which account it is spending and which configuration set it up. It
// is said where the thread opens rather than on every crossing after it, because
// neither changes for the life of a run — and it is said at all because the day
// there are two accounts, this is the message an operator will read to find out
// which one a thread is burning.
func TestARunStartingSaysWhichAccountAndConfigurationItRunsUnder(t *testing.T) {
	for _, selected := range []string{
		runstate.SelectedByDevelopmentManager,
		runstate.SelectedByOperator,
		runstate.SelectedByScheduler,
	} {
		state := running()
		state.Selection = &runstate.Selection{By: selected, Reason: "named on the command line", At: moment}
		state.AccountAlias = "default"
		state.ConfigRevision = "cfg-0123456789ab"
		_, notifications := crossed(t, runstate.State{}, state)
		started := only(t, notifications, KindRunStarted)
		message, err := Render(started.Topic, started.Speaker, started.Event)
		if err != nil {
			t.Fatalf("render a run selected by the %s: %v", selected, err)
		}
		if !strings.Contains(message.Body, "account default") || !strings.Contains(message.Body, "configuration cfg-0123456789ab") {
			t.Fatalf("the %s's opening message does not say what the run runs as: %q", started.Speaker.Key(), message.Body)
		}
	}
	// A run recorded before either was carried says so rather than leaving a hole
	// in the sentence.
	_, notifications := crossed(t, runstate.State{}, running())
	started := only(t, notifications, KindRunStarted)
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "an account the record does not name") {
		t.Fatalf("body %q does not state the absence", message.Body)
	}
}

func TestARunStartingIsSpokenByTheSelectorTheRecordNames(t *testing.T) {
	// The development manager speaks only for the work its own triage chose. The
	// operator is not a persona and the scheduler is not a role, so a run either
	// of them selected is the harness's account rather than anybody's judgment.
	for _, selected := range []struct {
		by      string
		speaker string
	}{
		{by: runstate.SelectedByDevelopmentManager, speaker: string(domain.RoleDevelopmentManager)},
		{by: runstate.SelectedByOperator, speaker: HarnessSpeaker},
		{by: runstate.SelectedByScheduler, speaker: HarnessSpeaker},
	} {
		state := running()
		state.Selection = &runstate.Selection{By: selected.by, Reason: "named on the command line", At: moment}
		_, notifications := crossed(t, runstate.State{}, state)
		started := only(t, notifications, KindRunStarted)
		if started.Speaker.Key() != selected.speaker {
			t.Fatalf("a run selected by the %s is spoken by the %s, want the %s", selected.by, started.Speaker.Key(), selected.speaker)
		}
	}
}

func TestAnOperatorNamedRunSaysTheOperatorChoseIt(t *testing.T) {
	// The harness speaks for it, and what it says still names the selector: the
	// operator is who a reader has to be able to see behind the choice.
	state := running()
	state.Selection = &runstate.Selection{By: runstate.SelectedByOperator, Reason: "from a conversation, after turn 103", At: moment}
	_, notifications := crossed(t, runstate.State{}, state)
	started := only(t, notifications, KindRunStarted)
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, runstate.SelectedByOperator) {
		t.Fatalf("body %q does not say the operator chose it", message.Body)
	}
	if !strings.Contains(message.Body, "from a conversation, after turn 103") {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
}

func TestARunWithNoRecordedSelectionSaysSoRatherThanNothing(t *testing.T) {
	state := running()
	state.Selection = nil
	_, notifications := crossed(t, runstate.State{}, state)
	started := only(t, notifications, KindRunStarted)
	// Nobody is named as having chosen it, so nobody speaks for it either.
	if !started.Speaker.IsHarness() {
		t.Fatalf("an unaccounted run is spoken by the %s", started.Speaker.Key())
	}
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "no reason recorded") {
		t.Fatalf("an unaccounted run reads as %q", message.Body)
	}
}

func TestAFreshAttemptIsNotComparedToThePreviousOne(t *testing.T) {
	// A second run at the same item must not read as the first one losing its
	// promotion, its verdict, and its published request.
	first := running()
	first.Phase = runstate.PhaseCompleting
	first.ReviewDecision = runstate.ReviewApprove
	first.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("a", 40)}
	second := running()
	second.RunID = "run-91b2"
	kinds, _ := crossed(t, first, second)
	if len(kinds) != 1 || kinds[0] != KindRunStarted {
		t.Fatalf("a fresh attempt crossed %v", kinds)
	}
}

func TestEachCrossingIsSaidOnce(t *testing.T) {
	state := running()
	state.Phase = runstate.PhaseReviewing
	state.ReviewDecision = runstate.ReviewApprove
	kinds, _ := crossed(t, runstate.State{}, state)
	if len(kinds) == 0 {
		t.Fatal("a fresh run crossed nothing")
	}
	if again, _ := crossed(t, state, state); len(again) != 0 {
		t.Fatalf("re-reading the same record said %v again", again)
	}
}

func TestASinkThatStartsLateSaysWhatItMissed(t *testing.T) {
	// At-least-once catch-up: a sink that was away must not pretend the run began
	// where it first read it.
	state := running()
	state.Phase = runstate.PhaseCompleting
	state.ReviewDecision = runstate.ReviewApprove
	state.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: strings.Repeat("a", 40), SourceCommit: strings.Repeat("a", 40)}
	kinds, _ := crossed(t, runstate.State{}, state)
	for _, want := range []Kind{KindRunStarted, KindChecksPassed, KindReviewApproved, KindPromoted} {
		found := false
		for _, kind := range kinds {
			if kind == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("a late sink did not say %s; it said %v", want, kinds)
		}
	}
}

func TestAFailingCheckIsSaidAsAWarningInTheChecksOwnWords(t *testing.T) {
	before := running()
	after := before
	after.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindChecksFailed {
		t.Fatalf("a failing check crossed %v", kinds)
	}
	failed := only(t, notifications, KindChecksFailed)
	if failed.Event.Severity != report.SeverityWarning {
		t.Fatalf("a failing check is a %s", failed.Event.Severity)
	}
	message, err := Render(failed.Topic, failed.Speaker, failed.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "go test ./...") || !strings.Contains(message.Body, "1") {
		t.Fatalf("body %q does not carry the check itself", message.Body)
	}
	// The same failure again is the same fact; a different one is news.
	if again, _ := crossed(t, after, after); len(again) != 0 {
		t.Fatalf("the same failing check was said twice: %v", again)
	}
	differently := after
	differently.CheckFailure = &runstate.CheckFailure{Command: "go vet ./...", ExitCode: 2}
	if kinds, _ := crossed(t, after, differently); len(kinds) != 1 || kinds[0] != KindChecksFailed {
		t.Fatalf("a different failing check crossed %v", kinds)
	}
}

// A change the protected-path gate refused used to be silent in the thread: the
// blocker note and `yoyo status` said why a round was spent, and a reader of the
// thread saw a repair round happen with no stated reason. It is said now, in the
// developer's voice, naming the refused path and the grant that would admit it.
func TestARefusedPathIsSaidByTheDeveloperWithThePathAndTheGrantThatWouldAdmitIt(t *testing.T) {
	before := running()
	after := before
	after.RepairAttempts = 1
	after.PathRefusal = &runstate.PathRefusal{
		Paths:  []string{"docs/designs/v1-harness-design.md"},
		Grants: []string{"docs/product/brief.md"},
	}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindPathRefused {
		t.Fatalf("a refused path crossed %v", kinds)
	}
	said := only(t, notifications, KindPathRefused)
	if said.Speaker.Role != domain.RoleDeveloper {
		t.Fatalf("a refused path is spoken by %q, want the developer", said.Speaker.Key())
	}
	if said.Event.Severity != report.SeverityWarning {
		t.Fatalf("a refused path is a %s; a repair round spent is a warning as a failing check is", said.Event.Severity)
	}
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"docs/designs/v1-harness-design.md",
		"docs/product/brief.md",
		"`protected-path grant: docs/designs/v1-harness-design.md`",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("body %q does not say %q", message.Body, want)
		}
	}
	if message.Reach != ReachThread {
		t.Fatalf("a refused path reaches %q, want the item's thread: it is the developer's next attempt", message.Reach)
	}
}

// Once per refusal, as the round it bought: the same refusal read twice on one
// round is one refusal; the same paths refused again on the next round are a
// second round spent for the same stated cause; a refusal that cleared says
// nothing of its own, because what follows it is the checks, and they say what
// they find; and a refusal that found the budget already gone spent no round
// and is the blocker line's to say, not the developer's, whose line would
// promise an attempt the run is not going to make.
func TestARefusedPathIsSaidOncePerRefusal(t *testing.T) {
	before := running()
	first := before
	first.RepairAttempts = 1
	first.PathRefusal = &runstate.PathRefusal{Paths: []string{"docs/designs/v1-harness-design.md"}}
	if kinds, _ := crossed(t, before, first); len(kinds) != 1 || kinds[0] != KindPathRefused {
		t.Fatalf("the first refusal crossed %v", kinds)
	}
	if again, _ := crossed(t, first, first); len(again) != 0 {
		t.Fatalf("the same refusal on the same round was said twice: %v", again)
	}
	// The developer reaches for the same path again: a second round spent.
	second := first
	second.RepairAttempts = 2
	if kinds, _ := crossed(t, first, second); len(kinds) != 1 || kinds[0] != KindPathRefused {
		t.Fatalf("the same paths refused on a later round crossed %v", kinds)
	}
	// The paths taken back out: the refusal clears and nothing is said for it.
	cleared := second
	cleared.PathRefusal = nil
	if kinds, _ := crossed(t, second, cleared); len(kinds) != 0 {
		t.Fatalf("a cleared refusal crossed %v", kinds)
	}
	// The budget spent, the gate refuses once more and the run blocks: the
	// record carries the refusal with the count unmoved and the blocker beside
	// it, and the blocker line is the one that names the paths.
	blocked := endedRun(second, runstate.StatusFailed)
	blocked.PathRefusal = &runstate.PathRefusal{Paths: []string{"docs/product/brief.md"}}
	blocked.Failure = "protected paths refused after 2 of 2 permitted attempt(s): docs/product/brief.md"
	blocked.Blocker = runstate.RecordBlocker(blocked.Failure)
	kinds, _ := crossed(t, second, blocked)
	if slices.Contains(kinds, KindPathRefused) || !slices.Contains(kinds, KindBlockerRecorded) {
		t.Fatalf("a refusal that spent no round crossed %v; it is the blocker line's to say", kinds)
	}
	// A sink that starts late says the refusal along with everything else the
	// record already holds — and, on a run that blocked on its refusal, says the
	// blocker and not a developer promising an attempt.
	kinds, _ = crossed(t, runstate.State{}, first)
	if !slices.Contains(kinds, KindPathRefused) || !slices.Contains(kinds, KindRunStarted) {
		t.Fatalf("a late sink said %v", kinds)
	}
	kinds, _ = crossed(t, runstate.State{}, blocked)
	if slices.Contains(kinds, KindPathRefused) || !slices.Contains(kinds, KindBlockerRecorded) {
		t.Fatalf("a late sink on a run blocked on its refusal said %v", kinds)
	}
}

// The bound on what a refusal records is said rather than dropped: a developer
// has every one of the paths to take back out, and a list that stopped without
// saying so would read as the whole of what the gate caught. An item that grants
// nothing is said to grant nothing, which is what tells a reader whether the
// change reached outside its item or the item is missing a grant.
func TestARefusedPathSaysWhatWasLeftOffTheListAndWhatTheItemGrants(t *testing.T) {
	before := running()
	after := before
	after.RepairAttempts = 1
	after.PathRefusal = &runstate.PathRefusal{
		Paths:   []string{"docs/designs/a.md", "docs/designs/b.md"},
		Omitted: 3,
	}
	_, notifications := crossed(t, before, after)
	said := only(t, notifications, KindPathRefused)
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"docs/designs/a.md, docs/designs/b.md and 3 further paths not listed here",
		"the item grants nothing",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("body %q does not say %q", message.Body, want)
		}
	}
}

// Passing checks are said with what the stage spent of its bound, because the
// bound is the number that says whether a stage was slow, and a thread that
// said only "passed" left an operator to work out from timestamps that the
// checks had taken two hours.
func TestPassingChecksAreSaidWithWhatTheStageSpentOfItsBound(t *testing.T) {
	before := running()
	after := before
	after.Phase = runstate.PhaseReviewing
	finished := moment.Add(14 * time.Minute)
	after.CheckStage = &runstate.CheckStage{
		StartedAt: moment, BoundSeconds: 1800, Command: "make vet",
		FinishedAt: &finished, ElapsedSeconds: 14 * 60,
	}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindChecksPassed {
		t.Fatalf("passing checks crossed %v", kinds)
	}
	passed := only(t, notifications, KindChecksPassed)
	message, err := Render(passed.Topic, passed.Speaker, passed.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "in 14m0s of the 30m0s bound") {
		t.Fatalf("body %q does not say what the stage spent of its bound", message.Body)
	}
}

// A landing is said once its checks have ended: green stays in the thread as a
// note, red is a warning carrying the item it filed, and one whose checks did
// not run is said as that rather than as either. None is said again while the
// record stands, and none is said while the checks are still running.
func TestALandingIsSaidOnceWhicheverWayItWent(t *testing.T) {
	commit := strings.Repeat("c", 40)
	finished := moment.Add(20 * time.Minute)
	for _, test := range []struct {
		name     string
		landing  runstate.LandingChecks
		kind     Kind
		severity report.Severity
		body     string
	}{
		{
			name: "green",
			landing: runstate.LandingChecks{Commit: commit, StartedAt: moment, FinishedAt: &finished, BoundSeconds: 1800, Ran: true, Green: true,
				Checks: []runstate.LandingCheckResult{{Command: "make race", Passed: true}}},
			kind: KindLandingGreen, severity: report.SeverityNote, body: "green landing: 1 landing check passed over cccccccccccc",
		},
		{
			name: "red",
			landing: runstate.LandingChecks{Commit: commit, StartedAt: moment, FinishedAt: &finished, BoundSeconds: 1800, Ran: true,
				Checks: []runstate.LandingCheckResult{{Command: "make race", ExitCode: 1}}, FiledWorkItem: "yoyodyne-ifd.500"},
			kind: KindLandingRed, severity: report.SeverityWarning, body: "red landing: make race exited 1 over cccccccccccc; filed as yoyodyne-ifd.500",
		},
		{
			name:    "unverified",
			landing: runstate.LandingChecks{Commit: commit, StartedAt: moment, FinishedAt: &finished, BoundSeconds: 1800, Problem: "no checkout of the integrated commit could be cut"},
			kind:    KindLandingUnverified, severity: report.SeverityWarning, body: "unverified landing: the landing checks did not run to the end over cccccccccccc (no checkout of the integrated commit could be cut)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := running()
			before.Status = runstate.StatusSucceeded
			before.Phase = runstate.PhaseComplete
			running := before
			started := test.landing
			started.FinishedAt = nil
			running.LandingChecks = &started
			if kinds, _ := crossed(t, before, running); len(kinds) != 0 {
				t.Fatalf("landing checks still running crossed %v", kinds)
			}
			after := before
			landing := test.landing
			after.LandingChecks = &landing
			kinds, notifications := crossed(t, running, after)
			if len(kinds) != 1 || kinds[0] != test.kind {
				t.Fatalf("a %s landing crossed %v", test.name, kinds)
			}
			said := only(t, notifications, test.kind)
			if said.Event.Severity != test.severity {
				t.Fatalf("a %s landing is a %s, want %s", test.name, said.Event.Severity, test.severity)
			}
			message, err := Render(said.Topic, said.Speaker, said.Event)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(message.Body, test.body) {
				t.Fatalf("body %q does not carry %q", message.Body, test.body)
			}
			if again, _ := crossed(t, after, after); len(again) != 0 {
				t.Fatalf("the landing was said twice: %v", again)
			}
		})
	}
}

func TestTheVerdictIsTheReviewersOwnAccount(t *testing.T) {
	before := running()
	before.Phase = runstate.PhaseReviewing
	for decision, want := range map[string]Kind{
		runstate.ReviewApprove: KindReviewApproved,
		runstate.ReviewRepair:  KindReviewRepairs,
	} {
		after := before
		after.ReviewDecision = decision
		after.ReviewFindings = 3
		kinds, notifications := crossed(t, before, after)
		if len(kinds) != 1 || kinds[0] != want {
			t.Fatalf("%s crossed %v", decision, kinds)
		}
		verdict := notifications[0]
		if verdict.Speaker.Role != domain.RoleReviewer {
			t.Fatalf("%s spoken by %q", decision, verdict.Speaker.Key())
		}
	}
	after := before
	after.ReviewDecision = runstate.ReviewRepair
	after.ReviewFindings = 3
	_, notifications := crossed(t, before, after)
	message, err := Render(notifications[0].Topic, notifications[0].Speaker, notifications[0].Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "3 findings") {
		t.Fatalf("body %q does not count the findings", message.Body)
	}
}

// What the reviewer asked for is read from the durable record, so the message an
// operator gets names each change rather than only counting them. The words are
// the reviewer's own: the record was redacted before it was written, and nothing
// here paraphrases what it holds.
func TestARepairRequestCarriesWhatTheRecordSaysWasAskedFor(t *testing.T) {
	before := running()
	before.Phase = runstate.PhaseReviewing
	after := before
	after.ReviewDecision = runstate.ReviewRepair
	after.ReviewFindings = 2
	after.ReviewFindingDetails = []runstate.Finding{
		{
			Severity: "blocker",
			Message:  "handle the nil worktree. Every caller can be handed one.",
			File:     "runner.go",
			Line:     42,
		},
		{Severity: "major", Message: "integration is now automatic; README.md still says otherwise", File: "README.md"},
	}
	_, notifications := crossed(t, before, after)
	repairs := only(t, notifications, KindReviewRepairs)
	want := []string{
		"blocker: handle the nil worktree (runner.go:42).",
		"major: integration is now automatic; README.md still says otherwise (README.md)",
	}
	if len(repairs.Event.Detail.Requested) != len(want) {
		t.Fatalf("the request carries %q, want one entry per finding", repairs.Event.Detail.Requested)
	}
	for at, requested := range want {
		if repairs.Event.Detail.Requested[at] != requested {
			t.Fatalf("requested change %d is %q, want %q", at, repairs.Event.Detail.Requested[at], requested)
		}
	}
	message, err := Render(repairs.Topic, repairs.Speaker, repairs.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, requested := range want {
		if !strings.Contains(message.Body, "- "+requested) {
			t.Fatalf("body %q does not name %q on its own line", message.Body, requested)
		}
	}
	// A record that counted findings without keeping them is every run reviewed
	// before the repair loop kept them, and it says so rather than listing
	// nothing.
	countedOnly := after
	countedOnly.ReviewFindingDetails = nil
	_, older := crossed(t, before, countedOnly)
	if requested := only(t, older, KindReviewRepairs).Event.Detail.Requested; len(requested) != 0 {
		t.Fatalf("a record keeping no findings still carried %q", requested)
	}
}

func TestOnlyTheFirstSentenceOfAFindingIsSaid(t *testing.T) {
	for written, want := range map[string]string{
		"handle the nil worktree. Every caller can be handed one.":  "handle the nil worktree.",
		"README.md still says the harness does not integrate":       "README.md still says the harness does not integrate",
		"the decoder accepts trailing JSON\nand the test misses it": "the decoder accepts trailing JSON",
		"  padded either side  ":                                    "padded either side",
	} {
		if got := firstSentence(written); got != want {
			t.Fatalf("firstSentence(%q) = %q, want %q", written, got, want)
		}
	}
}

// awaitingVerdict is the record as it stands with the checks behind it and the
// review about to run: the evidence is cleared and no verdict is recorded.
func awaitingVerdict() runstate.State {
	state := running()
	state.Phase = runstate.PhaseReviewing
	return state
}

// reviewed is the record as it stands once a review has answered: the phase it
// answered in, the session that answered, and the verdict itself.
func reviewed(session, decision string, findings, attempts int) runstate.State {
	state := running()
	state.Phase = runstate.PhaseReviewing
	state.ReviewSessionID = session
	state.ReviewDecision = decision
	state.ReviewFindings = findings
	state.RepairAttempts = attempts
	return state
}

// repairing is the record as it stands while the developer works on what the
// reviewer asked for: the verdict is still standing, the count has moved, and no
// new review has happened.
func repairing(from runstate.State) runstate.State {
	state := from
	state.Phase = runstate.PhaseDeveloping
	state.RepairAttempts = from.RepairAttempts + 1
	return state
}

// verdicts reports only the verdicts one reading to the next produced. A repair
// round re-runs the deterministic checks and crosses them again on its way back
// to a review, which is a real crossing and not what these tests are about.
func verdicts(t *testing.T, before, after runstate.State) []Kind {
	t.Helper()
	kinds, _ := crossed(t, before, after)
	said := make([]Kind, 0, len(kinds))
	for _, kind := range kinds {
		if kind == KindReviewApproved || kind == KindReviewRepairs {
			said = append(said, kind)
		}
	}
	return said
}

func TestEveryRoundOfARepairLoopIsSaid(t *testing.T) {
	// A repair loop produces several verdicts and most of them say the same word.
	// Keyed on the word alone, a thread would read as one request for repairs
	// followed by an approval, with every round between them missing.
	first := reviewed("review-session-1", runstate.ReviewRepair, 3, 0)
	if said := verdicts(t, awaitingVerdict(), first); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("the first verdict crossed %v", said)
	}
	// The verdict stands over the whole repair round and is not said again.
	working := repairing(first)
	if said := verdicts(t, first, working); len(said) != 0 {
		t.Fatalf("a verdict standing over a repair round was said again as %v", said)
	}
	// The next review clears the evidence before it runs, and a sink reading in
	// that window sees no verdict rather than the previous one.
	clearing := working
	clearing.Phase = runstate.PhaseReviewing
	clearing.ReviewSessionID = ""
	clearing.ReviewDecision = ""
	clearing.ReviewFindings = 0
	if said := verdicts(t, working, clearing); len(said) != 0 {
		t.Fatalf("clearing the evidence before a review crossed %v", said)
	}
	second := reviewed("review-session-2", runstate.ReviewRepair, 3, 1)
	if said := verdicts(t, clearing, second); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("the second verdict crossed %v", said)
	}
	// And a sink that missed the window between them still says both, because
	// what identifies a verdict is the review that gave it rather than its word.
	// This is the case the decision alone gets wrong: the word did not change and
	// neither did the repair count, and it is still a second verdict.
	if said := verdicts(t, working, second); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("a second verdict read across the clearing window crossed %v", said)
	}
	// An identical verdict from an identical review is the same fact, said once.
	if said := verdicts(t, second, second); len(said) != 0 {
		t.Fatalf("re-reading one verdict said %v again", said)
	}
	approved := reviewed("review-session-3", runstate.ReviewApprove, 0, 2)
	if said := verdicts(t, second, approved); len(said) != 1 || said[0] != KindReviewApproved {
		t.Fatalf("the approval crossed %v", said)
	}
}

func TestAVerdictIsSaidEvenWhenNoSessionWasRecorded(t *testing.T) {
	// The session is what tells two verdicts apart; the decision is compared
	// beside it so a record missing the session loses the rounds rather than the
	// verdict.
	before := reviewed("", runstate.ReviewRepair, 2, 0)
	if said := verdicts(t, awaitingVerdict(), before); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("a verdict with no recorded session crossed %v", said)
	}
	after := reviewed("", runstate.ReviewApprove, 0, 1)
	if said := verdicts(t, before, after); len(said) != 1 || said[0] != KindReviewApproved {
		t.Fatalf("an approval with no recorded session crossed %v", said)
	}
}

func TestAReplayedChangeGetsItsOwnVerdict(t *testing.T) {
	// An integration retry discards the verdict and obtains a fresh independent
	// one, because the reviewed change is not the change that would now be
	// promoted. That is a new verdict and is said as one.
	approved := reviewed("review-session-1", runstate.ReviewApprove, 0, 0)
	replayed := approved
	replayed.ReviewSessionID = ""
	replayed.ReviewDecision = ""
	replayed.IntegrationRetries = 1
	if said := verdicts(t, approved, replayed); len(said) != 0 {
		t.Fatalf("discarding a verdict crossed %v", said)
	}
	again := reviewed("review-session-2", runstate.ReviewApprove, 0, 0)
	again.IntegrationRetries = 1
	if kinds, _ := crossed(t, replayed, again); len(kinds) != 1 || kinds[0] != KindReviewApproved {
		t.Fatalf("the verdict on the replayed change crossed %v", kinds)
	}
}

func TestEachLostRaceIsSaidAsANoteNamingTheBranchAndTheCount(t *testing.T) {
	// A lost race is news in the thread and nothing more: the harness replays the
	// change by itself, so it is a note, and it is never a blocker or an ending.
	before := running()
	before.Phase = runstate.PhaseIntegrating
	before.TargetBranch = "main"
	before.IntegrationRetries = 2
	after := before
	after.IntegrationRetries = 3
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindRaceLost {
		t.Fatalf("a lost race crossed %v", kinds)
	}
	lost := notifications[0]
	if lost.Event.Severity != report.SeverityNote || !lost.Speaker.IsHarness() || KindRaceLost.Reach() != ReachThread {
		t.Fatalf("a lost race is said at %q by %q reaching %q, want a harness note in the thread",
			lost.Event.Severity, lost.Speaker.Key(), KindRaceLost.Reach())
	}
	message, err := Render(lost.Topic, lost.Speaker, lost.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "main") || !strings.Contains(message.Body, "3 lost races") {
		t.Fatalf("body %q does not name the branch and the count", message.Body)
	}
	// The same count read twice is the same race, and is not said again.
	if kinds, _ := crossed(t, after, after); len(kinds) != 0 {
		t.Fatalf("re-reading one race crossed %v", kinds)
	}
}

func TestAPromotionIsSaidByTheHarnessThatMadeIt(t *testing.T) {
	// No agent performs a promotion, so no persona gets to give an account of one.
	before := running()
	before.Phase = runstate.PhaseIntegrating
	after := before
	after.Integration = &runstate.Integration{
		TargetBranch: "main",
		SourceCommit: strings.Repeat("b", 40),
		TargetCommit: strings.Repeat("b", 40),
	}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindPromoted {
		t.Fatalf("a promotion crossed %v", kinds)
	}
	promoted := notifications[0]
	if !promoted.Speaker.IsHarness() {
		t.Fatalf("a promotion is spoken by %q", promoted.Speaker.Key())
	}
	message, err := Render(promoted.Topic, promoted.Speaker, promoted.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "main") || !strings.Contains(message.Body, "bbbbbbbbbbbb") {
		t.Fatalf("body %q does not say where the change went", message.Body)
	}
}

func TestPublicationQueuedMergeAndMergeAreThreeSeparateFacts(t *testing.T) {
	before := running()
	published := before
	published.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/ifd-68-2", Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("c", 40),
	}
	if kinds, _ := crossed(t, before, published); len(kinds) != 1 || kinds[0] != KindPublished {
		t.Fatalf("a publication crossed %v", kinds)
	}
	queued := published
	request := *published.PullRequest
	request.MergeQueued = true
	queued.PullRequest = &request
	if kinds, _ := crossed(t, published, queued); len(kinds) != 1 || kinds[0] != KindMergeQueued {
		t.Fatalf("a queued merge crossed %v", kinds)
	}
	merged := queued
	settled := request
	settled.MergeQueued = false
	settled.Merged = true
	merged.PullRequest = &settled
	kinds, notifications := crossed(t, queued, merged)
	if len(kinds) != 1 || kinds[0] != KindMergeCompleted {
		t.Fatalf("a merge crossed %v", kinds)
	}
	message, err := Render(notifications[0].Topic, notifications[0].Speaker, notifications[0].Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "#84") {
		t.Fatalf("body %q does not name the request", message.Body)
	}
}

// A merge withdrawn because its checks fail on the target itself is said once,
// as a warning from the harness, naming the check and the item filed for it: the
// change reads as done and the publication is not landing.
func TestAMergeWaitingOnTheTargetsRedCheckIsSaidOnceNamingTheFiledItem(t *testing.T) {
	queued := running()
	queued.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/ifd-434-10", Number: 863,
		URL: "https://example.test/pull/863", HeadCommit: strings.Repeat("c", 40), MergeQueued: true,
	}
	waiting := queued
	request := *queued.PullRequest
	request.MergeQueued = false
	request.TargetRed = &runstate.TargetRed{At: moment, TargetBranch: "main", HeadCommit: strings.Repeat("c", 40),
		Checks: []runstate.TargetRedCheck{{Name: "adoption", WorkItem: "yoyodyne-red-1"}}}
	waiting.PullRequest = &request

	kinds, notifications := crossed(t, queued, waiting)
	if len(kinds) != 1 || kinds[0] != KindMergeWaitingOnTarget {
		t.Fatalf("a merge waiting on the target crossed %v", kinds)
	}
	said := notifications[0]
	if said.Event.Severity != report.SeverityWarning || !said.Speaker.IsHarness() {
		t.Fatalf("said at %q by %q, want a warning from the harness", said.Event.Severity, said.Speaker.Key())
	}
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, fact := range []string{"#863", "main's red check", "adoption (filed as yoyodyne-red-1)"} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	if kinds, _ := crossed(t, waiting, waiting); len(kinds) != 0 {
		t.Fatalf("the same wait crossed again as %v", kinds)
	}
}

// The fourth fact about getting a change out, and the one that used to be said
// nowhere: the merge is not going to happen. It is a warning because nobody
// chose it and nothing else in the record says it — the change is promoted, the
// thread reads as landed, and what follows is a publication sitting on the forge
// until somebody happens to look.
func TestAMergeTheForgeWillNotMakeIsSaidAsAWarningWhenItIsDropped(t *testing.T) {
	queued := running()
	queued.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/ifd-68-2", Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("c", 40), MergeQueued: true,
	}
	dropped := queued
	request := *queued.PullRequest
	request.MergeQueued = false
	dropped.PullRequest = &request
	dropped.PublishFailure = "the forge dropped the queued merge of pull request 84: it is open and has no merge queued for it"
	dropped.MergeDrop = &runstate.MergeDrop{At: moment, Reason: dropped.PublishFailure}

	kinds, notifications := crossed(t, queued, dropped)
	if len(kinds) != 1 || kinds[0] != KindMergeDropped {
		t.Fatalf("a dropped merge crossed %v", kinds)
	}
	said := notifications[0]
	if said.Event.Severity != report.SeverityWarning {
		t.Fatalf("a dropped merge is said at %q, want a warning", said.Event.Severity)
	}
	if !said.Speaker.IsHarness() {
		t.Fatalf("a dropped merge is spoken by %q, want the harness", said.Speaker.Key())
	}
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, fact := range []string{"#84", "dropped the queued merge"} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	// Said once, like every other crossing: a thread is a narrative, and the
	// resurfacing is the heartbeat's count rather than this message again.
	if kinds, _ := crossed(t, dropped, dropped); len(kinds) != 0 {
		t.Fatalf("the same drop crossed again as %v", kinds)
	}
}

// The drop is read from the moment the record holds rather than from the queued
// flag going out, and this is why: a sink that first reads a run after the drop
// has to say it, and comparing flags it never saw set would say nothing at all.
// The four merges that waited hours were exactly this reading — nobody was
// watching when they were queued.
func TestADropIsSaidToASinkThatNeverSawTheMergeQueued(t *testing.T) {
	dropped := running()
	dropped.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/ifd-68-2", Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("c", 40),
	}
	dropped.PublishFailure = "check the remote target branch before merging: origin main moved"
	dropped.MergeDrop = &runstate.MergeDrop{At: moment, Reason: dropped.PublishFailure}

	kinds, _ := crossed(t, runstate.State{}, dropped)
	if !slices.Contains(kinds, KindMergeDropped) {
		t.Fatalf("a run first read after its merge was dropped crossed %v", kinds)
	}
}

func TestAParkedRunSaysWhatItIsWaitingOn(t *testing.T) {
	reset := moment.Add(2 * time.Hour)
	held := moment.Add(-time.Hour)
	// An exhausted limit is the one park an operator did not cause and cannot
	// shorten, so it is the one said as a warning. The rest are notes: a hold and
	// a directive are waiting on the person reading the channel, and an overload
	// lifts in seconds.
	for name, park := range map[string]struct {
		apply    func(*runstate.State)
		severity report.Severity
	}{
		"an exhausted provider usage limit": {
			apply: func(s *runstate.State) {
				s.UsageLimitResetsAt = &reset
				s.PauseCause = runstate.PauseUsageLimit
				s.UsageLimitKind = "provider"
			},
			severity: report.SeverityWarning,
		},
		"a transient provider server overload": {
			apply: func(s *runstate.State) {
				s.UsageLimitResetsAt = &reset
				s.PauseCause = runstate.PauseServerOverload
			},
			severity: report.SeverityNote,
		},
		"the operator's pause on all harness activity": {
			apply: func(s *runstate.State) {
				s.OperatorHeldSince = &held
				s.PauseCause = runstate.PauseOperatorHold
			},
			severity: report.SeverityNote,
		},
		"an unresolved directive (ambiguous)": {
			apply: func(s *runstate.State) {
				s.DirectivePause = &runstate.DirectivePause{
					DirectiveID: "directive-7f3a",
					Kind:        "ambiguous",
					Unresolved:  "which branch the change belongs on",
				}
			},
			severity: report.SeverityNote,
		},
	} {
		before := running()
		after := before
		park.apply(&after)
		kinds, notifications := crossed(t, before, after)
		if len(kinds) != 1 || kinds[0] != KindRunParked {
			t.Fatalf("%s crossed %v", name, kinds)
		}
		parkedRun := notifications[0]
		message, err := Render(parkedRun.Topic, parkedRun.Speaker, parkedRun.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(message.Body, name) {
			t.Fatalf("a run waiting on %s reads as %q", name, message.Body)
		}
		if parkedRun.Event.Severity != park.severity {
			t.Fatalf("a run waiting on %s is said at %q, want %q", name, parkedRun.Event.Severity, park.severity)
		}
		// The weight is said in words before it is said in decoration, so the
		// distinction survives a reader whose client renders neither.
		if park.severity == report.SeverityWarning && !strings.Contains(message.Body, "Warning") {
			t.Fatalf("a run waiting on %s reads as %q, which does not say its weight in words", name, message.Body)
		}
		// A run held on a directive leads back to the directive itself, because a
		// pause nobody can name is a pause nobody can lift.
		if after.DirectivePause != nil && parkedRun.Event.Refs.DirectiveID != "directive-7f3a" {
			t.Fatalf("a directive pause refers to %q", parkedRun.Event.Refs.DirectiveID)
		}
		// And it says so when it carries on.
		if kinds, _ := crossed(t, after, before); len(kinds) != 1 || kinds[0] != KindRunContinued {
			t.Fatalf("resuming from %s crossed %v", name, kinds)
		}
	}
}

func TestARefusalOutsideARunIsSaidAtWarningAndNamesWhatIsWaiting(t *testing.T) {
	reset := moment.Add(3 * time.Hour)
	refused := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      "yoyodyne",
		At:             moment,
		Waiting:        "the product manager conversation chat-91253e0e",
		Kind:           "five-hour",
		ResetsAt:       &reset,
		ConversationID: "chat-91253e0e",
	}
	notification, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal: %v", err)
	}
	if notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("a refusal is said at %q, want %q", notification.Event.Severity, report.SeverityWarning)
	}
	// A refusal that stopped no work item belongs to the whole line rather than
	// to somebody's thread, and the harness says it because no persona did it.
	if notification.Topic != Product() {
		t.Fatalf("a refusal with no work item is addressed to %v", notification.Topic)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("a refusal is spoken by %v", notification.Speaker)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Warning",
		refused.Waiting,
		"an exhausted five-hour usage limit",
		reset.UTC().Format(time.RFC3339),
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("a refusal reads as %q, which does not say %q", message.Body, want)
		}
	}
	// A provider that named no reset time says so rather than reading as one that
	// lifts at some moment nobody recorded.
	refused.ResetsAt = nil
	unknown, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal with no reset: %v", err)
	}
	silent, err := Render(unknown.Topic, unknown.Speaker, unknown.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(silent.Body, "until") {
		t.Fatalf("a refusal that named no reset reads as %q", silent.Body)
	}
	// A refusal that did stop one item's work is addressed to that item's thread,
	// which is where somebody following the item is already reading.
	refused.WorkItemID = "yoyodyne-ifd.68.10"
	addressed, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal on an item: %v", err)
	}
	if addressed.Topic.Kind != TopicWorkItem || addressed.Topic.ID != refused.WorkItemID {
		t.Fatalf("a refusal on an item is addressed to %v", addressed.Topic)
	}
	// Every persona has a line for it, so a producer that is not the harness
	// still says it in a voice somebody wrote.
	for _, speaker := range []Speaker{
		Harness(),
		Persona(domain.RoleDeveloper, ""),
		Persona(domain.RoleReviewer, ""),
		Persona(domain.RoleDevelopmentManager, ""),
		Persona(domain.RoleProductManager, ""),
		Persona(domain.RoleArchitect, ""),
	} {
		if _, err := Render(addressed.Topic, speaker, addressed.Event); err != nil {
			t.Fatalf("the %s cannot say a refusal: %v", speaker.Key(), err)
		}
	}
}

// A refusal a permitted alternate served through is the same record read the
// other way round: nothing stopped, so it is a note rather than a warning, and
// it names both the model that has no capacity and the one the work is being
// produced by while it has none. Silence would be the wrong answer to it — an
// agent answering on a model the operator did not configure it for is a change
// to the evidence, whatever it saved.
func TestASubstitutedTurnIsSaidAtNoteAndNamesBothModels(t *testing.T) {
	reset := moment.Add(3 * time.Hour)
	substituted := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      "yoyodyne",
		At:             moment,
		Waiting:        "the development manager conversation chat-91253e0e",
		Kind:           "five-hour",
		ResetsAt:       &reset,
		ConversationID: "chat-91253e0e",
		Model:          "fable",
		ServedBy:       "opus",
	}
	notification, err := FromUsageLimit(substituted)
	if err != nil {
		t.Fatalf("select from a substitution: %v", err)
	}
	if notification.Event.Kind != KindModelSubstituted {
		t.Fatalf("a substitution is said as %q, want %q", notification.Event.Kind, KindModelSubstituted)
	}
	if notification.Event.Severity != report.SeverityNote {
		t.Fatalf("a substitution is said at %q, want %q", notification.Event.Severity, report.SeverityNote)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("a substitution is spoken by %v", notification.Speaker)
	}
	// It reaches the channel rather than only a thread, because an agent
	// answering on a different model is something the operator has to be able to
	// see without going looking.
	if reach := notification.Reach(); reach != ReachChannel {
		t.Fatalf("a substitution reaches %q, want %q", reach, ReachChannel)
	}
	// Every persona has a line for it, and every line says which model has no
	// capacity and which one is serving.
	for _, speaker := range []Speaker{
		Harness(),
		Persona(domain.RoleDeveloper, ""),
		Persona(domain.RoleReviewer, ""),
		Persona(domain.RoleDevelopmentManager, ""),
		Persona(domain.RoleProductManager, ""),
		Persona(domain.RoleArchitect, ""),
	} {
		message, err := Render(notification.Topic, speaker, notification.Event)
		if err != nil {
			t.Fatalf("the %s cannot say a substitution: %v", speaker.Key(), err)
		}
		for _, want := range []string{"fable", "opus"} {
			if !strings.Contains(message.Body, want) {
				t.Fatalf("the %s says %q, which does not name %q", speaker.Key(), message.Body, want)
			}
		}
	}
}

func TestARunThatStoppedOnItsWorkIsSaidAsAWarning(t *testing.T) {
	before := running()
	after := endedRun(before, runstate.StatusFailed)
	after.Failure = "the repair budget was spent with the checks still failing"
	after.Blocker = runstate.RecordBlocker("the checks kept failing after every repair round it was granted")
	kinds, notifications := crossed(t, before, after)
	blockerRecorded := only(t, notifications, KindBlockerRecorded)
	if blockerRecorded.Event.Severity != report.SeverityWarning {
		t.Fatalf("a blocker is a %s among %v", blockerRecorded.Event.Severity, kinds)
	}
	message, err := Render(blockerRecorded.Topic, blockerRecorded.Speaker, blockerRecorded.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, after.Failure) {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
	if !strings.Contains(message.Body, "Warning") {
		t.Fatalf("body %q does not say its severity in words", message.Body)
	}
	// The whole reason the vocabulary exists: a stoppage keeps the change, and
	// the thread says so rather than leaving it to be guessed from the word.
	if !strings.Contains(message.Body, "work preserved") {
		t.Fatalf("body %q does not say what remains of the change", message.Body)
	}
	// A run that succeeded is not a blocker however it is read.
	succeeded := after
	succeeded.Status = runstate.StatusSucceeded
	succeeded.Integration = &runstate.Integration{TargetBranch: "main"}
	succeeded.Failure = ""
	succeeded.Blocker = ""
	if kinds, _ := crossed(t, before, succeeded); slices.Contains(kinds, KindBlockerRecorded) || slices.Contains(kinds, KindRunEnded) {
		t.Fatalf("a landed run was said as an ending without landing: %v", kinds)
	}
}

func TestASuccessfulRunThatLandsNothingNamesItsCauseInTheThread(t *testing.T) {
	t.Parallel()
	for _, class := range []runstate.StopClass{runstate.StopEscalated, runstate.StopIntegrationPolicy} {
		before := running()
		after := before
		after.Status = runstate.StatusSucceeded
		after.StopClass = class
		if class == runstate.StopEscalated {
			after.LandingOutcome = runstate.LandingEscalate
			after.LandingReason = "the requirements contradict the product's goals"
		}
		_, notifications := crossed(t, before, after)
		ended := only(t, notifications, KindRunEnded)
		message, err := Render(ended.Topic, ended.Speaker, ended.Event)
		if err != nil || !strings.Contains(message.Body, class.Name()) {
			t.Fatalf("successful non-landing cause missing: %v, %v", message, err)
		}
		if class == runstate.StopEscalated && !strings.Contains(message.Body, "docket entry") {
			t.Fatalf("the thread did not say where the escalation is decided: %s", message.Body)
		}
	}
}

// The misreading this exists to end: "failed" is accurate about the attempt and
// says nothing about the work, so a channel that said one word over all four
// endings told an operator the attempt was over and nothing about whether their
// change still existed. Each ending is said in the read model's own word, and
// each says what remains.
func TestEachWayARunEndsIsSaidAsItselfWithWhatRemains(t *testing.T) {
	before := running()
	for _, ending := range []struct {
		status   runstate.Status
		blocker  string
		kind     Kind
		word     string
		severity report.Severity
	}{
		{runstate.StatusFailed, "the checks kept failing", KindBlockerRecorded, "blocked", report.SeverityWarning},
		{runstate.StatusFailed, "", KindRunEnded, string(runstate.OutcomeFailed), report.SeverityWarning},
		{runstate.StatusCancelled, "", KindRunEnded, string(runstate.OutcomeCancelled), report.SeverityNote},
		{runstate.StatusTimedOut, "", KindRunEnded, string(runstate.OutcomeTimedOut), report.SeverityWarning},
	} {
		after := endedRun(before, ending.status)
		after.Failure = "what the record gave as the reason"
		if ending.blocker != "" {
			after.Blocker = runstate.RecordBlocker(ending.blocker)
		}
		_, notifications := crossed(t, before, after)
		said := only(t, notifications, ending.kind)
		if said.Event.Severity != ending.severity {
			t.Fatalf("%s is a %s, want %s", ending.status, said.Event.Severity, ending.severity)
		}
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render %s: %v", ending.status, err)
		}
		if !strings.Contains(message.Body, ending.word) {
			t.Fatalf("a %s run is said as %q, which does not name it", ending.status, message.Body)
		}
		if !strings.Contains(message.Body, "work preserved") {
			t.Fatalf("a %s run is said as %q, which does not say what remains", ending.status, message.Body)
		}
	}
}

// A run whose check stage its bound stopped is said as load having stopped it,
// not the change, and with the harness as the one that continues it — in the
// run record's own sentence, which the docket entry carries too. Once the
// harness's continuations are spent, the same line names the development
// manager instead.
func TestAStageTheBoundStoppedIsSaidAsLoadWithTheHarnessContinuingIt(t *testing.T) {
	before := running()
	after := endedRun(before, runstate.StatusTimedOut)
	after.Phase = runstate.PhaseChecking
	after.Failure = "the check stage reached its 30m0s execution.check_stage_timeout bound during make race"
	after.ProviderSessionID, after.BaseCommit, after.TargetBranch = "developer-session", "7a327265ee14", "main"
	after.CheckStage = &runstate.CheckStage{StartedAt: time.Now(), BoundSeconds: 1800, Command: "make race", StoppedAtBound: true}
	say := func(state runstate.State) string {
		t.Helper()
		_, notifications := crossed(t, before, state)
		said := only(t, notifications, KindRunEnded)
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return message.Body
	}
	if !after.HarnessContinuesCheckStage() {
		t.Fatalf("fixture = %#v, want a run the harness continues at its checks", after)
	}
	body := say(after)
	for _, want := range []string{"the harness's", "stopped by load", "not by the change", "the harness continues it itself"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the ending is said as %q, which does not say %q", body, want)
		}
	}
	for range runstate.MaxCheckStageContinuations {
		after.CheckStageContinuations = append(after.CheckStageContinuations, runstate.CheckStageContinuation{ContinuedAt: time.Now(), Reason: "continued"})
	}
	if body := say(after); !strings.Contains(body, "the development manager's") || strings.Contains(body, "the harness continues it itself") {
		t.Fatalf("a run past its continuations is said as %q, want it handed to the development manager", body)
	}
}

// A stoppage recorded onto a run that had already ended. Reconciliation settles
// a run some killed process left terminal: it takes a blocker from the tracker
// and saves it onto a record whose status has not moved since the crash, so the
// ending and the stoppage are two separate readings in that order. Watching only
// for a run becoming terminal, the sink said "failed" at the crash and then
// nothing at all when the stoppage arrived — the one ending an operator has to
// act on, swallowed because the run was already over.
func TestAStoppageRecordedAfterTheRunAlreadyEndedIsStillSaid(t *testing.T) {
	// What the crash left: terminal, a reason, and no blocker. This is the
	// reading the sink has already reported as a plain failure.
	crashed := endedRun(running(), runstate.StatusFailed)
	crashed.Failure = "the process was killed mid-change"
	if kinds, notifications := crossed(t, running(), crashed); len(kinds) != 1 {
		t.Fatalf("the crash crossed %v, want the ending said once", kinds)
	} else if only(t, notifications, KindRunEnded).Event.Severity != report.SeverityWarning {
		t.Fatal("the crash was not said as a warning, so what follows is not a correction of one")
	}

	// What reconciliation then writes: the same terminal status, now carrying the
	// blocker it put on the work item.
	settled := crashed
	settled.Blocker = runstate.RecordBlocker("the run was interrupted with nothing integrated and its worktree preserved")
	kinds, notifications := crossed(t, crashed, settled)
	stoppage := only(t, notifications, KindBlockerRecorded)
	if stoppage.Event.Severity != report.SeverityWarning || stoppage.Reach() != ReachChannel {
		t.Fatalf("the stoppage is a %s reaching the %s among %v, want a warning at the channel for the development manager's decision", stoppage.Event.Severity, stoppage.Reach(), kinds)
	}
	message, err := Render(stoppage.Topic, stoppage.Speaker, stoppage.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "work preserved") {
		t.Fatalf("the stoppage does not say what remains of the change: %q", message.Body)
	}

	// And it is still a crossing rather than a state: a sweep that reads the
	// settled record again says nothing.
	if kinds, _ := crossed(t, settled, settled); len(kinds) != 0 {
		t.Fatalf("re-reading the settled run crossed %v, want the stoppage said once", kinds)
	}
	// A run that ended without a blocker and never gains one is not re-announced
	// either, or every reading of the record would repeat the ending.
	if kinds, _ := crossed(t, crashed, crashed); len(kinds) != 0 {
		t.Fatalf("re-reading the crashed run crossed %v, want the ending said once", kinds)
	}
}

// Both lines finish on the reason the record gives, and a run can honestly have
// none: a cancellation is the operator stopping it without owing anybody a
// sentence. The line has to stay a sentence rather than trailing off a colon,
// and it must not report an ordinary act as a record that lost something.
func TestAnEndingWhoseRecordNamesNoReasonIsStillASentence(t *testing.T) {
	before := running()
	for _, ending := range []struct {
		status runstate.Status
		kind   Kind
	}{
		{runstate.StatusCancelled, KindRunEnded},
		{runstate.StatusTimedOut, KindRunEnded},
	} {
		after := endedRun(before, ending.status)
		_, notifications := crossed(t, before, after)
		said := only(t, notifications, ending.kind)
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render %s with no reason recorded: %v", ending.status, err)
		}
		if !strings.Contains(message.Body, runstate.NoReasonSays) {
			t.Fatalf("a %s run with no reason is said as %q, which does not state the absence", ending.status, message.Body)
		}
		// The absence is stated as itself rather than as words that went missing,
		// which is what the renderer's generic fallback would have said.
		if strings.Contains(message.Body, "nothing the record could carry") {
			t.Fatalf("a %s run with no reason reports an ordinary act as a record that lost something: %q", ending.status, message.Body)
		}
		if strings.Contains(message.Body, ": .") || strings.Contains(message.Body, ": Next:") {
			t.Fatalf("a %s run with no reason trails off a colon: %q", ending.status, message.Body)
		}
	}
}

// A blocker can reach a record whose failure is empty, which is what a stoppage
// settled onto an already-terminal run left before the sweep began writing its
// own reason there. The record names a reason then — the words the item was
// blocked in — so the critical line says them rather than the absence, as
// `yoyo status` does from the same derivation.
func TestAStoppageWithABlockerAndNoFailureIsSaidByItsBlocker(t *testing.T) {
	before := running()
	after := endedRun(before, runstate.StatusFailed)
	after.Blocker = runstate.RecordBlocker("the interrupted run left a worktree nothing could settle")
	_, notifications := crossed(t, before, after)
	said := only(t, notifications, KindBlockerRecorded)
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render a stoppage with a blocker and no failure: %v", err)
	}
	if !strings.Contains(message.Body, "the interrupted run left a worktree nothing could settle") {
		t.Fatalf("a stoppage with a blocker and no failure is said as %q, which leaves the blocker out", message.Body)
	}
	if strings.Contains(message.Body, runstate.NoReasonSays) {
		t.Fatalf("a stoppage whose record names a blocker is said as naming no reason: %q", message.Body)
	}
}

// A run whose change the harness removed and one whose record names no artifact
// are opposite answers to "is my work gone", and neither may be said as the
// other. The three phrases are the read model's, so `yoyo status` and the thread
// cannot come to say different words about one run.
func TestWhatRemainsIsSaidAsTheRecordHasItRatherThanGuessed(t *testing.T) {
	before := running()
	for remains, apply := range map[string]func(*runstate.State){
		"work preserved": func(*runstate.State) {},
		"work removed": func(state *runstate.State) {
			state.BranchRemoved, state.WorktreeRemoved = true, true
		},
		"no artifacts recorded": func(state *runstate.State) {
			state.Branch, state.WorktreePath = "", ""
		},
	} {
		after := endedRun(before, runstate.StatusFailed)
		after.Failure = "what the record gave as the reason"
		apply(&after)
		_, notifications := crossed(t, before, after)
		ended := only(t, notifications, KindRunEnded)
		message, err := Render(ended.Topic, ended.Speaker, ended.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(message.Body, remains) {
			t.Fatalf("a run whose record wanted %q is said as %q", remains, message.Body)
		}
	}
}

// The stoppage that reaches a record saying "succeeded". A run promotes its work
// and records it, the target turns out not to carry the promotion, and the sweep
// hands the item to a person and leaves the status the run wrote for itself
// alone. Reading the status here silenced the stoppage line on the one record
// where the status is the least true thing about the run: the item is blocked,
// the docket says a person owns it, and the channel said nothing.
func TestAStoppageOnARecordThatSaysSucceededIsStillSaid(t *testing.T) {
	// The run as it was cleaning up, and then the same run recorded as succeeded.
	// Nothing is said of work that landed, which is what makes the line below the
	// only thing the channel says about this record.
	before := running()
	before.Phase = runstate.PhaseCleaningUp
	before.Integration = &runstate.Integration{TargetBranch: "main"}
	landed := endedRun(before, runstate.StatusSucceeded)
	if kinds, _ := crossed(t, before, landed); len(kinds) != 0 {
		t.Fatalf("the successful run crossed %v, want nothing said of work that landed", kinds)
	}

	settled := landed
	settled.Failure = "reconciled after an interrupted run: the run recorded commit abc as integrated into main, but main does not contain it"
	settled.Blocker = runstate.RecordBlocker("the run recorded a promotion main does not carry")
	kinds, notifications := crossed(t, landed, settled)
	stoppage := only(t, notifications, KindBlockerRecorded)
	if stoppage.Event.Severity != report.SeverityWarning || stoppage.Reach() != ReachChannel {
		t.Fatalf("the stoppage is a %s reaching the %s among %v, want a warning at the channel for the development manager's decision", stoppage.Event.Severity, stoppage.Reach(), kinds)
	}
	message, err := Render(stoppage.Topic, stoppage.Speaker, stoppage.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, settled.Failure) {
		t.Fatalf("the stoppage does not carry the settled reason: %q", message.Body)
	}
	if strings.Contains(message.Body, string(runstate.OutcomeSucceeded)) {
		t.Fatalf("the stoppage still says its work landed: %q", message.Body)
	}
	// The item's own status line is read from the same record, and a completion
	// mark on an item a person was handed is the same false fact in one glyph.
	if status := StatusOfRun(settled); status != StatusBlocked {
		t.Fatalf("StatusOfRun() = %q, want the item read as blocked", status)
	}
	// And it is a crossing rather than a state: the settled record read again says
	// nothing.
	if kinds, _ := crossed(t, settled, settled); len(kinds) != 0 {
		t.Fatalf("re-reading the settled run crossed %v, want the stoppage said once", kinds)
	}
}

// endedRun is one reading of a run that has reached a terminal status, with the
// artifacts a run that got as far as making a change carries.
func endedRun(before runstate.State, status runstate.Status) runstate.State {
	completed := moment.Add(time.Minute)
	after := before
	after.Status = status
	after.CompletedAt = &completed
	after.Branch = "yoyodyne/yoyodyne-ifd-68-2/4d1f"
	after.WorktreePath = "/tmp/worktrees/run-4d1f"
	return after
}

func TestARunWithNoUsableWorkItemIsRefusedRatherThanMisaddressed(t *testing.T) {
	state := running()
	state.WorkItemID = ""
	if _, err := FromRun(runstate.State{}, state, nil); err == nil {
		t.Fatal("selected from a run naming no work item")
	}
	// A reading with no run at all is nothing to say rather than a failure: it is
	// what the first moments of every run look like.
	notifications, err := FromRun(runstate.State{}, runstate.State{}, nil)
	if err != nil || len(notifications) != 0 {
		t.Fatalf("an empty reading gave %v, %v", notifications, err)
	}
}

func TestAReportIsSaidByTheRoleThatFiledItAtItsOwnSeverity(t *testing.T) {
	for _, role := range domain.Roles() {
		filed := report.Report{
			SchemaVersion: report.SchemaVersion,
			ID:            "report-" + strings.Repeat("a", 32),
			Role:          role,
			Agent:         "opus",
			RunID:         "run-4d1f",
			WorkItemID:    "yoyodyne-ifd.68.2",
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			Severity:      report.SeverityWarning,
			Message:       "the staleness check reads the tracker twice",
			RecordedAt:    moment,
		}
		notification, err := FromReport(filed)
		if err != nil {
			t.Fatalf("select a %s report: %v", role, err)
		}
		if notification.Speaker.Role != role || notification.Speaker.Agent != "opus" {
			t.Fatalf("a %s report is spoken by %+v", role, notification.Speaker)
		}
		if notification.Event.Severity != report.SeverityWarning {
			t.Fatalf("a %s report is a %s", role, notification.Event.Severity)
		}
		message, err := Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("render a %s report: %v", role, err)
		}
		if !strings.Contains(message.Body, filed.Message) {
			t.Fatalf("a %s report reads as %q", role, message.Body)
		}
		if message.Identity.Name == "" || !strings.Contains(message.Identity.Name, "opus") {
			t.Fatalf("a %s report appears as %q", role, message.Identity.Name)
		}
	}
}

func TestSomethingConcerningNoItemIsAddressedToTheProduct(t *testing.T) {
	filed := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-" + strings.Repeat("a", 32),
		Role:          domain.RoleProductManager,
		RunID:         "chat-91253e0e",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityNote,
		Message:       "the brief has not been revised since the goals were rewritten",
		RecordedAt:    moment,
	}
	notification, err := FromReport(filed)
	if err != nil {
		t.Fatalf("select a report from a conversation: %v", err)
	}
	if notification.Topic.Kind != TopicProduct {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
}

// A report nobody's agent wrote — the pull's refusal of an item whose own
// sentence holds it back — is said in the harness's voice, which is the one
// speaker that is not a persona. A role nobody configured would be refused.
func TestAReportTheHarnessFiledIsSaidInTheHarnessVoice(t *testing.T) {
	filed := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-" + strings.Repeat("b", 32),
		Role:          report.HarnessReporter,
		RunID:         "unready_item:yoyodyne-ifd.298:subject-not-in-repository",
		WorkItemID:    "yoyodyne-ifd.298",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityWarning,
		Message:       "yoyodyne-ifd.298 is passed over at every pull because of what it states.",
		RecordedAt:    moment,
	}
	notification, err := FromReport(filed)
	if err != nil {
		t.Fatalf("select a report the harness filed: %v", err)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("speaker = %+v, want the harness", notification.Speaker)
	}
	if err := notification.Speaker.Validate(); err != nil {
		t.Fatalf("the harness's voice was refused: %v", err)
	}
}

func TestAProposalCarriesBothHalvesOfWhatWasWritten(t *testing.T) {
	proposal := amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            "amendment-" + strings.Repeat("a", 32),
		Role:          domain.RoleDeveloper,
		RunID:         "run-4d1f",
		WorkItemID:    "yoyodyne-ifd.68.2",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "slack-reporting-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which role speaks each reportable kind",
		Why:           "the notifier has to pick a speaker and the design does not name one",
		RaisedAt:      moment,
	}
	notification, err := FromProposal(proposal)
	if err != nil {
		t.Fatalf("select a proposal: %v", err)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("render a proposal: %v", err)
	}
	for _, half := range []string{proposal.Artifact, proposal.Change, proposal.Why} {
		if !strings.Contains(message.Body, half) {
			t.Fatalf("body %q does not carry %q", message.Body, half)
		}
	}
}

func TestTheOperatorsSwitchesAreAddressedToTheWholeLine(t *testing.T) {
	for _, notification := range []Notification{
		FromIntakeHold(runstate.IntakeHold{
			SchemaVersion: runstate.IntakeHoldSchemaVersion,
			ProductID:     "yoyodyne",
			HeldAt:        moment,
			Reason:        "reordering the backlog first",
		}),
		IntakeReleased(moment, runstate.IntakeRelease{}, false),
		FromOperatorHold(runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: moment}),
		HoldLifted(moment),
	} {
		if notification.Topic.Kind != TopicProduct {
			t.Fatalf("%s addressed to %q", notification.Event.Kind, notification.Topic.Key())
		}
		if !notification.Speaker.IsHarness() {
			t.Fatalf("%s spoken by %q", notification.Event.Kind, notification.Speaker.Key())
		}
		if _, err := Render(notification.Topic, notification.Speaker, notification.Event); err != nil {
			t.Fatalf("render %s: %v", notification.Event.Kind, err)
		}
	}
	held := FromIntakeHold(runstate.IntakeHold{HeldAt: moment, Reason: "reordering the backlog first"})
	message, err := Render(held.Topic, held.Speaker, held.Event)
	if err != nil {
		t.Fatalf("render a held intake: %v", err)
	}
	if !strings.Contains(message.Body, "reordering the backlog first") {
		t.Fatalf("body %q does not carry the operator's reason", message.Body)
	}
	if strings.Contains(message.Body, "the runs it counted") {
		t.Fatalf("body %q names runs on a hold that counted none", message.Body)
	}
}

// The brake's hold names the runs that tripped it — each with its item and what
// stopped it — because that is what the operator asked to be told rather than a
// count. The release names who lifted it, and says so where nobody is named
// rather than naming the operator by default.
func TestABrakeTripNamesTheRunsItCountedAndTheReleaseNamesWhoLiftedIt(t *testing.T) {
	held := FromIntakeHold(runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        moment,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		Brake: &runstate.IntakeBrake{
			Blocked: []runstate.BrakeBlockedRun{
				{RunID: "run-1", WorkItemID: "yoyodyne-ifd.398", Reason: "its reviewer still required repair after 2 repair attempt(s)"},
				{RunID: "run-2", WorkItemID: "yoyodyne-ifd.401", Reason: "check `make test` failed (exit 1) after 2 repair attempt(s)"},
			},
			CooldownEndsAt: moment.Add(30 * time.Minute),
		},
	})
	message, err := Render(held.Topic, held.Speaker, held.Event)
	if err != nil {
		t.Fatalf("render a brake trip: %v", err)
	}
	for _, want := range []string{
		"the harness's own brake placed it after 3 run(s) blocked in a row",
		"the runs it counted: run run-1 of yoyodyne-ifd.398: its reviewer still required repair after 2 repair attempt(s)",
		"run run-2 of yoyodyne-ifd.401: check `make test` failed (exit 1)",
		"`yoyo release`, or `/release` in the conversation, lifts it sooner",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("body %q does not carry %q", message.Body, want)
		}
	}

	released := IntakeReleased(moment, runstate.IntakeRelease{
		SchemaVersion: runstate.IntakeReleaseSchemaVersion,
		Hold:          runstate.IntakeHold{SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "yoyodyne", HeldAt: moment, HeldBy: runstate.IntakeHolderBrake},
		ReleasedAt:    moment.Add(time.Hour),
		ReleasedBy:    "the operator, at a terminal (`yoyo release`)",
	}, true)
	if !released.Event.At.Equal(moment.Add(time.Hour)) {
		t.Fatalf("a recorded release is dated %s, want the moment it was recorded", released.Event.At)
	}
	message, err = Render(released.Topic, released.Speaker, released.Event)
	if err != nil {
		t.Fatalf("render a release: %v", err)
	}
	if !strings.Contains(message.Body, "released by the operator, at a terminal (`yoyo release`)") {
		t.Fatalf("body %q does not say who lifted the hold", message.Body)
	}
	unrecorded := IntakeReleased(moment, runstate.IntakeRelease{}, false)
	message, err = Render(unrecorded.Topic, unrecorded.Speaker, unrecorded.Event)
	if err != nil {
		t.Fatalf("render an unrecorded release: %v", err)
	}
	if !strings.Contains(message.Body, "released by somebody the record does not name") {
		t.Fatalf("body %q names somebody on a release nothing recorded", message.Body)
	}
}

// A finding only the operator can act on is said as what is needed, who found
// it, and where it is recorded, in that order, and it is a warning whatever the
// report was filed at. It is addressed to the item where the finding is about
// one, and to the product otherwise.
func TestAFindingForTheOperatorSaysWhatIsNeededAndWhereItIsRecorded(t *testing.T) {
	finding, err := FromOperatorAction(OperatorAction{
		WorkItemID: "yoyodyne-ifd.383",
		Needs:      "add the PreToolUse hook to .claude/settings.json; the harness may not write that file",
		RecordedIn: "the handling of report-00000000000000000000000000000002 recorded in chat-1, over the developer's report from run-1",
		FoundBy:    "the Lead Product Manager, handling the report",
		Ends:       "a later handling of the report records it done",
		Mover:      "the operator's — only a person can act on this; a later handling of the report records it done",
		Since:      moment,
	})
	if err != nil {
		t.Fatalf("select a finding: %v", err)
	}
	if finding.Topic.Kind != TopicWorkItem || finding.Topic.ID != "yoyodyne-ifd.383" {
		t.Fatalf("a finding about an item is addressed to %q", finding.Topic.Key())
	}
	if finding.Event.Severity != report.SeverityWarning || !finding.Speaker.IsHarness() {
		t.Fatalf("finding = %+v, want a warning the harness says", finding.Event)
	}
	message, err := Render(finding.Topic, finding.Speaker, finding.Event)
	if err != nil {
		t.Fatalf("render a finding: %v", err)
	}
	for _, want := range []string{
		"add the PreToolUse hook to .claude/settings.json; the harness may not write that file",
		"the Lead Product Manager, handling the report",
		"report-00000000000000000000000000000002",
		"`yoyo status`",
		"a later handling of the report records it done",
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("body %q does not carry %q", message.Body, want)
		}
	}
	// The clause it closes on is the read model's wording of whose move it is,
	// so the terminal's attention line and this message agree.
	if !strings.HasSuffix(message.Body, nextMoveLead+"the operator's — only a person can act on this; a later handling of the report records it done.") {
		t.Fatalf("body %q does not close on the read model's clause", message.Body)
	}
	if message.Reach != ReachChannel {
		t.Fatalf("a finding for the operator reaches %q, want the channel level", message.Reach)
	}
	product, err := FromOperatorAction(OperatorAction{Needs: "renew the login", RecordedIn: "report-1", FoundBy: "the developer, in a critical report", Since: moment})
	if err != nil {
		t.Fatalf("select a finding about no item: %v", err)
	}
	if product.Topic.Kind != TopicProduct {
		t.Fatalf("a finding about no item is addressed to %q", product.Topic.Key())
	}
	message, err = Render(product.Topic, product.Speaker, product.Event)
	if err != nil {
		t.Fatalf("render a finding about no item: %v", err)
	}
	if message.Reach != ReachChannel {
		t.Fatalf("a finding about no item reaches %q, want the channel level", message.Reach)
	}
}

// A watch session is about the whole line rather than any one item, so it is
// addressed and spoken exactly as the operator's switches are — and the one
// state somebody has to act on is the one said as a warning.
func TestAWatchSessionIsAddressedToTheWholeLine(t *testing.T) {
	states := map[runstate.WatchState]Kind{
		runstate.WatchWatching: KindWatchStarted,
		runstate.WatchIdle:     KindWatchIdle,
		runstate.WatchBraked:   KindWatchBraked,
		runstate.WatchBlocked:  KindWatchBlocked,
		runstate.WatchResumed:  KindWatchResumed,
		runstate.WatchStopped:  KindWatchStopped,
	}
	for state, kind := range states {
		notification, err := FromWatch(watchTransition(state, "the backlog is empty"))
		if err != nil {
			t.Fatalf("address a %s session: %v", state, err)
		}
		if notification.Event.Kind != kind {
			t.Fatalf("%s was said as %q, want %q", state, notification.Event.Kind, kind)
		}
		if notification.Topic.Kind != TopicProduct || !notification.Speaker.IsHarness() {
			t.Fatalf("%s addressed to %q and spoken by %q", state, notification.Topic.Key(), notification.Speaker.Key())
		}
		message, err := Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("render %s: %v", state, err)
		}
		if !strings.Contains(message.Body, "the backlog is empty") {
			t.Fatalf("body %q does not carry what the session said about itself", message.Body)
		}
		wantSeverity := report.SeverityNote
		if state == runstate.WatchBraked || state == runstate.WatchBlocked {
			wantSeverity = report.SeverityWarning
		}
		if notification.Event.Severity != wantSeverity {
			t.Fatalf("%s said at %q, want %q", state, notification.Event.Severity, wantSeverity)
		}
	}
	// A state nothing has words for is refused rather than posted as something
	// nobody wrote a line for.
	if _, err := FromWatch(watchTransition(runstate.WatchState("pondering"), "")); err == nil {
		t.Fatal("FromWatch() error = nil, want a state nothing says refused")
	}
}

// What the session recorded about the state of the line reaches the message
// that closes on whose move it is. The idle line that misled an operator three
// times carried only its own sentence, so every surface downstream had to guess
// at the two facts that decide the answer: whether the harness is nonetheless
// working, and who carries the work it passed over.
func TestAnIdleSessionCarriesWhatItSawGoingAndWhoItWaitsOn(t *testing.T) {
	idle := watchTransition(runstate.WatchIdle,
		"1 run in flight; 3 items passed over, of 3 admitted: carried in conversation (architect: yoyodyne-ifd.212)")
	idle.Running = 1
	idle.Executor = domain.ConversationWith(domain.RoleArchitect)

	notification, err := FromWatch(idle)
	if err != nil {
		t.Fatalf("address an idle session: %v", err)
	}
	if notification.Event.Detail.Running != 1 {
		t.Fatalf("detail = %+v, want the run in flight carried", notification.Event.Detail)
	}
	if notification.Event.Detail.Executor != string(domain.ConversationWith(domain.RoleArchitect)) {
		t.Fatalf("detail = %+v, want the conversation the session waits on carried", notification.Event.Detail)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("render an idle session: %v", err)
	}
	if !strings.HasSuffix(message.Body, nextMoveLead+"the architect's, in conversation — the work this poll passed over is carried there, and no run will ever start it.") {
		t.Fatalf("body %q does not close on the architect's move", message.Body)
	}

	// The poll that read nothing at all travels the same way. Admitting work to a
	// store that will not answer changes nothing, so the mark has to reach the
	// clause rather than staying in the session's own prose.
	outage := watchTransition(runstate.WatchIdle, "the harness could not be read and is being read again")
	outage.Unreadable = true
	failed, err := FromWatch(outage)
	if err != nil {
		t.Fatalf("address a session that could not read the harness: %v", err)
	}
	if !failed.Event.Detail.Unreadable {
		t.Fatalf("detail = %+v, want the reading that failed carried", failed.Event.Detail)
	}
	said, err := Render(failed.Topic, failed.Speaker, failed.Event)
	if err != nil {
		t.Fatalf("render a session that could not read the harness: %v", err)
	}
	if strings.HasSuffix(said.Body, nextMoveLead+nextMoves[KindWatchIdle]) {
		t.Fatalf("body %q sends the reader to an admission over a store that would not answer", said.Body)
	}
}

// A poll that never read the queue is not a poll that found nothing in it. It
// records the same idle state, so the mark the session wrote is what the kind is
// taken from — the precedent the restart below set — and the message it becomes
// is the harness's, saying a read is being retried, rather than the idle line.
func TestAnIdlePollThatCouldNotReadTheStoreIsSaidAsARetryRatherThanAsIdle(t *testing.T) {
	quiet, err := FromWatch(watchTransition(runstate.WatchIdle, "the backlog is empty"))
	if err != nil {
		t.Fatalf("address an idle session: %v", err)
	}
	if quiet.Event.Kind != KindWatchIdle {
		t.Fatalf("a poll that read the queue and found nothing was said as %q, want %q", quiet.Event.Kind, KindWatchIdle)
	}

	outage := watchTransition(runstate.WatchIdle, "the harness could not be read and is being read again for up to 2h0m0s before the session gives up on it: database is locked")
	outage.Unreadable = true
	retrying, err := FromWatch(outage)
	if err != nil {
		t.Fatalf("address a session retrying a read: %v", err)
	}
	if retrying.Event.Kind != KindWatchReadRetrying {
		t.Fatalf("a poll that could not read the store was said as %q, want %q", retrying.Event.Kind, KindWatchReadRetrying)
	}
	if !retrying.Speaker.IsHarness() {
		t.Fatalf("a retried read was spoken by %q, want the harness", retrying.Speaker.Key())
	}
	message, err := Render(retrying.Topic, retrying.Speaker, retrying.Event)
	if err != nil {
		t.Fatalf("render a session retrying a read: %v", err)
	}
	if strings.Contains(message.Body, "started nothing") || strings.Contains(strings.ToLower(message.Body), "product manager") {
		t.Fatalf("body %q says the session found nothing, or hands the move to the product manager", message.Body)
	}
	if !strings.Contains(message.Body, "reading it again") || !strings.HasSuffix(message.Body, nextMoveLead+nextMoves[KindWatchReadRetrying]) {
		t.Fatalf("body %q does not say the read is being retried and close on the harness's move", message.Body)
	}
	if !strings.HasPrefix(nextMoves[KindWatchReadRetrying], "the harness's") {
		t.Fatalf("whose move follows %s is %q, want the harness's", KindWatchReadRetrying, nextMoves[KindWatchReadRetrying])
	}
}

// The stop that is not an ending. A session being re-executed into a build
// deployed over it records the same stopped state as a session somebody closed,
// and the two ask opposite things of a reader — so the mark the session wrote is
// what the kind is taken from, rather than the state alone.
func TestAStopMarkedAsARestartIsSaidAsOneRatherThanAsAnEnding(t *testing.T) {
	stopped := watchTransition(runstate.WatchStopped, "the operator stopped it")
	ending, err := FromWatch(stopped)
	if err != nil {
		t.Fatalf("address a stopped session: %v", err)
	}
	if ending.Event.Kind != KindWatchStopped {
		t.Fatalf("a session that ended was said as %q, want %q", ending.Event.Kind, KindWatchStopped)
	}

	restarting := watchTransition(runstate.WatchStopped, "a build was deployed over the one this session was started from")
	restarting.Restarting = true
	coming, err := FromWatch(restarting)
	if err != nil {
		t.Fatalf("address a session restarting into a deployed build: %v", err)
	}
	if coming.Event.Kind != KindWatchRedeploying {
		t.Fatalf("a session restarting itself was said as %q, want %q", coming.Event.Kind, KindWatchRedeploying)
	}
	message, err := Render(coming.Topic, coming.Speaker, coming.Event)
	if err != nil {
		t.Fatalf("render a restarting session: %v", err)
	}
	if strings.Contains(message.Body, nextMoves[KindWatchStopped]) {
		t.Fatalf("body %q hands the operator the move a session that ended would", message.Body)
	}
}

// A line that is choosing nothing over ready work is the one thing said as a
// state rather than as a crossing, so what it says has to carry the three facts
// somebody woken by it needs: what stopped it, how long that has been true, and
// how much is waiting behind it.
func TestALineWaitingSaysWhatStoppedItHowLongAndWhatIsWaiting(t *testing.T) {
	waiting := FromLine(Line{
		Stopped: "intake is held, so nothing new is being chosen",
		Since:   moment,
		Ready:   3,
	}, report.SeverityNote, moment.Add(10*time.Hour))
	if waiting.Topic.Kind != TopicProduct || !waiting.Speaker.IsHarness() {
		t.Fatalf("a waiting line was addressed to %q and spoken by %q", waiting.Topic.Key(), waiting.Speaker.Key())
	}
	// Every persona says it, because the sink posts in whichever voice the message
	// belongs to and a persona with no line for a kind is a message nobody wrote.
	for _, speaker := range []Speaker{Harness(), Persona(domain.RoleDeveloper, ""), Persona(domain.RoleArchitect, "")} {
		message, err := Render(waiting.Topic, speaker, waiting.Event)
		if err != nil {
			t.Fatalf("render a waiting line as the %s: %v", speaker.Key(), err)
		}
		for _, fact := range []string{"intake is held", "10 hours", "3 items"} {
			if !strings.Contains(message.Body, fact) {
				t.Fatalf("body %q does not carry %q", message.Body, fact)
			}
		}
	}
}

// The age is what changes between one heartbeat and the next, so it is said the
// way somebody would say it out loud rather than as a duration to parse — and
// measured against the moment the message dates itself to, so a message read
// later still says the age it had when it was said.
func TestTheAgeOfAWaitingLineIsSaidInTheLargestHonestUnit(t *testing.T) {
	for _, spoken := range []struct {
		stood time.Duration
		want  string
	}{
		{stood: 0, want: "under a minute"},
		{stood: 30 * time.Second, want: "under a minute"},
		{stood: time.Minute, want: "one minute"},
		{stood: 47 * time.Minute, want: "47 minutes"},
		{stood: time.Hour, want: "one hour"},
		{stood: 10*time.Hour + 3*time.Minute, want: "10 hours"},
		{stood: 50 * time.Hour, want: "2 days"},
	} {
		waiting := FromLine(Line{Stopped: "no watch session is running", Since: moment, Ready: 1}, report.SeverityNote, moment.Add(spoken.stood))
		message, err := Render(waiting.Topic, Harness(), waiting.Event)
		if err != nil {
			t.Fatalf("render a line waiting %s: %v", spoken.stood, err)
		}
		if !strings.Contains(message.Body, spoken.want) {
			t.Fatalf("a line waiting %s said %q, want it to carry %q", spoken.stood, message.Body, spoken.want)
		}
	}
	// A state with no recorded start says so rather than reading as one that began
	// at the zero time, which would be an age nobody could believe.
	unrecorded := FromLine(Line{Stopped: "no watch session is running", Ready: 1}, report.SeverityNote, moment)
	message, err := Render(unrecorded.Topic, Harness(), unrecorded.Event)
	if err != nil {
		t.Fatalf("render a line with no recorded start: %v", err)
	}
	if !strings.Contains(message.Body, "an unrecorded length of time") {
		t.Fatalf("body %q does not state that the start was not recorded", message.Body)
	}
}

// A digest stands for messages nobody will ever see one at a time, so what it
// says has to carry the whole of the claim: how many there were, over what span,
// and that the durable record is the account of them. It is the harness's own
// line, because deciding not to repeat a backlog is not any persona's account of
// the work.
func TestADigestSaysHowMuchAccumulatedAndWhereTheWholeOfItIs(t *testing.T) {
	topic, err := WorkItem("yoyodyne-ifd.68.8")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	digest := FromAccumulation(Accumulation{
		Topic:    topic,
		Events:   37,
		Since:    moment.Add(-9 * time.Hour),
		At:       moment,
		Severity: report.SeverityWarning,
	})
	if digest.Speaker.Key() != HarnessSpeaker {
		t.Fatalf("digest speaker = %q, want the harness rather than a persona", digest.Speaker.Key())
	}
	if digest.Event.Kind != KindCatchUpDigest {
		t.Fatalf("digest kind = %q, want %q", digest.Event.Kind, KindCatchUpDigest)
	}

	message, err := Render(digest.Topic, digest.Speaker, digest.Event)
	if err != nil {
		t.Fatalf("render a digest: %v", err)
	}
	if !strings.HasPrefix(message.Body, warningMark) {
		t.Fatalf("digest = %q, want it marked by the loudest thing it stands for", message.Body)
	}
	for _, want := range []string{"37 events", "9 hours", "record"} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("digest = %q, want it to carry %q", message.Body, want)
		}
	}
	if message.Topic != topic.Key() {
		t.Fatalf("digest addressed to %q, want the thread it stands for", message.Topic)
	}
}

func watchTransition(state runstate.WatchState, reason string) runstate.WatchTransition {
	return runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         state,
		At:            moment,
		Reason:        reason,
	}
}

// recorder is a poster that keeps what it was handed, so a test can check that
// the notifier renders before it posts rather than after.
type recorder struct {
	posted []Message
}

func (r *recorder) Post(_ context.Context, message Message) error {
	r.posted = append(r.posted, message)
	return nil
}

func TestTheNotifierRendersEachEventBeforeItIsPosted(t *testing.T) {
	posted := &recorder{}
	notifier := New(posted, Appearance{})
	_, notifications := crossed(t, runstate.State{}, running())
	for _, notification := range notifications {
		if err := notification.Notify(context.Background(), notifier); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}
	if len(posted.posted) != len(notifications) {
		t.Fatalf("posted %d messages for %d events", len(posted.posted), len(notifications))
	}
	message := posted.posted[0]
	if message.SchemaVersion != SchemaVersion || message.Topic != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("posted %+v", message)
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("posted a message that does not validate: %v", err)
	}
}

func TestAnUnsayableEventNeverReachesThePoster(t *testing.T) {
	posted := &recorder{}
	err := New(posted, Appearance{}).Notify(context.Background(), Product(), Harness(), Event{Kind: "run.exploded", At: moment, Severity: report.SeverityNote})
	if err == nil {
		t.Fatal("notified an event nobody wrote words for")
	}
	if len(posted.posted) != 0 {
		t.Fatalf("posted %d messages anyway", len(posted.posted))
	}
}

func TestReportingWithNowhereToReportIsNotAFailure(t *testing.T) {
	// Reporting is observation, never a gate: a product nobody pointed at a
	// workspace behaves exactly as it did before there was one.
	if err := (Discard{}).Notify(context.Background(), Product(), Harness(), Event{
		Kind: KindHoldPlaced, At: moment, Severity: report.SeverityNote,
	}); err != nil {
		t.Fatalf("discarding a notification failed: %v", err)
	}
}

// A pinned version the provider has not got is the same news said the same way:
// the model behind the work is not the one the configuration names. What
// separates it from a closed window is the cause, which every persona's line
// carries, so nobody reads a catalogue as a capacity outage that is about to
// lift.
func TestAVersionFallbackIsSaidAsASubstitutionAndNamesWhy(t *testing.T) {
	fellBack := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      "yoyodyne",
		At:             moment,
		Waiting:        "the architect conversation chat-91253e0e",
		ConversationID: "chat-91253e0e",
		Model:          "claude-opus-5-20260401",
		ServedBy:       "opus",
		Substitution:   runstate.SubstitutedForAvailability,
	}
	notification, err := FromUsageLimit(fellBack)
	if err != nil {
		t.Fatalf("select from a version fallback: %v", err)
	}
	if notification.Event.Kind != KindModelSubstituted {
		t.Fatalf("a version fallback is said as %q, want %q", notification.Event.Kind, KindModelSubstituted)
	}
	if notification.Event.Severity != report.SeverityNote {
		t.Fatalf("a version fallback is said at %q, want %q", notification.Event.Severity, report.SeverityNote)
	}
	for _, speaker := range []Speaker{
		Harness(),
		Persona(domain.RoleDeveloper, ""),
		Persona(domain.RoleReviewer, ""),
		Persona(domain.RoleDevelopmentManager, ""),
		Persona(domain.RoleProductManager, ""),
		Persona(domain.RoleArchitect, ""),
	} {
		message, err := Render(notification.Topic, speaker, notification.Event)
		if err != nil {
			t.Fatalf("the %s cannot say a version fallback: %v", speaker.Key(), err)
		}
		for _, want := range []string{"claude-opus-5-20260401", "opus", "has not got"} {
			if !strings.Contains(message.Body, want) {
				t.Fatalf("the %s said %q, which does not name %q", speaker.Key(), message.Body, want)
			}
		}
		// And it does not claim an exhausted account, which is a window an
		// operator would otherwise sit and wait out.
		if strings.Contains(message.Body, "no capacity") || strings.Contains(message.Body, "usage limit") {
			t.Fatalf("the %s said %q, which reads a missing model as an exhausted limit", speaker.Key(), message.Body)
		}
	}
}

// The line the operator actually read on 2026-09-07, twenty-nine times: a run
// that died at the claim was said as an ending with nothing recorded for anybody
// to decide. That was true, and it was the defect — the class left no docket
// entry, so it existed in no surface the development manager's sweep reads. Now
// it is docketed as it dies, and the clause has to say so or it sends the reader
// away from the one entry that was made for them.
func TestARunThatDiedBeforeClaimingIsSaidAsSomebodysMove(t *testing.T) {
	before := running()
	before.Status = runstate.StatusPending
	before.Phase = ""

	// The run took nothing and cut nothing, which is what a claim the tracker
	// refused leaves behind.
	completed := moment.Add(time.Second)
	after := before
	after.Status = runstate.StatusFailed
	after.CompletedAt = &completed
	after.Failure = "claim work item: Error claiming yoyodyne-ifd.68.2: issue not claimable: status blocked"

	_, notifications := crossed(t, before, after)
	said := only(t, notifications, KindRunEnded)
	message, err := Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render the ending: %v", err)
	}
	if !strings.Contains(message.Body, "development manager") || !strings.Contains(message.Body, "docket") {
		t.Fatalf("the ending does not send the reader to the entry made for them:\n%s", message.Body)
	}
	if strings.Contains(message.Body, "nothing was recorded for anybody to decide") {
		t.Fatalf("the ending still says nothing was recorded, which is the defect:\n%s", message.Body)
	}

	// A run that got as far as claiming keeps the clause it always had: nothing
	// about it was docketed, and sending a reader to triage over it would be the
	// guessing the fixed clause exists to avoid.
	claimed := endedRun(running(), runstate.StatusFailed)
	claimed.WorkItemClaimedAt = &moment
	claimed.Failure = "the process was killed mid-change"
	_, alsoSaid := crossed(t, running(), claimed)
	ordinary, err := Render(alsoSaid[0].Topic, alsoSaid[0].Speaker, only(t, alsoSaid, KindRunEnded).Event)
	if err != nil {
		t.Fatalf("render the ordinary ending: %v", err)
	}
	if !strings.Contains(ordinary.Body, "nothing was recorded for anybody to decide") {
		t.Fatalf("an ordinary ending lost the clause it had:\n%s", ordinary.Body)
	}
}

// A turn served by another provider is said as a substitution like any other,
// and says which provider produced the work. The two model selectors alone
// cannot: two providers can spell one model name, and an operator shown "fable
// rather than fable" is shown a substitution that reads as no substitution at
// all.
func TestACrossProviderSubstitutionNamesTheProviderThatProducedTheWork(t *testing.T) {
	crossed := runstate.UsageLimitExhaustion{
		SchemaVersion:    runstate.UsageLimitSchemaVersion,
		ProductID:        "yoyodyne",
		At:               moment,
		Waiting:          "the development manager conversation chat-91253e0e",
		ConversationID:   "chat-91253e0e",
		Kind:             "five_hour",
		Model:            "fable",
		ServedBy:         "fable",
		Provider:         "claude-code",
		ServedByProvider: "codex",
		Substitution:     runstate.SubstitutedForCapacity,
	}
	notification, err := FromUsageLimit(crossed)
	if err != nil {
		t.Fatalf("select from a cross-provider substitution: %v", err)
	}
	if notification.Event.Kind != KindModelSubstituted {
		t.Fatalf("a crossing is said as %q, want %q", notification.Event.Kind, KindModelSubstituted)
	}
	if notification.Event.Severity != report.SeverityNote {
		t.Fatalf("a crossing is said at %q, want %q: nothing stopped", notification.Event.Severity, report.SeverityNote)
	}
	for _, speaker := range []Speaker{
		Harness(),
		Persona(domain.RoleDeveloper, ""),
		Persona(domain.RoleReviewer, ""),
		Persona(domain.RoleDevelopmentManager, ""),
		Persona(domain.RoleProductManager, ""),
		Persona(domain.RoleArchitect, ""),
	} {
		message, err := Render(notification.Topic, speaker, notification.Event)
		if err != nil {
			t.Fatalf("the %s cannot say a crossing: %v", speaker.Key(), err)
		}
		for _, want := range []string{"claude-code's fable", "codex's fable", "rebuilt its context from the durable record"} {
			if !strings.Contains(message.Body, want) {
				t.Fatalf("the %s said %q, which does not name %q", speaker.Key(), message.Body, want)
			}
		}
	}
}
