package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var passWindowStart = time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)

func TestAdmissionEventsRefreshBeforeReadingAndRefuseAStaleSnapshot(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	reader := passEvents{repository: repository, refresh: func(context.Context) error {
		if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(repository, issuesExport), []byte(fmt.Sprintf(
			`{"id":"new-admission","title":"new work","created_at":%q}`+"\n", passWindowStart.Add(time.Minute).Format(time.RFC3339Nano))), 0o600)
	}}
	events, err := reader.Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour))
	if err != nil || len(events) != 1 || events[0].Subject != "new-admission" {
		t.Fatalf("fresh admissions = %v, %v", events, err)
	}
	reader.refresh = func(context.Context) error { return errors.New("export refused") }
	if events, err := reader.Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour)); err == nil || len(events) != 0 {
		t.Fatalf("stale admission snapshot = %v, %v", events, err)
	}
}

// Admissions are read from the tracker's export by when each item was
// created: the thirty created inside the window, and nothing created before it,
// with an unreadable line and a record that is not an item set aside.
func TestAdmissionsAreTheItemsTheExportRecordsAsCreatedInTheWindow(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf(`{"_type":"issue","id":"yoyodyne-ifd.1","title":"before","created_at":%q}`, passWindowStart.Add(-time.Minute).Format(time.RFC3339Nano)))
	for index := 1; index <= 30; index++ {
		lines = append(lines, fmt.Sprintf(`{"_type":"issue","id":"yoyodyne-ifd.1%02d","title":"item %d","created_at":%q}`, index, index, passWindowStart.Add(time.Minute).Format(time.RFC3339Nano)))
	}
	lines = append(lines, "{not json", fmt.Sprintf(`{"_type":"memory","id":"m-1","created_at":%q}`, passWindowStart.Add(time.Minute).Format(time.RFC3339Nano)))
	if err := os.WriteFile(filepath.Join(repository, issuesExport), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	events, err := passEvents{repository: repository}.Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(events) != 30 {
		t.Fatalf("events = %d, want the thirty created inside the window", len(events))
	}
	for _, event := range events {
		if event.Class != config.TriggerAdmissions || event.Stream != runstate.PassStreamTracker || !strings.HasPrefix(event.Detail, "item ") || event.Key != event.Subject {
			t.Fatalf("event = %+v, want an admission read from the tracker", event)
		}
	}
}

// A repository with a tracker and no export is a stream that could not be
// read, not a quiet one; a repository with no tracker has admitted nothing.
func TestAMissingExportIsUnreadableOnlyWhereThereIsATracker(t *testing.T) {
	t.Parallel()

	bare := t.TempDir()
	if events, err := (passEvents{repository: bare}).Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour)); err != nil || len(events) != 0 {
		t.Fatalf("Events() = %v, %v; want nothing admitted where there is no tracker", events, err)
	}
	tracked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tracked, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (passEvents{repository: tracked}).Events(context.Background(), runstate.PassStreamTracker, passWindowStart, passWindowStart.Add(time.Hour)); err == nil {
		t.Fatal("Events() error = nil, want a tracker with no export reported")
	}
}

// Landings and stoppages are read from the run records by when each run
// ended: a run that landed its change, and one that stopped with a blocker;
// a run that succeeded without landing, one still running, and one that ended
// outside the window are none of them.
func TestRunEventsAreTheRunsThatLandedOrStoppedInTheWindow(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	inside := passWindowStart.Add(10 * time.Minute)

	landed := recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.landed", inside)
	landed.WorkItemTitle = "the change that landed"
	landed.WorktreePath = filepath.Join(t.TempDir(), "worktree")
	landed.Branch = "yoyodyne/landed"
	landed.BaseCommit = substrateBase
	promoted(&landed)
	if err := store.Save(landed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stopped := recordedRun(t, store, runstate.StatusFailed, "yoyodyne-ifd.stopped", inside)
	stopped.Blocker = "a configured check failed"
	if err := store.Save(stopped); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.unlanded", inside)
	recordedRun(t, store, runstate.StatusRunning, "yoyodyne-ifd.running", inside)
	recordedRun(t, store, runstate.StatusSucceeded, "yoyodyne-ifd.before", passWindowStart.Add(-time.Minute))

	events, err := passEvents{runs: store}.Events(context.Background(), runstate.PassStreamRuns, passWindowStart, passWindowStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	classes := map[string]config.TriggerEvent{}
	for _, event := range events {
		classes[event.Subject] = event.Class
		if !strings.HasSuffix(event.Key, "/"+string(event.Class)) || !strings.HasPrefix(event.Key, "run-") {
			t.Errorf("key = %q, want the run and the class, so a record read again is known as carried", event.Key)
		}
	}
	if len(events) != 2 || classes["yoyodyne-ifd.landed"] != config.TriggerLandings || classes["yoyodyne-ifd.stopped"] != config.TriggerStoppages {
		t.Fatalf("events = %+v, want the landing and the stoppage alone", events)
	}
}

// Every terminal failure and decision wait reaches the instance once, even
// when it has no blocker or ended during checks. The next scheduled pass reads
// the overlap behind its cursor without carrying those runs again.
func TestEveryStoppedRunReachesThePassOnceAfterItsCursor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := runstate.NewPassCursorStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	sweeps, err := runstate.NewSweepStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	const agent = "factory-flow-pm"
	clock := &steppedClock{at: passWindowStart}
	role := &wakeCapture{}
	trigger := orchestrator.Trigger{
		Instances: map[string]config.AgentConfig{
			agent: {
				Role:     domain.RoleProgramManager,
				Lane:     "factory-flow",
				Triggers: config.Triggers{Every: config.Duration(5 * time.Minute), On: []config.TriggerEvent{config.TriggerStoppages}},
			},
		},
		Claims: sweeps, Reports: sweeps, Roles: role, Clock: clock,
		Cursors: cursors, Events: passEvents{runs: store},
	}
	fire := func(want int) {
		t.Helper()
		fired, err := trigger.Fire(context.Background())
		if err != nil || len(fired.Fired) != 1 {
			t.Fatalf("Fire() = %+v, %v; want one scheduled pass", fired, err)
		}
		if got := fired.Fired[0].Events[string(config.TriggerStoppages)]; got != want {
			t.Fatalf("pass carried %d stoppages, want %d", got, want)
		}
	}
	fire(0) // Begin watching at this moment, without reading earlier runs.

	inside := passWindowStart.Add(10 * time.Minute)
	var stopped []runstate.State
	for _, ending := range []struct {
		name    string
		status  runstate.Status
		phase   runstate.Phase
		failure string
		blocker string
		class   runstate.StopClass
		landing string
		review  string
		why     string
	}{
		{name: "failed", status: runstate.StatusFailed, phase: runstate.PhaseDeveloping, failure: "the developer process failed"},
		{name: "timed-out", status: runstate.StatusTimedOut, phase: runstate.PhaseChecking, failure: "the check exceeded its time bound", class: runstate.StopCheckTimeout},
		{name: "cancelled", status: runstate.StatusCancelled, phase: runstate.PhaseReviewing, failure: "the run was cancelled"},
		{name: "decision", status: runstate.StatusFailed, phase: runstate.PhaseChecking, blocker: "a configured check kept failing", class: runstate.StopChecks},
		{name: "succeeded-blocked", status: runstate.StatusSucceeded, blocker: "the recorded promotion was not on the target"},
		{name: "developer-decision", status: runstate.StatusSucceeded, phase: runstate.PhaseDeveloping, landing: runstate.LandingEscalate, why: "the developer found contradictory requirements"},
		{name: "reviewer-decision", status: runstate.StatusSucceeded, phase: runstate.PhaseReviewing, review: runstate.ReviewEscalate, why: "the reviewer found an impossible criterion"},
		{name: "no-reason", status: runstate.StatusCancelled},
	} {
		state := recordedRun(t, store, runstate.StatusRunning, "yoyodyne-"+ending.name, passWindowStart.Add(-time.Hour))
		state.WorkItemTitle = "the " + ending.name + " change"
		state.Status, state.CompletedAt, state.UpdatedAt = ending.status, &inside, inside
		state.Phase, state.Failure, state.Blocker, state.StopClass = ending.phase, ending.failure, ending.blocker, ending.class
		state.LandingOutcome, state.LandingReason = ending.landing, ending.why
		state.ReviewDecision, state.ReviewSummary = ending.review, ending.why
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		stopped = append(stopped, state)
	}
	// Neither an active run with a blocker nor a run outside the time window
	// belongs to this pass. The start of the window is exclusive.
	for _, at := range []time.Time{passWindowStart.Add(-time.Minute), passWindowStart, passWindowStart.Add(time.Hour)} {
		recordedRun(t, store, runstate.StatusFailed, "yoyodyne-outside", at)
	}
	running := recordedRun(t, store, runstate.StatusRunning, "yoyodyne-running", inside)
	running.Blocker = "still running"
	if err := store.Save(running); err != nil {
		t.Fatal(err)
	}

	clock.at = passWindowStart.Add(20 * time.Minute)
	events, err := (passEvents{runs: store}).Events(context.Background(), runstate.PassStreamRuns, passWindowStart, clock.at)
	if err != nil || len(events) != len(stopped) {
		t.Fatalf("Events() = %+v, %v; want every stopped run", events, err)
	}
	byKey := map[string]orchestrator.PassEvent{}
	for _, event := range events {
		if _, duplicate := byKey[event.Key]; duplicate {
			t.Fatalf("event %q appeared twice", event.Key)
		}
		byKey[event.Key] = event
	}
	for _, state := range stopped {
		key := state.RunID + "/" + string(config.TriggerStoppages)
		event, found := byKey[key]
		reason := state.Reason()
		if state.Escalated() {
			reason = state.EscalationReason()
		}
		if !found || event.Stream != runstate.PassStreamRuns || event.Class != config.TriggerStoppages || event.Subject != state.WorkItemID || !event.At.Equal(inside) ||
			!strings.Contains(event.Detail, state.WorkItemTitle) || !strings.Contains(event.Detail, reason) {
			t.Fatalf("event = %+v, want %s with its item, completion, title, and reason %q", event, key, reason)
		}
	}

	fire(len(stopped))
	cursor, found, err := cursors.Load(agent)
	if err != nil || !found || !cursor.Streams[runstate.PassStreamRuns].Equal(clock.at) {
		t.Fatalf("cursor = %+v, %v, %v; want the completed pass's watermark", cursor, found, err)
	}
	for key := range byKey {
		if !cursor.WasCarried(runstate.PassStreamRuns, key) {
			t.Fatalf("cursor did not record carrying %s", key)
		}
		if count := strings.Count(role.messages[1], strings.TrimSuffix(key, "/"+string(config.TriggerStoppages))); count != 1 {
			t.Fatalf("pass listed %s %d times, want once", key, count)
		}
	}
	clock.at = clock.at.Add(5 * time.Minute)
	overlap, err := (passEvents{runs: store}).Events(context.Background(), runstate.PassStreamRuns, cursor.ReadFrom(runstate.PassStreamRuns), clock.at)
	if err != nil || len(overlap) != len(stopped) {
		t.Fatalf("overlapping Events() = %+v, %v; want the same runs read again behind the cursor", overlap, err)
	}
	fire(0)
	for key := range byKey {
		if strings.Contains(role.messages[2], strings.TrimSuffix(key, "/"+string(config.TriggerStoppages))) {
			t.Fatalf("the next pass carried %s again", key)
		}
	}
}

// `yoyo sweeps` says what a program manager's pass carried.
func TestASweepListingSaysWhatAPassCarried(t *testing.T) {
	t.Parallel()

	rendered := renderSweep(runstate.Sweep{
		Task:      "reliability-pm",
		Role:      "program-manager",
		StartedAt: passWindowStart,
		Turns:     1,
		Model:     "opus",
		Events:    map[string]int{"admissions": 30, "landings": 1},
		Problem:   "the role answered in prose",
	})
	if !strings.Contains(rendered, "reliability-pm (program-manager), 1 turn(s), on opus") || !strings.Contains(rendered, "carried 1 landing, 30 admissions since its last pass") {
		t.Fatalf("rendered = %q, want the pass's model and what it carried", rendered)
	}
}

// `yoyo sweeps` says how long each pass stood due before it was taken, and says
// nothing of it for a pass whose record does not know when it fell due.
func TestASweepListingSaysHowLongAPassWaitedAfterItFellDue(t *testing.T) {
	t.Parallel()

	waited := renderSweep(runstate.Sweep{
		Task:      "development-manager-sweep",
		Role:      "development-manager",
		DueAt:     passWindowStart.Add(-64 * time.Minute),
		StartedAt: passWindowStart,
		Turns:     1,
		Problem:   "the role answered in prose",
	})
	want := "fell due at " + passWindowStart.Add(-64*time.Minute).UTC().Format(time.RFC3339) + " and waited 1h4m0s before it was taken"
	if !strings.Contains(waited, want) {
		t.Fatalf("rendered = %q, want %q", waited, want)
	}
	unknown := renderSweep(runstate.Sweep{Task: "development-manager-sweep", Role: "development-manager", StartedAt: passWindowStart, Turns: 1, Problem: "the role answered in prose"})
	if strings.Contains(unknown, "fell due") {
		t.Fatalf("rendered = %q, want no wait said where the record has no due time", unknown)
	}
}

// A program manager's pass opens the instance's own conversation, by the
// agent's name, rather than whichever agent fills the role first.
func TestAnInstancesPassOpensTheInstancesOwnConversation(t *testing.T) {
	t.Parallel()

	var opened []string
	open := func(_ context.Context, _ domain.AgentRole, agent, _ string, _ orchestrator.RecurringTurnOptions) (*chat.Session, *runstate.ConversationHold, *runstate.SweepConversationReplacement, error) {
		opened = append(opened, agent)
		return nil, nil, nil, errors.New("the provider is not signed in")
	}
	_, err := roleConversation{open: open}.Wake(context.Background(), domain.RoleProgramManager, "reliability-pm", "reliability-pm#1", "", "a pass", orchestrator.RecurringTurnOptions{})
	if !errors.Is(err, orchestrator.ErrRoleUnreachable) {
		t.Fatalf("Wake() error = %v, want the conversation reported unreachable", err)
	}
	if len(opened) != 1 || opened[0] != "reliability-pm" {
		t.Fatalf("opened = %v, want the instance's own conversation", opened)
	}
}

// A program manager instance's pass opened through the real conversation path,
// over a `claude` that is on PATH and does not answer its version check, is
// refused with what the check came to and never with "not installed".
//
// The cancelled case is the one that happened: factory-flow-pm's first pass,
// at 2026-09-27T00:21:34Z, began in the second the operator's maintenance job
// stopped the scheduler, and its version check was cancelled with the pull. It
// was recorded as a failed firing of a backend that "is not installed". Carried
// as the cancellation it was, it is a pass that could not reach the role and
// not a failed firing, which is what passNotOpened already makes of a stopped
// scheduler once it can see one.
func TestAnInstancesPassRefusedByItsVersionCheckSaysWhatTheCheckCameTo(t *testing.T) {
	// Not parallel: the state root and the PATH the conversation resolves are set
	// here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, twoArchitectsConfig+`  factory-flow-pm:
    role: program-manager
    backend: claude-code
    model: fable
    lane: factory-flow
    triggers:
      every: 2h
`)
	directory := t.TempDir()
	script := "#!/bin/sh\necho 'claude: this install is damaged' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(directory, "claude"), []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	conversation := roleConversation{configPath: configPath, stderr: io.Discard}

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := conversation.Wake(stopped, domain.RoleProgramManager, "factory-flow-pm", "factory-flow-pm#1", "", "a pass", orchestrator.RecurringTurnOptions{})
	if err == nil || strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Wake() on a stopped scheduler error = %v, want the cancellation rather than a missing backend", err)
	}
	var notStarted *orchestrator.NotStartedError
	if !errors.Is(err, context.Canceled) || !errors.Is(err, orchestrator.ErrRoleUnreachable) || errors.As(err, &notStarted) {
		t.Fatalf("Wake() on a stopped scheduler error = %v, want the role unreachable because the pass was cancelled, and not a failed firing", err)
	}

	_, err = conversation.Wake(context.Background(), domain.RoleProgramManager, "factory-flow-pm", "factory-flow-pm#1", "", "a pass", orchestrator.RecurringTurnOptions{})
	if err == nil || strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Wake() over a failing claude error = %v, want what the version check came to rather than a missing backend", err)
	}
	for _, want := range []string{"factory-flow-pm", "`claude --version` exited with status 3", "this install is damaged"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Wake() over a failing claude error = %v, want it to say %q", err, want)
		}
	}
	if !errors.As(err, &notStarted) || notStarted.Cause != runstate.PreTurnConversationUnopened {
		t.Fatalf("Wake() over a failing claude error = %v, want a failed firing whose conversation could not be opened", err)
	}
}
