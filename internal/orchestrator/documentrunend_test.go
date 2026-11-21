package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// publicationItems is a tracker that files work items as the harness does for
// a publication whose revision names none, and keeps what is written to them.
// Everything about any other item goes to the tracker it wraps.
type publicationItems struct {
	*orchestratortest.Tracker
	mu        sync.Mutex
	filed     map[string]beads.NewWorkItem
	notes     map[string][]string
	completed map[string]string
}

func newPublicationItems(wrapped *orchestratortest.Tracker) *publicationItems {
	return &publicationItems{Tracker: wrapped, filed: map[string]beads.NewWorkItem{}, notes: map[string][]string{}, completed: map[string]string{}}
}

func (p *publicationItems) Create(_ context.Context, item beads.NewWorkItem) (beads.WorkItem, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := fmt.Sprintf("yoyodyne-pub%d", len(p.filed)+1)
	p.filed[id] = item
	return beads.WorkItem{ID: id, Title: item.Title, Status: "open"}, nil
}

func (p *publicationItems) List(context.Context, string) ([]beads.WorkItem, error) { return nil, nil }

func (p *publicationItems) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	p.mu.Lock()
	item, ok := p.filed[id]
	p.mu.Unlock()
	if ok {
		return beads.WorkItem{ID: id, Title: item.Title, Status: "open"}, nil
	}
	return p.Tracker.Show(ctx, id)
}

func (p *publicationItems) RecordOutcome(ctx context.Context, id, note string) (beads.WorkItem, error) {
	p.mu.Lock()
	_, ok := p.filed[id]
	if ok {
		p.notes[id] = append(p.notes[id], note)
	}
	p.mu.Unlock()
	if ok {
		return beads.WorkItem{ID: id, Status: "open"}, nil
	}
	return p.Tracker.RecordOutcome(ctx, id, note)
}

func (p *publicationItems) Complete(ctx context.Context, id, reason string) (beads.WorkItem, error) {
	p.mu.Lock()
	_, ok := p.filed[id]
	if ok {
		p.completed[id] = reason
	}
	p.mu.Unlock()
	if ok {
		return beads.WorkItem{ID: id, Status: "closed"}, nil
	}
	return p.Tracker.Complete(ctx, id, reason)
}

func (p *publicationItems) notesOf(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.notes[id], "\n")
}

// publishedThroughAFiledItem lands one design whose revision names no work
// item, through a pipeline that files items, and returns the run it landed in.
func publishedThroughAFiledItem(t *testing.T) (*publicationItems, Pipeline, *runstate.Store, runstate.State) {
	t.Helper()
	_, tracker, provider, pipeline, runs := automaticFixture(t)
	items := newPublicationItems(tracker)
	pipeline.Tracker, pipeline.Filer = items, items
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("recovery-design", "# Recovery\n\nThe owner's exact words.")}, nil
		}
		return original(request)
	}
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := runs.Recorded()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("runs = %+v, %v", recorded, err)
	}
	return items, pipeline, runs, recorded[0]
}

// A publication whose revision names no work item is given one: the harness
// files it as the owning role's conversation's work, so no developer run is
// started for it, records it on the run, writes the run's start and end on it,
// and closes it once the document lands.
func TestAPublicationWhoseRevisionNamesNoItemIsRecordedAgainstAnItemFiledForIt(t *testing.T) {
	t.Parallel()

	items, _, _, state := publishedThroughAFiledItem(t)
	if state.Status != runstate.StatusSucceeded || state.Integration == nil {
		t.Fatalf("run = %+v, want the document landed", state)
	}
	if len(items.filed) != 1 {
		t.Fatalf("filed = %+v, want one item for the publication", items.filed)
	}
	id := state.DocumentItem
	filed, ok := items.filed[id]
	if !ok {
		t.Fatalf("the run records item %q, which is not the one filed: %+v", id, items.filed)
	}
	if filed.Executor != domain.ConversationWith(domain.RoleArchitect) || filed.Origin.Asker != domain.AskerHarness {
		t.Fatalf("filed item = %+v, want it the architect's conversation's work, filed by the harness", filed)
	}
	if !strings.Contains(filed.Title, "recovery-design") || !strings.Contains(filed.Notes, state.Document.WriteID) {
		t.Fatalf("filed item does not name the document and its handoff: %+v", filed)
	}
	notes := items.notesOf(id)
	if !strings.Contains(notes, "Yoyodyne started run "+state.RunID) || !strings.Contains(notes, "published document") {
		t.Fatalf("notes on the filed item = %q, want the run's start and end", notes)
	}
	if _, closed := items.completed[id]; !closed {
		t.Fatalf("the filed item was not closed when its document landed: %+v", items.completed)
	}
	if got := state.DocumentItemSays(); got != id {
		t.Fatalf("DocumentItemSays = %q, want %q", got, id)
	}
}

// A publication whose document's revision names its item is recorded against
// that item, and nothing is filed or closed for it.
func TestAPublicationWhoseRevisionNamesItsItemFilesNothing(t *testing.T) {
	t.Parallel()

	_, tracker, _, _, _ := automaticFixture(t)
	items := newPublicationItems(tracker)
	document := runstate.DocumentPublication{Owner: domain.RoleArchitect, WriteID: "document-1.1", ConversationID: "chat-architect"}
	document.Candidate.Artifact.Revisions = append(document.Candidate.Artifact.Revisions, artifact.Revision{Action: artifact.ActionAmended, By: domain.RoleArchitect, Reason: "yoyodyne-fp3.8: the queue says so"})
	filed := ""
	record := documentItemRecorder{tracker: items, filer: items, document: document, filed: &filed}
	if got := record.file(context.Background(), "run-1"); got != "" || len(items.filed) != 0 {
		t.Fatalf("file = %q, filed %+v; want nothing filed", got, items.filed)
	}
	if got := record.item(); got != "yoyodyne-fp3.8" {
		t.Fatalf("item = %q, want the item the revision names", got)
	}
}

// A publication whose document has been admitted to the merge queue is not
// lost and not stopped: its work item, its owning conversation and its record
// each say it waits in the merge queue, naming the entry and the pull request,
// and its document is not handed back.
func TestAQueuedPublicationSaysItWaitsInTheMergeQueue(t *testing.T) {
	t.Parallel()

	items, pipeline, runs, landed := publishedThroughAFiledItem(t)
	queued := landed
	admitted := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	queued.Integration = nil
	queued.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-" + strings.Repeat("a", 32), Order: 170, Repository: "origin", TargetBranch: "main", Mode: runstate.MergeQueueHarness, ApprovedHead: strings.Repeat("b", 40), AdmittedAt: admitted}
	queued.PullRequest = &runstate.PullRequest{Remote: "origin", Branch: queued.Branch, HeadCommit: strings.Repeat("b", 40), Number: 1899, URL: "https://example.invalid/pull/1899"}
	queued.BranchRemoved, queued.WorktreeRemoved = false, false
	queued.Phase = runstate.PhaseCleaningUp
	if err := runs.Save(queued); err != nil {
		t.Fatal(err)
	}

	says := queued.MergeQueueSays()
	for _, want := range []string{"merge queue", "entry 170", queued.MergeQueue.EntryID, "pull request #1899"} {
		if !strings.Contains(says, want) {
			t.Fatalf("MergeQueueSays = %q, want it to name %q", says, want)
		}
	}

	delivery := documentDelivery(queued)
	if delivery.Settled || delivery.Landed || delivery.Queued != says || !strings.Contains(delivery.Retrying, "pull request #1899") || !strings.Contains(delivery.Retrying, "not handed back") {
		t.Fatalf("delivery = %+v, want an unsettled delivery saying where it waits", delivery)
	}
	standing, err := pipeline.DocumentStanding(*queued.Document)
	if err != nil || !standing.Running() || standing.HandedBack() || standing.Delivery.Queued == "" {
		t.Fatalf("standing = %+v, %v; want it still publishing, and queued", standing, err)
	}

	record := recordedDocumentItem(items, queued)
	if _, _, err := pipeline.settledDocumentAttempt(context.Background(), queued.RunID, true, record, nil); err != nil {
		t.Fatal(err)
	}
	notes := items.notesOf(queued.DocumentItem)
	last := notes[strings.LastIndex(notes, "\n")+1:]
	if !strings.Contains(last, "did not lose document") || !strings.Contains(last, "pull request #1899") || !strings.Contains(last, queued.MergeQueue.EntryID) {
		t.Fatalf("item note = %q, want it to say the document waits in the merge queue", last)
	}
	if strings.Contains(last, "without judging it") || strings.Contains(last, "recorded no reason") {
		t.Fatalf("item note = %q, reads as a stop", last)
	}

	history, err := runs.History(runstate.RunQuery{WorkItemID: queued.WorkItemID})
	if err != nil || len(history.Runs) != 1 {
		t.Fatalf("history = %+v, %v", history, err)
	}
	summary := history.Runs[0]
	if summary.Queued != says || summary.DocumentItem != queued.DocumentItem {
		t.Fatalf("summary = %+v, want the queue and the item said for yoyo status", summary)
	}
}

// A run whose pull request was last seen queued and that then failed holds
// nothing: it is settled and hands its document back, so a later revision is
// not kept waiting behind a run that has ended.
func TestAFailedPublicationWithALeftoverQueuedMergeIsSettled(t *testing.T) {
	t.Parallel()

	_, _, _, landed := publishedThroughAFiledItem(t)
	ended := landed
	ended.Integration = nil
	ended.Status = runstate.StatusFailed
	ended.Phase = runstate.PhaseCleaningUp
	ended.Failure = "the forge dropped the merge"
	ended.PullRequest = &runstate.PullRequest{Number: 1900, MergeQueued: true}
	if ended.MergeQueueSays() != "" {
		t.Fatalf("MergeQueueSays = %q for a failed run", ended.MergeQueueSays())
	}
	delivery := documentDelivery(ended)
	if !delivery.Settled || delivery.Landed || delivery.Passed || !strings.Contains(delivery.Detail, "the forge dropped the merge") {
		t.Fatalf("delivery = %+v, want it settled and handed back with its reason", delivery)
	}
}

// A publication run whose process died before the run ended is settled by the
// sweep, in plain words, as a stop that judged nothing: its document no longer
// holds later revisions, the next offer runs the same text again, and the item
// it is recorded against is told now.
func TestASweepSettlesAPublicationRunWhoseProcessDied(t *testing.T) {
	t.Parallel()

	items, pipeline, runs, landed := publishedThroughAFiledItem(t)
	orphan := landed
	orphan.Integration = nil
	orphan.CompletedAt = nil
	orphan.Status = runstate.StatusRunning
	orphan.Phase = runstate.PhaseReviewing
	orphan.BranchRemoved, orphan.WorktreeRemoved = false, false
	if err := runs.Save(orphan); err != nil {
		t.Fatal(err)
	}
	if !documentProcessGone(orphan) {
		t.Fatal("documentProcessGone = false for a document run marked as going with nothing landed or queued")
	}
	if standing, err := pipeline.DocumentStanding(*orphan.Document); err != nil || !standing.Running() {
		t.Fatalf("standing = %+v, %v; want it held as still publishing before the sweep", standing, err)
	}

	result, err := (Reconciler{Store: runs, Tracker: items}).settle(context.Background(), orphan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != ActionFailed || !strings.Contains(result.Detail, "stopped at the run's reviewing step") {
		t.Fatalf("result = %+v, want the run settled with its reason", result)
	}
	settled, err := runs.Load(orphan.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !orphan.PublishingCopy() || settled.PublishingCopy() {
		t.Fatalf("PublishingCopy = %v before and %v after the sweep, want the run held as publishing until the sweep ends it", orphan.PublishingCopy(), settled.PublishingCopy())
	}
	if !settled.Status.Terminal() || settled.DocumentJudged() || !retryable(settled) {
		t.Fatalf("settled = %+v, want it ended without judging the document, so it is tried again", settled)
	}
	standing, err := pipeline.DocumentStanding(*orphan.Document)
	if err != nil || standing.Running() || !standing.HandedBack() {
		t.Fatalf("standing = %+v, %v; want it no longer holding its document", standing, err)
	}
	attempt, previous, err := pipeline.documentAttempt(*orphan.Document)
	if err != nil || attempt != 1 || previous == nil || previous.RunID != orphan.RunID {
		t.Fatalf("documentAttempt = %d, %+v, %v; want the next attempt after the settled run", attempt, previous, err)
	}
	notes := items.notesOf(orphan.DocumentItem)
	if !strings.Contains(notes, "stopped publishing document") || !strings.Contains(notes, "runs the same text again") {
		t.Fatalf("item notes = %q, want the stop and what happens next", notes)
	}

	// A run at its promotion, queued, or with a merge in flight is never
	// settled this way: ending it could publish a second copy.
	for name, change := range map[string]func(*runstate.State){
		"integrating": func(s *runstate.State) { s.Phase = runstate.PhaseIntegrating },
		"queued":      func(s *runstate.State) { s.MergeQueue = &runstate.MergeQueueAdmissionRecord{EntryID: "mqe-x"} },
		"merging":     func(s *runstate.State) { s.PullRequest = &runstate.PullRequest{Number: 3, MergeQueued: true} },
		"ended":       func(s *runstate.State) { s.Status = runstate.StatusFailed },
	} {
		candidate := orphan
		change(&candidate)
		if documentProcessGone(candidate) {
			t.Fatalf("documentProcessGone = true for a run that is %s", name)
		}
	}
}
