package readmodel

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/epicwake"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// wakeRecords is epic mode on with a working epic and a delivered one.
func wakeRecords(t *testing.T) (epicscope.Record, epicprogress.View) {
	t.Helper()
	root := t.TempDir()
	operator := operatorOf(t, root)
	for _, naming := range []epicscope.Naming{
		{Epic: "yoyodyne-ep", Members: []string{"yoyodyne-ep.1"}, From: "terminal"},
		{Epic: "yoyodyne-done", Members: []string{"yoyodyne-done.1"}, From: "terminal"},
	} {
		if _, err := operator.Start(naming, moment); err != nil {
			t.Fatal(err)
		}
	}
	record, err := epicscope.Read(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	view, err := epicprogress.Read(root, "yoyodyne", record)
	if err != nil {
		t.Fatal(err)
	}
	return record, view
}

func wakeEpics(waits []EpicWait, working bool) *Epics {
	delivered := OpenEpic{WorkItemID: "yoyodyne-done", EpicStanding: EpicStanding{State: EpicIntegrated,
		Feedback: []EpicFeedback{{Comment: "comment-7", Member: "yoyodyne-done.1", PullRequest: 7, Says: "the label is wrong", WorkItemID: "yoyodyne-fb"}}}}
	epics := &Epics{Open: []OpenEpic{delivered}}
	if working {
		epics.Open = append(epics.Open, OpenEpic{WorkItemID: "yoyodyne-ep", EpicStanding: EpicStanding{State: EpicWorking, Waits: waits}})
	}
	return epics
}

// Each step the design gives a role, as status names it owed by that role, is an
// event of that role, for the epic it is in.
func TestEachStepARoleOwesIsAnEventOfThatRole(t *testing.T) {
	t.Parallel()
	record, view := wakeRecords(t)
	lead, manager, architect := MoverOf(domain.RoleProductManager), MoverOf(domain.RoleDevelopmentManager), MoverOf(domain.RoleArchitect)
	for _, step := range []struct {
		says  string
		owner Mover
		role  domain.AgentRole
	}{
		{"a question about scope", lead, domain.RoleProductManager},
		{"a check of the whole epic that could not tell", lead, domain.RoleProductManager},
		{"a third failed check", lead, domain.RoleProductManager},
		{"a cancelled or given up member", lead, domain.RoleProductManager},
		{"a pull request closed unmerged by a person", lead, domain.RoleProductManager},
		{"a member with no description to build from", lead, domain.RoleProductManager},
		{"a stopped member", manager, domain.RoleDevelopmentManager},
		{"a member too large for one change", manager, domain.RoleDevelopmentManager},
		{"a piece to write", manager, domain.RoleDevelopmentManager},
		{"a piece to rewrite after the answer on scope", manager, domain.RoleDevelopmentManager},
		{"a paid call whose result cannot be known", manager, domain.RoleDevelopmentManager},
		{"a follow-up of a member that fell due", manager, domain.RoleDevelopmentManager},
		{"a member held for design", architect, domain.RoleArchitect},
		{"a change proposed to a design a member needs", architect, domain.RoleArchitect},
		{"a document a run handed back", architect, domain.RoleArchitect},
		{"an operator's answer to the Lead Product Manager", lead, domain.RoleProductManager},
		{"an operator's answer to the development manager", manager, domain.RoleDevelopmentManager},
		{"an operator's answer to the architect", architect, domain.RoleArchitect},
		{"a member's ordinary comment for the development manager", manager, domain.RoleDevelopmentManager},
	} {
		events := EpicWakeEvents(wakeEpics([]EpicWait{{WorkItemID: "yoyodyne-ep.1", Owner: step.owner, Says: step.says}}, true), record, view, nil, nil)
		// The delivered epic's feedback comes first, and is the Lead Product
		// Manager's as well.
		if len(events) != 2 || events[1].Role != step.role || !strings.Contains(events[1].Says, step.says) ||
			!slices.Equal(events[1].Namings, []epicprogress.Naming{{WorkItemID: "yoyodyne-ep", NamedAt: moment.UTC()}}) {
			t.Errorf("%s: events = %+v, want one of the %s for the epic", step.says, events, step.role)
		}
		again := EpicWakeEvents(wakeEpics([]EpicWait{{WorkItemID: "yoyodyne-ep.1", Owner: step.owner, Says: step.says}}, true), record, view, nil, nil)
		if len(again) != 2 || again[1].Key != events[1].Key {
			t.Errorf("%s: the same step has two keys", step.says)
		}
	}
	// What waits on the reviewer, the harness or something outside
	// starts nobody.
	for _, owner := range []Mover{MoverOf(domain.RoleReviewer), MoverHarness, MoverForge, MoverProvider, MoverNobody, MoverProgramManager} {
		if events := EpicWakeEvents(wakeEpics([]EpicWait{{Owner: owner, Says: "waits"}}, true), record, view, nil, nil); len(events) != 1 {
			t.Errorf("a wait on %s is %+v, want only the feedback", owner, events)
		}
	}
	if events := EpicWakeEvents(wakeEpics(nil, true), epicscope.Record{}, view, nil, nil); len(events) != 0 {
		t.Errorf("with no epic named the events are %+v", events)
	}
}

// Feedback after delivery is the Lead Product Manager's event while some epic is
// open and not delivered, and nobody's while none is.
func TestFeedbackAfterDeliveryWakesTheLeadProductManagerOnlyWhileAnEpicIsOpen(t *testing.T) {
	t.Parallel()
	record, view := wakeRecords(t)
	events := EpicWakeEvents(wakeEpics(nil, true), record, view, nil, nil)
	if len(events) != 1 || events[0].Role != domain.RoleProductManager || events[0].Key != "feedback:yoyodyne-done@"+moment.UTC().Format("20060102T150405.000000000Z")+":comment-7" ||
		!strings.Contains(events[0].Says, "yoyodyne-fb") {
		t.Fatalf("events = %+v, want the feedback as the Lead Product Manager's", events)
	}
	if events := EpicWakeEvents(wakeEpics(nil, false), record, view, nil, nil); len(events) != 0 {
		t.Fatalf("with only the delivered epic open, events = %+v", events)
	}
}

// Which reports are events: one waiting on one of the three roles that names a
// member, one waiting on a program manager that names a member, for the Lead
// Product Manager, and a critical one waiting on the development manager or the
// architect. A critical one waiting on her or a program manager is delivered to
// her as a pass of its own, and any other waiting on a program manager stays on
// its list.
func TestWhichReportsAreEvents(t *testing.T) {
	t.Parallel()
	record, view := wakeRecords(t)
	at := moment
	filed := func(id string, role domain.AgentRole, severity report.Severity, item string) report.Report {
		at = at.Add(time.Minute)
		return report.Report{ID: id, Role: domain.RoleDeveloper, For: role, Severity: severity, WorkItemID: item, Message: id, RecordedAt: at}
	}
	reports := []report.Report{
		filed("dm-member", domain.RoleDevelopmentManager, report.SeverityNote, "yoyodyne-ep.1"),
		filed("arch-member", domain.RoleArchitect, report.SeverityWarning, "yoyodyne-ep.1"),
		filed("lead-member", domain.RoleProductManager, report.SeverityNote, "yoyodyne-ep.1"),
		filed("pm-member", domain.RoleProgramManager, report.SeverityNote, "yoyodyne-ep.1"),
		filed("dm-critical", domain.RoleDevelopmentManager, report.SeverityCritical, ""),
		filed("arch-critical", domain.RoleArchitect, report.SeverityCritical, "yoyodyne-outside"),
		filed("lead-critical", domain.RoleProductManager, report.SeverityCritical, ""),
		filed("pm-critical", domain.RoleProgramManager, report.SeverityCritical, ""),
		filed("pm-other", domain.RoleProgramManager, report.SeverityWarning, "yoyodyne-outside"),
		filed("dm-other", domain.RoleDevelopmentManager, report.SeverityWarning, ""),
		filed("handled", domain.RoleDevelopmentManager, report.SeverityCritical, ""),
	}
	handlings := []report.Handling{{ReportID: "handled", Reason: "dealt with", RecordedAt: at}}
	events := EpicWakeEvents(&Epics{}, record, view, reports, handlings)
	var got []string
	for _, event := range events {
		got = append(got, strings.TrimPrefix(event.Key, "report:")+" "+string(event.Role))
	}
	want := []string{
		"dm-member development-manager", "arch-member architect", "lead-member product-manager", "pm-member product-manager",
		"dm-critical development-manager", "arch-critical architect",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	if events[0].Namings[0].WorkItemID != "yoyodyne-ep" || len(events[4].Namings) != 0 {
		t.Fatalf("a report naming a member is for its epic and one naming none for none: %+v", events)
	}
}

// An event no pass settled is said on status as a problem.
func TestAnEventNoPassSettledIsAProblemOnStatus(t *testing.T) {
	t.Parallel()
	root, sources := epicFixture(t)
	now := moment
	store, err := epicwake.Open(root, "yoyodyne", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	owed := []epicwake.Event{{Key: "step", Role: domain.RoleProductManager, Says: "epic yoyodyne-ep: a question about scope"}}
	for range 2 {
		due, err := store.Owed(owed)
		if err == nil && len(due[domain.RoleProductManager].Events) == 0 {
			now = now.Add(epicwake.Gather)
			due, err = store.Owed(owed)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Woken(domain.RoleProductManager, "pass-"+now.String(), due[domain.RoleProductManager].Events); err != nil {
			t.Fatal(err)
		}
		now = now.Add(epicwake.Again)
	}
	if _, err := store.Owed(owed); err != nil {
		t.Fatal(err)
	}
	rendered := ReadStanding(context.Background(), sources).RenderEpics()
	if !strings.Contains(rendered, "nothing more starts for this, because 2 passes of the Lead Product Manager ended without settling it: epic yoyodyne-ep: a question about scope") {
		t.Fatalf("status does not name the event:\n%s", rendered)
	}
}
