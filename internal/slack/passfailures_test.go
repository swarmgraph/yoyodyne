package slack

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func TestAPassFailurePersonOnlyStepIsSaidOnceUntilThePassSucceeds(t *testing.T) {
	t.Parallel()
	for _, remedy := range []ownership.PersonOnlyRemedy{
		{Reason: ownership.PersonCredential, Target: "forge login", Step: "renew the forge login by hand"},
		{Reason: ownership.PersonProtectedFile, Target: ".claude/settings.json", Step: "add the notes-writer hook to .claude/settings.json by hand"},
	} {
		t.Run(string(remedy.Reason), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			// The finding predates the channel, but the person's step does not.
			harness := newTestHarness(t, moment.Add(30*time.Minute))
			passes := harness.runs.Sweeps()
			appendPass := func(at time.Time, failed bool) {
				t.Helper()
				pass := runstate.Sweep{Task: "maintenance", StartedAt: at, EndedAt: at.Add(time.Minute),
					Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "maintenance completed"},
					Steps:  []runstate.SweepStep{{Name: "reconcile", Outcome: runstate.StepRan}}}
				if failed {
					pass.Result = nil
					pass.Problem = "reconcile could not finish"
					pass.Steps[0].Outcome, pass.Steps[0].Detail = runstate.StepFailed, pass.Problem
				}
				if err := passes.Append(pass); err != nil {
					t.Fatal(err)
				}
				if failed {
					if err := passes.RecordPassFailures(ctx, harness.reports, report.Attribution{RepositoryID: "yoyodyne"}, "factory-watch"); err != nil {
						t.Fatal(err)
					}
				}
			}
			for i := 0; i < 3; i++ {
				appendPass(moment.Add(time.Duration(i)*10*time.Minute), true)
			}
			sources := readmodel.Sources{Passes: passes, Reports: harness.reports,
				ProgramManagers: []readmodel.ProgramManagerInstance{{Agent: "factory-watch", Lane: "factory-flow"}}}
			entries, problem := readmodel.ReadPassFailures(sources)
			if problem != "" || len(entries) != 1 {
				t.Fatalf("pass failures = %+v, %s", entries, problem)
			}
			id := entries[0].FailingTask.ReportID
			cursors := harness.poll(t, harness.start())
			// An ordinary remedy and an untyped flag must not send a direct message.
			harness.handle(t, id, "repair the harness and rerun its tests", true, moment.Add(40*time.Minute))
			cursors = harness.poll(t, cursors)
			handledAt := moment.Add(time.Hour)
			if err := harness.reports.Handle(report.Handling{SchemaVersion: report.HandlingSchemaVersion,
				ReportID: id, Role: domain.RoleDevelopmentManager, RunID: "chat-one", ProductID: "yoyodyne", RepositoryID: "yoyodyne",
				RecordedAt: handledAt, NeedsOperator: true, Reason: "only a person can perform this step", PersonOnly: &remedy}); err != nil {
				t.Fatal(err)
			}
			batch, err := harness.feed.Poll(ctx, cursors)
			if err != nil {
				t.Fatal(err)
			}
			var findings []Delivery
			for _, delivery := range batch.Deliveries {
				if delivery.Stream == operatorActionStream && delivery.Posts() {
					findings = append(findings, delivery)
				}
			}
			if len(findings) != 1 || !findings[0].Direct || !findings[0].Tag {
				t.Fatalf("findings = %+v, want one direct, tagged notification of the person's step", findings)
			}
			event := findings[0].Notification.Event
			if event.Detail.Needs != remedy.Step || !event.At.Equal(handledAt) {
				t.Fatalf("notification = %+v, want the exact step dated from its handling", event)
			}
			message, err := notify.Render(findings[0].Notification.Topic, findings[0].Notification.Speaker, event)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{remedy.Step, id, "maintenance", "the development manager, handling the report", "succeeding"} {
				if !strings.Contains(message.Body, want) {
					t.Fatalf("notification %q does not name %q", message.Body, want)
				}
			}
			standing := readmodel.ReadStanding(ctx, sources)
			if len(standing.FactoryProblems) != 1 || standing.FactoryProblems[0].Mover != readmodel.MoverOperator {
				t.Fatalf("factory problems = %+v, want the same person's finding", standing.FactoryProblems)
			}
			count := 0
			for _, entry := range standing.NeedsHuman {
				if entry.Kind == readmodel.AttentionFailingTask || entry.Kind == readmodel.AttentionOperatorAction {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("status carries %d copies of the finding", count)
			}
			cursors = harness.poll(t, cursors, notify.KindOperatorAction)
			mark := findingMark + readmodel.PassFailureKeyPrefix + id
			if !cursors.Streams[operatorActionStream].Has(mark) {
				t.Fatal("the delivery was not remembered")
			}
			cursors = harness.poll(t, cursors)
			// An interrupted log write keeps the delivery mark and still lets
			// an unrelated report reach the channel.
			log, err := os.ReadFile(passes.Path())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(passes.Path(), append(append([]byte(nil), log...), []byte("{\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityNote, moment.Add(65*time.Minute))
			cursors = harness.poll(t, cursors, notify.KindReportFiled)
			if !cursors.Streams[operatorActionStream].Has(mark) {
				t.Fatal("an unreadable sweep log removed the delivery mark")
			}
			if err := os.WriteFile(passes.Path(), log, 0600); err != nil {
				t.Fatal(err)
			}
			cursors = harness.poll(t, cursors)
			appendPass(moment.Add(70*time.Minute), true)
			cursors = harness.poll(t, cursors)
			// Success ends the finding even before its clearing can be filed.
			appendPass(moment.Add(80*time.Minute), false)
			cursors = harness.poll(t, cursors)
			if cursors.Streams[operatorActionStream].Has(mark) {
				t.Fatal("the delivery mark stood after the affected pass succeeded")
			}
			if entries, problem := readmodel.ReadPassFailures(sources); problem != "" || len(entries) != 0 {
				t.Fatalf("the cleared finding still stands: %+v, %s", entries, problem)
			}
			if err := passes.RecordPassFailures(ctx, harness.reports, report.Attribution{RepositoryID: "yoyodyne"}, "factory-watch"); err != nil {
				t.Fatal(err)
			}
			harness.poll(t, cursors)
		})
	}
}
