package chat

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// correctionSession is a product manager's conversation over a tracker, whose
// one turn carries out the actions given.
func correctionSession(t *testing.T, tracker *fakeTracker, actions ...string) *Session {
	t.Helper()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Correcting the closed work.", actions...)},
		{SessionID: "session-1", FinalText: "Done."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	return openTestSession(t, options)
}

const closedItem = "yoyodyne-ifd.10"

func correctionCreate(corrects string, extra string) string {
	return `{"action":"create","kind":"bug","title":"Say the held line in words","description":"Replace the code word.","goal":"` + recordedGoal + `","corrects":[` + corrects + `]` + extra + `,"reason":"the audit found it"}`
}

func sendCorrection(t *testing.T, session *Session) Reply {
	t.Helper()
	reply, err := session.Send(context.Background(), "audit the closed work")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v, want one", reply.Actions)
	}
	return reply
}

// A correction is admitted at priority 0, and its notes name each closed item
// it corrects and the pass that admitted it, which is what the duplicate check
// and the bound on a pass's corrections read.
func TestACorrectionIsAdmittedAtPriorityZeroNamingTheClosedItems(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{all: []beads.WorkItem{{ID: closedItem, Title: "The status line names a held line", Status: "closed"}}}
	session := correctionSession(t, tracker, correctionCreate(`"`+closedItem+`"`, ""))
	session.ForPass("product-manager-sweep#2")
	reply := sendCorrection(t, session)
	if !reply.Actions[0].Applied || len(tracker.created) != 1 {
		t.Fatalf("action = %#v, created = %d, want the correction admitted", reply.Actions[0], len(tracker.created))
	}
	created := tracker.created[0]
	if created.Priority == nil || *created.Priority != 0 {
		t.Errorf("priority = %v, want 0", created.Priority)
	}
	for _, want := range []string{"Corrects closed item yoyodyne-ifd.10: The status line names a held line",
		"Admitted to correct a broken standing goal on the pass product-manager-sweep#2."} {
		if !strings.Contains(created.Notes, want) {
			t.Errorf("notes = %q, want %q", created.Notes, want)
		}
	}
	if !strings.Contains(reply.Actions[0].Summary, "at priority 0") || !strings.Contains(reply.Actions[0].Summary, "correcting the closed work yoyodyne-ifd.10") {
		t.Errorf("summary = %q, want the priority and the corrected item said", reply.Actions[0].Summary)
	}
}

// A correction naming a closed item an open correction already names is the
// same violation: nothing is created, and the role is told to widen the open
// one, so one violation across several closed items or passes is one item.
func TestASecondCorrectionOfTheSameClosedItemIsRefusedAndNamesTheOneToWiden(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{all: []beads.WorkItem{
		{ID: closedItem, Title: "The status line names a held line", Status: "closed"},
		{ID: "yoyodyne-ifd.11", Title: "The dashboard names a held line", Status: "closed"},
		{ID: "yoyodyne-ifd.20", Title: "Say the held line in words", Status: "open",
			Notes: "Admitted.\n\nCorrects closed item yoyodyne-ifd.10: The status line names a held line"},
	}}
	session := correctionSession(t, tracker, correctionCreate(`"yoyodyne-ifd.11","`+closedItem+`"`, ""))
	reply := sendCorrection(t, session)
	if reply.Actions[0].Applied || len(tracker.created) != 0 {
		t.Fatalf("action = %#v, want the second correction refused", reply.Actions[0])
	}
	failure := reply.Actions[0].Failure
	for _, want := range []string{"yoyodyne-ifd.20", "already corrects yoyodyne-ifd.10", "widen it", `"corrects"`} {
		if !strings.Contains(failure, want) {
			t.Errorf("failure = %q, want %q", failure, want)
		}
	}
}

// One pass admits at most three corrections; the fourth is refused and named
// as deferred, and a pass's earlier corrections are what is counted.
func TestAPassAdmitsAtMostThreeCorrections(t *testing.T) {
	t.Parallel()

	all := []beads.WorkItem{{ID: closedItem, Title: "The status line names a held line", Status: "closed"}}
	for _, id := range []string{"yoyodyne-ifd.31", "yoyodyne-ifd.32", "yoyodyne-ifd.33"} {
		all = append(all, beads.WorkItem{ID: id, Title: "An earlier correction " + id, Status: "open",
			Notes: "Admitted to correct a broken standing goal on the pass product-manager-sweep#4."})
	}
	// A correction from another pass does not count against this one.
	all = append(all, beads.WorkItem{ID: "yoyodyne-ifd.34", Title: "Another pass's", Status: "open",
		Notes: "Admitted to correct a broken standing goal on the pass product-manager-sweep#3."})
	tracker := &fakeTracker{all: all}
	session := correctionSession(t, tracker, correctionCreate(`"`+closedItem+`"`, ""))
	session.ForPass("product-manager-sweep#4")
	reply := sendCorrection(t, session)
	if reply.Actions[0].Applied || len(tracker.created) != 0 {
		t.Fatalf("action = %#v, want the fourth correction refused", reply.Actions[0])
	}
	if !strings.Contains(reply.Actions[0].Failure, "already admitted 3 corrections for broken standing goals") || !strings.Contains(reply.Actions[0].Failure, "deferred") {
		t.Errorf("failure = %q, want the bound and the deferral named", reply.Actions[0].Failure)
	}
}

// A correction corrects closed work at priority 0: one naming open or unknown
// work is refused before anything is created, and one asking for another
// priority, or naming an item twice, is refused before it is carried out.
func TestACorrectionOfOpenWorkOrAtAnotherPriorityIsRefused(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		named string
		want  string
	}{
		"open work":    {`"yoyodyne-ifd.12"`, "is open rather than closed"},
		"unknown work": {`"yoyodyne-ifd.99"`, "holds no item yoyodyne-ifd.99"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tracker := &fakeTracker{all: []beads.WorkItem{
				{ID: closedItem, Title: "Closed", Status: "closed"},
				{ID: "yoyodyne-ifd.12", Title: "Open", Status: "open"},
			}}
			reply := sendCorrection(t, correctionSession(t, tracker, correctionCreate(test.named, "")))
			if reply.Actions[0].Applied || len(tracker.created) != 0 || !strings.Contains(reply.Actions[0].Failure, test.want) {
				t.Errorf("action = %#v, want it refused with %q", reply.Actions[0], test.want)
			}
		})
	}
	two := 2
	for name, test := range map[string]struct {
		action TrackerAction
		want   string
	}{
		"other priority": {TrackerAction{Action: actionCreate, Corrects: []string{closedItem}, Priority: &two}, "admitted at priority 0"},
		"named twice":    {TrackerAction{Action: actionCreate, Corrects: []string{closedItem, closedItem}}, "names yoyodyne-ifd.10 twice"},
		"not an item":    {TrackerAction{Action: actionUpdate, Corrects: []string{"not an id!"}}, "corrects[0]"},
	} {
		problems := test.action.correctionProblems()
		said := ""
		for _, problem := range problems {
			said += problem.Error() + "; "
		}
		if !strings.Contains(said, test.want) {
			t.Errorf("%s: problems = %q, want %q", name, said, test.want)
		}
	}
}

// An update naming further closed items widens a correction: a line for each is
// appended, and one it already corrects is refused rather than written twice.
func TestAnUpdateWidensACorrectionToFurtherClosedItems(t *testing.T) {
	t.Parallel()

	items := map[string]beads.WorkItem{
		"yoyodyne-ifd.20": {ID: "yoyodyne-ifd.20", Title: "Say the held line in words", Status: "open",
			Notes: "Corrects closed item yoyodyne-ifd.10: The status line names a held line"},
		"yoyodyne-ifd.11": {ID: "yoyodyne-ifd.11", Title: "The dashboard names a held line", Status: "closed"},
		closedItem:        {ID: closedItem, Title: "The status line names a held line", Status: "closed"},
	}
	tracker := &fakeTracker{items: items}
	reply := sendCorrection(t, correctionSession(t, tracker,
		`{"action":"update","id":"yoyodyne-ifd.20","corrects":["yoyodyne-ifd.11"],"reason":"the dashboard breaks the goal the same way"}`))
	if !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("action = %#v, want the correction widened", reply.Actions[0])
	}
	if notes := tracker.updates[0].change.AppendNotes; !strings.Contains(notes, "Corrects closed item yoyodyne-ifd.11: The dashboard names a held line") {
		t.Errorf("appended = %q, want the further closed item named", notes)
	}

	again := &fakeTracker{items: items}
	reply = sendCorrection(t, correctionSession(t, again,
		`{"action":"update","id":"yoyodyne-ifd.20","corrects":["yoyodyne-ifd.10"],"reason":"again"}`))
	if reply.Actions[0].Applied || len(again.updates) != 0 || !strings.Contains(reply.Actions[0].Failure, "already corrects yoyodyne-ifd.10") {
		t.Errorf("action = %#v, want a repeated item refused", reply.Actions[0])
	}
}
