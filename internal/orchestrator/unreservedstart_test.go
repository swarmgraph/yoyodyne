package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// unreservedRefusal is the clause a decision is passed over with while a start
// of this session's on the same item has not reserved yet.
const unreservedRefusal = "had already started a run of the item that had not yet reserved"

// A start this session made that came back without ever reserving is no longer
// one that stands over its item, however the session's waits fell since it
// came back. Here the first attempt at a decided re-run is still going when the
// session's wait wakes on its poll interval, the store then fails to answer for
// one pull, and the attempt comes back refused while the session waits that
// out. Neither wait collected it, so until a start's ending was noted as its
// goroutine finished, the next pull still counted it as a start in progress and
// passed the decided re-run over for it.
func TestADecidedRerunIsNotRefusedForAnEarlierStartThatEndedWithoutReserving(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 1
	// The session's wait on its own start wakes on this interval, in real time:
	// long enough that the second attempt, which returns at once, is collected
	// before the pull after it rather than read there as still going.
	harness.poll = 250 * time.Millisecond
	fired := map[string]bool{}
	harness.outstanding = outstandingUntilFired(fired, decidedTask("yoyodyne-ifd.346"))

	release := make(chan struct{})
	cameBack := make(chan struct{})
	attempts := 0
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		h.mu.Lock()
		attempts++
		attempt := attempts
		h.mu.Unlock()
		if attempt == 1 {
			// The first attempt is still on its way to reserving through the first
			// pull's wait, and is then refused before it reserves anything.
			<-release
			defer close(cameBack)
			return CarriedOut{
				WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
				Gate:    runstate.TriageGateHarness,
				Problem: "the GitHub command-line tool is not logged in",
			}, Outcome{}, nil
		}
		h.mu.Lock()
		fired[task.WorkItemID] = true
		h.mu.Unlock()
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	// The second pull cannot be opened, so the session waits that out rather
	// than waiting on its start.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.mu.Lock()
			h.openErr = errors.New("the store is busy")
			h.mu.Unlock()
		}
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			// The first attempt comes back while the session waits out the store,
			// and is left handing its ending back to a session not reading it.
			h.mu.Lock()
			h.openErr = nil
			h.mu.Unlock()
			close(release)
			<-cameBack
			time.Sleep(100 * time.Millisecond)
			return true
		}
		return false
	}

	schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 4}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	// The first pull fired the first attempt, the second could not be opened,
	// and the third is the first that read the first attempt as come back. What
	// those pulls passed over is the whole question: a pull after the second
	// attempt was fired may rightly pass the decision over for that attempt,
	// which may not have run yet.
	for pull, passed := range harness.passedOver[:min(2, len(harness.passedOver))] {
		for run, why := range passed {
			if strings.Contains(why, unreservedRefusal) {
				t.Fatalf("account %d passed run %s's decided re-run over for a start that had already come back: %q", pull, run, why)
			}
		}
	}
	if len(harness.passedOver) < 2 || len(schedule.Started) != 2 || !fired["yoyodyne-ifd.346"] {
		t.Fatalf("accounts = %d, fired = %v; want the refused attempt and then the decided re-run carried out, two starts in all: %s", len(harness.passedOver), fired, schedule.Render())
	}
	carried := false
	for _, out := range schedule.CarriedOut {
		carried = carried || out.Carried
	}
	if !carried {
		t.Fatalf("carried out = %#v, want the decided re-run carried out once the first attempt came back", schedule.CarriedOut)
	}
}

// A start this session made that is still on its way to reserving does stand
// over its item: a decided re-run of the same item is passed over for it, and
// the reason says which start it is and how long ago it began.
func TestADecidedRerunWaitsForAnEarlierStartStillOnItsWayToReserving(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	harness.poll = 10 * time.Millisecond
	first, second := decidedTaskOf("yoyodyne-ifd.346", "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), decidedTaskOf("yoyodyne-ifd.346", "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	fired := map[string]bool{}
	offered := 0
	harness.outstanding = func(h *scheduleHarness) ([]CarryOutTask, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		offered++
		if fired[second.RunID] {
			return nil, nil
		}
		// The first decision is offered on the first pull only; the second is
		// offered from the next pull on, while the first's start is still going.
		if offered == 1 {
			return []CarryOutTask{first}, nil
		}
		return []CarryOutTask{second}, nil
	}
	release := make(chan struct{})
	passedOnce := make(chan struct{})
	accounts := make(chan struct{}, 1)
	harness.unattempted = accounts
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		if task.RunID == first.RunID {
			<-release
		}
		h.mu.Lock()
		fired[task.RunID] = true
		h.mu.Unlock()
		return CarriedOut{WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision, Carried: true, Reason: rerunReasoning},
			Outcome{WorkItemID: task.WorkItemID, Status: runstate.StatusSucceeded}, nil
	}
	go func() {
		// Once a pull has passed the second decision over, the first start is let
		// finish, so the session can go on to carry the second out.
		defer close(release)
		deadline := time.After(time.Minute)
		for {
			select {
			case <-accounts:
				harness.mu.Lock()
				passed := false
				for _, pull := range harness.passedOver {
					if _, ok := pull[second.RunID]; ok {
						passed = true
					}
				}
				harness.mu.Unlock()
				if passed {
					close(passedOnce)
					return
				}
			case <-deadline:
				return
			}
		}
	}()
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 4}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	select {
	case <-passedOnce:
	default:
		t.Fatalf("passed over = %#v; want the second decision passed over while the first start was going", harness.passedOver)
	}
	harness.mu.Lock()
	defer harness.mu.Unlock()
	var why string
	for _, pull := range harness.passedOver {
		if said, ok := pull[second.RunID]; ok {
			why = said
			break
		}
	}
	for _, want := range []string{unreservedRefusal, "has not come back yet", "ago", first.RunID, "one item is never given two runs at once"} {
		if !strings.Contains(why, want) {
			t.Fatalf("why the second decision waited = %q, want it to say %q", why, want)
		}
	}
	if !fired[second.RunID] {
		t.Fatalf("fired = %v; want the second decision carried out once the first start had ended", fired)
	}
}
