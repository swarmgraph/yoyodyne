package readmodel

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
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
// the step for a person, and otherwise on the Lead Product Manager until the
// evidence is named, then on the development manager's piece giving it.
func TestCannotBeToldWaitsOnTheOperatorOrForEvidence(t *testing.T) {
	t.Parallel()
	person := newStateBench(t)
	person.landed(stateOne, stateTwo)
	person.check(epicprogress.FoundPersonsStep, hours(2))
	person.wants(EpicWaitsOnOperator, "the epic's description reserves a step for a person")

	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.check(epicprogress.FoundCannotTell, hours(2))
	b.wants(EpicWorking, "Waiting on the Lead Product Manager: say what evidence the check of the whole epic lacked")
	b.owe("a test that runs the new command", "")
	b.wants(EpicWorking, "Waiting on the development manager: write the piece that corrects the check", "a test that runs the new command")
	b.owe("a test that runs the new command", statePiece)
	b.wants(EpicWorking, "Waiting on the reviewer: judge whether the correcting piece "+statePiece)
}
