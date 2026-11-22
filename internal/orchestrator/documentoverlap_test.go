package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type unpublishedList struct {
	writes []runstate.PendingWrite
	err    error
}

func (u unpublishedList) Unpublished() ([]runstate.PendingWrite, error) { return u.writes, u.err }

type openWork struct{ items []beads.WorkItem }

func (o openWork) List(context.Context, string) ([]beads.WorkItem, error) { return o.items, nil }

type reportPile struct{ reports []report.Report }

func (r reportPile) List() ([]report.Report, error)        { return r.reports, nil }
func (r reportPile) Handlings() ([]report.Handling, error) { return nil, nil }

var overlapWritten = recurringNow.Add(-2 * time.Hour)

func overlapDocument() runstate.PendingWrite {
	return runstate.PendingWrite{ID: "document-9.1", Artifact: "design-x", Action: "create", Answers: []string{"example-5"}, WrittenAt: overlapWritten}
}

func overlapItem(updated time.Time, notes string) beads.WorkItem {
	return beads.WorkItem{ID: "example-5", Title: "Build the thing", Status: "open", UpdatedAt: updated, Notes: beads.PlainNotes(notes), FramedNotes: notes}
}

func firedMessage(t *testing.T, tasks map[string]config.RecurringTask, trigger Trigger) string {
	t.Helper()
	role := &wokenRole{answers: []scriptedTurn{{result: complete("done")}}}
	trigger.Tasks, trigger.Claims, trigger.Reports, trigger.Roles, trigger.Clock = tasks, sweepStore(t), nil, role, recurringClock{}
	trigger.Reports = trigger.Claims.(*runstate.SweepStore)
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(role.messages) == 0 {
		t.Fatal("the role was handed nothing")
	}
	return role.messages[0]
}

// A document written while an open item it names carries a decision is listed in
// the architect's pass and in the Lead Product Manager's, and a role that
// neither owns the document nor the item is handed nothing.
func TestAnUnpublishedDocumentNamingAnOpenItemIsListedInBothPasses(t *testing.T) {
	t.Parallel()

	decided := overlapItem(overlapWritten.Add(-time.Hour), beads.FrameNote("Decision: keep the old route."))
	for name, tasks := range map[string]map[string]config.RecurringTask{"architect": architectTask(), "lead product manager": productManagerSweep()} {
		message := firedMessage(t, tasks, Trigger{Unpublished: unpublishedList{writes: []runstate.PendingWrite{overlapDocument()}}, OpenWork: openWork{items: []beads.WorkItem{decided}}})
		for _, want := range []string{"## Unpublished documents that overlap open work", "Document design-x, revision document-9.1", "item Build the thing (example-5)",
			"the document names this item", "before the document was written", "Decision: keep the old route."} {
			if !strings.Contains(message, want) {
				t.Errorf("%s's message does not carry %q:\n%s", name, want, message)
			}
		}
	}
	other := firedMessage(t, hourlyTask("sweep"), Trigger{Unpublished: unpublishedList{writes: []runstate.PendingWrite{overlapDocument()}}, OpenWork: openWork{items: []beads.WorkItem{decided}}})
	if strings.Contains(other, "overlap open work") {
		t.Errorf("the development manager was handed the list:\n%s", other)
	}
	if strings.Contains(firedMessage(t, architectTask(), Trigger{}), "overlap open work") {
		t.Error("a trigger wired without the sources must list nothing")
	}
}

// A later entry on the item lists the overlap again, saying it came after the
// document, and moves what the check before a scheduled pass compares.
func TestALaterEntryOnTheItemListsTheOverlapAgain(t *testing.T) {
	t.Parallel()

	before := FindDocumentOverlaps([]runstate.PendingWrite{overlapDocument()}, []beads.WorkItem{overlapItem(overlapWritten.Add(-time.Hour), "")}, nil)
	later := overlapItem(overlapWritten.Add(time.Hour), beads.FrameNote("A new decision, after the design."))
	after := FindDocumentOverlaps([]runstate.PendingWrite{overlapDocument()}, []beads.WorkItem{later}, nil)
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("overlaps = %d before and %d after, want one each", len(before), len(after))
	}
	if said := newestEntrySaid(after[0]); !strings.Contains(said, "after the document was written") || !strings.Contains(said, "A new decision, after the design.") {
		t.Errorf("newest entry = %q", said)
	}
	if documentOverlapDigest(before) == documentOverlapDigest(after) {
		t.Error("a newer entry on the item did not move the digest, so a scheduled pass would be skipped")
	}
}

// An agreement note answers the overlap until either side changes, and a report
// of the conflict naming the revision answers it too.
func TestAnAgreementNoteOrAConflictReportAnswersTheOverlap(t *testing.T) {
	t.Parallel()

	doc := overlapDocument()
	recorded := overlapWritten.Add(time.Hour)
	agree := beads.FrameNote(AgreementNote("design-x", "document-9.1", recorded.Add(-time.Minute), recorded))
	find := func(write runstate.PendingWrite, item beads.WorkItem, reports ...report.Report) int {
		return len(FindDocumentOverlaps([]runstate.PendingWrite{write}, []beads.WorkItem{item}, reports))
	}
	if got := find(doc, overlapItem(recorded.Add(time.Second), agree)); got != 0 {
		t.Fatalf("an agreement note left %d overlap(s) listed", got)
	}
	if got := find(doc, overlapItem(recorded.Add(time.Hour), agree)); got != 1 {
		t.Errorf("a change to the item after the agreement left %d listed, want 1", got)
	}
	if got := find(doc, overlapItem(recorded.Add(time.Second), agree+beads.FrameNote("Another decision."))); got != 1 {
		t.Errorf("a note after the agreement left %d listed, want 1", got)
	}
	revised := doc
	revised.ID = "document-9.2"
	if got := find(revised, overlapItem(recorded.Add(time.Second), agree)); got != 1 {
		t.Errorf("a newer revision left %d listed, want 1", got)
	}
	// An agreement note for another document, or one not shaped as written, answers nothing.
	other := beads.FrameNote(AgreementNote("design-y", "document-9.1", recorded.Add(-time.Minute), recorded))
	if got := find(doc, overlapItem(recorded.Add(time.Second), other)); got != 1 {
		t.Errorf("another document's agreement left %d listed, want 1", got)
	}
	forged := beads.FrameNote("Quoting: " + AgreementNote("design-x", "document-9.1", recorded, recorded))
	if got := find(doc, overlapItem(recorded.Add(time.Second), forged)); got != 1 {
		t.Errorf("a quoted agreement left %d listed, want 1", got)
	}
	conflict := report.Report{Message: "Document revision document-9.1 refuses what example-5 requires."}
	if got := find(doc, overlapItem(recorded.Add(time.Hour), ""), conflict); got != 0 {
		t.Errorf("a conflict report left %d listed, want 0", got)
	}
	// A report naming a longer identifier that starts the same way answers nothing.
	longer := report.Report{Message: "Revision document-9.10 refuses what example-50 requires."}
	if got := find(doc, overlapItem(recorded.Add(time.Hour), ""), longer); got != 1 {
		t.Errorf("a report of document-9.10 and example-50 left %d listed, want 1", got)
	}
	if got := find(revised, overlapItem(recorded.Add(time.Hour), ""), conflict); got != 1 {
		t.Errorf("a report of the older revision left %d listed, want 1", got)
	}
}

// A document waiting for a person to copy it into the companion intent
// repository is listed as any other unpublished document is, whether confirmed,
// held, or unconfirmed; and a closed item overlaps nothing.
func TestEveryWaitingKindOfDocumentIsListedAndClosedItemsAreNot(t *testing.T) {
	t.Parallel()

	waiting := overlapDocument()
	confirmed := overlapDocument()
	confirmed.ID, confirmed.Publication = "document-9.2", &runstate.DocumentPublication{}
	held := overlapDocument()
	held.ID = "document-9.3"
	held.Hold = &artifact.SendingHold{UntilClosed: "example-5", Reason: "waits for the fix"}
	item := overlapItem(overlapWritten.Add(-time.Hour), "")
	if got := FindDocumentOverlaps([]runstate.PendingWrite{waiting, confirmed, held}, []beads.WorkItem{item}, nil); len(got) != 3 {
		t.Errorf("overlaps = %d, want one for each waiting document", len(got))
	}
	item.Status = "closed"
	if got := FindDocumentOverlaps([]runstate.PendingWrite{waiting}, []beads.WorkItem{item}, nil); len(got) != 0 {
		t.Errorf("a closed item overlaps %d document(s)", len(got))
	}
}

// A document revising one an item cites, by its file or its identifier, overlaps
// the item; a one-word identifier read in prose does not.
func TestARevisionOverlapsAnItemCitingTheDocumentItRevises(t *testing.T) {
	t.Parallel()

	revision := runstate.PendingWrite{ID: "document-4.1", Artifact: "v1-goals", Action: "revise", WrittenAt: overlapWritten,
		Revises: []runstate.DocumentRef{{ID: "v1-goals", Path: "docs/product/goals/v1-goals.md"}}}
	byPath := beads.WorkItem{ID: "example-1", Status: "open", Description: "Follow docs/product/goals/v1-goals.md."}
	byID := beads.WorkItem{ID: "example-2", Status: "open", Notes: "Per `v1-goals`, do it."}
	neither := beads.WorkItem{ID: "example-3", Status: "open", Description: "Unrelated, though it says v1-goalsx."}
	got := FindDocumentOverlaps([]runstate.PendingWrite{revision}, []beads.WorkItem{byPath, byID, neither}, nil)
	if len(got) != 2 || got[0].Item.ID != "example-1" || got[1].Item.ID != "example-2" {
		t.Fatalf("overlaps = %+v, want the two items citing the document", got)
	}
	brief := runstate.PendingWrite{ID: "document-5.1", Artifact: "brief", Action: "revise", Revises: []runstate.DocumentRef{{ID: "brief"}}}
	prose := beads.WorkItem{ID: "example-4", Status: "open", Description: "Write a brief note."}
	if got := FindDocumentOverlaps([]runstate.PendingWrite{brief}, []beads.WorkItem{prose}, nil); len(got) != 0 {
		t.Errorf("a one-word identifier in prose overlapped: %+v", got)
	}
}

// What cannot be read is said in the pass's record and never read as no overlap.
func TestAnUnreadableUnpublishedListingIsSaid(t *testing.T) {
	t.Parallel()

	_, problem := Trigger{Unpublished: unpublishedList{err: errors.New("the store is gone")}, OpenWork: openWork{}}.documentOverlaps(context.Background())
	if !strings.Contains(problem, "the store is gone") {
		t.Errorf("problem = %q", problem)
	}
}

// The check before a scheduled pass reads the overlaps for the two roles only,
// and only where the trigger reads them.
func TestOnlyTheArchitectAndTheLeadProductManagerReadTheOverlaps(t *testing.T) {
	t.Parallel()

	wired := Trigger{Unpublished: unpublishedList{}, OpenWork: openWork{}}
	for role, want := range map[domain.AgentRole]bool{domain.RoleArchitect: true, domain.RoleProductManager: true, domain.RoleDevelopmentManager: false, domain.RoleProgramManager: false} {
		if got := slicesHas(wired.passKinds(PassScope{Role: role}), ChangeOverlaps); got != want {
			t.Errorf("%s reads the overlaps = %v, want %v", role, got, want)
		}
	}
	if slicesHas(Trigger{}.passKinds(PassScope{Role: domain.RoleArchitect}), ChangeOverlaps) {
		t.Error("an unwired trigger reads the overlaps")
	}
}

func slicesHas(kinds []PassChangeKind, kind PassChangeKind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}
