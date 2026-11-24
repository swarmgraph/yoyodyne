package orchestrator

// Publishing a document, which asks the one decision on what may start under the
// named epics (internal/maystart) for a new run only.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// epicDocuments is a pipeline that publishes documents while epic mode is on,
// with one member, and the record of refused documents it keeps.
func epicDocuments(t *testing.T) (Pipeline, *runstate.Store, *epicscope.Operator, func() []maystart.RefusedDocument) {
	t.Helper()
	_, root, _, pipeline, store := documentPipeline(t)
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Start(epicscope.Naming{Epic: namedEpic, From: "terminal", Members: []string{blockedMember}}, baseTime); err != nil {
		t.Fatal(err)
	}
	pipeline.MayStart = func(start maystart.Start) maystart.Decision {
		return maystart.Ask(root, "yoyodyne", start, baseTime)
	}
	pipeline.EndRefusedDocument = func(id string) {
		if err := maystart.DocumentEnded(root, "yoyodyne", id); err != nil {
			t.Error(err)
		}
	}
	return pipeline, store, operator, func() []maystart.RefusedDocument {
		t.Helper()
		record, err := maystart.ReadRefusals(root, "yoyodyne")
		if err != nil || len(record.Items) != 0 {
			t.Fatalf("refusals = %+v, %v; want no entry for an item", record, err)
		}
		return record.Documents
	}
}

func epicDesign(t *testing.T, pipeline Pipeline, id, write, reason string, answers ...string) runstate.DocumentPublication {
	t.Helper()
	candidate, automatic, err := artifact.StoreFor(pipeline.Repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: id, Kind: artifact.KindDesign, Title: "Design " + id, Directory: "docs/designs", Body: "# Design\n\nThe owner's words.", Reason: reason}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = %v, %v", automatic, err)
	}
	return runstate.DocumentPublication{ConversationID: "chat-architect", WriteID: write, Turn: 1, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: candidate, Answers: answers}
}

func TestADocumentAnsweringAMemberOrTheOpenEpicIsPublished(t *testing.T) {
	t.Parallel()
	for name, design := range map[string]struct {
		reason  string
		answers []string
	}{
		"its revision names a member": {blockedMember + ": ruled", nil},
		"its revision names the epic": {namedEpic + ": ruled", nil},
		"it answers a member beside others, though its revision names one outside": {"yoyodyne-other: ruled", []string{"yoyodyne-out", blockedMember}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pipeline, _, _, refused := epicDocuments(t)
			delivery, err := pipeline.PublishDocument(context.Background(), epicDesign(t, pipeline, "a-design", "document-1.1", design.reason, design.answers...))
			if err != nil || !delivery.Landed || len(refused()) != 0 {
				t.Fatalf("PublishDocument() = %+v, %v, refused %+v; want it landed", delivery, err, refused())
			}
		})
	}
}

func TestAnyOtherDocumentStaysConfirmedAndWaiting(t *testing.T) {
	t.Parallel()
	pipeline, store, operator, refused := epicDocuments(t)
	ctx := context.Background()
	outside := epicDesign(t, pipeline, "a-design", "document-1.1", "yoyodyne-other: ruled", "yoyodyne-out")
	member := epicDesign(t, pipeline, "b-design", "document-1.2", blockedMember+": ruled")
	outcome, err := pipeline.PublishWaitingDocument(ctx, outside)
	var declined *OutsideNamedEpicsError
	if !errors.As(err, &declined) || outcome.RunID != "" {
		t.Fatalf("PublishWaitingDocument() = %+v, %v; want it declined with no run", outcome, err)
	}
	entry := Started{}
	entry.record(completed{outcome: outcome, err: err, document: true})
	if entry.Failure != "" || !entry.heldByNamedEpics || !strings.Contains(entry.Declined, "it answers a member of an open epic or the open epic itself, or the operator runs `yoyo epic off`") {
		t.Fatalf("schedule entry = %+v", entry)
	}
	got := refused()
	if len(got) != 1 || got[0].DocumentID != "a-design" || got[0].WriteID != "document-1.1" || !slices.Equal(got[0].Answers, []string{"yoyodyne-out", "yoyodyne-other"}) {
		t.Fatalf("refused = %+v, want its one entry", got)
	}
	recorded, err := store.Recorded()
	if err != nil || len(recorded) != 0 {
		t.Fatalf("runs = %+v, %v; want no attempt spent", recorded, err)
	}
	// The next waiting document is looked at, with one landing free.
	if _, waiting, err := store.WaitForSlot(ctx, member, 0, baseTime.Add(time.Hour)); err != nil || !waiting {
		t.Fatal(err)
	}
	pull := Pull{Documents: waitingDocumentsOver{store: store}, DocumentLandings: 1, MayStart: pipeline.MayStart}
	next := func() (names []string, passed []string) {
		starting, err := Scheduler{}.nextDocuments(pull, map[string]runstate.State{}, map[string]int{}, map[string]refusedDocument{}, 0, func(_, reason string) { passed = append(passed, reason) })
		if err != nil {
			t.Fatal(err)
		}
		for _, wait := range starting {
			names = append(names, wait.Document.WriteID)
		}
		return names, passed
	}
	if names, passed := next(); !slices.Equal(names, []string{"document-1.2"}) || len(passed) != 1 || !strings.Contains(passed[0], "answers no open epic") {
		t.Fatalf("starting %v, passed over %q; want the member's started past the other", names, passed)
	}
	// Once the item it answers joins, the same waiting document starts.
	if err := store.ClearDocumentWait(member.RunID()); err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Add(namedEpic, "yoyodyne-out", baseTime); err != nil {
		t.Fatal(err)
	}
	if names, _ := next(); !slices.Equal(names, []string{"document-1.1"}) || len(refused()) != 0 {
		t.Fatalf("starting %v, refused %+v; want it started and its entry gone", names, refused())
	}
	if outcome, err := pipeline.PublishWaitingDocument(ctx, outside); err != nil || outcome.RunID != outside.RunID() {
		t.Fatalf("PublishWaitingDocument() = %+v, %v; want its first attempt run", outcome, err)
	}
}

func TestARecordedDocumentRunCarriesOnUnderEpicMode(t *testing.T) {
	t.Parallel()
	pipeline, store, _, refused := epicDocuments(t)
	design := epicDesign(t, pipeline, "a-design", "document-1.1", "yoyodyne-other: ruled")
	now := time.Now().UTC()
	lease, err := store.ReserveDocumentLanding(context.Background(), runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: design.RunID(), ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: design.RunID(), WorkItemTitle: "Publishing it", Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &design, TargetBranch: "main", StartedAt: now, UpdatedAt: now}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	asked := 0
	pipeline.MayStart = func(maystart.Start) maystart.Decision { asked++; return maystart.Decision{} }
	delivery, err := pipeline.PublishDocument(context.Background(), design)
	if err != nil || !delivery.Landed || asked != 0 || len(refused()) != 0 {
		t.Fatalf("PublishDocument() = %+v, %v, asked %d; want the recorded run landed and nothing asked", delivery, err, asked)
	}
}

// A document handed back before any run ends its entry. One withdrawn to make
// room for a newer revision keeps it, and the newer revision updates it.
func TestARefusedDocumentsEntryFollowsTheDocument(t *testing.T) {
	t.Parallel()
	pipeline, store, _, refused := epicDocuments(t)
	ctx := context.Background()
	first := epicDesign(t, pipeline, "a-design", "document-1.1", "written by the architect")
	newer := epicDesign(t, pipeline, "a-design", "document-2.1", "written again")
	offer := func(document runstate.DocumentPublication) runstate.DocumentDelivery {
		t.Helper()
		delivery, err := pipeline.PublishDocument(ctx, document)
		if err != nil {
			t.Fatal(err)
		}
		return delivery
	}
	if told := offer(first).Retrying; !strings.Contains(told, "is confirmed and waiting") || !strings.Contains(told, "written again naming a member") || !strings.Contains(told, "Nothing is counted against it") {
		t.Fatalf("its owner is told %q", told)
	}
	if err := pipeline.WithdrawDocument(first); err != nil {
		t.Fatal(err)
	}
	offer(newer)
	if got := refused(); len(got) != 1 || got[0].WriteID != "document-2.1" || !got[0].Since.Equal(baseTime) {
		t.Fatalf("refused = %+v, want the one entry about the newer write", got)
	}
	waits, err := store.DocumentWaits()
	if err != nil || len(waits) != 1 || waits[0].Document.WriteID != "document-2.1" {
		t.Fatalf("waits = %+v, %v; want only the newer write", waits, err)
	}
	// It is no longer one that may be published, so it goes back.
	pipeline.Config.Approvals.Designs = domain.ApprovalHuman
	if _, err := pipeline.PublishDocument(ctx, newer); !errors.Is(err, runstate.ErrDocumentNotPublishable) || len(refused()) != 0 {
		t.Fatalf("PublishDocument() error = %v, refused = %+v; want it handed back and its entry gone", err, refused())
	}
}
