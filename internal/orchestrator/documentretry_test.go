package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The work item the documents in these tests name at the start of their
// revision reason, which is the automatic fixture's own item.
const documentRetryItem = "yoyodyne-task"

// retryDesign is the architect's one document, written once: every later turn
// of the conversation writes nothing, so a run after the first can only be the
// harness running the stored text again.
func retryDesign(provider *orchestratortest.Backend, id string) {
	original := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleArchitect {
			if len(provider.RequestsForRole(domain.RoleArchitect)) > 1 {
				return backend.RunResult{SessionID: "author", FinalText: "Noted."}, nil
			}
			return backend.RunResult{SessionID: "author", FinalText: fmt.Sprintf("Written.\n%s\n{\"documents\":[{\"action\":\"create\",\"id\":%q,\"kind\":\"design\",\"title\":\"Retried design\",\"directory\":\"docs/designs\",\"body\":\"# The owner's words\",\"reason\":%q}]}\n```", artifact.WriteFence, id, documentRetryItem+": drafted by the architect")}, nil
		}
		return original(request)
	}
}

// slowOnce is a check that runs past its budget the first time it is run and
// passes every time after, which is a suite stopped by load and then let finish.
func slowOnce(t *testing.T) string {
	marker := filepath.Join(t.TempDir(), "ran")
	return fmt.Sprintf("if [ -f %q ]; then exit 0; fi; touch %q; sleep 5", marker, marker)
}

// forgeFailing refuses the first failures promotions the way a forge answering
// with its own server error does, and promotes normally after that.
type forgeFailing struct {
	WorktreeManager
	failures int
}

func (w *forgeFailing) Integrate(ctx context.Context, tree gitworktree.Worktree, message string) (gitworktree.Integration, error) {
	if w.failures != 0 {
		w.failures--
		return gitworktree.Integration{}, errors.New("push to origin failed: remote: Internal Server Error (HTTP 500)")
	}
	return w.WorktreeManager.Integrate(ctx, tree, message)
}

func conversationRecord(t *testing.T, store *runstate.ConversationStore) runstate.Conversation {
	t.Helper()
	saved, err := store.Load(runstate.ConversationIdentity{Role: domain.RoleArchitect, Agent: "architect"})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// toldArchitect is everything the owning role has been told, delivered or not.
func toldArchitect(provider *orchestratortest.Backend, saved runstate.Conversation) string {
	var told strings.Builder
	for _, request := range provider.RequestsForRole(domain.RoleArchitect) {
		told.WriteString(request.Prompt)
	}
	return told.String() + saved.PendingTrackerResults
}

func documentRuns(t *testing.T, runs *runstate.Store) []runstate.State {
	t.Helper()
	recorded, err := runs.Recorded()
	if err != nil {
		t.Fatal(err)
	}
	return recorded
}

func runByID(t *testing.T, runs *runstate.Store, id string) runstate.State {
	t.Helper()
	state, err := runs.Load(id)
	if err != nil {
		t.Fatalf("run %s: %v", id, err)
	}
	return state
}

// A check stopped by its time limit judged nothing about the document, so the
// document is not handed back to the architect: the harness runs the same text
// again, counts nothing against it, and writes both runs onto the work item the
// revision names.
func TestADocumentRunStoppedByACheckTimeLimitIsRunAgainWithTheSameText(t *testing.T) {
	repository, tracker, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "retried-design")
	pipeline.Config.Execution.CheckTimeout = config.Duration(time.Second)
	pipeline.Checks = checks.Runner{Process: execution.OSProcessRunner{}, Timeout: time.Second}
	pipeline.Config.Checks = []string{slowOnce(t)}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 2 {
		t.Fatalf("runs = %d, want the stopped run and one more: %s %s %s %q %+v", len(recorded), recorded[0].Status, recorded[0].Phase, recorded[0].RecordedStopClass(), recorded[0].Failure, recorded[0].DocumentRetry)
	}
	document := *recorded[0].Document
	first, second := runByID(t, runs, document.RunIDFor(0)), runByID(t, runs, document.RunIDFor(1))
	if first.Status == runstate.StatusSucceeded || first.DocumentJudged() || first.RecordedStopClass() != runstate.StopCheckTimeout {
		t.Fatalf("first run = %s %s, want stopped on its check time limit without judging the document", first.Status, first.RecordedStopClass())
	}
	if !strings.Contains(first.Failure, "was stopped at its 1s execution.check_timeout budget") {
		t.Fatalf("first run's reason = %q, want the budget that stopped it", first.Failure)
	}
	if second.Status != runstate.StatusSucceeded || second.Integration == nil {
		t.Fatalf("second run = %s, want the same text landed", second.Status)
	}
	if first.DocumentRetry == nil || first.DocumentRetry.Next != second.RunID || first.DocumentRetry.WaitedFor == "" {
		t.Fatalf("first run's retry record = %+v, want what the next attempt waited for and which run it was", first.DocumentRetry)
	}
	if second.Selection == nil || !strings.Contains(second.Selection.Reason, "Attempt 2 of 3") {
		t.Fatalf("second run's selection = %+v, want it to say which attempt it is and why", second.Selection)
	}
	if publicationGit(t, repository, "show", "main:docs/designs/retried-design.md") != strings.TrimSpace(second.Document.Candidate.Content) {
		t.Fatal("the target holds something other than the confirmed text")
	}
	saved := conversationRecord(t, store)
	if len(saved.DocumentReturns) != 0 {
		t.Fatalf("returns = %v, want nothing counted for a stop that judged nothing", saved.DocumentReturns)
	}
	told := toldArchitect(provider, saved)
	if !strings.Contains(told, "stopped without judging it") || !strings.Contains(told, "does not need writing again") || strings.Contains(told, "must revise") {
		t.Fatalf("the architect was told:\n%s", told)
	}
	if len(provider.RequestsForRole(domain.RoleDeveloper)) != 0 {
		t.Fatal("a developer was invoked")
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	for _, want := range []string{"started run " + first.RunID, "stopped publishing document", "started run " + second.RunID, "published document"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the work item's notes do not say %q:\n%s", want, notes)
		}
	}
}

// A forge that answers a push with its own server error judged nothing either.
func TestADocumentRunStoppedByAForgeErrorIsRunAgainWithTheSameText(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "forge-design")
	pipeline.Worktrees = &forgeFailing{WorktreeManager: pipeline.Worktrees, failures: 1}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 2 {
		t.Fatalf("runs = %d, want two", len(recorded))
	}
	document := *recorded[0].Document
	first, second := runByID(t, runs, document.RunIDFor(0)), runByID(t, runs, document.RunIDFor(1))
	if first.DocumentJudged() || !strings.Contains(first.Failure, "HTTP 500") {
		t.Fatalf("first run = %s %q, want the forge's error as a stop that judged nothing", first.RecordedStopClass(), first.Failure)
	}
	if second.Status != runstate.StatusSucceeded {
		t.Fatalf("second run = %s %q", second.Status, second.Failure)
	}
	if saved := conversationRecord(t, store); len(saved.DocumentReturns) != 0 || strings.Contains(toldArchitect(provider, saved), "must revise") {
		t.Fatalf("a forge error was handed back to the architect: %v", saved.DocumentReturns)
	}
}

// A check that ran and failed, and a reviewer that refused, did judge the
// document: it goes back to its owner as before, counted, and is not run again.
func TestADocumentRunThatJudgedTheDocumentIsHandedBackAndNotRunAgain(t *testing.T) {
	for _, cause := range []string{"check", "review"} {
		t.Run(cause, func(t *testing.T) {
			_, _, provider, pipeline, runs := automaticFixture(t)
			retryDesign(provider, "judged-design")
			if cause == "check" {
				pipeline.Config.Checks = []string{"! grep -q 'owner' docs/designs/judged-design.md"}
			} else {
				original := provider.Respond
				provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
					if request.Role == domain.RoleReviewer {
						return backend.RunResult{SessionID: "independent", FinalText: `{"decision":"repair","summary":"revise it","findings":[{"severity":"blocker","message":"name the recovery step"}]}`}, nil
					}
					return original(request)
				}
			}
			options, store := publicationConversation(t, pipeline, provider)
			session, err := chat.Open(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Send(context.Background(), "Write it."); err != nil {
				t.Fatal(err)
			}
			if err := session.PublishDocuments(context.Background()); err != nil {
				t.Fatal(err)
			}
			recorded := documentRuns(t, runs)
			if len(recorded) != 1 || !recorded[0].DocumentJudged() {
				t.Fatalf("runs = %d, want the one run that judged the document and no retry", len(recorded))
			}
			saved := conversationRecord(t, store)
			if saved.DocumentReturns["judged-design"] != 1 || !strings.Contains(toldArchitect(provider, saved), "architect must revise") {
				t.Fatalf("returns = %v; told:\n%s", saved.DocumentReturns, toldArchitect(provider, saved))
			}
		})
	}
}

// Once every attempt has stopped without judging the document, the last run is
// put to the development manager as a stopped run with the text kept on it, and
// the architect is told it does not need to write it again.
func TestADocumentWhoseEveryAttemptJudgedNothingIsHandedToTheDevelopmentManager(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "stubborn-design")
	pipeline.Worktrees = &forgeFailing{WorktreeManager: pipeline.Worktrees, failures: -1}
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Docket = docketerOverStore(docket, runs, pipeline.Config)
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runstate.MaxDocumentAttempts+1; i++ {
		if err := session.PublishDocuments(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != runstate.MaxDocumentAttempts {
		t.Fatalf("runs = %d, want %d and no more", len(recorded), runstate.MaxDocumentAttempts)
	}
	last := runByID(t, runs, recorded[0].Document.RunIDFor(runstate.MaxDocumentAttempts-1))
	if last.DocumentRetry == nil || !last.DocumentRetry.HandedOver || last.Blocker == "" || last.Document.Candidate.Content == "" {
		t.Fatalf("last run = %+v, blocker %q; want it handed over with the text kept", last.DocumentRetry, last.Blocker)
	}
	entries, err := docket.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].RunID != last.RunID {
		t.Fatalf("docket = %+v, want the last run put to the development manager", entries)
	}
	saved := conversationRecord(t, store)
	told := toldArchitect(provider, saved)
	if len(saved.DocumentReturns) != 0 || len(saved.PendingWrites) != 0 || !strings.Contains(told, "handed to the development manager") || strings.Contains(told, "must revise") {
		t.Fatalf("returns %v, pending %d; told:\n%s", saved.DocumentReturns, len(saved.PendingWrites), told)
	}
}

// A retry after a check time limit does not start under the load that caused
// it: while as many developer runs are going as were when the checks ran out of
// time it waits, and the record says what it waited for once it starts.
func TestADocumentRetryAfterATimeLimitWaitsForFewerDeveloperRuns(t *testing.T) {
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "loaded-design")
	pipeline.Config.Execution.MaxConcurrentDevelopers = 4
	developer := pipeline.Config.Agents["developer"]
	developer.Instances = 4
	pipeline.Config.Agents["developer"] = developer
	pipeline.Config.Execution.CheckTimeout = config.Duration(time.Second)
	pipeline.Checks = checks.Runner{Process: execution.OSProcessRunner{}, Timeout: time.Second}
	pipeline.Config.Checks = []string{slowOnce(t)}
	var others []string
	for index, id := range []string{"other-one", "other-two"} {
		state := runstate.State{SchemaVersion: runstate.StateSchemaVersion, RunID: fmt.Sprintf("run-%032x", index+1), ProductID: "yoyodyne", RepositoryID: "yoyodyne", WorkItemID: id, Backend: domain.BackendClaudeCode, Status: runstate.StatusPending, StartedAt: time.Now(), UpdatedAt: time.Now()}
		lease, err := runs.Reserve(context.Background(), state, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
		others = append(others, state.RunID)
	}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopped := documentRuns(t, runs)
	var first runstate.State
	for _, state := range stopped {
		if state.Document != nil {
			first = state
		}
	}
	if first.DocumentRetry == nil || first.DocumentRetry.DevelopersAtStop != 3 {
		t.Fatalf("retry record = %+v, want three developer runs counted at the stop; told %s", first.DocumentRetry, toldArchitect(provider, conversationRecord(t, store)))
	}
	if _, err := runs.Load(first.Document.RunIDFor(1)); err == nil {
		t.Fatal("the retry started under the load that stopped the first run")
	}
	if told := toldArchitect(provider, conversationRecord(t, store)); !strings.Contains(told, "waits until fewer are going") {
		t.Fatalf("the architect was not told the retry is waiting:\n%s", told)
	}
	// One of the other runs finishes, and the next message starts the retry.
	finished, lease, err := runs.AdoptRun(context.Background(), others[0])
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now()
	finished.Status, finished.CompletedAt, finished.Failure, finished.StopClass = runstate.StatusCancelled, &completed, "stopped by the test", runstate.StopCancelled
	if err := runs.Save(finished); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := runByID(t, runs, first.Document.RunIDFor(1))
	if second.Status != runstate.StatusSucceeded {
		t.Fatalf("retry = %s %q, want it landed once the load fell", second.Status, second.Failure)
	}
	first = runByID(t, runs, first.RunID)
	if !strings.Contains(first.DocumentRetry.WaitedFor, "fewer developer runs were going: 3 when its checks ran out of time, 2 with this attempt") {
		t.Fatalf("waited for = %q", first.DocumentRetry.WaitedFor)
	}
}

// A document an older build stopped publishing after three returns that judged
// nothing is published again from its kept text when its conversation opens,
// and the returns that judged nothing stop counting.
func TestADocumentStoppedOverReturnsThatJudgedNothingIsResumed(t *testing.T) {
	repository, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "stopped-design")
	pipeline.Config.Execution.CheckTimeout = config.Duration(time.Second)
	pipeline.Checks = checks.Runner{Process: execution.OSProcessRunner{}, Timeout: time.Second}
	pipeline.Config.Checks = []string{slowOnce(t)}
	options, store := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	// What the older build left: the handoff decided and dropped, and the stop
	// counted as the third return.
	saved := conversationRecord(t, store)
	saved.PendingWrites = nil
	saved.DocumentReturns = map[string]int{"stopped-design": runstate.MaxDocumentReturns}
	if err := store.Save(saved); err != nil {
		t.Fatal(err)
	}
	session, err = chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorded := documentRuns(t, runs)
	if len(recorded) != 2 {
		t.Fatalf("runs = %d, want the resumed document run once more", len(recorded))
	}
	if content := publicationGit(t, repository, "show", "main:docs/designs/stopped-design.md"); !strings.Contains(content, "# The owner's words") {
		t.Fatalf("target = %s", content)
	}
	saved = conversationRecord(t, store)
	if _, counted := saved.DocumentReturns["stopped-design"]; counted || !strings.Contains(saved.PendingTrackerResults, "is published again") {
		t.Fatalf("returns = %v; told:\n%s", saved.DocumentReturns, saved.PendingTrackerResults)
	}
	if len(provider.RequestsForRole(domain.RoleArchitect)) != 1 {
		t.Fatal("the architect was asked to write the document again")
	}
}

// A check stopped by the deadline of whatever started its run is said to be
// stopped by that, never by the thirty-minute budget it did not reach; and a
// timed-out check that stopped short of its budget does not claim the budget.
func TestACheckStopNeverClaimsABudgetItDidNotReach(t *testing.T) {
	started := time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC)
	check := checks.Result{Command: "make race", Timeout: 30 * time.Minute, Process: execution.ProcessResult{Status: execution.ProcessTimedOut, StartedAt: started, FinishedAt: started.Add(6*time.Minute + 43*time.Second)}, StoppedByCaller: true}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	said := callerStoppedCheck(ctx, check).Error()
	if !strings.Contains(said, "make race ran for 6m43s") || !strings.Contains(said, "deadline of the work that started this run") || strings.Contains(said, "stopped at its") {
		t.Fatalf("caller's deadline said as %q", said)
	}
	if class, _ := recordedStopIn(callerStoppedCheck(ctx, check)); class != runstate.StopCheckTimeout {
		t.Fatalf("class = %s, want a time limit", class)
	}
	check.StoppedByCaller = false
	if said := checkTimedOut(check).Error(); strings.Contains(said, "stopped at its 30m0s") || !strings.Contains(said, "before it reached its own 30m0s") {
		t.Fatalf("a stop short of the budget said as %q", said)
	}
	check.Process.FinishedAt = started.Add(30 * time.Minute)
	if said := checkTimedOut(check).Error(); !strings.Contains(said, "ran for 30m0s and was stopped at its 30m0s execution.check_timeout budget") {
		t.Fatalf("a stop at the budget said as %q", said)
	}
}
