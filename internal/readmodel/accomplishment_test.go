package readmodel

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
	"github.com/mason-bryant/yoyodyne/internal/workref"
)

type fakeAccomplishmentSpend []runstate.Spend

func (f fakeAccomplishmentSpend) List() ([]runstate.Spend, error) { return f, nil }

type fakeAccomplishmentPasses []runstate.Sweep

func (f fakeAccomplishmentPasses) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return f, nil, nil
}

type fakeAccomplishmentRuns struct {
	states []runstate.State
	events map[string][]execution.Event
}

func (f fakeAccomplishmentRuns) Recorded() ([]runstate.State, error) { return f.states, nil }

func (f fakeAccomplishmentRuns) EventsOfType(id string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error) {
	return ofTypes(f.events[id], types), nil, nil
}

type fakeAccomplishmentConversations map[string][]execution.Event

func (f fakeAccomplishmentConversations) EventsOfType(id string, types ...execution.EventType) ([]execution.Event, []runstate.SkippedLine, error) {
	return ofTypes(f[id], types), nil, nil
}

func ofTypes(events []execution.Event, types []execution.EventType) []execution.Event {
	var kept []execution.Event
	for _, event := range events {
		for _, wanted := range types {
			if event.Type == wanted {
				kept = append(kept, event)
			}
		}
	}
	return kept
}

func atPacific(t *testing.T, at string) time.Time {
	t.Helper()
	moment, err := time.ParseInLocation("2006-01-02 15:04", at, DeliveryZone())
	if err != nil {
		t.Fatal(err)
	}
	return moment
}

func spent(role domain.AgentRole, phase runstate.SpendPhase, at time.Time, amount float64) runstate.Spend {
	return runstate.Spend{Role: role, Agent: string(role), Phase: phase, At: at, Classification: runstate.SpendKnown, AmountUSD: amount}
}

func inConversation(line runstate.Spend, conversationID string) runstate.Spend {
	line.ConversationID = conversationID
	return line
}

func inRun(line runstate.Spend, runID, item string) runstate.Spend {
	line.RunID, line.WorkItemID = runID, item
	return line
}

func recordedEvent(t *testing.T, stream string, sequence uint64, at time.Time, eventType execution.EventType, payload any) execution.Event {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return execution.Event{SchemaVersion: execution.EventSchemaVersion, RunID: stream, Sequence: sequence, Timestamp: at, Type: eventType, Source: "test", Payload: encoded}
}

func trackerApplied(t *testing.T, stream string, sequence uint64, at time.Time, action, item string) execution.Event {
	return recordedEvent(t, stream, sequence, at, execution.EventTrackerActionApplied, map[string]any{
		"action":       map[string]any{"action": action, "id": item},
		"work_item_id": item,
	})
}

func reviewed(t *testing.T, runID string, sequence uint64, at time.Time, decision string, findings int) execution.Event {
	return recordedEvent(t, runID, sequence, at, execution.EventReviewCompleted, map[string]any{"decision": decision, "findings": findings})
}

// accomplishmentFixture is one recorded example of every kind the reading
// classifies, in the week of October 19 2026 and the weekend before it.
func accomplishmentFixture(t *testing.T) AccomplishmentSources {
	t.Helper()
	const (
		chat      = "chat-11111111111111111111111111111111"
		devChat   = "chat-22222222222222222222222222222222"
		archChat  = "chat-33333333333333333333333333333333"
		itemA     = "yoyodyne-aaa"
		itemB     = "yoyodyne-bbb"
		itemC     = "yoyodyne-ccc"
		runA1     = "run-a1"
		runA2     = "run-a2"
		runC      = "run-c"
		runDoc    = "run-doc"
		taskDM    = "development-manager-sweep"
		taskArch  = "architect-pass"
		statusFix = sweep.StatusComplete
	)
	lines := []runstate.Spend{
		// A Lead Product Manager turn on Saturday that released item A, which a
		// developer then worked on: it unblocked delivery.
		inConversation(spent(domain.RoleProductManager, runstate.SpendPhaseConversation, atPacific(t, "2026-10-17 09:00"), 4), chat),
		// A turn that released item B, which nothing then worked on: records only.
		inConversation(spent(domain.RoleProductManager, runstate.SpendPhaseConversation, atPacific(t, "2026-10-17 10:00"), 3), chat),
		// A turn that only read the tracker: it changed nothing.
		inConversation(spent(domain.RoleProductManager, runstate.SpendPhaseConversation, atPacific(t, "2026-10-17 11:00"), 1), chat),
		// A development manager pass of two turns that wrote a memory.
		inConversation(spent(domain.RoleDevelopmentManager, runstate.SpendPhaseConversation, atPacific(t, "2026-10-19 08:01"), 2), devChat),
		inConversation(spent(domain.RoleDevelopmentManager, runstate.SpendPhaseConversation, atPacific(t, "2026-10-19 08:02"), 2), devChat),
		// Three architect passes: the first and second handed the same and saying
		// the same, the third saying something else.
		inConversation(spent(domain.RoleArchitect, runstate.SpendPhaseConversation, atPacific(t, "2026-10-19 09:01"), 1), archChat),
		inConversation(spent(domain.RoleArchitect, runstate.SpendPhaseConversation, atPacific(t, "2026-10-19 10:01"), 1), archChat),
		inConversation(spent(domain.RoleArchitect, runstate.SpendPhaseConversation, atPacific(t, "2026-10-19 11:01"), 1), archChat),
		// Item A: a first run whose review asked for a repair the developer made
		// and the next review approved, and a second run after it.
		inRun(spent(domain.RoleDeveloper, runstate.SpendPhaseDevelopment, atPacific(t, "2026-10-17 12:00"), 2), runA1, itemA),
		inRun(spent(domain.RoleReviewer, runstate.SpendPhaseReview, atPacific(t, "2026-10-17 13:00"), 1), runA1, itemA),
		inRun(spent(domain.RoleDeveloper, runstate.SpendPhaseRepair, atPacific(t, "2026-10-17 14:00"), 3), runA1, itemA),
		inRun(spent(domain.RoleReviewer, runstate.SpendPhaseReview, atPacific(t, "2026-10-17 15:00"), 1), runA1, itemA),
		inRun(spent(domain.RoleDeveloper, runstate.SpendPhaseDevelopment, atPacific(t, "2026-10-20 12:00"), 1.5), runA2, itemA),
		// Item C: a review whose findings were never repaired, and a reviewer
		// invocation after it that recorded no verdict.
		inRun(spent(domain.RoleDeveloper, runstate.SpendPhaseDevelopment, atPacific(t, "2026-10-21 12:00"), 2), runC, itemC),
		inRun(spent(domain.RoleReviewer, runstate.SpendPhaseReview, atPacific(t, "2026-10-21 13:00"), 1), runC, itemC),
		inRun(spent(domain.RoleReviewer, runstate.SpendPhaseReview, atPacific(t, "2026-10-21 14:00"), 0.5), runC, itemC),
		// A document publication: a review of a run that served no work item.
		inRun(spent(domain.RoleReviewer, runstate.SpendPhaseReview, atPacific(t, "2026-10-21 15:00"), 0.7), runDoc, runDoc),
	}
	repeatInputs := map[string]int{"landed": 1}
	passes := []runstate.Sweep{
		{
			Task: taskDM, Role: domain.RoleDevelopmentManager, ConversationID: devChat,
			StartedAt: atPacific(t, "2026-10-19 08:00"), EndedAt: atPacific(t, "2026-10-19 08:05"), Turns: 2,
			Saved:  []runstate.SavedWrite{{Kind: runstate.SavedMemory, Action: "remember", Memory: "queue", Revision: 1}},
			Result: &sweep.Result{Status: statusFix},
			// The account names item A and item B, so the pass's cost is shared
			// between them.
			References: &runstate.SweepReferences{Summary: []workref.Reference{{ID: itemA}, {ID: itemB}}},
		},
		{
			Task: taskArch, Role: domain.RoleArchitect, ConversationID: archChat, Events: repeatInputs,
			StartedAt: atPacific(t, "2026-10-19 09:00"), EndedAt: atPacific(t, "2026-10-19 09:05"), Turns: 1,
			Result: &sweep.Result{Status: statusFix},
		},
		{
			Task: taskArch, Role: domain.RoleArchitect, ConversationID: archChat, Events: repeatInputs,
			StartedAt: atPacific(t, "2026-10-19 10:00"), EndedAt: atPacific(t, "2026-10-19 10:05"), Turns: 1,
			Result: &sweep.Result{Status: statusFix},
		},
		{
			Task: taskArch, Role: domain.RoleArchitect, ConversationID: archChat, Events: repeatInputs,
			StartedAt: atPacific(t, "2026-10-19 11:00"), EndedAt: atPacific(t, "2026-10-19 11:05"), Turns: 1,
			Result: &sweep.Result{Status: statusFix, Findings: []sweep.Finding{{Issue: "a stale design", Disposition: sweep.DispositionLeft}}},
		},
	}
	conversations := fakeAccomplishmentConversations{
		chat: {
			trackerApplied(t, chat, 1, atPacific(t, "2026-10-17 09:00").Add(time.Second), "update", itemA),
			trackerApplied(t, chat, 2, atPacific(t, "2026-10-17 10:00").Add(time.Second), "update", itemB),
			trackerApplied(t, chat, 3, atPacific(t, "2026-10-17 11:00").Add(time.Second), "read", itemB),
		},
	}
	mergedA := atPacific(t, "2026-10-20 13:00")
	finishedDoc := atPacific(t, "2026-10-21 15:05")
	runs := fakeAccomplishmentRuns{
		states: []runstate.State{
			{RunID: runA1, WorkItemID: itemA, WorkItemTitle: "Item A", StartedAt: atPacific(t, "2026-10-17 11:59")},
			{RunID: runA2, WorkItemID: itemA, WorkItemTitle: "Item A", StartedAt: atPacific(t, "2026-10-20 11:59"), CompletedAt: &mergedA, Integration: &runstate.Integration{TargetBranch: "main"}},
			{RunID: runC, WorkItemID: itemC, WorkItemTitle: "Item C", StartedAt: atPacific(t, "2026-10-21 11:59")},
			{RunID: runDoc, WorkItemID: runDoc, StartedAt: atPacific(t, "2026-10-21 14:59"), CompletedAt: &finishedDoc, Integration: &runstate.Integration{TargetBranch: "main"}, Document: &runstate.DocumentPublication{}},
		},
		events: map[string][]execution.Event{
			runA1: {
				reviewed(t, runA1, 1, atPacific(t, "2026-10-17 13:00").Add(time.Second), "repair", 2),
				reviewed(t, runA1, 2, atPacific(t, "2026-10-17 15:00").Add(time.Second), "approve", 0),
			},
			runC:   {reviewed(t, runC, 1, atPacific(t, "2026-10-21 13:00").Add(time.Second), "repair", 1)},
			runDoc: {reviewed(t, runDoc, 1, atPacific(t, "2026-10-21 15:00").Add(time.Second), "approve", 0)},
		},
	}
	return AccomplishmentSources{
		Spend:         fakeAccomplishmentSpend(lines),
		Passes:        fakeAccomplishmentPasses(passes),
		Runs:          runs,
		Conversations: conversations,
		Now:           func() time.Time { return atPacific(t, "2026-10-25 09:00") },
	}
}

func TestAccomplishmentClassifiesEachKindOfPass(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), accomplishmentFixture(t), AccomplishmentRange{From: "2026-10-17", To: "2026-10-24"})
	if reading.Problem != "" || len(reading.NotRead) > 0 {
		t.Fatalf("reading failed: %q %v", reading.Problem, reading.NotRead)
	}
	var got []string
	for _, pass := range reading.Passes {
		got = append(got, string(pass.Role)+" "+string(pass.Outcome))
	}
	want := []string{
		"product-manager unblocked-delivery",
		"product-manager changed-records-only",
		"product-manager changed-nothing",
		"development-manager changed-records-only",
		"architect changed-nothing",
		"architect repeated-earlier",
		"architect changed-nothing",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("passes classified as\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if first := reading.Passes[0]; strings.Join(first.Progressed, ",") != "yoyodyne-aaa" {
		t.Fatalf("the unblocking turn progressed %v, want item A", first.Progressed)
	}
	if repeated := reading.Passes[5]; repeated.RepeatOf == nil || !repeated.RepeatOf.Equal(atPacific(t, "2026-10-19 09:00")) {
		t.Fatalf("the repeat names %v as what it repeated, want the 09:00 pass", repeated.RepeatOf)
	}
	// The development manager's two turns are one pass, as its record bounds it.
	if dm := reading.Passes[3]; dm.Invocations != 2 || dm.CostUSD != 4 || dm.Task != "development-manager-sweep" {
		t.Fatalf("the development manager's pass is %+v, want its two turns as one pass of its task", dm)
	}

	roles := map[domain.AgentRole]RoleAccomplishment{}
	for _, role := range reading.Roles {
		roles[role.Role] = role
	}
	pm := roles[domain.RoleProductManager]
	if pm.Passes != 3 || pm.CostUSD != 8 || !pm.Management {
		t.Fatalf("the Lead Product Manager's row is %+v", pm)
	}
	for _, share := range pm.Outcomes {
		if share.Outcome == string(PassUnblocked) && (share.Count != 1 || share.CostUSD != 4 || math.Abs(share.Share-1.0/3) > 1e-9) {
			t.Fatalf("the unblocked share is %+v", share)
		}
	}
	if developer := roles[domain.RoleDeveloper]; developer.Management || developer.Passes != 0 || developer.Invocations != 4 {
		t.Fatalf("the developer's row is %+v, want its invocations and no passes", developer)
	}
}

func TestAccomplishmentClassifiesEachReview(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), accomplishmentFixture(t), AccomplishmentRange{From: "2026-10-17", To: "2026-10-24"})
	var got []string
	for _, review := range reading.Reviews {
		got = append(got, review.RunID+" "+string(review.Outcome))
	}
	want := []string{
		"run-a1 prevented-defect",
		"run-a1 approved-unchanged",
		"run-c findings-not-fixed",
		"run-c no-verdict",
		"run-doc approved-unchanged",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reviews classified as\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, share := range reading.ReviewOutcomes {
		if share.Outcome == string(ReviewPreventedDefect) && (share.Count != 1 || share.Share != 0.2) {
			t.Fatalf("the prevented-defect share is %+v", share)
		}
	}
}

func TestAccomplishmentPricesEachCompletedItemInFull(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), accomplishmentFixture(t), AccomplishmentRange{From: "2026-10-17", To: "2026-10-24"})
	if len(reading.Completed) != 1 {
		t.Fatalf("completed items are %+v, want item A alone: a document publication serves no item", reading.Completed)
	}
	item := reading.Completed[0]
	// Development is the first run's first attempt; retries are the repair and
	// the second run; management is the whole releasing turn and half the
	// development manager's pass, which named items A and B.
	if item.WorkItemID != "yoyodyne-aaa" || item.Runs != 2 || item.DevelopmentUSD != 2 || item.ReviewUSD != 2 || item.RetriesUSD != 4.5 || item.ManagementUSD != 6 || item.TotalUSD != 14.5 || item.ManagementPasses != 2 {
		t.Fatalf("item A costs %+v", item)
	}
	if reading.CostPerCompletedItem != 14.5 {
		t.Fatalf("cost per completed item is %v", reading.CostPerCompletedItem)
	}
	// Two of the Lead Product Manager's turns and all three architect passes
	// named nothing.
	if reading.UnnamedPasses != 4 || reading.UnnamedManagementUSD != 4 {
		t.Fatalf("unnamed management is %d passes, $%v", reading.UnnamedPasses, reading.UnnamedManagementUSD)
	}
}

func TestAccomplishmentSplitsTheRangeIntoWeeks(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), accomplishmentFixture(t), AccomplishmentRange{From: "2026-10-17", To: "2026-10-24"})
	if len(reading.Weeks) != 2 {
		t.Fatalf("weeks are %+v", reading.Weeks)
	}
	first, second := reading.Weeks[0], reading.Weeks[1]
	if first.Week != "2026-10-12" || first.From != "2026-10-17" || first.To != "2026-10-18" || first.CostUSD != 15 || first.Completed != 0 {
		t.Fatalf("the first week is %+v", first)
	}
	if second.Week != "2026-10-19" || second.From != "2026-10-19" || second.To != "2026-10-24" || second.Completed != 1 || second.CostPerCompletedItem != 14.5 {
		t.Fatalf("the second week is %+v", second)
	}
	if math.Abs(reading.CostUSD-(first.CostUSD+second.CostUSD)) > 1e-9 {
		t.Fatalf("the weeks add to %v and %v, the range to %v", first.CostUSD, second.CostUSD, reading.CostUSD)
	}
}

func TestAccomplishmentNarrowsToTheRange(t *testing.T) {
	reading := ReadAccomplishment(context.Background(), accomplishmentFixture(t), AccomplishmentRange{From: "2026-10-19", To: "2026-10-19"})
	for _, pass := range reading.Passes {
		if pass.Role == domain.RoleProductManager {
			t.Fatalf("a Saturday turn is in a Monday reading: %+v", pass)
		}
	}
	// The pass before the range still counts as the earlier one a repeat repeats.
	if got := reading.Passes[2].Outcome; got != PassRepeated {
		t.Fatalf("the second architect pass is %s", got)
	}
	if len(reading.Completed) != 0 || len(reading.Reviews) != 0 {
		t.Fatalf("Monday completed %v and reviewed %v", reading.Completed, reading.Reviews)
	}
}

func TestAccomplishmentSaysWhatItCouldNotRead(t *testing.T) {
	sources := accomplishmentFixture(t)
	sources.Conversations = nil
	sources.ConversationsProblem = "permission denied"
	reading := ReadAccomplishment(context.Background(), sources, AccomplishmentRange{From: "2026-10-17", To: "2026-10-24"})
	if !reading.Floor || len(reading.NotRead) != 1 || !strings.Contains(reading.NotRead[0], "permission denied") {
		t.Fatalf("an unread conversation log is reported as %v, floor %v", reading.NotRead, reading.Floor)
	}

	sources.Spend = nil
	sources.SpendProblem = "gone"
	if reading := ReadAccomplishment(context.Background(), sources, AccomplishmentRange{}); !strings.Contains(reading.Problem, "gone") {
		t.Fatalf("an unread spend log is reported as %q", reading.Problem)
	}
}

func TestAccomplishmentRefusesARangeItCannotRead(t *testing.T) {
	sources := accomplishmentFixture(t)
	for _, span := range []AccomplishmentRange{{From: "October 17"}, {To: "2026-13-01"}, {From: "2026-10-24", To: "2026-10-17"}} {
		if reading := ReadAccomplishment(context.Background(), sources, span); reading.Problem == "" {
			t.Fatalf("range %+v was read", span)
		}
	}
	// No range is the current week and the three before it.
	reading := ReadAccomplishment(context.Background(), sources, AccomplishmentRange{})
	if reading.From != "2026-09-28" || reading.To != "2026-10-25" || len(reading.Weeks) != 4 {
		t.Fatalf("the default range is %s to %s in %d weeks", reading.From, reading.To, len(reading.Weeks))
	}
}
