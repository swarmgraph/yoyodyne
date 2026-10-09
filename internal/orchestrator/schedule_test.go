package orchestrator

// What the scheduler chooses, how many at a time, and what it records about
// having chosen. The end-to-end test at the top is the acceptance criterion
// itself — several real runs at once, each in a worktree of its own, none of
// them force-integrated — and the rest are about the arithmetic and the
// refusals, which are cheaper to drive against a fake harness than against
// three real Git worktrees.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readiness"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The acceptance criterion, against the real pipeline: three ready items, a
// configured capacity of two, and every run in a worktree of its own. Two
// developers are held in a rendezvous until both are inside, which is what makes
// "concurrently" an observation rather than an inference, and all three changes
// reach the target branch by fast-forward — the second and third having been
// replayed onto where the first left it rather than forced over it.
func TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn(t *testing.T) {
	t.Parallel()

	harness := newRealScheduleHarness(t, 2, "yoyodyne-alpha", "yoyodyne-beta", "yoyodyne-gamma")
	// Worktrees are registered and unregistered underneath these runs for the
	// whole test, by Git run outside the worktree manager and so outside its
	// registry lease. This test was one of the two seen failing intermittently
	// on a rebase that crossed another run's half-written registration, so the
	// condition is part of what it judges rather than something that has to be
	// turned on: see startCreationLoop.
	startCreationLoop(t, harness.repository)
	// Two developers must be inside at once before either is let out. With a
	// capacity of one this deadlocks until the rendezvous times out, which is
	// exactly the failure the criterion is about.
	harness.developersMeet(2)

	scheduler := Scheduler{Open: harness.open}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s) (%s), want all three items pulled", len(schedule.Started), schedule.Render())
	}
	for _, started := range schedule.Started {
		if started.Failure != "" || started.Declined != "" {
			t.Fatalf("%s did not run: failure=%q declined=%q", started.WorkItemID, started.Failure, started.Declined)
		}
		if started.Outcome.Integration == nil {
			t.Fatalf("%s was not integrated: %#v", started.WorkItemID, started.Outcome)
		}
	}

	// Every run had its own worktree and its own branch. Sharing either is the
	// thing the design forbids outright, so it is checked rather than assumed
	// from the runs having succeeded.
	worktrees := map[string]string{}
	branches := map[string]string{}
	for _, started := range schedule.Started {
		if previous, seen := worktrees[started.Outcome.WorktreePath]; seen {
			t.Fatalf("%s and %s shared worktree %s", previous, started.WorkItemID, started.Outcome.WorktreePath)
		}
		worktrees[started.Outcome.WorktreePath] = started.WorkItemID
		if previous, seen := branches[started.Outcome.Branch]; seen {
			t.Fatalf("%s and %s shared branch %s", previous, started.WorkItemID, started.Outcome.Branch)
		}
		branches[started.Outcome.Branch] = started.WorkItemID
	}

	// The target branch carries all three changes, each promoted onto the one
	// before it. A promotion that had been forced would have left one of these
	// files behind.
	for _, id := range []string{"yoyodyne-alpha", "yoyodyne-beta", "yoyodyne-gamma"} {
		if _, err := os.Stat(filepath.Join(harness.repository, id+".txt")); err != nil {
			t.Fatalf("%s is not on the target branch after its run integrated: %v", id, err)
		}
	}

	// Every run accounts for itself in durable state: work the harness chose
	// without a recorded reason is what the reason exists to make impossible.
	recorded, err := harness.store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 3 {
		t.Fatalf("recorded runs = %d, want one per started item", len(recorded))
	}
	for _, state := range recorded {
		if state.Selection == nil {
			t.Fatalf("run %s (%s) recorded no reason it was selected", state.RunID, state.WorkItemID)
		}
		if state.Selection.By != runstate.SelectedByScheduler {
			t.Fatalf("run %s was selected by %q, want the scheduler", state.RunID, state.Selection.By)
		}
		if !strings.Contains(state.Selection.Reason, state.WorkItemID) {
			t.Fatalf("run %s reason = %q, want it to account for the item it chose", state.RunID, state.Selection.Reason)
		}
	}
}

// The other half of the criterion, and the case concurrency is what makes
// reachable at all: two runs started from the same base, both approved, both
// changing the same line. One of them promotes; the other finds its target
// moved and cannot replay onto it, and the conflict goes back to the developer
// that wrote the losing change rather than to a person. It settles it in its own
// session and lands too.
//
// The harness still chooses nothing. What reaches the target is what the loser's
// own developer decided, judged again by the checks and by an independent
// reviewer before it was promoted — which is the difference between resolving a
// conflict and forcing one.
//
// Which of the two wins is decided by the promotion lease and is deliberately
// not asserted: what matters is that exactly one promoted first and the other
// reconciled onto it.
func TestSchedulerReturnsAConflictToTheLoserRatherThanForcingIt(t *testing.T) {
	t.Parallel()

	harness := newRealScheduleHarness(t, 2, "yoyodyne-alpha", "yoyodyne-beta")
	// Both developers are inside before either returns, so both runs were created
	// from the same base commit: that is what makes the second promotion a
	// contended one rather than a fast-forward onto work it already had.
	harness.developersMeet(2)
	harness.develop = func(workItemID, worktree string) error {
		shared := filepath.Join(worktree, "shared.txt")
		// A worktree that already holds Git's markers is the continuation: the
		// loser has been moved onto the winner's change and is being asked to
		// reconcile with it. This developer settles it by keeping both answers,
		// which is a decision it makes rather than one anything made for it.
		if existing, err := os.ReadFile(shared); err == nil && strings.Contains(string(existing), "<<<<<<<") {
			return os.WriteFile(shared, []byte(withoutConflictMarkers(string(existing))), 0o600)
		}
		return os.WriteFile(shared, []byte(workItemID+" wrote this\n"), 0o600)
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want both items pulled: %s", len(schedule.Started), schedule.Render())
	}

	var reconciled *Started
	for index := range schedule.Started {
		started := &schedule.Started[index]
		if started.Outcome.Blocked || started.Outcome.Integration == nil {
			t.Fatalf("%s did not land: %s", started.WorkItemID, schedule.Render())
		}
		if started.Outcome.RepairAttempts > 0 {
			reconciled = started
		}
	}
	if reconciled == nil {
		t.Fatalf("neither run reconciled anything, so nothing contended: %s", schedule.Render())
	}
	// One continuation, not a fresh run and not a person: the conflict cost the
	// loser a single repair attempt.
	if reconciled.Outcome.RepairAttempts != 1 {
		t.Fatalf("%s spent %d repair attempt(s) on one conflict", reconciled.WorkItemID, reconciled.Outcome.RepairAttempts)
	}

	// The target branch carries what the loser's developer settled on, which is
	// both answers: a promotion that had been forced through would show one of
	// them, and nothing here chose between them on anybody's behalf.
	content, err := os.ReadFile(filepath.Join(harness.repository, "shared.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, item := range []string{"yoyodyne-alpha", "yoyodyne-beta"} {
		if !strings.Contains(string(content), item+" wrote this") {
			t.Fatalf("shared.txt = %q, want the reconciliation the loser's developer made", content)
		}
	}

	// And neither item was handed to a person, which is the whole saving.
	for _, started := range schedule.Started {
		item, err := harness.Show(context.Background(), started.WorkItemID)
		if err != nil {
			t.Fatalf("Show() error = %v", err)
		}
		if item.Status == "blocked" {
			t.Fatalf("%s was blocked: %q", item.ID, item.Notes)
		}
	}
}

// withoutConflictMarkers is how the fake developer above settles a conflict:
// both sides kept, Git's markers dropped. It is what a developer asked to
// reconcile does in the simplest case, done simply enough to assert on.
func withoutConflictMarkers(conflicted string) string {
	var kept []string
	for _, line := range strings.Split(conflicted, "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<<"), strings.HasPrefix(line, "======="), strings.HasPrefix(line, ">>>>>>>"):
		case line == "":
		default:
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n") + "\n"
}

// A capacity of one -- the default -- serializes the work: three items still all
// run, and never two at a time.
func TestSchedulerHonorsACapacityOfOne(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
	if harness.peak != 1 {
		t.Fatalf("peak concurrent runs = %d, want a configured capacity of one to be honored", harness.peak)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the queue drained", schedule.Stopped)
	}
}

// A raised capacity runs more at once, and the arithmetic accounts for the runs
// this pass has started but that have not reserved yet: a scheduler that counted
// only the recorded runs would start the same slot repeatedly.
func TestSchedulerRunsUpToTheConfiguredCapacity(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.capacity = 3
	harness.developersMeet(3)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 4 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
	if harness.peak > 3 {
		t.Fatalf("peak concurrent runs = %d, want no more than the configured capacity of 3", harness.peak)
	}
}

// The intake hold is the operator's narrow control over work the harness chooses
// for itself, and the scheduler is the thing that chooses. Nothing is started
// and nothing is claimed.
func TestSchedulerStartsNothingWhileIntakeIsHeld(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC),
		Reason:        "the decomposition is heading somewhere odd",
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing chosen while intake is held", schedule.Started)
	}
	if schedule.Stopped != ScheduleIntakeHeld || schedule.IntakeHeld == nil {
		t.Fatalf("schedule = %#v, want the hold reported as what stopped the choosing", schedule)
	}
	if !strings.Contains(schedule.Render(), "the decomposition is heading somewhere odd") {
		t.Fatalf("rendered = %q, want the operator's own reason for the hold", schedule.Render())
	}
	// A held pass never reads the queue, so it says nothing about it rather than
	// reporting the zeroes it did not read. "0 admitted items" over a backlog
	// nobody looked at would be worse than saying nothing.
	if schedule.BacklogRead {
		t.Fatalf("schedule = %#v, want no claim about a backlog a held pass never read", schedule)
	}
	if strings.Contains(schedule.Render(), "backlog at the last pull") {
		t.Fatalf("rendered = %q, want no backlog counts from a pass that stopped before reading it", schedule.Render())
	}
}

// A hold placed while the scheduler is running stops it choosing anything more,
// and leaves what is already running alone. That is the whole difference between
// holding intake and pausing everything.
func TestSchedulerStopsChoosingWhenIntakeIsHeldMidPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		if len(h.order) == 1 {
			h.held = &runstate.IntakeHold{
				SchemaVersion: runstate.IntakeHoldSchemaVersion,
				ProductID:     "yoyodyne",
				HeldAt:        time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC),
			}
		}
		h.mu.Unlock()
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d, want the run already going to finish and nothing more chosen: %s",
			len(schedule.Started), schedule.Render())
	}
	if schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run in flight to have finished", schedule.Started[0].Outcome)
	}
	if schedule.Stopped != ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be what stopped the choosing", schedule.Stopped)
	}
}

// Work an unresolved directive pauses is named and skipped rather than started
// into a pause. The pipeline stops it either way; what this saves is the slot,
// and what it adds is the directive's own words in the schedule.
func TestSchedulerSkipsWorkAnUnresolvedDirectivePauses(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.pausing["yoyodyne-one"] = []directive.Directive{{
		ID:         "directive-1",
		Kind:       directive.KindArtifact,
		Text:       "the goal is being rewritten",
		Unresolved: "which goal this item now serves",
	}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-two" {
		t.Fatalf("started = %#v, want only the item nothing pauses", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("deferred = %#v, want the paused item named rather than dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "directive-1") {
		t.Fatalf("deferred reason = %q, want the directive that paused it named", schedule.Deferred[0].Reason)
	}
}

// The mis-selection this guard exists for, replayed: the architect's
// brief-promotion item, admitted and pullable and reported by the tracker as
// ready, with a free developer slot and an unattended scheduler. What it cost
// the first time was a whole run and two review rounds producing a correctly
// refused empty diff — and those rounds count against the item's cap, so a
// second mis-selection escalates work nobody ever started.
//
// So: nothing is started, and the pass says which item it passed over and why.
// Saying why is half the criterion. The item is not waiting for anything and
// never becomes pullable, so a pass that silently counted it among the unready
// would report a queue that is about to move when it is not.
func TestSchedulerNeverSelectsWorkAConversationCarries(t *testing.T) {
	t.Parallel()

	promotion := beads.WorkItem{
		ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open", Priority: 0,
		Executor: domain.WorkItemExecutorConversation,
	}
	harness := newScheduleHarness(promotion)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing selected for a run that cannot execute it: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != promotion.ID {
		t.Fatalf("deferred = %#v, want the item named rather than counted among the unready", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "conversation") {
		t.Fatalf("deferred reason = %q, want what carries the work named", schedule.Deferred[0].Reason)
	}
	// The item is still admitted work in the product manager's order; what it is
	// not is pullable.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want it queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), promotion.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the pass readable by an operator", schedule.Render())
	}
}

// The selection this guard exists for, replayed exactly: a queue drained to the
// bottom, and the Codex backend sitting there — open, at the lowest priority,
// reported by the tracker as ready, with a free developer slot and an unattended
// watch session. That is what happened on 2026-08-27, and it cost $34.38 for a
// run that failed at work a scope decision had put off the critical path months
// earlier. The deferral was expressed as priority 4, which reads as "last" to
// everything that pulls, so nothing about the pull was wrong.
//
// Parked, the same pass selects nothing. Draining to the bottom now finds
// nothing at the bottom, and the pass says which item it passed over and that
// releasing it is a decision rather than a wait.
func TestSchedulerNeverSelectsParkedWorkHoweverFarTheQueueDrains(t *testing.T) {
	t.Parallel()

	codex := beads.WorkItem{
		ID: "yoyodyne-ifd.6", Title: "Add the thin Codex developer and reviewer backend",
		Status: "open", Priority: 4,
		Parking: "off the critical path by the scope decision; released when a second backend is wanted",
	}
	harness := newScheduleHarness(codex)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want a drained queue to select nothing parked: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained rather than stopped for anything else", schedule.Stopped)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != codex.ID {
		t.Fatalf("deferred = %#v, want the parked item named rather than counted among the unready", schedule.Deferred)
	}
	// Half the criterion is saying why. Parking is not a wait: an operator told
	// only that it was not pulled would go looking for a blocker to clear.
	reason := schedule.Deferred[0].Reason
	for _, required := range []string{"parked", "however far the queue drains", "off the critical path by the scope decision"} {
		if !strings.Contains(reason, required) {
			t.Fatalf("deferred reason = %q, want it to contain %q", reason, required)
		}
	}
	// It is still admitted work in the product manager's order; what it is not is
	// pullable.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want it queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), codex.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the pass readable by an operator", schedule.Render())
	}
}

// Parking one item does not stop the pass: the slot goes to the next thing in
// the order rather than being spent on it or idled beside it. This is the other
// half of the parked set staying out of reach — a scheduler that stalled on a
// parked item would be a worse failure than the one it replaced.
func TestSchedulerCarriesOnPastParkedWork(t *testing.T) {
	t.Parallel()

	parked := beads.WorkItem{
		ID: "yoyodyne-ifd.77", Title: "First-class external configuration",
		Status: "open", Priority: 0,
		Parking: "deferred until team mode is scoped",
	}
	ordinary := beads.WorkItem{ID: "yoyodyne-ifd.188", Title: "Parked work is unschedulable", Status: "open", Priority: 1}
	harness := newScheduleHarness(parked, ordinary)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ordinary.ID {
		t.Fatalf("started = %#v, want the pass to carry on past the parked item: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained", schedule.Stopped)
	}
}

// landedOnPull is a landing sweep that reports the given items landed, closing
// them in the harness's tracker as the real one closes them in bd, and refuses
// to say anything about the rest.
type landedOnPull struct {
	harness *scheduleHarness
	landed  map[string]bool
	settles int
}

func (l *landedOnPull) Settle(_ context.Context, entries []backlog.Entry) (LandingSweep, error) {
	l.settles++
	var sweep LandingSweep
	for _, entry := range entries {
		if !l.landed[entry.ID] {
			continue
		}
		l.harness.mu.Lock()
		for index := range l.harness.Items {
			if l.harness.Items[index].ID == entry.ID {
				l.harness.Items[index].Status = "closed"
			}
		}
		l.harness.mu.Unlock()
		sweep.Landed = append(sweep.Landed, LandedConversation{
			WorkItemID: entry.ID, Executor: entry.Executor,
			Document:  "docs/designs/management-and-supervision.md",
			RevisedAt: time.Date(2026, 9, 7, 5, 30, 0, 0, time.UTC),
			Reason:    entry.ID + " - side conversations designed",
		})
	}
	return sweep, nil
}

// yoyodyne-ifd.330 replayed to its end: the architect's item, its design already
// in the tree, read by a pass. It is closed on the pull rather than passed over
// as work waiting on somebody opening a conversation, the pass says so, and the
// item beside it whose design has not landed is passed over exactly as before.
func TestAConversationsItemWhoseDesignLandedIsClosedOnThePullRatherThanPassedOver(t *testing.T) {
	t.Parallel()

	landed := beads.WorkItem{
		ID: "yoyodyne-ifd.330", Title: "The architect designs side conversations with merge-back", Status: "open", Priority: 2,
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}
	waiting := beads.WorkItem{
		ID: "yoyodyne-ifd.306", Title: "The architect designs conversation account failover", Status: "open", Priority: 2,
		Executor: domain.ConversationWith(domain.RoleArchitect),
	}
	ordinary := beads.WorkItem{ID: "yoyodyne-ifd.367", Title: "Close on landing", Status: "open", Priority: 3}
	harness := newScheduleHarness(landed, waiting, ordinary)
	sweep := &landedOnPull{harness: harness, landed: map[string]bool{landed.ID: true}}
	harness.landings = sweep

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Landed) != 1 || schedule.Landed[0].WorkItemID != landed.ID {
		t.Fatalf("landed = %#v, want 330 closed on its landing", schedule.Landed)
	}
	if schedule.LandingProblem != "" {
		t.Fatalf("landing problem = %q, want none", schedule.LandingProblem)
	}
	// The closed item is off the queue this pull chose from: not passed over,
	// not counted among the admitted.
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == landed.ID {
			t.Fatalf("deferred = %#v, want the closed item not passed over as still waiting on a conversation", schedule.Deferred)
		}
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != waiting.ID {
		t.Fatalf("deferred = %#v, want the item whose design has not landed passed over as before", schedule.Deferred)
	}
	// The counts are the last pull's, which found the ordinary item done and the
	// closed one gone: one admitted item, the one still waiting on its design.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want only the item whose design has not landed", schedule.Admitted, schedule.Pullable)
	}
	if len(schedule.Landed) != 1 || sweep.settles < 2 {
		t.Fatalf("landed = %#v after %d sweeps, want the close recorded once across every pull", schedule.Landed, sweep.settles)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ordinary.ID {
		t.Fatalf("started = %#v, want the ordinary item pulled as ever", schedule.Started)
	}
	if !strings.Contains(schedule.Render(), landed.ID+" was closed: its architect landed as the 2026-09-07 05:30:00Z revision of docs/designs/management-and-supervision.md") {
		t.Fatalf("rendered = %q, want the close said where the pass is read", schedule.Render())
	}
}

// A marked item does not stop the pass: the slot it would have taken goes to the
// next thing in the order rather than being spent on it or idled beside it.
func TestSchedulerCarriesOnPastWorkAConversationCarries(t *testing.T) {
	t.Parallel()

	promotion := beads.WorkItem{
		ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open", Priority: 0,
		Executor: domain.WorkItemExecutorConversation,
	}
	ordinary := beads.WorkItem{ID: "yoyodyne-ifd.144", Title: "Mark them", Status: "open", Priority: 1}
	harness := newScheduleHarness(promotion, ordinary)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ordinary.ID {
		t.Fatalf("started = %#v, want the pass to carry on to the next item: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained", schedule.Stopped)
	}
}

// The failure this guard exists for, replayed: an epic and the child that
// carries its execution both sitting ready, and a pass with room for both. The
// tracker reports both as pullable and the reservation sees two different items,
// so nothing downstream would have stopped two developers making the same change
// -- and the second of them would have met the first at integration.
func TestSchedulerLeavesAnEpicItsOpenChildrenAlreadyCover(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: "open", Priority: 1, Parent: epic.ID}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2

	// One pass with room for both is the whole of the failure: the epic is first
	// in the order, so a scheduler that did not know it was a container would have
	// started it — and started the child beside it.
	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != child.ID {
		t.Fatalf("started = %#v, want only the child that carries the work: %s", schedule.Started, schedule.Render())
	}
	// The skip is named against the item like any other selection decision, and
	// it names where the execution went: a container left in the queue is only
	// legible if the report says what is covering it.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the covered epic named rather than silently dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
	if !strings.Contains(schedule.Render(), epic.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the skip readable by an operator", schedule.Render())
	}
}

// The same guard against a shape a tracker can hand it. The pair above states
// its parentage as a field; this one states it only as a parent-child edge,
// which is what the tracker's own export does — carrying no parent field on any
// item in it — and a guard that read only the field would see such a store as a
// backlog with nothing decomposed in it.
//
// The identifiers are yoyodyne-ifd.121's because that is the decomposition the
// guard was written for, not because this reading is what failed on it: nothing
// keyed on parentage was in the tree when those two runs were started. See
// docs/diagnoses/yoyodyne-ifd-273-121-double-run-mechanism.md.
func TestSchedulerLeavesAnEpicCoveredByAChildTheTrackerStatesAsAnEdge(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{
		ID: "yoyodyne-ifd.121", Title: "Docs architecture: a readable README", Status: "open", Priority: 1,
	}
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Execute the README split", Status: "open", Priority: 1,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if child.Parent != "" {
		t.Fatalf("child parent field = %q, want the shape the tracker states, which sets none", child.Parent)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != child.ID {
		t.Fatalf("started = %#v, want only the child that carries the work: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the covered epic named rather than silently dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
}

// The shape of the double-run as it actually happened, rather than of the cause
// that was recorded for it. From the two runs' own durable state: the scheduler
// pulled yoyodyne-ifd.121.2 at 2026-08-20T06:05:34Z as position 1 of 48, and
// twenty minutes later pulled the epic yoyodyne-ifd.121 at 06:25:43Z as position
// 2, while that first run was still going — it ended at 06:47:15Z. Both were
// started from the same base commit, which carried nothing keyed on parentage in
// either direction; the coverage guard landed 12h43m after that second pull. So
// the failure was the absence of a guard, and this is what the one that exists
// now does when handed that pull.
//
// Three things about that pull have to be together for it to be the observed
// one, and each is a separate way through the guard:
//
//   - the child is claimed rather than queued, so a reading of the queue alone
//     cannot see it;
//   - its parentage is stated only as an edge, so a reading of the parent field
//     alone cannot see it; and
//   - the epic carries a parent-child edge of its own, pointing up at the root
//     epic it belongs to, so a reading that took an edge's ends the other way
//     round would defer the child and run the epic — the double-run over again
//     with the two runs swapped.
func TestSchedulerLeavesTheEpicWhoseClaimedChildIsTheOneThe121DoubleRunStarted(t *testing.T) {
	t.Parallel()

	const root = "yoyodyne-ifd"
	epic := beads.WorkItem{
		ID: "yoyodyne-ifd.121", Title: "Docs architecture: a readable README", Status: "open", Priority: 2,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121", ID: root, Type: "parent-child"},
		},
	}
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Execute the README split", Status: "in_progress", Priority: 2,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2
	harness.inFlight[child.ID] = runstate.State{
		RunID: "run-44940ec3d71cdbc6e47e39745fedf15f", WorkItemID: child.ID, Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// The whole of the failure: a second developer run of the same scope, beside
	// the one already going.
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started beside the run already carrying this scope: %s",
			schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the epic named as covered by the run in flight under it", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
	// And not the other way round, which is what a mirrored reading of the edges
	// would produce from this same pull.
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == child.ID {
			t.Fatalf("deferred = %#v, want the child left alone: it is the work, and its own edge points up at %s",
				schedule.Deferred, epic.ID)
		}
	}
}

// What covers an item is an unfinished child, whatever slice of the tracker it
// is in. A blocked child is work somebody will release; a claimed one is a run
// in flight over the very same change, and it is the case a reading of the queue
// alone would miss, because a claimed item has left the queue.
func TestSchedulerLeavesAnEpicItsUnfinishedChildrenCoverWhereverTheySit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		childStatus string
	}{
		{name: "a child waiting on a blocker", childStatus: "blocked"},
		{name: "a child somebody is already running", childStatus: "in_progress"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
			child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: test.childStatus, Priority: 1, Parent: epic.ID}
			other := beads.WorkItem{ID: "yoyodyne-other", Title: "Something else", Status: "open", Priority: 2}
			harness := newScheduleHarness(epic, child, other)
			harness.capacity = 2

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != other.ID {
				t.Fatalf("started = %#v, want the epic left to its child: %s", schedule.Started, schedule.Render())
			}
			if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
				t.Fatalf("deferred = %#v, want the epic named as covered", schedule.Deferred)
			}
			if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
				t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
			}
		})
	}
}

// Coverage is read from the tracker at every pull rather than remembered, so it
// is a state an item is in rather than a mark it carries: an item stops being
// covered when its last unfinished child leaves, and is ordinary work again.
// What the guard does not do is retire an item permanently -- a parent whose one
// child has closed while it stayed open is common, and holding it back forever
// would strand real work behind a decomposition that finished.
func TestSchedulerPullsAContainerOnceItsChildrenHaveLeftTheBacklog(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: "open", Priority: 1, Parent: epic.ID}
	harness := newScheduleHarness(epic, child)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if pulled := harness.pullOrder(); len(pulled) != 2 || pulled[0] != child.ID || pulled[1] != epic.ID {
		t.Fatalf("pulled = %v, want the child first and the parent only once its run had closed it: %s",
			pulled, schedule.Render())
	}
	// The deferral is what the pass said while the epic was covered, and it is
	// said once: the item is skipped for as long as the cover lasts, not reported
	// once per pull that skips it.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the coverage said once rather than once per pull", schedule.Deferred)
	}
}

// Dependencies are the tracker's answer rather than the scheduler's guess: an
// admitted item the tracker does not report as ready is left in the queue,
// however high its priority.
func TestSchedulerLeavesWorkTheTrackerDoesNotReportAsReady(t *testing.T) {
	t.Parallel()

	blocked := beads.WorkItem{ID: "yoyodyne-blocked", Title: "Blocked", Status: "open", Priority: 0}
	ready := beads.WorkItem{ID: "yoyodyne-ready", Title: "Ready", Status: "open", Priority: 2}
	harness := newScheduleHarness(blocked, ready)
	harness.ReadyItems = map[string]bool{ready.ID: true}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ready.ID {
		t.Fatalf("started = %#v, want only the item the tracker reports as pullable", schedule.Started)
	}
	// The unready item is accounted for by the counts rather than by a line of its
	// own. Both halves matter: naming every unready item would print a line per
	// backlog entry on every pass, and reporting nothing at all would leave a
	// backlog full of unpullable work indistinguishable from an empty one.
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want an unready item left to the counts", schedule.Deferred)
	}
	// The counts are the last pull's, which is deliberately the reading an
	// operator wants from a finished pass: the ready item ran and left the queue,
	// so what is left is the one item nothing can pull -- which is the answer to
	// "why did it stop with work still admitted?".
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("admitted = %d, pullable = %d, want the item nothing can pull still counted",
			schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), "1 admitted item(s), 0 of them ready to pull") {
		t.Fatalf("rendered = %q, want the backlog counted in what an operator reads", schedule.Render())
	}
}

// An item another process already has in flight is not started a second time.
// The reservation refuses it anyway; what this checks is that the scheduler does
// not spend a slot finding that out.
func TestSchedulerLeavesWorkAlreadyInFlightAlone(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.inFlight["yoyodyne-one"] = runstate.State{
		RunID: "run-elsewhere", WorkItemID: "yoyodyne-one", Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-two" {
		t.Fatalf("started = %#v, want the item nobody else is running", schedule.Started)
	}
	// A run in flight is not a deferral: the item was chosen already, by whoever
	// is running it, and `yoyo status` is where that run is read. What this pass
	// owes is the slot arithmetic that explains its own choices.
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want a run in flight left to the slot counts", schedule.Deferred)
	}
	if schedule.Occupied < 1 {
		t.Fatalf("occupied = %d, want the run another process holds counted against capacity", schedule.Occupied)
	}
}

// Every developer slot held by somebody else leaves the scheduler nothing to
// start and nothing to wait for, so it says so rather than spinning.
func TestSchedulerStopsWhenEveryDeveloperSlotIsHeldElsewhere(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{
		RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// The stop reason is the whole account here: no item is named, because what
	// kept this one out was the machine being full rather than anything about it.
	if len(schedule.Started) != 0 || len(schedule.Deferred) != 0 || schedule.Stopped != ScheduleCapacityFull {
		t.Fatalf("schedule = %#v, want the full capacity reported rather than an empty queue", schedule)
	}
}

// The configuration is re-read at every pull, so a capacity raised while the
// scheduler is running takes effect at the next selection rather than at the
// next restart.
func TestSchedulerReadsTheConfigurationAtEveryPull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1
	// The operator raises the limit after the first pull, which is the case the
	// design question was actually about.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.capacity = 3
		}
	}
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.pulls < 2 {
		t.Fatalf("pulls = %d, want the configuration read more than once", harness.pulls)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
}

// Staleness reports and decides nothing. An item whose goal was amended after it
// was admitted is pulled exactly as it would have been, and the change is
// written into the reason the run records, where somebody reading what the
// harness chose can see it.
func TestSchedulerPullsStaleWorkAndRecordsWhatChangedUnderIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.stale = []staleness.WorkItem{{
		ID:         "yoyodyne-one",
		ArtifactID: "v1-goals",
		Changes: []staleness.Change{{
			ArtifactID: "v1-goals",
			Action:     "amended",
			By:         domain.RoleProductManager,
			Reason:     "the second goal was narrowed",
		}},
	}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want stale work pulled rather than withheld", schedule.Started)
	}
	reason := harness.selectionFor("yoyodyne-one").Reason
	if !strings.Contains(reason, "v1-goals") || !strings.Contains(reason, "the second goal was narrowed") {
		t.Fatalf("reason = %q, want what changed upstream named in it", reason)
	}
	if !strings.Contains(reason, "held nothing back") {
		t.Fatalf("reason = %q, want it stated that staleness decided nothing", reason)
	}
}

// A staleness reading that fails costs the recorded reasons a sentence and costs
// the pass nothing else.
func TestSchedulerSchedulesWhenStalenessCannotBeRead(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.staleErr = errors.New("the artifact home is unreadable")

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the pass to carry on without a staleness reading", schedule.Started)
	}
	if !strings.Contains(schedule.StalenessProblem, "the artifact home is unreadable") {
		t.Fatalf("staleness problem = %q, want the failed reading named", schedule.StalenessProblem)
	}
}

// Two schedulers racing for the last slot is the design working, so a
// reservation that lost is reported as declined rather than as a failed run, and
// the pass exits zero.
func TestSchedulerReportsALostRaceAsDeclinedRatherThanFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{WorkItemID: id}, fmt.Errorf("reserve developer run: %w", runstate.CapacityError{Limit: 1, Active: 1})
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Declined == "" {
		t.Fatalf("started = %#v, want the lost race recorded as declined", schedule.Started)
	}
	if schedule.Started[0].Failure != "" || schedule.Failed() {
		t.Fatalf("schedule = %#v, want a lost race not to be a failure", schedule)
	}
}

// A run that failed is a failed run, and it does not stop the pass choosing
// others: what stops the choosing is the operator holding intake, not one item
// going wrong.
func TestSchedulerCarriesOnAfterOneRunFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 1
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		if id == "yoyodyne-one" {
			return Outcome{WorkItemID: id, Status: runstate.StatusFailed}, errors.New("the checks did not pass")
		}
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d, want a failed run not to stop the pass: %s", len(schedule.Started), schedule.Render())
	}
	if schedule.Started[0].Failure == "" || !schedule.Failed() {
		t.Fatalf("schedule = %#v, want the failed run reported as a failure", schedule)
	}
}

// A pull that cannot be made stops the choosing and does not abandon the runs
// already going: a scheduler that returned with work in flight would leave runs
// nothing in its own report accounts for.
func TestSchedulerWaitsOutStartedRunsWhenAPullFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 1
	released := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-released
		return h.complete(id), nil
	}
	// The pull after the one that started the run is the one that fails, so what
	// the scheduler is holding when it stops is a run of its own still going.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.openErr = errors.New("the configuration is unreadable")
			close(released)
		}
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the configuration is unreadable") {
		t.Fatalf("Schedule() error = %v, want the failed pull reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unreadable pull named", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the run already going to have been waited out", schedule.Started)
	}
}

// An operator watching a pass wants a number; an unattended one wants the queue
// drained. The bound is respected exactly.
func TestSchedulerStopsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 3

	schedule, err := Scheduler{Open: harness.open, Limit: 2}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("schedule = %#v, want exactly the requested number of runs", schedule)
	}
}

// An empty backlog is an answer rather than a failure.
func TestSchedulerReportsAnEmptyQueue(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %#v, want an empty queue reported as drained", schedule)
	}
	if !strings.Contains(schedule.Render(), ScheduleDrained) {
		t.Fatalf("rendered = %q, want it to say why nothing was started", schedule.Render())
	}
	// An empty backlog that was actually read is a different fact from one that
	// was not, and the emptiness is only reported because this pass looked.
	if !schedule.BacklogRead || schedule.Admitted != 0 {
		t.Fatalf("schedule = %#v, want an emptiness this pass actually read", schedule)
	}
	if !strings.Contains(schedule.Render(), "0 admitted item(s), 0 of them ready to pull") {
		t.Fatalf("rendered = %q, want the empty backlog counted", schedule.Render())
	}
}

// The order is the product manager's: highest priority first, and a lower
// priority never promoted past one because it happened to be listed earlier.
func TestSchedulerPullsInTheProductManagersOrder(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-low", Title: "Low", Status: "open", Priority: 3},
		beads.WorkItem{ID: "yoyodyne-high", Title: "High", Status: "open", Priority: 0},
		beads.WorkItem{ID: "yoyodyne-middle", Title: "Middle", Status: "open", Priority: 1},
	)
	harness.capacity = 1

	if _, err := (Scheduler{Open: harness.open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-high", "yoyodyne-middle", "yoyodyne-low"}
	if strings.Join(harness.pullOrder(), ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v", harness.pullOrder(), want)
	}
}

// A scheduler with nothing to open is refused rather than reporting a queue it
// never read, and so is one asked to start a negative number of runs.
func TestSchedulerRefusesAPassItCannotMake(t *testing.T) {
	t.Parallel()

	if _, err := (Scheduler{}).Schedule(context.Background()); err == nil {
		t.Fatal("Schedule() error = nil, want a scheduler with no way to pull refused")
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	if _, err := (Scheduler{Open: harness.open, Limit: -1}).Schedule(context.Background()); err == nil {
		t.Fatal("Schedule() error = nil, want a negative limit refused")
	}
}

// A pull missing a collaborator is refused rather than half-used, because a
// scheduler that could not read the intake hold would be choosing work under a
// hold it never saw.
func TestSchedulerRefusesAnIncompletePull(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{Open: func(context.Context) (Pull, error) { return Pull{Capacity: 1}, nil }}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil {
		t.Fatal("Schedule() error = nil, want an incomplete pull refused")
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
}

// --- watching ------------------------------------------------------------------

// The whole of what watching is: an empty queue does not end the pass. The
// session waits out its interval, reads the queue again, and starts work that
// was admitted while it was waiting — with nothing between the readings but the
// wait, because nothing about the queue is cached.
func TestWatchingPullsWorkAdmittedWhileItWasWaiting(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	sessions := &recordedSessions{}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.admit(readyItems("yoyodyne-late")...)
		}
		// The second wait is the queue empty again with the late item run, which
		// is where the operator stops the session.
		return sleeps < 2
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-late" {
		t.Fatalf("started = %#v, want the item admitted between polls pulled", schedule.Started)
	}
	if !schedule.Watched || schedule.Polls != 2 {
		t.Fatalf("schedule = watched %v after %d poll(s), want a session that waited twice", schedule.Watched, schedule.Polls)
	}
	// A drain would have stopped at the first empty queue and said so. The stop
	// reason a watch ends on is the operator, never an empty queue.
	if schedule.Stopped != ScheduleCancelled {
		t.Fatalf("stopped = %q, want the session ended by its operator rather than by an empty queue", schedule.Stopped)
	}
	// Idle is said once and stopping is said at the end, so a session that waited
	// twice over one quiet queue does not write two lines about it.
	want := []runstate.WatchState{runstate.WatchWatching, runstate.WatchIdle, runstate.WatchResumed, runstate.WatchIdle, runstate.WatchStopped}
	if got := sessions.states(); !sameStates(got, want) {
		t.Fatalf("recorded states = %v, want %v", got, want)
	}
	if reason := sessions.said(runstate.WatchIdle); !strings.Contains(reason, "backlog is empty") {
		t.Fatalf("idle reason = %q, want it to say what the session found", reason)
	}
}

// The 2026-09-26 stall, replayed: three developer slots, three runs started,
// two of them ending early, and the third going on for over an hour. The pulls
// the two endings woke found nothing they could start at that moment, and the
// session then waited on the third run's completion rather than on its poll
// interval — so work that became ready behind it sat unstarted beside two empty
// slots until the long run ended.
//
// A free slot is refilled at the next poll, whatever else is still in flight:
// the work ready at the first poll after the two endings is started there,
// with the long run still going, and the watch log says how many slots each
// filling poll filled.
func TestAFreedSlotIsRefilledAtTheNextPollWhileAnotherRunIsStillGoing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 3
	sessions := &recordedSessions{}
	// hold keeps the long runs going until the last of the refills has started.
	// It carries no bound: a session that waits on the long run's completion
	// never starts the refill, and the binary's own -timeout reports that hang
	// naming this wait. A bound that let the old behaviour fail below instead
	// was one a loaded machine could reach with the new behaviour working.
	hold := make(chan struct{})
	var (
		longRunEnded    bool
		longRunGoing    bool
		releaseLongRuns sync.Once
		ticks           int
	)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		switch id {
		case "yoyodyne-one", "yoyodyne-two":
			// The two that end early.
			return h.complete(id), nil
		case "yoyodyne-five":
			h.mu.Lock()
			longRunGoing = !longRunEnded
			h.mu.Unlock()
			releaseLongRuns.Do(func() { close(hold) })
			return h.complete(id), nil
		}
		<-hold
		if id == "yoyodyne-three" {
			h.mu.Lock()
			longRunEnded = true
			h.mu.Unlock()
		}
		return h.complete(id), nil
	}
	// The first interval the session waits on beside the long run is the one work
	// becomes ready in, and it elapses at once. No later interval elapses, so
	// anything else the session does waits on a run ending.
	interval := func(time.Duration) <-chan time.Time {
		ticks++
		if ticks > 1 {
			return nil
		}
		harness.admit(readyItems("yoyodyne-four", "yoyodyne-five")...)
		elapsed := make(chan time.Time, 1)
		elapsed <- time.Time{}
		return elapsed
	}
	// With nothing of its own going, the session sleeps; the first such sleep is
	// after everything has ended, which is where the operator stops it.
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Interval: interval, Sessions: sessions,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	got := harness.pullOrder()
	slices.Sort(got)
	want := []string{"yoyodyne-five", "yoyodyne-four", "yoyodyne-one", "yoyodyne-three", "yoyodyne-two"}
	if !slices.Equal(got, want) {
		t.Fatalf("pulled %v, want %v: the work ready at the poll after the two endings pulled into the slots they freed", harness.pullOrder(), want)
	}
	if !longRunGoing {
		t.Fatal("the freed slots were refilled only after the long run ended, want them refilled while it was still going")
	}
	if ticks < 1 {
		t.Fatal("the session never waited on its poll interval beside the long run")
	}
	// Every filling poll says how many slots it filled: three at the first, and
	// the two the early endings freed at the poll after the work became ready.
	filled := 0
	var lines []string
	for _, transition := range sessions.recorded() {
		var count, free int
		if _, err := fmt.Sscanf(transition.reason, "filled %d of %d", &count, &free); err == nil {
			filled += count
			lines = append(lines, transition.reason)
		}
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "filled 3 of 3 free developer slots; 3 item(s) pulled") {
		t.Fatalf("filling lines = %q, want the first poll to say it filled all three slots", lines)
	}
	if filled != 5 {
		t.Fatalf("filling lines = %q account for %d slot(s) filled, want 5", lines, filled)
	}
	if !strings.Contains(strings.Join(lines[1:], "\n"), "this session already had in flight") {
		t.Fatalf("filling lines = %q, want the refills said beside the runs already in flight", lines)
	}
	if len(schedule.Started) != 5 {
		t.Fatalf("started = %d run(s), want 5", len(schedule.Started))
	}
}

// Two sessions sharing the reservation limit, which the bounded drain makes
// routine: this session's one slot is taken by its own long run and the other
// by a run another process hosts. When that run ends, nothing of this session's
// has ended, so a session waiting only on its own completions left the slot it
// freed empty until its own run finished.
//
// A slot freed by any run is refilled at the next poll: the ready item is
// started there with this session's own run still going, and the filling line
// names the run that freed it and says it was another process's.
func TestASlotAnotherProcessFreesIsRefilledAtTheNextPollWhileTheSessionIsFull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{
		RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning,
	}
	sessions := &recordedSessions{}
	// hold keeps this session's own run going until the refill has started. It
	// carries no bound: a session that waits on its own run's completion never
	// starts the refill, and the binary's own -timeout reports that hang naming
	// this wait, rather than a bound a loaded machine could reach first.
	hold := make(chan struct{})
	var (
		ownRunGoing bool
		ownRunEnded bool
		release     sync.Once
		ticks       int
		runs        int
	)
	// The first item started is this session's long run; the second is the
	// refill, whichever of the two the order puts first.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		runs++
		refilled := runs > 1
		if refilled {
			ownRunGoing = !ownRunEnded
		}
		h.mu.Unlock()
		if refilled {
			release.Do(func() { close(hold) })
			return h.complete(id), nil
		}
		<-hold
		h.mu.Lock()
		ownRunEnded = true
		h.mu.Unlock()
		return h.complete(id), nil
	}
	// The first interval the full session waits on is the one the other
	// process's run ends in, and it elapses at once. No later interval elapses.
	interval := func(time.Duration) <-chan time.Time {
		ticks++
		if ticks > 1 {
			return nil
		}
		harness.mu.Lock()
		delete(harness.inFlight, "yoyodyne-elsewhere")
		harness.mu.Unlock()
		elapsed := make(chan time.Time, 1)
		elapsed <- time.Time{}
		return elapsed
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Interval: interval, Sessions: sessions,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	got := harness.pullOrder()
	slices.Sort(got)
	if want := []string{"yoyodyne-one", "yoyodyne-two"}; !slices.Equal(got, want) {
		t.Fatalf("pulled %v, want %v", harness.pullOrder(), want)
	}
	if !ownRunGoing {
		t.Fatal("the slot the other process freed was refilled only after this session's own run ended, want it refilled at the next poll")
	}
	if ticks < 1 {
		t.Fatal("the full session never waited on its poll interval beside another process's run")
	}
	var refill string
	for _, transition := range sessions.recorded() {
		if strings.HasPrefix(transition.reason, "filled 1 of 1 free developer slot") && strings.Contains(transition.reason, "freed since the last poll") {
			refill = transition.reason
		}
	}
	if !strings.Contains(refill, "1 developer slot freed since the last poll: run-elsewhere over yoyodyne-elsewhere, another process's run") {
		t.Fatalf("filling line = %q, want the refill to name the other process's run that freed the slot", refill)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want 2", len(schedule.Started))
	}
}

// A run that pauses on work its item waits on gives its developer slot back as it
// records the pause, so the next poll fills the slot rather than leaving it held
// by a run with no process behind it. On 2026-09-27 run-3b94404c held the only
// slot this way for nineteen hours beside a ready queue. The paused run is still
// in flight: it is not cancelled, and its item is not started a second time.
func TestARunPausedOnADependencyGivesItsSlotBackAndTheNextPollFillsIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.inFlight["yoyodyne-paused"] = runstate.State{
		RunID: "run-paused", WorkItemID: "yoyodyne-paused", Status: runstate.StatusRunning,
	}
	sessions := &recordedSessions{}
	// The first poll finds the one slot held, and the wait after it is the one
	// the run pauses in. The session stops at the wait after that.
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		paused := h.inFlight["yoyodyne-paused"]
		paused.DependencyPause = &runstate.DependencyPause{Blockers: []string{"yoyodyne-blocker"}}
		h.inFlight["yoyodyne-paused"] = paused
		h.mu.Unlock()
		return true
	}

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.sleeps < 1 {
		t.Fatal("the first poll did not find the slot held by the running run")
	}
	if got := harness.pullOrder(); !slices.Equal(got, []string{"yoyodyne-one"}) {
		t.Fatalf("pulled %v, want the slot the paused run gave back filled with the ready item", got)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the ready item started and the paused one not started again", schedule.Started)
	}
	var refill string
	for _, transition := range sessions.recorded() {
		if strings.HasPrefix(transition.reason, "filled 1 of 1 free developer slot") {
			refill = transition.reason
		}
	}
	if !strings.Contains(refill, "1 developer slot freed since the last poll: run-paused over yoyodyne-paused, another process's run") {
		t.Fatalf("filling line = %q, want it to name the paused run as what freed the slot", refill)
	}
}

// A session whose every slot is held by its own runs has nothing but one of
// those runs ending to wait on, so it waits on that and never on the interval,
// exactly as it did before a slot held elsewhere was read at each poll.
func TestASessionFullOfItsOwnRunsWaitsOnThemRatherThanThePoll(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 1
	sessions := &recordedSessions{}
	ticks := 0
	interval := func(time.Duration) <-chan time.Time {
		ticks++
		return nil
	}
	// A run that reserved carries its identifier back, which is what names it.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		outcome := h.complete(id)
		outcome.RunID = "run-" + id
		return outcome, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Interval: interval, Sessions: sessions,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want both items run one after the other", len(schedule.Started))
	}
	if ticks != 0 {
		t.Fatalf("the session waited on its poll interval %d time(s) with every slot its own, want it to wait on its runs", ticks)
	}
	// The second item filled the slot the first run freed, and the line says so.
	var said bool
	for _, transition := range sessions.recorded() {
		if strings.Contains(transition.reason, "1 developer slot freed since the last poll: run-yoyodyne-one over yoyodyne-one, this session's run") {
			said = true
		}
	}
	if !said {
		t.Fatalf("filling lines = %+v, want the refill to name this session's run that freed the slot", sessions.recorded())
	}
}

// The state that misled an operator three times on 2026-09-01, replayed: a
// session idle on one developer slot while a run works on the other, over a
// queue whose only unstarted work is the architect's to carry in conversation.
//
// What it said then was that nothing among the ready items was startable that
// the session had not already tried, and that the next move was the product
// manager's, nothing being chosen until ready work was admitted. Both halves
// were false. The line had not stopped — a run was in flight — and no admission
// would have moved any of the three items, because no run will ever start one.
//
// So the idle line says what the poll actually found: the runs going, the items
// it passed over, and the conversation each of them is carried in.
func TestAnIdleWatchSaysWhatItPassedOverAndWhichConversationCarriesIt(t *testing.T) {
	t.Parallel()

	architects := []beads.WorkItem{
		{ID: "yoyodyne-ifd.212", Title: "Amend the invariants", Status: "open", Priority: 1,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.203", Title: "Settle the design", Status: "open", Priority: 2,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.162", Title: "Record the decision", Status: "open", Priority: 3,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
	}
	harness := newScheduleHarness(architects...)
	harness.capacity = 2
	// The other slot: a run somebody else's process is carrying, which is what
	// makes the line moving rather than stopped.
	harness.inFlight["yoyodyne-ifd.236"] = runstate.State{
		RunID: "run-236", WorkItemID: "yoyodyne-ifd.236", Status: runstate.StatusRunning,
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started for work no run can carry", schedule.Started)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded, so nothing said what the session found")
	}
	for _, want := range []string{
		"1 run in flight",
		"3 items passed over, of 3 admitted",
		"carried in conversation (architect: yoyodyne-ifd.212, yoyodyne-ifd.203, yoyodyne-ifd.162)",
	} {
		if !strings.Contains(idle.reason, want) {
			t.Fatalf("idle reason = %q, want it to say %q", idle.reason, want)
		}
	}
	// The sentence that was read as a stopped line, gone rather than reworded.
	if strings.Contains(idle.reason, "this session has not already tried") {
		t.Fatalf("idle reason = %q, want what was found rather than a count of what was not", idle.reason)
	}
	// The two facts whose move follows is derived from. Without them a reader is
	// back to the clause that named the one person who could do nothing about it.
	if idle.running != 1 {
		t.Fatalf("idle running = %d, want the run in flight recorded so the line does not read as stopped", idle.running)
	}
	if want := domain.ConversationWith(domain.RoleArchitect); idle.executor != want {
		t.Fatalf("idle executor = %q, want %q, the conversation that carries the work passed over", idle.executor, want)
	}
	// And the same account in classes rather than in prose, which is what a
	// surface that has to answer a question about the queue reads instead of
	// working the answer out from the silence around it.
	want := runstate.PassedOver{Admitted: 3, Groups: []runstate.PassedOverGroup{{
		Class: runstate.PassedOverCarriedInConversation,
		Role:  domain.RoleArchitect,
		Count: 3,
		Items: []string{"yoyodyne-ifd.212", "yoyodyne-ifd.203", "yoyodyne-ifd.162"},
	}}}
	if !reflect.DeepEqual(idle.passedOver, want) {
		t.Fatalf("idle passed over = %+v, want %+v", idle.passedOver, want)
	}
	cause, accounted := readmodel.WhyThePollStartedNothing([]runstate.WatchTransition{{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         runstate.WatchIdle,
		At:            time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC),
		PassedOver:    idle.passedOver,
	}}, time.Time{}, time.Date(2026, 9, 6, 3, 0, 0, 0, time.UTC))
	if !accounted || !strings.Contains(cause.Says(), "carried in conversation by the architect") {
		t.Fatalf("the alarm reads %q from what this poll recorded, want the conversation that carries the work", cause.Says())
	}
}

// The 2026-09-05 window, replayed from the session's side: a run comes back
// parked on an exhausted usage limit that the provider said lifts at 13:43Z, and
// every poll after it starts nothing because the provider will not serve.
//
// What the session said then was that it had passed some items over, which is
// the same thing it says over an empty queue — so the ninety minutes read from
// outside as a line nobody was pulling, the stall watchdog said nothing
// accounted for it, and the operator was paged for a machine doing exactly what
// the provider had told it to. So the window is said, from the first poll made
// inside it, with the time the provider named.
func TestASessionWaitingOutAProvidersWindowSaysSoWithTheResetTime(t *testing.T) {
	t.Parallel()

	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.290")...)
	harness.now = time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	sessions := &recordedSessions{}
	// The run the provider refused. It is parked rather than failed: the deadline
	// is in its own durable state, and this is the outcome the session is handed.
	harness.run = func(*scheduleHarness, string) (Outcome, error) {
		return Outcome{
			WorkItemID: "yoyodyne-ifd.290", Status: runstate.StatusRunning,
			Paused: true, PauseCause: runstate.PauseUsageLimit, UsageLimitKind: "five-hour",
			UsageLimitResetsAt: &lifts,
		}, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the one item started and parked", schedule.Started)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded, so nothing said the session was inside a window")
	}
	if !idle.window {
		t.Fatalf("idle = %+v, want the poll marked as one made inside the provider's window", idle)
	}
	if idle.windowResetsAt == nil || !idle.windowResetsAt.Equal(lifts) {
		t.Fatalf("idle reset time = %v, want %s, the moment the provider named", idle.windowResetsAt, lifts)
	}
	// The words a person reads, which lead with the window because it is the whole
	// of why nothing started — and still say what the poll found behind it, because
	// that is what gets pulled when the window lifts.
	if !strings.HasPrefix(idle.reason, "Paused on the provider's usage limit until 13:43Z") {
		t.Fatalf("idle reason = %q, want it to lead with the window and the reset time", idle.reason)
	}
	if !strings.Contains(idle.reason, "passed over") {
		t.Fatalf("idle reason = %q, want what the poll found said behind the window", idle.reason)
	}
}

// A window the provider named accounts for the polls made inside it and for
// nothing after it. Past the deadline the session is choosing nothing for a
// reason nobody recorded, which is exactly the state the watchdog exists to
// catch — so the account goes back to saying what the poll actually found.
func TestAWindowThatHasLiftedStopsBeingWhatTheSessionSays(t *testing.T) {
	t.Parallel()

	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newScheduleHarness(readyItems("yoyodyne-ifd.290")...)
	harness.now = time.Date(2026, 9, 5, 13, 40, 0, 0, time.UTC)
	sessions := &recordedSessions{}
	harness.run = func(*scheduleHarness, string) (Outcome, error) {
		return Outcome{
			WorkItemID: "yoyodyne-ifd.290", Status: runstate.StatusRunning,
			Paused: true, PauseCause: runstate.PauseUsageLimit, UsageLimitResetsAt: &lifts,
		}, nil
	}
	// The first wait carries the session past the deadline the provider named, and
	// the poll after it is one the window no longer explains.
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.mu.Lock()
			h.now = lifts.Add(time.Minute)
			h.mu.Unlock()
			return true
		}
		return false
	}

	if _, err := (Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock,
	}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	transitions := sessions.recorded()
	windows := 0
	for _, transition := range transitions {
		if transition.window {
			windows++
		}
	}
	if windows != 1 {
		t.Fatalf("%d poll(s) were said to be inside the window, want only the one made before it lifted", windows)
	}
	// The last line is the stop; the one before it is the poll made after the
	// window lifted, which is the one under test.
	last := transitions[len(transitions)-2]
	if last.window || strings.Contains(last.reason, "usage window") {
		t.Fatalf("the poll after the window lifted still reports one: %+v", last)
	}
}

// The idle line names items and counts runs, and it is said again when either
// changes — so what it must not do is say itself again when neither has. A
// session polling an unchanged queue writes one line however many polls it
// makes, which is what the reporting promises a reader for a quiet night.
func TestAnUnchangedIdlePollSaysItselfOnce(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(beads.WorkItem{
		ID: "yoyodyne-ifd.212", Title: "Amend the invariants", Status: "open", Priority: 1,
		Executor: domain.ConversationWith(domain.RoleArchitect),
	})
	sessions := &recordedSessions{}
	// Four polls over a queue nothing touches between them.
	harness.onSleep = func(*scheduleHarness, int) bool { return harness.sleeps < 4 }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	idles := 0
	for _, state := range sessions.states() {
		if state == runstate.WatchIdle {
			idles++
		}
	}
	if idles != 1 {
		t.Fatalf("recorded %d idle transitions across %d polls, want the unchanged account said once", idles, harness.sleeps)
	}
}

// The other side of the same line: a poll with nothing going and nothing
// anybody carries says so, and names nobody it should not. The conversation and
// the runs are what displace the admission clause, so a session that has neither
// leaves it where it was.
func TestAnIdleWatchOverAnEmptyBacklogNamesNoConversationAndNoRun(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded over an empty backlog")
	}
	if idle.reason != "the backlog is empty" {
		t.Fatalf("idle reason = %q, want the empty backlog said as itself", idle.reason)
	}
	if idle.running != 0 || idle.executor != "" {
		t.Fatalf("idle = %d run(s), executor %q, want neither over a queue with nothing in it", idle.running, idle.executor)
	}
}

// The intake hold is a brake rather than a stop for a session that is watching:
// it polls, chooses nothing, and resumes in place when the operator releases it.
// A drain still returns, because a drain is a command somebody is waiting on.
func TestWatchingBrakesOnHeldIntakeAndResumesWhenItIsReleased(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Now().UTC(),
		HeldBy:        runstate.IntakeHolderOperator,
		Reason:        "the queue is being reordered",
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.release()
		}
		return sleeps < 2
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the released session to pull the item that was waiting", schedule.Started)
	}
	want := []runstate.WatchState{runstate.WatchWatching, runstate.WatchBraked, runstate.WatchResumed, runstate.WatchIdle, runstate.WatchStopped}
	if got := sessions.states(); !sameStates(got, want) {
		t.Fatalf("recorded states = %v, want %v", got, want)
	}
	if reason := sessions.said(runstate.WatchBraked); !strings.Contains(reason, "the operator placed it — the queue is being reordered") {
		t.Fatalf("braked reason = %q, want the operator named as the holder with their own reason", reason)
	}
}

// The misattribution from the other side. A session whose own brake trips while
// the operator is already holding intake must not report its brake as what
// stopped the line: the hold in force is theirs, and what an operator does about
// a stopped queue depends entirely on which of the two placed it.
func TestWatchingReportsTheOperatorsHoldWhenItsOwnBrakeTripsUnderOne(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		// The operator holds intake while the third run is blocking, so the storm
		// is complete and the hold is already in force when the brake reaches for
		// it.
		if id == "yoyodyne-three" {
			h.mu.Lock()
			h.held = &runstate.IntakeHold{
				SchemaVersion: runstate.IntakeHoldSchemaVersion,
				ProductID:     "yoyodyne",
				HeldAt:        time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
				HeldBy:        runstate.IntakeHolderOperator,
				Reason:        "something about these runs looks wrong",
			}
			h.mu.Unlock()
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want a hold this session did not place reported as somebody else's", schedule.Braked)
	}
	reason := sessions.said(runstate.WatchBraked)
	if !strings.Contains(reason, "the operator placed it — something about these runs looks wrong") {
		t.Fatalf("braked reason = %q, want the operator's hold reported as theirs", reason)
	}
	if strings.Contains(reason, "brake") {
		t.Fatalf("braked reason = %q, want nothing claiming the session's own brake stopped the line", reason)
	}
	if rendered := schedule.Render(); strings.Contains(rendered, "this session's own brake held intake") {
		t.Fatalf("rendered = %q, want the operator's hold never rendered as the session's own", rendered)
	}
}

// The guard the day's own history asked for. A run that fails before it starts
// leaves the item exactly as ready as it was, so a loop with no memory would
// pull it again every interval forever. It is left alone until something about
// the item changes, and tried again as soon as something does.
func TestWatchingLeavesAFailedStartAloneUntilTheItemChanges(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-preflight")...)
	// The brake is out of the way here: what this is about is one item failing
	// repeatedly, which is exactly the case the brake is not for.
	harness.blockedRuns = 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// Nothing is claimed and nothing is recorded: the item is left where it
		// was, which is what makes it ready again at the next reading.
		return Outcome{WorkItemID: id}, errors.New("validate work item context: acceptance criteria are required")
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 3:
			// Three quiet polls in, the development manager rewrites the item.
			h.amend("yoyodyne-preflight", "now with acceptance criteria")
		case 6:
			return false
		}
		return true
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s) over six polls, want once before it was amended and once after: %v",
			starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want the two attempts accounted for", schedule.Started)
	}
	for _, started := range schedule.Started {
		if started.Failure == "" {
			t.Fatalf("%s = %#v, want the failed start recorded as failed", started.WorkItemID, started)
		}
	}
}

// What pauses an item for a directive is a person, and the whole point of a
// session that outlives them answering is that it notices. The directive is
// re-read at every pull, so an item deferred all night is pulled at the poll
// after it is resolved — and named once in the report rather than once a minute.
func TestWatchingPullsAnItemOnceItsDirectiveIsResolved(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-directed")...)
	harness.pausing["yoyodyne-directed"] = []directive.Directive{{
		ID:         "directive-1",
		Kind:       directive.KindArtifact,
		Text:       "the goal is being rewritten",
		Unresolved: "which goal this item now serves",
	}}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			h.mu.Lock()
			delete(h.pausing, "yoyodyne-directed")
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-directed" {
		t.Fatalf("started = %#v, want the item pulled once its directive was resolved", schedule.Started)
	}
	// Deferred is a report rather than a decision, and an item paused across four
	// polls is one line in it.
	if len(schedule.Deferred) != 1 {
		t.Fatalf("deferred = %#v, want the pause said once rather than once per poll", schedule.Deferred)
	}
}

// A drain is unchanged by any of this: it tries an item once and stops when
// nothing is left, whatever the item does afterwards.
func TestDrainingStillStopsWhenNothingMoreIsReady(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// The item stays ready, which is what would make a watch look at it
		// again. A drain never does.
		return Outcome{WorkItemID: id}, errors.New("the run could not be started")
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain to end on an empty queue", schedule.Stopped)
	}
	if starts := len(harness.pullOrder()); starts != 1 {
		t.Fatalf("the item was started %d time(s), want a drain to try it once", starts)
	}
	if schedule.Watched || schedule.Polls != 0 {
		t.Fatalf("schedule = watched %v after %d poll(s), want a drain that waited for nothing", schedule.Watched, schedule.Polls)
	}
}

// The failure-storm brake: runs blocking one after another with nothing landing
// between them holds intake, and it stays held. What it places is the operator's
// own switch, so what an operator arriving at a stopped line finds is one thing
// to understand and one thing to lift.
func TestWatchingHoldsIntakeWhenRunsKeepBlocking(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// A blocked run leaves the tracker with the item blocked, which is what
		// takes it out of the ready queue.
		h.retire(id)
		return Outcome{WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil {
		t.Fatalf("schedule = %#v, want the brake to have held intake", schedule)
	}
	if schedule.BlockedInARow < 3 {
		t.Fatalf("blocked in a row = %d, want the storm that tripped the brake counted", schedule.BlockedInARow)
	}
	// The fourth item is what says the line actually stopped: three runs blocked,
	// and the brake held intake before the queue got to it.
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s), want the brake to have stopped the line at three: %s", len(schedule.Started), schedule.Render())
	}
	if _, held, _ := harness.Held(); !held {
		t.Fatal("intake is not held, want the brake to have placed the operator's own switch")
	}
	// The session says who stopped it and what caused it, in one sentence and
	// without naming the operator for a hold they did not place.
	reason := sessions.said(runstate.WatchBraked)
	if !strings.Contains(reason, "the harness's own brake placed it after 3 run(s) blocked in a row") {
		t.Fatalf("braked reason = %q, want the brake named as the holder and the storm as the cause", reason)
	}
	if strings.Contains(reason, "the operator") {
		t.Fatalf("braked reason = %q, want a hold the brake placed never attributed to the operator", reason)
	}
}

// One item failing is not a storm. A run that blocks between runs that land
// leaves the count where it started, because what the brake is for is a broken
// machine rather than a broken item.
func TestWatchingDoesNotBrakeOnBlockedRunsThatAreNotConsecutive(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 2
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		if id == "yoyodyne-two" {
			return Outcome{WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want a run that landed to have cleared the count", schedule.Braked)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s), want every item pulled: %s", len(schedule.Started), schedule.Render())
	}
}

// A session given a budget stops when the runs it started have spent it. Nothing
// in flight is interrupted for money: the spend is already made, and what
// stopping a run would lose is the work it bought.
func TestASessionStopsWhenItHasSpentItsBudget(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.prices["yoyodyne-one"] = 1.25
	harness.prices["yoyodyne-two"] = 1.25
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Budget: 2}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the session ended on its budget: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want the third left unstarted once the budget was spent", len(schedule.Started))
	}
	if schedule.SpentUSD != 2.5 || schedule.Budget != 2 {
		t.Fatalf("spent $%.2f of $%.2f, want what the two runs actually cost against what the session was given", schedule.SpentUSD, schedule.Budget)
	}
}

// A bounded session that cannot tell what it spent stops, rather than carrying
// on inside a bound it has lost the ability to hold. It is the difference
// between a budget and the appearance of one: an unpriceable run left running is
// a session spending against a number nothing is comparing anything to, and the
// operator would find that out from a schedule this session does not return
// until it is over.
func TestABoundedSessionStopsWhenItCannotTellWhatItSpent(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		// A run whose price is not among the recorded runs of its item is
		// evidence that went missing rather than a run that was free.
		return Outcome{RunID: "run-elsewhere", WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}
	harness.prices["yoyodyne-one"] = 1
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Budget: 100}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleSpendUnreadable {
		t.Fatalf("stopped = %q, want the session stopped rather than left unbounded: %s", schedule.Stopped, schedule.Render())
	}
	// The budget is nowhere near spent, so nothing but the unreadable evidence
	// could have stopped this, and it stopped before the queue was drained.
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d run(s), want the session to stop at the first run it could not price", len(schedule.Started))
	}
	if !strings.Contains(schedule.SpendProblem, "run-elsewhere") {
		t.Fatalf("spend problem = %q, want the unpriceable run named", schedule.SpendProblem)
	}
	// And it says so where somebody who is not at this terminal reads it, which
	// is the whole reason a session that stops has to stop loudly.
	said := sessions.said(runstate.WatchStopped)
	if !strings.Contains(said, "budget") || !strings.Contains(said, "run-elsewhere") {
		t.Fatalf("recorded stop = %q, want the budget and the run it could not price both carried into it", said)
	}
}

// An unbounded pass is unaffected by the same evidence: nothing there was
// spending against a number, so the unpriceable run costs the report a sentence
// and the pass nothing at all.
func TestAnUnboundedPassReportsSpendItCouldNotReadAndCarriesOn(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{RunID: "run-elsewhere", WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %s, want every item run and the pass drained", schedule.Render())
	}
	if !strings.Contains(schedule.SpendProblem, "run-elsewhere") {
		t.Fatalf("spend problem = %q, want the unpriceable run named", schedule.SpendProblem)
	}
}

// A budget with nothing to measure it against is refused before anything is
// started. The operator asked for a bound; a pass that ran anyway would be
// reporting one it never had.
func TestASessionGivenABudgetIsRefusedWithNoWayToPriceItself(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	unpriced := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Spend = nil
		return pull, err
	}

	schedule, err := (Scheduler{Open: unpriced, Budget: 20}).Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "price what it has spent") {
		t.Fatalf("Schedule() error = %v, want a budget nothing can measure refused", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing run under a bound that was never held", schedule.Started)
	}
	// The same pull is fine unbounded: nothing there is spending against a
	// number, so nothing needs to price it.
	if _, err := (Scheduler{Open: unpriced}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want an unbounded pass unaffected", err)
	}
}

// The ordinary recovery a development manager makes: a run stops on a blocker,
// they release the item without editing anything else, and the session pulls it
// again. The blocker the harness wrote into the item's notes is what says the
// item is not the one this session already tried.
func TestWatchingPullsAnItemAgainAfterItsBlockerIsReleased(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-blocked")...)
	harness.blockedRuns = 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// What a blocked run does to the tracker: the item leaves the ready
		// queue, and the blocker is appended to its notes.
		h.block(id, "the replay conflicted and was left for a person")
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			// The development manager unblocks it and changes nothing else.
			h.unblock("yoyodyne-blocked")
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s), want it pulled again once somebody released it: %v", starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want both attempts accounted for", schedule.Started)
	}
}

// A session nobody can read the state of still does its work, and says that
// nobody can read it. The alternative is a session that stops working because
// it could not be observed, which is the wrong way round.
func TestASessionThatCannotRecordItselfStillWorks(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{failure: errors.New("the watch log is not writable")}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the work done anyway", schedule.Started)
	}
	if !strings.Contains(schedule.SessionProblem, "the watch log is not writable") {
		t.Fatalf("session problem = %q, want the unwritable log reported", schedule.SessionProblem)
	}
}

// A watching pull with no interval is refused rather than read again as fast as
// the machine allows.
func TestWatchingRefusesAPullWithNoInterval(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Poll = 0
		return pull, err
	}
	schedule, err := (Scheduler{Open: open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background())
	if err == nil {
		t.Fatal("Schedule() error = nil, want a watch with no interval refused")
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
	// The same pull drains perfectly well: a pass that never waits never reads
	// the interval.
	if _, err := (Scheduler{Open: open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want a drain unaffected by an interval it never uses", err)
	}
}

// --- readings of the harness that fail ------------------------------------------

// contendedStore is the reading that ended a session on 2026-09-01: the tracker
// refusing a listing while something else was writing to the same store, which
// succeeded again minutes later.
const contendedStore = "bd list failed with status cancelled and exit code -1"

// The observed sequence, replayed: one tracker reading fails and the next one
// succeeds. The session used to exit on the first of those and leave the queue
// to an external job; it now waits, reads again, pulls the work, and says that
// it did.
func TestWatchingReadsTheHarnessAgainAfterAReadingThatFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(_ *scheduleHarness, lists int) error {
		if lists == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	// The first wait is the retry and the session carries on; the second is the
	// drained queue after the item ran, which is where the operator stops it.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 2 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v, want a contended reading ridden through", err)
	}
	if schedule.Stopped != ScheduleCancelled {
		t.Fatalf("stopped = %q, want the session ended by its operator rather than by one failed reading", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the work pulled at the reading that succeeded", schedule.Started)
	}
	if schedule.ReadsRetried != 1 || !strings.Contains(schedule.ReadProblem, contendedStore) {
		t.Fatalf("schedule = %d retried, problem %q, want the one failed reading counted and named", schedule.ReadsRetried, schedule.ReadProblem)
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "1 reading(s) of the harness failed and were made again") {
		t.Fatalf("rendered = %q, want the reading it rode through reported", rendered)
	}
	// The session says it while it is happening, not only in the report it
	// returns when it is over: a session nobody is sitting at is read from the
	// watch log or not at all.
	if said := sessions.said(runstate.WatchIdle); !strings.Contains(said, contendedStore) || !strings.Contains(said, "read again") {
		t.Fatalf("the session said %q while it retried, want the failed reading and that it is being made again", said)
	}
	// And it says so as a reading that failed rather than as a queue it read and
	// found nothing in. Nothing anybody admits reaches a store that will not
	// answer, so this is the mark that keeps the message off the admission clause.
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded || !idle.unreadable {
		t.Fatalf("idle transition = %#v, want the poll marked as a reading that failed", idle)
	}
}

// A reading that fails while this session has a run of its own going. The line
// is moving and the store is not answering, and both have to be said: the runs
// are what stop it reading as a stopped machine, and the failed reading is what
// stops it reading as a queue somebody has to admit work to.
func TestAReadingThatFailsBesideARunSaysBothRatherThanNeither(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-held")...)
	harness.capacity = 2
	sessions := &recordedSessions{}
	// The run is held open across the pull whose reading fails, so the session
	// genuinely has one going when it records the outage.
	holding := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-holding
		return h.complete(id), nil
	}
	// Which pull is being made, so the reading that fails is the one after the
	// run started rather than a listing counted by hand.
	pulls := 0
	harness.onPull = func(_ *scheduleHarness, made int) { pulls = made }
	harness.failList = func(*scheduleHarness, int) error {
		if pulls == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			close(holding)
		}
		return sleeps < 3
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want a contended reading ridden through", err)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded while the reading failed")
	}
	if !strings.Contains(idle.reason, "1 run in flight") || !strings.Contains(idle.reason, contendedStore) {
		t.Fatalf("idle reason = %q, want the run it had going and the reading that failed", idle.reason)
	}
	if idle.running != 1 || !idle.unreadable {
		t.Fatalf("idle transition = %#v, want %d run(s) and the reading marked as failed", idle, 1)
	}
}

// A store that stays unreadable is not contention, and the session stops on it —
// saying how long it went on failing, which is the whole of what tells an
// operator which of the two they are looking at.
func TestWatchingStopsOnceTheHarnessGoesOnBeingUnreadable(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(*scheduleHarness, int) error { return errors.New(contendedStore) }
	// The operator never stops this one: what ends it is the window, and a session
	// that did not have one would back off here forever.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 100 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), contendedStore) {
		t.Fatalf("Schedule() error = %v, want the store that would not be read reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unreadable harness named", schedule.Stopped)
	}
	if !strings.Contains(err.Error(), "5m0s") {
		t.Fatalf("Schedule() error = %v, want it to say how long the session tried", err)
	}
	if schedule.ReadsRetried < 2 {
		t.Fatalf("retried = %d, want a session that tried again rather than stopping on the first reading", schedule.ReadsRetried)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing pulled from a queue that was never read", schedule.Started)
	}
	// The duration travels with the stop into the log, because the reader who has
	// to tell a broken store from one bad minute is not at this terminal.
	if said := sessions.said(runstate.WatchStopped); !strings.Contains(said, "5m0s") {
		t.Fatalf("the session stopped saying %q, want how long it tried", said)
	}
	if !strings.Contains(schedule.ReadFailure, "5m0s") {
		t.Fatalf("read failure = %q, want the reading the session stopped on carried apart from the ones it rode through", schedule.ReadFailure)
	}
}

// A session stops as unreadable for a second reason — a pull that assembles and
// is unusable — and what it says about that stop is about that stop. A contended
// reading it rode through earlier is a different fact about a different moment,
// and reporting it as why the session ended would put a store outage in front of
// an operator whose capacity is misconfigured.
func TestAReadingRiddenThroughIsNotWhyASessionStoppedForSomethingElse(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(_ *scheduleHarness, lists int) error {
		if lists == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	// The configuration is edited to something no pass can be made from between
	// the reading that failed and the one that would have succeeded.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.mu.Lock()
			h.capacity = 0
			h.mu.Unlock()
		}
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 100 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "developer capacity is 0") {
		t.Fatalf("Schedule() error = %v, want the unusable pull reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable || schedule.ReadFailure != "" {
		t.Fatalf("schedule = stopped %q on read failure %q, want a stop that was not a reading that failed", schedule.Stopped, schedule.ReadFailure)
	}
	if schedule.ReadsRetried != 1 || !strings.Contains(schedule.ReadProblem, contendedStore) {
		t.Fatalf("schedule = %d retried, problem %q, want the earlier reading still accounted for", schedule.ReadsRetried, schedule.ReadProblem)
	}
	// The line the session ends on is the whole of what a reader away from this
	// terminal gets, so a contended reading from before must not be in it.
	if said := sessions.said(runstate.WatchStopped); said != ScheduleUnreadable {
		t.Fatalf("the session stopped saying %q, want %q and nothing about a reading it rode through", said, ScheduleUnreadable)
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "the last of them: ") || !strings.Contains(rendered, contendedStore) {
		t.Fatalf("rendered = %q, want the reading it rode through still named", rendered)
	}
}

// A drain does not wait any of that out. It is a command somebody is waiting on
// the return of, and one that slept through an outage would be one that hung.
func TestADrainStopsOnTheFirstReadingThatFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.failList = func(*scheduleHarness, int) error { return errors.New(contendedStore) }

	schedule, err := (Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}).Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), contendedStore) {
		t.Fatalf("Schedule() error = %v, want the failed reading reported at once", err)
	}
	if schedule.Stopped != ScheduleUnreadable || schedule.ReadsRetried != 0 {
		t.Fatalf("schedule = stopped %q after %d retried reading(s), want a drain that did not wait", schedule.Stopped, schedule.ReadsRetried)
	}
	if harness.sleeps != 0 {
		t.Fatalf("the drain waited %d time(s), want none", harness.sleeps)
	}
}

// --- redeploying ---------------------------------------------------------------

// The whole of what a session redeploying itself is: a build lands over the one
// it is executing, and it stops choosing and stops, so the caller can restart it
// into what was deployed. The item still queued proves it stopped choosing
// rather than merely finishing.
func TestAWatchingSessionStopsToTakeUpTheBuildDeployedOverIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	sessions := &recordedSessions{}
	deployment := &deployedOver{}
	// The build lands while the first item is running, which is the ordinary case:
	// nobody deploys into an idle machine on purpose.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !schedule.Redeploying() || schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session stopped to be restarted into what was deployed", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the session to have claimed nothing after the deploy landed", schedule.Started)
	}
	// Nothing waited: a session with a build to take up does not spend a poll
	// interval first, because every interval it waits is an interval the machine
	// dispatches from the build somebody replaced.
	if schedule.Polls != 0 {
		t.Fatalf("polls = %d, want a session that stopped rather than waited", schedule.Polls)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "restarting into it") {
		t.Fatalf("stopped reason = %q, want the restart said where somebody who is not at the terminal reads it", reason)
	}
	// And the stop is marked as a restart rather than left to read as an ending.
	// Every surface takes whose move follows from that mark: a session that ended
	// is waiting on somebody to start another, and this one is waiting on nothing,
	// which is the whole difference between ending the operator's chore and
	// reproducing it once per deploy.
	if !sessions.restarted() {
		t.Fatal("the session recorded its stop as an ending, which tells every reader to start a session that is already coming back")
	}
}

// A run already going inside the drain bound is never interrupted for a
// redeploy: the session waits it out, and restarts the moment it hosts nothing.
// What the wait does not stop is the session's other duties — a seat freed
// while it drains is pulled into, because draining is about the runs the
// session hosts and not about the queue. The 07:35Z shape of 2026-09-19 was a
// session that had stopped everything to wait on one run, with the second seat
// empty for two hours.
func TestARedeployWaitsOutTheRunsAlreadyGoingAndKeepsPullingIntoFreeSeats(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 2
	// Both runs are required to be inside at once, so the deploy below lands with
	// two live runs rather than with one that has already finished.
	harness.developersMeet(2)
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session stopped to be restarted", schedule.Stopped)
	}
	// The third item was pulled into the seat the first finished run freed,
	// while the session was still draining, and every run was carried to its own
	// end: the bound was nowhere near.
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s), want the two runs already going and the third pulled into the seat one of them freed: %s", len(schedule.Started), schedule.Render())
	}
	for _, started := range schedule.Started {
		if started.Failure != "" || started.Outcome.Status != runstate.StatusSucceeded {
			t.Fatalf("%s = %#v, want a live run carried to its own end rather than cut off", started.WorkItemID, started)
		}
	}
	if schedule.Drain == nil || schedule.Drain.BoundReached || len(schedule.Drain.Stopped) != 0 {
		t.Fatalf("drain = %#v, want a drain whose runs all ended inside the bound", schedule.Drain)
	}
	// And the drain is on every line the session wrote while it lasted, with its
	// bound, so a reader is told what stops the wait rather than left to time it.
	drained := false
	for _, transition := range sessions.recorded() {
		if transition.draining != nil {
			drained = true
			if transition.draining.Bound() != 15*time.Minute {
				t.Fatalf("transition draining = %#v, want the configured bound on it", transition.draining)
			}
		}
	}
	if !drained {
		t.Fatalf("no transition carried the drain: %#v", sessions.recorded())
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "restarting into it") || strings.Contains(reason, "ran out") {
		t.Fatalf("stopped reason = %q, want a restart with no bound reached", reason)
	}
}

// The bound is what makes the drain a drain rather than a wait on whatever the
// hosted run happens to be doing. Past it the session stops the runs it hosts
// with the drain as the cause, which their pipelines read as a stop to continue
// from rather than a failure, and restarts with them preserved. On 2026-09-19 a
// session waited two hours on one run's race suite; this is the two hours.
func TestTheDrainBoundStopsTheHostedRunsAndRestartsAnyway(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.developersMeet(2)
	// A bound the test reaches in real time. The harness's clock is fake and
	// never reaches it; what fires is the drain's own timer, which is the thing
	// under test.
	harness.drainLimit = 20 * time.Millisecond
	// The deploy lands while the session is waiting on the runs, which is where
	// it lands in practice, so the session has to wake to find it.
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	// One run is checking under a five-hour stage limit and the other is
	// reviewing. Both end only when the session stops them, recording the cause
	// as their pipelines would.
	var stopped sync.Map
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseReviewing
		if id == "yoyodyne-one" {
			state.Phase = runstate.PhaseChecking
			state.CheckStage = &runstate.CheckStage{StartedAt: time.Now().UTC(), BoundSeconds: int64(5 * time.Hour / time.Second), Command: "make race"}
		}
		h.inFlight[id] = state
		h.mu.Unlock()
		<-ctx.Done()
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		stopped.Store(id, drained)
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: state.Phase, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !schedule.Redeploying() {
		t.Fatalf("stopped = %q, want the session restarted anyway once the bound ran out: %s", schedule.Stopped, schedule.Render())
	}
	if schedule.Drain == nil || !schedule.Drain.BoundReached {
		t.Fatalf("drain = %#v, want the bound recorded as reached", schedule.Drain)
	}
	if got := schedule.Drain.Stopped; len(got) != 2 || got[0] != "yoyodyne-one" || got[1] != "yoyodyne-two" {
		t.Fatalf("stopped = %v, want both hosted runs stopped for the restart", got)
	}
	for _, id := range []string{"yoyodyne-one", "yoyodyne-two"} {
		cause, found := stopped.Load(id)
		if !found {
			t.Fatalf("%s was not stopped with the drain as its cause", id)
		}
		if drained := cause.(RedeployDrain); drained.Bound != 20*time.Millisecond || drained.SessionID != "watch-drain" {
			t.Fatalf("%s cause = %#v, want the bound and the session on it", id, drained)
		}
	}
	// Neither run is a failure on the schedule: each is a run owed a
	// continuation, exactly as a provider the harness stopped on time leaves one.
	for _, started := range schedule.Started {
		if started.Failure != "" || !started.Outcome.Paused || started.Outcome.RedeployStop == nil {
			t.Fatalf("%s = %#v, want a run stopped and preserved rather than failed", started.WorkItemID, started)
		}
	}
	// The stop names what was preserved, so the reader of the log knows what the
	// session that comes back is about to pick up.
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "ran out with 2 run(s) still going") || !strings.Contains(reason, "yoyodyne-one, yoyodyne-two") {
		t.Fatalf("stopped reason = %q, want the bound and the preserved runs named", reason)
	}
	if !sessions.restarted() {
		t.Fatal("the session recorded its stop as an ending rather than a restart")
	}
	// And the bound running out was said before the runs were stopped, marked
	// on the drain so `yoyo status` names it rather than reading an idle line.
	reached := false
	for _, transition := range sessions.recorded() {
		if transition.draining != nil && transition.draining.BoundReached &&
			strings.Contains(transition.reason, "bound has run out") {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("no transition said the bound had run out: %#v", sessions.recorded())
	}
}

// Once the bound has stopped every run the session hosts, the restart is made
// in the same step: no further pull is opened and no recurring pass is fired
// first. On 2026-10-05 the runs stopped at the bound ended within a second,
// and the session went from stopping them straight into two recurring passes
// that kept it from reading their endings for forty minutes, pulling nothing
// all the while, until it was restarted by hand. The session that comes back
// then picks up the runs it preserved, as it always has, and pulls new work
// into the seats beside them.
func TestTheDrainBoundRestartsAtOnceAndTheSessionThatComesBackCarriesOn(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 3
	harness.developersMeet(2)
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	pastTheBound := func() bool {
		for _, transition := range sessions.recorded() {
			if transition.draining != nil && transition.draining.BoundReached {
				return true
			}
		}
		return false
	}
	var pullsPastTheBound, firedPastTheBound atomic.Int32
	harness.onPull = func(*scheduleHarness, int) {
		if pastTheBound() {
			pullsPastTheBound.Add(1)
		}
	}
	// A recurring pass is due at every pull, as the architect's was on the
	// afternoon this is about.
	harness.fire = func(*scheduleHarness, int) (RecurringSweep, error) {
		if pastTheBound() {
			firedPastTheBound.Add(1)
		}
		return RecurringSweep{Fired: []Fired{{Task: "architect-pass", Role: domain.RoleArchitect, Turns: 1}}}, nil
	}
	var secondSession atomic.Bool
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		if secondSession.Load() {
			return h.complete(id), nil
		}
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseDeveloping
		h.inFlight[id] = state
		h.mu.Unlock()
		<-ctx.Done()
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseDeveloping, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	first := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-before", Deployment: deployment}
	schedule, err := first.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted once the bound ran out: %s", schedule.Stopped, schedule.Render())
	}
	if got := schedule.Drain.Stopped; len(got) != 2 || got[0] != "yoyodyne-one" || got[1] != "yoyodyne-two" {
		t.Fatalf("drain stopped = %v, want both hosted runs stopped and preserved", got)
	}
	if pulls, fired := pullsPastTheBound.Load(), firedPastTheBound.Load(); pulls != 0 || fired != 0 {
		t.Fatalf("past the bound the session opened %d pull(s) and fired %d pass(es) before restarting, want the restart made at once", pulls, fired)
	}
	// Both stops were heard back before the restart, so nothing is unaccounted for.
	for _, started := range schedule.Started {
		if !started.Outcome.Paused || started.Outcome.RedeployStop == nil {
			t.Fatalf("%s = %#v, want a run stopped and preserved", started.WorkItemID, started)
		}
	}
	if len(schedule.Drain.Unreported) != 0 {
		t.Fatalf("unreported = %v, want every stopped run heard back from", schedule.Drain.Unreported)
	}

	// The session that comes back, on the build deployed over the last one.
	secondSession.Store(true)
	harness.admit(readyItems("yoyodyne-three")...)
	harness.mu.Lock()
	harness.order = nil
	harness.onPull = nil
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	harness.mu.Unlock()
	second := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, SessionID: "watch-after"}
	next, err := second.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	readopted := map[string]bool{}
	fresh := []string{}
	for _, started := range next.Started {
		if started.Readopted != "" {
			readopted[started.WorkItemID] = true
			continue
		}
		fresh = append(fresh, started.WorkItemID)
	}
	if !readopted["yoyodyne-one"] || !readopted["yoyodyne-two"] {
		t.Fatalf("started = %#v, want both preserved runs re-adopted", next.Started)
	}
	if len(fresh) != 1 || fresh[0] != "yoyodyne-three" {
		t.Fatalf("started fresh = %v, want the new item pulled into the free seat beside them", fresh)
	}
}

// A run the bound has stopped has no process behind it, so it is not counted
// as one the session hosts. A stopped run that never reports back is not
// waited on for the restart: the session restarts once the short grace a stop
// takes to record has passed, and names the run it did not hear from.
func TestARunStoppedByTheDrainBoundIsNotCountedAsHostedForTheRestart(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-silent")...)
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	// The run's stop is recorded, and then its goroutine never returns: what the
	// session holds of it is a record, with nothing behind it.
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseReviewing
		h.inFlight[id] = state
		h.mu.Unlock()
		<-ctx.Done()
		<-never
		return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
	}
	if live := liveHosted(map[string]int{"stopped": 0, "live": 1}, map[int]context.CancelCauseFunc{1: func(error) {}}); live != 1 {
		t.Fatalf("liveHosted = %d, want only the run still hosted counted", live)
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment, stoppedRunGrace: 50 * time.Millisecond}
	done := make(chan Schedule, 1)
	go func() {
		schedule, err := scheduler.Schedule(context.Background())
		if err != nil {
			t.Errorf("Schedule() error = %v", err)
		}
		done <- schedule
	}()
	var schedule Schedule
	select {
	case schedule = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the session went on waiting for a run it had stopped rather than restarting")
	}
	if schedule.Stopped != ScheduleRedeployed || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarted: %s", schedule.Stopped, schedule.Render())
	}
	if got := schedule.Drain.Stopped; len(got) != 1 || got[0] != "yoyodyne-silent" {
		t.Fatalf("drain stopped = %v, want the run stopped at the bound", got)
	}
	if got := schedule.Drain.Unreported; len(got) != 1 || got[0] != "yoyodyne-silent" {
		t.Fatalf("unreported = %v, want the stopped run that never reported back named", got)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "had not reported back") || !strings.Contains(reason, "yoyodyne-silent") {
		t.Fatalf("stopped reason = %q, want the run not heard back from named", reason)
	}
}

// A running check stage is stopped at the restart drain limit, even when load
// has scaled the stage's own limit to five hours. The clock reaches the drain
// deadline while the check stays running; no wall-clock allowance decides
// whether the restart was timely.
func TestTheDrainBoundStopsACheckStageInFlight(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-checking")...)
	harness.drainLimit = 15 * time.Minute
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	stageStarted := harness.clock()
	const stageBound = 5 * time.Hour
	checking := make(chan context.Context, 1)
	var running context.Context
	harness.onPull = func(h *scheduleHarness, pulls int) {
		switch pulls {
		case 1:
			// The next look sees both the deploy and the running stage before
			// the test advances the clock to the drain deadline.
			running = <-checking
		case 2:
			h.mu.Lock()
			h.now = stageStarted.Add(h.drainLimit)
			h.mu.Unlock()
		case 3:
			if running.Err() == nil {
				t.Error("the check stage was still running after the drain deadline")
				// Let the old behavior finish too, so the regression fails on
				// the missed drain limit instead of hanging until Go's timeout.
				h.mu.Lock()
				h.now = stageStarted.Add(stageBound + 2*time.Minute)
				h.mu.Unlock()
			}
		}
	}
	var cause atomic.Value
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseChecking
		state.CheckStage = &runstate.CheckStage{StartedAt: stageStarted, BoundSeconds: int64(stageBound / time.Second), Command: "make race"}
		h.inFlight[id] = state
		h.mu.Unlock()
		deployment.deploy()
		checking <- ctx
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseChecking, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed || !schedule.Redeploying() || !sessions.restarted() {
		t.Fatalf("stopped = %q, want the session restarting once the drain limit ran out: %s", schedule.Stopped, schedule.Render())
	}
	var drained RedeployDrain
	if stoppedWith, _ := cause.Load().(error); !errors.As(stoppedWith, &drained) {
		t.Fatalf("the check stage ended with %v, want the drain as its cause", cause.Load())
	}
	if !drained.At.Equal(stageStarted.Add(harness.drainLimit)) || drained.Bound != harness.drainLimit || drained.SessionID != "watch-drain" {
		t.Fatalf("cause = %#v, want the stage stopped exactly at the drain deadline under its configured limit", drained)
	}
	if elapsed := harness.clock().Sub(stageStarted); elapsed != harness.drainLimit {
		t.Fatalf("session restarted after %s, want %s", elapsed, harness.drainLimit)
	}
	if schedule.Drain == nil || !schedule.Drain.BoundReached || !schedule.Drain.Since.Equal(stageStarted) {
		t.Fatalf("drain = %#v, want the drain deadline reached", schedule.Drain)
	}
	if got := schedule.Drain.Stopped; len(got) != 1 || got[0] != "yoyodyne-checking" {
		t.Fatalf("drain stopped = %v, want the run preserved at its checks", got)
	}
	if len(schedule.Drain.ChecksWaited) != 0 {
		t.Fatalf("drain checks waited = %v, want no running check stage waited out past the drain limit", schedule.Drain.ChecksWaited)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want one hosted run", schedule.Started)
	}
	started := schedule.Started[0]
	if started.Failure != "" || !started.Outcome.Paused || started.Outcome.Status != runstate.StatusRunning || started.Outcome.RedeployStop == nil || started.Outcome.RedeployStop.Phase != runstate.PhaseChecking {
		t.Fatalf("started = %#v, want a run preserved at its checks rather than failed", started)
	}
	for _, transition := range sessions.recorded() {
		if transition.draining != nil && (transition.draining.Checking != 0 || !transition.draining.ChecksUntil.IsZero()) {
			t.Fatalf("draining = %#v, want no wait extending to the check stage's limit", transition.draining)
		}
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "stopped and preserved") || !strings.Contains(reason, "yoyodyne-checking") || strings.Contains(reason, "waited out to its end") {
		t.Fatalf("stopped reason = %q, want the preserved run named", reason)
	}
}

// Only a stage that has already finished gets a brief grace to retain its
// verdict. A running stage's own bound never extends the restart drain.
func TestCheckStageFinishingOnlyWaitsForAnAlreadyFinishedStage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 28, 21, 0, 0, 0, time.UTC)
	stage := func(started time.Time, finished *time.Time) runstate.State {
		return runstate.State{Phase: runstate.PhaseChecking, CheckStage: &runstate.CheckStage{StartedAt: started, BoundSeconds: 1800, FinishedAt: finished}}
	}
	justFinished := now.Add(-10 * time.Second)
	atGrace := now.Add(-checkStageDrainGrace)
	longFinished := now.Add(-10 * time.Minute)
	cases := []struct {
		name  string
		state runstate.State
		want  bool
	}{
		{"running inside its bound", stage(now.Add(-20*time.Minute), nil), false},
		{"running past its bound", stage(now.Add(-40*time.Minute), nil), false},
		{"just ended", stage(now.Add(-20*time.Minute), &justFinished), true},
		{"at the grace deadline", stage(now.Add(-20*time.Minute), &atGrace), false},
		{"ended long ago", stage(now.Add(-20*time.Minute), &longFinished), false},
		{"no stage recorded", runstate.State{Phase: runstate.PhaseChecking}, false},
		{"no start recorded", stage(time.Time{}, &justFinished), false},
	}
	for _, tc := range cases {
		if got := checkStageFinishing(tc.state, now); got != tc.want {
			t.Errorf("%s: checkStageFinishing = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// A run that has landed and is running its landing checks has no in-flight
// record left for the bound to read a phase off, and must not be read as a run
// still short of its claim: the landing budget allows hours, which is the wait
// the bound refuses. Past the bound the landing is stopped with the drain as
// its cause — the pipeline records it unverified and files nothing — and the
// session restarts.
func TestTheDrainBoundStopsAHostedRunsLandingChecks(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-landed")...)
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	var cause atomic.Value
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		// The run is over: its record has left the in-flight listing, and what
		// it is doing now is its landing.
		h.mu.Lock()
		delete(h.inFlight, id)
		h.mu.Unlock()
		landingBegun(ctx)
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session restarted once the bound ran out: %s", schedule.Stopped, schedule.Render())
	}
	var drained RedeployDrain
	if stoppedWith, _ := cause.Load().(error); !errors.As(stoppedWith, &drained) || drained.SessionID != "watch-drain" {
		t.Fatalf("landing stopped with %v, want the drain as its cause", cause.Load())
	}
	if got := schedule.Drain.Landings; len(got) != 1 || got[0] != "yoyodyne-landed" {
		t.Fatalf("drain landings = %v, want the landing named as stopped", got)
	}
	if len(schedule.Drain.Stopped) != 0 {
		t.Fatalf("drain stopped = %v, want a landed run not counted as one preserved for re-adoption", schedule.Drain.Stopped)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "landing checks of 1 run(s)") || !strings.Contains(reason, "unverified") {
		t.Fatalf("stopped reason = %q, want the stopped landing named", reason)
	}
}

// A draining session re-adopts nothing, and least of all a run it stopped
// itself. Past the bound a run at its review is stopped while one at its
// promotion is waited out, so the session goes on pulling with the stopped run
// in flight carrying its stop. Picking that run up again would resume it only
// for the next look to stop it — once a poll for as long as the promotion
// lasts, each round re-running its gate and noting another redeploy stop on
// its item under a selection reason that says a session before this one
// stopped it. The stopped run is the session that comes back's to re-adopt.
func TestADrainingSessionDoesNotReadoptARunItStoppedItself(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-reviewing", "yoyodyne-promoting")...)
	harness.capacity = 2
	harness.developersMeet(2)
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	// The promotion ends once the session has gone on pulling for several polls
	// past the stop, which is where a re-adoption would have been made.
	var stopped atomic.Bool
	var pullsAfterStop atomic.Int32
	release := make(chan struct{})
	var releasing sync.Once
	harness.onPull = func(*scheduleHarness, int) {
		if stopped.Load() && pullsAfterStop.Add(1) >= 5 {
			releasing.Do(func() { close(release) })
		}
	}
	var reviewingStarts atomic.Int32
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		phase := runstate.PhaseReviewing
		if id == "yoyodyne-promoting" {
			phase = runstate.PhaseIntegrating
		}
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = phase
		h.inFlight[id] = state
		h.mu.Unlock()
		if id == "yoyodyne-promoting" {
			select {
			case <-release:
			case <-ctx.Done():
				t.Errorf("the run at its promotion was stopped: %v", context.Cause(ctx))
			}
			return h.complete(id), nil
		}
		reviewingStarts.Add(1)
		<-ctx.Done()
		var drained RedeployDrain
		if !errors.As(context.Cause(ctx), &drained) {
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		defer stopped.Store(true)
		return Outcome{WorkItemID: id, Status: runstate.StatusRunning, Paused: true, RedeployStop: &runstate.RedeployStop{At: drained.At, Phase: runstate.PhaseReviewing, BoundSeconds: int64(drained.Bound / time.Second), SessionID: drained.SessionID}}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, SessionID: "watch-drain", Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session restarted once the promotion ended: %s", schedule.Stopped, schedule.Render())
	}
	if got := schedule.Drain.Stopped; len(got) != 1 || got[0] != "yoyodyne-reviewing" {
		t.Fatalf("drain stopped = %v, want only the run at its review stopped", got)
	}
	if pullsAfterStop.Load() < 5 {
		t.Fatalf("the session made %d pull(s) past the stop, want it to have gone on pulling while the promotion was waited out", pullsAfterStop.Load())
	}
	if starts := reviewingStarts.Load(); starts != 1 {
		t.Fatalf("the stopped run was started %d times, want once: the draining session re-adopted a run it had stopped itself", starts)
	}
	for _, started := range schedule.Started {
		if started.Readopted != "" {
			t.Fatalf("started = %#v, want nothing re-adopted by the draining session", started)
		}
	}
	// And the stop is still on the run's record, for the session that comes back.
	harness.mu.Lock()
	record := harness.inFlight["yoyodyne-reviewing"]
	harness.mu.Unlock()
	if record.RedeployStop == nil || record.RedeployStop.SessionID != "watch-drain" {
		t.Fatalf("record = %#v, want the stop left for the session that comes back", record)
	}
}

// Draining is about the runs the session hosts and not about its other duties:
// a recurring task that comes due while the session waits on a hosted run is
// fired on schedule, from the pull the session goes on making every interval.
// On 2026-09-19 the development manager's hourly pass was missed twice inside
// one drain; this is the pass that would have fired.
func TestADrainingSessionFiresItsRecurringTasksWhileItWaitsOnARun(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 2
	// The session wakes from its wait on the run once per poll, in real time,
	// which is how a cadence coming due reaches it.
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	// The hosted run outlives two firings, and ends only once they have been
	// made — inside the bound, so nothing is stopped for it.
	fired := make(chan struct{}, 8)
	harness.fire = func(_ *scheduleHarness, passes int) (RecurringSweep, error) {
		if passes < 2 {
			return RecurringSweep{}, nil
		}
		select {
		case fired <- struct{}{}:
		default:
		}
		return RecurringSweep{Fired: []Fired{{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 1}}}, nil
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		for made := 0; made < 2; {
			select {
			case <-fired:
				made++
			case <-ctx.Done():
				return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
			}
		}
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the restart once the run ended: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the hosted run carried to its end inside the bound", schedule.Started)
	}
	if len(schedule.Fired) < 2 {
		t.Fatalf("fired = %#v, want the recurring task fired while the session drained and waited on its run", schedule.Fired)
	}
	if schedule.Drain == nil || schedule.Drain.BoundReached {
		t.Fatalf("drain = %#v, want a drain the runs ended inside", schedule.Drain)
	}
}

// A pull made with the bound less than one poll away would start a run only to
// stop it, so the seat is left for the session that comes back — and the skip
// is said, in the watch log and on the pass, rather than reading as a poll that
// found nothing.
func TestAPullSkippedForTheDrainBoundSaysSo(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 2
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	skipped := make(chan struct{})
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		// The run ends once the session has said it skipped a pull.
		select {
		case <-skipped:
		case <-ctx.Done():
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		}
		return h.complete(id), nil
	}
	// Once the session has found the deploy — said in the log before the pull
	// that follows is opened — the bound is moved to under one poll away on the
	// session's own clock, which is the clock the skip is decided on; and a
	// second item is admitted for the free seat the session is about to decline
	// to fill. It is done on the pull itself so the queue is read after both.
	moved := false
	harness.onPull = func(h *scheduleHarness, _ int) {
		if moved {
			return
		}
		for _, transition := range sessions.recorded() {
			if transition.draining != nil {
				h.mu.Lock()
				h.now = h.now.Add(15*time.Minute - h.poll/2)
				h.mu.Unlock()
				h.admit(readyItems("yoyodyne-two")...)
				moved = true
				return
			}
		}
	}
	go func() {
		for {
			for _, transition := range sessions.recorded() {
				if strings.Contains(transition.reason, "less than one poll") {
					close(skipped)
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the restart once the run ended: %s", schedule.Stopped, schedule.Render())
	}
	// The second item was never pulled: the seat was free, and the bound was
	// too close for a pull into it to be worth making. The run ended inside the
	// bound, so the session restarted with nothing stopped for it.
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want nothing pulled into the free seat with the bound a minute away", schedule.Started)
	}
	if schedule.Drain == nil || schedule.Drain.Skipped == 0 {
		t.Fatalf("drain = %#v, want the skipped pull counted", schedule.Drain)
	}
	var said *recordedTransition
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchIdle && strings.Contains(transition.reason, "less than one poll") {
			said = &transition
			break
		}
	}
	if said == nil || !strings.Contains(said.reason, "the drain bound is") || !strings.Contains(said.reason, "1 free seat(s)") {
		t.Fatalf("transitions = %#v, want the skip said with the bound and the seat it left", sessions.recorded())
	}
	if said.draining == nil || said.draining.Bound() != 15*time.Minute {
		t.Fatalf("idle transition draining = %#v, want the drain and its bound on the line", said.draining)
	}
}

// A re-adoption the pipeline never took — a lease another process held at that
// moment, a tracker that would not answer — is not the end of it. The run's
// record still carries its stop and its note still promises a re-adoption, so
// the session tries again at its next pull rather than leaving a run nothing is
// going to pick up; the schedule reports the latest try on one line, and the
// refusal counts toward nothing.
func TestAReadoptionThePipelineDidNotTakeIsTriedAgainAtTheNextPull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	stoppedAt := time.Date(2026, 9, 19, 7, 50, 0, 0, time.UTC)
	stopped := runstate.State{
		RunID:        "run-one",
		WorkItemID:   "yoyodyne-one",
		Status:       runstate.StatusRunning,
		Phase:        runstate.PhaseChecking,
		RedeployStop: &runstate.RedeployStop{At: stoppedAt, Phase: runstate.PhaseChecking, BoundSeconds: 900},
	}
	harness.inFlight["yoyodyne-one"] = stopped
	// The first try is refused as a run another process holds, the second fails
	// before the pipeline reaches the run, and the third is taken.
	tries := 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		tries++
		switch tries {
		case 1:
			return Outcome{}, ExistingRunError{State: stopped}
		case 2:
			return Outcome{}, errors.New("load work item: the tracker did not answer")
		}
		return h.complete(id), nil
	}
	// Three polls, and then the operator stops the session.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if tries != 3 {
		t.Fatalf("tries = %d, want the re-adoption made again at each pull until the pipeline took it", tries)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the three tries reported on one line", schedule.Started)
	}
	readopted := schedule.Started[0]
	if readopted.Readopted != "run-one" || readopted.Readoptions != 3 || readopted.Outcome.Status != runstate.StatusSucceeded || readopted.Failure != "" {
		t.Fatalf("started = %#v, want the latest try's outcome with the count of tries", readopted)
	}
	if !strings.Contains(schedule.Render(), "re-adopted 3 times") {
		t.Fatalf("render = %s, want the tries said", schedule.Render())
	}
	// A refused re-adoption is not a run that blocked and not a dispatch that
	// never became a run: it counts toward nothing.
	if schedule.BlockedInARow != 0 || len(harness.attempts) != 0 {
		t.Fatalf("blocked in a row = %d, attempts = %#v, want a refused re-adoption to count toward nothing", schedule.BlockedInARow, harness.attempts)
	}
}

// A run at its promotion is the one the bound does not stop. It holds the
// target branch's lease, and a promotion cancelled part-way is the one boundary
// durable state cannot describe — so the session waits it out past the bound,
// and the restart follows it. The wait is the session's ordinary loop rather
// than a silence: its recurring tasks fire on their cadence, and only new
// starts are declined, each declined pull saying so. A forge outage can hold a
// promotion for hours, and those hours must not be the 07:35Z shape again.
func TestTheDrainBoundLeavesARunAtItsPromotionToFinishAndKeepsFiring(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 2
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	finished := make(chan struct{})
	// The cadence comes due only once the bound has run out, which is the moment
	// under test, and a second item is admitted then for the free seat.
	pastTheBound := func() bool {
		for _, transition := range sessions.recorded() {
			if transition.draining != nil && transition.draining.BoundReached {
				return true
			}
		}
		return false
	}
	var firedPastTheBound atomic.Int32
	harness.fire = func(h *scheduleHarness, _ int) (RecurringSweep, error) {
		if !pastTheBound() {
			return RecurringSweep{}, nil
		}
		if firedPastTheBound.Add(1) == 1 {
			h.admit(readyItems("yoyodyne-two")...)
		}
		return RecurringSweep{Fired: []Fired{{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 1}}}, nil
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseIntegrating
		h.inFlight[id] = state
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		case <-finished:
			return h.complete(id), nil
		}
	}
	// The promotion finishes on its own clock, once the session has fired its
	// task past the bound and declined the seat the second item would take.
	go func() {
		for {
			if firedPastTheBound.Load() >= 2 {
				close(finished)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the restart once the promotion finished: %s", schedule.Stopped, schedule.Render())
	}
	// The promotion was carried to its end, and nothing was pulled into the
	// free seat past the bound: the second item is left for the session that
	// comes back.
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the promotion carried to its end rather than cancelled, and nothing started beside it", schedule.Started)
	}
	if schedule.Drain == nil || !schedule.Drain.BoundReached || len(schedule.Drain.Stopped) != 0 || schedule.Drain.Skipped == 0 {
		t.Fatalf("drain = %#v, want the bound reached, nothing stopped for it, and the declined pulls counted", schedule.Drain)
	}
	// The recurring task fired past the bound, from the pull the session went
	// on making while it waited on the promotion.
	if len(schedule.Fired) < 2 {
		t.Fatalf("fired = %#v, want the recurring task fired while the promotion was waited out past the bound", schedule.Fired)
	}
	// And the declined pull says what it declined for, marked on the drain so
	// `yoyo status` names the session as restarting rather than idle.
	declined := false
	for _, transition := range sessions.recorded() {
		if transition.state == runstate.WatchIdle && transition.draining != nil && transition.draining.BoundReached && transition.draining.PullSkipped &&
			strings.Contains(transition.reason, "still going at its promotion") && strings.Contains(transition.reason, "1 free seat(s)") {
			declined = true
		}
	}
	if !declined {
		t.Fatalf("no idle transition said the seat was declined for the promotion being waited out: %#v", sessions.recorded())
	}
}

// A session waiting out a promotion past its drain bound says so on its line,
// with since when, and says it again at intervals for as long as the wait
// lasts. The readers of the watch log take a line that old as a session that
// died waiting, so one that wrote nothing through an hour-long promotion would
// be reported as stuck while it was doing exactly what it should.
func TestASessionWaitingOutAPromotionPastTheBoundSaysSoAgainWhileItWaits(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.drainLimit = 20 * time.Millisecond
	harness.poll = 5 * time.Millisecond
	deployment := &deployedOver{}
	sessions := &recordedSessions{}
	waitLines := func() []recordedTransition {
		var lines []recordedTransition
		for _, transition := range sessions.recorded() {
			if transition.draining != nil && transition.draining.BoundReached && transition.draining.Promoting > 0 &&
				strings.HasPrefix(transition.reason, "the watch session is ") {
				lines = append(lines, transition)
			}
		}
		return lines
	}
	// Each pull past the bound moves the session's clock on by the interval it
	// says the wait again at, and the promotion ends once it has been said three
	// times.
	finished := make(chan struct{})
	var finishing sync.Once
	harness.onPull = func(h *scheduleHarness, _ int) {
		said := len(waitLines())
		if said == 0 {
			return
		}
		if said >= 3 {
			finishing.Do(func() { close(finished) })
			return
		}
		h.mu.Lock()
		h.now = h.now.Add(promotionResayEvery)
		h.mu.Unlock()
	}
	harness.hostedRun = func(ctx context.Context, h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.mu.Lock()
		state := h.inFlight[id]
		state.Phase = runstate.PhaseIntegrating
		h.inFlight[id] = state
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			t.Errorf("the run at its promotion was stopped: %v", context.Cause(ctx))
			return Outcome{WorkItemID: id, Status: runstate.StatusCancelled}, nil
		case <-finished:
			return h.complete(id), nil
		}
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the restart once the promotion finished: %s", schedule.Stopped, schedule.Render())
	}
	lines := waitLines()
	if len(lines) < 3 {
		t.Fatalf("transitions = %#v, want the promotion wait said and then said again as the clock moved on", sessions.recorded())
	}
	since := lines[0].draining.PromotingSince
	for _, line := range lines {
		if line.draining.Promoting != 1 || !line.draining.PromotingSince.Equal(since) || since.IsZero() {
			t.Fatalf("line = %#v, want every line naming the one promotion and when the wait on it began", line)
		}
		if !strings.Contains(line.reason, "at their promotion since "+since.UTC().Format(time.RFC3339)) {
			t.Fatalf("reason = %q, want the promotion wait said with since when", line.reason)
		}
	}
}

// A held intake lets what is running finish, and a run the session before this
// one put down for its redeploy is running work: it is re-adopted at the first
// pull whether or not intake is held, because continuing it chooses nothing.
func TestAHeldIntakeDoesNotStopAReadoption(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-two")...)
	harness.capacity = 2
	held := runstate.IntakeHold{SchemaVersion: runstate.IntakeHoldSchemaVersion, ProductID: "yoyodyne", HeldAt: harness.clock(), HeldBy: runstate.IntakeHolderOperator, Reason: "let what is running finish"}
	harness.held = &held
	harness.inFlight["yoyodyne-one"] = runstate.State{
		RunID:        "run-one",
		WorkItemID:   "yoyodyne-one",
		Status:       runstate.StatusRunning,
		Phase:        runstate.PhaseChecking,
		RedeployStop: &runstate.RedeployStop{At: harness.clock(), Phase: runstate.PhaseChecking, BoundSeconds: 900},
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	sessions := &recordedSessions{}

	schedule, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-one" {
		t.Fatalf("pulled = %v, want the stopped run re-adopted and nothing new chosen under the hold", order)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Readopted != "run-one" || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the re-adoption carried to its end", schedule.Started)
	}
	if _, braked := sessions.entered(runstate.WatchBraked); !braked {
		t.Fatalf("states = %v, want the session braked by the hold after the re-adoption", sessions.states())
	}
}

// The session that comes back picks up what the one before it put down: a run
// in flight carrying a redeploy stop is re-adopted at the first pull, into the
// seat it already holds, ahead of anything new. Its selection says it was handed
// over rather than chosen, and names the run and the phase it continues from.
func TestASessionReadoptsTheRunsTheOneBeforeItStoppedForARedeploy(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-two")...)
	harness.capacity = 2
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	// Both are required to be inside at once, which is what proves the re-adopted
	// run took no seat from the new one.
	harness.developersMeet(2)
	stoppedAt := time.Date(2026, 9, 19, 7, 50, 0, 0, time.UTC)
	harness.inFlight["yoyodyne-one"] = runstate.State{
		RunID:         "run-one",
		WorkItemID:    "yoyodyne-one",
		WorkItemTitle: "yoyodyne-one",
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseChecking,
		RedeployStop:  &runstate.RedeployStop{At: stoppedAt, Phase: runstate.PhaseChecking, BoundSeconds: 900, SessionID: "watch-before"},
	}
	// The re-adopted run continues from its record; the harness stands in for
	// the pipeline adopting it and carrying it to its end.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Both were started from the one pull; which goroutine ran first is not the
	// order they were chosen in, and the schedule below says that.
	order := harness.pullOrder()
	slices.Sort(order)
	if len(order) != 2 || order[0] != "yoyodyne-one" || order[1] != "yoyodyne-two" {
		t.Fatalf("pulled = %v, want the stopped run re-adopted and the new item started", order)
	}
	if len(schedule.Started) != 2 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the re-adoption ahead of the new item on the schedule", schedule.Started)
	}
	var readopted *Started
	for index := range schedule.Started {
		if schedule.Started[index].Readopted != "" {
			readopted = &schedule.Started[index]
		}
	}
	if readopted == nil || readopted.WorkItemID != "yoyodyne-one" || readopted.Readopted != "run-one" {
		t.Fatalf("started = %#v, want the stopped run re-adopted and named as such", schedule.Started)
	}
	selection := harness.selectionFor("yoyodyne-one")
	if selection.By != runstate.SelectedByScheduler || !strings.Contains(selection.Reason, "re-adopted") ||
		!strings.Contains(selection.Reason, "run-one") || !strings.Contains(selection.Reason, "checking") || !strings.Contains(selection.Reason, "15m0s") {
		t.Fatalf("selection = %#v, want the hand-over, the run, its phase, and the bound named", selection)
	}
	// The re-adopted run held a seat already, so the second seat was still free
	// for the new item: nothing was pulled into a seat a continuation holds.
	if harness.peak != 2 {
		t.Fatalf("peak = %d, want the re-adoption and the new item running side by side", harness.peak)
	}
	if strings.Contains(schedule.Render(), "failed") {
		t.Fatalf("render = %s, want no failure", schedule.Render())
	}
}

// A bound the session has reached wins over a deploy waiting to be taken up. The
// operator gave this session a number to stay inside, and a restart is a session
// starting that number again — so a budget that is gone stops the session as
// spent, and what takes the build up is the next session somebody starts.
func TestASpentBudgetStopsTheSessionRatherThanRedeployingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.prices["yoyodyne-one"] = 2.50
	deployment := &deployedOver{}
	// The deploy lands while the run that spends the budget is still going, so
	// both are true at the pull that follows it.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.close(id)
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Budget: 2, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the bound the operator set to end the session: %s", schedule.Stopped, schedule.Render())
	}
	if schedule.Redeploying() {
		t.Fatal("the session asked to be restarted with its budget spent, which is the bound starting over")
	}
}

// The count of runs is the other bound, and it holds the same way. A session
// that has started the last run it was allowed stops on the number rather than
// restarting into the build: the restart would be that number starting again,
// which is not what the operator asked for by writing it.
func TestALastPermittedRunStopsTheSessionRatherThanRedeployingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	deployment := &deployedOver{}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 1, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("stopped = %q, want the number of runs the operator asked for to end the session: %s", schedule.Stopped, schedule.Render())
	}
	if schedule.Redeploying() {
		t.Fatal("the session asked to be restarted having started every run it was allowed, which is the bound starting over")
	}
}

// A session that cannot tell whether it has been deployed over goes on working.
// Stopping the line because a file could not be read would be a worse failure
// than the staleness this guards against, and the reading is tried again at
// every pull.
func TestASessionThatCannotReadItsOwnBinaryKeepsChoosingWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	deployment := &deployedOver{failure: errors.New("no such file or directory")}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Redeploying() {
		t.Fatalf("schedule = %#v, want the work done and no restart claimed", schedule.Started)
	}
	if !strings.Contains(schedule.RedeployProblem, "no such file or directory") {
		t.Fatalf("redeploy problem = %q, want the reading that failed reported", schedule.RedeployProblem)
	}
}

// A drain is a command somebody is waiting on the return of, so it returns. A
// pass that restarted itself would run the whole pass again from the top, which
// is the opposite of what was asked for.
func TestADrainNeverRedeploysItself(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	deployment := &deployedOver{}
	deployment.deploy()

	schedule, err := (Scheduler{Open: harness.open, Deployment: deployment}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain that ran its pass and returned", schedule.Stopped)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the queue drained", schedule.Started)
	}
}

// deployedOver is the session's own binary: unchanged until a test deploys over
// it, and unreadable where a test says the reading itself fails.
type deployedOver struct {
	mu       sync.Mutex
	replaced bool
	failure  error
}

func (d *deployedOver) deploy() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.replaced = true
}

func (d *deployedOver) Replaced() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failure != nil {
		return false, d.failure
	}
	return d.replaced, nil
}

func sameStates(got, want []runstate.WatchState) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// --- the fake harness the tests above pull from -------------------------------

// scheduleHarness is a whole harness for one scheduler to pull from: the
// tracker, the runs in flight, the operator's hold on intake, the recorded
// directives, and a way to run a chosen item. Everything it holds is behind one
// mutex, because a scheduler starts runs in parallel and they all report back
// into it.
func TestARepairRecoveryReusesOnlyItsOwnUnservedSlot(t *testing.T) {
	t.Parallel()
	run := continuableState()
	run.Status = runstate.StatusRunning
	run.RepairContinuations = []runstate.RepairContinuation{{DispatchPending: true}}
	task := CarryOutTask{WorkItemID: run.WorkItemID, RunID: run.RunID, Recover: true}
	for _, name := range []string{"unserved", "different run", "served", "already dispatched by this session", "not yet reserved"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := run
			state.RepairContinuations = append([]runstate.RepairContinuation(nil), run.RepairContinuations...)
			mine := map[string]int{}
			switch name {
			case "different run":
				state.RunID = "another-run"
			case "served":
				state.RepairContinuations[0].DispatchPending = false
			case "already dispatched by this session":
				mine[run.WorkItemID] = 0
			case "not yet reserved":
				state = runstate.State{WorkItemID: run.WorkItemID}
			}
			occupied := map[string]runstate.State{run.WorkItemID: state}
			harness := newScheduleHarness()
			harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) { return []CarryOutTask{task}, nil }
			chosen, _, pending := Scheduler{}.nextCarryOuts(&Schedule{}, Pull{CarryOut: harness}, occupied, mine, nil, closedGates{}, 0, 1,
				func(CarryOutTask) (outrankedCarryOut, bool) {
					t.Error("recovery of an occupied slot competed with ready work for a new one")
					return outrankedCarryOut{}, true
				})
			want := 0
			if name == "unserved" {
				want = 1
			}
			if len(chosen) != want || len(pending) != 0 || !reflect.DeepEqual(occupied[run.WorkItemID], state) {
				t.Fatalf("chosen = %+v, pending = %+v, occupied = %+v; want %d recovery and the original slot preserved", chosen, pending, occupied, want)
			}
		})
	}
}

func TestARepairRecoveryLeavesFreeSlotsForOtherDecisionsAndHonorsTheSessionLimit(t *testing.T) {
	t.Parallel()
	run := continuableState()
	run.Status = runstate.StatusRunning
	run.RepairContinuations = []runstate.RepairContinuation{{DispatchPending: true}}
	tasks := []CarryOutTask{
		{WorkItemID: run.WorkItemID, RunID: run.RunID, Recover: true},
		{WorkItemID: "fresh-item", RunID: "fresh-run"},
	}
	for _, limit := range []int{1, 2} {
		harness := newScheduleHarness()
		harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) { return tasks, nil }
		occupied := map[string]runstate.State{run.WorkItemID: run}
		chosen, _, _ := Scheduler{}.nextCarryOuts(&Schedule{}, Pull{CarryOut: harness}, occupied, nil, nil, closedGates{}, 1, limit, nil)
		if len(chosen) != limit || len(occupied) != 1 || !reflect.DeepEqual(occupied[run.WorkItemID], run) {
			t.Fatalf("limit %d: chosen = %+v, occupied = %+v; want recovery to reuse its slot and fresh work to take the free one within the limit", limit, chosen, occupied)
		}
	}
}

type scheduleHarness struct {
	orchestratortest.ScheduleTracker
	mu       *sync.Mutex
	inFlight map[string]runstate.State
	pausing  map[string][]directive.Directive
	stale    []staleness.WorkItem
	staleErr error
	held     *runstate.IntakeHold
	capacity int
	// slots is execution.developer_slots as each pull reads it: what each slot
	// prefers. Every other test here leaves it empty, which is every slot pulling
	// in the product manager's order.
	slots   []domain.DeveloperSlot
	openErr error
	// discharged is the human gates a person has recorded passing, and gatesErr
	// is a store that will not answer. Nothing recorded is the default, so an item
	// declaring a gate is one no pass may pull.
	discharged map[string][]string
	gatesErr   error
	// blockedRuns is the brake bound each pull reports, and prices is what each
	// run this harness ran cost. Both are per pull for the reason capacity is:
	// the scheduler re-reads them, and a test changes them under it.
	blockedRuns int
	prices      map[string]float64
	dirty       []string
	// sleeps counts the intervals a watching scheduler waited out, and onSleep is
	// how a test changes the world between polls. It reports whether the session
	// carries on, so returning false is the operator stopping it.
	sleeps  int
	onSleep func(*scheduleHarness, int) bool
	// onPull runs at the start of every pull with the number of pulls already
	// made, which is how a test changes the configuration under a running
	// scheduler.
	onPull func(*scheduleHarness, int)
	// failList decides whether a tracker listing fails, with the number of
	// listings already made. It stands in for the store contention a reconcile or
	// a settling run makes, which is a reading that fails and then does not.
	lists    int
	failList func(*scheduleHarness, int) error
	// now is the clock a watching session stamps its retries against. The fake
	// sleep below advances it by exactly the interval it was asked to wait, so a
	// test that drives a session through a store outage spends no real time
	// reaching the window the session gives up at.
	now time.Time
	// run stands in for the pipeline. The default completes the item; a test
	// that cares about a refusal or a failure replaces it.
	run func(*scheduleHarness, string) (Outcome, error)
	// stoppages is the harness's own record of work it stopped and nobody has
	// decided about, which is what separates a blocked status somebody has to
	// release from one whose blockers have all closed. A pull is wired with one
	// only where a test asks for it; without it a pull holds every blocked item,
	// which is what every other test here means.
	stoppages readmodel.Stoppages
	// decisions is what triage has already decided about the items those
	// stoppages belong to, which is what separates a held item waiting on the
	// development manager from one waiting on the harness carrying her decision
	// out. A pull wired without one passes every held item over as one nobody has
	// decided about, which is what every other test here means.
	decisions readmodel.Decisions
	// escalate stands in for putting stopped work to the development manager,
	// with the number of passes already made. A pull is wired with one only where
	// a test asks for it, so every other test's pass is what it always was.
	escalations int
	escalate    func(*scheduleHarness, int) (EscalationSweep, error)
	// summon stands in for the brake firing her sweep out of its cadence, with
	// the hold as it was summoned over and the number of summonses so far.
	// summonses is every hold this harness was asked to summon her over,
	// releases every hold the scheduler lifted, and revisions how many times the
	// brake's record was rewritten. brakeErr refuses the hold, cooldown is
	// execution.brake_cooldown as a pull reads it, and cycleBound is
	// execution.brake_escalation_cycles — left at zero, which is no bound, by
	// every test that is not about it.
	summon     func(*scheduleHarness, BrakeSummons, int) (Fired, error)
	summonses  []runstate.IntakeHold
	releases   []runstate.IntakeHold
	revisions  int
	brakeErr   error
	cooldown   time.Duration
	cycleBound int
	// tree stands in for the repository an item's stated prerequisites are read
	// against, and docketed is every unready item this harness was asked to route
	// to triage, by the key the docket would hold it under. A pull is wired with a
	// tree only where a test asks for one, so every other test's pass reads no
	// repository at all — which is what every pass did before this existed.
	tree       ScheduleTree
	routeErr   error
	docketed   []string
	unroutable bool
	// triage is a real docket to route to in place of this harness's stand-in,
	// for the tests about what the durable docket and the report pile end up
	// holding across pulls.
	triage ScheduleTriage
	// attempts is every dispatch that never became a run this harness was asked to
	// record, and attemptErr is a docket that refuses the write. They are separate
	// from the unready routing above because they describe the opposite thing: one
	// is an item nothing was spent on, and this is a start that was made and left
	// nothing behind.
	attempts   []UnstartedAttempt
	attemptErr error
	// fire stands in for waking a role on its cadence, with the number of passes
	// already made. A pull is wired with one only where a test asks for it, so
	// every other test's pass is a project that has scheduled nothing — which is
	// every project until one opts in.
	firings int
	fire    func(*scheduleHarness, int) (RecurringSweep, error)
	// recurring replaces the schedule a pull carries outright, for a test that
	// needs one that can also say when it is due.
	recurring ScheduleRecurring
	// outages is the product's record of the provider answering nobody, and
	// provider is what a watch asks whether the login has been renewed. A pull is
	// wired with them only where a test asks for it, so every other test's pass
	// reads no outage at all — which is what every pass did before the wait was
	// named.
	outages     ScheduleOutages
	provider    ScheduleProvider
	outageProbe time.Duration
	// divergences is the product's record of the target branches the harness
	// will not catch up to the remote's. A pull is wired with it only where a
	// test asks, so every other test's pass reads no divergence at all.
	divergences ScheduleDivergences
	// launchSettings is the product's record of a developer's provider that did
	// not apply its launch settings, build the harness revision the
	// pull runs, and developerProvider the configured developer's provider. A
	// pull is wired with them only where a test asks.
	launchSettings    ScheduleLaunchSettings
	build             string
	developerProvider domain.Backend
	// usageLimits is the product's record of the provider refusing the harness
	// for want of capacity, and developers every endpoint a developer's turn can
	// end on. A pull is wired with them only where a test asks, so every other
	// test's pass reads no recorded window — which is what every pass did before.
	usageLimits readmodel.UsageLimits
	developers  []readmodel.AgentEndpoint
	// capacityServed is what the provider has served since, read against the
	// refusals above; nil for every test that does not ask, which lifts nothing.
	capacityServed readmodel.CapacityServedRecord
	// outstanding stands in for the decisions the development manager recorded
	// and nobody has acted on, and carry for what firing one comes to. A pull is
	// wired with them only where a test asks, so every other test's pass carries
	// nothing out — which is what every pass did before this existed.
	carried     []CarryOutTask
	outstanding func(*scheduleHarness) ([]CarryOutTask, error)
	carry       func(*scheduleHarness, CarryOutTask) (CarriedOut, Outcome, error)
	// passedOver is what each pull handed the carry-out as the decisions it
	// offered and did not attempt, one map per pull, in order.
	passedOver []map[string]string
	// unattempted is told each time a pull hands over what it passed over, so a
	// test can wait on the account rather than poll for it; nil for every test
	// that does not wait, which tells nobody.
	unattempted chan<- struct{}
	// paused is the operator's pause over everything the harness spends, as the
	// pull reads it. A pull is wired with the switch only where a test asks, so
	// every other test's pass cannot see it — which is what every pass was before.
	paused    bool
	seePaused bool
	// audit stands in for the claim audit, with the claimed items the pull read,
	// and claims is the real one where a test drives it end to end. A pull is
	// wired with at most one of them and only where a test asks, so every other
	// test's pass is what it always was.
	audits int
	audit  func(*scheduleHarness, []beads.WorkItem) (ClaimSweep, error)
	claims ScheduleClaims
	// finished is every run this harness has recorded an ending for, which is what
	// a claim audit settling a dead run produces.
	finished []runstate.State
	// landings stands in for the landing sweep. A pull is wired with one only
	// where a test asks, so every other test's pass closes nothing — which is
	// what every pass did before this existed.
	landings ScheduleLandings
	// poll is the interval a watching pull reports. A minute is the shipped one,
	// which no test spends: the fake sleep advances the clock by it instead. A
	// test that needs the session to wake from a run it is waiting on, in real
	// time, sets one short enough to.
	poll time.Duration
	// drainLimit is the bound a watching pull reports on the session's wait to
	// restart into a deployed build. The default is the shipped one, which no
	// test reaches in real time; a test about the bound running out sets one
	// short enough to.
	drainLimit time.Duration
	// hostedRun stands in for the pipeline where a test needs the run to see the
	// context the session hosts it under — which is how the drain bound reaches
	// a run. It takes precedence over run where both are set.
	hostedRun func(context.Context, *scheduleHarness, string) (Outcome, error)

	pulls      int
	order      []string
	selections map[string]runstate.Selection
	running    int
	peak       int
	// started is announced on every start, and holds one announcement however
	// many starts made it, so a test that read the order and found it short
	// waits here for the next start rather than polling a clock.
	started chan struct{}

	// The rendezvous a test uses to require that runs actually overlap.
	meet    int
	arrived int
	gate    chan struct{}
}

func newScheduleHarness(items ...beads.WorkItem) *scheduleHarness {
	harness := &scheduleHarness{
		inFlight:   map[string]runstate.State{},
		pausing:    map[string][]directive.Directive{},
		selections: map[string]runstate.Selection{},
		prices:     map[string]float64{},
		capacity:   1,
		cooldown:   30 * time.Minute,
		poll:       time.Minute,
		drainLimit: 15 * time.Minute,
		gate:       make(chan struct{}),
		started:    make(chan struct{}, 1),
		// The morning the session that provoked the retry died, so a test reading
		// its own timings reads the ones in the report.
		now: time.Date(2026, 9, 1, 5, 40, 0, 0, time.UTC),
	}
	harness.ScheduleTracker = orchestratortest.ScheduleTracker{Items: items, ReadyItems: map[string]bool{}}
	harness.mu = &harness.ScheduleTracker.Mu
	for _, item := range items {
		harness.ReadyItems[item.ID] = true
	}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
	return harness
}

// readyItems builds the ordinary case: open work at one priority, in the order
// given.
func readyItems(ids ...string) []beads.WorkItem {
	items := make([]beads.WorkItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, beads.WorkItem{ID: id, Title: id, Status: "open", Priority: 2})
	}
	return items
}

// developersMeet requires that many runs to be inside at once before any of them
// is let out. A test that asks for more overlap than the capacity allows
// deadlocks here, and is reported by the binary's own timeout with every run
// named in the dump as parked in rendezvous -- rather than by a bound of this
// test's own, which a loaded machine reaches with the scheduler working.
func (h *scheduleHarness) developersMeet(count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.meet = count
}

func (h *scheduleHarness) rendezvous() {
	h.mu.Lock()
	if h.meet == 0 {
		h.mu.Unlock()
		return
	}
	h.arrived++
	if h.arrived == h.meet {
		close(h.gate)
	}
	gate := h.gate
	h.mu.Unlock()
	<-gate
}

func (h *scheduleHarness) open(context.Context) (Pull, error) {
	h.mu.Lock()
	pulls := h.pulls
	h.pulls++
	onPull := h.onPull
	h.mu.Unlock()
	if onPull != nil {
		onPull(h, pulls)
	}
	h.mu.Lock()
	openErr, capacity, slots := h.openErr, h.capacity, append([]domain.DeveloperSlot(nil), h.slots...)
	h.mu.Unlock()
	if openErr != nil {
		return Pull{}, openErr
	}
	h.mu.Lock()
	blockedRuns, cooldown, cycleBound := h.blockedRuns, h.cooldown, h.cycleBound
	stoppages, decisions := h.stoppages, h.decisions
	var escalations ScheduleEscalations
	if h.escalate != nil {
		escalations = h
	}
	// The summons is wired only where a test supplies one, for the reason the
	// escalation is: a pull without one brakes and probes exactly as it would,
	// and records that she could not be summoned.
	var summons ScheduleSummons
	if h.summon != nil {
		summons = h
	}
	// A project that has scheduled nothing carries no trigger at all rather than
	// one with an empty schedule, which is what recurringTrigger returns for it.
	var recurring ScheduleRecurring
	if h.fire != nil {
		recurring = h
	}
	if h.recurring != nil {
		recurring = h.recurring
	}
	var carryOut ScheduleCarryOut
	if h.outstanding != nil {
		carryOut = h
	}
	var holds OperatorHolds
	if h.seePaused {
		holds = harnessPause{h}
	}
	h.mu.Unlock()
	h.mu.Lock()
	tree := h.tree
	poll, drainLimit := h.poll, h.drainLimit
	// The docket is wired whether or not a tree is, because the two things it
	// records are found at different moments: an unready item is found by reading
	// the tree, and a dispatch that never became a run is found by making one. A
	// pull that carries no docket at all is what `unroutable` asks for.
	var docket ScheduleTriage
	if !h.unroutable {
		docket = h
	}
	if h.triage != nil {
		docket = h.triage
	}
	claims := h.claims
	if h.audit != nil {
		claims = h
	}
	h.mu.Unlock()
	return Pull{
		Tracker: h, Runs: h, Intake: h, Directives: h, Staleness: h, Gates: h,
		Stoppages: stoppages, Decisions: decisions,
		Environment: h,
		Capacity:    capacity, Slots: slots, Start: h.start, Escalations: escalations,
		Tree: tree, Triage: docket, Recurring: recurring, CarryOut: carryOut, Holds: holds,
		Claims: claims, Landings: h.landings,
		// A minute is the shipped interval, and no test spends one: the sleep is
		// the harness's own, so this is only what a watching pull is validated
		// against — unless a test shortened it to wake a waiting session.
		Poll:                        poll,
		BlockedRunsBeforeIntakeHold: blockedRuns,
		BrakeCooldown:               cooldown,
		BrakeEscalationCycles:       cycleBound,
		Brake:                       h,
		Summons:                     summons,
		Spend:                       h,
		Outages:                     h.outages,
		Provider:                    h.provider,
		OutageProbe:                 h.outageProbe,
		UsageLimits:                 h.usageLimits,
		Developers:                  h.developers,
		CapacityServed:              h.capacityServed,
		Divergences:                 h.divergences,
		LaunchSettings:              h.launchSettings,
		Build:                       h.build,
		DeveloperProvider:           h.developerProvider,
		RedeployDrainLimit:          drainLimit,
	}, nil
}

// Fire stands in for waking a role on its cadence, which like the delivery
// below is a provider turn the scheduler never makes itself.
func (h *scheduleHarness) Fire(context.Context) (RecurringSweep, error) {
	h.mu.Lock()
	h.firings++
	passes, fire := h.firings, h.fire
	h.mu.Unlock()
	return fire(h, passes)
}

// Escalate stands in for delivering one stopped run to the development
// manager's conversation, which is a provider turn the scheduler never makes
// itself.
func (h *scheduleHarness) Escalate(context.Context) (EscalationSweep, error) {
	h.mu.Lock()
	h.escalations++
	passes, escalate := h.escalations, h.escalate
	h.mu.Unlock()
	return escalate(h, passes)
}

// Audit stands in for auditing the claims the tracker holds against the runs the
// harness has, which is the one thing in a pull that writes to the tracker
// without starting anything.
func (h *scheduleHarness) Audit(_ context.Context, claimed []beads.WorkItem) (ClaimSweep, error) {
	h.mu.Lock()
	h.audits++
	audit := h.audit
	h.mu.Unlock()
	return audit(h, claimed)
}

// sleep stands in for waiting out a poll interval. It spends no time at all: a
// test that wants a session to end says so by returning false, which is the
// operator stopping it. The clock moves by what the wait asked for, so a session
// backing off through an outage reaches the window it gives up at.
func (h *scheduleHarness) sleep(_ context.Context, interval time.Duration) bool {
	h.mu.Lock()
	h.sleeps++
	h.now = h.now.Add(interval)
	sleeps, onSleep := h.sleeps, h.onSleep
	h.mu.Unlock()
	if onSleep == nil {
		return false
	}
	return onSleep(h, sleeps)
}

// Brake is the brake placing the operator's own switch with its trip attached.
// Like the store it stands in for, it leaves a hold already in force exactly as
// it was.
func (h *scheduleHarness) Brake(trip runstate.IntakeBrake, reason string, at time.Time) (runstate.IntakeHold, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.brakeErr != nil {
		return runstate.IntakeHold{}, h.brakeErr
	}
	if h.held != nil {
		return *h.held, nil
	}
	held := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        at,
		HeldBy:        runstate.IntakeHolderBrake,
		Reason:        reason,
		Brake:         &trip,
	}
	if err := held.Validate(); err != nil {
		return runstate.IntakeHold{}, err
	}
	h.held = &held
	return held, nil
}

// ReviseBrake rewrites the brake's record on its own hold, refusing every other
// hold exactly as the store does.
func (h *scheduleHarness) ReviseBrake(revise func(*runstate.IntakeBrake) error) (runstate.IntakeHold, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil || !h.held.Braked() {
		return runstate.IntakeHold{}, runstate.ErrNoBrakeHold
	}
	revised := *h.held.Brake
	if err := revise(&revised); err != nil {
		return *h.held, err
	}
	if err := revised.Validate(); err != nil {
		return *h.held, err
	}
	h.held.Brake = &revised
	h.revisions++
	return *h.held, nil
}

// ReleaseBrake is the harness lifting the brake's own hold and no other: on
// the development manager's decision, or on a probe that landed.
func (h *scheduleHarness) ReleaseBrake(string, time.Time) (runstate.IntakeHold, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil || !h.held.Braked() {
		return runstate.IntakeHold{}, false, nil
	}
	lifted := *h.held
	h.held = nil
	h.releases = append(h.releases, lifted)
	return lifted, true, nil
}

// Summon stands in for firing the development manager's sweep out of its
// cadence, which is a provider turn the scheduler never makes itself. It is
// wired into a pull only where a test supplies it.
func (h *scheduleHarness) Summon(_ context.Context, summons BrakeSummons) (Fired, error) {
	h.mu.Lock()
	h.summonses = append(h.summonses, summons.Hold)
	count, summon := len(h.summonses), h.summon
	h.mu.Unlock()
	return summon(h, summons, count)
}

// decideBrake is the development manager recording a decision about the
// brake's hold from her conversation, which is a write the scheduler reads at
// its next poll rather than one it makes.
func (h *scheduleHarness) decideBrake(decision runstate.IntakeBrakeDecision, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil || !h.held.Braked() {
		panic("decideBrake on a hold the brake did not place")
	}
	at := h.now
	revised := *h.held.Brake
	revised.Decision, revised.DecidedAt, revised.DecisionReason = decision, &at, reason
	h.held.Brake = &revised
}

// Price is what the runs of one item cost, as the recorded evidence would say.
func (h *scheduleHarness) Price(workItemID string) (runstate.ItemPrice, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cost, priced := h.prices[workItemID]
	if !priced {
		return runstate.ItemPrice{WorkItemID: workItemID}, nil
	}
	return runstate.ItemPrice{
		WorkItemID: workItemID,
		Runs:       []runstate.RunPrice{{RunID: "run-" + workItemID, WorkItemID: workItemID, CostUSD: cost}},
		TotalUSD:   cost,
	}, nil
}

// admit puts work in the backlog, ready to pull. It is what a product manager
// admitting something while a session watches looks like from the queue's side.
func (h *scheduleHarness) admit(items ...beads.WorkItem) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Items = append(h.Items, items...)
	for _, item := range items {
		h.ReadyItems[item.ID] = true
	}
}

// block is what a run that stopped on a blocker does to the tracker: the item's
// status moves out of the ready queue and the reason is appended to its notes,
// which is exactly what beads.Client.Block does.
func (h *scheduleHarness) block(workItemID, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ReadyItems, workItemID)
	for index := range h.Items {
		if h.Items[index].ID == workItemID {
			h.Items[index].Status = "blocked"
			h.Items[index].Notes = strings.TrimSpace(h.Items[index].Notes + "\n" + reason)
		}
	}
}

// unblock is the development manager releasing that item and changing nothing
// else about it, which is the recovery a session has to notice.
func (h *scheduleHarness) unblock(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ReadyItems[workItemID] = true
	for index := range h.Items {
		if h.Items[index].ID == workItemID {
			h.Items[index].Status = "open"
		}
	}
}

// amend changes what an item says, which is what a development manager
// replanning stopped work does to it.
func (h *scheduleHarness) amend(workItemID, description string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.Items {
		if h.Items[index].ID == workItemID {
			h.Items[index].Description = description
		}
	}
}

// release lifts the intake hold, which is the operator letting a braked session
// carry on.
func (h *scheduleHarness) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = nil
}

// recordedSessions is the durable account a watch session writes about itself,
// as a test reads it back.
type recordedSessions struct {
	mu          sync.Mutex
	transitions []recordedTransition
	failure     error
}

type recordedTransition struct {
	state  runstate.WatchState
	reason string
	// running, executor, and unreadable are what a reader takes whose move follows
	// an idle poll from: the runs the session could see going, the conversation
	// carrying the work it passed over, and a reading of the harness that failed.
	running    int
	executor   domain.WorkItemExecutor
	unreadable bool
	// restarting is the session marking its last line as a restart rather than an
	// ending, which is what every surface reads to tell the operator whether
	// anything is waiting on them.
	restarting bool
	// window and windowResetsAt are the provider's usage window the poll was made
	// inside, which is what every surface reads to tell this silence from one
	// nothing accounts for.
	window         bool
	windowResetsAt *time.Time
	// passedOver is the same account the reason states, in the classes every
	// reader of it reads. It is what the stall alarm names a cause from, so a test
	// about what a woken operator is told reads it here.
	passedOver runstate.PassedOver
	// mover is whose move a braked poll is, in the hold's own words.
	mover string
	// draining is the session's wait to restart into a deployed build, with its
	// bound, on every line written while it lasts.
	draining *runstate.WatchDrain
	// pass is the recurring pass a note says the session has begun.
	pass *runstate.WatchPass
}

func (r *recordedSessions) Record(transition SessionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	r.transitions = append(r.transitions, recordedTransition{
		state:      transition.State,
		mover:      transition.Mover,
		reason:     transition.Reason,
		running:    transition.Running,
		executor:   transition.Executor,
		unreadable: transition.Unreadable,
		restarting: transition.Restarting,

		window:         transition.ProviderWindow,
		windowResetsAt: transition.ProviderWindowResetsAt,
		passedOver:     transition.PassedOver,
		draining:       transition.Draining,
		pass:           transition.RecurringPass,
	})
	return nil
}

// entered is the first transition recorded into a state, whole rather than only
// its reason: a test about what a reader is told has to read the fields the
// reader's whose-move clause is derived from too.
func (r *recordedSessions) entered(state runstate.WatchState) (recordedTransition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.state == state {
			return transition, true
		}
	}
	return recordedTransition{}, false
}

// recorded is every transition in the order it was written, which is what a test
// about a state the session came out of has to read: entered above answers with
// the first of a state, and a window that lifted is a question about the last.
func (r *recordedSessions) recorded() []recordedTransition {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedTransition(nil), r.transitions...)
}

func (r *recordedSessions) states() []runstate.WatchState {
	r.mu.Lock()
	defer r.mu.Unlock()
	states := make([]runstate.WatchState, 0, len(r.transitions))
	for _, transition := range r.transitions {
		states = append(states, transition.state)
	}
	return states
}

// restarted reports the session having marked a stop as a restart rather than as
// an ending, which is what a reader takes whose move follows from.
func (r *recordedSessions) restarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.restarting {
			return true
		}
	}
	return false
}

// lastMover is whose move the most recent transition into a state named.
func (r *recordedSessions) lastMover(state runstate.WatchState) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	mover := ""
	for _, transition := range r.transitions {
		if transition.state == state {
			mover = transition.mover
		}
	}
	return mover
}

func (r *recordedSessions) said(state runstate.WatchState) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.state == state {
			return transition.reason
		}
	}
	return ""
}

// clock is what a scheduler stamps its own timings with, moved only by the fake
// sleep above.
func (h *scheduleHarness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *scheduleHarness) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	h.mu.Lock()
	h.lists++
	lists, failList := h.lists, h.failList
	h.mu.Unlock()
	if failList != nil {
		if err := failList(h, lists); err != nil {
			return nil, err
		}
	}
	return h.ScheduleTracker.List(ctx, status)
}

func (h *scheduleHarness) Incomplete() ([]runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make([]runstate.State, 0, len(h.inFlight))
	for _, state := range h.inFlight {
		states = append(states, state)
	}
	return states, nil
}

func (h *scheduleHarness) Held() (runstate.IntakeHold, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil {
		return runstate.IntakeHold{}, false, nil
	}
	return *h.held, true, nil
}

// DischargedGates is the human gates a person has recorded passing, as this
// harness is told to report them. Nothing recorded is the ordinary fixture: an
// item declaring a gate is then an item the scheduler must not pull.
func (h *scheduleHarness) DischargedGates() (map[string][]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gatesErr != nil {
		return nil, h.gatesErr
	}
	discharged := make(map[string][]string, len(h.discharged))
	for subject, gates := range h.discharged {
		discharged[subject] = append([]string(nil), gates...)
	}
	return discharged, nil
}

func (h *scheduleHarness) Pausing(workItemID string) ([]directive.Directive, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pausing[workItemID], nil
}

func (h *scheduleHarness) Stale(context.Context) ([]staleness.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.staleErr != nil {
		return nil, h.staleErr
	}
	return h.stale, nil
}

// start stands in for a reservation and a run: the item takes a slot, the
// selection is kept for the test to read, and the replaceable run decides what
// becomes of it.
func (h *scheduleHarness) start(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
	h.mu.Lock()
	h.order = append(h.order, workItemID)
	select {
	case h.started <- struct{}{}:
	default:
	}
	h.selections[workItemID] = selection
	// A run a session before this one stopped for its redeploy is already in
	// flight, and re-adopting it continues that record rather than making one.
	prior, adopted := h.inFlight[workItemID]
	if !adopted {
		h.inFlight[workItemID] = runstate.State{RunID: "run-" + workItemID, WorkItemID: workItemID, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping}
	}
	h.running++
	if h.running > h.peak {
		h.peak = h.running
	}
	run, hostedRun := h.run, h.hostedRun
	h.mu.Unlock()

	h.rendezvous()
	var outcome Outcome
	var err error
	if hostedRun != nil {
		outcome, err = hostedRun(ctx, h, workItemID)
	} else {
		outcome, err = run(h, workItemID)
	}

	h.mu.Lock()
	switch {
	case err == nil && outcome.RedeployStop != nil:
		// A run the drain bound stopped is left in flight carrying its stop,
		// whoever started it, which is what the real pipeline records: the next
		// pull reads it exactly as the session that comes back will.
		stopped := h.inFlight[workItemID]
		stopped.Phase = outcome.RedeployStop.Phase
		stopped.RedeployStop = outcome.RedeployStop
		h.inFlight[workItemID] = stopped
	case adopted && (err != nil || outcome.Paused):
		// A re-adoption the pipeline refused or parked leaves the record exactly
		// as it found it, stop and all, which is what the real pipeline does:
		// only a run it picked up clears the stop.
		h.inFlight[workItemID] = prior
	default:
		delete(h.inFlight, workItemID)
	}
	h.running--
	h.mu.Unlock()
	return outcome, err
}

// complete is what an ordinary run does to the tracker: the item closes and
// leaves the queue.
func (h *scheduleHarness) complete(workItemID string) Outcome {
	h.close(workItemID)
	return Outcome{WorkItemID: workItemID, Status: runstate.StatusSucceeded, WorkItemClosed: true}
}

func (h *scheduleHarness) close(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ReadyItems, workItemID)
	for index := range h.Items {
		if h.Items[index].ID == workItemID {
			h.Items[index].Status = "closed"
		}
	}
}

// retire drops an item's readiness and leaves its status alone. The real harness
// uses it rather than close because there the pipeline is what sets the status —
// closed when the work landed, blocked when a conflict stopped it — and a
// fixture that overwrote it would have the test asserting its own bookkeeping
// instead of what the run actually recorded.
func (h *scheduleHarness) retire(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ReadyItems, workItemID)
}

func (h *scheduleHarness) pullOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.order...)
}

func (h *scheduleHarness) selectionFor(workItemID string) runstate.Selection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.selections[workItemID]
}

// --- the real harness the end-to-end test pulls from --------------------------

// realScheduleHarness runs the actual pipeline: one Git repository, one worktree
// root, one run state store, and a fresh provider per run. The provider is per
// run rather than shared because that is what a run genuinely is — its own
// process — and because two runs sharing one fake would be a data race the
// harness itself does not have.
type realScheduleHarness struct {
	*scheduleHarness
	t            *testing.T
	repository   string
	worktreeRoot string
	store        *runstate.Store
	directives   *runstate.DirectiveStore
	holds        *runstate.OperatorHoldStore
	intake       *runstate.IntakeHoldStore
	// notes is every note a run appended to an item, which a test about what a
	// run records on its item reads.
	notes []string
	// develop is what each run's developer writes into its worktree. The default
	// gives every item a file of its own, which is the ordinary case; a test
	// about what happens when two changes collide points them at one path.
	develop func(workItemID, worktree string) error
}

func newRealScheduleHarness(t *testing.T, capacity int, ids ...string) *realScheduleHarness {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	harness := &realScheduleHarness{
		scheduleHarness: newScheduleHarness(readyItems(ids...)...),
		t:               t,
		repository:      pipelineRepository(t),
		worktreeRoot:    filepath.Join(t.TempDir(), "worktrees"),
		store:           store,
		directives:      newDirectiveStore(t),
		holds:           newOperatorHoldStore(t),
		intake:          newIntakeHoldStore(t),
	}
	harness.capacity = capacity
	harness.develop = func(workItemID, worktree string) error {
		return os.WriteFile(filepath.Join(worktree, workItemID+".txt"), []byte("implemented\n"), 0o600)
	}
	return harness
}

func (h *realScheduleHarness) open(context.Context) (Pull, error) {
	h.mu.Lock()
	h.pulls++
	capacity := h.capacity
	h.mu.Unlock()
	return Pull{
		Tracker: h, Runs: h.store, Intake: h.intake, Directives: h.directives, Gates: h.store,
		Capacity: capacity, Start: h.start,
	}, nil
}

// start builds one real pipeline over the shared repository and run state and
// runs the item through it, exactly as the command does.
func (h *realScheduleHarness) start(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
	h.mu.Lock()
	h.selections[workItemID] = selection
	h.mu.Unlock()

	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		h.rendezvous()
		return h.develop(workItemID, request.WorkingDirectory)
	}, approveVerdict)
	pipeline := newSharedPipeline(h.t, h.repository, h.worktreeRoot, h.store, h, provider, []string{"exit 0"})
	pipeline.Config.Execution.MaxConcurrentDevelopers = h.scheduleHarness.capacity
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: testDeveloperModel,
		Instances: h.scheduleHarness.capacity,
	}
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{
		Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: testReviewerModel,
		Instances: h.scheduleHarness.capacity,
	}
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel}
	pipeline.Directives = h.directives
	pipeline.Holds = h.holds
	pipeline.Intake = h.intake
	// Every run needs an identifier of its own; the shared fixture's constant one
	// would have two concurrent runs claiming the same lease.
	pipeline.NewRunID = runstate.NewRunID
	pipeline.Selection = selection

	outcome, err := pipeline.Run(ctx, workItemID)
	h.retire(workItemID)
	return outcome, err
}

// Show, Claim, RecordOutcome, Block, and Complete are the tracker the real
// pipeline drives. They are the fake harness's items behind its own mutex,
// because three runs are calling them at once.
func (h *realScheduleHarness) Show(_ context.Context, id string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range h.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *realScheduleHarness) Claim(_ context.Context, id string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	item, err := h.setStatus(id, "in_progress")
	return item, nil, err
}

func (h *realScheduleHarness) RecordOutcome(_ context.Context, id, notes string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notes = append(h.notes, notes)
	return h.itemLocked(id)
}

// recordedNotes is every note the runs appended to an item, in order.
func (h *realScheduleHarness) recordedNotes() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.notes...)
}

func (h *realScheduleHarness) Block(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "blocked")
}

func (h *realScheduleHarness) Release(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "open")
}

func (h *realScheduleHarness) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "closed")
}

// Reopen puts the item back in the backlog under the parking it was given, which
// is the whole of what a later pull reads: an item returned open and unparked is
// one the very next poll offers again.
func (h *realScheduleHarness) Reopen(_ context.Context, id, _ string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	h.mu.Lock()
	for index := range h.Items {
		if h.Items[index].ID == id {
			h.Items[index].Parking = parking
		}
	}
	h.mu.Unlock()
	return h.setStatus(id, "open")
}

func (h *realScheduleHarness) AddBlocker(_ context.Context, id, blockerID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.Items {
		if h.Items[index].ID == id {
			h.Items[index].Dependencies = append(h.Items[index].Dependencies,
				beads.Dependency{IssueID: id, ID: blockerID, Type: "blocks"})
			return nil
		}
	}
	return fmt.Errorf("no work item %s", id)
}

func (h *realScheduleHarness) setStatus(id, status string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.Items {
		if h.Items[index].ID == id {
			h.Items[index].Status = status
			return h.Items[index], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *realScheduleHarness) itemLocked(id string) (beads.WorkItem, error) {
	for _, item := range h.Items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

// Every stoppage comes back to the scheduler as an error, so a pass that read
// the error first said "failed" over a run handed to a person with its branch
// and worktree intact — the same word it said over one that broke with nothing
// to show. The pass reports the ending the run reached, in the read model's
// vocabulary, and works none of it out for itself.
func TestAPassNamesEachEndingRatherThanCallingEveryStoppageAFailure(t *testing.T) {
	t.Parallel()

	for _, want := range []struct {
		named   string
		started Started
	}{
		// The reading this exists to stop: the item is back with a person and the
		// change is still there, which is not what "failed" says.
		{"stopped", Started{
			WorkItemID: "yoyodyne-stopped",
			Outcome:    Outcome{Status: runstate.StatusFailed, Blocked: true, Branch: "yoyodyne/stopped"},
			Failure:    "the repair budget was spent with the checks still failing",
		}},
		{"failed", Started{
			WorkItemID: "yoyodyne-broke",
			Outcome:    Outcome{Status: runstate.StatusFailed},
			Failure:    "the worktree could not be cut",
		}},
		{"cancelled", Started{
			WorkItemID: "yoyodyne-cancelled",
			Outcome:    Outcome{Status: runstate.StatusCancelled},
			Failure:    "context canceled",
		}},
		{"timed out", Started{
			WorkItemID: "yoyodyne-late",
			Outcome:    Outcome{Status: runstate.StatusTimedOut},
			Failure:    "context deadline exceeded",
		}},
		// The three that are not endings at all keep the words they had: a start
		// the scheduler lost to another process, a run still in flight, and a run
		// whose work landed.
		{"declined", Started{WorkItemID: "yoyodyne-lost", Declined: "the slot went to another process"}},
		{"paused", Started{WorkItemID: "yoyodyne-parked", Outcome: Outcome{Status: runstate.StatusRunning, Paused: true}}},
		{"integrated", Started{
			WorkItemID: "yoyodyne-landed",
			Outcome:    Outcome{Status: runstate.StatusSucceeded, Integration: integratedOnto("main")},
		}},
		// A start that never became a run has no ending to name, so the failure is
		// still what is said.
		{"failed", Started{WorkItemID: "yoyodyne-unstarted", Failure: "the store refused the reservation"}},
	} {
		if named := want.started.state(); named != want.named {
			t.Errorf("%s is reported as %q, want %q", want.started.WorkItemID, named, want.named)
		}
	}

	// And the word reaches the rendered pass rather than only the helper.
	rendered := Schedule{Started: []Started{{
		WorkItemID: "yoyodyne-stopped",
		Outcome:    Outcome{Status: runstate.StatusFailed, Blocked: true},
		Failure:    "the repair budget was spent",
	}}}.Render()
	if !strings.Contains(rendered, "yoyodyne-stopped: stopped") {
		t.Errorf("the pass reads:\n%s\nwant the stoppage named as one", rendered)
	}
}

// integratedOnto is a promotion onto one branch, which is all the pass's own
// account of a run reads from it.
func integratedOnto(branch string) *gitworktree.Integration {
	return &gitworktree.Integration{TargetBranch: branch}
}

// Stopped work reaches the development manager from the pass rather than from
// somebody carrying it to her, and what the pass did about it is on the schedule
// beside what it pulled.
func TestAPassPutsStoppedWorkToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		if passes > 1 {
			return EscalationSweep{}, nil
		}
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			DocketKey:  "stopped_run:run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			Decision:   "repair",
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Escalated) != 1 || !schedule.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want the stoppage this pass delivered", schedule.Escalated)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the pass to have gone on choosing work", schedule.Started)
	}
	if !strings.Contains(schedule.Render(), "put the stoppage of run run-0123456789abcdef0123456789abcdef") {
		t.Fatalf("rendered = %q, want the delivery said beside what the pass pulled", schedule.Render())
	}
}

// A delivery that failed costs the pass nothing it was doing, so it is reported
// beside the pull rather than stopping it — and never left unsaid.
func TestADeliveryThatFailedDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{}, errors.New("the conversation could not be opened")
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %#v, want a pass that carried on choosing work", schedule)
	}
	if !strings.Contains(schedule.EscalationProblem, "waiting on somebody carrying it to her") {
		t.Fatalf("escalation problem = %q, want the failed delivery said out loud", schedule.EscalationProblem)
	}
	if !strings.Contains(schedule.Render(), "could not be opened") {
		t.Fatalf("rendered = %q, want the failure in the pass's own account", schedule.Render())
	}
}

// Held intake stops the harness choosing work. It does not stop stopped work
// reaching the development manager, because the judgment a held queue is waiting
// on is exactly what that delivery produces.
func TestAHeldPassStillPutsStoppedWorkToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		Reason:        "three runs blocked in a row",
	}
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleIntakeHeld || len(schedule.Started) != 0 {
		t.Fatalf("schedule = %#v, want a held pass that chose nothing", schedule)
	}
	if len(schedule.Escalated) != 1 {
		t.Fatalf("escalated = %#v, want the stoppage delivered while intake was held", schedule.Escalated)
	}
}

// Held intake does not stop a program manager instance's pass any more than it
// stops a recurring task: a pass chooses no work, and a held queue is often
// waiting on exactly the look a pass takes. The pause is what stops one.
func TestAHeldPassStillFiresAProgramManagersPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		Reason:        "three runs blocked in a row",
	}
	harness.fire = func(_ *scheduleHarness, passes int) (RecurringSweep, error) {
		if passes > 1 {
			return RecurringSweep{}, nil
		}
		return RecurringSweep{Fired: []Fired{{
			Task:   "reliability-pm",
			Role:   domain.RoleProgramManager,
			Turns:  1,
			Events: map[string]int{"stoppages": 3},
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleIntakeHeld || len(schedule.Started) != 0 {
		t.Fatalf("schedule = %#v, want a held pass that chose nothing", schedule)
	}
	if len(schedule.Fired) != 1 || schedule.Fired[0].Task != "reliability-pm" {
		t.Fatalf("fired = %#v, want the instance's pass fired while intake was held", schedule.Fired)
	}
	if !strings.Contains(schedule.Render(), "carried 3 stoppages") {
		t.Errorf("rendered = %q, want what the pass carried on the pass's account", schedule.Render())
	}
}

// A delivery that keeps failing is one problem rather than a thousand. A
// watching session polls all night, so a pass that accumulated every failed
// attempt would report the same sentence once a minute and bury what it did.
func TestRepeatedDeliveryFailuresAreOneProblemOnThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Problem:    fmt.Sprintf("the conversation could not be opened, attempt %d", passes),
		}}}, nil
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	scheduler.escalate(context.Background(), &schedule, pull)

	if len(schedule.Escalated) != 0 {
		t.Fatalf("escalated = %#v, want a delivery that did not happen kept as a problem rather than an event", schedule.Escalated)
	}
	if schedule.EscalationProblem != "the conversation could not be opened, attempt 2" {
		t.Fatalf("escalation problem = %q, want the latest attempt and only it", schedule.EscalationProblem)
	}

	// And a pass that gets through says so, rather than going on reporting a
	// failure that is over.
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			Decision:   "repair",
		}}}, nil
	}
	scheduler.escalate(context.Background(), &schedule, pull)
	if len(schedule.Escalated) != 1 || schedule.EscalationProblem != "" {
		t.Fatalf("schedule = %#v, want the delivery kept and the failure behind it cleared", schedule)
	}
}

// firedTasks is a schedule of recurring tasks that has already decided what this
// pass will find, so the wiring between a pull and its trigger can be driven
// without a provider, a conversation, or a clock.
type firedTasks struct {
	sweep RecurringSweep
	err   error
}

func (f firedTasks) Fire(context.Context) (RecurringSweep, error) { return f.sweep, f.err }

// The item's central claim is that the sweep runs from configuration, and this is
// the join that makes it true: a pull carrying a trigger puts what fired onto the
// schedule and charges what it cost to the session.
//
// Both halves are silent when they break. A pass that never called the trigger
// looks exactly like a schedule with nothing due, and a firing whose cost never
// reached SpentUSD is a session spending past a --budget it was given while every
// surface reports it inside one.
func TestAFiredTaskReachesTheScheduleWithItsCost(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{}
	pull := Pull{Recurring: firedTasks{sweep: RecurringSweep{Fired: []Fired{{
		Task:          "development-manager-sweep",
		Role:          domain.RoleDevelopmentManager,
		Turns:         2,
		CostUSD:       0.25,
		Findings:      3,
		SilentRepairs: 1,
	}}}}}
	schedule := Schedule{}

	scheduler.fire(context.Background(), &schedule, pull)

	if len(schedule.Fired) != 1 {
		t.Fatalf("fired = %#v, want the firing on the pass", schedule.Fired)
	}
	if schedule.Fired[0].Task != "development-manager-sweep" || schedule.Fired[0].Turns != 2 {
		t.Errorf("fired = %#v, want the task and its turns as the trigger reported them", schedule.Fired[0])
	}
	if schedule.Fired[0].SilentRepairs != 1 {
		t.Errorf("silent repairs = %d, want the count carried to the pass", schedule.Fired[0].SilentRepairs)
	}
	if schedule.SpentUSD != 0.25 {
		t.Errorf("spent = %v, want the firing's turns charged to the session", schedule.SpentUSD)
	}
	if schedule.RecurringProblem != "" {
		t.Errorf("recurring problem = %q, want none for a firing that worked", schedule.RecurringProblem)
	}
}

// A pull with nothing due says nothing and spends nothing, which is almost every
// pull on a healthy harness. A pass that reported an empty firing would put a
// line into every poll of a watching session for the rest of the night.
func TestAPassWithNothingDueSaysNothingAboutTheSchedule(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{}
	schedule := Schedule{}

	scheduler.fire(context.Background(), &schedule, Pull{Recurring: firedTasks{}})

	if len(schedule.Fired) != 0 || schedule.RecurringProblem != "" || schedule.SpentUSD != 0 {
		t.Fatalf("schedule = %#v, want a pass with nothing due to leave it alone", schedule)
	}
}

// A pull wired without a trigger is every project that has scheduled nothing, and
// it pulls exactly as it did before the schedule existed.
func TestAPullWithoutATriggerIsUnchanged(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{}
	schedule := Schedule{}

	scheduler.fire(context.Background(), &schedule, Pull{})

	if len(schedule.Fired) != 0 || schedule.RecurringProblem != "" {
		t.Fatalf("schedule = %#v, want a pull with no schedule to say nothing about one", schedule)
	}
}

// A firing that failed is a problem on the pass rather than an event, and what it
// spent is still charged: the provider bills a turn that failed exactly as it
// bills one that answered, so a session counting against a budget has to see it.
func TestAFailedFiringIsAProblemThatStillCosts(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{}
	pull := Pull{Recurring: firedTasks{sweep: RecurringSweep{Fired: []Fired{{
		Task:    "development-manager-sweep",
		Role:    domain.RoleDevelopmentManager,
		Turns:   0,
		CostUSD: 0.05,
		Problem: "the conversation could not be opened",
	}}}}}
	schedule := Schedule{}

	scheduler.fire(context.Background(), &schedule, pull)

	if len(schedule.Fired) != 0 {
		t.Fatalf("fired = %#v, want a firing that took no turn kept as a problem rather than an event", schedule.Fired)
	}
	if schedule.RecurringProblem != "the conversation could not be opened" {
		t.Errorf("recurring problem = %q, want what stopped the firing", schedule.RecurringProblem)
	}
	if schedule.SpentUSD != 0.05 {
		t.Errorf("spent = %v, want a failed turn charged like any other", schedule.SpentUSD)
	}
}

// A schedule that could not be read at all is said on the pass rather than
// stopping it: a firing that failed costs the pull nothing it was doing.
func TestAScheduleThatCouldNotBeFiredIsSaidRatherThanStoppingThePass(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{}
	schedule := Schedule{}

	scheduler.fire(context.Background(), &schedule, Pull{Recurring: firedTasks{err: errors.New("the claim could not be taken")}})

	if !strings.Contains(schedule.RecurringProblem, "the claim could not be taken") {
		t.Errorf("recurring problem = %q, want the failure named on the pass", schedule.RecurringProblem)
	}
	if len(schedule.Fired) != 0 {
		t.Errorf("fired = %#v, want nothing recorded as having fired", schedule.Fired)
	}
}

// The tests above drive fire with a Pull built by hand, which leaves the one
// thing the item's central claim actually rests on untested: that a pass carrying
// a trigger calls it at all. Every full-pull test here passes a Pull with no
// Recurring, so fire returns at its first line in all of them — and a pull that
// never reached the trigger looks exactly like a schedule with nothing due, which
// is the failure mode this whole item exists to eliminate.
//
// So this drives the pulling loop itself: an ordinary drain over an ordinary
// queue, with a task due on its first pull.
func TestADrainingPassFiresTheScheduleItWasWiredWith(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	// Due on the first pull and not on the ones after it, which is what a cadence
	// does: the claim is the due check, and a task already claimed this hour is
	// passed over rather than fired again.
	harness.fire = func(_ *scheduleHarness, passes int) (RecurringSweep, error) {
		if passes > 1 {
			return RecurringSweep{}, nil
		}
		return RecurringSweep{Fired: []Fired{{
			Task:          "development-manager-sweep",
			Role:          domain.RoleDevelopmentManager,
			Turns:         2,
			CostUSD:       0.25,
			Findings:      3,
			SilentRepairs: 1,
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.firings == 0 {
		t.Fatalf("firings = 0, want the pass to have asked the trigger it was wired with: %s", schedule.Render())
	}
	if len(schedule.Fired) != 1 {
		t.Fatalf("fired = %#v, want the firing on the pass's own schedule", schedule.Fired)
	}
	if schedule.Fired[0].Task != "development-manager-sweep" || schedule.Fired[0].Turns != 2 {
		t.Errorf("fired = %#v, want the task and its turns as the trigger reported them", schedule.Fired[0])
	}
	if schedule.SpentUSD != 0.25 {
		t.Errorf("spent = %v, want the firing's turns charged to the session that took them", schedule.SpentUSD)
	}
	if schedule.RecurringProblem != "" {
		t.Errorf("recurring problem = %q, want none for a firing that worked", schedule.RecurringProblem)
	}
	// The firing is beside the work rather than instead of it: a pass that woke a
	// role still drains the queue it was pulling.
	if len(schedule.Started) != 1 {
		t.Errorf("started = %d, want the ready item run as it would have been: %s", len(schedule.Started), schedule.Render())
	}
}

// And a pass wired with no schedule is every project that has configured no
// recurring task: it pulls exactly as it did before this existed, and says
// nothing about a schedule it does not have.
func TestADrainingPassWithNoScheduleSaysNothingAboutOne(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.firings != 0 {
		t.Errorf("firings = %d, want a pull with no trigger to ask for none", harness.firings)
	}
	if len(schedule.Fired) != 0 || schedule.RecurringProblem != "" {
		t.Errorf("schedule = %#v, want nothing said about a schedule that was never wired", schedule)
	}
	if len(schedule.Started) != 1 {
		t.Errorf("started = %d, want the ready item run: %s", len(schedule.Started), schedule.Render())
	}
}

// A pass does not erase its own account of stopped work. A pull that finds
// nothing to say about a stoppage is not evidence that the last one's failure
// was resolved — the stoppage may be waiting out its retry delay — so what the
// pass said stands until a delivery actually happens.
func TestAPassDoesNotEraseWhatItSaidAboutStoppedWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		switch passes {
		case 1:
			return EscalationSweep{Escalated: []Escalated{{
				WorkItemID: "yoyodyne-stopped",
				RunID:      "run-0123456789abcdef0123456789abcdef",
				Problem:    "the provider refused the turn on attempt 1 of 3",
			}}}, nil
		case 2, 3:
			// The pulls a drain makes while the stoppage waits out its delay.
			return EscalationSweep{}, nil
		default:
			return EscalationSweep{Escalated: []Escalated{{
				WorkItemID: "yoyodyne-stopped",
				RunID:      "run-0123456789abcdef0123456789abcdef",
				Delivered:  true,
				Decision:   "repair",
			}}}, nil
		}
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	if schedule.EscalationProblem == "" {
		t.Fatal("the pass said nothing about a delivery that failed")
	}
	for pass := 2; pass <= 3; pass++ {
		scheduler.escalate(context.Background(), &schedule, pull)
		if schedule.EscalationProblem == "" {
			t.Fatalf("pass %d erased what the pass had already said about stopped work", pass)
		}
	}

	// And a delivery that actually happened is what clears it.
	scheduler.escalate(context.Background(), &schedule, pull)
	if schedule.EscalationProblem != "" || len(schedule.Escalated) != 1 {
		t.Fatalf("schedule = %#v, want the delivery kept and the failure behind it cleared", schedule)
	}
}

// A delivery failure the sweep meets again is said again, so a pass that ends
// hours later still names what is still true rather than what one sweep
// happened to find last.
//
// What the sweep no longer hands up is a stoppage the harness has given up
// delivering: that one is on every operator surface as work held for a person,
// and restating it per pull buried what the pass had done. See the escalator.
func TestADeliveryFailureIsSaidByEveryPassThatMeetsIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Problem:    "the stoppage of run run-0123456789abcdef0123456789abcdef could not be put to the development manager: the conversation could not be opened",
		}}}, nil
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	for pass := 1; pass <= 3; pass++ {
		scheduler.escalate(context.Background(), &schedule, pull)
		if !strings.Contains(schedule.EscalationProblem, "could not be opened") {
			t.Fatalf("pass %d says %q, want the failure that is still happening still named", pass, schedule.EscalationProblem)
		}
	}
	// Said once, however many passes have found it: what a reader needs is the
	// standing fact rather than one line per pull.
	if strings.Count(schedule.EscalationProblem, "could not be opened") != 1 {
		t.Fatalf("the pass says %q, want the standing fact once rather than once per pull", schedule.EscalationProblem)
	}
	if len(schedule.Escalated) != 0 {
		t.Fatalf("escalated = %#v, want nothing reported as delivered", schedule.Escalated)
	}
}

// A delivery is a spend the pass makes itself, so it counts against the bound
// the session was given. A turn is not a run and nothing else in the pass would
// see it, and a session that spent past its cap on turns nobody counted is the
// operator's cap disappearing quietly.
func TestWhatADeliveryCostCountsAgainstTheSessionsBudget(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  passes == 1,
			CostUSD:    0.40,
			// The second pass is a turn that failed, which the provider charged
			// for exactly as it charged for the first.
			Problem: map[bool]string{true: "", false: "the reply could not be read"}[passes == 1],
		}}}, nil
	}
	scheduler := Scheduler{Budget: 1}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	scheduler.escalate(context.Background(), &schedule, pull)

	if schedule.SpentUSD != 0.80 {
		t.Fatalf("spent = %.2f, want both turns counted against the session", schedule.SpentUSD)
	}
}

// And a session stops on its budget over deliveries alone. A watching session
// with an empty queue starts nothing and used to spend nothing; it can now spend
// a turn per poll, so the bound has to be able to end it on that spend by itself.
func TestASessionStopsOnItsBudgetOverDeliveriesAlone(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			CostUSD:    0.40,
		}}}, nil
	}
	// The session keeps polling; what stops it is the bound rather than the
	// operator.
	harness.onSleep = func(*scheduleHarness, int) bool { return true }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Budget: 1, Sleep: harness.sleep, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the session stopped on its budget", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want a session that spent its budget on deliveries alone", schedule.Started)
	}
	if schedule.SpentUSD < 1 {
		t.Fatalf("spent = %.2f, want the deliveries counted up to the bound", schedule.SpentUSD)
	}
}

// haltedWork is the harness's durable account of the work it stopped: the runs
// it recorded and the stoppages it put in front of the development manager. It
// is what tells a blocked status somebody still has to release from one whose
// blockers have all closed.
type haltedWork struct {
	runs        []runstate.State
	escalations []runstate.Escalation
}

func (h haltedWork) Recorded() ([]runstate.State, error) { return h.runs, nil }

func (h haltedWork) Escalated() ([]runstate.Escalation, error) { return h.escalations, nil }

// The idle morning of 2026-09-04, replayed at the grain the scheduler reads at.
// Two items sit at status blocked with no unfinished dependency between them.
// One of them stopped last night and its change is still on a branch; the other
// was blocked months ago on work that has since landed, and nothing rewrote the
// field when it did. The status says the same word about both, which is why the
// scheduler asks the records instead: it starts the released one and passes over
// the held one, naming what holds it.
func TestSchedulerStartsBlockedWorkNothingIsHoldingAndPassesOverAStoppage(t *testing.T) {
	t.Parallel()

	stopped := beads.WorkItem{
		ID: "yoyodyne-ifd.153", Title: "Guard the notes writer", Status: "blocked", Priority: 0,
	}
	released := beads.WorkItem{
		ID: "yoyodyne-ifd.117.1", Title: "Split the configuration reference", Status: "blocked", Priority: 1,
		// The blocker closed as the week's work landed, so it has left the backlog.
		// The dependency is still listed, exactly as Beads lists it.
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.117", Type: "blocks"}},
	}
	harness := newScheduleHarness(stopped, released)
	// Neither is on the tracker's ready list, because that list is computed from
	// the same status field. That is the whole of what hid them.
	harness.ReadyItems = map[string]bool{}
	harness.stoppages = haltedWork{runs: []runstate.State{{
		RunID:        "run-5035c832",
		WorkItemID:   stopped.ID,
		Status:       runstate.StatusFailed,
		UpdatedAt:    harness.now.Add(-8 * time.Hour),
		Branch:       "yoyodyne/yoyodyne-ifd-153/5035c832",
		WorktreePath: "/state/worktrees/yoyodyne-ifd-153-5035c832",
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != released.ID {
		t.Fatalf("started = %#v, want the released item pulled: %s", schedule.Started, schedule.Render())
	}
	// The stoppage keeps its place in the order and is passed over with what holds
	// it named, rather than being started on top of the change it left behind.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != stopped.ID {
		t.Fatalf("deferred = %#v, want the stoppage named rather than counted among the unready", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "run-5035c832") {
		t.Fatalf("deferred reason = %q, want the preserved change named", schedule.Deferred[0].Reason)
	}
	// The released item has left the backlog by being pulled, and the stoppage is
	// still admitted work in the product manager's order. What it is not is
	// pullable, which is a different thing from being hidden.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want the stoppage queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
}

// yoyodyne-ifd.295, at the grain the scheduler reads at. The item is open, the
// tracker reports it as ready, and nothing is wrong with it except that its work
// is already on main: a run integrated the change and only the publication of it
// did not finish. Pulling it started a developer three times over, each of them
// ending in a diagnosis that the change had already landed, so the pull passes
// over it and says why.
func TestSchedulerPassesOverAnItemWhoseOnlyOutstandingStateIsAPublication(t *testing.T) {
	t.Parallel()

	published := beads.WorkItem{
		ID: "yoyodyne-ifd.295", Title: "Stall detection runs without Slack", Status: "open", Priority: 2,
	}
	next := beads.WorkItem{
		ID: "yoyodyne-ifd.301", Title: "An item closes on the confirmed merge", Status: "open", Priority: 2,
	}
	harness := newScheduleHarness(published, next)
	harness.stoppages = haltedWork{runs: []runstate.State{{
		RunID:      "run-55443d4c",
		WorkItemID: published.ID,
		// The run succeeded and cleaned up after itself: no blocker, no branch, no
		// worktree. Nothing but the publication says anything is unfinished.
		Status:      runstate.StatusSucceeded,
		UpdatedAt:   harness.now.Add(-time.Hour),
		Integration: &runstate.Integration{TargetBranch: "main", SourceCommit: "b206ca1", TargetCommit: "b206ca1"},
		PullRequest: &runstate.PullRequest{Number: 428, Merged: true, MergeCommit: "f382df4"},
		PublishFailure: "delete the merged remote branch: resolve yoyodyne/yoyodyne-ifd-295/55443d4c on origin " +
			"failed with exit code 128: Read from remote host ssh.github.com: Connection reset by peer",
	}}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != next.ID {
		t.Fatalf("started = %#v, want the next item pulled rather than the published one: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != published.ID {
		t.Fatalf("deferred = %#v, want the outstanding publication named", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "only the publication is unfinished") {
		t.Fatalf("deferred reason = %q, want the publication named rather than a stoppage", schedule.Deferred[0].Reason)
	}
}

// And the safe direction when the records cannot be read at all: a pull wired
// without them holds every blocked item rather than releasing work whose hold it
// could not see. Holding a releasable item costs one pull; releasing a held one
// starts a run over a change that is still there.
func TestSchedulerHoldsBlockedWorkWhenNothingCanSayWhatIsHoldingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(beads.WorkItem{
		ID: "yoyodyne-ifd.117.1", Title: "Split the configuration reference", Status: "blocked", Priority: 1,
	})
	harness.ReadyItems = map[string]bool{}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || schedule.Pullable != 0 {
		t.Fatalf("started = %#v, pullable = %d, want nothing released on an unread hold: %s",
			schedule.Started, schedule.Pullable, schedule.Render())
	}
}

// The loop that chooses work is the one process that is running whenever the
// harness is choosing at all, so it is where the stall reading is taken from —
// once per pull, before anything is chosen, and deciding nothing about the pass.
//
// It is here rather than only in the sweep because of what each catches. A
// session that died writes nothing about that and is `yoyo reconcile`'s to
// notice; a session that is alive and has stopped starting anything is this
// loop's, and nothing outside it was watching for that without an operator's
// cron.
func TestAWatchingSessionTakesTheStallReadingOncePerPull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	// Two quiet polls and then the operator stops the session, so the loop makes
	// more than one pull with nothing to start — which is the shape a stalled
	// machine is in.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 2 }

	readings := 0
	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Sleep: harness.sleep,
		Watchdog: func(context.Context) { readings++ },
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	harness.mu.Lock()
	pulls := harness.pulls
	harness.mu.Unlock()
	if readings == 0 {
		t.Fatal("the session took no stall reading at all, so a harness that stopped choosing would go unnoticed")
	}
	if readings != pulls {
		t.Fatalf("the session took %d reading(s) over %d pull(s), want one per pull", readings, pulls)
	}
	// It decides nothing: the pass ends exactly as a pass wired without one does.
	if schedule.Stopped != ScheduleCancelled {
		t.Fatalf("stopped = %q, want the session ended by its operator", schedule.Stopped)
	}
}

// RecordUnreadyItem stands in for routing an item the tree is not ready for to
// the development manager's docket, and records the key the durable docket would
// hold it under so a test can say that repeated pulls are one entry.
func (h *scheduleHarness) RecordUnreadyItem(item beads.WorkItem, unmet []readiness.Unmet) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.routeErr != nil {
		return false, h.routeErr
	}
	key := triage.UnreadyKey(item.ID, readiness.Kinds(unmet))
	for _, already := range h.docketed {
		if already == key {
			return false, nil
		}
	}
	h.docketed = append(h.docketed, key)
	return true, nil
}

// SettleUnreadyItems stands in for taking an unready entry off the docket: a key
// whose item the pull no longer finds unready for the same kinds is dropped.
func (h *scheduleHarness) SettleUnreadyItems(reread func(workItemID string) UnreadyReading) (int, error) {
	h.mu.Lock()
	docketed := append([]string(nil), h.docketed...)
	h.mu.Unlock()
	var kept []string
	settled := 0
	for _, key := range docketed {
		id := strings.Split(key, ":")[1]
		reading := reread(id)
		if reading.Unreadable || (reading.Present && triage.UnreadyKey(id, readiness.Kinds(reading.Unmet)) == key) {
			kept = append(kept, key)
			continue
		}
		settled++
	}
	h.mu.Lock()
	h.docketed = kept
	h.mu.Unlock()
	return settled, nil
}

// harnessDocketClock is the harness's own clock, for a real docketer wired into
// its pulls: the fake sleep moves it, so an entry recorded at one pull and the
// closure made at the next are in the order they happened.
type harnessDocketClock struct{ h *scheduleHarness }

func (c harnessDocketClock) Now() time.Time { return c.h.clock() }

// RecordUnstartedAttempt stands in for docketing a dispatch that failed before
// any run record existed, and keeps what it was handed so a test can say what the
// durable record would hold.
func (h *scheduleHarness) RecordUnstartedAttempt(attempt UnstartedAttempt) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attemptErr != nil {
		return false, h.attemptErr
	}
	h.attempts = append(h.attempts, attempt)
	return true, nil
}

// recordedAttempts is what this harness was asked to docket, read under the lock
// because the session records them from its own goroutine while a test reads
// them from another.
func (h *scheduleHarness) recordedAttempts() []UnstartedAttempt {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]UnstartedAttempt(nil), h.attempts...)
}

// citingTree is the tree as a test says it stands: the symbols it declares and
// the files it has, and nothing else. It is a stand-in rather than a checkout
// because what these tests are about is what the scheduler does with the answer;
// the answers themselves are the readiness package's own tests, which read real
// files.
type citingTree struct {
	declares map[string]bool
	files    map[string]int
	failure  error
}

func (t citingTree) File(path string) (int, bool, error) {
	if t.failure != nil {
		return 0, false, t.failure
	}
	lines, present := t.files[path]
	return lines, present, nil
}

func (t citingTree) Declares(symbol string) (bool, error) {
	if t.failure != nil {
		return false, t.failure
	}
	return t.declares[symbol], nil
}

// The four shapes that cost a run each in a fortnight, replayed against a
// scheduler that reads the tree: a pinpoint naming code the tree no longer has,
// an item that says it inherits machinery nothing has landed, one that states its
// own activation conditions, and one written blocked on a decision. Every one of
// them is reported by the tracker as ready to pull, because the tracker knows
// about dependency links and not about a sentence.
//
// None is dispatched, each is named with what is missing and who releases it,
// and each reaches the development manager's docket rather than a run.
func TestSchedulerDoesNotDispatchAnItemTheTreeIsNotReadyFor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		item     beads.WorkItem
		required []string
	}{
		{
			name: "stale pinpoint",
			item: beads.WorkItem{
				ID: "yoyodyne-ifd.291", Title: "Configuration fails closed on an unrecognized role", Status: "open",
				Description: "The implementation pinpoint: domain.Backend.SupportsRole returns true for every role.",
			},
			required: []string{"stale-pinpoint", "domain.Backend.SupportsRole", "the Lead Product Manager"},
		},
		{
			name: "machinery on a branch",
			item: beads.WorkItem{
				ID: "yoyodyne-ifd.284", Title: "The product manager's coherence scan", Status: "open",
				Description: "The second recurring-task consumer, shipping after the DM hourly sweep and inheriting its machinery.",
			},
			required: []string{"machinery-on-a-branch", "inheriting its machinery", "development manager"},
		},
		{
			name: "subject not in the repository",
			item: beads.WorkItem{
				ID: "yoyodyne-ifd.209.14", Title: "Convert the coordination slice to the workflow runtime", Status: "open",
				Description: "Not decomposable further until the runtime exists and the management-conversion design lands.",
			},
			required: []string{"subject-not-in-repository", "Not decomposable further until the runtime exists"},
		},
		{
			name: "forbidden by a ruling",
			item: beads.WorkItem{
				ID: "yoyodyne-ifd.100.1", Title: "Commit and publish an approved artifact write", Status: "open",
				Description: "Blocked until the architect's answer exists; the answer is recorded by the operator.",
			},
			required: []string{"forbidden-by-ruling", "Blocked until the architect's answer exists"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			harness := newScheduleHarness(test.item)
			harness.tree = citingTree{}

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 0 {
				t.Fatalf("started = %#v, want an item the tree is not ready for never dispatched: %s", schedule.Started, schedule.Render())
			}
			if schedule.ReadinessProblem != "" {
				t.Fatalf("readiness problem = %q, want the reading to have been made", schedule.ReadinessProblem)
			}
			if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != test.item.ID {
				t.Fatalf("deferred = %#v, want the item named rather than counted among the unready", schedule.Deferred)
			}
			reason := schedule.Deferred[0].Reason
			for _, required := range test.required {
				if !strings.Contains(reason, required) {
					t.Fatalf("deferred reason = %q, want it to contain %q", reason, required)
				}
			}
			// The other half of the criterion: it goes to triage rather than only
			// into one pass's output, so it is still there when this session is over.
			if len(harness.docketed) != 1 {
				t.Fatalf("docketed = %#v, want the unmet prerequisite routed to the development manager", harness.docketed)
			}
			if !strings.Contains(harness.docketed[0], test.item.ID) {
				t.Fatalf("docket key = %q, want it to name the item", harness.docketed[0])
			}
		})
	}
}

// The half that has to hold for the guard to be worth having: an item whose
// citation the tree meets is dispatched exactly as it was, and nothing is
// docketed about it.
func TestSchedulerDispatchesAnItemTheTreeIsReadyFor(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.304", Title: "Readiness checks prerequisites against the tree", Status: "open",
		Description: "The check is readiness.Check, read at dispatch.",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{declares: map[string]bool{"readiness.Check": true}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != item.ID {
		t.Fatalf("started = %#v, want the item dispatched: %s", schedule.Started, schedule.Render())
	}
	if len(harness.docketed) != 0 || len(schedule.Deferred) != 0 {
		t.Fatalf("docketed = %#v, deferred = %#v, want nothing said about a ready item", harness.docketed, schedule.Deferred)
	}
}

// A watching session re-reads the queue every interval, so an item that stays
// unready is met again on every poll. It reaches the development manager once:
// the docket is keyed to the item and what was found, not to the reading.
func TestAnUnreadyItemReachesTriageOncePerFinding(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.100.1", Title: "Commit and publish an approved artifact write", Status: "open",
		Description: "Blocked until the architect's answer exists.",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.sleeps < 3 {
		t.Fatalf("sleeps = %d, want the session to have polled more than once", harness.sleeps)
	}
	if len(harness.docketed) != 1 {
		t.Fatalf("docketed = %#v, want one entry however many polls met the item", harness.docketed)
	}
	if len(schedule.Deferred) != 1 {
		t.Fatalf("deferred = %#v, want the item named once rather than once per poll", schedule.Deferred)
	}
}

// yoyodyne-ifd.298, replayed. Its description said it did not start before a
// design landed, weeks after the design had; the pull passed it over at priority
// 1 and the only surface that said so was the development manager's docket. The
// product manager removed the sentence, and the docket went on quoting it until
// the item had long since closed.
//
// Here the item is amended between two pulls of one watching session. The next
// pull takes it; the docket entry is taken off in the same pull, by the harness,
// saying why; and the product manager was told once, in a report naming the item
// and the sentence, rather than by whoever read the docket and relayed it.
func TestAnItemAmendedUnderASentenceRefusalIsTakenAtTheNextPull(t *testing.T) {
	t.Parallel()

	const sentence = "This item does not start before 282's design lands and should ride its implementation wave"
	item := beads.WorkItem{
		ID: "yoyodyne-ifd.298", Title: "yoyo agent memory: a role's memories read as text", Status: "open", Priority: 1,
		Description: "The reading surface for the per-role memory store. " + sentence + ".",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{}
	docket, reports := unreadyDocketFor(t, harness)
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.Items[0].Description = "The reading surface for the per-role memory store. The store exists and this reads it as it stands."
		h.now = h.now.Add(time.Minute)
		return true
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != item.ID {
		t.Fatalf("started = %#v, want the amended item taken at the pull after the amendment: %s", schedule.Started, schedule.Render())
	}
	if harness.sleeps < 1 {
		t.Fatal("the session never polled twice, so nothing here was read after the amendment")
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("read the docket: %v", err)
	}
	if len(entries) != 1 || entries[0].Class != triage.ClassUnreadyItem {
		t.Fatalf("docket = %+v, want the one unready entry the first pull made", entries)
	}
	if !strings.Contains(entries[0].Unready.Prerequisites[0].Missing, "its description says of it") {
		t.Fatalf("entry = %+v, want the refusal to name the field the sentence was in", entries[0].Unready.Prerequisites[0])
	}
	closed := entries[0].Closed
	if closed == nil || closed.Decision != clearedUnreadyDecision || !strings.Contains(closed.Reason, "asks for nothing the tree does not have") {
		t.Fatalf("closure = %+v, want the entry taken off by the pull that found the sentence gone", closed)
	}

	if len(reports.appended) != 1 {
		t.Fatalf("reports = %+v, want the refusal said to the product manager once", reports.appended)
	}
	said := reports.appended[0]
	if said.Role != report.HarnessReporter || said.WorkItemID != item.ID || said.RunID != entries[0].Key {
		t.Fatalf("report = %+v, want it filed by the harness, on the item, leading back to the docket entry", said)
	}
	if !strings.Contains(said.Message, item.ID) || !strings.Contains(said.Message, sentence) {
		t.Fatalf("report message = %q, want the item and the sentence named", said.Message)
	}
}

// The sentence moved rather than removed: the product manager took it out of the
// description, and the same words stand in the design guidance, which her update
// does not rewrite. The next pull still refuses the item — the words are there —
// but in the words it now carries and naming the field they are in, and the
// entry quoting the description is taken off rather than left standing under
// words the item no longer says.
func TestASentenceThatSurvivesInAnotherFieldIsRefusedNamingThatField(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.298", Title: "yoyo agent memory", Status: "open", Priority: 1,
		Description: "The reading surface. It does not start before 282's design lands.",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{}
	docket, reports := unreadyDocketFor(t, harness)
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.Items[0].Description = "The reading surface."
		h.Items[0].Design = "Build on the store. It does not start before 282's design lands."
		h.now = h.now.Add(time.Minute)
		return true
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want the item still refused while the sentence stands anywhere it authored", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || !strings.Contains(schedule.Deferred[0].Reason, "its design guidance, which the Lead Product Manager's update does not rewrite,") {
		t.Fatalf("deferred = %#v, want the last pull's refusal to name the field the sentence survives in", schedule.Deferred)
	}

	entries, err := docket.List()
	if err != nil {
		t.Fatalf("read the docket: %v", err)
	}
	if len(entries) != 1 || entries[0].Closed != nil {
		t.Fatalf("docket = %+v, want one standing entry for the item", entries)
	}
	if missing := entries[0].Unready.Prerequisites[0].Missing; !strings.Contains(missing, "its design guidance") {
		t.Fatalf("standing entry quotes %q, want the words the item carries now", missing)
	}
	closures, err := docket.Closures()
	if err != nil {
		t.Fatalf("read the docket's closures: %v", err)
	}
	if len(closures[entries[0].Key]) != 1 || !strings.Contains(closures[entries[0].Key][0].Reason, "no longer what this entry quotes") {
		t.Fatalf("closures = %+v, want the entry quoting the description taken off as restated", closures)
	}
	if len(reports.appended) != 2 || !strings.Contains(reports.appended[1].Message, "design guidance") {
		t.Fatalf("reports = %+v, want the product manager told again, of the field the words now stand in", reports.appended)
	}
}

// unreadyDocketFor wires a real docket and a report pile into a harness's pulls,
// so what a test reads back is what the durable records would hold.
func unreadyDocketFor(t *testing.T, harness *scheduleHarness) (*runstate.DocketStore, *fakeReports) {
	t.Helper()
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	reports := &fakeReports{}
	harness.triage = &Docketer{
		Docket:       docket,
		Runs:         recordedRuns{},
		Decisions:    &recordedDecisions{},
		Reruns:       &recordedDecisions{},
		Caps:         docketedCaps,
		Triage:       docketedTriage,
		ProductID:    "yoyodyne",
		Reports:      reports,
		RepositoryID: "yoyodyne",
		Clock:        harnessDocketClock{harness},
	}
	return docket, reports
}

// A tree that cannot be read says nothing about the item. Holding work back for
// a reading that failed would convert an unreadable repository into a stopped
// line, which is a worse failure than the one this guards against — so the item
// is dispatched exactly as it would have been, and the failed reading is said.
func TestATreeThatCannotBeReadDoesNotHoldWorkBack(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.304", Status: "open",
		Title: "Readiness checks prerequisites against the tree", Description: "The check is readiness.Check.",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{failure: errors.New("the repository could not be walked")}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want an unreadable tree to hold nothing back: %s", schedule.Started, schedule.Render())
	}
	if !strings.Contains(schedule.ReadinessProblem, "could not be read in full") {
		t.Fatalf("readiness problem = %q, want the failed reading reported", schedule.ReadinessProblem)
	}
	if !strings.Contains(schedule.Render(), "could not be read in full") {
		t.Fatalf("rendered = %q, want the failed reading readable by an operator", schedule.Render())
	}
}

// Where the finding cannot be made durable, the refusal still stands. Dispatching
// an item the tree cannot serve in order to avoid losing a line of the record
// would spend a run to save a sentence.
func TestAnUnreadyItemWithNowhereToRouteIsStillNotDispatched(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID: "yoyodyne-ifd.100.1", Status: "open", Title: "Commit and publish an approved artifact write",
		Description: "Blocked until the architect's answer exists.",
	}
	harness := newScheduleHarness(item)
	harness.tree = citingTree{}
	harness.unroutable = true

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want the refusal to stand without the record: %s", schedule.Started, schedule.Render())
	}
	if !strings.Contains(schedule.ReadinessProblem, "nowhere durable") {
		t.Fatalf("readiness problem = %q, want the missing record reported", schedule.ReadinessProblem)
	}
}

// The 2026-09-07 shape, from the pull that records it. Two items are held and
// neither is a wait for anything, and that is where they stop resembling each
// other: one stoppage nobody has decided about is the development manager's, and
// one she decided days ago is the harness's to carry out. Recording both as one
// class is what made thirty-three already-decided items read as a decision
// backlog for days.
func TestAPollPassesADecidedStoppageOverSeparatelyFromAnUndecidedOne(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-ifd.150", Title: "Decided days ago", Status: "blocked", Priority: 1},
		beads.WorkItem{ID: "yoyodyne-ifd.151", Title: "Nobody has decided", Status: "blocked", Priority: 2},
	)
	harness.stoppages = heldStoppages{runs: []runstate.State{
		stoppedRunOf("run-aaaa1111", "yoyodyne-ifd.150", stopped),
		stoppedRunOf("run-bbbb2222", "yoyodyne-ifd.151", stopped),
	}}
	harness.decisions = decidedItems{"yoyodyne-ifd.150": {
		Decisions: []runstate.TriageDecision{{
			Decision: runstate.TriageDecisionRerun, RunID: "run-aaaa1111",
		}},
	}}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded, so nothing said what the poll found")
	}
	want := runstate.PassedOver{Admitted: 2, Groups: []runstate.PassedOverGroup{
		{Class: runstate.PassedOverAwaitingCarryOut, Count: 1, Items: []string{"yoyodyne-ifd.150"}},
		{Class: runstate.PassedOverAwaitingDecision, Count: 1, Items: []string{"yoyodyne-ifd.151"}},
	}}
	if !reflect.DeepEqual(idle.passedOver, want) {
		t.Fatalf("idle passed over = %+v, want %+v", idle.passedOver, want)
	}
	// And the prose the same poll writes says which is which, so the log reads the
	// way the classes do.
	for _, said := range []string{
		"awaiting carry-out of a decision (yoyodyne-ifd.150)",
		"awaiting a decision (yoyodyne-ifd.151)",
	} {
		if !strings.Contains(idle.reason, said) {
			t.Fatalf("idle reason = %q, want it to say %q", idle.reason, said)
		}
	}
}

// heldStoppages is the harness's own record of the work it stopped, for the
// tests that need a held queue rather than a blocked one.
type heldStoppages struct {
	runs []runstate.State
}

func (h heldStoppages) Recorded() ([]runstate.State, error) { return h.runs, nil }

func (h heldStoppages) Escalated() ([]runstate.Escalation, error) { return nil, nil }

// decidedItems is one item's triage record for the items it names, and the
// empty record every other item actually stands at.
type decidedItems map[string]runstate.TriageCounters

func (d decidedItems) Counters(workItemID string) (runstate.TriageCounters, error) {
	return d[workItemID], nil
}

// stoppedRunOf is a run that stopped on a durable blocker and left its change
// behind, which is the shape that holds an item for a person.
func stoppedRunOf(runID, workItemID string, stopped time.Time) runstate.State {
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusFailed,
		UpdatedAt:    stopped,
		Branch:       "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath: "/state/worktrees/" + runID,
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

// The 06:25Z shape of 2026-09-13, replayed: a watch session pulls two items four
// hours into a returned capacity window, both dispatches fail before anything
// reserves a run, and the session then excludes both for the rest of its life.
//
// What it left then was nothing. The runs directory had not been written to in
// five days, the two items were passed over as "already tried this session" with
// no reason against them, and the queue behind them — seventy-four items — sat
// idle until a person noticed. So the replay has to leave three things: a
// durable record of both attempts and what stopped them, an exclusion list that
// names its reasons, and a session log a sweep can read the two failures from
// rather than seeing nothing at all.
func TestAnAttemptThatDiesBeforeItsRunRecordStillRecordsWhatHappened(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.353", "yoyodyne-ifd.354")...)
	harness.capacity = 2
	// A dispatch that dies before the reservation: the pipeline returns no run at
	// all, only the failure, and touches nothing in the tracker.
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, errors.New("repository is not ready for an isolated run: the primary checkout has uncommitted changes")
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want both attempts on the pass", schedule.Started)
	}
	for _, started := range schedule.Started {
		if started.Failure == "" || started.Outcome.RunID != "" {
			t.Fatalf("started = %+v, want an attempt that failed with no run behind it", started)
		}
	}
	if schedule.AttemptProblem != "" {
		t.Fatalf("attempt problem = %q, want both attempts recorded without one", schedule.AttemptProblem)
	}

	// (1) The durable record: both attempts, each saying what was tried, why it was
	// chosen, what stopped it, and that the session will not try it again.
	attempts := harness.recordedAttempts()
	if len(attempts) != 2 {
		t.Fatalf("recorded attempts = %+v, want both dispatches docketed", attempts)
	}
	recorded := map[string]UnstartedAttempt{}
	for _, attempt := range attempts {
		recorded[attempt.WorkItemID] = attempt
	}
	for _, id := range []string{"yoyodyne-ifd.353", "yoyodyne-ifd.354"} {
		attempt, found := recorded[id]
		if !found {
			t.Fatalf("recorded attempts = %+v, want %s among them", attempts, id)
		}
		if attempt.WorkItemTitle != id {
			t.Fatalf("attempt = %+v, want the item's title carried, since nothing else will say what was tried", attempt)
		}
		if !strings.Contains(attempt.Failure, "uncommitted changes") {
			t.Fatalf("attempt = %+v, want the failure that stopped the dispatch", attempt)
		}
		if attempt.SelectedBecause == "" || attempt.SelectedBecause != harness.selections[id].Reason {
			t.Fatalf("attempt = %+v, want the selection reason the run record would have carried", attempt)
		}
		if !attempt.ExcludedForTheSession {
			t.Fatalf("attempt = %+v, want the session-long exclusion said out loud", attempt)
		}
	}

	// (2) The exclusion list names its reasons: the poll that passed both over
	// says, against each item, that the dispatch failed before a run was recorded
	// and where the record of that now is.
	// The last idle poll rather than the first: the session says a start is in
	// flight until it ends, and what this is about is what it says once both have.
	idle, found := lastEntered(sessions, runstate.WatchIdle)
	if !found {
		t.Fatal("no idle transition was recorded, so nothing said what the session found")
	}
	if len(idle.passedOver.Groups) != 1 || idle.passedOver.Groups[0].Class != runstate.PassedOverAlreadyTried {
		t.Fatalf("idle passed over = %+v, want the two items passed over as already tried", idle.passedOver)
	}
	tried := idle.passedOver.Groups[0]
	if tried.Count != 2 || len(tried.Items) != 2 || len(tried.Reasons) != 2 {
		t.Fatalf("already-tried group = %+v, want both items named with a reason against each", tried)
	}
	for index, reason := range tried.Reasons {
		for _, want := range []string{
			"the dispatch failed before any run was recorded",
			"uncommitted changes",
			"docket",
		} {
			if !strings.Contains(reason, want) {
				t.Fatalf("reason for %s = %q, want it to say %q", tried.Items[index], reason, want)
			}
		}
	}
	// And the same account in words, which is what an operator reading the log
	// sees: each item, and beside it what excluded it.
	for _, id := range []string{"yoyodyne-ifd.353", "yoyodyne-ifd.354"} {
		if !strings.Contains(idle.reason, id+" — this session tried it and the dispatch failed before any run was recorded") {
			t.Fatalf("idle reason = %q, want %s named with what excluded it", idle.reason, id)
		}
	}
}

// A docket that refuses the write does not lose the session's own account, and
// the refusal is said rather than reported as a record that was made: the
// exclusion says the session's log is all there is, and the pass names the
// dispatch nothing outside the session recorded.
func TestAnAttemptThatCannotBeDocketedSaysSo(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.353")...)
	harness.attemptErr = errors.New("the docket is unwritable")
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, errors.New("the claude-code backend is not installed")
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	for _, want := range []string{"yoyodyne-ifd.353", "never became a run", "the docket is unwritable"} {
		if !strings.Contains(schedule.AttemptProblem, want) {
			t.Fatalf("attempt problem = %q, want it to say %q", schedule.AttemptProblem, want)
		}
	}
	if !strings.Contains(schedule.Render(), schedule.AttemptProblem) {
		t.Fatalf("rendered schedule does not carry the attempt problem:\n%s", schedule.Render())
	}
	idle, found := sessions.entered(runstate.WatchIdle)
	if !found {
		t.Fatal("no idle transition was recorded")
	}
	if len(idle.passedOver.Groups) != 1 || len(idle.passedOver.Groups[0].Reasons) != 1 {
		t.Fatalf("idle passed over = %+v, want the one item passed over with a reason", idle.passedOver)
	}
	reason := idle.passedOver.Groups[0].Reasons[0]
	for _, want := range []string{"not installed", "could not be recorded on the docket", "this is the only account of it"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason = %q, want it to say %q", reason, want)
		}
	}
}

// A pull wired with no docket at all still says what became of the attempt, and
// says that nothing outside the session recorded it. This is the shape from
// before the docket was wired into a pull, and it is the one that must not read
// as a record having been made.
func TestAnAttemptWithNothingWiredToRecordItIsStillAccountedFor(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.353")...)
	harness.unroutable = true
	harness.run = func(_ *scheduleHarness, id string) (Outcome, error) {
		return Outcome{}, errors.New("the claude-code backend is not installed")
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !strings.Contains(schedule.AttemptProblem, "nothing was wired to docket it") {
		t.Fatalf("attempt problem = %q, want the missing docket named", schedule.AttemptProblem)
	}
	idle, found := sessions.entered(runstate.WatchIdle)
	if !found {
		t.Fatal("no idle transition was recorded")
	}
	if reason := idle.passedOver.Groups[0].Reasons[0]; !strings.Contains(reason, "nothing was wired to record that durably") {
		t.Fatalf("reason = %q, want it to say this is the only account of it", reason)
	}
}

// Every ending an exclusion can have says what it is, so an item passed over as
// already tried never reads as a bare exclusion whatever became of the start.
func TestAnExclusionSaysWhatBecameOfTheStart(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		started Started
		want    string
	}{
		{
			name:    "the work went to another process",
			started: Started{Declined: "another process is already running it"},
			want:    "went to another process: another process is already running it",
		},
		{
			name:    "the dispatch failed before a run was recorded",
			started: Started{Failure: "the backend is not installed"},
			want:    "failed before any run was recorded: the backend is not installed",
		},
		{
			name:    "the dispatch stopped short of a run and is owed a continuation",
			started: Started{Outcome: Outcome{Paused: true}},
			want:    "paused and owed a continuation",
		},
		{
			name:    "the run failed",
			started: Started{Failure: "the push was refused", Outcome: Outcome{RunID: "run-1"}},
			want:    "run run-1 failed: the push was refused",
		},
		{
			name:    "the run stopped on a blocker",
			started: Started{Outcome: Outcome{RunID: "run-1", Blocked: true}},
			want:    "run run-1 stopped on a durable blocker",
		},
		{
			name:    "the run is paused",
			started: Started{Outcome: Outcome{RunID: "run-1", Paused: true}},
			want:    "run run-1 is paused",
		},
		{
			name:    "the run ended",
			started: Started{Outcome: Outcome{RunID: "run-1", Status: runstate.StatusSucceeded}},
			want:    "run run-1 ended succeeded",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := excludedBecause(test.started); !strings.Contains(got, test.want) {
				t.Fatalf("excludedBecause() = %q, want it to say %q", got, test.want)
			}
		})
	}
}

// lastEntered is the last transition recorded into a state, for a test about
// what a session says once everything it started has ended.
func lastEntered(sessions *recordedSessions, state runstate.WatchState) (recordedTransition, bool) {
	var last recordedTransition
	found := false
	for _, transition := range sessions.recorded() {
		if transition.state == state {
			last, found = transition, true
		}
	}
	return last, found
}

// cadencedTasks is the development manager's hourly task as a watching session
// meets it: due an interval after it last fired, fired by the pass that finds it
// due, and able to say when it is next due without firing. refuse stands in for
// a schedule the harness cannot fire, for as long as it returns a failure.
type cadencedTasks struct {
	mu      sync.Mutex
	clock   func() time.Time
	every   time.Duration
	firedAt time.Time
	refuse  func(now time.Time) error
	// turnAway stands in for a firing the provider refuses without the cadence
	// moving — a capacity wait holding the task — for as long as it says why.
	turnAway func(now time.Time) string
	firings  []time.Time
	misses   []RecurringMiss
	// missedAt is when each miss was recorded, by the session's clock.
	missedAt []time.Time
}

func (c *cadencedTasks) Fire(context.Context) (RecurringSweep, error) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.firedAt.Add(c.every)) {
		return RecurringSweep{}, nil
	}
	if c.refuse != nil {
		if err := c.refuse(now); err != nil {
			return RecurringSweep{}, err
		}
	}
	if c.turnAway != nil {
		if why := c.turnAway(now); why != "" {
			return RecurringSweep{Fired: []Fired{{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Problem: why}}}, nil
		}
	}
	c.firedAt = now
	c.firings = append(c.firings, now)
	return RecurringSweep{Fired: []Fired{{Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager, Turns: 1}}}, nil
}

func (c *cadencedTasks) Cadence(context.Context) ([]RecurringDue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []RecurringDue{{
		Task: "development-manager-sweep", Role: domain.RoleDevelopmentManager,
		Every: c.every, At: c.firedAt.Add(c.every),
	}}, nil
}

func (c *cadencedTasks) Missed(_ context.Context, missed RecurringMiss) error {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses = append(c.misses, missed)
	c.missedAt = append(c.missedAt, now)
	return nil
}

// The twenty hours of 2026-09-13, replayed. A watching session pulled
// yoyodyne-ifd.362 at 18:34:22Z, three minutes after the development manager's
// hourly task last fired, and the run took until 14:35:31Z the next day. The
// session waited on that run and nothing else, and the task fired nothing until
// seven seconds after it ended.
//
// A run is not a bound on a cadence. The session waiting on it wakes when the
// task falls due and fires it, every hour, for as long as the run goes on — and
// nothing is recorded as missed, because nothing was.
func TestARunInFlightDoesNotHoldTheCadence(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.362")...)
	harness.now = time.Date(2026, 9, 13, 18, 34, 22, 0, time.UTC)
	ended := time.Date(2026, 9, 14, 14, 35, 31, 0, time.UTC)
	stopped := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-release
		return h.complete(id), nil
	}
	tasks := &cadencedTasks{
		clock:   harness.clock,
		every:   time.Hour,
		firedAt: time.Date(2026, 9, 13, 18, 31, 48, 0, time.UTC),
	}
	harness.recurring = tasks
	released := false
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		now := h.clock()
		if !released && !now.Before(ended) {
			released = true
			close(release)
		}
		return now.Before(stopped)
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !released {
		t.Fatalf("the session stopped before the run ended: %s", schedule.Render())
	}

	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	var during []time.Time
	for _, at := range tasks.firings {
		if at.Before(ended) {
			during = append(during, at)
		}
	}
	// Due at 19:31:48 and every hour after, through to 14:31:48 while the run was
	// still going: twenty firings where there were none.
	if len(during) != 20 {
		t.Fatalf("fired %d times while the run was in flight (%v), want the twenty hourly firings it was due", len(during), during)
	}
	if first := during[0]; first.After(time.Date(2026, 9, 13, 19, 32, 48, 0, time.UTC)) {
		t.Errorf("first firing at %s, want it within a poll of 19:31:48Z", first.Format(time.RFC3339))
	}
	previous := time.Date(2026, 9, 13, 18, 31, 48, 0, time.UTC)
	for _, at := range tasks.firings {
		if gap := at.Sub(previous); gap > time.Hour+time.Minute {
			t.Errorf("fired at %s, %s after the one before it, want the hourly cadence held; all: %v", at.Format(time.RFC3339), gap, tasks.firings)
		}
		previous = at
	}
	if len(tasks.misses) != 0 {
		t.Errorf("misses = %+v, want nothing recorded as missed on a cadence that held", tasks.misses)
	}
	if len(schedule.Started) != 1 {
		t.Errorf("started = %d, want the one run, waited out as before: %s", len(schedule.Started), schedule.Render())
	}
}

// The same session, with the harness failing to fire its schedule from the
// moment the task falls due until 23:00Z. The miss is recorded once, at the
// first firing that went missing — an interval after it fell due — with what
// kept it, at critical because the harness held its own cadence. And when the
// cause clears, the task fires on its own, and the cadence runs on from there.
func TestAMissedCadenceSaysWhyAndResumesWhenTheCauseClears(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.362")...)
	harness.now = time.Date(2026, 9, 13, 18, 34, 22, 0, time.UTC)
	due := time.Date(2026, 9, 13, 19, 31, 48, 0, time.UTC)
	cleared := time.Date(2026, 9, 13, 23, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	stopped := time.Date(2026, 9, 14, 2, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-release
		return h.complete(id), nil
	}
	tasks := &cadencedTasks{
		clock:   harness.clock,
		every:   time.Hour,
		firedAt: due.Add(-time.Hour),
		refuse: func(now time.Time) error {
			if now.Before(cleared) {
				return errors.New("the claim could not be taken")
			}
			return nil
		},
	}
	harness.recurring = tasks
	released := false
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		now := h.clock()
		if !released && !now.Before(ended) {
			released = true
			close(release)
		}
		return now.Before(stopped)
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if len(tasks.misses) != 1 {
		t.Fatalf("misses = %+v, want the one gap recorded once: %s", tasks.misses, schedule.Render())
	}
	missed := tasks.misses[0]
	if !missed.Due.Equal(due) || missed.Every != time.Hour {
		t.Errorf("missed = %+v, want the firing due at %s", missed, due.Format(time.RFC3339))
	}
	if !strings.Contains(missed.Why, "the claim could not be taken") {
		t.Errorf("why = %q, want what kept it", missed.Why)
	}
	if missed.Severity != report.SeverityCritical {
		t.Errorf("severity = %q, want critical for the harness holding its own cadence", missed.Severity)
	}
	firstMissed := due.Add(time.Hour)
	if at := tasks.missedAt[0]; at.Before(firstMissed) || at.After(firstMissed.Add(time.Minute)) {
		t.Errorf("recorded at %s, want it at the first missed firing, %s", at.Format(time.RFC3339), firstMissed.Format(time.RFC3339))
	}
	if len(tasks.firings) == 0 {
		t.Fatalf("fired nothing, want the task to resume once the cause cleared")
	}
	if first := tasks.firings[0]; first.Before(cleared) || first.After(cleared.Add(time.Minute)) {
		t.Errorf("resumed at %s, want within a poll of %s", first.Format(time.RFC3339), cleared.Format(time.RFC3339))
	}
	// 23:00, midnight, and 01:00 — the cadence running on from the resumption
	// while the run is still in flight.
	if len(tasks.firings) < 3 {
		t.Errorf("firings = %v, want the hourly cadence resumed from %s", tasks.firings, cleared.Format(time.RFC3339))
	}
}

// Who kept a task from firing decides how the miss is said. A task that fell
// due before this session opened fell due with no session running, which is a
// warning; the operator's own pause is recorded and said to nobody.
func TestAMissIsSaidAccordingToWhatKeptIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 14, 14, 0, 0, 0, time.UTC)
	due := now.Add(-3 * time.Hour)
	for name, test := range map[string]struct {
		watch    recurringWatch
		severity report.Severity
		says     string
	}{
		"no session running": {
			watch:    recurringWatch{opened: now.Add(-time.Minute)},
			severity: report.SeverityWarning,
			says:     "no watch session was running",
		},
		"the operator's pause": {
			watch:    recurringWatch{opened: due.Add(-time.Hour), held: recurringHold{why: "the operator paused harness activity at 2026-09-14T10:00:00Z", at: now.Add(-time.Minute), quiet: true}},
			severity: "",
			says:     "the operator paused",
		},
		"held after a late opening": {
			watch:    recurringWatch{opened: now.Add(-time.Minute), held: recurringHold{why: "the harness could not fire its recurring schedule: the claim could not be taken", at: now.Add(-30 * time.Second)}},
			severity: report.SeverityCritical,
			says:     "the claim could not be taken",
		},
		"a refused firing": {
			watch:    recurringWatch{opened: due.Add(-time.Hour), held: recurringHold{why: "You've hit your weekly limit · resets Sep 14 at 18:00Z", at: now.Add(-time.Minute), refused: true}},
			severity: report.SeverityWarning,
			says:     "resets Sep 14",
		},
		"a hold from before the task fell due": {
			watch:    recurringWatch{opened: due.Add(-2 * time.Hour), held: recurringHold{why: "the pass took its one firing for the recurring task architect-pass", at: due.Add(-10 * time.Minute), fired: "architect-pass"}},
			severity: report.SeverityCritical,
			says:     "recorded nothing that kept it",
		},
		"the task's own firing": {
			watch:    recurringWatch{opened: due.Add(-2 * time.Hour), held: recurringHold{why: "the pass took its one firing for the recurring task development-manager-sweep", at: now.Add(-time.Minute), fired: "development-manager-sweep"}},
			severity: report.SeverityCritical,
			says:     "recorded nothing that kept it",
		},
		"another task's firing after it fell due": {
			watch:    recurringWatch{opened: due.Add(-2 * time.Hour), held: recurringHold{why: "the pass took its one firing for the recurring task architect-pass", at: now.Add(-time.Minute), fired: "architect-pass"}},
			severity: report.SeverityCritical,
			says:     "architect-pass",
		},
		"nothing recorded": {
			watch:    recurringWatch{opened: due.Add(-time.Hour)},
			severity: report.SeverityCritical,
			says:     "recorded nothing that kept it",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tasks := &cadencedTasks{clock: func() time.Time { return now }, every: time.Hour, firedAt: due.Add(-time.Hour)}
			watch := test.watch
			watch.missed = map[string]time.Time{}
			scheduler := Scheduler{Now: func() time.Time { return now }}
			schedule := Schedule{}

			scheduler.missed(context.Background(), &schedule, Pull{Recurring: tasks}, &watch)
			scheduler.missed(context.Background(), &schedule, Pull{Recurring: tasks}, &watch)

			if len(tasks.misses) != 1 {
				t.Fatalf("misses = %+v, want the gap recorded once however many passes find it", tasks.misses)
			}
			if tasks.misses[0].Severity != test.severity || !strings.Contains(tasks.misses[0].Why, test.says) {
				t.Errorf("missed = %+v, want %q said at %q", tasks.misses[0], test.says, test.severity)
			}
		})
	}
}

// A capacity wait that holds the task past an interval is the first cause the
// item names. The firing is turned away with the provider's own words, reset
// among them, and the miss says so — as a warning, since the wait is the
// provider's and has its own notice — and the task fires on its own once the
// window lifts.
//
// This covers only a refusal that holds the cadence without moving it, which is
// what the double here does. The real trigger moves the cadence on a refused
// firing and records the refusal as that cadence's pass instead, so no miss
// arises from it at all; TestACapacityRefusalIsRecordedAtEachCadenceAndMissesNothing
// covers that path.
func TestACapacityWaitThatHoldsTheCadenceIsNamedWithItsReset(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.362")...)
	harness.now = time.Date(2026, 9, 13, 18, 34, 22, 0, time.UTC)
	due := time.Date(2026, 9, 13, 19, 31, 48, 0, time.UTC)
	resets := time.Date(2026, 9, 13, 23, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 9, 14, 0, 30, 0, 0, time.UTC)
	stopped := time.Date(2026, 9, 14, 1, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-release
		return h.complete(id), nil
	}
	tasks := &cadencedTasks{
		clock:   harness.clock,
		every:   time.Hour,
		firedAt: due.Add(-time.Hour),
		turnAway: func(now time.Time) string {
			if now.Before(resets) {
				return "the recurring task development-manager-sweep could not be put to the development-manager at all: api_error: You've hit your weekly limit · resets Sep 13 at 23:00Z"
			}
			return ""
		},
	}
	harness.recurring = tasks
	released := false
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		now := h.clock()
		if !released && !now.Before(ended) {
			released = true
			close(release)
		}
		return now.Before(stopped)
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if len(tasks.misses) != 1 {
		t.Fatalf("misses = %+v, want the one gap recorded once: %s", tasks.misses, schedule.Render())
	}
	missed := tasks.misses[0]
	if !strings.Contains(missed.Why, "weekly limit") || !strings.Contains(missed.Why, "resets Sep 13 at 23:00Z") {
		t.Errorf("why = %q, want the capacity wait named with its reset", missed.Why)
	}
	if strings.Contains(missed.Why, "recorded nothing") {
		t.Errorf("why = %q, want the wait rather than nothing", missed.Why)
	}
	if missed.Severity != report.SeverityWarning {
		t.Errorf("severity = %q, want a warning for a wait the provider holds", missed.Severity)
	}
	if len(tasks.firings) == 0 || tasks.firings[0].Before(resets) || tasks.firings[0].After(resets.Add(time.Minute)) {
		t.Errorf("firings = %v, want the task resumed within a poll of the reset", tasks.firings)
	}
}

// A session waiting out a redeploy goes on firing its recurring tasks on their
// cadence, because the drain is about the runs it hosts and not about the
// scheduler's other duties — the twenty-hour shape with a deploy in the middle
// of it, which until yoyodyne-ifd.398 fired nothing and recorded the miss under
// the redeploy instead. Nothing is missed, so nothing is recorded as missed.
func TestARedeployWaitGoesOnFiringTheCadence(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.362")...)
	harness.now = time.Date(2026, 9, 13, 18, 34, 22, 0, time.UTC)
	due := time.Date(2026, 9, 13, 19, 31, 48, 0, time.UTC)
	ended := time.Date(2026, 9, 13, 21, 0, 0, 0, time.UTC)
	deployment := &deployedOver{}
	release := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-release
		return h.complete(id), nil
	}
	tasks := &cadencedTasks{clock: harness.clock, every: time.Hour, firedAt: due.Add(-time.Hour)}
	harness.recurring = tasks
	released := false
	harness.onSleep = func(h *scheduleHarness, _ int) bool {
		// The build lands while the run is going, before the task falls due.
		deployment.deploy()
		if !released && !h.clock().Before(ended) {
			released = true
			close(release)
		}
		return true
	}

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock, Deployment: deployment}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session restarted into the deploy", schedule.Stopped)
	}

	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if len(tasks.firings) == 0 || tasks.firings[0].Before(due) || tasks.firings[0].After(due.Add(time.Minute)) {
		t.Errorf("firings = %v, want the task fired within a poll of %s while the session drained", tasks.firings, due.Format(time.RFC3339))
	}
	if len(tasks.misses) != 0 {
		t.Errorf("misses = %+v, want nothing missed by a session that went on firing: %s", tasks.misses, schedule.Render())
	}
}

// ValidateReady is the readiness gate: the machine refuses every run while
// somebody's uncommitted work is sitting in the primary checkout.
func (h *scheduleHarness) ValidateReady(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.dirty) == 0 {
		return nil
	}
	return gitworktree.PrimaryDirtyError{Paths: append([]string(nil), h.dirty...)}
}

// leaveUncommitted is the hand edit that stopped the line: two lines saved in
// the primary checkout and never committed.
func (h *scheduleHarness) leaveUncommitted(paths ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirty = paths
}

// commit is the operator doing the one thing that releases the line.
func (h *scheduleHarness) commit() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dirty = nil
}

// The incident this was written for, replayed. A two-line hand edit sits
// uncommitted in the primary checkout, which correctly refuses every run — and
// the whole failure was that the refusal was invisible: the session marked every
// ready item tried and idled over a full queue, and the operator diagnosed it by
// hand, twice, days apart.
//
// So: nothing is started, the session names the state in words carrying the file
// and the move that ends it, it says so once rather than once a poll, and the
// moment the change is committed the work it was holding is pulled.
func TestWatchingNamesADirtyPrimaryCheckoutRatherThanIdlingOverIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.leaveUncommitted("docs/notes.md")
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 3 {
			h.commit()
		}
		return sleeps < 5
	}
	sessions := &recordedSessions{}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	const blocked = "runs cannot start: uncommitted changes in the primary checkout (docs/notes.md); commit or stash to release"
	// The schedule reads as of the last pull, like the capacity and the queue
	// counts beside it, so it carries nothing here: the operator committed and the
	// state is over. What outlives it is the session's own log, which is the whole
	// point — a state that stood for three polls in the night has to be readable
	// afterwards by somebody who was never at the terminal.
	if schedule.Blocked != "" {
		t.Fatalf("blocked = %q, want a state that has cleared to read as cleared", schedule.Blocked)
	}
	// Blocked while it stood rather than idle, said once across the three polls it
	// stood for, and idle only afterwards — over a queue that really was empty by
	// then, which is the one time that word is true. The first item resumes
	// selection; the second records the next fill of the single developer slot.
	want := []runstate.WatchState{
		runstate.WatchWatching, runstate.WatchBlocked, runstate.WatchResumed,
		runstate.WatchWatching, runstate.WatchIdle, runstate.WatchStopped,
	}
	if got := sessions.states(); !sameStates(got, want) {
		t.Fatalf("recorded states = %v, want %v", got, want)
	}
	if reason := sessions.said(runstate.WatchBlocked); reason != blocked {
		t.Fatalf("blocked reason = %q, want %q", reason, blocked)
	}
	// Nothing was started while it stood, and both items were pulled once it was
	// released — neither of them remembered as one this session had tried.
	if len(harness.pullOrder()) != 2 {
		t.Fatalf("pulled = %v, want both items started once the change was committed", harness.pullOrder())
	}
}

// A drain meets the same refusal and stops on it rather than reporting an empty
// queue. It is a foreground command somebody is waiting on, and "nothing more is
// ready to pull" over a backlog that is entirely ready is the same lie the
// session used to tell overnight.
func TestDrainingStopsOnADirtyPrimaryCheckoutRatherThanReportingAnEmptyQueue(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.leaveUncommitted("docs/notes.md", "internal/thing.go")

	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBlocked {
		t.Fatalf("stopped = %q, want the pass stopped on what refuses every run", schedule.Stopped)
	}
	// The state still stands when this pass returns, so the schedule carries it —
	// and carries it as the sentence somebody acts on rather than as a diagnosis
	// they have to make, with every path in the way named.
	const blocked = "runs cannot start: uncommitted changes in the primary checkout (docs/notes.md, internal/thing.go); commit or stash to release"
	if schedule.Blocked != blocked {
		t.Fatalf("blocked = %q, want %q", schedule.Blocked, blocked)
	}
	if !strings.Contains(schedule.Render(), blocked) {
		t.Fatalf("render = %q, want the stalled line named in it", schedule.Render())
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started under a machine that refuses every run", schedule.Started)
	}
}

// The general rule, one half of it: a start the machine refused is never
// remembered as the item having been tried, even where nothing declared the
// refusal. Here the checkout goes dirty under a run that had already begun, so
// the refusal arrives as that run's failure with no mark on it and is recognized
// by asking the machine — and the item is still pulled again after the operator
// commits, without anybody editing it.
func TestAStartTheMachineRefusedIsNotRememberedAsTheItemHavingBeenTried(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// The edit lands after the pull's readiness read, so the refusal reaches
		// the scheduler as this run's own failure rather than as the gate's.
		h.leaveUncommitted("docs/notes.md")
		return Outcome{}, errors.New("repository is not ready for an isolated run")
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.commit()
			// From here a start would succeed, so the only thing that could keep
			// the item out of the queue is this session remembering it.
			h.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
		}
		return sleeps < 3
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s) (%v), want it tried again after the machine was put right", starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 || schedule.Started[1].Failure != "" {
		t.Fatalf("started = %#v, want the second attempt to have run", schedule.Started)
	}
	// Nothing about the work failed, so the failure-storm brake counted nothing.
	if schedule.BlockedInARow != 0 || schedule.Braked != nil {
		t.Fatalf("blocked in a row = %d, braked = %#v, want a machine refusal to count as neither", schedule.BlockedInARow, schedule.Braked)
	}
}

// The other half, and the one asking the machine cannot reach. A sandbox that
// will not spawn a process leaves the checkout spotless, so a readiness read
// answers yes and the refusal would read as the item's own failure — which is
// how the E2BIG shell failure the item names would have been recorded: written
// into tried-memory with a fingerprint, held out until somebody edited work that
// was never the problem, and counted toward the brake that then holds intake
// over a machine already unable to start anything.
//
// The step that meets such a condition marks it, and the mark is what is read
// here. Nothing about the item changes in this test and nothing about the
// checkout is ever wrong: the item is pulled again at the next interval anyway.
func TestAStartRefusedByTheExecutionEnvironmentIsRetriedWithoutTheItemChanging(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	// The brake is armed at a single blocked run, so a machine refusal counted as
	// one would hold intake here and the session would never pull anything again.
	harness.blockedRuns = 1
	harness.run = func(*scheduleHarness, string) (Outcome, error) {
		return Outcome{}, refusedByEnvironment("the sandbox refused to start a process",
			errors.New("fork/exec /bin/zsh: argument list too long"))
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
		}
		return sleeps < 3
	}
	sessions := &recordedSessions{}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s) (%v), want it tried again at the next interval with nothing about it edited", starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 || schedule.Started[1].Failure != "" {
		t.Fatalf("started = %#v, want the second attempt to have run", schedule.Started)
	}
	// Nothing about the work failed, so the storm the brake counts neither grew
	// nor tripped, however tightly it was wound.
	if schedule.BlockedInARow != 0 || schedule.Braked != nil || schedule.BrakeProblem != "" {
		t.Fatalf("blocked in a row = %d, braked = %#v, brake problem = %q, want a machine refusal to feed none of them",
			schedule.BlockedInARow, schedule.Braked, schedule.BrakeProblem)
	}
	// And the session names the condition in the words of the step that met it,
	// rather than in a sentence about a repository that was never the problem.
	const blocked = "runs cannot start: the sandbox refused to start a process: fork/exec /bin/zsh: argument list too long"
	if reason := sessions.said(runstate.WatchBlocked); reason != blocked {
		t.Fatalf("blocked reason = %q, want %q", reason, blocked)
	}
}
