package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// owe is a correction owed for the last check, naming the piece where one is.
func (b *stateBench) owe(missing, piece string) {
	last := &b.entry.Checks[len(b.entry.Checks)-1]
	last.Owed = &epicprogress.Correction{Missing: missing, At: last.At, Piece: piece}
}

// Each step of a correction names who owes it, and the check is made again once
// the correcting piece that joined has landed.
func TestEachStepOfACorrectionNamesWhoOwesIt(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundNotMet, hours(2))
	b.owe("the second thing: nothing reads it", "")
	b.wants(EpicWorking, "Waiting on the development manager: write the piece that corrects the check", "naming that check: the second thing: nothing reads it")
	b.owe("the second thing: nothing reads it", statePiece)
	b.wants(EpicWorking, "Waiting on the reviewer: judge whether the correcting piece "+statePiece+" joins the epic")

	// A piece judged outside is a question about scope, named from its check.
	from := epicprogress.FromCheck(b.entry.Checks[0].ID)
	outside := *b
	outside.entry.Pieces = []epicprogress.Piece{{WorkItemID: statePiece, From: from, Judgment: "j", At: hours(3),
		Outcome: &epicprogress.Outcome{Answer: epicprogress.AnswerOutside, Why: "it asks for more", At: hours(3)}}}
	outside.wants(EpicWorking, "Waiting on the Lead Product Manager: Title "+statePiece+" ("+statePiece+"), made from "+from+" of the whole epic, is a question about scope")

	b.join(statePiece, from, hours(3))
	b.ready = []string{statePiece}
	b.wants(EpicWorking)
	b.ready = nil
	b.landed(statePiece)
	b.wants(EpicWorking, "Waiting on the reviewer: every member is done and the check of the whole epic is owed")
	b.check(epicprogress.FoundAllMet, hours(4))
	b.wants(EpicIntegrated)
}

// An answer of cannot be told waits on the operator where the description reserves
// the step for a person. Otherwise it is the Lead Product Manager's step until her
// handling of the question the harness files her is recorded: a handling naming the
// operator's reason waits on him, and any other owes the development manager's piece.
func TestCannotBeToldWaitsOnTheOperatorOrForEvidence(t *testing.T) {
	t.Parallel()
	const question = "report-0123456789abcdef0123456789abcdef"
	person := newStateBench(t)
	person.landed(stateOne, stateTwo)
	person.check(epicprogress.FoundPersonsStep, hours(2))
	person.wants(EpicWaitsOnOperator, "the epic's description reserves a step for a person")

	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundCannotTell, hours(2))
	b.wants(EpicWorking, "Waiting on the Lead Product Manager: say what evidence the check of the whole epic lacked", "the harness is filing her the question")
	last := &b.entry.Checks[0]
	last.Asked = &epicprogress.Asked{Report: question, At: hours(3)}
	b.wants(EpicWorking, "Waiting on the Lead Product Manager: say what evidence the check of the whole epic lacked, for the development manager to have written, by handling report "+question)

	operator := *b
	operator.entry.Checks = []epicprogress.Check{*last}
	answered := hours(4)
	operator.entry.Checks[0].Asked = &epicprogress.Asked{Report: question, At: hours(3), Answered: &answered, Operator: ownership.OperatorHumanGate, Says: "only he signs the release"}
	operator.wants(EpicWaitsOnOperator, "and the answer to "+question+" says only the operator can act (human-gate): only he signs the release")

	last.Asked = &epicprogress.Asked{Report: question, At: hours(3), Answered: &answered}
	b.owe("a test that runs the new command", "")
	b.wants(EpicWorking, "Waiting on the development manager: write the piece that corrects the check", "a test that runs the new command")
	b.owe("a test that runs the new command", statePiece)
	b.wants(EpicWorking, "Waiting on the reviewer: judge whether the correcting piece "+statePiece)
}

// answered is a check whose answers are given, what it found worked out from them.
func (b *stateBench) answered(at time.Time, findings ...epicprogress.Finding) {
	b.entry.Checks = append(b.entry.Checks, epicprogress.Check{ID: "check-" + at.String(), Commit: "abc", Found: epicprogress.FoundOf(findings), At: at, Findings: findings})
}

// A check that could not tell owes the operator each step the description
// reserves for a person, named in plain words, and the Lead Product Manager the
// evidence it lacked for anything else; where it owes both, both are shown, at
// every step of the question and its correction, and neither hides the other.
func TestACheckOwingTheOperatorsStepAndAQuestionShowsBoth(t *testing.T) {
	t.Parallel()
	const question = "report-0123456789abcdef0123456789abcdef"
	signed := epicprogress.Finding{Asks: "the release is signed", Answer: epicprogress.CannotBeTold, Says: "the description says only the operator signs it", ForAPerson: true}
	unread := epicprogress.Finding{Asks: "the new command works", Answer: epicprogress.CannotBeTold, Says: "no test runs it"}
	met := epicprogress.Finding{Asks: "the old command still works", Answer: epicprogress.Met, Says: "its tests pass"}
	step := "Waiting on the operator: the epic's description reserves a step for a person, and it has not been taken: the release is signed (the description says only the operator signs it)"
	evidence := "Waiting on the Lead Product Manager: say what evidence the check of the whole epic lacked"

	only := newStateBench(t)
	only.landed(stateOne, stateTwo)
	only.answered(hours(2), met, signed)
	if standing := only.wants(EpicWaitsOnOperator, step); strings.Contains(standing.render(), "Lead Product Manager") {
		t.Errorf("a check owing only the operator's step asks the Lead Product Manager for evidence:\n%s", standing.render())
	}

	asks := newStateBench(t)
	asks.landed(stateOne, stateTwo)
	asks.answered(hours(2), met, unread)
	if standing := asks.wants(EpicWorking, evidence); strings.Contains(standing.render(), "reserves a step for a person") {
		t.Errorf("a check owing only a question names a step for a person:\n%s", standing.render())
	}

	both := newStateBench(t)
	both.landed(stateOne, stateTwo)
	both.answered(hours(2), signed, met, unread)
	if both.entry.Checks[0].Found != epicprogress.FoundCannotTell {
		t.Fatalf("the check found %q", both.entry.Checks[0].Found)
	}
	both.wants(EpicWorking, step, evidence, "the harness is filing her the question")
	last := &both.entry.Checks[0]
	last.Asked = &epicprogress.Asked{Report: question, At: hours(3)}
	both.wants(EpicWorking, step, evidence+", for the development manager to have written, by handling report "+question)

	operator := *both
	operator.entry.Checks = []epicprogress.Check{*last}
	answeredAt := hours(4)
	operator.entry.Checks[0].Asked = &epicprogress.Asked{Report: question, At: hours(3), Answered: &answeredAt, Operator: ownership.OperatorCredential, Says: "only he holds the signing key"}
	operator.wants(EpicWaitsOnOperator, step, "says only the operator can act (credential): only he holds the signing key")

	last.Asked = &epicprogress.Asked{Report: question, At: hours(3), Answered: &answeredAt}
	both.owe("a test that runs the new command", "")
	both.wants(EpicWorking, step, "Waiting on the development manager: write the piece that corrects the check", "a test that runs the new command")
	both.owe("a test that runs the new command", statePiece)
	both.wants(EpicWorking, step, "Waiting on the reviewer: judge whether the correcting piece "+statePiece)
}
