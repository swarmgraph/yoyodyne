package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// stoppedPublication is a document publication whose every attempt stopped
// without judging it, handed to the development manager with pull request #1173
// left open on the forge, which is the shape the stopped architect designs were
// left in. It returns the pipeline with promotion working again, the docketer
// her docket is built by, the forge holding the request, and the stopped run.
func stoppedPublication(t *testing.T) (Pipeline, *Docketer, *orchestratortest.Forge, *runstate.Store, runstate.State) {
	t.Helper()
	_, _, provider, pipeline, runs := automaticFixture(t)
	retryDesign(provider, "stuck-design")
	failing := &forgeFailing{WorktreeManager: pipeline.Worktrees, failures: -1}
	pipeline.Worktrees = failing
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Docket = docketerOverStore(docket, runs, pipeline.Config)
	options, _ := publicationConversation(t, pipeline, provider)
	session, err := chat.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Write it."); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runstate.MaxDocumentAttempts; i++ {
		if err := session.PublishDocuments(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	recorded := documentRuns(t, runs)
	last := runByID(t, runs, recorded[0].Document.RunIDFor(runstate.MaxDocumentAttempts-1))
	if last.DocumentRetry == nil || !last.DocumentRetry.HandedOver {
		t.Fatalf("last run = %+v, want it handed to the development manager", last.DocumentRetry)
	}
	published := runstate.PullRequest{Remote: "origin", Branch: last.Branch, Number: 1173, URL: "https://github.example/yoyodyne/pull/1173", HeadCommit: last.BaseCommit, State: "OPEN"}
	last.PullRequest = &published
	if err := runs.Save(last); err != nil {
		t.Fatal(err)
	}
	forge := &orchestratortest.Forge{}
	forge.Hold(last.Branch, publish.PullRequest{Number: 1173, URL: published.URL, State: "OPEN", HeadCommit: last.BaseCommit})
	failing.failures = 0
	return pipeline, pipeline.Docket, forge, runs, last
}

func decider(runs *runstate.Store, forge *orchestratortest.Forge) DocumentDecider {
	return DocumentDecider{Runs: runs, Publications: forge}
}

var developmentManagersDecision = runstate.DocumentDecision{Reason: "the newest copy of this design, which nothing judged", DecidedBy: "the development manager in conversation chat-dm at turn 4"}

// The stopped publication is on her list of stopped runs, naming the document
// and its pull request in plain words and the two decisions that answer it,
// with no work item's budgets beside it.
func TestAStoppedDocumentPublicationIsOnTheDocketNamingItsDocumentAndPullRequest(t *testing.T) {
	_, docketer, _, _, last := stoppedPublication(t)
	built, err := docketer.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(built.Entries) != 1 || built.Entries[0].RunID != last.RunID || built.Entries[0].Class != triage.ClassStoppedRun {
		t.Fatalf("docket = %+v, want the stopped publication as a stopped run", built.Entries)
	}
	entry := built.Entries[0]
	if entry.Document == nil || entry.Document.ID != "stuck-design" || entry.Document.PullRequest != 1173 {
		t.Fatalf("document = %+v, want the design and its pull request", entry.Document)
	}
	rendered := entry.Render()
	for _, want := range []string{`"Retried design"`, "Its pull request #1173", "newest stopped copy", `"retry"`, `"retire"`, "no work item"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Triage counters") {
		t.Fatalf("a document publication has no item budget to report:\n%s", rendered)
	}
}

// Retrying lands the same confirmed text through a fresh run the scheduler
// starts, which records why it exists, and closes the stopped run's request.
func TestRetryingAStoppedDocumentPublicationLandsTheSameTextThroughAFreshRun(t *testing.T) {
	pipeline, _, forge, runs, last := stoppedPublication(t)
	said, err := decider(runs, forge).RetryDocument(context.Background(), last.RunID, developmentManagersDecision)
	if err != nil {
		t.Fatal(err)
	}
	stopped := runByID(t, runs, last.RunID)
	if stopped.DocumentDecision == nil || stopped.DocumentDecision.Decision != runstate.DocumentDecisionRetry || !stopped.DocumentDecision.PullRequestClosed || stopped.PullRequest.State != "CLOSED" {
		t.Fatalf("stopped run's decision = %+v, request %+v; want the retry recorded and its request closed", stopped.DocumentDecision, stopped.PullRequest)
	}
	closed := forge.ClosedRequests()
	if len(closed) != 1 || closed[0].Number != 1173 || !strings.Contains(closed[0].Comment, "newest copy") || !strings.Contains(closed[0].Comment, stopped.DocumentDecision.RetriedAs) {
		t.Fatalf("closed = %+v, want #1173 closed naming the reason and the fresh run", closed)
	}
	waits, err := runs.DocumentWaits()
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0].RunID() != stopped.DocumentDecision.RetriedAs || waits[0].Document.Retried == nil || waits[0].Document.Retried.FromRun != last.RunID || waits[0].Document.Candidate.Content != last.Document.Candidate.Content {
		t.Fatalf("waits = %+v, want the same text waiting as a fresh handoff", waits)
	}
	if !strings.Contains(said, stopped.DocumentDecision.RetriedAs) {
		t.Fatalf("she was told %q, want the fresh run named", said)
	}
	// Recording it again names the same handoff and starts nothing more.
	if again, err := decider(runs, forge).RetryDocument(context.Background(), last.RunID, developmentManagersDecision); err != nil || !strings.Contains(again, "already decided") {
		t.Fatalf("second retry = %q, %v; want it answered as already decided", again, err)
	}
	outcome, err := pipeline.PublishWaitingDocument(context.Background(), waits[0].Document)
	if err != nil {
		t.Fatal(err)
	}
	fresh := runByID(t, runs, outcome.RunID)
	if fresh.RunID != stopped.DocumentDecision.RetriedAs || fresh.Status != runstate.StatusSucceeded || fresh.Integration == nil {
		t.Fatalf("fresh run %s = %s, want the retried text landed by run %s", fresh.RunID, fresh.Status, stopped.DocumentDecision.RetriedAs)
	}
	if !strings.Contains(fresh.Selection.Reason, "It retries run "+last.RunID) || !strings.Contains(fresh.Selection.Reason, "newest copy") {
		t.Fatalf("selection = %q, want the decision it was started on", fresh.Selection.Reason)
	}
}

// Retiring closes the request with the reason and lands nothing; a request the
// forge would not close records nothing, so she is asked again.
func TestRetiringAStoppedDocumentPublicationClosesItsPullRequestAndRecordsWhy(t *testing.T) {
	_, _, forge, runs, last := stoppedPublication(t)
	forge.CloseErr = errors.New("HTTP 502")
	if _, err := decider(runs, forge).RetireDocument(context.Background(), last.RunID, developmentManagersDecision); err == nil || !strings.Contains(err.Error(), "not recorded") {
		t.Fatalf("retire with the forge failing = %v, want it refused with nothing recorded", err)
	}
	if runByID(t, runs, last.RunID).DocumentDecision != nil {
		t.Fatal("a retirement whose request stayed open was recorded")
	}
	forge.CloseErr = nil
	said, err := decider(runs, forge).RetireDocument(context.Background(), last.RunID, developmentManagersDecision)
	if err != nil {
		t.Fatal(err)
	}
	stopped := runByID(t, runs, last.RunID)
	if stopped.DocumentDecision == nil || stopped.DocumentDecision.Decision != runstate.DocumentDecisionRetire || stopped.DocumentDecision.RetriedAs != "" || !strings.Contains(stopped.DocumentDecision.Reason, "newest copy") {
		t.Fatalf("decision = %+v, want the retirement and its reason", stopped.DocumentDecision)
	}
	closed := forge.ClosedRequests()
	if len(closed) != 1 || closed[0].Number != 1173 || !strings.Contains(closed[0].Comment, "retired") || !strings.Contains(closed[0].Comment, "newest copy") {
		t.Fatalf("closed = %+v, want #1173 closed with the reason", closed)
	}
	if waits, err := runs.DocumentWaits(); err != nil || len(waits) != 0 {
		t.Fatalf("waits = %+v, %v; a retirement starts nothing", waits, err)
	}
	if !strings.Contains(said, "#1173 is closed") {
		t.Fatalf("she was told %q", said)
	}
	if _, err := decider(runs, forge).RetryDocument(context.Background(), last.RunID, developmentManagersDecision); err == nil {
		t.Fatal("a retired publication was retried")
	}
}

// Of a document's open publications, an older copy is hers at once, and the
// conversation's latest word on it is hers once it has stood stopped for the
// stuck-merge age, or at once where another copy has landed since.
func TestWhichStoppedDocumentPublicationsAreTheDevelopmentManagers(t *testing.T) {
	_, _, _, _, last := stoppedPublication(t)
	base := last
	base.DocumentRetry = nil
	base.Blocker = ""
	at := func(document runstate.DocumentPublication, started time.Time) runstate.State {
		state := base
		d := document
		state.Document = &d
		state.RunID = d.RunIDFor(0)
		state.StartedAt = started
		completed := started.Add(time.Minute)
		state.CompletedAt = &completed
		return state
	}
	now := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	older := *base.Document
	older.WriteID = "document-700.1"
	newer := *base.Document
	newer.WriteID, newer.Turn = "document-900.1", older.Turn+1
	olderRun, newerRun := at(older, now.Add(-3*time.Hour)), at(newer, now.Add(-10*time.Minute))
	copies := documentCopiesOf([]runstate.State{olderRun, newerRun})
	age := 2 * time.Hour
	if !copies.docketable(olderRun, now, age) {
		t.Fatal("an older copy of a document with its request open is not put to her")
	}
	if copies.docketable(newerRun, now, age) {
		t.Fatal("the conversation's latest copy was put to her before the stuck-merge age")
	}
	if !copies.docketable(newerRun, now.Add(3*time.Hour), age) {
		t.Fatal("the latest copy was not put to her once it had stood stopped for the stuck-merge age")
	}
	if document := copies.document(olderRun); !strings.Contains(document.NewerCopy, newerRun.RunID) {
		t.Fatalf("older copy's document = %+v, want the newer copy named", document)
	}
	landed := at(newer, now.Add(-5*time.Minute))
	landed.RunID = newer.RunIDFor(1)
	landed.Status = runstate.StatusSucceeded
	landed.Integration = &runstate.Integration{TargetBranch: "main"}
	withLanding := documentCopiesOf([]runstate.State{olderRun, newerRun, landed})
	if !withLanding.docketable(newerRun, now, age) || !strings.Contains(withLanding.document(olderRun).LandedBy, landed.RunID) {
		t.Fatal("a copy of a document that has landed since was not put to her as moot")
	}
	decided := olderRun
	decided.DocumentDecision = &runstate.DocumentDecision{Decision: runstate.DocumentDecisionRetire}
	if copies.docketable(decided, now, age) {
		t.Fatal("a decided publication was put to her again")
	}
}

// A retried publication has no conversation to offer it again, so a run of it
// that stops without judging the document leaves its next attempt where the
// scheduler starts it.
func TestARetriedDocumentPublicationThatStopsAgainIsContinuedByTheScheduler(t *testing.T) {
	pipeline, _, forge, runs, last := stoppedPublication(t)
	if _, err := decider(runs, forge).RetryDocument(context.Background(), last.RunID, developmentManagersDecision); err != nil {
		t.Fatal(err)
	}
	waits, err := runs.DocumentWaits()
	if err != nil || len(waits) != 1 {
		t.Fatalf("waits = %+v, %v", waits, err)
	}
	pipeline.Worktrees.(*forgeFailing).failures = 1
	if _, err := pipeline.PublishWaitingDocument(context.Background(), waits[0].Document); err != nil {
		t.Fatal(err)
	}
	next, err := runs.DocumentWaits()
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Attempt != 1 || next[0].Document.WriteID != waits[0].Document.WriteID {
		t.Fatalf("waits after the stop = %+v, want the retried handoff's second attempt waiting", next)
	}
}
