package orchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/followup/checkrun"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// xcodeLicense is `xcodebuild -license check` on a machine whose license is
// accepted once accept is called.
type xcodeLicense struct {
	mu       sync.Mutex
	accepted bool
	asked    int
}

func (l *xcodeLicense) accept() { l.mu.Lock(); l.accepted = true; l.mu.Unlock() }

func (l *xcodeLicense) command(_ context.Context, name string, args ...string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if name != "xcodebuild" || strings.Join(args, " ") != "-license check" {
		return "", errors.New("unexpected command " + name)
	}
	l.asked++
	if l.accepted {
		return "", nil
	}
	return "You have not agreed to the Xcode license agreements.", errors.New("exit status 69")
}

func followUpStore(t *testing.T) *runstate.FollowUpStore {
	t.Helper()
	store, err := runstate.NewFollowUpStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// A run stops because the Xcode license is not accepted. The session holds new
// work on a follow-up whose check is the license, starts no run to find out
// however long the license stays unaccepted, and once it is accepted releases
// the hold within one check interval, records why, and starts work again.
func TestALicenseHoldIsReleasedWithinOneIntervalOfItsCheckPassing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	var started []string
	var startedAt []int
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		started, startedAt = append(started, id), append(startedAt, h.sleeps)
		h.mu.Unlock()
		if id == "yoyodyne-one" {
			return stoppedWithNoCheckRan(h, id)
		}
		return h.complete(id), nil
	}
	// Accepted at forty-five minutes: after the thirty a trial run would have
	// waited, so a run started before then is a schedule other than the runner.
	const acceptedAt = 45
	machine := &xcodeLicense{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool {
		if sleeps == acceptedAt {
			machine.accept()
		}
		return sleeps < 90
	}
	store := followUpStore(t)
	told := &toldReports{}
	runner := &checkrun.Runner{Store: store, Environment: checkrun.Environment{Command: machine.command},
		Tell: &checkrun.Teller{Reports: told, ProductID: "yoyodyne", RepositoryID: "yoyodyne"}}
	sessions := &recordedSessions{}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock, FollowUps: runner}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	if strings.Join(started, ",") != "yoyodyne-one,yoyodyne-two" {
		t.Fatalf("started %v, want the run that met the license and then the next item once it was accepted", started)
	}
	if startedAt[1] < acceptedAt || startedAt[1] > acceptedAt+int(checkrun.DefaultCommandInterval/time.Minute) {
		t.Fatalf("the next run started after %d one-minute polls, license accepted after %d; want it within one interval of accepting", startedAt[1], acceptedAt)
	}
	kept, err := store.List()
	if err != nil || len(kept) != 1 {
		t.Fatalf("follow-ups = %+v, %v; want the hold's one", kept, err)
	}
	passed := kept[0]
	if passed.State != followup.StatePassed || passed.Passed == nil || passed.Check.Arguments[followup.ArgCheck] != string(followup.EnvironmentXcodeLicense) {
		t.Fatalf("follow-up = %+v, want the license check passed with its time", passed)
	}
	if holds := passed.Holds(); len(holds) != 1 || holds[0] != checkrun.HoldID(checkrun.HoldChecksCouldNotRun, "run-yoyodyne-one") {
		t.Fatalf("follow-up is about holds %v, want the one run-yoyodyne-one placed", holds)
	}
	// The runner asked the machine about once every ten minutes and nothing else
	// asked at all.
	if machine.asked > startedAt[1]/int(checkrun.DefaultCommandInterval/time.Minute)+1 {
		t.Fatalf("the license was asked about %d times in %d minutes, want only the check runner's rounds", machine.asked, startedAt[1])
	}
	if len(schedule.FollowUpReleased) != 1 || !strings.Contains(schedule.FollowUpReleased[0], "the Xcode license has been accepted") {
		t.Fatalf("released = %q, want the release said with its reason", schedule.FollowUpReleased)
	}
	waiting, ok := sessions.entered(runstate.WatchBlocked)
	if !ok || !strings.Contains(waiting.reason, "the harness checks every 10 minutes whether the Xcode license has been accepted") {
		t.Fatalf("blocked transition = %+v, want it to say what the harness is checking", waiting)
	}
	resumed, ok := releasedTransition(sessions)
	if !ok || !strings.Contains(resumed.reason, "released the hold run run-yoyodyne-one placed on new work") {
		t.Fatalf("released transition = %+v, %t; want the release recorded for status and the channel", resumed, ok)
	}
	if schedule.FollowUpProblem != "" {
		t.Fatalf("follow-up problem = %q", schedule.FollowUpProblem)
	}
	// The development manager, who waits on the hold's follow-up, is told by a
	// report addressed to her, which is put in front of her next pass.
	if len(told.filed) != 1 || told.filed[0].Addressee() != domain.RoleDevelopmentManager || told.filed[0].Role != report.HarnessReporter ||
		!strings.Contains(told.filed[0].Message, passed.ID) || !strings.Contains(told.filed[0].Message, "the Xcode license is accepted") {
		t.Fatalf("reports filed = %+v, want one telling the development manager the follow-up passed", told.filed)
	}
}

type toldReports struct {
	mu    sync.Mutex
	filed []report.Report
}

func (r *toldReports) Append(reported report.Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filed = append(r.filed, reported)
	return nil
}

// unwritable is a check runner whose store will not take a new follow-up.
type unwritable struct{ checkrun.Runner }

func (u *unwritable) Attach(followup.FollowUp) (followup.FollowUp, error) {
	return followup.FollowUp{}, errors.New("the state directory is read-only")
}

// A lapsed login whose follow-up cannot be written stays held, and the session
// still asks the provider nothing itself: it says why and tries the follow-up
// again at the next poll, rather than re-checking the login on its own.
func TestALapsedLoginWhoseFollowUpCannotBeWrittenIsNotReCheckedByTheSession(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.developerProvider = "claude-code"
	outages := newOutageStore(t)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Waiting: "an earlier dispatch", At: harness.now}); err != nil {
		t.Fatal(err)
	}
	session := &orchestratortest.LoginProbe{Authenticated: true}
	harness.outages, harness.provider = outages, session
	started := 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) { started++; return h.complete(id), nil }
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 15 }
	runner := &unwritable{checkrun.Runner{Store: followUpStore(t)}}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock, FollowUps: runner}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if session.Asked != 0 || started != 0 {
		t.Fatalf("the session asked the provider %d times and started %d runs; want the hold standing and nothing asked by the session", session.Asked, started)
	}
	if !strings.Contains(schedule.FollowUpProblem, "could not be written, so the hold stands") {
		t.Fatalf("follow-up problem = %q, want the session to say the follow-up could not be written", schedule.FollowUpProblem)
	}
}

func releasedTransition(sessions *recordedSessions) (recordedTransition, bool) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	for _, transition := range sessions.transitions {
		if transition.released {
			return transition, transition.state == runstate.WatchResumed
		}
	}
	return recordedTransition{}, false
}

// A hold whose follow-up has no check that can pass is never released by it:
// a runner that cannot run the license check leaves the hold standing and says
// why, and no run is started to find out.
func TestAHoldWhoseCheckCannotRunIsNotReleased(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	var started []string
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		started = append(started, id)
		h.mu.Unlock()
		return stoppedWithNoCheckRan(h, id)
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 90 }
	store := followUpStore(t)
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock, FollowUps: &checkrun.Runner{Store: store}}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if strings.Join(started, ",") != "yoyodyne-one" || len(schedule.FollowUpReleased) != 0 {
		t.Fatalf("started %v, released %q; want the hold to stand", started, schedule.FollowUpReleased)
	}
	kept, _ := store.List()
	if len(kept) != 1 || kept[0].State != followup.StateOpen || kept[0].LastResult == nil || kept[0].LastResult.CouldNotRun == "" {
		t.Fatalf("follow-ups = %+v, want one open whose check could not be run, saying why", kept)
	}
}

// A lapsed login is re-checked by the check runner alone: the session asks the
// provider nothing itself, and once the login passes its check the runner's
// round clears the outage and the session starts work again.
func TestALapsedLoginIsReCheckedOnlyByTheCheckRunner(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	outages := newOutageStore(t)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Provider: "claude-code", Waiting: "an earlier dispatch", At: harness.now}); err != nil {
		t.Fatal(err)
	}
	session, machine := &orchestratortest.LoginProbe{}, &orchestratortest.LoginProbe{}
	harness.outages, harness.provider = outages, session
	var startedAt []int
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		startedAt = append(startedAt, h.sleeps)
		h.mu.Unlock()
		return h.complete(id), nil
	}
	const loggedInAt = 12
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool {
		if sleeps == loggedInAt {
			machine.Login()
			session.Login()
		}
		return sleeps < 40
	}
	store := followUpStore(t)
	runner := &checkrun.Runner{Store: store, Environment: checkrun.Environment{Logins: map[string]checkrun.Login{"claude-code": machine}}}
	sessions := &recordedSessions{}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Sessions: sessions, Now: harness.clock, FollowUps: runner}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if session.Asked != 0 {
		t.Fatalf("the session asked the provider about its login %d times itself, want none: the check runner is what re-checks it", session.Asked)
	}
	if len(startedAt) != 1 || startedAt[0] < loggedInAt || startedAt[0] > loggedInAt+int(checkrun.DefaultCommandInterval/time.Minute) {
		t.Fatalf("started after polls %v, logged in after %d; want one run within one interval of the login", startedAt, loggedInAt)
	}
	if _, standing, _ := outages.Standing(); standing {
		t.Fatal("the outage still stands after its follow-up passed")
	}
	if _, ok := releasedTransition(sessions); !ok || len(schedule.FollowUpReleased) != 1 || !strings.Contains(schedule.FollowUpReleased[0], "claude-code is logged in again") {
		t.Fatalf("released = %q, want the release recorded with its reason", schedule.FollowUpReleased)
	}
}

// The operator's own hold is never released by a follow-up passing, whatever
// the follow-up is about.
func TestTheOperatorsHoldIsNeverReleasedByAFollowUp(t *testing.T) {
	t.Parallel()

	holds, err := runstate.NewOperatorHoldStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holds.Hold(time.Now()); err != nil {
		t.Fatal(err)
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
	store := followUpStore(t)
	runner := &checkrun.Runner{Store: store, Environment: checkrun.Environment{LookPath: func(string) (string, error) { return "/bin/sh", nil }}}
	written, err := runner.Attach(checkrun.HoldFollowUp(checkrun.HoldOperator, checkrun.EnvironmentCheck(followup.EnvironmentToolOnPath, "sh"), nil, harness.clock()))
	if err != nil {
		t.Fatal(err)
	}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock, FollowUps: runner}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	passed, _ := store.Load(written.ID)
	if passed.State != followup.StatePassed {
		t.Fatalf("follow-up state %s, want its check passed", passed.State)
	}
	if _, held, _ := holds.Held(); !held || len(schedule.FollowUpReleased) != 0 {
		t.Fatalf("held = %t, released %q; want the operator's hold standing", held, schedule.FollowUpReleased)
	}
}

// A login follow-up that passes for one provider leaves an outage standing for
// another provider where it was.
func TestALoginPassingForOneProviderLeavesAnotherProvidersOutage(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	outages := newOutageStore(t)
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnauthenticated, Provider: "codex", Waiting: "an earlier dispatch", At: harness.now}); err != nil {
		t.Fatal(err)
	}
	harness.outages, harness.provider = outages, &orchestratortest.LoginProbe{}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }
	store := followUpStore(t)
	runner := &checkrun.Runner{Store: store, Environment: checkrun.Environment{Logins: map[string]checkrun.Login{"claude-code": &orchestratortest.LoginProbe{Authenticated: true}}}}
	if _, err := runner.Attach(checkrun.HoldFollowUp(checkrun.HoldID(checkrun.HoldProviderLogin, "claude-code"),
		checkrun.EnvironmentCheck(followup.EnvironmentProviderLogin, "claude-code"), nil, harness.clock())); err != nil {
		t.Fatal(err)
	}
	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock, FollowUps: runner}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if _, standing, _ := outages.Standing(); !standing || len(schedule.FollowUpReleased) != 0 {
		t.Fatalf("standing = %t, released %q; want codex's outage left where it was", standing, schedule.FollowUpReleased)
	}
}
