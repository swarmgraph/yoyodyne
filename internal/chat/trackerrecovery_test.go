package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// killedWrite is what internal/beads formats for a bd the harness stopped
// waiting on: the status and the exit code a killed process reports, with the
// colon the recovery rule matches on. It is the exact string the 2026-09-06
// write on yoyodyne-ifd.142 failed with, message and all.
const killedWrite = "bd update failed with status timed_out and exit code -1: "

// contendedTracker fails the first few calls of the named verbs the way a
// contended store does and then answers, which is the shape the recovery rule
// exists for: a failure the next attempt survives. A verb not named answers at
// once, so the waits a test counts are the contended verbs' own, and every call
// is counted by verb whether it answered or not.
type contendedTracker struct {
	*fakeTracker
	// verbs are the calls that fail; none named means every one of them does.
	verbs    []string
	failures int
	failure  error
	calls    map[string]int
}

func (c *contendedTracker) contended(verb string) error {
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	if len(c.verbs) > 0 && !slices.Contains(c.verbs, verb) {
		return nil
	}
	c.calls[verb]++
	if c.calls[verb] <= c.failures {
		return c.failure
	}
	return nil
}

func (c *contendedTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	if err := c.contended("show"); err != nil {
		return beads.WorkItem{}, err
	}
	return c.fakeTracker.Show(ctx, id)
}

func (c *contendedTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	if err := c.contended("list"); err != nil {
		return nil, err
	}
	return c.fakeTracker.List(ctx, status)
}

func (c *contendedTracker) Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if err := c.contended("update"); err != nil {
		return beads.WorkItem{}, err
	}
	return c.fakeTracker.Update(ctx, id, change)
}

// recordingSleep drives a wait without spending it and keeps what it was asked
// for, so the backoff is an assertion rather than a claim.
func recordingSleep(sleeps *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, duration time.Duration) error {
		*sleeps = append(*sleeps, duration)
		return nil
	}
}

// The rule itself, on the path that lost yoyodyne-ifd.142's re-run recording: a
// conversation's write that fails the way a killed bd does is waited out on the
// pipeline's backoff and asked again, and the action is reported as applied with
// the waits named, rather than reported as failed on the first reset.
func TestAConversationTrackerWriteThatFailsRecoverablyIsAskedAgainOnTheBackoff(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose write was contended", Status: "open"},
		}},
		// The read that precedes the write answers; the write itself fails three
		// times and then lands.
		verbs:    []string{"update"},
		failures: 3,
		failure:  errors.New(killedWrite),
	}
	var sleeps []time.Duration
	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"the store was contended","reason":"so the item says so"}`)},
		{SessionID: "session-1", FinalText: "The note is on it."},
	}})
	options.Tracker = tracker
	options.Store = newTestStore(t, root)
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the write applied once the store answered", reply.Actions)
	}
	// Three failures, three waits on the operator's series, and the fourth
	// attempt landed: the store saw four writes and kept one.
	if want := []time.Duration{time.Second, time.Second, 2 * time.Second}; !equalDurations(sleeps, want) {
		t.Fatalf("waits = %v, want the Fibonacci series %v", sleeps, want)
	}
	if tracker.calls["update"] != 4 {
		t.Fatalf("writes = %d, want the write asked for four times", tracker.calls["update"])
	}
	// The operator is told the store had to be asked again, in the action's own
	// line, because a store that had to be asked three times is one they should
	// hear about before it has to be asked twenty.
	if !strings.Contains(reply.Actions[0].Summary, "asked 3 further time(s) over 4s first") {
		t.Fatalf("summary = %q, want the waits named on the applied action", reply.Actions[0].Summary)
	}
	// And every wait is on the conversation's record, written before it was
	// taken, with the attempt, the interval, and the failure in bd's own words.
	var retried []map[string]any
	for _, event := range loadTestEvents(t, root, session) {
		if event.Type != execution.EventTrackerRetried {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Type, err)
		}
		retried = append(retried, payload)
	}
	if len(retried) != 3 {
		t.Fatalf("tracker.retried events = %d, want one per wait", len(retried))
	}
	for i, payload := range retried {
		if payload["attempt"] != float64(i+1) || payload["boundary"] != runstate.RetryTracker ||
			payload["delay_seconds"] != float64(recovery.Interval(i+1)/time.Second) ||
			!strings.Contains(payload["failure"].(string), "timed_out") {
			t.Fatalf("tracker.retried event %d = %#v", i+1, payload)
		}
	}
}

// A read that gates a write is retried the same way, and the write it gates
// goes ahead once the read answers: the duplicate check that failed open on
// 2026-09-18 is asked again rather than skipped.
func TestAConversationTrackerReadThatFailsRecoverablyIsAskedAgain(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{},
		verbs:       []string{"list"},
		failures:    2,
		failure:     errors.New("bd list failed with status timed_out and exit code -1: "),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","kind":"feature","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.created) != 1 {
		t.Fatalf("actions = %#v, created = %#v, want the admission made once the listing answered", reply.Actions, tracker.created)
	}
	if tracker.calls["list"] != 3 || len(sleeps) != 2 {
		t.Fatalf("listings = %d, waits = %v, want the listing asked for again after each wait", tracker.calls["list"], sleeps)
	}
}

// The window is the pipeline's, and spending it is the only thing that produces
// the failure the write would have produced at once before. What is reported
// then is what yoyodyne-ifd.327 made honest — the spend that stands, the write
// nobody can confirm — with the attempts and the time in front of it, which is
// exactly the order the item asked for: reported per 327 only after the retries
// are spent.
func TestATrackerWriteThatSpendsTheWindowIsReportedWithTheAttemptsInFront(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	// The store never takes the write. One that took it would be found by the
	// read that precedes the next attempt and reported as landed, which is the
	// other half of this rule and has tests of its own below.
	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose re-run was reported as unrecorded", Status: "open"},
		}},
		verbs:    []string{"update"},
		failures: 1 << 20,
		failure:  errors.New(killedWrite),
	}
	var sleeps []time.Duration
	options := triageOptions(t, tracker, budgets, trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`))
	options.Reports = &fakeReports{}
	options.Sleep = recordingSleep(&sleeps)
	reply := triageSend(t, options)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	// The whole window was waited out before anything was reported, on the
	// series the operator asked for, and not a second past it.
	var waited time.Duration
	for i, wait := range sleeps {
		if wait != recovery.Interval(i+1) {
			t.Fatalf("wait %d = %s, want %s", i+1, wait, recovery.Interval(i+1))
		}
		waited += wait
	}
	if waited > recovery.Window || waited+recovery.Interval(len(sleeps)+1) <= recovery.Window {
		t.Fatalf("waited %s over %d waits, want the whole of the %s window and no more", waited, len(sleeps), recovery.Window)
	}
	if tracker.calls["update"] != len(sleeps)+1 {
		t.Fatalf("updates = %d, want one write per wait and one more", tracker.calls["update"])
	}
	// Then the report is 327's, with the retries in front of it: not applied, the
	// spend named as standing, and the failure saying how long it was waited out.
	if outcome.Applied || !outcome.PartlyLanded() {
		t.Fatalf("outcome = %#v, want a failure with the spend standing behind it", outcome)
	}
	for _, want := range []string{"reaching the tracker kept failing", "did not outlast it", "timed_out"} {
		if !strings.Contains(outcome.Failure, want) {
			t.Fatalf("failure = %q, want it to say %q", outcome.Failure, want)
		}
	}
	rendered := renderTrackerOutcomes(options.Role, reply.Actions)
	if strings.Contains(rendered, "changed nothing") {
		t.Fatalf("a durable spend was reported as having changed nothing:\n%s", rendered)
	}
	for _, want := range []string{
		"did not finish, and part of it stands",
		"the re-run is spent against yoyodyne-ifd.142's durable budget",
		"not known to have landed: the decision recorded on the item",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the account is missing %q:\n%s", want, rendered)
		}
	}
}

// One window for the message, not one per call. The settling read a failed
// write is followed by runs under a context nothing can cancel, so it has to
// find the window the write spent rather than a fresh two hours to wait out
// uninterruptibly — and every later call in the message is in the same store's
// weather.
func TestTheTrackerWindowIsTheMessagesRatherThanEachCalls(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		},
		err: errors.New(killedWrite),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Two notes.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"first","reason":"r"}`,
			`{"action":"update","id":"yoyodyne-ifd.142","note":"second","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "Neither landed."},
	}})
	options.Tracker = tracker
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it twice")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || reply.Actions[0].Applied || reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want both writes failed", reply.Actions)
	}
	var waited time.Duration
	for _, wait := range sleeps {
		waited += wait
	}
	if waited > recovery.Window {
		t.Fatalf("waited %s across the message, want no more than the one %s window", waited, recovery.Window)
	}
	// The second action took no wait of its own and says so: the window was
	// spent, and its failure names the retries that spent it.
	if !strings.Contains(reply.Actions[1].Failure, "did not outlast it") {
		t.Fatalf("second failure = %q, want the spent window named", reply.Actions[1].Failure)
	}
}

// A failure whose class says the next attempt earns the identical one is not
// waited out: a store that refused the write is an answer about the write, and
// the wait would turn it into two hours of the same answer.
func TestATrackerFailureNoLaterAttemptCouldSurviveIsNotRetried(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		}},
		verbs:    []string{"update"},
		failures: 100,
		failure:  errors.New("bd update failed with status failed and exit code 1: the item is locked"),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"n","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It refused."},
	}})
	options.Tracker = tracker
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the refusal reported", reply.Actions)
	}
	if len(sleeps) != 0 || tracker.calls["update"] != 1 {
		t.Fatalf("waits = %v, writes = %d, want the refusal taken as the answer", sleeps, tracker.calls["update"])
	}
	if strings.Contains(reply.Actions[0].Failure, "retr") {
		t.Fatalf("failure = %q, want no retry claimed where none was taken", reply.Actions[0].Failure)
	}
}

// stoppedStoreTracker is a store that never takes the write and, once the
// operator has stopped the turn, will not answer a read either. It is the shape
// the settling read meets: the write timed out, the turn was stopped during the
// wait, and the read that would settle what the write left behind runs under a
// context nothing can cancel against a store that is still contended.
type stoppedStoreTracker struct {
	*fakeTracker
	stopped  *bool
	shows    int
	updates  int
	refusals int
}

func (s *stoppedStoreTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	s.shows++
	if *s.stopped {
		s.refusals++
		return beads.WorkItem{}, errors.New("bd show failed with status timed_out and exit code -1: ")
	}
	return s.fakeTracker.Show(ctx, id)
}

func (s *stoppedStoreTracker) Update(context.Context, string, beads.WorkItemChange) (beads.WorkItem, error) {
	s.updates++
	return beads.WorkItem{}, errors.New(killedWrite)
}

// The operator stopping the turn ends the waiting — all of it. The call being
// waited on is left as it failed rather than asked again under a context that
// has ended, and so is every call after it in the message: the settling read a
// failed triage write is followed by runs under a context nothing can cancel,
// and a wait taken there would be one the operator had already stopped and
// could not stop again.
func TestAStoppedTurnEndsTheTrackerWait(t *testing.T) {
	t.Parallel()

	stopped := false
	tracker := &stoppedStoreTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		}},
		stopped: &stopped,
	}
	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleeps := 0
	options := triageOptions(t, tracker, budgets, trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`))
	options.Reports = &fakeReports{}
	options.Sleep = func(ctx context.Context, _ time.Duration) error {
		sleeps++
		// The operator stops the turn during the second wait.
		if sleeps == 2 {
			stopped = true
			cancel()
		}
		return ctx.Err()
	}
	session := openTestSession(t, options)

	reply, _ := session.Send(ctx, "Work the docket.")
	if sleeps != 2 {
		t.Fatalf("waits = %d, want the waiting ended where the turn was stopped, with none taken for the settling read", sleeps)
	}
	if tracker.updates != 2 {
		t.Fatalf("writes = %d, want no write asked for under a context that had ended", tracker.updates)
	}
	// The settling read was still taken — it is what says whether the timed-out
	// write landed — and when the store would not answer it, it was not waited
	// on: the operator had stopped waiting, and that read cannot be stopped.
	if tracker.refusals != 1 {
		t.Fatalf("reads refused after the stop = %d, want the settling read asked once and not waited on", tracker.refusals)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the write left as it failed", reply.Actions)
	}
	outcome := reply.Actions[0]
	// What is reported says the turn was stopped, rather than that a two-hour
	// window ran out: the two ask different things of whoever reads them.
	if !strings.Contains(outcome.Failure, "the turn was stopped while waiting to ask again, after 2 retr(ies)") {
		t.Fatalf("failure = %q, want the stop named rather than a window that ran out", outcome.Failure)
	}
	if strings.Contains(outcome.Failure, "did not outlast it") {
		t.Fatalf("failure = %q, want no claim that the window ran out", outcome.Failure)
	}
	// And 327's account still follows it: the spend stands, and the write is not
	// known to have landed because the read that would say so was not answered.
	if !outcome.PartlyLanded() || len(outcome.Unknown) != 1 ||
		!strings.Contains(outcome.Unknown[0], "the tracker would not say whether the write reached yoyodyne-ifd.142") ||
		!strings.Contains(outcome.Unknown[0], "the turn was stopped while waiting") {
		t.Fatalf("outcome = %#v, want the spend standing and the unsettled write saying the turn was stopped", outcome)
	}
}

// A conversation with no tracker is exactly what it was: the nil checks at every
// call site still answer rather than reaching a wrapper around nothing.
func TestAConversationWithoutATrackerIsNotGivenAWrappedNothing(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"n","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "There is no tracker."},
	}})
	options.Tracker = nil
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !strings.Contains(reply.Actions[0].Failure, "no work tracker is configured") {
		t.Fatalf("actions = %#v, want the plain refusal", reply.Actions)
	}
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// How each tracker operation is asked for again after a failure a later attempt
// could survive. It is a listing rather than something derived, because which
// class a write belongs in is a judgement about what repeating it does — and the
// test below holds it against the Tracker interface, so an operation added there
// fails here until somebody has made that judgement.
var trackerRetryClasses = map[string]string{
	// Reads change nothing, and are asked again as they stand.
	"Show": "read",
	"List": "read",
	// Writes that set something to a value leave the tracker as the first attempt
	// did, and are asked again as they stand.
	"AddBlocker":    "safe to repeat",
	"RemoveBlocker": "safe to repeat",
	// Writes that add something are asked again only once the tracker says the
	// failed attempt did not land: an item under the parent, a note at the end of
	// the item's notes, a status of closed.
	"Create":   "checked before it is asked again",
	"Update":   "checked before it is asked again",
	"Block":    "checked before it is asked again",
	"Unblock":  "checked before it is asked again",
	"Complete": "checked before it is asked again",
}

func TestEveryTrackerOperationIsClassifiedForRetry(t *testing.T) {
	t.Parallel()

	operations := reflect.TypeOf((*Tracker)(nil)).Elem()
	declared := map[string]bool{}
	for i := range operations.NumMethod() {
		name := operations.Method(i).Name
		declared[name] = true
		if _, classified := trackerRetryClasses[name]; !classified {
			t.Errorf("Tracker.%s is not classified: decide whether a retry of it is a read, safe to repeat, or checked for having landed first, "+
				"and make recoveringTracker agree", name)
		}
	}
	for name := range trackerRetryClasses {
		if !declared[name] {
			t.Errorf("%s is classified and Tracker declares no such operation", name)
		}
	}
	// And every operation in a class is driven below as its class says, so the
	// listing is what the wrapper does rather than what somebody said about it.
	for name := range trackerRetryClasses {
		if _, driven := killedOnceDrivers[name]; !driven {
			t.Errorf("Tracker.%s has no driver in killedOnceDrivers, so nothing holds the wrapper to its class", name)
		}
	}
}

// killedOnceTracker takes the first call of one operation and then reports it
// failed the way a bd killed at its timeout does, which is the shape the retry
// has to survive without doubling anything: the store kept the write and the
// caller was told the command failed. Every other call answers, the reads that
// check whether it landed among them.
type killedOnceTracker struct {
	*fakeTracker
	target string
	calls  map[string]int
	killed bool
	reads  int
}

func newKilledOnceTracker(target string) *killedOnceTracker {
	return &killedOnceTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.433": {ID: "yoyodyne-ifd.433", Title: "the parent", Status: "open"},
			"yoyodyne-ifd.7":   {ID: "yoyodyne-ifd.7", Title: "the item written to", Status: "open", Notes: "Admitted long ago."},
		}},
		target: target,
		calls:  map[string]int{},
	}
}

func (k *killedOnceTracker) answer(operation string) error {
	k.calls[operation]++
	if operation != k.target || k.killed {
		return nil
	}
	k.killed = true
	return errors.New(killedWrite)
}

func (k *killedOnceTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	k.reads++
	if err := k.answer("Show"); err != nil {
		return beads.WorkItem{}, err
	}
	return k.fakeTracker.Show(ctx, id)
}

func (k *killedOnceTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	k.reads++
	if err := k.answer("List"); err != nil {
		return nil, err
	}
	return k.fakeTracker.List(ctx, status)
}

func (k *killedOnceTracker) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	created := beads.WorkItem{ID: fmt.Sprintf("yoyodyne-ifd.433.%d", len(k.all)+1), Title: item.Title, Parent: item.Parent, Notes: item.Notes, Status: "open"}
	k.all = append(k.all, created)
	return created, k.answer("Create")
}

func (k *killedOnceTracker) Update(_ context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	k.append(id, change.AppendNotes)
	return k.items[id], k.answer("Update")
}

func (k *killedOnceTracker) Block(_ context.Context, id, reason string) (beads.WorkItem, error) {
	k.setStatus(id, "blocked")
	k.append(id, reason)
	return k.items[id], k.answer("Block")
}

func (k *killedOnceTracker) Unblock(_ context.Context, id, note string) (beads.WorkItem, error) {
	k.setStatus(id, "open")
	k.append(id, note)
	return k.items[id], k.answer("Unblock")
}

func (k *killedOnceTracker) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	k.setStatus(id, "closed")
	return k.items[id], k.answer("Complete")
}

func (k *killedOnceTracker) AddBlocker(_ context.Context, id, blockerID string) error {
	k.links = append(k.links, [2]string{id, blockerID})
	return k.answer("AddBlocker")
}

func (k *killedOnceTracker) RemoveBlocker(_ context.Context, id, blockerID string) error {
	k.unlinks = append(k.unlinks, [2]string{id, blockerID})
	return k.answer("RemoveBlocker")
}

func (k *killedOnceTracker) setStatus(id, status string) {
	item := k.items[id]
	item.Status = status
	k.items[id] = item
}

const killedOnceNote = "Noted by the product manager in conversation chat-433, after turn 3: the store was contended."

// killedOnceDrivers asks for each operation once through the wrapper.
var killedOnceDrivers = map[string]func(context.Context, Tracker) error{
	"Show": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Show(ctx, "yoyodyne-ifd.7")
		return err
	},
	"List": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.List(ctx, "")
		return err
	},
	"Create": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Create(ctx, beads.NewWorkItem{Title: "a child", Parent: "yoyodyne-ifd.433", Notes: killedOnceNote})
		return err
	},
	"Update": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Update(ctx, "yoyodyne-ifd.7", beads.WorkItemChange{AppendNotes: killedOnceNote})
		return err
	},
	"Block": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Block(ctx, "yoyodyne-ifd.7", killedOnceNote)
		return err
	},
	"Unblock": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Unblock(ctx, "yoyodyne-ifd.7", killedOnceNote)
		return err
	},
	"Complete": func(ctx context.Context, tracker Tracker) error {
		_, err := tracker.Complete(ctx, "yoyodyne-ifd.7", killedOnceNote)
		return err
	},
	"AddBlocker": func(ctx context.Context, tracker Tracker) error {
		return tracker.AddBlocker(ctx, "yoyodyne-ifd.7", "yoyodyne-ifd.433")
	},
	"RemoveBlocker": func(ctx context.Context, tracker Tracker) error {
		return tracker.RemoveBlocker(ctx, "yoyodyne-ifd.7", "yoyodyne-ifd.433")
	},
}

// Each operation, killed once after the store took it, is asked for again as its
// class says. A read and a write that is safe to repeat are asked again as they
// stand, and nothing is read first. A write that is not is asked again only
// after the tracker has been read, and since this one landed it is never asked
// again at all: the store holds it once, and the call answers as applied.
func TestEachTrackerOperationIsRetriedAsItsClassSays(t *testing.T) {
	t.Parallel()

	for name, class := range trackerRetryClasses {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			drive, driven := killedOnceDrivers[name]
			if !driven {
				t.Skip("no driver; TestEveryTrackerOperationIsClassifiedForRetry reports it")
			}
			tracker := newKilledOnceTracker(name)
			var sleeps []time.Duration
			options := testOptions(t, &fakeBackend{})
			options.Sleep = recordingSleep(&sleeps)
			session := openTestSession(t, options)

			if err := drive(context.Background(), session.recovering(tracker)); err != nil {
				t.Fatalf("%s through the wrapper error = %v, want it answered once the store did", name, err)
			}
			if len(sleeps) != 1 {
				t.Fatalf("waits = %v, want the one failure waited out once", sleeps)
			}
			switch class {
			case "read", "safe to repeat":
				if tracker.calls[name] != 2 {
					t.Fatalf("%s asked %d time(s), want it asked again as it stands", name, tracker.calls[name])
				}
				if class == "safe to repeat" && tracker.reads != 0 {
					t.Fatalf("%s read the tracker %d time(s) before asking again, want no read for a write safe to repeat", name, tracker.reads)
				}
			case "checked before it is asked again":
				if tracker.calls[name] != 1 {
					t.Fatalf("%s asked %d time(s), want the landed write found and not asked for again", name, tracker.calls[name])
				}
				if tracker.reads == 0 {
					t.Fatalf("%s was not checked for having landed before it was settled", name)
				}
				if got := strings.Count(tracker.items["yoyodyne-ifd.7"].Notes, killedOnceNote); got > 1 {
					t.Fatalf("the item carries the note %d times, want one", got)
				}
				if len(tracker.all) > 1 {
					t.Fatalf("the tracker holds %d created items, want one", len(tracker.all))
				}
			default:
				t.Fatalf("unknown class %q", class)
			}
		})
	}
}

// An update that appends nothing sets values, so it is asked for again as it
// stands, without a read — there is no note whose presence could say it landed.
func TestAnUpdateThatAppendsNothingIsAskedAgainAsItStands(t *testing.T) {
	t.Parallel()

	tracker := newKilledOnceTracker("Update")
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{})
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	priority := 1
	if _, err := session.recovering(tracker).Update(context.Background(), "yoyodyne-ifd.7", beads.WorkItemChange{Priority: &priority}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if tracker.calls["Update"] != 2 || tracker.reads != 0 {
		t.Fatalf("updates = %d, reads = %d, want the update asked again with nothing read", tracker.calls["Update"], tracker.reads)
	}
}

// The case the item was admitted for, end to end: a `bd update --append-notes`
// killed at its timeout after it wrote. The retry used to append the note a
// second time. Now the item is read first, the note is found at the end of its
// notes, and the action is reported as applied with one copy on the item.
func TestAnAppendKilledAfterItWroteLeavesOneCopyOfTheNote(t *testing.T) {
	t.Parallel()

	tracker := newKilledOnceTracker("Update")
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.7","note":"the store was contended","reason":"so the item says so"}`)},
		{SessionID: "session-1", FinalText: "The note is on it."},
	}})
	options.Tracker = tracker
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the landed note reported as applied", reply.Actions)
	}
	if tracker.calls["Update"] != 1 {
		t.Fatalf("appends = %d, want the note appended once", tracker.calls["Update"])
	}
	if got := strings.Count(tracker.items["yoyodyne-ifd.7"].Notes, "the store was contended"); got != 1 {
		t.Fatalf("the item carries the note %d times, want one:\n%s", got, tracker.items["yoyodyne-ifd.7"].Notes)
	}
}
