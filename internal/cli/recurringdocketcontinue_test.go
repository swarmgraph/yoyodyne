package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type docketPassRole struct {
	messages []string
	after    func(turn int)
	failAt   int
	more     bool
}

func (r *docketPassRole) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string) (orchestrator.Turn, error) {
	r.messages = append(r.messages, message)
	if len(r.messages) == r.failAt {
		return orchestrator.Turn{}, errors.New("the provider's usage limit ended the turn")
	}
	if r.after != nil {
		r.after(len(r.messages))
	}
	status := sweep.StatusComplete
	if r.more {
		status = sweep.StatusMore
	}
	return orchestrator.Turn{ConversationID: "chat-1", Result: &sweep.Result{Status: status, Summary: "looked at this slice"}}, nil
}

type docketPassClock struct{ at time.Time }

func (c *docketPassClock) Now() time.Time { return c.at }

// These are the October 4 counts: the first slice carries 25 stopped runs and
// promises 57 more. The real run, docket and sweep stores keep all the evidence;
// the only substitute is the role that answers the turns.
func docketPassFixture(t *testing.T, count, turns int, role *docketPassRole) (orchestrator.Trigger, *runstate.DocketStore, *runstate.Store, *docketPassClock) {
	t.Helper()
	runs, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= count; i++ {
		state := stoppedRunOf(fmt.Sprintf("yoyodyne-ifd.430.40.%d", i))
		state.WorkItemTitle = fmt.Sprintf("Stopped change %d", i)
		state.RunID = fmt.Sprintf("run-%032x", i)
		completed := state.CompletedAt.Add(time.Duration(i) * time.Minute)
		state.CompletedAt, state.UpdatedAt = &completed, completed
		if err := runs.Create(state); err != nil {
			t.Fatal(err)
		}
	}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	clock := &docketPassClock{at: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	tasks := developmentManagerSweep()
	task := tasks["development-manager-sweep"]
	task.MaxTurns = turns
	tasks["development-manager-sweep"] = task
	docketer := docketerOverDocket(runs, docket)
	docketer.Clock = clock
	return orchestrator.Trigger{Tasks: tasks, Claims: sweeps, Reports: sweeps, Roles: role, Clock: clock,
		Docket: sweepDocket{docketer: docketer, items: fixedItems{}, window: docket, now: clock.Now}}, docket, runs, clock
}

func assertDocketItems(t *testing.T, messages []string, expected []int) {
	t.Helper()
	var got []int
	for _, message := range messages {
		for _, line := range strings.Split(message, "\n") {
			if !strings.Contains(line, "[stopped run]") {
				continue
			}
			var item int
			at := strings.Index(line, "yoyodyne-ifd.430.40.")
			if at < 0 {
				t.Fatalf("docket heading names no work item: %s", line)
			}
			if _, err := fmt.Sscanf(line[at:], "yoyodyne-ifd.430.40.%d", &item); err != nil {
				t.Fatal(err)
			}
			got = append(got, item)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(expected) {
		t.Fatalf("delivered items = %v, want %v", got, expected)
	}
}

func docketItemRange(first, last int) []int {
	var items []int
	for i := first; i <= last; i++ {
		items = append(items, i)
	}
	return items
}

func TestADocketLargerThanOneTurnIsDeliveredAcrossThePassInOrder(t *testing.T) {
	t.Parallel()
	for _, count := range []int{82, 83} {
		t.Run(fmt.Sprintf("%d entries", count), func(t *testing.T) {
			t.Parallel()
			role := &docketPassRole{}
			if count == 82 {
				// The original omission followed replies explicitly asking for
				// more turns. The recurrence also cannot finish on a premature
				// complete while entries remain unseen.
				role.after = func(turn int) { role.more = turn < 4 }
			}
			trigger, _, _, _ := docketPassFixture(t, count, 4, role)
			result, err := trigger.Fire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(role.messages) != 4 || result.Fired[0].Truncated {
				t.Fatalf("turns = %d, result = %+v, want four turns finishing the docket", len(role.messages), result)
			}
			if !strings.Contains(role.messages[0], fmt.Sprintf("%d further live docket entry(s)", count-25)) {
				t.Fatal("the first turn did not preserve the October 4 unread count")
			}
			assertDocketItems(t, role.messages, docketItemRange(1, count))
			for turn, bounds := range [][2]int{{1, 25}, {26, 50}, {51, 75}, {76, count}} {
				assertDocketItems(t, role.messages[turn:turn+1], docketItemRange(bounds[0], bounds[1]))
			}
			recorded, unreadable, err := trigger.Reports.List()
			if err != nil || len(unreadable) > 0 || len(recorded) != 1 {
				t.Fatalf("List() = %v, %v, %v", recorded, unreadable, err)
			}
			if delivery := recorded[0].Docket; delivery == nil || delivery.Delivered != count || len(delivery.Undelivered) != 0 || delivery.Oldest != nil {
				t.Fatalf("delivery = %+v, want every entry delivered", delivery)
			}
		})
	}
}

func TestAPassEndingEarlyRecordsItsUnreadDocketAndTheNextPassStartsThere(t *testing.T) {
	t.Parallel()
	for _, end := range []string{"turn bound", "provider limit"} {
		t.Run(end, func(t *testing.T) {
			t.Parallel()
			role := &docketPassRole{more: true}
			turns := 1
			if end == "provider limit" {
				turns, role.failAt = 4, 2
			}
			trigger, _, _, clock := docketPassFixture(t, 82, turns, role)
			if _, err := trigger.Fire(context.Background()); err != nil {
				t.Fatal(err)
			}
			recorded, unreadable, err := trigger.Reports.List()
			if err != nil || len(unreadable) > 0 || len(recorded) != 1 {
				t.Fatalf("List() = %v, %v, %v", recorded, unreadable, err)
			}
			delivery := recorded[0].Docket
			if delivery == nil || delivery.Delivered != 25 || len(delivery.Undelivered) != 57 || delivery.Oldest == nil || delivery.Oldest.WorkItemID != "yoyodyne-ifd.430.40.26" {
				t.Fatalf("delivery = %+v, want 25 delivered, 57 unread, oldest item 26", delivery)
			}
			if !strings.Contains(recorded[0].Problem, "57 live docket entry(s) were never delivered") || !strings.Contains(recorded[0].Problem, "Stopped change 26 (yoyodyne-ifd.430.40.26)") {
				t.Fatalf("the record does not name the unread count and oldest entry: %s", recorded[0].Problem)
			}
			if recorded[0].Failed != (end == "provider limit") || recorded[0].Result.Status != sweep.StatusMore {
				t.Fatalf("the pass's ending was lost: %+v", recorded[0])
			}
			// A fresh trigger reads the durable report. The next pass's four
			// slices put all 57 unread entries before any of the 25 already shown.
			clock.at = clock.at.Add(time.Hour)
			nextRole := &docketPassRole{}
			trigger.Roles = nextRole
			task := trigger.Tasks["development-manager-sweep"]
			task.MaxTurns = 4
			trigger.Tasks["development-manager-sweep"] = task
			if end == "provider limit" {
				// Unread work belongs to the product's docket, not a task name.
				trigger.Tasks = map[string]config.RecurringTask{"renamed-sweep": task}
			}
			if _, err := trigger.Fire(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertDocketItems(t, nextRole.messages, append(docketItemRange(26, 82), docketItemRange(1, 25)...))
		})
	}
}

func TestADocketEntryDecidedOrClosedBetweenTurnsIsNotDeliveredAgain(t *testing.T) {
	t.Parallel()
	role := &docketPassRole{}
	trigger, docket, _, clock := docketPassFixture(t, 82, 4, role)
	items := fixedItems{}
	source := trigger.Docket.(sweepDocket)
	source.items = &items
	trigger.Docket = source
	role.after = func(turn int) {
		if turn != 1 {
			return
		}
		// One entry already shown and one waiting behind it are decided. A
		// third's work item closes. None is delivered on following turns.
		for _, i := range []int{1, 26} {
			run := fmt.Sprintf("run-%032x", i)
			if _, err := docket.Close(triage.Closure{SchemaVersion: triage.ClosureSchemaVersion, ProductID: "yoyodyne", Key: triage.Key(triage.ClassStoppedRun, run),
				RunID: run, WorkItemID: fmt.Sprintf("yoyodyne-ifd.430.40.%d", i), Decision: "rescope",
				Reason: "the work must be replanned", DecidedBy: "development manager in chat-1", ClosedAt: clock.at}); err != nil {
				t.Fatal(err)
			}
		}
		items = append(items, beads.WorkItem{ID: "yoyodyne-ifd.430.40.27", Status: "closed"})
	}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDocketItems(t, role.messages, append(docketItemRange(1, 25), docketItemRange(28, 82)...))
	recorded, _, err := trigger.Reports.List()
	if err != nil || len(recorded) != 1 || recorded[0].Docket.Delivered != 80 || len(recorded[0].Docket.Undelivered) != 0 {
		t.Fatalf("the settled entries remained in the unread count: %v, %v", recorded, err)
	}
}

type docketThatBecomesUnreadable struct {
	source interface {
		Build() (orchestrator.DocketBuild, error)
	}
	unreadable bool
	partial    bool
}

func (d *docketThatBecomesUnreadable) Build() (orchestrator.DocketBuild, error) {
	if d.unreadable {
		if d.partial {
			built, err := d.source.Build()
			if err != nil {
				return built, err
			}
			built.Entries = built.Entries[:25]
			return built, errors.New("the docket log could only be read in part")
		}
		return orchestrator.DocketBuild{}, errors.New("the docket log could not be read")
	}
	return d.source.Build()
}

func TestAPassKeepsItsKnownUnreadDocketWhenALaterReadingFails(t *testing.T) {
	t.Parallel()
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			t.Parallel()
			checkPassKeepsUnreadDocket(t, partial)
		})
	}
}

func checkPassKeepsUnreadDocket(t *testing.T, partial bool) {
	t.Helper()
	role := &docketPassRole{}
	trigger, _, _, _ := docketPassFixture(t, 82, 2, role)
	source := trigger.Docket.(sweepDocket)
	docket := &docketThatBecomesUnreadable{source: source.docketer, partial: partial}
	source.docketer = docket
	trigger.Docket = source
	role.after = func(int) { docket.unreadable = true }
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorded, _, err := trigger.Reports.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %v, %v", recorded, err)
	}
	delivery := recorded[0].Docket
	if delivery == nil || delivery.Delivered != 25 || len(delivery.Undelivered) != 57 || delivery.Oldest.WorkItemID != "yoyodyne-ifd.430.40.26" {
		t.Fatalf("a failed docket read lost known unread work: %+v", delivery)
	}
	explanation := "which entries remain undelivered could not be established"
	if partial {
		explanation = "The docket could only be built in part"
	}
	if !strings.Contains(recorded[0].Problem, explanation) {
		t.Fatalf("a failed docket read was not recorded: %s", recorded[0].Problem)
	}
}
