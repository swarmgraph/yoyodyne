package orchestrator

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

const (
	architectWarning = "report-00000000000000000000000000000b01"
	architectNote    = "report-00000000000000000000000000000b02"
	namedItem        = "yoyodyne-ifd.501"
)

// architectPile is a warning to the architect that names the labelled item and
// a note to her that names no item.
func architectPile() *memoryPile {
	warning := filedReport(architectWarning, domain.RoleProductManager, report.SeverityWarning,
		"the design's Slack route posts as the operator with no app registered", recurringNow.Add(-time.Hour))
	warning.For, warning.WorkItemID = domain.RoleArchitect, namedItem
	note := filedReport(architectNote, domain.RoleProgramManager, report.SeverityNote,
		"the decision index is a week behind", recurringNow.Add(-time.Hour))
	note.For = domain.RoleArchitect
	return &memoryPile{reports: []report.Report{warning, note}}
}

// latestPass is the last pass the sweep log records.
func latestPass(t *testing.T, sweeps *runstate.SweepStore) runstate.Sweep {
	t.Helper()
	recorded, _, err := sweeps.List()
	if err != nil || len(recorded) == 0 {
		t.Fatalf("List() = %d passes, %v, want the pass recorded", len(recorded), err)
	}
	return recorded[len(recorded)-1]
}

// A warning addressed to the architect reaches her next turn, and where it
// names an item that turn decides on, it is shown under that item as well as in
// the section of reports addressed to her. The pass records both reports as
// shown to it.
func TestAWarningAddressedToARoleReachesItsNextTurnBesideTheItemItNames(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.trigger.Pile = architectPile()
	w.items.label(namedItem, "Choose how Slack is reached")
	w.items.label("yoyodyne-ifd.502", "Choose the record's owner")
	w.fire(t, recurringNow)

	message := w.lastMessage(t)
	for _, want := range []string{"## Reports addressed to you", architectWarning, "[warning]", architectNote, `"reports":[`} {
		if !strings.Contains(message, want) {
			t.Fatalf("the turn's message lacks %q:\n%s", want, message)
		}
	}
	lines := strings.Split(message, "\n")
	beside := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "- "+namedItem+":") {
			beside = index + 1
		}
	}
	if beside < 0 || beside >= len(lines) || !strings.Contains(lines[beside], "names this item") || !strings.Contains(lines[beside], architectWarning) {
		t.Fatalf("the warning is not shown on the line under %s:\n%s", namedItem, message)
	}
	for _, line := range lines {
		if strings.Contains(line, "names this item") && strings.Contains(line, architectNote) {
			t.Fatalf("a report naming no item is shown beside one: %s", line)
		}
	}
	if pass := latestPass(t, w.sweeps); strings.Join(pass.Addressed, ",") != architectWarning+","+architectNote {
		t.Fatalf("the pass recorded %v as shown, want both reports", pass.Addressed)
	}
}

// A pass's account says what it decided about each report it was shown or that
// it left one; the decision is kept on the record, and a report the account
// says nothing about is named on the record as passed over.
func TestAPassRecordsWhatItDecidedAboutEachReportItWasShown(t *testing.T) {
	t.Parallel()

	w := newArchitectWake(t)
	w.trigger.Pile = architectPile()
	w.items.label(namedItem, "Choose how Slack is reached")
	w.clock.now = recurringNow
	w.role.answers = []scriptedTurn{{result: &sweep.Result{
		Status:  sweep.StatusComplete,
		Summary: "revised the design for the warning",
		Reports: []sweep.ReportDecision{{Report: architectWarning, Decision: "revised the Slack route before landing the design"}},
	}}}
	if _, err := w.trigger.Fire(t.Context()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}

	pass := latestPass(t, w.sweeps)
	if pass.Result == nil {
		t.Fatalf("the pass recorded no account: %+v", pass)
	}
	if decided, found := pass.Result.ReportDecided(architectWarning); !found || decided.Decision == "" {
		t.Fatalf("the account's decision on %s was not kept: %+v", architectWarning, pass.Result.Reports)
	}
	if !strings.Contains(pass.Problem, "passed over") || !strings.Contains(pass.Problem, architectNote) || strings.Contains(pass.Problem, architectWarning) {
		t.Fatalf("the record does not name the note alone as passed over: %q", pass.Problem)
	}
}

// A new warning reaches the next scheduled turn however many older reports
// still wait and were shown before, and one naming an item waiting in the role's
// conversation is shown under that item and recorded as shown.
func TestANewWarningIsNotKeptOffTheListByOlderReports(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	pile := &memoryPile{}
	for index := range maxAddressedListed {
		old := filedReport(fmt.Sprintf("report-%032x", 0xe00+index), domain.RoleProgramManager, report.SeverityNote, "old", recurringNow.Add(-48*time.Hour))
		old.For = domain.RoleArchitect
		pile.reports = append(pile.reports, old)
	}
	role := &wokenRole{answers: []scriptedTurn{{result: complete("first")}, {result: complete("second")}}}
	work := fixedConversationWork{Section: "## Work waiting in your conversation\n\n- Slack route (" + namedItem + ") [P1, open]\n", Items: []runstate.DeliveredWork{{ID: namedItem, Priority: 1}}}
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{
		Tasks:  map[string]config.RecurringTask{"architect-pass": {Role: domain.RoleArchitect, Every: config.Duration(time.Hour), Enabled: true, Prompt: "look", MaxTurns: 1}},
		Claims: store, Reports: store, Roles: role, Pile: pile, Clock: clock, ConversationWork: work,
	}
	if _, err := trigger.Fire(t.Context()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	warning := architectPile().reports[0]
	warning.RecordedAt = recurringNow.Add(time.Minute)
	pile.reports = append(pile.reports, warning)
	clock.now = recurringNow.Add(2 * time.Hour)
	if _, err := trigger.Fire(t.Context()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 2 {
		t.Fatalf("%d turns, want two", len(role.messages))
	}
	message := role.messages[1]
	_, list, _ := strings.Cut(message, "## Reports addressed to you")
	if !strings.Contains(list, architectWarning) {
		t.Fatalf("the new warning is not in the list:\n%s", message)
	}
	if !strings.Contains(message, "("+namedItem+") [P1, open]\n  - A report addressed to you names this item; decide it before you decide the item: "+architectWarning) {
		t.Fatalf("the warning is not under the item it names:\n%s", message)
	}
	passes, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	shown := 0
	for _, pass := range passes {
		if slices.Contains(pass.Addressed, architectWarning) {
			shown++
		}
	}
	if shown != 1 {
		t.Fatalf("%d passes record the warning as shown, want the second alone", shown)
	}
}

// The development manager's summoned turn lists the reports addressed to her.
func TestASummonedTurnListsTheReportsAddressedToTheRole(t *testing.T) {
	t.Parallel()

	warning := filedReport(architectWarning, domain.RoleProgramManager, report.SeverityWarning, "reviews wait", recurringNow)
	warning.For = domain.RoleDevelopmentManager
	task := config.RecurringTask{Role: domain.RoleDevelopmentManager, Every: config.Duration(time.Hour), Prompt: "work the docket"}
	trigger := Trigger{Pile: &memoryPile{reports: []report.Report{warning}}, Reports: sweepStore(t)}
	if message := summonsMessage("sweep", task, runstate.IntakeHold{}, "", trigger.addressedTo(task), amendmentBatch{}); !strings.Contains(message, architectWarning) {
		t.Fatalf("the summoned turn lacks the warning:\n%s", message)
	}
}

// fixedConversationWork is one reading of the work waiting in a conversation.
type fixedConversationWork ConversationWorkReading

func (w fixedConversationWork) Read(context.Context, domain.AgentRole, StandingRulings) (ConversationWorkReading, error) {
	return ConversationWorkReading(w), nil
}
