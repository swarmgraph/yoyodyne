package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// harnessDocuments is the waiting documents a pull over the fake harness
// reads, and their publication: a document run holds its slot in the harness's
// in-flight runs while it goes, exactly as a real reservation would.
type harnessDocuments struct {
	harness   *scheduleHarness
	waits     []runstate.DocumentWait
	published []string
}

func (d *harnessDocuments) DocumentWaits() ([]runstate.DocumentWait, error) {
	d.harness.mu.Lock()
	defer d.harness.mu.Unlock()
	return append([]runstate.DocumentWait(nil), d.waits...), nil
}

func (d *harnessDocuments) Publish(_ context.Context, document runstate.DocumentPublication) (Outcome, error) {
	h, runID := d.harness, document.RunID()
	h.mu.Lock()
	h.order = append(h.order, runID)
	d.published = append(d.published, runID)
	h.inFlight[runID] = runstate.State{RunID: runID, WorkItemID: runID, Status: runstate.StatusRunning}
	d.waits = slices.DeleteFunc(d.waits, func(wait runstate.DocumentWait) bool { return wait.RunID() == runID })
	h.mu.Unlock()

	h.mu.Lock()
	delete(h.inFlight, runID)
	h.mu.Unlock()
	return Outcome{RunID: runID, WorkItemID: runID, Status: runstate.StatusSucceeded}, nil
}

func (d *harnessDocuments) publishedRuns() []string {
	d.harness.mu.Lock()
	defer d.harness.mu.Unlock()
	return append([]string(nil), d.published...)
}

func waitingDesign(writeID string) runstate.DocumentWait {
	return runstate.DocumentWait{
		SchemaVersion: runstate.DocumentWaitSchemaVersion,
		Since:         newScheduleHarness().now,
		Document: runstate.DocumentPublication{
			ConversationID: "chat-architect", WriteID: writeID, Turn: 4, Owner: domain.RoleArchitect,
			Candidate: artifact.ConfirmedDocument{Artifact: artifact.Artifact{ID: "recovery-design", Title: "Recovery design", Path: "docs/designs/recovery-design.md"}},
		},
	}
}

// A line whose one slot is held by development work frees it while a confirmed
// document and a ready development item both wait for it. The document takes
// the slot, with nobody sending its conversation a message, and the item is
// pulled only once the document's run has given the slot back. The run that was
// holding the slot is never stopped for it.
func TestAWaitingDocumentTakesTheFreedSlotAheadOfNewDevelopment(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}
	wait := waitingDesign("document-4.1")
	documents := &harnessDocuments{harness: harness, waits: []runstate.DocumentWait{wait}}
	// The first poll finds the slot held; the wait after it is the one the other
	// run ends in. The session stops at the wait after that.
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		if _, running := h.inFlight["yoyodyne-elsewhere"]; !running {
			h.mu.Unlock()
			t.Error("the run holding the slot was stopped for the waiting document")
			return false
		}
		delete(h.inFlight, "yoyodyne-elsewhere")
		h.mu.Unlock()
		return true
	}
	var deferredWhileFull bool
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Documents = documents
		return pull, err
	}

	schedule, err := Scheduler{Open: open, Watching: true, Sleep: func(ctx context.Context, interval time.Duration) bool {
		harness.mu.Lock()
		if len(documents.published) == 0 && len(harness.order) == 0 {
			deferredWhileFull = true
		}
		harness.mu.Unlock()
		return harness.sleep(ctx, interval)
	}}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !deferredWhileFull {
		t.Fatal("something was started while the one slot was held, want the first poll to wait")
	}
	if got, want := harness.pullOrder(), []string{wait.RunID(), "yoyodyne-one"}; !slices.Equal(got, want) {
		t.Fatalf("started %v, want the waiting document first and the ready item after it", got)
	}
	if len(schedule.Started) != 2 || schedule.Started[0].WorkItemID != wait.RunID() {
		t.Fatalf("schedule = %s, want the document's run first", schedule.Render())
	}
	reason := schedule.Started[0].Reason
	for _, says := range []string{"document-4.1", "Recovery design", "architect", "ahead of any new development run"} {
		if !strings.Contains(reason, says) {
			t.Fatalf("document run reason = %q, want it to say %q", reason, says)
		}
	}
	if !deferredSaying(schedule, wait.RunID(), "takes the next one that frees") {
		t.Fatalf("passed over = %#v, want the document named as waiting for the next slot while the line was full", schedule.Deferred)
	}
}

// A document whose landing run is already in flight — started by its
// conversation, or by a session before this one — is left to that run, and a
// pull never starts it a second time, however many slots are free.
func TestAWaitingDocumentWhoseRunIsAlreadyInFlightIsNotStartedAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 3
	wait := waitingDesign("document-7.1")
	harness.inFlight[wait.RunID()] = runstate.State{RunID: wait.RunID(), WorkItemID: wait.RunID(), Status: runstate.StatusRunning}
	documents := &harnessDocuments{harness: harness, waits: []runstate.DocumentWait{wait}}

	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Documents = documents
		return pull, err
	}}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if published := documents.publishedRuns(); len(published) != 0 || len(schedule.Started) != 0 {
		t.Fatalf("published %v and started %#v, want nothing started beside the run already landing it", published, schedule.Started)
	}
	if !deferredSaying(schedule, wait.RunID(), "already being landed by run "+wait.RunID()) {
		t.Fatalf("passed over = %#v, want the document named as already being landed", schedule.Deferred)
	}
}

// documentWaitFixture is an architect's conversation whose confirmed design
// found the one developer slot held by development work: the design is saved,
// confirmed, waiting in the run store, and nothing has run for it.
type documentWaitFixture struct {
	repository string
	root       string
	provider   *orchestratortest.Backend
	pipeline   Pipeline
	store      *runstate.Store
	options    chat.Options
	session    *chat.Session
	holder     string
}

func waitingForASlot(t *testing.T) *documentWaitFixture {
	t.Helper()
	repository, _, provider, pipeline, _ := automaticFixture(t)
	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Store = store
	pipeline.Config.Execution.MaxConcurrentDevelopers = 1
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: "Noted."}, nil
			}
			return backend.RunResult{Backend: domain.BackendClaudeCode, SessionID: "author-session", FinalText: submittedDesign("recovery-design", "# Recovery\n\nThe owner's exact words.")}, nil
		}
		return original(request)
	}
	holderID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	holder := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: holderID, ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: "yoyodyne-development", Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := store.Reserve(context.Background(), holder, 1)
	if err != nil {
		t.Fatalf("Reserve() the development run holding the slot: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatalf("a document waiting for a slot stopped the message: %v", err)
	}
	return &documentWaitFixture{repository: repository, root: root, provider: provider, pipeline: pipeline, store: store, options: options, session: session, holder: holderID}
}

// freeTheSlot ends the development run holding the only slot.
func (f *documentWaitFixture) freeTheSlot(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.store.Root(), f.holder+".json")); err != nil {
		t.Fatal(err)
	}
}

func (f *documentWaitFixture) waits(t *testing.T, store *runstate.Store) []runstate.DocumentWait {
	t.Helper()
	waits, err := store.DocumentWaits()
	if err != nil {
		t.Fatalf("DocumentWaits() error = %v", err)
	}
	return waits
}

// pipelineDocuments starts waiting documents through the real pipeline, noting
// each start in the fake harness's order so it can be read beside the items.
type pipelineDocuments struct {
	harness  *scheduleHarness
	store    *runstate.Store
	pipeline Pipeline
}

func (d pipelineDocuments) DocumentWaits() ([]runstate.DocumentWait, error) {
	return d.store.DocumentWaits()
}

func (d pipelineDocuments) Publish(ctx context.Context, document runstate.DocumentPublication) (Outcome, error) {
	d.harness.mu.Lock()
	d.harness.order = append(d.harness.order, document.RunID())
	d.harness.mu.Unlock()
	return d.pipeline.PublishWaitingDocument(ctx, document)
}

// A confirmed design that found every developer slot taken is held as
// waiting, durably, and its owning role is told so once however many messages
// it waits through. The harness restarts while it waits. The session that
// comes back finds it in the run store and, once the slot frees, lands it
// ahead of the ready development item without any message to the
// conversation; the conversation then reads the landing back.
func TestADocumentThatFoundEverySlotTakenWaitsThroughARestartAndLandsWhenOneFrees(t *testing.T) {
	fixture := waitingForASlot(t)

	recorded, err := fixture.store.Recorded()
	if err != nil || len(recorded) != 1 || recorded[0].RunID != fixture.holder {
		t.Fatalf("runs = %+v, %v; want only the development run, with nothing run for the document", recorded, err)
	}
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 || waits[0].Document.Owner != domain.RoleArchitect || waits[0].Document.Candidate.Artifact.ID != "recovery-design" {
		t.Fatalf("waits = %+v, want the architect's design waiting for a slot", waits)
	}
	if _, err := fixture.session.Send(context.Background(), "Carry on."); err != nil {
		t.Fatal(err)
	}
	if again := fixture.waits(t, fixture.store); len(again) != 1 || !again[0].Since.Equal(waits[0].Since) {
		t.Fatalf("waits after another message = %+v, want the one wait, still dated from when it began", again)
	}
	saved, err := fixture.options.Store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PendingWrites) != 1 || saved.PendingWrites[0].Publication == nil {
		t.Fatal("the confirmation was not kept while it waited")
	}
	told := fixture.provider.RequestsForRole(domain.RoleArchitect)[1].Prompt + saved.PendingTrackerResults
	if strings.Count(told, "waiting for a developer slot") != 1 || !strings.Contains(told, "ahead of any new development run") || strings.Contains(told, "could not start or finish") {
		t.Fatalf("the architect was told %q; want the wait said once, as a wait rather than a failure", told)
	}

	// The restart: nothing of the process that recorded the wait survives but
	// what is on disk.
	restarted, err := runstate.NewStore(fixture.root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if waits := fixture.waits(t, restarted); len(waits) != 1 || waits[0].RunID() != waits[0].Document.RunID() {
		t.Fatalf("waits after the restart = %+v, want the design still waiting", waits)
	}
	fixture.freeTheSlot(t)
	pipeline := fixture.pipeline
	pipeline.Store = restarted
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Documents = pipelineDocuments{harness: harness, store: restarted, pipeline: pipeline}
		return pull, err
	}}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	documentRun := waits[0].RunID()
	if got, want := harness.pullOrder(), []string{documentRun, "yoyodyne-one"}; !slices.Equal(got, want) {
		t.Fatalf("started %v, want the waiting design first and the ready item after it", got)
	}
	if schedule.Started[0].Failure != "" || schedule.Started[0].Declined != "" || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("document run = %#v, want it landed", schedule.Started[0])
	}
	landed, err := restarted.Load(documentRun)
	if err != nil || landed.Status != runstate.StatusSucceeded || landed.Integration == nil {
		t.Fatalf("document run = %+v, %v; want it integrated", landed, err)
	}
	if content := publicationGit(t, fixture.repository, "show", "main:docs/designs/recovery-design.md"); content != strings.TrimSpace(landed.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if waits := fixture.waits(t, restarted); len(waits) != 0 {
		t.Fatalf("waits after the landing = %+v, want none", waits)
	}

	// The conversation reads the landing back at its next offer and runs
	// nothing again.
	session, err := chat.Open(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, err = fixture.options.Store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.PendingTrackerResults, "landed through reviewed run "+documentRun) {
		t.Fatalf("conversation results = %q, want the landing read back", saved.PendingTrackerResults)
	}
	if reviews := len(fixture.provider.RequestsForRole(domain.RoleReviewer)); reviews != 1 {
		t.Fatalf("reviewer invocations = %d, want the design reviewed once", reviews)
	}
	all, err := restarted.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	documentRuns := 0
	for _, run := range all {
		if run.Document != nil {
			documentRuns++
		}
	}
	if documentRuns != 1 {
		t.Fatalf("document runs = %d, want exactly one", documentRuns)
	}
}

// A document waits while another landing run of it is already in flight in
// another process. Its conversation's next offer finds that run held, says
// nothing is wrong, starts nothing, and takes the document off the wait; the
// scheduler's start of it is the same.
func TestADocumentWaitingWhileItsRunIsInFlightElsewhereIsNeverStartedTwice(t *testing.T) {
	fixture := waitingForASlot(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 {
		t.Fatalf("waits = %+v, want the design waiting", waits)
	}
	fixture.freeTheSlot(t)
	document := waits[0].Document
	inFlight := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunID(), ProductID: fixture.pipeline.Config.Product.ID, RepositoryID: string(fixture.pipeline.Config.Product.RepositoryID), WorkItemID: document.RunID(), WorkItemTitle: "Publishing " + document.Candidate.Artifact.Title, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &document, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := fixture.store.Reserve(context.Background(), inFlight, 1)
	if err != nil {
		t.Fatalf("Reserve() the document's run in another process: %v", err)
	}
	defer lease.Release()
	// A wait offered once the run is recorded is not written: the run is not
	// waiting for anything.
	if _, waiting, err := fixture.store.WaitForSlot(context.Background(), document, time.Now()); err != nil {
		t.Fatalf("WaitForSlot() beside the recorded run error = %v", err)
	} else if waiting {
		t.Fatal("WaitForSlot() recorded a wait for a document whose run is already recorded")
	}

	if err := fixture.session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	outcome, err := fixture.pipeline.PublishWaitingDocument(context.Background(), document)
	if err != nil || outcome.RunID != document.RunID() || outcome.Status != runstate.StatusPending {
		t.Fatalf("scheduler's start = %+v, %v; want it to find the run in flight and leave it", outcome, err)
	}
	saved, err := fixture.options.Store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved.PendingTrackerResults, "could not start or finish") || len(saved.PendingWrites) != 1 || saved.PendingWrites[0].Publication == nil {
		t.Fatalf("conversation = %q with %d pending, want the handoff kept and no failure said", saved.PendingTrackerResults, len(saved.PendingWrites))
	}
	recorded, err := fixture.store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	documentRuns := 0
	for _, run := range recorded {
		if run.Document != nil {
			documentRuns++
		}
	}
	if documentRuns != 1 || len(fixture.provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatalf("document runs = %d, reviews = %d; want the one run in flight and nothing reviewed beside it", documentRuns, len(fixture.provider.RequestsForRole(domain.RoleReviewer)))
	}
	if waits := fixture.waits(t, fixture.store); len(waits) != 0 {
		t.Fatalf("waits = %+v, want the document off the wait once its run is recorded", waits)
	}
}
