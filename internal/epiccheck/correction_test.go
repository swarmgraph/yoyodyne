package epiccheck

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

const correcting = "yoyodyne-ep.3"

func storeOf(t *testing.T, root string) *epicprogress.Store {
	t.Helper()
	store, err := epicprogress.Open(root, product)
	must(t, err)
	return store
}

func refused(t *testing.T, err error, says string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), says) {
		t.Fatalf("err = %v, want one saying %q", err, says)
	}
}

// checkOnce keeps one check under the identifier with the answers given, reserved
// and answered as the checker does.
func checkOnce(t *testing.T, root string, naming epicprogress.Naming, id string, findings ...epicprogress.Finding) epicprogress.Check {
	t.Helper()
	store := storeOf(t, root)
	_, err := store.CallWhileOpen(naming, id, "the check of the whole epic "+id, func() (string, epicprogress.Looked, error) { return "", epicprogress.NotSent, nil },
		func() (string, error) { return answering(findings...), nil })
	must(t, err)
	check, err := store.Checked(naming, epicprogress.Check{ID: id, Commit: commit, Findings: findings})
	must(t, err)
	return check
}

// An answer of not met owes one correction naming what is missing, which only the
// development manager's piece naming that check can take, once.
func TestNotMetOwesOneCorrectionThatOnePieceTakes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	naming := named(t, root)
	store := storeOf(t, root)
	refused(t, store.MayCorrect(epic, "check-1"), "has no check check-1")
	checkOnce(t, root, naming, "check-1", met, notMet)
	owed := entryOf(t, root, naming).Checks[0].Owed
	if owed == nil || owed.Piece != "" || owed.Noted || !strings.Contains(owed.Missing, "the second thing: nothing reads the new file") {
		t.Fatalf("owed %+v, want one correction naming what is missing and no piece", owed)
	}
	must(t, store.MayCorrect(epic, "check-1"))

	note, err := store.Correcting(epic, "check-1", correcting)
	must(t, err)
	if want := epicprogress.CorrectionNote(epic, entryOf(t, root, naming).Checks[0]); note != want || !strings.Contains(note, commit) || !strings.Contains(note, "check-1") {
		t.Fatalf("note %q, want %q naming the epic, the check and its commit", note, want)
	}
	if got := entryOf(t, root, naming); got.Checks[0].Owed.Piece != correcting || got.Checks[0].Owed.Noted {
		t.Fatalf("owed %+v, want the piece written before its note", got.Checks[0].Owed)
	}
	// Writing the same piece again changes nothing, and a second piece is refused.
	_, err = store.Correcting(epic, "check-1", correcting)
	must(t, err)
	refused(t, store.MayCorrect(epic, "check-1"), "has already been written, as "+correcting)
	_, err = store.Correcting(epic, "check-1", "yoyodyne-ep.4")
	refused(t, err, "has already been written, as "+correcting)
	must(t, store.Noted(epic, "check-1"))
	if !entryOf(t, root, naming).Checks[0].Owed.Noted {
		t.Fatal("the note is not recorded as written")
	}

	// A check that found everything met owes nothing.
	other := t.TempDir()
	checkOnce(t, other, named(t, other), "check-1", met)
	refused(t, storeOf(t, other).MayCorrect(epic, "check-1"), "no correction is owed for the check check-1")
	_, err = storeOf(t, other).Correcting(epic, "check-1", correcting)
	refused(t, err, "no correction is owed")
}

// Two rounds of correction start by themselves; a third answer of not met owes
// nothing, and the Lead Product Manager decides.
func TestAThirdNotMetOwesNoCorrection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	naming := named(t, root)
	for round := 1; round <= 3; round++ {
		checkOnce(t, root, naming, fmt.Sprintf("check-%d", round), notMet)
	}
	checks := entryOf(t, root, naming).Checks
	if checks[0].Owed == nil || checks[1].Owed == nil || checks[2].Owed != nil || entryOf(t, root, naming).Rounds(mustEpic(t, root)) != 3 {
		t.Fatalf("owed %v, %v, %v, want the first two only", checks[0].Owed, checks[1].Owed, checks[2].Owed)
	}
	refused(t, storeOf(t, root).MayCorrect(epic, "check-3"), "no correction is owed for the check check-3")
}

// An answer of cannot be told owes nothing until the Lead Product Manager's
// handling of the one question asked for it is recorded: a handling naming the
// operator's reason owes nothing, and any other owes one correction naming its
// reason. A check that did not answer cannot be told asks nothing.
func TestCannotBeToldOwesACorrectionOnlyForTheAnswerToItsQuestion(t *testing.T) {
	t.Parallel()
	const question, another = "report-0123456789abcdef0123456789abcdef", "report-fedcba9876543210fedcba9876543210"
	answered := namedAt.Add(time.Hour)
	root := t.TempDir()
	naming := named(t, root)
	store := storeOf(t, root)
	checkOnce(t, root, naming, "check-1", met, cannotTell)
	if asking, asks := entryOf(t, root, naming).Asking(); !asks || asking.ID != "check-1" || asking.Owed != nil {
		t.Fatalf("asking %+v, %t; want check-1 asking and owing nothing", asking, asks)
	}
	refused(t, store.MayCorrect(epic, "check-1"), "no correction is owed")
	refused(t, store.EvidenceAnswered(naming, "check-1", question, "a test", answered, ""), "did not ask for its evidence")
	must(t, store.AskEvidence(naming, "check-1", question))
	must(t, store.AskEvidence(naming, "check-1", question))
	refused(t, store.AskEvidence(naming, "check-1", another), "already asked for its evidence, in "+question)
	refused(t, store.MayCorrect(epic, "check-1"), "no correction is owed")
	refused(t, store.EvidenceAnswered(naming, "check-1", another, "a test", answered, ""), "did not ask for its evidence in "+another)
	must(t, store.EvidenceAnswered(naming, "check-1", question, "a test that runs the new command", answered, ""))
	must(t, store.EvidenceAnswered(naming, "check-1", question, "something else", answered.Add(time.Hour), ""))
	check := entryOf(t, root, naming).Checks[0]
	if owed := check.Owed; owed == nil || owed.Missing != "a test that runs the new command" || owed.Answered == nil || !owed.Answered.Equal(answered) || !check.Asked.Answered.Equal(answered) {
		t.Fatalf("check %+v owes %+v, want one correction naming the first answer and when it was recorded", check, check.Owed)
	}
	if _, asks := entryOf(t, root, naming).Asking(); asks {
		t.Fatal("an answered check still asks")
	}
	must(t, store.MayCorrect(epic, "check-1"))
	_, err := store.Correcting(epic, "check-1", correcting)
	must(t, err)

	operator := t.TempDir()
	operatorNaming := named(t, operator)
	checkOnce(t, operator, operatorNaming, "check-1", cannotTell)
	must(t, storeOf(t, operator).AskEvidence(operatorNaming, "check-1", question))
	must(t, storeOf(t, operator).EvidenceAnswered(operatorNaming, "check-1", question, "only he can sign the release key", answered, ownership.OperatorHumanGate))
	if check := entryOf(t, operator, operatorNaming).Checks[0]; check.Owed != nil || check.Asked.Operator != ownership.OperatorHumanGate || check.Asked.Says != "only he can sign the release key" {
		t.Fatalf("check %+v, want it waiting on the operator and owing nothing", check)
	}
	refused(t, storeOf(t, operator).MayCorrect(epic, "check-1"), "no correction is owed")

	person := t.TempDir()
	personNaming := named(t, person)
	checkOnce(t, person, personNaming, "check-1", met, personStep)
	refused(t, storeOf(t, person).AskEvidence(personNaming, "check-1", question), "only for one that could not tell")
	checkOnce(t, person, personNaming, "check-2", notMet)
	refused(t, storeOf(t, person).AskEvidence(personNaming, "check-2", question), "only for one that could not tell")
	if _, asks := entryOf(t, person, personNaming).Asking(); asks {
		t.Fatal("a check that did not answer cannot be told asks")
	}
}

// A piece the owed correction names is asked about and joins from the check, and
// only once a judgment lets it; one it does not name cannot be asked about from a
// check at all.
func TestACorrectingPieceJoinsOnlyByItsJudgment(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	naming := named(t, root)
	store := storeOf(t, root)
	checkOnce(t, root, naming, "check-1", notMet)
	refused(t, store.Ask(naming, epicprogress.Piece{WorkItemID: correcting, From: epicprogress.FromCheck("check-1"), Judgment: "judgment-1"}), "is not in epic")
	_, err := store.Correcting(epic, "check-1", correcting)
	must(t, err)
	must(t, store.Ask(naming, epicprogress.Piece{WorkItemID: correcting, From: "whatever its notes say", Judgment: "judgment-1"}))
	entry := entryOf(t, root, naming)
	if asked, _ := entry.Asked(correcting); asked.From != epicprogress.FromCheck("check-1") || entry.Has(correcting) {
		t.Fatalf("asked %+v, joined %v, want it asked about from the check and not joined", asked, entry.Joined)
	}
	must(t, store.Judged(naming, correcting, epicprogress.Outcome{Answer: epicprogress.AnswerInside, Why: "it asks only for the second thing", Session: "session-judge"}))
	entry = entryOf(t, root, naming)
	if !entry.Has(correcting) || entry.Joined[0].From != epicprogress.FromCheck("check-1") || entry.Joined[0].Session != "session-judge" {
		t.Fatalf("joined %+v, want the piece joined from the check by the judging session", entry.Joined)
	}
}

func mustEpic(t *testing.T, root string) epicscope.Epic {
	t.Helper()
	record, err := epicscope.Read(root, product)
	must(t, err)
	found, _ := record.Named(epic)
	return found
}
