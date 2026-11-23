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
			t.Fatalf("%s: the new revision waits behind the one handed back", name)
		}
		if !strings.Contains(told, "document-1.1's run run-1 ended without landing it, so that revision no longer waits and this one opens a fresh run") {
			t.Fatalf("%s: the role was told %q", name, told)
		}
		if returns := session.state.DocumentReturns["rules"]; (returns == 1) != standing.Delivery.Judged {
			t.Fatalf("%s: returns counted = %d, want one only for a run that judged it", name, returns)
		}
		if len(session.state.HandedBack) != 0 {
			t.Fatalf("%s: the entry handed back outlived the new revision of its document: %#v", name, session.state.HandedBack)
		}
		if len(publisher.withdrawn) != 0 {
			t.Fatalf("%s: withdrawn = %v", name, publisher.withdrawn)
		}
	}
}

// A run's account of why it handed a document back can run past the bound its
// reason is kept to. Cutting it to the bound and then adding a mark past it made
// the reason longer than its limit, so recording the turn was refused, and the
// architect's recurring pass, offered the same record each time, failed each
// time. The turn is recorded with the reason cut to fit, saying it was cut, how
// long the whole was, and which run's record keeps all of it.
func TestATurnIsRecordedWhenAHandedBackReasonIsOverItsLimit(t *testing.T) {
	t.Parallel()

	// Words with no gap at the bound, as a check's output often has, which is
	// what left the old cut over its limit.
	account := "Document document-1.1 did not land in run run-1. " + strings.Repeat("checkoutput", runstate.MaxHandedBackReasonBytes/10)
	whole := len(strings.Join(strings.Fields(account), " "))
	if whole <= runstate.MaxHandedBackReasonBytes {
		t.Fatalf("the account is %d bytes, want it over the %d-byte limit", whole, runstate.MaxHandedBackReasonBytes)
	}
	refused := confirmedRecord(t, "rules", "document-1.1", "# Rules", "")
	session, _ := standingSession(t, map[string]runstate.DocumentStanding{"document-1.1": handedBack("run-1", account, true)}, refused)

	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatalf("PublishDocuments() = %v, want the turn recorded", err)
	}
	recorded, err := session.options.Store.Load(session.options.identity())
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.HandedBack) != 1 {
		t.Fatalf("handed back = %#v, want the one revision kept", recorded.HandedBack)
	}
	reason := recorded.HandedBack[0].Reason
	if len(reason) > runstate.MaxHandedBackReasonBytes {
		t.Fatalf("the kept reason is %d bytes, limit is %d", len(reason), runstate.MaxHandedBackReasonBytes)
	}
	said := fmt.Sprintf("[cut here: the whole was %d bytes, and the record of run run-1 keeps all of it]", whole)
	if !strings.HasPrefix(reason, "Document document-1.1 did not land in run run-1. checkoutput") || !strings.HasSuffix(reason, said) {
		t.Fatalf("the kept reason does not say it was cut and where the rest is:\n%s", reason)
	}
}

// The record's last fit catches a reason written past its limit by any route,
// so saving the record shortens it rather than refusing the turn.
func TestRecordingFitsAStoredReasonThatIsOverItsLimit(t *testing.T) {
	t.Parallel()

	session, _ := standingSession(t, nil)
	long := strings.Repeat("x", runstate.MaxHandedBackReasonBytes+2)
	session.state.HandedBack = []runstate.HandedBackDocument{{ID: "document-0.1", Artifact: "rules-z", RunID: "run-0", Reason: long, At: session.state.StartedAt}}
	session.state.RefusedBlock = &runstate.TrackerRefusal{Turn: 1, Problem: strings.Repeat("y", runstate.MaxTrackerRefusalProblemBytes+5), RefusedAt: session.state.StartedAt}

	if err := session.record(); err != nil {
		t.Fatalf("record() = %v, want the turn recorded", err)
	}
	recorded, err := session.options.Store.Load(session.options.identity())
	if err != nil {
		t.Fatal(err)
	}
	if reason := recorded.HandedBack[0].Reason; len(reason) > runstate.MaxHandedBackReasonBytes || !strings.Contains(reason, fmt.Sprintf("the whole was %d bytes, and the record of run run-0 keeps all of it", len(long))) {
		t.Fatalf("the kept reason is %d bytes and says:\n%s", len(reason), reason)
	}
	if problem := recorded.RefusedBlock.Problem; len(problem) > runstate.MaxTrackerRefusalProblemBytes || !strings.HasSuffix(problem, fmt.Sprintf("[cut here: the whole was %d bytes]", runstate.MaxTrackerRefusalProblemBytes+5)) {
		t.Fatalf("the kept refusal is %d bytes and ends:\n%s", len(problem), problem[max(len(problem)-120, 0):])
	}
}

// A document written while every waiting place is taken is kept, not refused,
// and the role is told it does not need writing again; only documents beyond
// the allowance kept apart for them are refused.
func TestTheNinthWaitingDocumentIsKeptUntilThereIsRoom(t *testing.T) {
	t.Parallel()

	var records []*writeRecord
	standings := map[string]runstate.DocumentStanding{}
	for index := 1; index <= runstate.MaxPendingWrites; index++ {
		writeID := fmt.Sprintf("document-%d.1", index)
		records = append(records, confirmedRecord(t, fmt.Sprintf("rules-%d", index), writeID, "# Rules", ""))
		standings[writeID] = running(fmt.Sprintf("run-%d", index))
	}
	session, _ := standingSession(t, standings, records...)

	ninth := landedWrite("rules-ninth", "# Rules")
	if err := session.refuseWrites(context.Background(), []artifact.Write{ninth}); err != nil {
		t.Fatalf("refuseWrites(ninth) = %v, want it kept until there is room", err)
	}
	var over []artifact.Write
	for index := 0; index <= runstate.MaxHeldWrites; index++ {
		over = append(over, landedWrite(fmt.Sprintf("rules-over-%d", index), "# Rules"))
	}
	err := session.refuseWrites(context.Background(), over)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("limit of %d per role", runstate.MaxPendingWrites)) || !strings.Contains(err.Error(), "full too") || !strings.Contains(err.Error(), `"withdraw"`) {
		t.Fatalf("refuseWrites(%d more) = %v, want it refused with the limits and what to do", len(over), err)
	}
	// A new revision of a document already waiting takes no new place.
	if err := session.refuseWrites(context.Background(), []artifact.Write{landedWrite("rules-1", "# Rules, revised")}); err != nil {
		t.Fatalf("a new revision of a waiting document was refused: %v", err)
	}

	pending, err := session.recordWritesWithoutToolAudit([]artifact.Write{ninth})
	if err != nil || len(pending) != 1 || !pending[0].WaitsForRoom {
		t.Fatalf("recorded = %+v, %v, want the ninth kept until there is room", pending, err)
	}
	kept := session.writes[len(session.writes)-1]
	if told := session.state.PendingTrackerResults; !strings.Contains(told, "so the harness keeps it and sends it at the first message after one of them is settled; it does not need writing again") {
		t.Fatalf("the role was told %q", told)
	}
	for _, write := range session.Writes() {
		if write.ID == kept.pending.ID {
			t.Fatal("a document kept for room is put to the operator")
		}
	}
	recorded, err := session.options.Store.Load(session.options.identity())
	if err != nil {
		t.Fatal(err)
	}
	if last := recorded.PendingWrites[len(recorded.PendingWrites)-1]; last.ID != kept.pending.ID || !last.WaitsForRoom || !last.Held() {
		t.Fatalf("the record keeps %+v, want the kept document counted apart", last)
	}

	// While every place is still taken it stays kept; once one is settled it
	// takes that place.
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !kept.keptForRoom() {
		t.Fatal("the kept document was let in while every place was taken")
	}
	records[0].decided = true
	if err := session.PublishDocuments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kept.keptForRoom() || kept.decided {
		t.Fatalf("kept = %+v, want it let in once a place came free", kept.pending)
	}
	if told := session.state.PendingTrackerResults; !strings.Contains(told, "One of them is settled, so it now takes that place") {
		t.Fatalf("the role was told %q", told)
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
		t.Fatalf("decided = %v, %v, %v; want the unstarted ones and those handed back withdrawn and the running one kept", unstarted.decided, started.decided, back.decided)
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

// Waiting documents are also bounded by the bytes the conversation's record
// stores for them, so a few documents of the largest size cannot make the record
// too big to save: the one that would take it past the room is refused with the
// reason, while a document of ordinary size still has its place.
func TestADocumentThatWouldOverfillTheRecordIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 100<<10)
	var records []*writeRecord
	standings := map[string]runstate.DocumentStanding{}
	for index := 1; index <= 2; index++ {
		writeID := fmt.Sprintf("document-%d.1", index)
		records = append(records, confirmedRecord(t, fmt.Sprintf("design-%d", index), writeID, "# Design\n\n"+big, ""))
		standings[writeID] = running(fmt.Sprintf("run-%d", index))
	}
	session, _ := standingSession(t, standings, records...)

	err := session.refuseWrites(context.Background(), []artifact.Write{landedWrite("design-3", "# Design\n\n"+big)})
	if err == nil || !strings.Contains(err.Error(), "bytes of the") || !strings.Contains(err.Error(), `"withdraw"`) {
		t.Fatalf("refuseWrites(a third large document) = %v, want it refused with the room named and what to do", err)
	}
	if err := session.refuseWrites(context.Background(), []artifact.Write{landedWrite("rules", "# Rules")}); err != nil {
		t.Fatalf("a small document was refused: %v", err)
	}
}

// A document whose run passed it and whose change waits in the merge queue is
// listed as in the merge queue, with where it waits, and not as handed back or
// merely running.
func TestTheListingSaysADocumentWaitingInTheMergeQueueIsThere(t *testing.T) {
	t.Parallel()

	queued := confirmedRecord(t, "rules-q", "document-7.1", "# Rules", "")
	standing := running("run-7")
	standing.Delivery.Queued = "it passed its checks and the independent review and waits in the merge queue for main as entry 170 (mqe-1) on pull request #1899, admitted Nov 15 01:00 PST; nothing has landed it or handed it back yet"
	session, _ := standingSession(t, map[string]runstate.DocumentStanding{"document-7.1": standing}, queued)

	listed := session.renderWaitingDocuments()
	want := "In the merge queue: run run-7 ended once " + standing.Delivery.Queued + ". It is not handed back."
	if !strings.Contains(listed, want) || strings.Contains(listed, "Running: run run-7") {
		t.Fatalf("the listing does not say the document waits in the merge queue:\n%s", listed)
	}
}

// Secret masking runs over a role's reply before the harness reads it, so a
// value in a document it took for a credential arrives as the marker. The role
// is told which lines carry it rather than the document being published with
// nobody told; a document with no marker in it is answered as before.
func TestARoleIsToldWhichLinesOfItsDocumentMaskingChanged(t *testing.T) {
	t.Parallel()

	session, _ := standingSession(t, nil)
	clean := writeAgain(t, session, landedWrite("rules", "# Rules\n\ncredential: keychain"))
	if strings.Contains(clean, "secret masking") {
		t.Fatalf("a document with no marker in it was said to be masked:\n%s", clean)
	}

	told := writeAgain(t, session, landedWrite("rules-masked", "# Rules\n\nsetting: kept\ntoken: [REDACTED]"))
	recorded := latestRecord(session)
	for _, want := range []string{
		"Document " + recorded.pending.ID + " carries [REDACTED] where secret masking removed a value",
		"- body line 4: token: [REDACTED]\n",
		"revise the document",
	} {
		if !strings.Contains(told, want) {
			t.Errorf("the role was not told %q:\n%s", want, told)
		}
	}
	if strings.Contains(told, "body line 3") {
		t.Errorf("a line with no marker was named:\n%s", told)
	}
}
