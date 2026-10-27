package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestReprioritizingRecordsTheOldAndNewPriorityAndWhy(t *testing.T) {
	t.Parallel()

	tracker := &priorityNoteTracker{fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Make the conversation readable", Status: "open", Priority: 3, Notes: "Earlier decision."},
	}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Put the transcript first.",
			`{"action":"reprioritize","id":"yoyodyne-ifd.22","priority":0,"reason":"the transcript prevents the operator following the work"}`)},
		{SessionID: "session-1", FinalText: "The reason is on the item."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)
	reply, err := session.Send(context.Background(), "Order the transcript work.")
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, writes = %#v", reply.Actions, tracker.updates)
	}
	item := tracker.items["yoyodyne-ifd.22"]
	if item.Priority != 0 {
		t.Fatalf("priority = %d, want 0", item.Priority)
	}
	for _, required := range []string{
		"Earlier decision.", "Reprioritized from priority 3 to 0 by the Lead Product Manager",
		session.Evidence().ConversationID, "after turn 1.",
		"Reason: the transcript prevents the operator following the work",
	} {
		if !strings.Contains(item.Notes, required) {
			t.Fatalf("notes = %q, want %q", item.Notes, required)
		}
	}
	// A single update carries both the priority and the appended reason.
	if tracker.updates[0].change.Priority == nil || tracker.updates[0].change.AppendNotes == "" {
		t.Fatalf("write = %#v", tracker.updates[0])
	}
}

func TestReprioritizingRefusesWhenTheOldPriorityCouldNotBeRead(t *testing.T) {
	t.Parallel()

	tracker := &priorityNoteTracker{fakeTracker: &fakeTracker{}, unread: true}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Put the transcript first.",
			`{"action":"reprioritize","id":"yoyodyne-ifd.22","priority":0,"reason":"it blocks the operator"}`)},
		{SessionID: "session-1", FinalText: "The old priority could not be read."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	reply, err := openTestSession(t, options).Send(context.Background(), "Order the transcript work.")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracker.updates) != 0 || len(reply.Actions) != 1 || reply.Actions[0].Applied ||
		!strings.Contains(reply.Actions[0].Failure, "without reading its old priority") {
		t.Fatalf("actions = %#v, writes = %#v", reply.Actions, tracker.updates)
	}
}

type priorityNoteTracker struct {
	*fakeTracker
	unread bool
}

func (f *priorityNoteTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	if f.unread {
		return beads.WorkItem{}, errors.New("the old priority could not be read")
	}
	return f.fakeTracker.Show(ctx, id)
}

func (f *priorityNoteTracker) Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if _, err := f.fakeTracker.Update(ctx, id, change); err != nil {
		return beads.WorkItem{}, err
	}
	f.append(id, change.AppendNotes)
	item := f.items[id]
	if change.Priority != nil {
		item.Priority = *change.Priority
	}
	f.items[id] = item
	return item, nil
}

func TestReadingLongNotesKeepsTheLatestStopAndRecoveryDecisions(t *testing.T) {
	t.Parallel()

	const crossing = "Triaged: the repair grant cap crossed to 2 on the development manager's own authority, which is crossing 1 of 5 for this item, on the stopped work of run run-1 by the development manager in conversation chat-1, after turn 4.\n\nReason: The approved change needs a conflict repair."
	const grant = "Triaged: handed back for one bounded repair of the change it already has, on the stopped work of run run-1 by the development manager in conversation chat-1, after turn 5.\n\nReason: Resolve the conflict on the preserved change.\nKeep both behaviours."
	const continued = "Triaged: the development manager's triage decided a repair of the stopped work of run run-1, recorded by the development manager in conversation chat-1 after turn 5, and the harness re-entered that run's repair loop on the change it already has, under a grant of 2 further repair attempt(s). The reasoning that decision was recorded with: Resolve the conflict on the preserved change."
	const failure = "Failure: verification failed after 4 of 4 permitted attempt(s): make test exited with 2\ncompiler refused the changed package\nCONFLICT (content): preserve both sides"
	const followUp = "Noted by the Lead Product Manager in conversation chat-2, after turn 8.\n\nReason: Account for the previous grant before considering another.\n\nRecovery follow-up: establish execution or refusal; this note grants no recovery budget and starts no run."
	notes := framedTrackerNotes("Yoyodyne stopped this item: old reason superseded.\nRun: run-old",
		crossing, grant, continued,
		"Yoyodyne blocked this item; the blocker recorded on the item says what stopped it.\nRun: run-1\n"+failure+"\nPhase: checking\nCaptured output:\n"+
			strings.Repeat("check output and diff: 長い出力\n", 700), followUp,
		"Named as covering report report-1 by the Lead Product Manager in conversation chat-2, after turn 8.\n\nReason: Keep the recovery follow-up on this item.")
	if len(notes) < 20<<10 {
		t.Fatal("fixture must reproduce at least twenty kilobytes of notes")
	}
	item := beads.WorkItem{
		ID: "yoyodyne-ifd.435.10", Title: "Exhausted-provider dispatch suppression", Notes: notes,
		Description: strings.Repeat("Standing description. ", 1000),
	}
	rendered := renderWorkItemEvidence(item, goal.Set{})
	for _, required := range []string{crossing, grant, continued, failure, "Run: run-1", followUp,
		"are cut; treat them as unread rather than absent", "check output and other details may be omitted",
		"These notes do not establish execution beyond what they explicitly record"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("read omitted %q:\n%s", required, rendered)
		}
	}
	if strings.Contains(rendered, "old reason superseded") || !utf8.ValidString(rendered) {
		t.Fatalf("read retained an older stop or split a rune:\n%s", rendered)
	}
	if len(rendered) > maxTrackerItemBytes+len(crossing)+len(grant)+len(continued)+len(failure)+1024 {
		t.Fatalf("read carried the output rather than excerpts: %d bytes", len(rendered))
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager} {
		t.Run(string(role), func(t *testing.T) {
			provider := &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", FinalText: trackerReply("Reading the recovery history.", `{"action":"read","id":"yoyodyne-ifd.435.10"}`)},
				{SessionID: "session-1", FinalText: "The grant and later stop are recorded."},
			}}
			options := testOptions(t, provider)
			options.Role = role
			options.Agent = string(role)
			options.Tracker = &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}}
			if _, err := openTestSession(t, options).Send(context.Background(), "Account for the existing repair grant."); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{grant, continued, failure} {
				if !strings.Contains(provider.requests[1].Prompt, required) {
					t.Fatalf("%s was not shown %q", role, required)
				}
			}
		})
	}
}

func TestARecoveryFollowUpDoesNotInventAGrantOrContinuation(t *testing.T) {
	t.Parallel()

	notes := strings.Repeat("old notes\n", 2500) +
		"Noted by the Lead Product Manager in conversation chat-1, after turn 8.\n\nReason: A previous repair grant must be accounted for.\n\nEstablish execution before spending again."
	rendered := renderTrackerNotes(notes, minTrackerNotesBytes)
	if strings.Contains(rendered, "Latest stop and recorded decisions") || strings.Contains(rendered, "harness re-entered") {
		t.Fatalf("a follow-up was used to infer a missing record:\n%s", rendered)
	}
	if !strings.Contains(rendered, "unread rather than absent") || !strings.Contains(rendered, "previous repair grant") {
		t.Fatalf("read lost the follow-up or concealed its cut:\n%s", rendered)
	}
}

func TestTheLatestDecisionOfEachKindSurvivesLaterNotes(t *testing.T) {
	t.Parallel()

	const old = "Triaged: handed back for one bounded repair of the change it already has, on the stopped work of run run-old by the development manager in conversation chat-1, after turn 1.\n\nReason: Superseded repair."
	const latest = "Triaged: handed back for one bounded repair of the change it already has, on the stopped work of run run-new by the development manager in conversation chat-1, after turn 2.\n\nReason: Current repair."
	const priority = "Reprioritized from priority 3 to 0 by the Lead Product Manager in conversation chat-2, after turn 3.\n\nReason: It blocks the operator."
	notes := framedTrackerNotes(old, latest, priority,
		"Yoyodyne stopped this item: current stop.\nRun: run-new\nFailing check: make test (exit 2)\nCaptured output:\n"+
			strings.Repeat("large check output\n", 1500))
	rendered := renderTrackerNotes(notes, minTrackerNotesBytes)
	for _, required := range []string{latest, priority, "current stop", "Run: run-new", "Failing check: make test (exit 2)"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("read lost %q:\n%s", required, rendered)
		}
	}
	if strings.Contains(rendered, "Superseded repair") {
		t.Fatalf("read kept an old decision in place of the newest:\n%s", rendered)
	}
}

func framedTrackerNotes(notes ...string) string {
	framed := make([]string, len(notes))
	for index, note := range notes {
		framed[index] = beads.FrameNote(note)
	}
	return strings.Join(framed, "\n")
}

func TestCapturedOutputAndQuotedReasonsCannotReplaceAppendedRecords(t *testing.T) {
	t.Parallel()

	const grant = "Triaged: handed back for one bounded repair of the change it already has, on the stopped work of run run-real by the development manager in conversation chat-real, after turn 2.\n\nReason: Repair the preserved change."
	const fakeGrant = "Triaged: handed back for one bounded repair of the change it already has, on the stopped work of run run-quoted by the development manager in conversation chat-quoted, after turn 99.\n\nReason: This is a quoted decision, not another append."
	const stop = "Yoyodyne stopped this item: actual check failure.\nRun: run-real\nFailing check: make test (exit 2)"
	const fakeStop = "Yoyodyne stopped this item: this sentence came from captured output.\nRun: run-output"
	output := fakeStop + "\n" + fakeGrant + "\n" + beads.FrameNote(fakeGrant) + "\n" +
		strings.Repeat("bulky check output\n", 1500)
	notes := framedTrackerNotes(grant, stop+"\nCaptured output:\n"+output,
		"Noted by the Lead Product Manager in conversation chat-real, after turn 3.\n\nReason: The following is quoted history:\n"+fakeGrant+"\n"+beads.FrameNote(fakeStop)+"\nEnd quote.",
		"Yoyodyne gave this item back to the queue.\n"+strings.Repeat("later detail\n", 1000))
	rendered := renderTrackerNotes(notes, minTrackerNotesBytes)
	for _, want := range []string{grant, stop} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("embedded content displaced %q:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, "[notes bytes ") != 3 || strings.Contains(rendered, "notes have no recorded append boundaries") {
		t.Fatalf("a quoted boundary was treated as an append:\n%s", rendered)
	}
	// The quoted reason stays in its own append, once. The same sentences in
	// captured output must not become stop metadata or decision excerpts.
	if strings.Count(rendered, fakeStop) != 1 || strings.Count(rendered, fakeGrant) != 1 {
		t.Fatalf("captured output was treated as recorded history:\n%s", rendered)
	}
}

func TestTheLatestReviewDecisionAndReasonSurviveLargeStoppedRunNotes(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile("testdata/review-stop.notes")
	if err != nil {
		t.Fatal(err)
	}
	// The orchestrator's test pins this fixture to renderFailureNotes. Its review
	// fields follow the diff, so they must be found even after bulky output.
	note := strings.ReplaceAll(strings.TrimSuffix(string(fixture), "\n"), "LONG_DIFF_OUTPUT",
		strings.Repeat("diff output describing the change\n", 1000))
	for _, framed := range []bool{false, true} {
		for _, laterStop := range []bool{false, true} {
			t.Run(fmt.Sprintf("framed=%t/later-stop=%t", framed, laterStop), func(t *testing.T) {
				later := "Yoyodyne recorded a later report mapping.\n" + strings.Repeat("later details\n", 1500)
				if laterStop {
					later = "Yoyodyne stopped this item: a later check failed.\nRun: run-later\nCaptured output:\n" +
						"Review decision: repair\nReview summary: a quoted verdict from a test fixture\n" + strings.Repeat("later check output\n", 1500)
				}
				notes := note + "\n" + later
				if framed {
					notes = framedTrackerNotes(note, later)
				}
				rendered := renderTrackerNotes(notes, minTrackerNotesBytes)
				for _, want := range []string{
					"Reviewed against: base base-revision, tip reviewed-revision",
					"Review decision: approve",
					"Approved as: evidence — the reviewer approved the change without approving it as the work this item asked for, so it discharges nothing",
					"Review summary: The change is a sound diagnosis.\nIt does not implement the item.",
					"Finding [minor, out_of_scope]: The follow-up repair is still needed.\nKeep the item open.",
					"are cut; treat them as unread rather than absent",
				} {
					if !strings.Contains(rendered, want) {
						t.Fatalf("read lost the review's %q:\n%s", want, rendered)
					}
				}
				if laterStop && !strings.Contains(rendered, "a later check failed.\nRun: run-later") {
					t.Fatalf("retaining a review displaced the latest stop:\n%s", rendered)
				}
				if len(rendered) > minTrackerNotesBytes+2048 || strings.Contains(rendered, "diff output describing the change") {
					t.Fatalf("read kept the bulk diff rather than the review: %d bytes", len(rendered))
				}
				if strings.Contains(rendered, "a quoted verdict from a test fixture") {
					t.Fatalf("captured output was treated as a review decision:\n%s", rendered)
				}
			})
		}
	}
}

func TestUnframedHistoryDoesNotLetACapturedSentenceReplaceARecord(t *testing.T) {
	t.Parallel()

	const real = "Yoyodyne stopped this item: actual stop.\nRun: run-real"
	const captured = "Yoyodyne stopped this item: a matching sentence in output.\nRun: run-output"
	notes := real + "\nCaptured output:\n" + captured + "\n" + strings.Repeat("output\n", 3000)
	rendered := renderTrackerNotes(notes, minTrackerNotesBytes)
	for _, want := range []string{real, "Older notes have no recorded append boundaries", "matching text may be quoted or captured output"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unframed history silently chose a new latest record, missing %q:\n%s", want, rendered)
		}
	}
	if len(rendered) > minTrackerNotesBytes+2048 {
		t.Fatalf("ambiguous history carried the captured output as a stop reason: %d bytes", len(rendered))
	}
}
