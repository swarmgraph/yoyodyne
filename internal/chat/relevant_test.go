package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

func TestAdmissionCarriesTwoRelevantGoalsToTheRunAndReview(t *testing.T) {
	relevant := []string{recordedGoal, "Use ordinary words."}
	action, _ := json.Marshal(TrackerAction{Action: actionCreate, Title: "Record goal relevance", Description: "Keep the admission traceable.", Goal: recordedGoal, RelevantGoals: relevant, Kind: domain.WorkItemKindFeature, Reason: "the goals apply to this work"})
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: trackerReply("Admitting it.", string(action))}, {SessionID: "session-1", FinalText: "Admitted."}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(relevant...)
	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil || len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.created) != 1 {
		t.Fatalf("admission = %#v, %v", reply, err)
	}
	created := tracker.created[0]
	if !slices.Equal(created.RelevantGoals, relevant) {
		t.Fatalf("created goals = %q", created.RelevantGoals)
	}
	item := beads.WorkItem{ID: "yoyodyne-task", Title: created.Title, Notes: created.Notes, RelevantGoals: created.RelevantGoals}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: t.TempDir(), WorkItem: item})
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "review-session", FinalText: `{"decision":"approve","approves":"implementation","summary":"Checked the goals."}`, Process: execution.ProcessResult{Status: execution.ProcessSucceeded}}}}
	_, err = (review.Reviewer{Backend: provider, Model: "opus"}).Review(context.Background(), review.Request{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: item.ID, Context: bundle.Text, WorktreePath: "/worktree"})
	if err != nil {
		t.Fatal(err)
	}
	for _, briefing := range []string{bundle.Text, provider.requests[0].Prompt} {
		for _, want := range append([]string{"goals the change must not break"}, relevant...) {
			if !strings.Contains(briefing, want) {
				t.Fatalf("briefing omitted %q", want)
			}
		}
	}
}

func TestUnknownRelevantGoalRefusesAnAdmission(t *testing.T) {
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: trackerReply("Admitting it.", `{"action":"create","kind":"feature","title":"Record relevance","description":"Record the list.","goal":"`+recordedGoal+`","relevant_goals":["Unknown goal."],"reason":"test the refusal"}`)}, {SessionID: "session-1", FinalText: "The goal was refused."}}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil || len(reply.Actions) != 1 || reply.Actions[0].Applied || len(tracker.created) != 0 || !strings.Contains(reply.Actions[0].Failure, "Unknown goal.") {
		t.Fatalf("refusal = %#v, %v", reply, err)
	}
}

func TestRelevantGoalUpdateKeepsOmittedAndEmptyListsDistinct(t *testing.T) {
	for _, list := range [][]string{nil, {}, {recordedGoal}} {
		original := TrackerAction{Action: actionUpdate, ID: "yoyodyne-task", RelevantGoals: list, Reason: "record the assessment"}
		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var restored TrackerAction
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if (restored.RelevantGoals == nil) != (list == nil) || !slices.Equal(restored.RelevantGoals, list) {
			t.Fatalf("round trip = %s -> %#v", encoded, restored)
		}
	}
}

func TestRelevantGoalProposalSurvivesRecordingAndRefusesUnknownGoals(t *testing.T) {
	relevant := []string{recordedGoal, "Use ordinary words."}
	proposal := Proposal{Title: "Record relevance", Description: "Record the list.", Goal: recordedGoal, RelevantGoals: relevant, Rationale: "the goals apply"}
	restored := restoredProposal("conversation-1", (PendingProposal{ID: "1.1", Turn: 1, Proposal: proposal}).recorded())
	if !slices.Equal(restored.Proposal.RelevantGoals, relevant) {
		t.Fatalf("restored relevant goals = %q", restored.Proposal.RelevantGoals)
	}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Nothing to admit."}}})
	options.Goals = recordedGoals(relevant...)
	session := openTestSession(t, options)
	if err := session.verifyProposalGoals([]Proposal{proposal}); err != nil {
		t.Fatal(err)
	}
	proposal.RelevantGoals = []string{"Unknown goal."}
	if err := session.verifyProposalGoals([]Proposal{proposal}); err == nil {
		t.Fatal("unknown relevant goal accepted")
	}
}

func TestSurveyNamesItemsWithoutRelevantGoals(t *testing.T) {
	items := []beads.WorkItem{{ID: "yoyodyne-missing", Title: "Assess the older work", Status: "open"}, {ID: "yoyodyne-recorded", Title: "Record relevance", Status: "open", RelevantGoals: []string{recordedGoal}}}
	rendered := renderOpenQueueEvidence(items, recordedGoals(recordedGoal))
	if !strings.Contains(rendered, "Admitted items with no relevant goals recorded: Assess the older work (yoyodyne-missing)") || !strings.Contains(rendered, "relevant goals: "+recordedGoal) {
		t.Fatalf("survey = %s", rendered)
	}
}

func TestUpdatingRelevantGoalsResolvesAndClearsTheList(t *testing.T) {
	for _, list := range [][]string{{recordedGoal}, {}, {"Unknown goal."}} {
		tracker := &fakeTracker{items: map[string]beads.WorkItem{"yoyodyne-task": {ID: "yoyodyne-task", Title: "Record relevance", Status: "open"}}}
		action, err := json.Marshal(TrackerAction{Action: actionUpdate, ID: "yoyodyne-task", RelevantGoals: list, Reason: "record the assessment"})
		if err != nil {
			t.Fatal(err)
		}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: trackerReply("Updating it.", string(action))}, {SessionID: "session-1", FinalText: "The result is recorded."}}})
		options.Tracker = tracker
		options.Goals = recordedGoals(recordedGoal)
		reply, err := openTestSession(t, options).Send(context.Background(), "record the list")
		if err != nil {
			t.Fatal(err)
		}
		if len(list) > 0 && list[0] == "Unknown goal." {
			if len(tracker.updates) != 0 || reply.Actions[0].Applied {
				t.Fatalf("unknown goal reached tracker: %#v", reply)
			}
			continue
		}
		if len(tracker.updates) != 1 || tracker.updates[0].change.RelevantGoals == nil || !slices.Equal(tracker.updates[0].change.RelevantGoals, list) {
			t.Fatalf("updates = %#v", tracker.updates)
		}
	}
}
