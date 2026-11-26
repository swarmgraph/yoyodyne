package epicjoin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// fakeReports is the collected reports and their handlings.
type fakeReports struct {
	filed      []report.Report
	handlings  []report.Handling
	appendErr  error
	handledErr error
}

func (f *fakeReports) Append(reported report.Report) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.filed = append(f.filed, reported)
	return nil
}

func (f *fakeReports) List() ([]report.Report, error) { return f.filed, nil }

func (f *fakeReports) Handlings() ([]report.Handling, error) { return f.handlings, f.handledErr }

func (f *fakeReports) handle(id, reason string, at time.Time, operator ownership.OperatorReason) {
	f.handlings = append(f.handlings, report.Handling{ReportID: id, Role: domain.RoleProductManager, Reason: reason, RecordedAt: at,
		NeedsOperator: operator != "", OperatorReason: operator})
}

// couldNotTell is a check of the whole epic that could not tell.
func (b *bench) couldNotTell(id string) epicprogress.Naming {
	b.t.Helper()
	store, err := epicprogress.Open(b.root, product)
	b.must(err)
	record, err := epicscope.Read(b.root, product)
	b.must(err)
	named, _ := record.Named(epic)
	naming := epicprogress.Of(named)
	_, err = store.CallWhileOpen(naming, id, "the check", func() (string, epicprogress.Looked, error) { return "", epicprogress.NotSent, nil },
		func() (string, error) { return "made", nil })
	b.must(err)
	_, err = store.Checked(naming, epicprogress.Check{ID: id, Commit: "0f35eeff1e4a", Findings: []epicprogress.Finding{
		{Asks: "the second thing", Answer: epicprogress.CannotBeTold, Says: "no check covers it"},
		{Asks: "the release", Answer: epicprogress.CannotBeTold, Says: "a person signs it", ForAPerson: true}}})
	b.must(err)
	return naming
}

func (b *bench) asking(reports *fakeReports) Joiner {
	joiner := b.joiner()
	joiner.Evidence = Evidence{Reports: reports, Build: "abc1234", RepositoryID: "yoyodyne"}
	return joiner
}

func (b *bench) owes(joiner Joiner, want bool) {
	b.t.Helper()
	if owed, err := joiner.Owed(context.Background()); err != nil || owed != want {
		b.t.Fatalf("owed %t, %v; want %t", owed, err, want)
	}
}

// A check that could not tell files the Lead Product Manager one report, whose
// identifier is beside the check, and owes nothing until her handling is recorded.
func TestACheckThatCouldNotTellAsksTheLeadProductManagerOnce(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.couldNotTell("check-1")
	reports := &fakeReports{}
	joiner := b.asking(reports)
	b.owes(joiner, true)
	b.must(joiner.Round(context.Background()))
	if len(reports.filed) != 1 {
		t.Fatalf("filed %d reports, want one", len(reports.filed))
	}
	question := reports.filed[0]
	if question.For != domain.RoleProductManager || question.Role != report.HarnessReporter || question.WorkItemID != epic || question.DuringEpic != epic ||
		!strings.Contains(question.Message, "check-1") || !strings.Contains(question.Message, "0f35eeff1e4a") || !strings.Contains(question.Message, "the second thing: no check covers it") ||
		strings.Contains(question.Message, "a person signs it") {
		t.Fatalf("filed %+v", question)
	}
	check := b.entry().Checks[0]
	if check.Asked == nil || check.Asked.Report != question.ID || check.Owed != nil {
		t.Fatalf("check %+v, want the report named beside it and nothing owed", check)
	}
	b.owes(joiner, false)
	b.must(joiner.Round(context.Background()))
	if len(reports.filed) != 1 || len(b.requests) != 0 {
		t.Fatalf("filed %d reports and judged %d, want one and none", len(reports.filed), len(b.requests))
	}
}

// A stop after the identifier is written and before the report is filed leaves
// the report to be filed under that identifier at the next reading.
func TestAQuestionAStopKeptFromBeingFiledIsFiledUnderItsIdentifier(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.couldNotTell("check-1")
	reports := &fakeReports{appendErr: errors.New("the disk is full")}
	joiner := b.asking(reports)
	if err := joiner.Round(context.Background()); err == nil {
		t.Fatal("a report that could not be filed is not an error")
	}
	asked := b.entry().Checks[0].Asked
	if asked == nil || len(reports.filed) != 0 {
		t.Fatalf("asked %+v, filed %d", asked, len(reports.filed))
	}
	reports.appendErr = nil
	b.owes(joiner, true)
	b.must(joiner.Round(context.Background()))
	if len(reports.filed) != 1 || reports.filed[0].ID != asked.Report || b.entry().Checks[0].Asked.Report != asked.Report {
		t.Fatalf("filed %+v, want the one named %s", reports.filed, asked.Report)
	}
}

// Her handling is her answer: one naming the operator's reason owes nothing and
// waits on him; any other owes one correction naming its reason, and the
// development manager's piece for it joins only after its judgment.
func TestTheHandlingOfTheQuestionIsTheAnswer(t *testing.T) {
	t.Parallel()
	answered := namedAt.Add(3 * time.Hour)
	for name, operator := range map[string]ownership.OperatorReason{"evidence named": "", "only the operator": ownership.OperatorHumanGate} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newBench(t)
			b.couldNotTell("check-1")
			reports := &fakeReports{}
			joiner := b.asking(reports)
			b.must(joiner.Round(context.Background()))
			reports.handle(reports.filed[0].ID, "a test that runs the new command", answered, operator)
			b.owes(joiner, true)
			b.must(joiner.Round(context.Background()))
			check := b.entry().Checks[0]
			if check.Asked.Answered == nil || !check.Asked.Answered.Equal(answered) {
				t.Fatalf("asked %+v, want the handling's time recorded", check.Asked)
			}
			if operator != "" {
				if check.Owed != nil || check.Asked.Operator != operator {
					t.Fatalf("check %+v, want it waiting on the operator and owing nothing", check)
				}
				b.owes(joiner, false)
				return
			}
			if check.Owed == nil || check.Owed.Missing != "a test that runs the new command" || !check.Owed.Answered.Equal(answered) {
				t.Fatalf("owed %+v, want one correction naming the handling's reason", check.Owed)
			}
			b.corrected(correcting, "check-1")
			if b.starts(correcting) {
				t.Fatal("the piece giving the evidence starts before it is judged")
			}
			b.must(joiner.Round(context.Background()))
			if asked := b.wants(correcting, epicprogress.AnswerInside, true); asked.From != epicprogress.FromCheck("check-1") || len(reports.filed) != 1 {
				t.Fatalf("asked %+v, filed %d", asked, len(reports.filed))
			}
		})
	}
}

// While the handlings cannot be read, nothing is owed and the round says why.
func TestHandlingsThatCannotBeReadOweNothing(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.couldNotTell("check-1")
	reports := &fakeReports{}
	joiner := b.asking(reports)
	b.must(joiner.Round(context.Background()))
	reports.handle(reports.filed[0].ID, "a test", namedAt.Add(time.Hour), "")
	reports.handledErr = errors.New("the file is cut short")
	if _, err := joiner.Owed(context.Background()); err == nil {
		t.Fatal("owed reads handlings that cannot be read")
	}
	if err := joiner.Round(context.Background()); err == nil || !strings.Contains(err.Error(), "the file is cut short") {
		t.Fatalf("round: %v", err)
	}
	if check := b.entry().Checks[0]; check.Owed != nil || check.Asked.Answered != nil {
		t.Fatalf("check %+v, want nothing owed", check)
	}
}
