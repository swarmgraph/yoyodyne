package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

type listedItems []beads.WorkItem

func (l *listedItems) List(context.Context, string) ([]beads.WorkItem, error) { return *l, nil }

// passChangeRecords is every record the readers read, held so a test can
// change one and see which digest moves.
type passChangeRecords struct {
	head          string
	items         listedItems
	conversations []runstate.Conversation
	runs          []runstate.State
	followUps     []followup.FollowUp
	docket        []triage.Entry
	conditions    []string
	now           time.Time
}

func (r *passChangeRecords) sources() passChanges {
	return passChanges{
		head:          func(context.Context) (string, error) { return r.head, nil },
		items:         &r.items,
		conversations: func() ([]runstate.Conversation, error) { return r.conversations, nil },
		runs:          func() ([]runstate.State, error) { return r.runs, nil },
		followUps:     func() ([]followup.FollowUp, error) { return r.followUps, nil },
		docket:        func() ([]triage.Entry, error) { return r.docket, nil },
		health: func(context.Context, domain.AgentRole, string) ([]string, bool) {
			return r.conditions, true
		},
		now: func() time.Time { return r.now },
	}
}

var allPassChangeKinds = []orchestrator.PassChangeKind{
	orchestrator.ChangeLanded, orchestrator.ChangeWork, orchestrator.ChangeMessages, orchestrator.ChangeDocuments,
	orchestrator.ChangeFollowUps, orchestrator.ChangeStops, orchestrator.ChangeHealth,
}

func passChangeFixture() *passChangeRecords {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	return &passChangeRecords{
		head:  "aaaa",
		items: listedItems{{ID: "yoyodyne-ifd.1", Status: "open", Executor: domain.ConversationWith(domain.RoleDevelopmentManager)}, {ID: "yoyodyne-ifd.2", Status: "open"}},
		conversations: []runstate.Conversation{
			{ConversationID: "chat-dm", Role: domain.RoleDevelopmentManager},
			{ConversationID: "chat-pm", Role: domain.RoleProductManager},
		},
		runs:      []runstate.State{{RunID: "run-doc", Status: runstate.StatusRunning, Document: &runstate.DocumentPublication{Owner: domain.RoleDevelopmentManager}}},
		followUps: []followup.FollowUp{{ID: "followup-1", Owner: ownership.MoverOf(domain.RoleDevelopmentManager), State: followup.StateOpen, BackToOwnerAt: now.Add(time.Hour), ToManagerAt: now.Add(2 * time.Hour)}},
		docket:    []triage.Entry{{Key: "run-1", RunID: "run-1"}},
		now:       now,
	}
}

// Each reader's digest moves when, and only when, what the development
// manager would be handed of its kind changes.
func TestEachPassChangeReaderMovesOnlyOnItsOwnRecord(t *testing.T) {
	t.Parallel()

	scope := orchestrator.PassScope{Role: domain.RoleDevelopmentManager, Health: true}
	cases := []struct {
		kind   orchestrator.PassChangeKind
		change func(*passChangeRecords)
		// unrelated is a change of the same record that is not the role's, which
		// must leave the digest where it was.
		unrelated func(*passChangeRecords)
	}{
		{kind: orchestrator.ChangeLanded, change: func(r *passChangeRecords) { r.head = "bbbb" }},
		{kind: orchestrator.ChangeWork,
			change:    func(r *passChangeRecords) { r.items[0].Notes = "a new note" },
			unrelated: func(r *passChangeRecords) { r.items[1].Status = "closed" }},
		{kind: orchestrator.ChangeMessages,
			change:    func(r *passChangeRecords) { r.conversations[0].Told++ },
			unrelated: func(r *passChangeRecords) { r.conversations[1].Told++ }},
		{kind: orchestrator.ChangeMessages,
			change: func(r *passChangeRecords) {
				r.conversations[0].PendingNotices = []string{"the operator approved document-1.1"}
			}},
		{kind: orchestrator.ChangeDocuments,
			// A document that landed leaves the runs in flight.
			change: func(r *passChangeRecords) { r.runs = nil }},
		{kind: orchestrator.ChangeDocuments,
			change: func(r *passChangeRecords) {
				r.conversations[0].HandedBack = []runstate.HandedBackDocument{{ID: "document-1.1", Reason: "returned"}}
			}},
		{kind: orchestrator.ChangeFollowUps,
			change:    func(r *passChangeRecords) { r.now = r.now.Add(90 * time.Minute) },
			unrelated: func(r *passChangeRecords) { r.now = r.now.Add(30 * time.Minute) }},
		{kind: orchestrator.ChangeStops,
			change: func(r *passChangeRecords) { r.docket = append(r.docket, triage.Entry{Key: "run-2", RunID: "run-2"}) }},
		{kind: orchestrator.ChangeStops,
			// A document held for her decision in another role's conversation.
			change: func(r *passChangeRecords) {
				r.conversations[1].PendingWrites = []runstate.PendingWrite{{ID: "document-4.1", Artifact: "held-design",
					ReturnsHold: &runstate.DocumentReturnsHold{Why: "written against the same copy", HeldAt: r.now}}}
			}},
		{kind: orchestrator.ChangeHealth,
			change: func(r *passChangeRecords) { r.conditions = []string{"empty-slot/developer-1"} }},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			records := passChangeFixture()
			before := records.sources().Fingerprints(context.Background(), scope, allPassChangeKinds)
			if len(before.Unread) != 0 {
				t.Fatalf("unread = %v, want every kind read", before.Unread)
			}
			if tc.unrelated != nil {
				tc.unrelated(records)
				after := records.sources().Fingerprints(context.Background(), scope, allPassChangeKinds)
				if after.Digests[tc.kind] != before.Digests[tc.kind] {
					t.Fatalf("%s moved on a record that is not the development manager's", tc.kind)
				}
			}
			tc.change(records)
			after := records.sources().Fingerprints(context.Background(), scope, allPassChangeKinds)
			if after.Digests[tc.kind] == before.Digests[tc.kind] {
				t.Fatalf("%s did not move on a change the development manager would be handed", tc.kind)
			}
			for _, other := range allPassChangeKinds {
				if other != tc.kind && after.Digests[other] != before.Digests[other] {
					t.Errorf("%s moved as well, on a change that was not of its kind", other)
				}
			}
		})
	}
}

// The results of the role's own last actions, waiting to be told on its next
// turn, are not a new message: every pass that writes a lane report or a memory
// note leaves some, with a version that rises each time, and reading them made
// every such pass start the next.
func TestTheRolesOwnResultsDoNotMoveItsMessages(t *testing.T) {
	t.Parallel()

	scope := orchestrator.PassScope{Role: domain.RoleProgramManager, Agent: "flow-pm", Lane: "factory-flow"}
	kinds := []orchestrator.PassChangeKind{orchestrator.ChangeMessages}
	records := passChangeFixture()
	records.conversations = []runstate.Conversation{{ConversationID: "chat-flow", Role: domain.RoleProgramManager, Agent: "flow-pm",
		PendingTrackerResults: "# Lane report result\n\nYour lane report was written as version 1901.\n"}}
	before := records.sources().Fingerprints(context.Background(), scope, kinds)
	records.conversations[0].PendingTrackerResults = "# Memory results\n\n- remember: recorded as revision 17\n\n# Lane report result\n\nYour lane report was written as version 1902.\n"
	after := records.sources().Fingerprints(context.Background(), scope, kinds)
	if after.Digests[orchestrator.ChangeMessages] != before.Digests[orchestrator.ChangeMessages] {
		t.Fatal("messages moved on the results of the role's own last pass")
	}
	records.conversations[0].PendingTrackerResults += "\nThe document you sent was handed back.\n"
	records.conversations[0].Told++
	if handed := records.sources().Fingerprints(context.Background(), scope, kinds); handed.Digests[orchestrator.ChangeMessages] == after.Digests[orchestrator.ChangeMessages] {
		t.Fatal("messages did not move on a handoff the harness told the role")
	}
}

// A program manager's work is the items in its lane, and its conversation is
// its own instance's.
func TestAProgramManagersWorkIsItsLane(t *testing.T) {
	t.Parallel()

	scope := orchestrator.PassScope{Role: domain.RoleProgramManager, Agent: "flow-pm", Lane: "factory-flow"}
	records := passChangeFixture()
	records.items = listedItems{{ID: "yoyodyne-ifd.7", Status: "open", Labels: []string{"factory-flow"}}, {ID: "yoyodyne-ifd.8", Status: "open", Labels: []string{"cost"}}}
	before := records.sources().Fingerprints(context.Background(), scope, []orchestrator.PassChangeKind{orchestrator.ChangeWork})
	records.items[1].Priority = 0
	records.items[1].Status = "in_progress"
	if after := records.sources().Fingerprints(context.Background(), scope, []orchestrator.PassChangeKind{orchestrator.ChangeWork}); after.Digests[orchestrator.ChangeWork] != before.Digests[orchestrator.ChangeWork] {
		t.Fatal("work moved on an item outside the lane")
	}
	records.items[0].Status = "in_progress"
	if after := records.sources().Fingerprints(context.Background(), scope, []orchestrator.PassChangeKind{orchestrator.ChangeWork}); after.Digests[orchestrator.ChangeWork] == before.Digests[orchestrator.ChangeWork] {
		t.Fatal("work did not move on an item in the lane")
	}
}

// A record that cannot be read is said as unread, never digested as empty,
// and the health counts its conditions for the hour rule.
func TestAPassChangeReaderThatCannotReadSaysSo(t *testing.T) {
	t.Parallel()

	records := passChangeFixture()
	records.conditions = []string{"held-run/run-9", "empty-slot/developer-1"}
	sources := records.sources()
	sources.head = func(context.Context) (string, error) { return "", errors.New("git could not read the target branch") }
	sources.followUps = nil
	reading := sources.Fingerprints(context.Background(), orchestrator.PassScope{Role: domain.RoleDevelopmentManager, Health: true}, allPassChangeKinds)
	if _, unread := reading.Unread[orchestrator.ChangeLanded]; !unread {
		t.Errorf("unread = %v, want the target branch said as unread", reading.Unread)
	}
	if _, unread := reading.Unread[orchestrator.ChangeFollowUps]; !unread {
		t.Errorf("unread = %v, want a follow-up record nobody wired said as unread", reading.Unread)
	}
	if _, digested := reading.Digests[orchestrator.ChangeLanded]; digested {
		t.Error("the target branch was digested though it could not be read")
	}
	if reading.HealthConditions != 2 {
		t.Errorf("health conditions = %d, want the two standing", reading.HealthConditions)
	}
	incomplete := records.sources()
	incomplete.health = func(context.Context, domain.AgentRole, string) ([]string, bool) { return nil, false }
	if reading := incomplete.Fingerprints(context.Background(), orchestrator.PassScope{Role: domain.RoleDevelopmentManager, Health: true}, allPassChangeKinds); reading.Unread[orchestrator.ChangeHealth] == "" {
		t.Error("an observation that could not read every source was digested rather than said as unread")
	}
}
