package orchestrator

// Delivering a stopped run to the development manager, which asks the one
// decision on what may start (internal/maystart) as a prompted pass.

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicDeliveries is stopped runs waiting to be put to the development manager,
// in real stores, with every named epic ended.
type epicDeliveries struct {
	passEpics
	harness *resumeHarness
	judge   *standingJudge
	clock   *movingClock
	asks    int
}

func newEpicDeliveries(t *testing.T) *epicDeliveries {
	t.Helper()
	d := &epicDeliveries{
		passEpics: newPassEpics(t),
		harness:   newResumeHarness(t, reviewStoppedState(docketedRunID, docketedItem)),
		judge:     &standingJudge{judgment: Judgment{ConversationID: "chat-dm", Decision: "wait"}},
		clock:     &movingClock{now: docketedNow.Add(time.Hour)},
	}
	d.end()
	return d
}

func (d *epicDeliveries) stops(runID, item string) {
	d.t.Helper()
	state := reviewStoppedState(runID, item)
	if err := d.harness.runs.Create(state); err != nil {
		d.t.Fatal(err)
	}
	docketer := docketerOverStore(d.harness.docket, d.harness.runs, docketConfig())
	docketer.Clock = docketClock{}
	if _, err := docketer.RecordStoppedRun(state); err != nil {
		d.t.Fatal(err)
	}
}

// escalator is a new one over the same stores, as a restart's is.
func (d *epicDeliveries) escalator() Escalator {
	return Escalator{
		Docket: d.harness.docket, Runs: d.harness.runs, Records: d.harness.runs.Escalations(),
		Decisions: d.harness.runs.Triage(), Reruns: d.harness.runs.Reruns(), Manager: d.judge, Clock: d.clock,
		MayStart: func(start maystart.Start) maystart.Decision {
			d.asks++
			return d.ask(start)
		},
	}
}

func (d *epicDeliveries) pass() EscalationSweep {
	d.t.Helper()
	sweep, err := d.escalator().Escalate(context.Background())
	if err != nil {
		d.t.Fatal(err)
	}
	return sweep
}

const promptedDelivery = "development-manager  prompted-pass "

// With every epic ended nothing is sent and nothing of the run is written, each
// stopped run is counted once however often it is tried and by however many
// escalators, and each is delivered once when one may be.
func TestARefusedDeliveryStaysDueAndIsMadeOnce(t *testing.T) {
	t.Parallel()
	for name, permit := range map[string]func(d *epicDeliveries){
		"an epic opens":           func(d *epicDeliveries) { d.name("yoyodyne-again") },
		"epic mode is turned off": func(d *epicDeliveries) { d.off() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := newEpicDeliveries(t)
			before := snapshot(t, d.harness.runsRoot)
			for range 5 {
				sweep := d.pass()
				if len(sweep.Escalated) != 0 || len(sweep.Waiting) != 1 || !strings.Contains(sweep.Render(), "still due: every epic the operator named has ended") {
					t.Fatalf("pass = %s, want the delivery refused and still due", sweep.Render())
				}
				d.clock.now = d.clock.now.Add(time.Hour)
			}
			if after := snapshot(t, d.harness.runsRoot); !reflect.DeepEqual(after, before) || len(d.judge.shown) != 0 {
				t.Fatalf("a refusal changed what is stored or was sent to her: %d sent", len(d.judge.shown))
			}
			d.wants(promptedDelivery + "1")
			d.stops("run-fedcba9876543210fedcba9876543210", "yoyodyne-second")
			for range 3 {
				if sweep := d.pass(); len(sweep.Waiting) != 2 {
					t.Fatalf("pass = %s, want both still due", sweep.Render())
				}
			}
			d.wants(promptedDelivery + "2")

			permit(d)
			for range 3 {
				if sweep := d.pass(); len(sweep.Waiting) != 0 {
					t.Fatalf("pass = %s, want none refused", sweep.Render())
				}
			}
			if len(d.judge.shown) != 2 || d.judge.shown[0].RunID == d.judge.shown[1].RunID {
				t.Fatalf("shown %d, want each delivered once", len(d.judge.shown))
			}
			d.wants()
		})
	}
}

// With an epic open, with the mode off, and with no decision wired, a delivery is
// made as it was, and asks once where it asks.
func TestADeliveryThatMayStartIsMadeAsBefore(t *testing.T) {
	t.Parallel()
	for name, set := range map[string]func(d *epicDeliveries){
		"an epic open": func(d *epicDeliveries) { d.name("yoyodyne-again") },
		"the mode off": func(d *epicDeliveries) { d.off() },
	} {
		d := newEpicDeliveries(t)
		set(d)
		if sweep := d.pass(); len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || len(d.judge.shown) != 1 || d.asks != 1 {
			t.Fatalf("%s: pass = %#v after asking %d times, want it delivered", name, sweep, d.asks)
		}
	}
	d := newEpicDeliveries(t)
	escalator := d.escalator()
	escalator.MayStart = nil
	if sweep, err := escalator.Escalate(context.Background()); err != nil || len(sweep.Escalated) != 1 {
		t.Fatalf("with no decision wired: %#v, %v", sweep, err)
	}
}

// A decision that could not be made is a refusal.
func TestADeliveryThatCouldNotBeJudgedIsRefused(t *testing.T) {
	t.Parallel()
	d := newEpicDeliveries(t)
	escalator := d.escalator()
	escalator.MayStart = func(maystart.Start) maystart.Decision {
		return maystart.Decision{Answer: maystart.CouldNotTell, Why: "the record could not be read"}
	}
	sweep, err := escalator.Escalate(context.Background())
	if err != nil || len(sweep.Waiting) != 1 || len(d.judge.shown) != 0 || !strings.Contains(sweep.Render(), "the record could not be read") {
		t.Fatalf("pass = %s, %v; want it refused saying why", sweep.Render(), err)
	}
}

// A turn already started finishes, and its reply is recorded without asking,
// though the last epic ends under it.
func TestAReplyAlreadyReceivedIsRecordedWithoutAsking(t *testing.T) {
	t.Parallel()
	d := newEpicDeliveries(t)
	d.name("yoyodyne-again")
	d.judge.killTurn = func() {
		if _, err := d.operator.End("yoyodyne-again", "", baseTime); err != nil {
			t.Error(err)
		}
	}
	if sweep := d.pass(); len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || d.asks != 1 {
		t.Fatalf("pass = %#v after asking %d times, want the reply recorded", sweep, d.asks)
	}
	recorded, err := d.harness.runs.Escalations().List()
	if err != nil || len(recorded) != 1 || !recorded[0].Delivered() || recorded[0].Decision != "wait" {
		t.Fatalf("recorded = %+v, %v; want her decision", recorded, err)
	}
	d.clock.now = d.clock.now.Add(24 * time.Hour)
	if sweep := d.pass(); len(sweep.Waiting) != 0 || len(d.judge.shown) != 1 || d.asks != 1 {
		t.Fatalf("a later pass = %#v after asking %d times, want nothing due", sweep, d.asks)
	}
}

// A restart during a turn leaves the attempt recorded with no reply. The turn is
// asked about again before it is begun again, and a refusal spends nothing more.
func TestATurnARestartEndedIsAskedAboutAgain(t *testing.T) {
	t.Parallel()
	d := newEpicDeliveries(t)
	d.name("yoyodyne-again")
	d.judge.killTurn = func() {
		if _, err := d.operator.End("yoyodyne-again", "", baseTime); err != nil {
			t.Error(err)
		}
		panic("the process ended")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the turn was not ended")
			}
		}()
		d.pass()
	}()
	d.judge.killTurn = nil
	d.clock.now = d.clock.now.Add(runstate.EscalationRetryDelay + time.Minute)
	before := snapshot(t, d.harness.runsRoot)
	if sweep := d.pass(); len(sweep.Waiting) != 1 || len(d.judge.shown) != 1 || d.asks != 2 {
		t.Fatalf("pass after the restart = %#v after asking %d times, want it asked again and refused", sweep, d.asks)
	}
	if after := snapshot(t, d.harness.runsRoot); !reflect.DeepEqual(after, before) {
		t.Fatal("the refusal changed what is stored")
	}
	d.wants(promptedDelivery + "1")
	d.off()
	if sweep := d.pass(); len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || d.asks != 3 {
		t.Fatalf("pass once allowed = %#v after asking %d times, want it delivered", sweep, d.asks)
	}
	d.wants()
}

// In the pull that refuses a delivery, a change in the merge queue lands, and the
// pull says the delivery waits without calling it a problem.
func TestARefusedDeliveryDoesNotStopAChangeInTheMergeQueue(t *testing.T) {
	t.Parallel()
	d := newEpicDeliveries(t)
	r := newQueueRun(t, false)
	r.pipeline.Config.Execution.MergeQueue = true
	if outcome := r.run(t); outcome.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the change admitted", outcome)
	}
	r.pipeline.MayStart = d.ask
	harness := newScheduleHarness()
	polls := 0
	harness.onSleep = func(*scheduleHarness, int) bool { polls++; return polls < 3 }
	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.MayStart, pull.Escalations, pull.MergeQueues = d.ask, d.escalator(), r.driver()
		return pull, err
	}, Watching: true, Sleep: harness.sleep, besideTurns: harness.beside, Now: harness.clock}.Schedule(context.Background())
	if err != nil || len(schedule.EscalationsWaiting) != 1 || schedule.EscalationProblem != "" || !strings.Contains(schedule.Render(), "is still due") {
		t.Fatalf("schedule = %s, %v; want the delivery said to wait", schedule.Render(), err)
	}
	landed := false
	for _, pass := range schedule.QueuePasses {
		landed = landed || pass.Pass.Outcome == runstate.MergeQueuePassLanded
	}
	if !landed || len(d.judge.shown) != 0 {
		t.Fatalf("queue passes = %+v, sent %d; want the change landed and nothing sent", schedule.QueuePasses, len(d.judge.shown))
	}
}
