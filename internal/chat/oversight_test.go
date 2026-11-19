package chat

// Seeing and controlling work the harness is running on its own.
//
// Everything here is about work this conversation did not start. That is the
// whole difference: the commands that existed before assumed the operator had
// typed the identifier themselves, so they could report a run this process owned
// and cancel a context it held. None of these can, and none of them may pretend
// otherwise.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Why an item is running is the question with no answer at all once something
// other than the operator chose it, and an item running for no visible reason is
// the failure this exists to prevent. So a survey says why for every run, and
// says plainly when the run recorded nothing.
func TestStatusSaysWhyEachRunningItemWasChosen(t *testing.T) {
	t.Parallel()

	work := &fakeWork{survey: Survey{InFlight: []RunSnapshot{
		{
			RunID:           "run-0123456789abcdef0123456789abcdef",
			WorkItemID:      "yoyodyne-ifd.16",
			Status:          "running",
			Phase:           "developing",
			StartedAt:       fixedClock{}.Now(),
			SelectedBy:      DevelopmentManagerSelected,
			SelectedBecause: "highest-priority admitted item with nothing blocking it",
		},
		{
			RunID:      "run-fedcba9876543210fedcba9876543210",
			WorkItemID: "yoyodyne-ifd.17",
			Status:     "running",
			Phase:      "reviewing",
			StartedAt:  fixedClock{}.Now(),
		},
	}}}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/status\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if !strings.Contains(transcript, "chosen by the development manager: highest-priority admitted item with nothing blocking it") {
		t.Fatalf("transcript = %q, want the reason the harness picked the item", transcript)
	}
	// The unaccounted run is the one that matters most. A blank line where a
	// reason belongs reads as a reason nobody bothered to type; this has to read
	// as a run nothing accounts for.
	if !strings.Contains(transcript, "chosen by: nothing recorded why this run was started") {
		t.Fatalf("transcript = %q, want a run with no recorded reason named as one", transcript)
	}
}

// Stopping an item the operator never started is the point of the whole design.
// The run is somewhere else, so it is asked rather than cancelled, and what it
// leaves behind is settled exactly as a run stopped in this process is.
func TestStoppingAnItemThisConversationNeverStarted(t *testing.T) {
	t.Parallel()

	work := &fakeWork{
		survey: Survey{InFlight: []RunSnapshot{{
			RunID:      "run-0123456789abcdef0123456789abcdef",
			WorkItemID: "yoyodyne-ifd.16",
			Status:     "running",
			Phase:      "developing",
			StartedAt:  fixedClock{}.Now(),
		}}},
		// The run is still working when it is asked, and has ended by the second
		// reading — which is what honoring a stop at the next boundary looks like
		// from outside the process doing it.
		progress: []RunProgress{
			{RunID: "run-0123456789abcdef0123456789abcdef", Status: "running", Phase: "developing"},
			{RunID: "run-0123456789abcdef0123456789abcdef", Status: "cancelled", Phase: "developing"},
		},
		settlements: []Settlement{{
			RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.16",
			Action: "blocked", Detail: "its worktree is preserved",
		}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.StopGrace = time.Second
	session := openTestSession(t, options)

	stopped, err := session.StopWork(context.Background(), "yoyodyne-ifd.16", "it is rewriting the wrong file")
	if err != nil {
		t.Fatalf("StopWork() error = %v", err)
	}
	if !stopped.Requested || !stopped.Finished || stopped.AlreadyFinished {
		t.Fatalf("stopped = %#v, want a run that was asked and gave up", stopped)
	}
	asked := work.stopRequests()
	if len(asked) != 1 || asked[0].RunID != "run-0123456789abcdef0123456789abcdef" ||
		asked[0].Reason != "it is rewriting the wrong file" {
		t.Fatalf("stop requests = %#v, want the run named and the reason carried", asked)
	}
	// Settling is what keeps stopping one verb rather than one verb and a
	// follow-up, and it has to happen for a run in another process too.
	if work.settleCount() != 1 {
		t.Fatalf("settles = %d, want what the stopped run left behind settled once", work.settleCount())
	}
	if len(stopped.Settlements) != 1 {
		t.Fatalf("settlements = %#v, want what settling found reported back", stopped.Settlements)
	}
	if noted := work.takenNotes(); len(noted) != 1 || noted[0][0] != "yoyodyne-ifd.16" ||
		!strings.Contains(noted[0][1], "it is rewriting the wrong file") {
		t.Fatalf("notes = %#v, want the item told why it was stopped", noted)
	}
	rendered := stopped.Render()
	if !strings.Contains(rendered, "stopped work on yoyodyne-ifd.16") {
		t.Fatalf("rendered = %q, want it to say what was stopped", rendered)
	}
}

// The report says what happened rather than what was asked for. A run that
// reached its own conclusion first was not stopped, and nothing is written on
// its item or settled on its behalf.
func TestStoppingReportsAnItemThatFinishedBeforeTheStopArrived(t *testing.T) {
	t.Parallel()

	work := &fakeWork{
		survey: Survey{InFlight: []RunSnapshot{{
			RunID:      "run-0123456789abcdef0123456789abcdef",
			WorkItemID: "yoyodyne-ifd.16",
			Status:     "running",
			Phase:      "integrating",
			StartedAt:  fixedClock{}.Now(),
		}}},
		progress: []RunProgress{{
			RunID: "run-0123456789abcdef0123456789abcdef", Status: "succeeded", Phase: "complete",
			Integrated: true, TargetBranch: "main",
		}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.StopGrace = time.Second
	session := openTestSession(t, options)

	stopped, err := session.StopWork(context.Background(), "yoyodyne-ifd.16", "never mind")
	if err != nil {
		t.Fatalf("StopWork() error = %v", err)
	}
	if !stopped.AlreadyFinished {
		t.Fatalf("stopped = %#v, want the run reported as having finished first", stopped)
	}
	if len(work.takenNotes()) != 0 {
		t.Fatalf("notes = %#v, want nothing written on an item nothing stopped", work.takenNotes())
	}
	if work.settleCount() != 0 {
		t.Fatalf("settles = %d, want nothing settled for a run that finished on its own", work.settleCount())
	}
	rendered := stopped.Render()
	if !strings.Contains(rendered, "had already finished before the stop reached it") {
		t.Fatalf("rendered = %q, want it to say nothing was stopped", rendered)
	}
	if !strings.Contains(rendered, "integrated into main") {
		t.Fatalf("rendered = %q, want it to say what the run did instead", rendered)
	}
}

// A run mid-generation does not stop instantly, and the report must not say it
// did. What the operator is told is that it was asked, that it is still going,
// and that nothing it had was thrown away.
func TestStoppingReportsARunThatHasNotGivenUpYet(t *testing.T) {
	t.Parallel()

	work := &fakeWork{
		survey: Survey{InFlight: []RunSnapshot{{
			RunID:      "run-0123456789abcdef0123456789abcdef",
			WorkItemID: "yoyodyne-ifd.16",
			Status:     "running",
			Phase:      "developing",
			StartedAt:  fixedClock{}.Now(),
		}}},
		progress: []RunProgress{{RunID: "run-0123456789abcdef0123456789abcdef", Status: "running", Phase: "developing"}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	// A grace too short to fit a single probe still means look once: the run may
	// already have ended, and reporting it as still going would be wrong in
	// exactly the direction that matters.
	options.StopGrace = time.Millisecond
	session := openTestSession(t, options)

	stopped, err := session.StopWork(context.Background(), "yoyodyne-ifd.16", "")
	if err != nil {
		t.Fatalf("StopWork() error = %v", err)
	}
	if stopped.Finished || !stopped.Requested {
		t.Fatalf("stopped = %#v, want a request that has not landed yet", stopped)
	}
	if len(work.stopRequests()) != 1 {
		t.Fatalf("stop requests = %#v, want the request recorded even though it has not landed", work.stopRequests())
	}
	// Nothing is decided about a run that is still going: no note, no settling.
	if len(work.takenNotes()) != 0 || work.settleCount() != 0 {
		t.Fatalf("notes = %#v settles = %d, want nothing decided about a run still in flight", work.takenNotes(), work.settleCount())
	}
	rendered := stopped.Render()
	if !strings.Contains(rendered, "stops at its next step, no later than ten minutes after it was asked") {
		t.Fatalf("rendered = %q, want it to say what is actually true of the run", rendered)
	}
}

// Nothing in flight for the item is a plain answer rather than a failure to
// look, and it says which of the two it is: work that is over, or work the
// harness has never run.
func TestStoppingSaysPlainlyWhenNothingIsInFlight(t *testing.T) {
	t.Parallel()

	work := &fakeWork{progress: []RunProgress{{RunID: "run-0123456789abcdef0123456789abcdef", Status: "succeeded"}}}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	_, err := session.StopWork(context.Background(), "yoyodyne-ifd.16", "")
	if err == nil {
		t.Fatal("StopWork() error = nil, want it to say there is nothing to stop")
	}
	if !strings.Contains(err.Error(), "nothing is in flight") || !strings.Contains(err.Error(), "succeeded") {
		t.Fatalf("StopWork() error = %v, want it to say what became of the last run", err)
	}
	if len(work.stopRequests()) != 0 {
		t.Fatalf("stop requests = %#v, want nothing asked of a run that does not exist", work.stopRequests())
	}
}

// Holding intake and stopping a run are different intentions. This one starts
// nothing more and disturbs nothing that is running, which is what an operator
// reaches for when something looks wrong but not urgent.
func TestHoldingIntakeStopsNothingThatIsRunning(t *testing.T) {
	t.Parallel()

	work := &fakeWork{survey: Survey{InFlight: []RunSnapshot{{
		RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.16",
		Status: "running", StartedAt: fixedClock{}.Now(),
	}}}}
	intake := &fakeIntake{}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.Intake = intake
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader(
		"/hold the decomposition is heading somewhere odd\n/status\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if !strings.Contains(transcript, "held intake at") {
		t.Fatalf("transcript = %q, want the hold reported", transcript)
	}
	if !strings.Contains(transcript, "INTAKE HELD") {
		t.Fatalf("transcript = %q, want a held intake to lead the status report", transcript)
	}
	if !strings.Contains(intake.reason, "the decomposition is heading somewhere odd") {
		t.Fatalf("hold reason = %q, want what the operator said kept with the hold", intake.reason)
	}
	// Nothing was asked to stop and nothing was settled: that is the difference
	// between this and both of the other two verbs.
	if len(work.stopRequests()) != 0 || work.settleCount() != 0 {
		t.Fatalf("stops = %#v settles = %d, want a held intake to disturb nothing in flight", work.stopRequests(), work.settleCount())
	}
}

// The hold an operator finds in a conversation is not always their own: the
// failure-storm brake places the same switch. Every surface that reports it
// names whichever placed it, and none of them composes that out of what it
// happens to know — the banner said "the operator" for a hold the brake placed,
// which sent the operator diagnosing their own state instead of the line's.
func TestAConversationNamesTheBrakeForAHoldTheBrakePlaced(t *testing.T) {
	t.Parallel()

	intake := &fakeIntake{}
	if _, err := intake.Hold(runstate.IntakeHolderBrake,
		"3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
		fixedClock{}.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{}
	options.Intake = intake
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader(
		"/status\n/hold\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if !strings.Contains(transcript, "the harness's own brake placed it after 3 run(s) blocked in a row") {
		t.Fatalf("transcript = %q, want the brake named as the holder, with the storm as the cause", transcript)
	}
	if strings.Contains(transcript, "the operator placed it") {
		t.Fatalf("transcript = %q, want nothing attributing the brake's hold to the operator", transcript)
	}

	// Releasing it says what was lifted in the same words. Whoever lifts a hold,
	// who placed it is what the record says.
	lifted, err := session.ReleaseIntake()
	if err != nil {
		t.Fatalf("ReleaseIntake() error = %v", err)
	}
	if rendered := lifted.Render(); !strings.Contains(rendered, "the harness's own brake placed it") {
		t.Fatalf("rendered = %q, want the lifted hold reported as the brake's", rendered)
	}
}

// Releasing lets the harness choose work again, and releasing what is not held
// changed nothing — which the report has to say rather than claiming an act that
// never happened.
func TestReleasingIntakeSaysWhetherItChangedAnything(t *testing.T) {
	t.Parallel()

	intake := &fakeIntake{}
	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{}
	options.Intake = intake
	session := openTestSession(t, options)

	notHeld, err := session.ReleaseIntake()
	if err != nil {
		t.Fatalf("ReleaseIntake() error = %v", err)
	}
	if notHeld.Lifted || notHeld.Held {
		t.Fatalf("release = %#v, want a no-op over intake that was already running", notHeld)
	}
	if !strings.Contains(notHeld.Render(), "intake is not held") {
		t.Fatalf("rendered = %q, want it to say nothing was holding intake", notHeld.Render())
	}

	if _, err := session.HoldIntake("looking at the queue"); err != nil {
		t.Fatalf("HoldIntake() error = %v", err)
	}
	lifted, err := session.ReleaseIntake()
	if err != nil {
		t.Fatalf("ReleaseIntake() error = %v", err)
	}
	if !lifted.Lifted || lifted.Held {
		t.Fatalf("release = %#v, want the hold lifted", lifted)
	}
	if !strings.Contains(lifted.Render(), "released the hold on intake") {
		t.Fatalf("rendered = %q, want it to say what was lifted", lifted.Render())
	}
}

// Stopping everything is the third intention: hold intake so nothing more
// starts, and stop each run that is in flight. It reports what became of each
// rather than that it asked.
func TestStoppingEverythingHoldsIntakeAndStopsEveryRun(t *testing.T) {
	t.Parallel()

	work := &fakeWork{
		survey: Survey{InFlight: []RunSnapshot{
			{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.16", Status: "running", StartedAt: fixedClock{}.Now()},
			{RunID: "run-fedcba9876543210fedcba9876543210", WorkItemID: "yoyodyne-ifd.17", Status: "running", StartedAt: fixedClock{}.Now()},
		}},
		progressByItem: map[string]RunProgress{
			"yoyodyne-ifd.16": {RunID: "run-0123456789abcdef0123456789abcdef", Status: "cancelled"},
			// The second run integrated before the stop reached it, which is exactly
			// the case the report must not describe as stopped.
			"yoyodyne-ifd.17": {RunID: "run-fedcba9876543210fedcba9876543210", Status: "succeeded", Integrated: true, TargetBranch: "main"},
		},
	}
	intake := &fakeIntake{}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.Intake = intake
	options.StopGrace = time.Second
	session := openTestSession(t, options)

	everything, err := session.StopEverything(context.Background(), "something is badly wrong")
	if err != nil {
		t.Fatalf("StopEverything() error = %v", err)
	}
	if everything.Intake == nil || !everything.Intake.Placed {
		t.Fatalf("intake = %#v, want it held before anything was stopped", everything.Intake)
	}
	if len(everything.Stopped) != 2 {
		t.Fatalf("stopped = %#v, want both runs accounted for", everything.Stopped)
	}
	if len(work.stopRequests()) != 2 {
		t.Fatalf("stop requests = %#v, want every run in flight asked", work.stopRequests())
	}
	rendered := everything.Render()
	if !strings.Contains(rendered, "stopped work on yoyodyne-ifd.16") {
		t.Fatalf("rendered = %q, want the run that stopped reported as stopped", rendered)
	}
	if !strings.Contains(rendered, "yoyodyne-ifd.17 had already finished before the stop reached it") {
		t.Fatalf("rendered = %q, want the run that finished first reported honestly", rendered)
	}
}

// The three verbs reach the operator through the conversation they are already
// in, and none of them needs this conversation to have started the work.
func TestTheConversationOffersStoppingHoldingAndStoppingEverything(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{}
	options.Intake = &fakeIntake{}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/help\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	for _, offered := range []string{
		"/stop [beads-id] [reason]",
		"/hold [reason]",
		"/release",
		"/stop-everything [reason]",
	} {
		if !strings.Contains(transcript, offered) {
			t.Fatalf("transcript = %q, want it to offer %q", transcript, offered)
		}
	}
}

// A single message never started a run, and stopping a named item does not need
// one: it reads durable state and asks whichever process holds the run. Only the
// bare form is refused, and it says what to type instead.
func TestStoppingANamedItemWorksWithoutAConversation(t *testing.T) {
	t.Parallel()

	work := &fakeWork{
		survey: Survey{InFlight: []RunSnapshot{{
			RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.16",
			Status: "running", StartedAt: fixedClock{}.Now(),
		}}},
		progress: []RunProgress{{RunID: "run-0123456789abcdef0123456789abcdef", Status: "cancelled"}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.StopGrace = time.Second
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Command(context.Background(), "/stop yoyodyne-ifd.16 it is looping", &out); err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if len(work.stopRequests()) != 1 {
		t.Fatalf("stop requests = %#v, want the named run asked to stop", work.stopRequests())
	}
	if !strings.Contains(out.String(), "stopped work on yoyodyne-ifd.16") {
		t.Fatalf("output = %q, want the stop reported", out.String())
	}
}

// fakeIntake stands in for the durable switch over what the harness starts by
// itself. It behaves as the real one does in the two ways that matter: holding
// twice does not restamp the hold, and releasing what is not held is not an
// error.
type fakeIntake struct {
	mu     sync.Mutex
	held   bool
	heldAt time.Time
	heldBy runstate.IntakeHolder
	reason string
	err    error
	// brake is the brake's record on a hold it placed, and decisions is every
	// brake decision recorded onto it, in order.
	brake     *runstate.IntakeBrake
	decisions []runstate.IntakeBrake
}

func (f *fakeIntake) Hold(holder runstate.IntakeHolder, reason string, at time.Time) (runstate.IntakeHold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return runstate.IntakeHold{}, f.err
	}
	if !f.held {
		f.held = true
		f.heldAt = at.UTC()
		f.heldBy = holder
		f.reason = reason
	}
	return f.hold(), nil
}

func (f *fakeIntake) Held() (runstate.IntakeHold, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return runstate.IntakeHold{}, false, f.err
	}
	return f.hold(), f.held, nil
}

func (f *fakeIntake) ReleaseBy(string, time.Time) (runstate.IntakeHold, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return runstate.IntakeHold{}, false, f.err
	}
	held, lifted := f.hold(), f.held
	f.held = false
	return held, lifted, nil
}

// DecideBrake records the development manager's decision onto the brake's own
// hold, refusing every other hold exactly as the store does.
func (f *fakeIntake) DecideBrake(decision runstate.IntakeBrakeDecision, reason, by string, at time.Time) (runstate.IntakeHold, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return runstate.IntakeHold{}, f.err
	}
	if !f.held || f.heldBy != runstate.IntakeHolderBrake || f.brake == nil {
		return f.hold(), runstate.ErrNoBrakeHold
	}
	decidedAt := at.UTC()
	revised := *f.brake
	revised.Decision, revised.DecidedAt, revised.DecidedBy, revised.DecisionReason = decision, &decidedAt, by, reason
	f.brake = &revised
	f.decisions = append(f.decisions, revised)
	return f.hold(), nil
}

func (f *fakeIntake) hold() runstate.IntakeHold {
	return runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        f.heldAt,
		HeldBy:        f.heldBy,
		Reason:        f.reason,
		Brake:         f.brake,
	}
}

var _ IntakeHolds = (*fakeIntake)(nil)
