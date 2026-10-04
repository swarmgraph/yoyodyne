package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// memoryPile is the collected reports and their handlings as a test holds them.
type memoryPile struct {
	reports   []report.Report
	handlings []report.Handling
}

func (p *memoryPile) List() ([]report.Report, error) { return p.reports, nil }

func (p *memoryPile) Handlings() ([]report.Handling, error) { return p.handlings, nil }

func (p *memoryPile) handle(id string) {
	p.handlings = append(p.handlings, report.Handling{ReportID: id, Reason: "dealt with", RecordedAt: recurringNow})
}

// handlingRole is a woken role that does something between being asked and
// answering, the way a real one records a handling during its turn.
type handlingRole struct {
	wokenRole
	during func(turn int)
}

func (r *handlingRole) Wake(ctx context.Context, role domain.AgentRole, agent, pass, model, message string) (Turn, error) {
	if r.during != nil {
		r.during(len(r.messages))
	}
	return r.wokenRole.Wake(ctx, role, agent, pass, model, message)
}

func reportTriageTask() map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"report-triage": {
			Role:     domain.RoleProductManager,
			Every:    config.Duration(time.Hour),
			Enabled:  true,
			Prompt:   "work the collected reports",
			MaxTurns: 3,
		},
	}
}

func filedReport(id string, role domain.AgentRole, severity report.Severity, message string, at time.Time) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          role,
		Agent:         string(role),
		RunID:         "chat-pgm",
		ProductID:     "example",
		RepositoryID:  "example",
		Severity:      severity,
		Message:       message,
		RecordedAt:    at,
	}
}

// claimed takes the task's scheduled firing now, so a test starts from a
// cadence that is not due and whatever fires next is fired by something else.
func claimed(t *testing.T, store *runstate.SweepStore, task string) {
	t.Helper()
	if _, err := store.Claim(context.Background(), task, time.Hour, recurringNow); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), task, ""); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
}

// A critical report filed by any role is put in front of the Lead Product
// Manager as a turn of its own on the next pull, out of her task's cadence; her
// account saying the pass is complete while it stands unhandled is refused and
// she is asked again by name; and once she has handled it the pass ends.
func TestACriticalReportIsDeliveredAtOnceAndThePassCannotEndCompleteOverIt(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	claimed(t, store, "report-triage")
	const critical = "report-00000000000000000000000000000c01"
	pile := &memoryPile{reports: []report.Report{
		filedReport("report-00000000000000000000000000000001", domain.RoleDeveloper, report.SeverityNote, "a stale document", recurringNow.Add(-2*time.Hour)),
		filedReport(critical, domain.RoleProgramManager, report.SeverityCritical, "Nothing is landing on the protected main.", recurringNow.Add(-time.Minute)),
	}}
	role := &handlingRole{wokenRole: wokenRole{answers: []scriptedTurn{
		{result: complete("handled the note")},
		{result: complete("handled the critical")},
	}}}
	role.during = func(turn int) {
		// The first turn passes over the critical; the second handles it.
		if turn == 1 {
			pile.handle(critical)
		}
	}
	trigger := Trigger{Tasks: reportTriageTask(), Claims: store, Reports: store, Roles: role, Pile: pile, Clock: recurringClock{}}

	swept, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(swept.Fired) != 1 || swept.Fired[0].Task != "report-triage" {
		t.Fatalf("Fire() = %+v, want the report task fired for the critical though its cadence is not due", swept)
	}
	if len(role.messages) != 2 {
		t.Fatalf("the pass took %d turn(s), want two: the account ending complete over the critical was to be refused", len(role.messages))
	}
	first := role.messages[0]
	for _, want := range []string{"A critical report was filed", critical, "Nothing is landing on the protected main.", "not accepted as complete"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the delivering message is missing %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "a stale document") {
		t.Fatalf("the delivering message carried a routine report as though it were the critical:\n%s", first)
	}
	second := role.messages[1]
	for _, want := range []string{"refused", critical, "\"handle\""} {
		if !strings.Contains(second, want) {
			t.Fatalf("the refusal's message is missing %q:\n%s", want, second)
		}
	}

	recorded, _, err := store.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded = %+v (%v), want the one pass", recorded, err)
	}
	pass := recorded[0]
	if len(pass.Criticals) != 1 || pass.Criticals[0] != critical {
		t.Fatalf("pass criticals = %v, want the delivered report", pass.Criticals)
	}
	if pass.Turns != 2 || pass.Result == nil || pass.Result.Status != sweep.StatusComplete {
		t.Fatalf("pass = %+v, want two turns ending complete once the critical was handled", pass)
	}
	if !strings.Contains(pass.Problem, "refused it as complete") || !strings.Contains(pass.Problem, critical) {
		t.Fatalf("pass problem = %q, want the refusal named with the report", pass.Problem)
	}

	// Delivered once: the next pull does not fire again for it.
	again, err := trigger.Fire(context.Background())
	if err != nil || len(again.Fired) != 0 {
		t.Fatalf("second Fire() = %+v (%v), want nothing fired for a critical already delivered", again, err)
	}
}

// A pass that never handles a critical it was shown ends partial when its turns
// run out, and the critical is not delivered as its own turn a second time.
func TestAPassThatNeverHandlesTheCriticalEndsPartial(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	claimed(t, store, "report-triage")
	const critical = "report-00000000000000000000000000000c02"
	pile := &memoryPile{reports: []report.Report{
		filedReport(critical, domain.RoleReviewer, report.SeverityCritical, "the gate is open", recurringNow.Add(-time.Minute)),
	}}
	role := &wokenRole{answers: []scriptedTurn{
		{result: complete("nothing")}, {result: complete("nothing")}, {result: complete("nothing")},
	}}
	trigger := Trigger{Tasks: reportTriageTask(), Claims: store, Reports: store, Roles: role, Pile: pile, Clock: recurringClock{}}

	swept, err := trigger.Fire(context.Background())
	if err != nil || len(swept.Fired) != 1 {
		t.Fatalf("Fire() = %+v (%v)", swept, err)
	}
	if len(role.messages) != 3 || !swept.Fired[0].Truncated {
		t.Fatalf("the pass took %d turn(s), truncated %v; want every turn spent and the pass partial", len(role.messages), swept.Fired[0].Truncated)
	}
	recorded, _, _ := store.List()
	if recorded[0].Result.Status != sweep.StatusMore || !strings.Contains(recorded[0].Problem, "recorded as partial") {
		t.Fatalf("pass = %+v, want it recorded as partial over the standing critical", recorded[0])
	}
	if again, err := trigger.Fire(context.Background()); err != nil || len(again.Fired) != 0 {
		t.Fatalf("second Fire() = %+v (%v), want no second delivery", again, err)
	}
}

// A critical the conversation carried into an ordinary pass holds that pass
// the same way: the turn says which it was shown, and an account ending
// complete while it stands is refused.
func TestAnOrdinaryPassShownACriticalCannotEndCompleteOverIt(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	const critical = "report-00000000000000000000000000000c03"
	// The Lead Product Manager's own critical is never delivered as a turn of its
	// own — she filed it in the conversation it would go into — so this pass is
	// the scheduled one, and the critical reaches it through her conversation.
	pile := &memoryPile{reports: []report.Report{
		filedReport(critical, domain.RoleProductManager, report.SeverityCritical, "the tracker export is stale", recurringNow.Add(-time.Hour)),
	}}
	role := &shownRole{shown: []string{critical}, answers: []*sweep.Result{complete("done"), complete("done")}}
	role.during = func(turn int) {
		if turn == 1 {
			pile.handle(critical)
		}
	}
	trigger := Trigger{Tasks: reportTriageTask(), Claims: store, Reports: store, Roles: role, Pile: pile, Clock: recurringClock{}}
	swept, err := trigger.Fire(context.Background())
	if err != nil || len(swept.Fired) != 1 {
		t.Fatalf("Fire() = %+v (%v)", swept, err)
	}
	if len(role.messages) != 2 || !strings.Contains(role.messages[1], critical) {
		t.Fatalf("messages = %q, want a second turn naming the critical", role.messages)
	}
	if strings.Contains(role.messages[0], "A critical report was filed") {
		t.Fatalf("the Lead Product Manager's own critical was delivered to her as a turn of its own:\n%s", role.messages[0])
	}
}

// shownRole is a role whose conversation says it carried some criticals into
// the first turn.
type shownRole struct {
	shown    []string
	answers  []*sweep.Result
	messages []string
	during   func(turn int)
}

func (r *shownRole) Wake(_ context.Context, _ domain.AgentRole, _, _, _, message string) (Turn, error) {
	if r.during != nil {
		r.during(len(r.messages))
	}
	r.messages = append(r.messages, message)
	turn := Turn{ConversationID: "chat-1"}
	if len(r.messages) == 1 {
		turn.CriticalReports = r.shown
	}
	if len(r.answers) > 0 {
		turn.Result, r.answers = r.answers[0], r.answers[1:]
	}
	return turn, nil
}

// A program manager's warning or note that two of the Lead Product Manager's
// passes have started after, and nobody has handled, is named on her next
// pass as overdue; one only a single pass has gone by, one somebody handled,
// and another role's are not.
func TestAProgramManagersReportLeftThroughTwoPassesIsNamedOverdue(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	for _, started := range []time.Time{recurringNow.Add(-3 * time.Hour), recurringNow.Add(-2 * time.Hour)} {
		if err := store.Append(runstate.Sweep{
			SchemaVersion: runstate.SweepSchemaVersion, ProductID: "example", Task: "report-triage",
			Role: domain.RoleProductManager, StartedAt: started, EndedAt: started.Add(time.Minute),
			Turns: 1, Result: complete("worked the pile"),
		}); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	const (
		overdue  = "report-00000000000000000000000000000d01"
		recent   = "report-00000000000000000000000000000d02"
		handled  = "report-00000000000000000000000000000d03"
		otherOne = "report-00000000000000000000000000000d04"
	)
	pile := &memoryPile{reports: []report.Report{
		filedReport(overdue, domain.RoleProgramManager, report.SeverityWarning, "the lane has landed nothing in a day", recurringNow.Add(-4*time.Hour)),
		filedReport(recent, domain.RoleProgramManager, report.SeverityNote, "one pass since", recurringNow.Add(-150*time.Minute)),
		filedReport(handled, domain.RoleProgramManager, report.SeverityWarning, "handled already", recurringNow.Add(-4*time.Hour)),
		filedReport(otherOne, domain.RoleDeveloper, report.SeverityWarning, "a developer's", recurringNow.Add(-4*time.Hour)),
	}}
	pile.handle(handled)
	role := &wokenRole{answers: []scriptedTurn{{result: complete("worked the pile")}}}
	trigger := Trigger{Tasks: reportTriageTask(), Claims: store, Reports: store, Roles: role, Pile: pile, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %d, want the scheduled pass", len(role.messages))
	}
	message := role.messages[0]
	for _, want := range []string{"## Overdue reports", overdue, "unhandled through 2 of your passes", "the lane has landed nothing in a day"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the pass's message is missing %q:\n%s", want, message)
		}
	}
	for _, unwanted := range []string{recent, handled, otherOne} {
		if strings.Contains(message, unwanted) {
			t.Fatalf("the pass's message names %s as overdue:\n%s", unwanted, message)
		}
	}
}

func TestAnOverdueReportDeclaresItsCutAndHowToReadTheWholeMessage(t *testing.T) {
	t.Parallel()
	store := sweepStore(t)
	for _, started := range []time.Time{recurringNow.Add(-3 * time.Hour), recurringNow.Add(-2 * time.Hour)} {
		if err := store.Append(runstate.Sweep{
			SchemaVersion: runstate.SweepSchemaVersion, ProductID: "example", Task: "report-triage",
			Role: domain.RoleProductManager, StartedAt: started, EndedAt: started.Add(time.Minute),
			Turns: 1, Result: complete("worked the pile"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	const id = "report-00000000000000000000000000000001"
	message := strings.Repeat("é", overdueTextBytes) + "\nThe request at the end must be read."
	pile := &memoryPile{reports: []report.Report{
		filedReport(id, domain.RoleProgramManager, report.SeverityWarning, message, recurringNow.Add(-4*time.Hour)),
	}}
	trigger := Trigger{Reports: store, Pile: pile}
	listed := trigger.overdueFor(reportTriageTask()["report-triage"])
	for _, want := range []string{
		id,
		fmt.Sprintf("limited to %d bytes", overdueTextBytes),
		fmt.Sprintf("message cut; full message is %d bytes", len(message)),
		`{"action":"read","report":"report-id"}`,
	} {
		if !strings.Contains(listed, want) {
			t.Fatalf("the overdue list lacks %q: %s", want, listed)
		}
	}
	if strings.Contains(listed, "The request at the end") || !utf8.ValidString(listed) {
		t.Fatalf("the preview exceeded its bound or cut through a rune: %s", listed)
	}
}
