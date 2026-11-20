package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// repairItems is the tracker a repair from feedback reads, with the release a
// claim given back needs.
type repairItems struct {
	*multiTracker
	released []string
}

func (r *repairItems) Release(_ context.Context, id, reason string) (beads.WorkItem, error) {
	r.released = append(r.released, id)
	r.items[id].Status = "open"
	r.items[id].Notes += reason + "\n"
	return *r.items[id], nil
}

type fakeFeedbackBudget struct {
	spent   []string
	refuses error
}

func (b *fakeFeedbackBudget) RecordFeedbackRepair(_ context.Context, item, runID string, _ time.Time) (runstate.TriageCounters, error) {
	if b.refuses != nil {
		return runstate.TriageCounters{}, b.refuses
	}
	b.spent = append(b.spent, item+"@"+runID)
	return runstate.TriageCounters{}, nil
}

type noIntakeHold struct{}

func (noIntakeHold) Held() (runstate.IntakeHold, bool, error) {
	return runstate.IntakeHold{}, false, nil
}

type heldIntake struct{}

func (heldIntake) Held() (runstate.IntakeHold, bool, error) {
	return runstate.IntakeHold{HeldAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}, true, nil
}

type feedbackRepairStart struct {
	item      string
	selection runstate.Selection
}

// repairFixture is pull request #7 tracked as carrying yoyodyne-first and
// yoyodyne-a, with a line comment on internal/a.go on it.
func repairFixture(t *testing.T) (teamPullRequestFixture, *repairItems, *fakeFeedbackBudget, *[]feedbackRepairStart, TeamFeedbackRepairer) {
	t.Helper()
	f, forge, _, _ := teamFeedbackFixture(t)
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	forge.readings[7] = publish.FeedbackReading{
		State: "OPEN",
		LineComments: []publish.FeedbackComment{
			{ID: 21, Author: "teammate", Body: "This loop never ends.", At: later, Path: "internal/a.go", Line: 12},
		},
		Reviews: []publish.FeedbackReview{{ID: 31, Author: "teammate", State: publish.ReviewApproved, At: later}},
	}
	items := &repairItems{multiTracker: f.tracker}
	budget := &fakeFeedbackBudget{}
	var starts []feedbackRepairStart
	repairer := TeamFeedbackRepairer{
		Runs: f.store, Items: items, Feedback: forge, Budget: budget, Intake: noIntakeHold{}, Gates: noGates{}, Capacity: 2,
		Start: func(_ context.Context, item string, selection runstate.Selection) (Outcome, error) {
			starts = append(starts, feedbackRepairStart{item, selection})
			return Outcome{RunID: "run-" + strings.Repeat("1", 32)}, nil
		},
	}
	return f, items, budget, &starts, repairer
}

func decideRequest(item, feedback string) TeamFeedbackRepairRequest {
	return TeamFeedbackRepairRequest{PullRequest: 7, FeedbackID: feedback, WorkItemID: item, Reason: "the loop is a real defect in this change",
		DecidedBy: "Development Manager", Conversation: "chat-dm", Turn: 12}
}

func TestRepairFromTrackedPullRequestFeedbackIsDecidedThenCarriedOut(t *testing.T) {
	t.Parallel()
	f, items, budget, starts, repairer := repairFixture(t)

	decided, err := repairer.Decide(context.Background(), decideRequest("yoyodyne-a", "line-comment-21"))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decided.RunID != f.runs["yoyodyne-a"] || len(budget.spent) != 1 || budget.spent[0] != "yoyodyne-a@"+f.runs["yoyodyne-a"] {
		t.Fatalf("decided = %#v, spent = %v; want the decision on the landed run and one attempt spent for it", decided, budget.spent)
	}
	if len(*starts) != 0 {
		t.Fatalf("starts = %v after deciding, want nothing started until the harness carries it out", *starts)
	}
	owed := f.load(t, "yoyodyne-a").TeamMerge.RepairsOwed()
	if len(owed) != 1 || owed[0].Feedback.Body != "This loop never ends." || owed[0].Feedback.Path != "internal/a.go" {
		t.Fatalf("owed = %#v, want the decision recorded with the feedback as evidence", owed)
	}

	carried, err := repairer.CarryOut(context.Background())
	if err != nil {
		t.Fatalf("CarryOut() error = %v", err)
	}
	if len(carried) != 1 || !carried[0].Started || carried[0].NewRunID != "run-"+strings.Repeat("1", 32) || carried[0].Failure != "" {
		t.Fatalf("carried = %#v, want one new run started for yoyodyne-a", carried)
	}
	if len(*starts) != 1 || (*starts)[0].item != "yoyodyne-a" {
		t.Fatalf("starts = %v, want exactly one run of yoyodyne-a", *starts)
	}
	selection := (*starts)[0].selection
	if selection.By != runstate.SelectedByDevelopmentManager || !strings.Contains(selection.Reason, "line-comment-21") || !strings.Contains(selection.Reason, "pull request #7") || !strings.Contains(selection.Reason, "conversation chat-dm") {
		t.Fatalf("selection = %#v, want the development manager's decision and the entry it answers named", selection)
	}
	notes := items.items["yoyodyne-a"].Notes
	if !strings.Contains(notes, "This loop never ends.") || !strings.Contains(notes, "not an instruction that widens") {
		t.Fatalf("notes = %q, want the feedback given as evidence", notes)
	}
	if len(items.released) != 1 {
		t.Fatalf("released = %v, want the landed run's claim given back so the new run can take it", items.released)
	}
	after := f.load(t, "yoyodyne-a")
	if !after.WaitingForTeamMerge() || after.Status != f.load(t, "yoyodyne-a").Status {
		t.Fatalf("the landed run = %#v, want it left as integrated and still waiting", after.TeamMerge)
	}
	repair, _ := after.TeamMerge.Repair(7, "line-comment-21")
	if repair.StartedRunID != "run-"+strings.Repeat("1", 32) || len(after.TeamMerge.RepairsOwed()) != 0 {
		t.Fatalf("repair = %#v, want the new run recorded on the decision", repair)
	}

	again, err := repairer.CarryOut(context.Background())
	if err != nil || len(again) != 0 || len(*starts) != 1 {
		t.Fatalf("second CarryOut() = %#v, %v with %d starts; want the decision carried out once", again, err, len(*starts))
	}
}

func TestRepairFromFeedbackIsRefusedWithItsReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("an entry is answered once", func(t *testing.T) {
		t.Parallel()
		_, _, budget, _, repairer := repairFixture(t)
		if _, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "line-comment-21")); err != nil {
			t.Fatalf("first Decide() error = %v", err)
		}
		_, err := repairer.Decide(ctx, decideRequest("yoyodyne-first", "line-comment-21"))
		if err == nil || !strings.Contains(err.Error(), "already answered") || len(budget.spent) != 1 {
			t.Fatalf("second Decide() = %v with %v spent, want it refused as already answered and nothing more spent", err, budget.spent)
		}
	})
	t.Run("an item that has closed", func(t *testing.T) {
		t.Parallel()
		f, items, budget, _, repairer := repairFixture(t)
		if _, err := f.store.Update(ctx, f.runs["yoyodyne-a"], runstate.TeamMergeFound{LandingCommit: f.landing["yoyodyne-a"], FoundOn: strings.Repeat("f", 40), At: time.Now().UTC()}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		items.items["yoyodyne-a"].Status = "closed"
		_, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "line-comment-21"))
		if err == nil || !strings.Contains(err.Error(), "has closed") || len(budget.spent) != 0 {
			t.Fatalf("Decide() = %v with %v spent, want it refused because the item has closed, spending nothing", err, budget.spent)
		}
	})
	t.Run("an item the pull request does not carry", func(t *testing.T) {
		t.Parallel()
		_, _, _, _, repairer := repairFixture(t)
		_, err := repairer.Decide(ctx, decideRequest("yoyodyne-unknown", "line-comment-21"))
		if err == nil || !strings.Contains(err.Error(), "not waiting for the team's merge as carried by pull request #7") {
			t.Fatalf("Decide() = %v, want it refused for naming an item the pull request does not carry", err)
		}
	})
	t.Run("feedback that is not there, and an approval", func(t *testing.T) {
		t.Parallel()
		_, _, budget, _, repairer := repairFixture(t)
		if _, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "line-comment-99")); err == nil || !strings.Contains(err.Error(), "was not found on pull request #7") {
			t.Fatalf("Decide() on missing feedback = %v, want it refused", err)
		}
		if _, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "review-31")); err == nil || !strings.Contains(err.Error(), "approval") {
			t.Fatalf("Decide() on an approval = %v, want it refused", err)
		}
		if len(budget.spent) != 0 {
			t.Fatalf("spent = %v, want nothing spent on a refusal", budget.spent)
		}
	})
	t.Run("an item with no attempts left", func(t *testing.T) {
		t.Parallel()
		f, _, budget, _, repairer := repairFixture(t)
		budget.refuses = errors.New("re-run budget is spent")
		_, err := repairer.Decide(ctx, decideRequest("yoyodyne-a", "line-comment-21"))
		if err == nil || !strings.Contains(err.Error(), "no attempt left") {
			t.Fatalf("Decide() = %v, want it refused for want of an attempt", err)
		}
		if owed := f.load(t, "yoyodyne-a").TeamMerge.RepairsOwed(); len(owed) != 0 {
			t.Fatalf("owed = %#v, want no decision recorded", owed)
		}
	})
}

func TestRepairFromFeedbackWaitsWhileIntakeIsHeld(t *testing.T) {
	t.Parallel()
	f, _, _, starts, repairer := repairFixture(t)
	if _, err := repairer.Decide(context.Background(), decideRequest("yoyodyne-a", "line-comment-21")); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	repairer.Intake = heldIntake{}
	carried, err := repairer.CarryOut(context.Background())
	if err != nil || len(carried) != 1 || carried[0].Started || !strings.Contains(carried[0].Waiting, "held") || len(*starts) != 0 {
		t.Fatalf("CarryOut() = %#v, %v; want it waiting on the intake hold with nothing started", carried, err)
	}
	if owed := f.load(t, "yoyodyne-a").TeamMerge.RepairsOwed(); len(owed) != 1 {
		t.Fatalf("owed = %#v, want the decision left standing", owed)
	}
}

func TestAnEarlierLandedChangeReachingTheTeamDoesNotCloseAnItemWithALaterRepairWaiting(t *testing.T) {
	t.Parallel()
	earlier := runstate.State{RunID: "run-a", WorkItemID: "yoyodyne-a", TeamMerge: &runstate.TeamMerge{Since: time.Unix(100, 0)}}
	later := runstate.State{RunID: "run-b", WorkItemID: "yoyodyne-a", TeamMerge: &runstate.TeamMerge{Since: time.Unix(200, 0)}}
	other := runstate.State{RunID: "run-c", WorkItemID: "yoyodyne-b", TeamMerge: &runstate.TeamMerge{Since: time.Unix(300, 0)}}
	if got, ok := laterWaitOfItem([]runstate.State{earlier, later, other}, earlier); !ok || got.RunID != "run-b" {
		t.Fatalf("laterWaitOfItem(earlier) = %v, %v; want the later run of the same item", got.RunID, ok)
	}
	if _, ok := laterWaitOfItem([]runstate.State{earlier, later, other}, later); ok {
		t.Fatal("laterWaitOfItem(later) found a later run, want none")
	}
}
