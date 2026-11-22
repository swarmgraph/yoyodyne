package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

type fakeFeedbackRepairs struct {
	requests []FeedbackRepairRequest
	refuses  error
	persons  bool
}

func (f *fakeFeedbackRepairs) Decide(_ context.Context, request FeedbackRepairRequest) (FeedbackRepairDecided, error) {
	if f.refuses != nil {
		return FeedbackRepairDecided{}, f.refuses
	}
	f.requests = append(f.requests, request)
	return FeedbackRepairDecided{RunID: "run-fedcba9876543210fedcba9876543210", OnPersonsPullRequest: f.persons}, nil
}

const feedbackRepairAction = `{"action":"triage","id":"yoyodyne-ifd.435.1","decision":"repair-from-feedback","pull_request":7,"feedback":"line-comment-21","reason":"the loop is a real defect"}`

// Her decision to repair an item from feedback is recorded through the harness
// with who decided and where, and noted on the item.
func TestTheDevelopmentManagerDecidesARepairFromFeedback(t *testing.T) {
	t.Parallel()

	tracker := spentClaimItem()
	repairs := &fakeFeedbackRepairs{}
	options := triageOptions(t, tracker, nil, trackerReply("Deciding the feedback.", feedbackRepairAction))
	options.FeedbackRepairs = repairs
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(repairs.requests) != 1 {
		t.Fatalf("requests = %#v, want one", repairs.requests)
	}
	got := repairs.requests[0]
	if got.PullRequest != 7 || got.FeedbackID != "line-comment-21" || got.WorkItemID != "yoyodyne-ifd.435.1" ||
		got.DecidedBy != RoleTitle(domain.RoleDevelopmentManager) || got.Conversation == "" || !strings.Contains(got.Reason, "real defect") {
		t.Fatalf("request = %#v, want the entry, the item, her reason, and who decided it and where", got)
	}
	if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "repaired from feedback") {
		t.Fatalf("updates = %#v, want the decision noted on the item", tracker.updates)
	}
}

// The note on the item says which kind of pull request the feedback is on: one
// into the team's branch, or one the harness opened for a person to merge, and
// never the team's branch for the second.
func TestTheRepairNoteNamesWhichPullRequestTheFeedbackIsOn(t *testing.T) {
	t.Parallel()

	for _, persons := range []bool{false, true} {
		tracker := spentClaimItem()
		options := triageOptions(t, tracker, nil, trackerReply("Deciding the feedback.", feedbackRepairAction))
		options.FeedbackRepairs = &fakeFeedbackRepairs{persons: persons}
		reply := triageSend(t, options)
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
			t.Fatalf("person's pull request %v: actions = %#v, updates = %#v", persons, reply.Actions, tracker.updates)
		}
		note := tracker.updates[0].change.AppendNotes
		if !strings.Contains(note, "answering line-comment-21 on pull request #7") {
			t.Errorf("person's pull request %v: note = %q, want the feedback and the pull request", persons, note)
		}
		teams := strings.Contains(note, "team's branch")
		opened := strings.Contains(note, "which the harness opened for a person to merge")
		if persons && (teams || !opened) {
			t.Errorf("note = %q, want it to say the harness opened the pull request for a person to merge, and not name the team's branch", note)
		}
		if !persons && (!teams || opened) {
			t.Errorf("note = %q, want it to name the pull request into the team's branch", note)
		}
	}
}

// A decision the harness refuses is refused with its reason and noted nowhere.
func TestARepairFromFeedbackTheHarnessRefusesIsNotRecorded(t *testing.T) {
	t.Parallel()

	tracker := spentClaimItem()
	options := triageOptions(t, tracker, nil, trackerReply("Deciding the feedback.", feedbackRepairAction))
	options.FeedbackRepairs = &fakeFeedbackRepairs{refuses: errors.New("yoyodyne-ifd.435.1 has closed")}
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Summary+reply.Actions[0].Detail+reply.Actions[0].Failure, "has closed") {
		t.Fatalf("actions = %#v, want it refused with the harness's reason", reply.Actions)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want nothing noted", tracker.updates)
	}
}

// Naming no entry, or naming a run, is refused before anything is asked.
func TestARepairFromFeedbackNamingNoEntryIsRefused(t *testing.T) {
	t.Parallel()

	for name, action := range map[string]string{
		"no pull request":     `{"action":"triage","id":"yoyodyne-ifd.435.1","decision":"repair-from-feedback","feedback":"line-comment-21","reason":"why"}`,
		"no feedback":         `{"action":"triage","id":"yoyodyne-ifd.435.1","decision":"repair-from-feedback","pull_request":7,"reason":"why"}`,
		"a run":               `{"action":"triage","id":"yoyodyne-ifd.435.1","run":"run-fedcba9876543210fedcba9876543210","decision":"repair-from-feedback","pull_request":7,"feedback":"line-comment-21","reason":"why"}`,
		"on another decision": `{"action":"triage","id":"yoyodyne-ifd.435.1","run":"run-fedcba9876543210fedcba9876543210","decision":"rerun","pull_request":7,"reason":"why"}`,
	} {
		repairs := &fakeFeedbackRepairs{}
		options := triageOptions(t, spentClaimItem(), nil, trackerReply("Deciding.", action))
		options.FeedbackRepairs = repairs
		reply := triageSend(t, options)
		applied := false
		for _, outcome := range reply.Actions {
			applied = applied || outcome.Applied
		}
		if applied || len(repairs.requests) != 0 {
			t.Fatalf("%s: actions = %#v with %d requests, want it refused before anything was asked", name, reply.Actions, len(repairs.requests))
		}
	}
}

// A conversation with nothing wired refuses rather than appearing to decide.
func TestARepairFromFeedbackWithNothingWiredIsRefused(t *testing.T) {
	t.Parallel()

	options := triageOptions(t, spentClaimItem(), nil, trackerReply("Deciding the feedback.", feedbackRepairAction))
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want it refused", reply.Actions)
	}
}
