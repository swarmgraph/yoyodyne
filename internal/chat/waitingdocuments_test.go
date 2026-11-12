package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// standingPublisher answers where each document's run stands from what a test
// set, by write, and records what it was asked to withdraw and publish. A
// document it was given no standing for has not started.
type standingPublisher struct {
	standings map[string]runstate.DocumentStanding
	withdrawn []string
	offered   []string
}

// PublishesDocuments is false, so a new revision is never confirmed by policy
// here: what these tests follow is where it waits.
func (p *standingPublisher) PublishesDocuments() bool { return false }

func (p *standingPublisher) PublishDocument(_ context.Context, document runstate.DocumentPublication) (runstate.DocumentDelivery, error) {
	p.offered = append(p.offered, document.WriteID)
	standing := p.standings[document.WriteID]
	if !standing.Started {
		return runstate.DocumentDelivery{RunID: document.RunID()}, nil
	}
	return standing.Delivery, nil
}

func (p *standingPublisher) DocumentStanding(document runstate.DocumentPublication) (runstate.DocumentStanding, error) {
	return p.standings[document.WriteID], nil
}

func (p *standingPublisher) WithdrawDocument(document runstate.DocumentPublication) error {
	standing := p.standings[document.WriteID]
	if standing.Running() {
		return fmt.Errorf("run %s has started publishing it and has not ended", standing.RunID)
	}
	p.withdrawn = append(p.withdrawn, document.WriteID)
	return nil
}

func running(run string) runstate.DocumentStanding {
	return runstate.DocumentStanding{RunID: run, Started: true, Delivery: runstate.DocumentDelivery{RunID: run}}
}

func handedBack(run, detail string, judged bool) runstate.DocumentStanding {
	return runstate.DocumentStanding{RunID: run, Started: true, Delivery: runstate.DocumentDelivery{RunID: run, Settled: true, Judged: judged, Detail: detail}}
}

// standingSession is a conversation holding the given waiting documents, with
// a publisher that reads their runs' standings.
func standingSession(t *testing.T, standings map[string]runstate.DocumentStanding, records ...*writeRecord) (*Session, *standingPublisher) {
	t.Helper()
	session, _ := heldSession(t, nil, records...)
	publisher := &standingPublisher{standings: standings}
	session.options.DocumentPublisher = publisher
	return session, publisher
}

// writeAgain puts a new revision of a document through the gate and records it,
// as Send does, and returns what the role was told.
func writeAgain(t *testing.T, session *Session, write artifact.Write) string {
	t.Helper()
	if err := session.refuseWrites(context.Background(), []artifact.Write{write}); err != nil {
		t.Fatalf("the revision of %s was refused: %v", write.ID, err)
	}
	if _, err := session.recordWritesWithoutToolAudit([]artifact.Write{write}); err != nil {
		t.Fatal(err)
	}
	return session.state.PendingTrackerResults
}

func latestRecord(session *Session) *writeRecord { return session.writes[len(session.writes)-1] }

func TestANewRevisionReplacesAConfirmedOneWhoseRunHasNotStarted(t *testing.T) {
	t.Parallel()

	waiting := confirmedRecord(t, "rules", "document-1.1", "# Rules", "")
	session, publisher := standingSession(t, nil, waiting)
	told := writeAgain(t, session, landedWrite("rules", "# Rules, revised"))

	if !waiting.decided || len(publisher.withdrawn) != 1 || publisher.withdrawn[0] != "document-1.1" {
		t.Fatalf("decided = %v, withdrawn = %v; want the unstarted revision replaced and taken off the wait", waiting.decided, publisher.withdrawn)
	}
	fresh := latestRecord(session)
	if !strings.Contains(told, "Document "+fresh.pending.ID+" (rules") || !strings.Contains(told, "it replaces document-1.1, whose run had not started") {
		t.Fatalf("the role was told %q", told)
	}
	if events := landedEvents(t, session, execution.EventDocumentReplaced); len(events) != 1 {
		t.Fatalf("replaced events = %#v", events)
	}
}

func TestANewRevisionWaitsBehindOneWhoseRunHasStarted(t *testing.T) {
	t.Parallel()

	started := confirmedRecord(t, "rules", "document-1.1", "# Rules", "")
	standings := map[string]runstate.DocumentStanding{"document-1.1": running("run-1")}
	session, publisher := standingSession(t, standings, started)
	told := writeAgain(t, session, landedWrite("rules", "# Rules, revised"))

	fresh := latestRecord(session)
	if started.decided || fresh.decided || len(publisher.withdrawn) != 0 {
		t.Fatalf("started decided = %v, new decided = %v, withdrawn = %v; want both kept", started.decided, fresh.decided, publisher.withdrawn)
	}
	if !strings.Contains(told, "its earlier revision document-1.1 is being published in run run-1, and this one is sent once that run ends") {
		t.Fatalf("the role was told %q", told)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.pending.Publication != nil || len(publisher.offered) != 1 || publisher.offered[0] != "document-1.1" {
		t.Fatalf("offered = %v, new publication = %v; want only the started run offered", publisher.offered, fresh.pending.Publication)
	}
	// Once the run ahead of it ends, the new revision is no longer behind it.
	publisher.standings["document-1.1"] = runstate.DocumentStanding{RunID: "run-1", Started: true, Delivery: runstate.DocumentDelivery{RunID: "run-1", Settled: true, Landed: true, Passed: true, Detail: "landed\n"}}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !started.decided {
		t.Fatal("the landed revision was not settled")
	}
	if behind, _ := session.waitingBehind(fresh); behind != nil {
		t.Fatal("the new revision still waits behind a run that ended")
	}
}

// The architect's corrected revision once waited behind the copy the reviewer
// refused, until a person retired the refused run; and a publishing run that
// ended in clean-up without landing left its document waiting with nothing
// able to clear it. Either way the run has ended, so the author's next revision
// opens a fresh run.
func TestAHandedBackRevisionIsFollowedByANewRevisionThatOpensAFreshRun(t *testing.T) {
	t.Parallel()

	for name, standing := range map[string]runstate.DocumentStanding{
		"refused by the review":             handedBack("run-1", "Document document-1.1 did not land in run run-1. Review finding: the goal is not named.\n", true),
		"ended without reaching the target": handedBack("run-1", "Document document-1.1 did not land in run run-1. It ended at its clean-up step.\n", false),
	} {
		refused := confirmedRecord(t, "rules", "document-1.1", "# Rules", "")
		session, publisher := standingSession(t, map[string]runstate.DocumentStanding{"document-1.1": standing}, refused)
		told := writeAgain(t, session, landedWrite("rules", "# Rules, corrected"))

		fresh := latestRecord(session)
		if !refused.decided || fresh.decided {
			t.Fatalf("%s: handed back decided = %v, new decided = %v", name, refused.decided, fresh.decided)
		}
		if behind, _ := session.waitingBehind(fresh); behind != nil {
			t.Fatalf("%s: the new revision waits behind the handed-back one", name)
		}
		if !strings.Contains(told, "document-1.1's run run-1 ended without landing it, so that revision no longer waits and this one opens a fresh run") {
			t.Fatalf("%s: the role was told %q", name, told)
		}
		if returns := session.state.DocumentReturns["rules"]; (returns == 1) != standing.Delivery.Judged {
			t.Fatalf("%s: returns counted = %d, want one only for a run that judged it", name, returns)
		}
		if len(session.state.HandedBack) != 0 {
			t.Fatalf("%s: the handed-back entry outlived the new revision of its document: %#v", name, session.state.HandedBack)
		}
		if len(publisher.withdrawn) != 0 {
			t.Fatalf("%s: withdrawn = %v", name, publisher.withdrawn)
		}
	}
}

func TestTheNinthWaitingDocumentIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	var records []*writeRecord
	standings := map[string]runstate.DocumentStanding{}
	for index := 1; index <= runstate.MaxPendingWrites; index++ {
		writeID := fmt.Sprintf("document-%d.1", index)
		records = append(records, confirmedRecord(t, fmt.Sprintf("rules-%d", index), writeID, "# Rules", ""))
		standings[writeID] = running(fmt.Sprintf("run-%d", index))
	}
	session, _ := standingSession(t, standings, records...)

	err := session.refuseWrites(context.Background(), []artifact.Write{landedWrite("rules-ninth", "# Rules")})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%d documents would be waiting", runstate.MaxPendingWrites+1)) || !strings.Contains(err.Error(), fmt.Sprintf("limit is %d per role", runstate.MaxPendingWrites)) || !strings.Contains(err.Error(), `"withdraw"`) {
		t.Fatalf("refuseWrites(ninth) = %v, want it refused with the limit and what to do", err)
	}
	// A new revision of a document already waiting takes no new place.
	if err := session.refuseWrites(context.Background(), []artifact.Write{landedWrite("rules-1", "# Rules, revised")}); err != nil {
		t.Fatalf("a new revision of a waiting document was refused: %v", err)
	}
}

func TestARoleWithdrawsItsOwnUnstartedOrHandedBackDocumentAndNothingElse(t *testing.T) {
	t.Parallel()

	unstarted := confirmedRecord(t, "rules-a", "document-1.1", "# Rules", "")
	started := confirmedRecord(t, "rules-b", "document-2.1", "# Rules", "")
	back := confirmedRecord(t, "rules-c", "document-3.1", "# Rules", "")
	session, publisher := standingSession(t, map[string]runstate.DocumentStanding{
		"document-2.1": running("run-2"),
		"document-3.1": handedBack("run-3", "it ended without landing\n", false),
	}, unstarted, started, back)

	answer, err := session.withdrawWrites([]artifact.Withdrawal{
		{Write: "document-1.1", Reason: "superseded by a plan"},
		{Write: "document-2.1", Reason: "changed my mind"},
		{Write: "document-3.1", Reason: "no longer needed"},
		{Write: "document-9.9", Reason: "not mine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- document-1.1: withdrawn. Its run had not started",
		"- document-2.1: refused. Its run run-2 has started and has not ended",
		"- document-3.1: withdrawn. Its run run-3 had ended without landing it",
		"- document-9.9: refused. No document of yours with that name is waiting in this conversation",
	} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer does not say %q:\n%s", want, answer)
		}
	}
	if !unstarted.decided || started.decided || !back.decided {
		t.Fatalf("decided = %v, %v, %v; want the unstarted and handed-back ones withdrawn and the running one kept", unstarted.decided, started.decided, back.decided)
	}
	if len(publisher.withdrawn) != 2 {
		t.Fatalf("taken off the wait = %v", publisher.withdrawn)
	}
	events := landedEvents(t, session, execution.EventDocumentWithdrawn)
	if len(events) != 2 || !strings.Contains(string(events[0].Payload), "superseded by a plan") || !strings.Contains(string(events[0].Payload), string(domain.RoleProductManager)) {
		t.Fatalf("withdrawn events = %#v", events)
	}
}

func TestAWithdrawalOfAnotherRolesDocumentIsRefused(t *testing.T) {
	t.Parallel()

	session, _ := standingSession(t, nil)
	architect := openArchitectSession(t, session)
	theirs := confirmedRecord(t, "rules", "document-1.1", "# Rules", "")
	architect.writes = append(architect.writes, theirs)

	answer, err := session.withdrawWrites([]artifact.Withdrawal{{Write: "document-1.1", Reason: "tidying"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "refused") || theirs.decided {
		t.Fatalf("answer = %q, decided = %v; want another role's document left alone", answer, theirs.decided)
	}
	if listed := session.renderWaitingDocuments(); strings.Contains(listed, "document-1.1") {
		t.Fatalf("the listing shows another role's document:\n%s", listed)
	}
}

// openArchitectSession is the architect's conversation over the same store.
func openArchitectSession(t *testing.T, beside *Session) *Session {
	t.Helper()
	options := beside.options
	options.Role, options.Agent = domain.RoleArchitect, string(domain.RoleArchitect)
	return openTestSession(t, options)
}

func TestARoleThatOwnsNoDocumentsCannotWithdrawOne(t *testing.T) {
	t.Parallel()

	session, _ := standingSession(t, nil)
	session.state.Role = domain.RoleDeveloper
	answer, err := session.withdrawWrites([]artifact.Withdrawal{{Write: "document-1.1", Reason: "tidying"}})
	if err != nil || !strings.Contains(answer, "owns no documents") {
		t.Fatalf("withdrawWrites() = %q, %v", answer, err)
	}
}

func TestTheListingShowsEachOfTheRolesDocumentsAndWhereItStands(t *testing.T) {
	t.Parallel()

	unconfirmed := &writeRecord{pending: PendingWrite{ID: "document-1.1", Turn: 1, Write: landedWrite("rules-a", "# Rules")}, waitsFor: "it waits for the operator's decision"}
	unstarted := confirmedRecord(t, "rules-b", "document-2.1", "# Rules", "")
	started := confirmedRecord(t, "rules-c", "document-3.1", "# Rules", "")
	behind := &writeRecord{pending: PendingWrite{ID: "document-4.1", Turn: 4, Write: landedWrite("rules-c", "# Rules, again")}}
	ended := confirmedRecord(t, "rules-d", "document-5.1", "# Rules", "")
	held := &writeRecord{pending: PendingWrite{ID: "document-6.1", Turn: 6, Write: landedWrite("rules-e", "# Rules")}}
	held.pending.Write.Hold = &artifact.SendingHold{UntilClosed: "yoyodyne-fix", Reason: "waits for the fix"}
	session, _ := standingSession(t, map[string]runstate.DocumentStanding{
		"document-3.1": running("run-3"),
		"document-5.1": handedBack("run-5", "Review finding: the goal is not named.\n", true),
	}, unconfirmed, unstarted, started, behind, ended, held)
	session.state.HandedBack = []runstate.HandedBackDocument{{ID: "document-0.1", Artifact: "rules-z", Turn: 0, RunID: "run-0", Reason: "a check failed"}}

	listed := session.renderWaitingDocuments()
	for _, want := range []string{
		"4 of 8 places for waiting documents are taken",
		"- document-1.1: create rules-a (rules) in docs/product, written at turn 1. Waiting to start: it waits for the operator's decision.",
		"- document-2.1: create rules-b (rules) in docs/product, written at turn 1. Waiting to start: it is confirmed and its run has not started yet.",
		"- document-3.1: create rules-c (rules) in docs/product, written at turn 1. Running: run run-3 is publishing it.",
		"- document-4.1: create rules-c (rules) in docs/product, written at turn 4. Waiting to start: its earlier revision document-3.1 is being published in run " + started.pending.Publication.RunID(),
		"- document-5.1: create rules-d (rules) in docs/product, written at turn 1. Handed back: run run-5 ended without landing it: Review finding: the goal is not named.",
		"- document-6.1: create rules-e (rules) in docs/product, written at turn 6. Waiting to start: you held sending it until yoyodyne-fix closes (waits for the fix).",
		"- document-0.1: rules-z, written at turn 0. Handed back by run run-0: a check failed",
	} {
		if !strings.Contains(listed, want) {
			t.Errorf("the listing does not say %q:\n%s", want, listed)
		}
	}
}

// A role asks for the listing and a withdrawal in its document block, and the
// answer comes back to it in the same message.
func TestAListAndAWithdrawalInTheBlockAreAnsweredToTheRole(t *testing.T) {
	t.Parallel()

	block := "\n```yoyodyne-artifact\n" + `{"withdraw":[{"write":"document-1.1","reason":"replaced by a plan"}],"list":true}` + "\n```\n"
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5", FinalText: documentReply("create", "v2-goals", "goals", "docs/product", "# Goals\\n\\nShip the thing.")},
		{SessionID: "session-1", FinalText: "Taking it back." + block},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options, _ := documentOptions(t, provider)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Write the goals up."); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "Take that back."); err != nil {
		t.Fatal(err)
	}
	if len(session.Writes()) != 0 {
		t.Fatalf("waiting = %#v, want the document withdrawn", session.Writes())
	}
	prompt := provider.requests[len(provider.requests)-1].Prompt
	if !strings.Contains(prompt, "- document-1.1: withdrawn") || !strings.Contains(prompt, "# Your waiting documents") || !strings.Contains(prompt, "None is waiting.") {
		t.Fatalf("the role was not answered in its next turn:\n%s", prompt)
	}
}

func TestEveryWriteIsAnsweredAndAnUnansweredOneIsReported(t *testing.T) {
	t.Parallel()

	session, _ := standingSession(t, nil)
	reports := &fakeReports{}
	session.options.Reports = reports
	told := writeAgain(t, session, landedWrite("rules", "# Rules"))
	recorded := latestRecord(session)
	if recorded.unanswered || !strings.Contains(told, "Document "+recorded.pending.ID+" (rules, create rules (rules) in docs/product) is recorded. It is waiting: ") {
		t.Fatalf("unanswered = %v, told %q", recorded.unanswered, told)
	}
	if saved := session.undecidedWrites(); len(saved) != 1 || saved[0].Unanswered {
		t.Fatalf("saved = %#v, want it saved as answered", saved)
	}

	// A write recorded by a process that stopped before answering it.
	lost := &writeRecord{unanswered: true, pending: PendingWrite{ID: "document-7.1", Turn: 7, ConversationID: session.state.ConversationID, Write: landedWrite("rules-lost", "# Rules")}}
	session.writes = append(session.writes, lost)
	session.state.PendingTrackerResults = ""
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lost.unanswered || !strings.Contains(session.state.PendingTrackerResults, "Document document-7.1 (rules-lost) was recorded at turn 7 and you were not told so; that was the harness's fault, and it is reported.") {
		t.Fatalf("unanswered = %v, told %q", lost.unanswered, session.state.PendingTrackerResults)
	}
	if len(reports.appended) != 1 || reports.appended[0].Role != report.HarnessReporter || !strings.Contains(reports.appended[0].Message, "Harness defect: document document-7.1") {
		t.Fatalf("reports = %#v, want one harness defect", reports.appended)
	}
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reports.appended) != 1 {
		t.Fatalf("the defect was reported %d times, want once", len(reports.appended))
	}
}
