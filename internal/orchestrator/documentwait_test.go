package orchestrator

import (
	"context"
	"errors"
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
// reads, and their publication: a document run holds a document landing in the
// harness's in-flight runs while it goes, exactly as a real reservation would.
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
	h.inFlight[runID] = documentLandingRun(runID, document)
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

// documentLandingRun is a confirmed document's run in flight, as the store lists one.
func documentLandingRun(runID string, document runstate.DocumentPublication) runstate.State {
	return runstate.State{RunID: runID, WorkItemID: runID, Status: runstate.StatusRunning, Document: &document}
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

// A line whose one developer slot is held by development work has a confirmed
// document waiting and a ready development item. The document starts at once,
// with nobody sending its conversation a message and without the developer slot:
// its landing is counted apart. The item still waits for the developer slot, and
// is pulled once the run holding it ends; that run is never stopped for either.
func TestAWaitingDocumentStartsWhileEveryDeveloperSlotIsTaken(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}
	wait := waitingDesign("document-4.1")
	documents := &harnessDocuments{harness: harness, waits: []runstate.DocumentWait{wait}}
	// The first poll finds the developer slot held; the wait after it is the one
	// the other run ends in. The session stops at the wait after that.
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		if _, running := h.inFlight["yoyodyne-elsewhere"]; !running {
			h.mu.Unlock()
			t.Error("the run holding the developer slot was stopped for the waiting document")
			return false
		}
		delete(h.inFlight, "yoyodyne-elsewhere")
		h.mu.Unlock()
		return true
	}
	var startedWhileFull bool
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Documents = documents
		return pull, err
	}

	schedule, err := Scheduler{Open: open, Watching: true, Sleep: func(ctx context.Context, interval time.Duration) bool {
		harness.mu.Lock()
		if harness.sleeps == 0 && len(documents.published) == 1 && slices.Equal(harness.order, []string{wait.RunID()}) {
			startedWhileFull = true
		}
		harness.mu.Unlock()
		return harness.sleep(ctx, interval)
	}}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !startedWhileFull {
		t.Fatalf("started %v by the first wait, want the document alone started while the developer slot was held", harness.pullOrder())
	}
	if got, want := harness.pullOrder(), []string{wait.RunID(), "yoyodyne-one"}; !slices.Equal(got, want) {
		t.Fatalf("started %v, want the waiting document first and the ready item once the developer slot freed", got)
	}
	if len(schedule.Started) != 2 || schedule.Started[0].WorkItemID != wait.RunID() {
		t.Fatalf("schedule = %s, want the document's run first", schedule.Render())
	}
	reason := schedule.Started[0].Reason
	for _, says := range []string{"document-4.1", "Recovery design", "architect", "without taking a developer slot"} {
		if !strings.Contains(reason, says) {
			t.Fatalf("document run reason = %q, want it to say %q", reason, says)
		}
	}
}

// Every document landing is taken by another document's run while developer
// slots are free. The waiting document is passed over saying it takes the next
// landing that frees, the ready development item is pulled all the same — a
// developer run never waits on a landing — and the document starts once the
// landing frees.
func TestAWaitingDocumentWaitsForALandingAndNeverHoldsUpDevelopment(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 3
	other := waitingDesign("document-2.1")
	harness.inFlight[other.RunID()] = documentLandingRun(other.RunID(), other.Document)
	wait := waitingDesign("document-4.1")
	documents := &harnessDocuments{harness: harness, waits: []runstate.DocumentWait{wait}}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps > 1 {
			return false
		}
		h.mu.Lock()
		delete(h.inFlight, other.RunID())
		h.mu.Unlock()
		return true
	}
	schedule, err := Scheduler{Open: func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Documents = documents
		pull.DocumentLandings = 1
		return pull, err
	}, Watching: true, Sleep: harness.sleep}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got, want := harness.pullOrder(), []string{"yoyodyne-one", wait.RunID()}; !slices.Equal(got, want) {
		t.Fatalf("started %v, want the ready item at once and the document once the landing freed", got)
	}
	if !deferredSaying(schedule, wait.RunID(), "for one of the 1 document landings to free, and takes the next one that does") {
		t.Fatalf("passed over = %#v, want the document named as waiting for a landing", schedule.Deferred)
	}
}

// A document whose landing run is already in flight — started by its
// conversation, or by a session before this one — is left to that run, and a
// pull never starts it a second time, however many landings are free.
func TestAWaitingDocumentWhoseRunIsAlreadyInFlightIsNotStartedAgain(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 3
	wait := waitingDesign("document-7.1")
	harness.inFlight[wait.RunID()] = documentLandingRun(wait.RunID(), wait.Document)
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
// found the one document landing held by another document's run, beside a
// development run holding the one developer slot: the design is saved,
// confirmed, waiting in the run store, and nothing has run for it.
type documentWaitFixture struct {
	repository string
	root       string
	provider   *orchestratortest.Backend
	pipeline   Pipeline
	store      *runstate.Store
	options    chat.Options
	session    *chat.Session
	developer  string
	landing    string
}

// holdTheDeveloperSlot records a development run in flight, holding the one
// developer slot, and returns its run.
func holdTheDeveloperSlot(t *testing.T, store *runstate.Store, pipeline Pipeline) string {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	holder := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: "yoyodyne-development", Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := store.Reserve(context.Background(), holder, pipeline.Config.Execution.MaxConcurrentDevelopers)
	if err != nil {
		t.Fatalf("Reserve() the development run holding the slot: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return runID
}

// otherLanding is another confirmed document's run, pending, as its
// reservation records it.
func otherLanding(t *testing.T, repository string, pipeline Pipeline, writeID string) runstate.State {
	t.Helper()
	candidate, automatic, err := artifact.StoreFor(repository, pipeline.Config.Product).PrepareConfirmation(domain.RoleArchitect, artifact.Write{Action: artifact.WriteCreate, ID: "other-design", Kind: artifact.KindDesign, Title: "Other design", Directory: "docs/designs", Body: "# Other", Reason: "written by the architect"}, artifact.Policy{Designs: domain.ApprovalAutomatic}, "confirmed by harness", time.Now())
	if err != nil || !automatic {
		t.Fatalf("PrepareConfirmation() = %v, %v", automatic, err)
	}
	other := runstate.DocumentPublication{ConversationID: "chat-other", WriteID: writeID, Turn: 1, Owner: domain.RoleArchitect, AuthorBackend: string(domain.BackendClaudeCode), AuthorModel: "opus", Candidate: candidate}
	return runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: other.RunID(), ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: other.RunID(), WorkItemTitle: "Publishing Other design", Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &other, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
}

// holdTheLanding records another confirmed document's run in flight, holding
// the one document landing, and returns its run.
func holdTheLanding(t *testing.T, repository string, store *runstate.Store, pipeline Pipeline) string {
	t.Helper()
	holder := otherLanding(t, repository, pipeline, "document-1.1")
	lease, err := store.ReserveDocumentLanding(context.Background(), holder, pipeline.Config.Execution.DocumentLandingLimit())
	if err != nil {
		t.Fatalf("ReserveDocumentLanding() the other document's run: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return holder.RunID
}

// The developer slots and the document landings are two counts that never
// refuse each other: a landing is reserved with every developer slot taken, a
// developer run is refused only by the developer slots, a landing only by the
// landings, and neither reservation takes a run of the other kind.
func TestTheDocumentLandingsAndTheDeveloperSlotsNeverRefuseEachOther(t *testing.T) {
	t.Parallel()

	repository, _, _, pipeline, store := documentPipeline(t)
	holdTheDeveloperSlot(t, store, pipeline)
	holdTheLanding(t, repository, store, pipeline)

	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	developer := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: runID, ProductID: pipeline.Config.Product.ID, RepositoryID: string(pipeline.Config.Product.RepositoryID), WorkItemID: "yoyodyne-second", Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	var capacity runstate.CapacityError
	if _, err := store.Reserve(context.Background(), developer, 1); !errors.As(err, &capacity) || capacity.Landings || capacity.Active != 1 {
		t.Fatalf("Reserve() a second development run = %v, want the developer slots full and only the one development run counted", err)
	}
	second := otherLanding(t, repository, pipeline, "document-2.1")
	if _, err := store.ReserveDocumentLanding(context.Background(), second, 1); !errors.As(err, &capacity) || !capacity.Landings || capacity.Active != 1 || !strings.Contains(err.Error(), "document landings are full") {
		t.Fatalf("ReserveDocumentLanding() a second landing = %v, want the landings full and only the one landing counted", err)
	}
	if _, err := store.Reserve(context.Background(), second, 5); err == nil {
		t.Fatal("Reserve() took a document's run into a developer slot")
	}
	if _, err := store.ReserveDocumentLanding(context.Background(), developer, 5); err == nil {
		t.Fatal("ReserveDocumentLanding() took a development run as a document landing")
	}
}

// documentPipeline is an architect's conversation over a real run store with
// one developer slot and one document landing, the design it writes being the
// recovery design.
func documentPipeline(t *testing.T) (string, string, *orchestratortest.Backend, Pipeline, *runstate.Store) {
	t.Helper()
	repository, _, provider, pipeline, _ := automaticFixture(t)
	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Store = store
	pipeline.Config.Execution.MaxConcurrentDevelopers = 1
	pipeline.Config.Execution.DocumentLandings = 1
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
	return repository, root, provider, pipeline, store
}

func waitingForALanding(t *testing.T) *documentWaitFixture {
	t.Helper()
	repository, root, provider, pipeline, store := documentPipeline(t)
	developer := holdTheDeveloperSlot(t, store, pipeline)
	landing := holdTheLanding(t, repository, store, pipeline)

	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatalf("a document waiting for a landing stopped the message: %v", err)
	}
	return &documentWaitFixture{repository: repository, root: root, provider: provider, pipeline: pipeline, store: store, options: options, session: session, developer: developer, landing: landing}
}

// A confirmed design written while the one developer slot is held by
// development work lands at once through its reviewed run: the landing is
// counted on its own, so nothing waits, and the development run is untouched.
func TestADocumentLandsWhileEveryDeveloperSlotIsTaken(t *testing.T) {
	t.Parallel()

	repository, _, provider, pipeline, store := documentPipeline(t)
	developer := holdTheDeveloperSlot(t, store, pipeline)
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write the design."); err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	var landed *runstate.State
	for index := range recorded {
		if recorded[index].Document != nil {
			landed = &recorded[index]
		}
	}
	if landed == nil || landed.Status != runstate.StatusSucceeded || landed.Integration == nil {
		t.Fatalf("runs = %+v, want the design landed beside the development run", recorded)
	}
	if held, err := store.Load(developer); err != nil || !held.HoldsDeveloperSlot() {
		t.Fatalf("development run = %+v, %v; want it still holding its slot", held, err)
	}
	if waits, err := store.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v; want nothing waiting", waits, err)
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/recovery-design.md"); content != strings.TrimSpace(landed.Document.Candidate.Content) {
		t.Fatalf("published different bytes: %s", content)
	}
	if len(provider.RequestsForRole(domain.RoleReviewer)) != 1 || len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("the landing did not go through exactly one independent review")
	}
}

// freeTheLanding ends the other document's run holding the only landing. The
// development run goes on holding the only developer slot.
func (f *documentWaitFixture) freeTheLanding(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.store.Root(), f.landing+".json")); err != nil {
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

// A confirmed design that found every document landing taken is held as
// waiting, durably, and its owning role is told so once however many messages
// it waits through. The harness restarts while it waits. The session that
// comes back finds it in the run store and, once the landing frees, lands it
// without any message to the conversation and while the developer slot is
// still held; the conversation then reads the landing back.
func TestADocumentThatFoundEveryLandingTakenWaitsThroughARestartAndLandsWhenOneFrees(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)

	recorded, err := fixture.store.Recorded()
	if err != nil || len(recorded) != 2 {
		t.Fatalf("runs = %+v, %v; want only the development run and the other landing, with nothing run for the document", recorded, err)
	}
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 || waits[0].Document.Owner != domain.RoleArchitect || waits[0].Document.Candidate.Artifact.ID != "recovery-design" {
		t.Fatalf("waits = %+v, want the architect's design waiting for a landing", waits)
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
	if strings.Count(told, "waiting for a document landing to free") != 1 || !strings.Contains(told, "without taking a developer slot") || strings.Contains(told, "could not start or finish") {
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
	fixture.freeTheLanding(t)
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
	// The order the pass chose them in is the order of its started entries. Each
	// run then goes on in a goroutine of its own, so the order the two reached the
	// fakes in is the machine's, and only that both were reached is asked of it.
	var chosen []string
	for _, started := range schedule.Started {
		chosen = append(chosen, started.WorkItemID)
	}
	if want := []string{documentRun, "yoyodyne-one"}; !slices.Equal(chosen, want) {
		t.Fatalf("started %v, want the waiting design first and the ready item after it", chosen)
	}
	if reached := harness.pullOrder(); len(reached) != 2 || !slices.Contains(reached, documentRun) || !slices.Contains(reached, "yoyodyne-one") {
		t.Fatalf("runs reached %v, want the design and the ready item each run once", reached)
	}
	if schedule.Started[0].Failure != "" || schedule.Started[0].Declined != "" || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("document run = %#v, want it landed", schedule.Started[0])
	}
	landed, err := restarted.Load(documentRun)
	if err != nil || landed.Status != runstate.StatusSucceeded || landed.Integration == nil {
		t.Fatalf("document run = %+v, %v; want it integrated", landed, err)
	}
	if held, err := restarted.Load(fixture.developer); err != nil || !held.HoldsDeveloperSlot() {
		t.Fatalf("development run = %+v, %v; want it still holding the only developer slot the design landed beside", held, err)
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
	t.Parallel()

	fixture := waitingForALanding(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 {
		t.Fatalf("waits = %+v, want the design waiting", waits)
	}
	fixture.freeTheLanding(t)
	document := waits[0].Document
	inFlight := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunID(), ProductID: fixture.pipeline.Config.Product.ID, RepositoryID: string(fixture.pipeline.Config.Product.RepositoryID), WorkItemID: document.RunID(), WorkItemTitle: "Publishing " + document.Candidate.Artifact.Title, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &document, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := fixture.store.ReserveDocumentLanding(context.Background(), inFlight, 1)
	if err != nil {
		t.Fatalf("Reserve() the document's run in another process: %v", err)
	}
	defer lease.Release()
	// A wait offered once the run is recorded is not written: the run is not
	// waiting for anything.
	if _, waiting, err := fixture.store.WaitForSlot(context.Background(), document, 0, time.Now()); err != nil {
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

// A document whose first run stopped without judging it is published again by
// a run of its own. Where that retry finds every document landing taken, it waits
// the same way the first run would have: the one wait for the document now
// names the retry's run and is still dated from when the document began
// waiting, a run of an earlier attempt does not count as the retry's, and the
// scheduler's start lands the retry once a landing frees.
func TestARetryOfADocumentThatFindsEveryLandingTakenWaitsAndLandsWhenOneFrees(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 || waits[0].Attempt != 0 {
		t.Fatalf("waits = %+v, want the design's first run waiting", waits)
	}
	document := waits[0].Document
	first := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunIDFor(0), ProductID: fixture.pipeline.Config.Product.ID, RepositoryID: string(fixture.pipeline.Config.Product.RepositoryID), WorkItemID: document.RunIDFor(0), WorkItemTitle: "Publishing " + document.Candidate.Artifact.Title, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &document, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := fixture.store.ReserveDocumentLanding(context.Background(), first, 2)
	if err != nil {
		t.Fatalf("Reserve() the document's first run: %v", err)
	}
	stopped := newScheduleHarness().now
	first.Status, first.Failure, first.CompletedAt = runstate.StatusFailed, "the forge did not answer", &stopped
	if err := fixture.store.Save(first); err != nil {
		t.Fatalf("Save() the first run as stopped without judging the document: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	delivery, err := fixture.pipeline.PublishDocument(context.Background(), document)
	if err != nil || delivery.WaitingForSlot == nil || delivery.RunID != document.RunIDFor(1) {
		t.Fatalf("PublishDocument() = %+v, %v; want the retry waiting for a slot as run %s", delivery, err, document.RunIDFor(1))
	}
	retrying := fixture.waits(t, fixture.store)
	if len(retrying) != 1 || retrying[0].Attempt != 1 || retrying[0].RunID() != document.RunIDFor(1) || !retrying[0].Since.Equal(waits[0].Since) {
		t.Fatalf("waits = %+v, want the one wait naming the retry and dated from %s", retrying, waits[0].Since)
	}
	if _, waiting, err := fixture.store.WaitForSlot(context.Background(), document, 1, time.Now()); err != nil || !waiting {
		t.Fatalf("WaitForSlot() beside only the first run's record = %v, %v; want the retry still waiting", waiting, err)
	}

	fixture.freeTheLanding(t)
	outcome, err := fixture.pipeline.PublishWaitingDocument(context.Background(), document)
	if err != nil || outcome.RunID != document.RunIDFor(1) || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("scheduler's start = %+v, %v; want the retry landed as run %s", outcome, err, document.RunIDFor(1))
	}
	if waits := fixture.waits(t, fixture.store); len(waits) != 0 {
		t.Fatalf("waits after the retry landed = %+v, want none", waits)
	}
	if _, err := fixture.store.Load(document.RunIDFor(2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a third run of the document was recorded: %v", err)
	}
}

// A document whose first run's checks ran out of time finds the one document
// landing taken by another document's run when its retry is offered. The retry
// waits for that landing, as the document's wait for slot says, and for nothing
// else: once the landing frees, the scheduler's start lands it while the one
// developer slot is still held by development work.
func TestARetryAfterATimeLimitWaitsWhileEveryLandingIsTaken(t *testing.T) {
	t.Parallel()

	fixture := waitingForALanding(t)
	waits := fixture.waits(t, fixture.store)
	if len(waits) != 1 || waits[0].Attempt != 0 {
		t.Fatalf("waits = %+v, want the design's first run waiting", waits)
	}
	document := waits[0].Document
	first := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: document.RunIDFor(0), ProductID: fixture.pipeline.Config.Product.ID, RepositoryID: string(fixture.pipeline.Config.Product.RepositoryID), WorkItemID: document.RunIDFor(0), WorkItemTitle: "Publishing " + document.Candidate.Artifact.Title, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, Document: &document, StartedAt: newScheduleHarness().now, UpdatedAt: newScheduleHarness().now}
	lease, err := fixture.store.ReserveDocumentLanding(context.Background(), first, 2)
	if err != nil {
		t.Fatalf("Reserve() the document's first run: %v", err)
	}
	stopped := newScheduleHarness().now
	first.Status, first.Failure, first.CompletedAt, first.StopClass = runstate.StatusFailed, "check 1 was stopped at its 1s execution.check_timeout budget", &stopped, runstate.StopCheckTimeout
	if err := fixture.store.Save(first); err != nil {
		t.Fatalf("Save() the first run as stopped by its check time limit: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if saved, err := fixture.store.Load(first.RunID); err != nil || !saved.DocumentStoppedOnTime() || saved.DocumentJudged() {
		t.Fatalf("first run = %+v, %v; want it stopped on time without judging the document", saved, err)
	}

	delivery, err := fixture.pipeline.PublishDocument(context.Background(), document)
	if err != nil || delivery.WaitingForSlot == nil || !delivery.WaitingForSlot.Landings || delivery.RunID != document.RunIDFor(1) {
		t.Fatalf("PublishDocument() = %+v, %v; want the retry waiting for a document landing as run %s", delivery, err, document.RunIDFor(1))
	}
	if _, err := fixture.store.Load(document.RunIDFor(1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the retry was recorded while every landing was taken: %v", err)
	}
	if retrying := fixture.waits(t, fixture.store); len(retrying) != 1 || retrying[0].RunID() != document.RunIDFor(1) {
		t.Fatalf("waits = %+v, want the one wait naming the retry", retrying)
	}

	fixture.freeTheLanding(t)
	outcome, err := fixture.pipeline.PublishWaitingDocument(context.Background(), document)
	if err != nil || outcome.RunID != document.RunIDFor(1) || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("scheduler's start = %+v, %v; want the retry landed as run %s", outcome, err, document.RunIDFor(1))
	}
	if held, err := fixture.store.Load(fixture.developer); err != nil || !held.HoldsDeveloperSlot() {
		t.Fatalf("development run = %+v, %v; want it still holding the only developer slot the retry landed beside", held, err)
	}
	if recorded, err := fixture.store.Load(first.RunID); err != nil || recorded.DocumentRetry == nil || !strings.Contains(recorded.DocumentRetry.WaitedFor, "document landing was free after its checks ran out of time") {
		t.Fatalf("first run's retry record = %+v, %v; want it to say the retry waited for a document landing", recorded.DocumentRetry, err)
	}
}
