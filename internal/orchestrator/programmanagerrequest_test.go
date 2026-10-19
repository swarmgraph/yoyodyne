package orchestrator

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// onRequestOnly is an instance whose triggers name no schedule and no events:
// the technical-health program manager's shape, which runs only when asked.
func onRequestOnly() map[string]config.AgentConfig {
	return instance(0)
}

func withRequests(t *testing.T, h *passHarness) *runstate.PassRequestStore {
	t.Helper()
	requests, err := runstate.NewPassRequestStore(h.root, "example")
	if err != nil {
		t.Fatalf("NewPassRequestStore() error = %v", err)
	}
	h.trigger.Requests = requests
	return requests
}

func askForPass(t *testing.T, requests *runstate.PassRequestStore, by runstate.PassAsker, at time.Time, focus runstate.PassFocus) runstate.PassRequest {
	t.Helper()
	id, err := runstate.NewPassRequestID()
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := requests.Request(runstate.PassRequest{
		SchemaVersion: runstate.PassRequestSchemaVersion,
		ProductID:     "example",
		ID:            id,
		Agent:         instanceName,
		AskedBy:       by,
		RequestedAt:   at,
		Focus:         focus,
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	return recorded
}

func requestNamed(t *testing.T, requests *runstate.PassRequestStore, id string) runstate.PassRequest {
	t.Helper()
	listed, err := requests.List(instanceName)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, request := range listed {
		if request.ID == id {
			return request
		}
	}
	t.Fatalf("no request %s in %+v", id, listed)
	return runstate.PassRequest{}
}

// An instance with no schedule and no events is never woken by a timer or by
// work landing, however long passes and however much lands; nothing is claimed
// and nothing owes it a pass.
func TestAnInstanceWithNoTriggersPassesOnNoTimerAndNoLanding(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	withRequests(t, h)
	h.role.answers = []scriptedTurn{{result: complete("should never be asked")}}

	for _, at := range []time.Duration{0, time.Hour, 45 * time.Minute * 4, 48 * time.Hour} {
		h.events.land(recurringNow.Add(at), "yoyodyne-ifd.1")
		h.events.admit(recurringNow.Add(at), 3)
		if fired := h.fire(t, recurringNow.Add(at+5*time.Minute)); len(fired.Fired) != 0 {
			t.Fatalf("fired = %+v at +%s, want nothing: the instance runs only when asked", fired.Fired, at)
		}
	}
	if len(h.role.messages) != 0 {
		t.Errorf("messages = %v, want the instance never woken", h.role.messages)
	}
	if _, found, err := h.sweeps.Find(instanceName); err != nil || found {
		t.Errorf("Find() = %v, %v, want no pass ever claimed", found, err)
	}
	dues, err := h.trigger.Cadence(t.Context())
	if err != nil || len(dues) != 0 {
		t.Errorf("Cadence() = %+v, %v, want nothing owed to an instance that runs only when asked", dues, err)
	}
}

// The operator's request is taken at the next pull: the instance is woken and
// told who asked and what to look at, the record names the request and who
// made it, the request is closed by that pass, and nothing follows it.
func TestARequestedPassIsTakenRecordedWithWhoAskedAndClosed(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	h.role.answers = []scriptedTurn{{result: complete("inspected the tests"), model: "gpt-6-astra"}}

	focus, err := runstate.NewPassFocus("test quality", nil)
	if err != nil {
		t.Fatal(err)
	}
	asked := askForPass(t, requests, runstate.PassAskerOperator, recurringNow.Add(-time.Minute), focus)

	fired := h.fire(t, recurringNow)
	if len(fired.Fired) != 1 || fired.Fired[0].Task != instanceName {
		t.Fatalf("fired = %+v, want the requested pass", fired.Fired)
	}
	message := h.role.messages[0]
	for _, want := range []string{"the operator asked for this pass", asked.ID, `the area "test quality"`, "pass only when the operator or the Lead Product Manager asks"} {
		if !strings.Contains(message, want) {
			t.Errorf("message = %q, want it to say %q", message, want)
		}
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 || recorded[0].Request != asked.ID || recorded[0].AskedBy != runstate.PassAskerOperator || recorded[0].Model != "gpt-6-astra" {
		t.Fatalf("recorded = %+v, want the pass naming the request and who asked", recorded)
	}
	closed := requestNamed(t, requests, asked.ID)
	if closed.Open() || closed.Outcome != runstate.PassRequestCompleted || closed.Pass != instanceName+"#1" {
		t.Fatalf("request = %+v, want it closed completed by the pass that answered it", closed)
	}
	if closed.TakenAt == nil || !closed.TakenAt.Equal(recurringNow) {
		t.Errorf("request = %+v, want it recorded as taken when the pass was claimed", closed)
	}
	if state, since := closed.State(); state != runstate.PassRequestStateCompleted || since.IsZero() {
		t.Errorf("State() = %q, %s, want completed with when", state, since)
	}
	if again := h.fire(t, recurringNow.Add(time.Hour)); len(again.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing once the request is answered", again.Fired)
	}
}

// The Lead Product Manager's request is taken the same way and recorded as
// hers; a pass that fails closes the request as failed, so whoever asked sees
// it and may ask again, and the failure is not retried pull after pull.
func TestALeadProductManagersRequestThatFailsIsClosedFailed(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	h.role.answers = []scriptedTurn{{err: errors.New("the provider hung up")}}
	asked := askForPass(t, requests, runstate.PassAskerProductManager, recurringNow, runstate.PassFocus{})

	fired := h.fire(t, recurringNow)
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the requested pass taken", fired.Fired)
	}
	if !strings.Contains(h.role.messages[0], "the Lead Product Manager asked for this pass") || !strings.Contains(h.role.messages[0], "next area your remit's rotation names") {
		t.Errorf("message = %q, want who asked and the rotation named", h.role.messages[0])
	}
	recorded := h.recorded(t)
	if len(recorded) != 1 || recorded[0].AskedBy != runstate.PassAskerProductManager {
		t.Fatalf("recorded = %+v, want the pass recorded as the Lead Product Manager's", recorded)
	}
	closed := requestNamed(t, requests, asked.ID)
	if closed.Open() || closed.Outcome != runstate.PassRequestFailed || closed.Problem == "" {
		t.Fatalf("request = %+v, want it closed failed with what went wrong", closed)
	}
	if again := h.fire(t, recurringNow.Add(time.Hour)); len(again.Fired) != 0 {
		t.Errorf("fired = %+v, want a failed request left for whoever asked to ask again", again.Fired)
	}
}

// Two requests made while the first is open are one pass: the later ones are
// recorded as joined, their focus is added to the open one's, and the one pass
// answers all of them.
func TestRequestsMadeWhileOneIsOpenJoinItAndOnePassAnswersThem(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	h.role.answers = []scriptedTurn{{result: complete("answered both")}}

	first := askForPass(t, requests, runstate.PassAskerOperator, recurringNow.Add(-2*time.Minute), runstate.PassFocus{})
	paths, err := runstate.NewPassFocus("", []string{"internal/chat/chat.go", "./internal/orchestrator/pipeline.go"})
	if err != nil {
		t.Fatal(err)
	}
	second := askForPass(t, requests, runstate.PassAskerProductManager, recurringNow.Add(-time.Minute), paths)
	if second.JoinedTo != first.ID {
		t.Fatalf("second = %+v, want it joined to the open request %s", second, first.ID)
	}

	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want one pass for both requests", fired.Fired)
	}
	if message := h.role.messages[0]; !strings.Contains(message, "internal/orchestrator/pipeline.go") || !strings.Contains(message, second.ID) {
		t.Errorf("message = %q, want the joined request's paths and identifier", message)
	}
	if closed := requestNamed(t, requests, first.ID); closed.Open() || closed.Outcome != runstate.PassRequestCompleted {
		t.Errorf("first = %+v, want it closed by the pass", closed)
	}
	if joined := requestNamed(t, requests, second.ID); joined.Open() || joined.JoinedTo != first.ID {
		t.Errorf("second = %+v, want it answered through the request it joined", joined)
	}
}

// A request made while the provider answers nobody waits, with that recorded on
// it once rather than once a pull, and is taken once the provider answers.
func TestARequestWaitsOutAProviderAnsweringNobody(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	outages, err := runstate.NewProviderOutageStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnreachable, Waiting: "a pass", At: recurringNow}); err != nil {
		t.Fatal(err)
	}
	h.trigger.Outages = outages
	h.trigger.OutageProbe = time.Hour
	asked := askForPass(t, requests, runstate.PassAskerOperator, recurringNow, runstate.PassFocus{})

	for _, at := range []time.Duration{time.Minute, 2 * time.Minute} {
		if fired := h.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired = %+v, want the request held while the provider answers nobody", fired.Fired)
		}
	}
	waiting := requestNamed(t, requests, asked.ID)
	if !waiting.Open() || !strings.Contains(waiting.Waiting, "answered nobody") || !waiting.WaitingSince.Equal(recurringNow.Add(time.Minute)) {
		t.Fatalf("request = %+v, want it open and waiting on the provider since the first pull that tried", waiting)
	}
	if len(h.role.messages) != 0 {
		t.Fatalf("messages = %v, want nothing asked into the outage", h.role.messages)
	}

	if _, _, err := outages.Clear(); err != nil {
		t.Fatal(err)
	}
	h.role.answers = []scriptedTurn{{result: complete("back")}}
	if fired := h.fire(t, recurringNow.Add(4*time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the request taken once the provider answers", fired.Fired)
	}
	if closed := requestNamed(t, requests, asked.ID); closed.Open() || closed.Waiting != "" {
		t.Errorf("request = %+v, want it closed with its wait cleared", closed)
	}
}

// An instance with a schedule may be asked for a pass too: the request is taken
// at once rather than at the next scheduled pass.
func TestAScheduledInstanceMayBeAskedForAPassBetweenItsPasses(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, instance(2*time.Hour))
	requests := withRequests(t, h)
	h.role.answers = []scriptedTurn{{result: complete("scheduled")}, {result: complete("asked")}}
	if fired := h.fire(t, recurringNow); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the first scheduled pass", fired.Fired)
	}
	asked := askForPass(t, requests, runstate.PassAskerOperator, recurringNow.Add(10*time.Minute), runstate.PassFocus{})
	fired := h.fire(t, recurringNow.Add(11*time.Minute))
	if len(fired.Fired) != 1 || !strings.Contains(h.role.messages[1], asked.ID) {
		t.Fatalf("fired = %+v, want the request taken between scheduled passes", fired.Fired)
	}
}

// restart is the scheduler starting again over the same state: every store
// opened afresh, nothing carried over in memory but the provider it asks.
func (h *passHarness) restart(t *testing.T) *runstate.PassRequestStore {
	t.Helper()
	sweeps, err := runstate.NewSweepStore(h.root, "example")
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := runstate.NewPassCursorStore(h.root, "example")
	if err != nil {
		t.Fatal(err)
	}
	requests, err := runstate.NewPassRequestStore(h.root, "example")
	if err != nil {
		t.Fatal(err)
	}
	restarted := h.trigger
	restarted.Claims, restarted.Reports, restarted.Cursors, restarted.Requests = sweeps, sweeps, cursors, requests
	h.trigger, h.sweeps, h.cursors = restarted, sweeps, cursors
	return requests
}

// A request still waiting when the scheduler restarts is waiting afterwards,
// is taken by the restarted scheduler at its next pull, and is taken once.
func TestARequestWaitingAcrossASchedulerRestartIsTakenOnce(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	outages, err := runstate.NewProviderOutageStore(t.TempDir(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outages.Notice(runstate.ProviderOutageObservation{Cause: domain.ProviderUnreachable, Waiting: "a pass", At: recurringNow}); err != nil {
		t.Fatal(err)
	}
	h.trigger.Outages = outages
	h.trigger.OutageProbe = time.Hour
	asked := askForPass(t, requests, runstate.PassAskerProductManager, recurringNow, runstate.PassFocus{})
	if fired := h.fire(t, recurringNow.Add(time.Minute)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want the request held while the provider answers nobody", fired.Fired)
	}

	requests = h.restart(t)
	if waiting := requestNamed(t, requests, asked.ID); !waiting.Open() {
		t.Fatalf("request = %+v, want it still waiting after the restart", waiting)
	} else if state, _ := waiting.State(); state != runstate.PassRequestStateWaiting {
		t.Fatalf("State() = %q, want waiting", state)
	}
	if _, _, err := outages.Clear(); err != nil {
		t.Fatal(err)
	}
	h.role.answers = []scriptedTurn{{result: complete("taken after the restart")}, {result: complete("should never be asked")}}
	if fired := h.fire(t, recurringNow.Add(10*time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the restarted scheduler to take the request", fired.Fired)
	}
	for _, at := range []time.Duration{20 * time.Minute, 2 * time.Hour} {
		if fired := h.fire(t, recurringNow.Add(at)); len(fired.Fired) != 0 {
			t.Fatalf("fired = %+v at +%s, want the request taken once", fired.Fired, at)
		}
	}
	if len(h.role.messages) != 1 || !strings.Contains(h.role.messages[0], asked.ID) {
		t.Errorf("messages = %v, want one pass taken from the request", h.role.messages)
	}
	if closed := requestNamed(t, requests, asked.ID); closed.Open() || closed.Outcome != runstate.PassRequestCompleted {
		t.Errorf("request = %+v, want it closed by the one pass", closed)
	}
}

// A request whose pass the scheduler had taken when it stopped is taken again
// once by the scheduler that replaces it, since nothing is carrying it; while a
// turn is in flight on the instance's conversation, another process is, and it
// is not taken a second time.
func TestARequestTakenByAStoppedSchedulerIsTakenAgainOnce(t *testing.T) {
	t.Parallel()

	h := newPassHarness(t, onRequestOnly())
	requests := withRequests(t, h)
	asked := askForPass(t, requests, runstate.PassAskerOperator, recurringNow, runstate.PassFocus{})
	// The stopped scheduler claimed the pass and recorded the request taken,
	// and stopped before the pass ended.
	claimed, err := h.sweeps.Summon(t.Context(), instanceName, recurringNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := requests.Take(instanceName, asked.ID, passName(claimed), recurringNow); err != nil {
		t.Fatal(err)
	}

	requests = h.restart(t)
	carrying := busyConversations{instanceName: true}
	h.trigger.Conversations = carrying
	if fired := h.fire(t, recurringNow.Add(time.Minute)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want a request another process is carrying left to it", fired.Fired)
	}
	if taken := requestNamed(t, requests, asked.ID); taken.Pass != instanceName+"#1" {
		t.Fatalf("request = %+v, want it still taken by the first pass", taken)
	}

	carrying[instanceName] = false
	h.role.answers = []scriptedTurn{{result: complete("taken again")}, {result: complete("should never be asked")}}
	if fired := h.fire(t, recurringNow.Add(2*time.Minute)); len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the request taken again once nothing carries it", fired.Fired)
	}
	if fired := h.fire(t, recurringNow.Add(time.Hour)); len(fired.Fired) != 0 {
		t.Fatalf("fired = %+v, want it taken once", fired.Fired)
	}
	closed := requestNamed(t, requests, asked.ID)
	if closed.Open() || closed.Pass != instanceName+"#2" || closed.Outcome != runstate.PassRequestCompleted {
		t.Errorf("request = %+v, want it closed by the second pass", closed)
	}
}
