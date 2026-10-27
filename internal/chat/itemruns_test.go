package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The yoyodyne-ifd.430.13.4 shape, 2026-09-26. The note naming the stopped run
// was written early and the notes kept growing past it, so the read — which
// keeps the end of the notes and cuts the front — cut the one line a decision
// needed, and the development manager could not find the run to decide about.
// Reads now preserve the latest stop as an excerpt. The runs still come from
// the harness's records in a separate section, with their status, times, and
// preservation evidence, however long the notes have grown.
func TestAnItemReadCarriesTheRunItsHoldIsAboutWhenTheNotesAreCut(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.430.13.4"
	const stoppedRun = "run-6e1f0c3a9b2d4e5f8a7b6c5d4e3f2a1b"
	started := time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC)
	ended := started.Add(90 * time.Minute)

	var notes strings.Builder
	notes.WriteString("Yoyodyne stopped this item: run " + stoppedRun + " failed review after every permitted attempt.\n\n")
	for notes.Len() < 4*maxTrackerItemBytes {
		notes.WriteString("The sweep looked at this item again and recorded nothing new about it.\n")
	}
	item := beads.WorkItem{ID: itemID, Title: "A stopped item", Status: "blocked", Priority: 1, IssueType: "task", Notes: notes.String()}

	// The continuous notes view still cuts past the run's note, while the latest
	// stop is preserved separately. Neither the cut nor that excerpt replaces
	// the durable run list checked below.
	if strings.Contains(boundTextTail(item.Notes, maxTrackerItemBytes), stoppedRun) {
		t.Fatal("the continuous notes view did not cut past the run's note")
	}
	rendered := renderWorkItemEvidence(item, recordedGoals(recordedGoal))
	for _, want := range []string{stoppedRun, "Stop and decision excerpts from the cut notes", "Older notes have no recorded append boundaries", "are cut; treat them as unread rather than absent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the item rendering does not carry %q:\n%s", want, rendered)
		}
	}

	work := &fakeWork{price: ItemPrice{Runs: []RunPrice{
		{RunID: "run-earlier", Status: "succeeded", Outcome: "succeeded", StartedAt: started.Add(-48 * time.Hour), CompletedAt: timePointer(started.Add(-47 * time.Hour)), Remains: "work removed"},
		{RunID: stoppedRun, Status: "failed", Outcome: "blocked", Phase: "reviewing", StartedAt: started, CompletedAt: &ended, Remains: "work preserved"},
	}}}
	held := fakeHeldWork{holds: backlog.ReadHolds(map[string]backlog.Hold{
		itemID: {Reason: "run " + stoppedRun + " stopped on it and its change is preserved (branch and worktree checked and there); the development manager decides what happens to it", RunID: stoppedRun},
	})}

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Reading it.", `{"action":"read","id":"`+itemID+`"}`)},
				{SessionID: "session-1", FinalText: "Read."},
			}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{itemID: item}}
			options.Work = work
			options.Held = held
			session := openTestSession(t, options)

			reply, err := session.Send(context.Background(), "Which run stopped it?")
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v, want the read applied", reply.Actions)
			}
			detail := reply.Actions[0].Detail
			for _, want := range []string{
				"runs (2 recorded, newest first):",
				"- " + stoppedRun + " started 2026-09-25T14:00:00Z, ended 2026-09-25T15:30:00Z [blocked, reviewing] work preserved — the item's hold is about this run",
				"- run-earlier started",
				"work removed",
				"held: it is about run " + stoppedRun + ";",
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("the read does not carry %q:\n%s", want, detail[max(0, len(detail)-1500):])
				}
			}
			// Newest first: the stoppage is listed before the run that came before it.
			if strings.Index(detail, "- "+stoppedRun) > strings.Index(detail, "- run-earlier") {
				t.Errorf("the runs are not newest first:\n%s", detail[len(detail)-1000:])
			}
		})
	}
}

// The section is bounded by how many runs it lists rather than by bytes, and
// the run the hold is about survives the bound however old it is.
func TestAnItemReadListsABoundedNumberOfRunsAndAlwaysTheHoldsRun(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var runs []RunPrice
	for index := range maxItemRunsListed + 5 {
		runs = append(runs, RunPrice{RunID: fmt.Sprintf("run-%02d", index), Status: "failed", Outcome: "failed", StartedAt: started.Add(time.Duration(index) * time.Hour)})
	}
	listed, unlisted := listedItemRuns(runs, "run-00")
	if len(listed) != maxItemRunsListed || unlisted != 5 {
		t.Fatalf("listed %d and left out %d, want %d and 5", len(listed), unlisted, maxItemRunsListed)
	}
	if listed[0].RunID != fmt.Sprintf("run-%02d", maxItemRunsListed+4) {
		t.Errorf("the first run listed is %s, want the newest", listed[0].RunID)
	}
	if !runRecorded(listed, "run-00") {
		t.Errorf("the run the hold is about was dropped by the bound: %#v", listed)
	}
}

// A read whose run records or holds could not be read says so, rather than
// reading as an item nothing was ever run for and nothing holds.
func TestAnItemReadSaysWhenItsRunsCouldNotBeRead(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{priceErr: fmt.Errorf("the run store is locked")}
	options.Held = fakeHeldWork{err: fmt.Errorf("the triage record is locked")}
	session := openTestSession(t, options)

	rendered := session.renderItemRuns(context.Background(), "yoyodyne-ifd.1")
	for _, want := range []string{
		"runs: could not be read, so treat them as unknown rather than none: the run store is locked",
		"whether a stoppage holds this item is unknown: the triage record is locked",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the section = %q, want it to say %q", rendered, want)
		}
	}
}

func timePointer(at time.Time) *time.Time { return &at }
