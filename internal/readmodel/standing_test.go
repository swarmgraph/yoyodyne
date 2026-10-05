package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var moment = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// fakeStoppages is what the harness has stopped and nobody has decided about.
// A reading with none is a harness holding nothing, which is what every test
// here means unless it says otherwise.
type fakeStoppages struct {
	runs        []runstate.State
	escalations []runstate.Escalation
	fail        error
}

func (f fakeStoppages) Recorded() ([]runstate.State, error) { return f.runs, f.fail }

func (f fakeStoppages) Escalated() ([]runstate.Escalation, error) { return f.escalations, f.fail }

type fakeRuns struct {
	incomplete  []runstate.State
	outstanding []runstate.State
	recorded    []runstate.State
	prices      map[string]runstate.ItemPrice
	failIncomplete,
	failOutstanding,
	failRecorded,
	failPrice error
}

func (f fakeRuns) Incomplete() ([]runstate.State, error) {
	return f.incomplete, f.failIncomplete
}

func (f fakeRuns) Outstanding() ([]runstate.State, error) {
	return f.outstanding, f.failOutstanding
}

func (f fakeRuns) Recorded() ([]runstate.State, error) {
	return f.recorded, f.failRecorded
}

func (f fakeRuns) Price(workItemID string) (runstate.ItemPrice, error) {
	if f.failPrice != nil {
		return runstate.ItemPrice{}, f.failPrice
	}
	return f.prices[workItemID], nil
}

type fakeConversations struct {
	recorded    []runstate.Conversation
	held        map[string]bool
	fail        error
	failObserve map[string]error
}

func (f fakeConversations) Recorded() ([]runstate.Conversation, error) {
	return f.recorded, f.fail
}

func (f fakeConversations) InFlight(identity runstate.ConversationIdentity) (bool, error) {
	if err, named := f.failObserve[identity.Agent]; named {
		return false, err
	}
	return f.held[identity.Agent], nil
}

type fakeTracker struct {
	byStatus map[string][]beads.WorkItem
	ready    []beads.WorkItem
	fail     error
}

func (f fakeTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, f.fail
}

func (f fakeTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	return f.ready, f.fail
}

type statusTracker struct{ fakeTracker }

func (s statusTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return s.byStatus[status], nil
}

type fakeDirectives struct {
	recorded []directive.Directive
	fail     error
}

func (f fakeDirectives) List() ([]directive.Directive, error) { return f.recorded, f.fail }

type fakeAmendments struct {
	records []amendment.Record
	fail    error
}

func (f fakeAmendments) List() ([]amendment.Record, error) { return f.records, f.fail }

type fakeOperatorHolds struct {
	hold runstate.OperatorHold
	held bool
	fail error
}

func (f fakeOperatorHolds) Held() (runstate.OperatorHold, bool, error) {
	return f.hold, f.held, f.fail
}

type fakeIntakeHolds struct {
	hold runstate.IntakeHold
	held bool
	fail error
}

func (f fakeIntakeHolds) Held() (runstate.IntakeHold, bool, error) {
	return f.hold, f.held, f.fail
}

type fakeSessions struct {
	transitions []runstate.WatchTransition
	fail        error
}

func (f fakeSessions) List() ([]runstate.WatchTransition, error) { return f.transitions, f.fail }

type fakeReports struct {
	reports   []report.Report
	handlings []report.Handling
	fail      error
	handleErr error
}

func (f fakeReports) List() ([]report.Report, error) { return f.reports, f.fail }

func (f fakeReports) Handlings() ([]report.Handling, error) { return f.handlings, f.handleErr }

// filedReport is one report in the pile, distinguished only by when it was filed
// and how loudly it asks to be read.
func filedReport(id string, severity report.Severity, filed time.Time) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       "something was noticed",
		RecordedAt:    filed,
	}
}

// undecidedChange is one proposal nobody has decided, distinguished by its
// identifier and when it was raised.
func undecidedChange(id string, raised time.Time) amendment.Record {
	return amendment.Record{Proposal: &amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "brief",
		Kind:          artifact.KindBrief,
		Owner:         domain.RoleProductManager,
		Change:        "say where a finding goes",
		Why:           "nothing says it today",
		RaisedAt:      raised,
	}}
}

// quietSources is a harness with nothing wrong with it and nothing happening:
// one session choosing work, no holds, an empty queue. Each test moves one
// thing, so what a line says is attributable to the one record that changed.
func quietSources() Sources {
	return Sources{
		Runs:          fakeRuns{prices: map[string]runstate.ItemPrice{}},
		Stoppages:     fakeStoppages{},
		Conversations: fakeConversations{},
		Tracker:       statusTracker{},
		Directives:    fakeDirectives{},
		Amendments:    fakeAmendments{},
		OperatorHolds: fakeOperatorHolds{},
		IntakeHolds:   fakeIntakeHolds{},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{
			{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-time.Hour)},
		}},
		Reports:  fakeReports{},
		Capacity: 2,
		Now:      func() time.Time { return moment },
	}
}

// A pile that is being worked through says nothing on any line: it is not
// waiting on a person, and a status that named it every reading would be a
// status with a permanent entry nobody can clear. A critical report is the one
// exception, and it is tested below: critical is the severity that means
// action, so it is a finding for the operator until somebody handles it.
func TestAPileBeingWorkedThroughWaitsOnNobody(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{reports: []report.Report{
		filedReport("report-00000000000000000000000000000001", report.SeverityWarning, moment.Add(-2*time.Hour)),
		filedReport("report-00000000000000000000000000000002", report.SeverityNote, moment.Add(-30*time.Minute)),
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("NeedsHuman = %#v, want a fresh pile to wait on nobody", standing.NeedsHuman)
	}
	// The counts are still carried, because whether the pile is draining is a
	// question about a week of readings rather than about this one.
	if standing.Reports.Unhandled != 2 || standing.Reports.Collected != 2 {
		t.Fatalf("Reports = %#v", standing.Reports)
	}
	if standing.Reports.Worst != report.SeverityWarning {
		t.Fatalf("Worst = %q, want the pile's worst severity", standing.Reports.Worst)
	}
}

// A run stopping on a condition only a person can clear is recorded as the
// development manager's escalation of that stoppage, and it is a finding for the
// operator for as long as the escalation is the decision standing on the item's
// latest stopped run and the item is still admitted. A later decision on the
// run, a later run, and the item leaving the backlog each end it.
func TestAnEscalatedStoppageIsAFindingForTheOperatorWhileItStands(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	decided := moment.Add(-20 * time.Hour)
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {
			{ID: "yoyodyne-ifd.272", Title: "Escalated to a person", Status: "blocked"},
			{ID: "yoyodyne-ifd.187", Title: "Decided again since", Status: "blocked"},
			{ID: "yoyodyne-ifd.190", Title: "Run again since", Status: "blocked"},
		}},
	}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{
		heldRun("run-272a", "yoyodyne-ifd.272", stopped),
		heldRun("run-187a", "yoyodyne-ifd.187", stopped),
		heldRun("run-190a", "yoyodyne-ifd.190", stopped),
		heldRun("run-190b", "yoyodyne-ifd.190", stopped.Add(time.Hour)),
		// An item that has left the backlog: escalated, then retired.
		heldRun("run-300a", "yoyodyne-ifd.300", stopped),
	}}
	escalate := func(run string) runstate.TriageDecision {
		return runstate.TriageDecision{
			Decision: runstate.TriageDecisionEscalate, RunID: run,
			Reason:       "the target branch diverged from the forge and only a person can say which history is right",
			DecidedBy:    "development-manager",
			Conversation: "chat-dm", Turn: 12, DecidedAt: decided,
		}
	}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.272": {Decisions: []runstate.TriageDecision{escalate("run-272a")}},
		// Escalated and then decided again: the later decision supersedes it.
		"yoyodyne-ifd.187": {Decisions: []runstate.TriageDecision{escalate("run-187a"), {Decision: runstate.TriageDecisionRerun, RunID: "run-187a"}}},
		// Escalated on an earlier run than the item's latest.
		"yoyodyne-ifd.190": {Decisions: []runstate.TriageDecision{escalate("run-190a")}},
		"yoyodyne-ifd.300": {Decisions: []runstate.TriageDecision{escalate("run-300a")}},
	}

	standing := ReadStanding(context.Background(), sources)
	var named []Attention
	for _, waiting := range standing.NeedsHuman {
		if waiting.Named() {
			named = append(named, waiting)
		}
	}
	if len(named) != 1 {
		t.Fatalf("NeedsHuman = %#v, want the one standing escalation named", standing.NeedsHuman)
	}
	finding := named[0]
	for _, want := range []string{
		"yoyodyne-ifd.272 needs your hand: the target branch diverged from the forge",
		"found by the development manager, escalating the stopped run to the operator",
		"recorded in the development manager's escalation of run run-272a, recorded in chat-dm at turn 12, and the blocker on yoyodyne-ifd.272",
	} {
		if !strings.Contains(finding.What(), want) {
			t.Fatalf("finding = %q, want it to say %q", finding.What(), want)
		}
	}
	if !strings.HasPrefix(finding.Whose(), "the operator's") || !strings.Contains(finding.Whose(), "a later triage decision on the run") {
		t.Fatalf("finding is %q, want the operator's move and what ends it", finding.Whose())
	}
	if standing.NeedsHumanProblem != "" {
		t.Fatalf("NeedsHumanProblem = %q, want a fully read line", standing.NeedsHumanProblem)
	}

	// The same derivation without a queue to read admits every item, which is
	// the feed's reading: an item that left the backlog is still said once
	// rather than never.
	escalated, problem := EscalatedOperatorActions(sources.Stoppages.(fakeStoppages).runs, sources.Decisions, nil, nil)
	if problem != "" || len(escalated) != 2 || escalated[0].Key != "run:run-272a" || escalated[1].Key != "run:run-300a" {
		t.Fatalf("EscalatedOperatorActions() = %#v, %q, want the two standing escalations oldest first", escalated, problem)
	}
}

// The yoyodyne-ifd.78 shape, 2026-09-28. The development manager escalated a
// stopped run to the operator, the Lead Product Manager then parked the item,
// and the run's branch and worktree were long gone — and the escalation stood on
// the operator's line for two days, because nothing about either ended it. An
// escalation ends when its item is parked, when the run's change is gone, and
// when the reconcile sweep has recorded either on the run, and each ending says
// what ended it.
func TestAnEscalationEndsWhenItsItemIsParkedOrItsChangeIsGone(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-48 * time.Hour)
	decided := moment.Add(-47 * time.Hour)
	recorded := heldRun("run-320a", "yoyodyne-ifd.320", stopped)
	recorded.EscalationEnded = &runstate.EscalationEnding{At: moment.Add(-time.Hour), Why: "yoyodyne-ifd.320 was parked, so nothing about it waits on the operator: superseded"}
	runs := []runstate.State{
		heldRun("run-272a", "yoyodyne-ifd.272", stopped),
		heldRun("run-78a", "yoyodyne-ifd.78", stopped),
		heldRun("run-310a", "yoyodyne-ifd.310", stopped),
		recorded,
	}
	escalate := func(run string) runstate.TriageDecision {
		return runstate.TriageDecision{
			Decision: runstate.TriageDecisionEscalate, RunID: run,
			Reason:    "only a person can say which history is right",
			DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 12, DecidedAt: decided,
		}
	}
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {
			{ID: "yoyodyne-ifd.272", Title: "Still escalated", Status: "blocked"},
			{ID: "yoyodyne-ifd.78", Title: "Parked after the escalation", Status: "blocked", Parking: domain.WorkItemParking("waits on the machine-home design")},
			{ID: "yoyodyne-ifd.310", Title: "Change gone", Status: "blocked"},
			{ID: "yoyodyne-ifd.320", Title: "Ending already recorded", Status: "blocked"},
		}},
	}}
	sources.Stoppages = fakeStoppages{runs: runs}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.272": {Decisions: []runstate.TriageDecision{escalate("run-272a")}},
		"yoyodyne-ifd.78":  {Decisions: []runstate.TriageDecision{escalate("run-78a")}},
		"yoyodyne-ifd.310": {Decisions: []runstate.TriageDecision{escalate("run-310a")}},
		"yoyodyne-ifd.320": {Decisions: []runstate.TriageDecision{escalate("run-320a")}},
	}
	// The repository holds every run's change but run-310a's.
	sources.Remains = survivingExcept{"run-310a": true}

	standing := ReadStanding(context.Background(), sources)
	var named []string
	for _, waiting := range standing.NeedsHuman {
		if waiting.Kind == AttentionOperatorAction {
			named = append(named, waiting.OperatorAction.Key)
		}
	}
	if len(named) != 1 || named[0] != "run:run-272a" {
		t.Fatalf("operator findings = %v, want only the escalation nothing has ended", named)
	}

	standingActions, ended, problem := Escalations(runs, sources.Decisions,
		func(id string) EscalatedItem {
			if id == "yoyodyne-ifd.78" {
				return EscalatedItem{Admitted: true, Parked: "waits on the machine-home design"}
			}
			return EscalatedItem{Admitted: true}
		},
		Looking(context.Background(), sources.Remains, func() time.Time { return moment }))
	if problem != "" || len(standingActions) != 1 || len(ended) != 3 {
		t.Fatalf("Escalations() = %#v, %#v, %q, want one standing and three ended", standingActions, ended, problem)
	}
	byRun := map[string]EndedEscalation{}
	for _, one := range ended {
		byRun[one.RunID] = one
	}
	for run, want := range map[string]string{
		"run-78a":  "yoyodyne-ifd.78 was parked, so nothing about it waits on the operator: waits on the machine-home design",
		"run-310a": "run run-310a's branch and worktree are both gone",
		"run-320a": "yoyodyne-ifd.320 was parked",
	} {
		if !strings.Contains(byRun[run].Why, want) {
			t.Fatalf("ending of %s = %#v, want it to say %q", run, byRun[run], want)
		}
	}
	if !byRun["run-320a"].Recorded || byRun["run-78a"].Recorded {
		t.Fatalf("endings = %#v, want only the one the sweep recorded marked as recorded", ended)
	}

	// A look that never reached the repository ends nothing: a record is not a
	// look, and an escalation dropped on a removal flag is one the operator was
	// handed and never told the end of.
	_, ended, _ = Escalations(runs[:3], sources.Decisions, nil, Looking(context.Background(), nil, nil))
	if len(ended) != 0 {
		t.Fatalf("Escalations() with no repository = %#v, want nothing ended", ended)
	}
}

// survivingExcept is a repository holding every run's branch and worktree but
// the listed runs'.
type survivingExcept map[string]bool

func (s survivingExcept) Survives(_ context.Context, worktree gitworktree.Worktree) (gitworktree.Survival, error) {
	if s[worktree.RunID] {
		return gitworktree.Survival{}, nil
	}
	return gitworktree.Survival{BranchExists: true, WorktreePresent: true}, nil
}

// A finding only the operator can act on is named on the attention line, by
// name, ahead of the undecided proposals, and is never folded into "and N
// things not named here". Two records make one: a critical report nobody has
// handled, and a handling that says the report needs the operator's hand. A
// later handling of the same report that says nothing of the kind ends it.
func TestAFindingThatNeedsTheOperatorIsNamedAheadOfTheProposals(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	handled := filedReport("report-00000000000000000000000000000002", report.SeverityWarning, moment.Add(-3*time.Hour))
	handled.WorkItemID = "yoyodyne-ifd.383"
	sources.Reports = fakeReports{
		reports: []report.Report{
			filedReport("report-00000000000000000000000000000001", report.SeverityCritical, moment.Add(-2*time.Hour)),
			handled,
			filedReport("report-00000000000000000000000000000003", report.SeverityCritical, moment.Add(-time.Hour)),
		},
		handlings: []report.Handling{
			{ReportID: "report-00000000000000000000000000000002", Role: domain.RoleProductManager, RunID: "chat-1", Reason: "the operator has to add the hook to .claude/settings.json by hand", RecordedAt: moment.Add(-90 * time.Minute), NeedsOperator: true},
			// The third report was handled, so it is no longer a finding.
			{ReportID: "report-00000000000000000000000000000003", Role: domain.RoleProductManager, RunID: "chat-1", Reason: "admitted as yoyodyne-ifd.400", RecordedAt: moment.Add(-30 * time.Minute)},
		},
	}
	sources.Amendments = fakeAmendments{records: []amendment.Record{undecidedChange("proposal-1", moment.Add(-4*time.Hour))}}
	standing := ReadStanding(context.Background(), sources)

	var named []Attention
	proposal := -1
	for index, waiting := range standing.NeedsHuman {
		if waiting.Named() {
			named = append(named, waiting)
		}
		if strings.Contains(waiting.What(), "proposed and undecided") {
			proposal = index
		}
	}
	if len(named) != 2 {
		t.Fatalf("NeedsHuman = %#v, want the two findings named", standing.NeedsHuman)
	}
	if proposal < 0 {
		t.Fatalf("NeedsHuman = %#v, want the undecided proposal listed", standing.NeedsHuman)
	}
	for index, waiting := range standing.NeedsHuman {
		if waiting.Named() && index > proposal {
			t.Fatalf("finding %q is listed after the proposal at %d", waiting.What(), proposal)
		}
	}
	// Oldest first, in the order the pile was filed: the handled report was
	// filed before the critical one.
	handling, critical := named[0], named[1]
	if !strings.Contains(critical.What(), "report-00000000000000000000000000000001 needs your hand: something was noticed") ||
		!strings.Contains(critical.What(), "found by the developer, in a critical report") {
		t.Fatalf("critical finding = %q", critical.What())
	}
	if !strings.Contains(handling.What(), "report-00000000000000000000000000000002 needs your hand: the operator has to add the hook to .claude/settings.json by hand") ||
		!strings.Contains(handling.What(), "found by the Lead Product Manager, handling the report") ||
		!strings.Contains(handling.What(), "about yoyodyne-ifd.383") {
		t.Fatalf("handling finding = %q", handling.What())
	}
	for _, waiting := range named {
		if !strings.HasPrefix(waiting.Whose(), "the operator's") {
			t.Fatalf("finding %q is %q, want the operator's move", waiting.What(), waiting.Whose())
		}
	}

	// Rendered, the findings are listed by name however many other entries there
	// are: eleven proposals would fold the eleventh, and never a finding.
	var proposals []amendment.Record
	for index := 0; index < maxListed+1; index++ {
		proposals = append(proposals, undecidedChange(fmt.Sprintf("proposal-%d", index), moment.Add(-4*time.Hour)))
	}
	sources.Amendments = fakeAmendments{records: proposals}
	rendered := ReadStanding(context.Background(), sources).Render()
	for _, want := range []string{
		"report-00000000000000000000000000000001 needs your hand",
		"report-00000000000000000000000000000002 needs your hand",
		"and 1 thing waiting on somebody not named here",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered standing lacks %q:\n%s", want, rendered)
		}
	}
}

// The failure the whole report channel has: reports arriving faster than
// anything decides about them, which is invisible in any one reading and shows
// only as the oldest undecided report's age climbing.
func TestAPileNothingIsDrainingWaitsOnTheProductManager(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{
		reports: []report.Report{
			filedReport("report-00000000000000000000000000000001", report.SeverityWarning, moment.Add(-22*24*time.Hour)),
			filedReport("report-00000000000000000000000000000002", report.SeverityNote, moment.Add(-time.Hour)),
		},
		handlings: []report.Handling{{ReportID: "report-00000000000000000000000000000002"}},
	}
	standing := ReadStanding(context.Background(), sources)
	if standing.Reports.Unhandled != 1 || standing.Reports.OldestAge != 22*24*time.Hour {
		t.Fatalf("Reports = %#v", standing.Reports)
	}
	rendered := standing.Render()
	for _, want := range []string{"1 of 2 collected report(s) are unhandled", "22d ago", "the Lead Product Manager's"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered is missing %q:\n%s", want, rendered)
		}
	}
}

// A pile that could not be read is never reported as an empty one: "nobody has
// reported anything" and "nothing could read what anybody reported" send an
// operator in opposite directions.
func TestAPileThatCouldNotBeReadIsNotReportedAsEmpty(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Reports = fakeReports{fail: errors.New("the pile is unreadable")}
	standing := ReadStanding(context.Background(), sources)
	if standing.Reports.Collected != 0 || standing.ReportsProblem == "" {
		t.Fatalf("Reports = %#v, problem = %q", standing.Reports, standing.ReportsProblem)
	}
	if !strings.Contains(standing.Render(), "the pile is unreadable") {
		t.Fatalf("the failure never reached a line:\n%s", standing.Render())
	}
	// What became of the pile failing on its own is the same refusal: every report
	// would otherwise count as undecided, which overstates the backlog in the
	// direction that sends somebody to work on something already done.
	sources.Reports = fakeReports{
		reports:   []report.Report{filedReport("report-00000000000000000000000000000001", report.SeverityNote, moment)},
		handleErr: errors.New("the handling log is unreadable"),
	}
	standing = ReadStanding(context.Background(), sources)
	if standing.Reports.Unhandled != 0 || standing.ReportsProblem == "" {
		t.Fatalf("Reports = %#v, problem = %q", standing.Reports, standing.ReportsProblem)
	}
}

// A quiet harness still prints all four lines, and every one of them says
// "nothing" in words. This is the whole point of the format: silence is what an
// operator cannot read, so there is no state in which a line is simply absent.
func TestQuietHarnessPrintsAllFourLines(t *testing.T) {
	t.Parallel()
	standing := ReadStanding(context.Background(), quietSources())
	rendered := standing.Render()
	want := "Running: nothing\n" +
		"Working: nothing\n" +
		"Not startable: nothing, of no admitted items\n" +
		"Needs a human: nothing\n"
	if rendered != want {
		t.Fatalf("rendered:\n%s\nwant:\n%s", rendered, want)
	}
}

// The operator's own example, rendered from live state: a run with its item,
// phase, elapsed and spend; a conversation turn in flight; a refusal taken from
// the queue; and something waiting on a named person.
func TestTheOperatorsExampleRendersFromState(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:                 "run-a",
			WorkItemID:            "yoyodyne-ifd.194",
			WorkItemTitle:         "the four-line status",
			Backend:               "claude-code",
			ProviderModel:         "opus",
			ProviderResolvedModel: "claude-opus-5",
			ProviderEffort:        "medium",
			AccountAlias:          "default",
			Status:                runstate.StatusRunning,
			Phase:                 runstate.PhaseDeveloping,
			StartedAt:             moment.Add(-12 * time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.194": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 3.41}}},
		},
	}
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{{
			ConversationID: "chat-1",
			Agent:          "product-manager",
			Role:           domain.RoleProductManager,
			Backend:        "claude-code",
			ProviderModel:  "fable",
			ProviderEffort: "high",
			Turns:          270,
			UpdatedAt:      moment.Add(-40 * time.Second),
		}},
		held: map[string]bool{"product-manager": true},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"open": {
				{ID: "yoyodyne-ifd.194", Title: "the four-line status", Status: "open"},
				{ID: "yoyodyne-ifd.200", Title: "later work", Status: "open"},
			},
			"blocked": {{ID: "yoyodyne-ifd.201", Title: "waiting work", Status: "blocked"}},
		},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.194"}, {ID: "yoyodyne-ifd.200"}},
	}}
	// The blocked item is blocked because a run stopped on it and its change is
	// still on a branch. That is what the line names, rather than the status
	// field, which says the same word about work whose every blocker has closed.
	sources.Stoppages = fakeStoppages{runs: []runstate.State{{
		RunID:        "run-b",
		WorkItemID:   "yoyodyne-ifd.201",
		Status:       runstate.StatusFailed,
		UpdatedAt:    moment.Add(-24 * time.Hour),
		Branch:       "yoyodyne/yoyodyne-ifd-201/run-b",
		WorktreePath: "/state/worktrees/run-b",
		Blocker:      "Yoyodyne stopped this item: a configured check still failed after every permitted attempt.",
	}}}
	// And nothing has been decided about that stoppage, which is what makes it the
	// development manager's rather than the harness's: the two are named apart
	// wherever held work is counted.
	sources.Decisions = recordedDecisions{}
	// The repository holds the branch and the worktree the run left, which is
	// what the hold is decided from rather than the run's own removal flags.
	sources.Remains = &remainsOf{survives: map[string]gitworktree.Survival{"run-b": {BranchExists: true, WorktreePresent: true}}}
	sources.IntakeHolds = fakeIntakeHolds{
		hold: runstate.IntakeHold{HeldAt: moment.Add(-2 * time.Hour), HeldBy: runstate.IntakeHolderOperator, Reason: "the overnight looked wrong"},
		held: true,
	}

	rendered := ReadStanding(context.Background(), sources).Render()
	for _, want := range []string{
		"Running (1 developer run):\n",
		// The effort level is said beside the model it was asked of.
		"  yoyodyne-ifd.194 — developing, on claude-opus-5 at medium effort, 12m elapsed, $3.41 so far\n",
		"Working (1 conversation):\n",
		"  product-manager — product-manager, on fable at high effort, a turn in flight for 40s after 270 recorded turns\n",
		"Not startable (2 of 3 admitted items; 1 awaits the development manager's decision):\n",
		"  yoyodyne-ifd.200 — intake is held, and the operator placed it — the overnight looked wrong; `yoyo release` lifts it\n",
		// The whole line, because docs/operations.md prints it as the example an
		// operator reads: a wording change has to break the document and the test
		// together rather than leaving the two saying different things.
		// It opens with when the item was held, in the machine's own zone, and how
		// long ago that was, read from the run's stop.
		"  yoyodyne-ifd.201 — held since " + localMoment(moment.Add(-24*time.Hour)) + ", 24 hours ago; run run-b stopped on it and its change is preserved (branch and worktree checked and there), so a fresh run would start over on top of work that is still there; the development manager decides what happens to it, and nothing pulls it until she has\n",
		// Only the operator's entry is under the line named for a human; the
		// development manager's is under a line naming her.
		"Needs a human (1):\n  intake is held, since 2026-08-30T10:00:00Z: the operator placed it — the overnight looked wrong — the operator's",
		"Waiting on the development manager (1):\n  1 admitted item awaits the development manager's decision — the development manager's",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The item a run is already carrying is on the running line and nowhere else.
	if strings.Count(rendered, "yoyodyne-ifd.194") != 1 {
		t.Fatalf("the running item is named more than once:\n%s", rendered)
	}

	// The same reading carries what a card shows and a line does not: the title
	// and what the run is spending, with the resolved model preferred over the
	// selector; and each refusal's kind, so a pipeline is counted from the
	// reading that worded it.
	standing := ReadStanding(context.Background(), sources)
	run := standing.Running[0]
	if run.Title != "the four-line status" || run.Backend != "claude-code" || run.Model != "claude-opus-5" || run.Effort != "medium" || run.Account != "default" {
		t.Fatalf("running run carries %+v", run)
	}
	if turn := standing.Working[0]; turn.Backend != "claude-code" || turn.Model != "fable" || turn.Effort != "high" {
		t.Fatalf("working turn carries %+v", turn)
	}
	kinds := map[string]backlog.HoldKind{}
	for _, refused := range standing.NotStartable {
		kinds[refused.WorkItemID] = refused.Kind
	}
	if kinds["yoyodyne-ifd.200"] != backlog.HeldByStall || kinds["yoyodyne-ifd.201"] != backlog.HeldForAPerson {
		t.Fatalf("refusal kinds = %v", kinds)
	}
}

// A run promoting again after the environment stopped it says so on the running
// line in the read model's own words, in place of the bare phase: the approval
// stood and nothing was spent, which is what an operator who signed overrides
// for such stops is reading the line for.
func TestARunResumedAtItsPromotionSaysSoOnTheRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:           "run-a",
			WorkItemID:      "yoyodyne-ifd.309",
			Status:          runstate.StatusRunning,
			Phase:           runstate.PhaseIntegrating,
			StartedAt:       moment.Add(-3 * time.Hour),
			ReviewDecision:  runstate.ReviewApprove,
			ReviewSessionID: "reviewer-session",
			IntegrationResumptions: []runstate.IntegrationResumption{{
				Cause: runstate.CauseDirtyPrimary, Reason: "resumed", ResumedAt: moment.Add(-time.Minute),
			}},
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.309": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 12.50}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || !standing.Running[0].ResumingIntegration {
		t.Fatalf("running = %+v, want the resumed run marked as resuming its integration", standing.Running)
	}
	rendered := standing.Render()
	want := "  yoyodyne-ifd.309 — " + runstate.ResumingIntegrationSays + ", 3h00m elapsed, $12.50 so far\n"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
	}
}

// runsWithPresence is a run source that can say whether a process is behind each
// run, as the store the harness wires can.
type runsWithPresence struct {
	fakeRuns
	missing map[string]string
}

func (f runsWithPresence) Presence(state runstate.State, _ time.Duration, _ time.Time) (runstate.RunPresence, error) {
	if says, gone := f.missing[state.RunID]; gone {
		return runstate.RunPresence{Says: says}, nil
	}
	return runstate.RunPresence{Found: true}, nil
}

// A run with no process behind it is still in flight and still in its slot, so
// it stays on the running line — but the line says nothing is running it, in
// the head the channel carries as well as against the run, rather than printing
// the phase the dead process last wrote. run-3b94404c read "checking" in slot 1
// for twenty hours on 2026-09-26 with nothing behind it.
func TestARunWithNoProcessBehindItIsNamedAsSuchOnTheRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = runsWithPresence{
		fakeRuns: fakeRuns{incomplete: []runstate.State{
			{RunID: "run-a", WorkItemID: "yoyodyne-ifd.428.34", Status: runstate.StatusRunning, Phase: runstate.PhaseChecking, StartedAt: moment.Add(-20 * time.Hour)},
			{RunID: "run-b", WorkItemID: "yoyodyne-ifd.194", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-12 * time.Minute)},
		}},
		missing: map[string]string{"run-a": "no process holds it, and nothing has been written to it since 2026-08-29T16:05:00Z"},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 2 || standing.Running[0].NoProcess == "" || standing.Running[1].NoProcess != "" {
		t.Fatalf("running = %+v, want only the first run named as having no process", standing.Running)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Running (2 developer runs, 1 with no process behind it):\n",
		"  yoyodyne-ifd.428.34 — no process can be found behind it: no process holds it, and nothing has been written to it since 2026-08-29T16:05:00Z; recorded as checking; `yoyo reconcile` settles it — a parked run once its record has not moved for 30m0s — and `yoyo run yoyodyne-ifd.428.34` continues it before then, 20h00m elapsed",
		"  yoyodyne-ifd.194 — developing, 12m elapsed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
}

// A run in its checks says where the stage stands in place of the bare phase:
// what it has spent of the bound, and which check it is on. That is the line
// the 2026-09-19 stage would have been visible on — two hours into a stage
// nothing said the bound of — and it is derived from the record rather than
// from the run's own elapsed time, because the stage starts long after the run
// does.
func TestARunInItsChecksSaysWhereTheStageStandsOnTheRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:      "run-a",
			WorkItemID: "yoyodyne-ifd.389",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseChecking,
			StartedAt:  moment.Add(-time.Hour),
			CheckStage: &runstate.CheckStage{
				StartedAt:    moment.Add(-14 * time.Minute),
				BoundSeconds: int64((30 * time.Minute) / time.Second),
				Command:      "make race",
			},
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.389": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 4.00}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].Checks != "checks: 14m of 30m, on make race" {
		t.Fatalf("running = %+v, want the check stage said as it stands", standing.Running)
	}
	rendered := standing.Render()
	want := "  yoyodyne-ifd.389 — checks: 14m of 30m, on make race, 1h00m elapsed, $4.00 so far\n"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
	}

	// A stage that has ended says nothing here: the run has moved on and the
	// phase is what it is doing now.
	finished := moment
	sources.Runs.(fakeRuns).incomplete[0].CheckStage.FinishedAt = &finished
	sources.Runs.(fakeRuns).incomplete[0].Phase = runstate.PhaseReviewing
	standing = ReadStanding(context.Background(), sources)
	if standing.Running[0].Checks != "" {
		t.Fatalf("running = %+v, want nothing said of a stage that has ended", standing.Running)
	}
}

// A conversation turn in flight is what no surface counted before this. It is
// the lease that decides, so a recorded conversation nobody is holding is not
// working.
func TestWorkingCountsOnlyHeldConversations(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Agent: "architect", Role: domain.RoleArchitect, Turns: 4, UpdatedAt: moment},
			{ConversationID: "chat-2", Agent: "reviewer", Role: domain.RoleReviewer, Turns: 9, UpdatedAt: moment},
		},
		held: map[string]bool{"architect": true},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 || standing.Working[0].Agent != "architect" {
		t.Fatalf("working = %+v, want only the held conversation", standing.Working)
	}
}

// A conversation record written before the agent was part of the identity is
// probed under the agent named for its role, which is where it actually lives.
func TestWorkingProbesRoleNamedAgentForOlderRecords(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Role: domain.RoleArchitect, Turns: 2, UpdatedAt: moment},
		},
		held: map[string]bool{"architect": true},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 {
		t.Fatalf("working = %+v, want the record probed under its role's agent name", standing.Working)
	}
}

// A source that cannot be read never says "nothing". It says the harness does
// not know, which is a different answer and the one a reader acts on.
func TestAnUnreadableSourceIsNeverReportedAsNothing(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{failIncomplete: errors.New("the state directory would not open")}
	sources.Tracker = statusTracker{fakeTracker{fail: errors.New("the tracker did not answer")}}

	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	if !strings.Contains(rendered, "Running: could not be read — the runs in flight could not be read: the state directory would not open\n") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Not startable: could not be read — ") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "Running: nothing") || strings.Contains(rendered, "Not startable: nothing") {
		t.Fatalf("an unreadable source was reported as nothing:\n%s", rendered)
	}
}

// A switch nobody could read is never reported as clear either: the line that
// would otherwise say what holds work back says it could not tell.
func TestAnUnreadableSwitchIsSaidRatherThanAssumedClear(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.IntakeHolds = fakeIntakeHolds{fail: errors.New("the hold file would not open")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.NotStartableProblem, "the intake hold could not be read") {
		t.Fatalf("not-startable problem = %q", standing.NotStartableProblem)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "the intake hold could not be read") {
		t.Fatalf("needs-a-human problem = %q", standing.NeedsHumanProblem)
	}
}

// A run whose evidence cannot be priced is stated as unpriceable rather than as
// free. A figure of zero against an hour of provider work is the one number this
// may not print.
func TestAnUnpriceableRunIsNotFree(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-a", WorkItemID: "item-1", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour),
		}},
		prices: map[string]runstate.ItemPrice{
			"item-1": {Runs: []runstate.RunPrice{{RunID: "run-a", Unknown: "its event log is gone"}}},
		},
	}
	rendered := ReadStanding(context.Background(), sources).Render()
	if !strings.Contains(rendered, "cost unknown (its event log is gone)") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "$0.00") {
		t.Fatalf("an unpriceable run was reported as free:\n%s", rendered)
	}
}

// Nothing choosing work is a refusal in its own right. It is the state the
// overnight was in from midnight, and the one that reads exactly like a healthy
// quiet machine unless it is said.
func TestNoSessionChoosingIsARefusal(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-3 * time.Hour)},
		{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-2 * time.Hour)},
	}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "no watch session is running") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	// It is also waiting on somebody. A queue nobody is pulling from will wait
	// forever without a person, and the attention line is where a person looks.
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose(), "`yoyo work --watch`") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// A session that stopped while another carries on watching is not the product
// having stopped. The fold is per session, so the last line of the log never
// decides on its own.
func TestChoosingReadsTheLogPerSession(t *testing.T) {
	t.Parallel()
	sessions := []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-3 * time.Hour)},
		{SessionID: "watch-2", State: runstate.WatchWatching, At: moment.Add(-2 * time.Hour)},
		{SessionID: "watch-2", State: runstate.WatchStopped, At: moment.Add(-time.Hour)},
	}
	choosing := Choosing(sessions)
	if len(choosing) != 1 || choosing[0].SessionID != "watch-1" {
		t.Fatalf("choosing = %+v, want only the session still watching", choosing)
	}
	// An idle session is alive and choosing nothing, which is opposite answers to
	// the two questions the same fold serves.
	idle := []runstate.WatchTransition{{SessionID: "watch-3", State: runstate.WatchIdle, At: moment}}
	if len(Choosing(idle)) != 0 {
		t.Fatalf("an idle session was reported as choosing work")
	}
	if len(Live(idle)) != 1 {
		t.Fatalf("an idle session was reported as gone")
	}
}

// The state that read as a stopped machine, replayed against the four lines: a
// watch session idle on one developer slot while a run works on the other, over
// a queue whose only unstarted work is the architect's to carry.
//
// The run is on the Running line, which is what keeps idle-on-one-slot from
// reading as system-idle. And nothing here is waiting on the operator: the items
// are refused for what they are rather than for a session that has stopped, so
// the only attention is the architect's conversation. An operator sent to look
// at the session, or at an admission, was sent somewhere nothing would change.
func TestARunInFlightBesideWorkAConversationCarriesIsNotAStalledLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-236", WorkItemID: "yoyodyne-ifd.236", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-20 * time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{},
	}
	architects := []beads.WorkItem{
		{ID: "yoyodyne-ifd.212", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.203", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": architects},
		ready:    architects,
	}}
	// The session is alive and choosing nothing, which is exactly the log the
	// misleading line was written from.
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].WorkItemID != "yoyodyne-ifd.236" {
		t.Fatalf("running = %+v, want the run in flight on the running line", standing.Running)
	}
	for _, refused := range standing.NotStartable {
		if strings.Contains(refused.Reason, "found nothing it can start") {
			t.Fatalf("not startable = %+v, want each item refused for what it is rather than for the session", standing.NotStartable)
		}
	}
	if len(standing.NeedsHuman) != len(architects) {
		t.Fatalf("needs a human = %+v, want only the conversations that carry the work", standing.NeedsHuman)
	}
	for _, attention := range standing.NeedsHuman {
		if !strings.Contains(attention.Whose(), "the architect's") {
			t.Fatalf("needs a human = %+v, want the architect named rather than the operator", standing.NeedsHuman)
		}
	}
}

// The 2026-09-05 window against the four lines. The operator's oldest standing
// request on this system was a clear message when the harness is suspended
// pending a token window, and until now `yoyo status` said the same thing about
// it that it said about an empty afternoon: the session had found nothing it
// could start, and it was the operator's to look at.
func TestAProviderWindowIsTheRefusalRatherThanAnIdleSession(t *testing.T) {
	t.Parallel()
	lifts := moment.Add(time.Hour)
	sources := quietSources()
	waiting := []beads.WorkItem{{ID: "yoyodyne-ifd.290", Status: "open"}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": waiting},
		ready:    waiting,
	}}
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-2 * time.Hour)},
		{
			SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-30 * time.Minute),
			ProviderWindow: true, ProviderWindowResetsAt: &lifts,
		},
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 {
		t.Fatalf("not startable = %+v, want the ready item refused for the window", standing.NotStartable)
	}
	said := "Paused on the provider's usage window until " + lifts.Format("15:04") + "Z"
	if standing.NotStartable[0].Reason != said {
		t.Fatalf("refusal = %q, want the window said with the reset time", standing.NotStartable[0].Reason)
	}
	// Nobody has a move, so it is not on the attention line. Putting it there
	// would be telling somebody to act on a wait nothing they do can shorten,
	// which is what the old idle clause did.
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("needs a human = %+v, want a provider window waiting on nobody", standing.NeedsHuman)
	}
	// And the operator's own acceptance, which is about where the cause is rather
	// than only that it is there: when the system is paused on a provider usage
	// window, the cause is the first words of any message that reaches him. A
	// reading is one of those messages, and a refusal three lines down beside one
	// item is not the first words of it.
	if standing.Paused != said {
		t.Fatalf("paused = %q, want the window carried above the four lines", standing.Paused)
	}
	if rendered := standing.Render(); !strings.HasPrefix(rendered, said+"\n") {
		t.Fatalf("the reading opens %q, want it to open with the cause", firstLine(rendered))
	}
	// And the four lines alone, for the one message whose own first sentence is
	// already the banner: saying it twice in one message is repetition rather
	// than emphasis.
	if lines := standing.RenderLines(); !strings.HasPrefix(lines, "Running:") {
		t.Fatalf("the four lines open %q, want the banner left off them", firstLine(lines))
	}
}

// And the banner is only ever the window. Every other reason nothing is being
// chosen renders inside the four lines, which is what keeps this from becoming a
// second place a state can be reported from.
func TestAnIdleSessionPutsNothingAboveTheFourLines(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	waiting := []beads.WorkItem{{ID: "yoyodyne-ifd.290", Status: "open"}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": waiting},
		ready:    waiting,
	}}
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-2 * time.Hour)},
		{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-30 * time.Minute)},
	}}

	standing := ReadStanding(context.Background(), sources)
	if standing.Paused != "" {
		t.Fatalf("paused = %q, want nothing above the four lines but a provider window", standing.Paused)
	}
	if rendered := standing.Render(); !strings.HasPrefix(rendered, "Running:") {
		t.Fatalf("the reading opens %q, want the four lines", firstLine(rendered))
	}
}

// firstLine is what a reader actually sees first, which is what the acceptance
// above is about.
func firstLine(rendered string) string {
	line, _, _ := strings.Cut(rendered, "\n")
	return line
}

// Every developer slot taken refuses nothing: the ready item behind it is the
// next one started, so it is counted as startable and on a line of its own, and
// never inside the count of work that is not startable. On 2026-09-27 forty-five
// such items were filed as work nothing would pull.
func TestReadyWorkBehindAFullMachineIsCountedApartFromNotStartable(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-a", WorkItemID: "item-0", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want the ready item left out of it", standing.NotStartable)
	}
	for _, group := range standing.NotStartableGroups {
		if group.Kind == backlog.HeldByStall {
			t.Fatalf("groups = %+v, want no ready item filed as stalled", standing.NotStartableGroups)
		}
	}
	waiting := standing.WaitingForSlot
	if waiting == nil || waiting.Ready != 1 || waiting.Slots != 1 || len(waiting.Items) != 1 || waiting.Items[0].WorkItemID != "item-1" {
		t.Fatalf("waiting for a slot = %+v, want item-1 behind the one slot", waiting)
	}
	if standing.Startable != 1 {
		t.Fatalf("startable = %d, want the ready item counted as what is started next", standing.Startable)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Not startable: nothing, of 1 admitted item\n",
		"  - 1 ready, waiting for a developer slot; 1 slot, taken — not counted as not startable",
		"  - nothing here is the operator's: under his rule of 2026-09-26 only a change to the fundamental goals is, and nothing here waits on one\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered lacks %q:\n%s", want, rendered)
		}
	}
	if brief := standing.RenderBrief(); !strings.Contains(brief, "1 ready, waiting for a developer slot") {
		t.Fatalf("the hourly rendering drops the slot wait:\n%s", brief)
	}
}

// An item the harness would start next is not on the not-startable line at all.
// The line means what it says, and a startable item listed with a reason nobody
// could act on is what makes a listing stop being read.
func TestStartableWorkIsNotListedAsRefused(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want nothing", standing.NotStartable)
	}
	if standing.Admitted != 1 || standing.Startable != 1 {
		t.Fatalf("admitted = %d, startable = %d, want the startable item counted as both", standing.Admitted, standing.Startable)
	}
	if !strings.Contains(standing.Render(), "Not startable: nothing, of 1 admitted item\n") {
		t.Fatalf("rendered:\n%s", standing.Render())
	}
}

// The startable count is the other side of the refusals: an item a run is
// carrying is neither, a refused item is not startable, and once the pass-level
// stall stands nothing is startable at all, because a stall is every pullable
// item refused at once. A surface subtracting one list from another would get
// every one of those wrong.
func TestStartableIsCountedFromTheSameEntriesAsTheRefusals(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{incomplete: []runstate.State{{
		RunID: "run-a", WorkItemID: "item-carried", Status: runstate.StatusRunning, Phase: runstate.PhaseReviewing, StartedAt: moment.Add(-time.Minute),
	}}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"open": {
				{ID: "item-carried", Status: "open"},
				{ID: "item-next", Status: "open"},
				{ID: "item-after", Status: "open"},
				{ID: "item-parked", Status: "open", Parking: domain.WorkItemParking("later")},
			},
		},
		ready: []beads.WorkItem{{ID: "item-carried"}, {ID: "item-next"}, {ID: "item-after"}, {ID: "item-parked"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if standing.Admitted != 4 || standing.Startable != 2 || len(standing.NotStartable) != 1 {
		t.Fatalf("admitted %d, startable %d, refused %+v", standing.Admitted, standing.Startable, standing.NotStartable)
	}
	if standing.Running[0].Stage != StageReviewing {
		t.Fatalf("the reviewing run's stage is %q", standing.Running[0].Stage)
	}
	// The counts are taken over named lists, so a surface that opens a grouping
	// lists exactly what the figure counted: every admitted item once, the
	// carried one among them, and the startable ones in the queue's order.
	ids := func(items []WorkItemRef) []string {
		named := make([]string, 0, len(items))
		for _, item := range items {
			named = append(named, item.WorkItemID)
		}
		return named
	}
	if got := ids(standing.AdmittedItems); strings.Join(got, ",") != "item-carried,item-next,item-after,item-parked" {
		t.Fatalf("admitted items = %v, want every entry in the queue's order", got)
	}
	if got := ids(standing.StartableItems); strings.Join(got, ",") != "item-next,item-after" {
		t.Fatalf("startable items = %v, want the two nothing refuses", got)
	}

	// The same queue under the operator's hold: every pullable item is refused
	// by the stall, and nothing is startable — an empty list rather than an
	// absent one, because the queue was read.
	sources.OperatorHolds = fakeOperatorHolds{hold: runstate.OperatorHold{HeldAt: moment.Add(-time.Hour)}, held: true}
	held := ReadStanding(context.Background(), sources)
	if held.Startable != 0 || len(held.NotStartable) != 3 || held.StartableItems == nil || len(held.StartableItems) != 0 {
		t.Fatalf("under a hold: startable %d (%v), refused %+v", held.Startable, held.StartableItems, held.NotStartable)
	}
	for _, refused := range held.NotStartable {
		if refused.WorkItemID != "item-parked" && refused.Kind != backlog.HeldByStall {
			t.Fatalf("%s is refused as %q rather than by the stall", refused.WorkItemID, refused.Kind)
		}
	}
}

// Every phase folds onto one of the three stages a pipeline shows, and the
// stage is the model's rather than a list a page keeps.
func TestStageOfFoldsEveryPhase(t *testing.T) {
	t.Parallel()
	for phase, stage := range map[runstate.Phase]Stage{
		"":                        StageDeveloping,
		runstate.PhaseDeveloping:  StageDeveloping,
		runstate.PhaseChecking:    StageDeveloping,
		runstate.PhaseReviewing:   StageReviewing,
		runstate.PhaseIntegrating: StageIntegrating,
		runstate.PhaseCompleting:  StageIntegrating,
		runstate.PhaseCleaningUp:  StageIntegrating,
		runstate.PhaseComplete:    StageIntegrating,
	} {
		if got := StageOf(phase); got != stage {
			t.Errorf("StageOf(%q) = %q, want %q", phase, got, stage)
		}
	}
}

// An unresolved directive that pauses an item is the real refusal for it, and it
// is what the pipeline would refuse the work with.
func TestADirectivePauseIsTheItemsOwnRefusal(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID:         "dir-1",
		Kind:       directive.KindAmbiguous,
		Text:       "which branch does this land on?",
		Unresolved: "which branch does this land on?",
		Scope:      []string{"item-1"},
	}}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {
			{ID: "item-1", Status: "open"},
			{ID: "item-2", Status: "open"},
		}},
		ready: []beads.WorkItem{{ID: "item-1"}, {ID: "item-2"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 {
		t.Fatalf("not startable = %+v, want only the paused item", standing.NotStartable)
	}
	if !strings.Contains(standing.NotStartable[0].Reason, "paused for unresolved directive dir-1") || standing.NotStartable[0].Kind != backlog.HeldByDirective {
		t.Fatalf("refusal = %+v", standing.NotStartable[0])
	}
	// The same directive is a thing waiting on a person, with whose move it is.
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose(), "the operator's") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// What holds one item is asked for on its own by a surface that has to name the
// pauses rather than count them, and it is the same reading the four lines are
// assembled from: in force, reaching this item, and a directive that named no
// scope reaching it too.
func TestPausingIsWhatHoldsOneItem(t *testing.T) {
	t.Parallel()
	settled := moment
	sources := quietSources()
	sources.Directives = fakeDirectives{recorded: []directive.Directive{
		{
			ID: "dir-1", Kind: directive.KindAmbiguous, ReceivedAt: moment.Add(-2 * time.Hour),
			Text: "which branch", Unresolved: "which branch", Scope: []string{"item-1"},
		},
		{
			ID: "dir-2", Kind: directive.KindArtifact, ReceivedAt: moment.Add(-time.Hour),
			Text: "the design changes", Artifact: "slack-reporting-design",
			Unresolved: "whether product threads may carry directives",
		},
		{
			ID: "dir-3", Kind: directive.KindAmbiguous, ReceivedAt: moment,
			Text: "which branch", Unresolved: "which branch", Scope: []string{"item-2"},
		},
		{
			ID: "dir-4", Kind: directive.KindAmbiguous, ReceivedAt: moment,
			Text: "answered already", Unresolved: "answered already", Scope: []string{"item-1"},
			Resolution: "the target branch", ResolvedAt: &settled,
		},
		{
			ID: "dir-5", Kind: directive.KindOperational, ReceivedAt: moment,
			Text: "prefer the smaller change", Scope: []string{"item-1"},
		},
	}}

	held, err := Pausing(sources, "item-1")
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(held) != 2 || held[0].ID != "dir-1" || held[1].ID != "dir-2" {
		t.Fatalf("Pausing() = %+v, want the scoped and the unscoped pause, in the order they were received", held)
	}

	sources.Directives = fakeDirectives{fail: errors.New("the record could not be read")}
	if _, err := Pausing(sources, "item-1"); err == nil {
		t.Fatal("Pausing() error = nil, want a record that could not be read to say so rather than read as nothing held")
	}
	sources.Directives = nil
	if _, err := Pausing(sources, "item-1"); err == nil {
		t.Fatal("Pausing() error = nil, want an unwired source to say so rather than read as nothing held")
	}
}

// A run that ended still owing a step waits forever without somebody running the
// sweep, so it is named with the command that settles it.
func TestAnEndedRunOwingCleanupWaitsOnTheHarness(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		prices:      map[string]runstate.ItemPrice{},
		outstanding: []runstate.State{{RunID: "run-a", WorkItemID: "item-1", Status: runstate.StatusSucceeded, Phase: runstate.PhaseCleaningUp}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
	if !strings.Contains(standing.NeedsHuman[0].Whose(), "yoyo reconcile") {
		t.Fatalf("whose = %q, want the command that settles it", standing.NeedsHuman[0].Whose())
	}
	if standing.NeedsHuman[0].Mover != MoverHarness {
		t.Fatal("cleanup must be the harness's")
	}
}

type heldRuns struct {
	fakeRuns
	held    map[string]bool
	problem error
}

func (f heldRuns) Held(id string) (bool, error) { return f.held[id], f.problem }

func TestTheAttentionLineExcludesLiveRunsAndNamesTheStepAndMover(t *testing.T) {
	t.Parallel()
	ended := moment.Add(-time.Hour)
	live := runstate.State{RunID: "run-live", WorkItemID: "checks", Status: runstate.StatusRunning, Phase: runstate.PhaseChecking}
	cleanup := runstate.State{RunID: "run-cleanup", WorkItemID: "cleanup", Status: runstate.StatusSucceeded, Phase: runstate.PhaseCleaningUp, CompletedAt: &ended, Integration: &runstate.Integration{TargetBranch: "main"}, CleanupFailure: "remove worktree: directory busy"}
	queued := cleanup
	queued.RunID, queued.WorkItemID, queued.Phase = "run-queued", "queued", runstate.PhaseComplete
	queued.PullRequest = &runstate.PullRequest{Number: 700, MergeQueued: true, Checks: &runstate.PullRequestChecks{Failing: []runstate.FailingCheck{{Name: "build", Conclusion: "failure"}}}}
	dropped := queued
	dropped.RunID, dropped.WorkItemID = "run-dropped", "dropped"
	dropped.Phase = runstate.PhaseCleaningUp
	dropped.PullRequest = &runstate.PullRequest{Number: 732, Checks: queued.PullRequest.Checks}
	dropped.MergeDrop = &runstate.MergeDrop{At: ended, Reason: "build failed"}
	if !dropped.Outstanding() {
		t.Fatal("the dropped local merge must still owe cleanup")
	}
	landing := cleanup
	landing.RunID = "run-live-landing"
	landing.LandingChecks = &runstate.LandingChecks{StartedAt: ended}
	superseded := dropped
	superseded.RunID = "run-superseded"
	superseded.Phase = runstate.PhaseComplete
	superseded.PullRequest = &runstate.PullRequest{Number: 751, MergeQueued: true, Superseded: "pull request 752"}
	sources := quietSources()
	sources.Runs = heldRuns{fakeRuns: fakeRuns{outstanding: []runstate.State{live, cleanup, queued, dropped, landing, superseded}, recorded: []runstate.State{live, cleanup, queued, dropped, superseded}}, held: map[string]bool{live.RunID: true, landing.RunID: true}}
	standing := ReadStanding(context.Background(), sources)
	seen := map[string]int{}
	droppedKinds := map[AttentionKind]int{}
	for _, entry := range standing.NeedsHuman {
		seen[entry.ID]++
		if entry.Mover == MoverOperator {
			t.Fatalf("harness work reached the operator: %+v", entry)
		}
		switch entry.ID {
		case cleanup.RunID:
			if entry.Mover != MoverHarness || entry.Label() != "run not finished" || !strings.Contains(entry.What(), "cleanup of the branch and worktree") || !strings.Contains(entry.What(), "directory busy") || !strings.Contains(entry.Whose(), "finishes the run's cleanup") {
				t.Fatalf("cleanup entry = %+v", entry)
			}
		case queued.RunID:
			if entry.Mover != MoverHarness || entry.Label() != "merge stuck" || !strings.Contains(entry.What(), "build") || !strings.Contains(entry.Whose(), "withdraws the merge") {
				t.Fatalf("queued entry = %+v", entry)
			}
		case dropped.RunID:
			droppedKinds[entry.Kind]++
			if entry.Kind == AttentionOwedStep {
				if entry.Mover != MoverHarness || entry.Label() != "run not finished" || !strings.Contains(entry.What(), "cleanup of the branch and worktree") || !strings.Contains(entry.Whose(), "finishes the run's cleanup") || strings.Contains(entry.What()+entry.Whose(), "merge") {
					t.Fatalf("dropped merge's cleanup entry = %+v", entry)
				}
			} else if entry.Kind != AttentionPublication || entry.Mover != MoverDevelopmentManager || entry.Label() != "merge stuck" || !strings.Contains(entry.What(), "was dropped by the forge") || !strings.Contains(entry.Whose(), "forge dropped") {
				t.Fatalf("dropped merge's decision entry = %+v", entry)
			}
		default:
			t.Fatalf("live or superseded run on attention line: %+v", entry)
		}
	}
	if seen[cleanup.RunID] != 1 || seen[queued.RunID] != 2 || seen[dropped.RunID] != 2 {
		t.Fatalf("attention entries = %v", seen)
	}
	if droppedKinds[AttentionOwedStep] != 1 || droppedKinds[AttentionPublication] != 1 {
		t.Fatalf("dropped merge must carry one cleanup entry and one decision entry: %v", droppedKinds)
	}
	sources.Runs = heldRuns{fakeRuns: fakeRuns{outstanding: []runstate.State{cleanup}}, problem: errors.New("holder unreadable")}
	standing = ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 0 || !strings.Contains(standing.NeedsHumanProblem, "holder unreadable") {
		t.Fatalf("unreadable holder was treated as dead: %+v", standing)
	}
}

func TestObsoletePublicationsKeepIndependentRunSteps(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{"superseded", "handed back"} {
		for _, obligation := range []string{"cleanup", "landing", "none"} {
			t.Run(marker+"/"+obligation, func(t *testing.T) {
				pr := &runstate.PullRequest{Number: 751, MergeQueued: true}
				if marker == "superseded" {
					pr.Superseded = "pull request 752"
				} else {
					pr.HandedBack = &runstate.PublicationHandBack{At: moment}
				}
				state := runstate.State{RunID: "run-obsolete", WorkItemID: "item", Status: runstate.StatusSucceeded, Phase: runstate.PhaseComplete, CompletedAt: &moment, Integration: &runstate.Integration{TargetBranch: "main"}, PullRequest: pr}
				wantWhat, wantWhose := "", ""
				switch obligation {
				case "cleanup":
					state.Phase = runstate.PhaseCleaningUp
					state.CleanupFailure = "directory busy"
					wantWhat, wantWhose = "cleanup of the branch and worktree", "finishes the run's cleanup"
				case "landing":
					state.Integration.ThroughPullRequest = true
					state.LandingChecks = &runstate.LandingChecks{StartedAt: moment}
					wantWhat, wantWhose = "landing checks", "records the interrupted landing as unverified"
				}
				if !state.Outstanding() {
					t.Fatal("fixture must be included by Outstanding, even when only an obsolete queue flag remains")
				}
				sources := quietSources()
				sources.Runs = heldRuns{fakeRuns: fakeRuns{outstanding: []runstate.State{state}, recorded: []runstate.State{state}}}
				standing := ReadStanding(context.Background(), sources)
				if obligation == "none" {
					if len(standing.NeedsHuman) != 0 {
						t.Fatalf("obsolete merge is still awaited: %+v", standing.NeedsHuman)
					}
					return
				}
				if len(standing.NeedsHuman) != 1 {
					t.Fatalf("want only the independent run step: %+v", standing.NeedsHuman)
				}
				entry := standing.NeedsHuman[0]
				if entry.Kind != AttentionOwedStep || entry.ID != state.RunID || entry.Mover != MoverHarness || entry.Label() != "run not finished" || !strings.Contains(entry.What(), wantWhat) || !strings.Contains(entry.Whose(), wantWhose) || strings.Contains(entry.What()+entry.Whose(), "merge") {
					t.Fatalf("wrong remaining step: %+v; what %q; whose %q", entry, entry.What(), entry.Whose())
				}
				if !pr.MergeQueued || entry.OwedStep.PullRequest != pr {
					t.Fatal("projection must retain the original publication record")
				}
			})
		}
	}
}

// A promotion the forge has not published is on the attention line by the same
// predicate the channel's heartbeat counts it by, so the hourly "N promotions
// awaiting the forge" and the four lines name the same runs. A run settled after
// a dropped merge is the case that separated them: it owes no step, so the
// outstanding listing does not have it, and its publication is still not on the
// remote. The three movers are three different entries.
func TestAPromotionAwaitingTheForgeNeedsAHuman(t *testing.T) {
	t.Parallel()
	promoted := func(runID string, published runstate.PullRequest, drop *runstate.MergeDrop) runstate.State {
		return runstate.State{
			RunID:       runID,
			WorkItemID:  "item-" + runID,
			Status:      runstate.StatusSucceeded,
			Integration: &runstate.Integration{TargetBranch: "main"},
			PullRequest: &published,
			MergeDrop:   drop,
		}
	}
	dropped := promoted("run-dropped", runstate.PullRequest{Number: 401, URL: "https://forge.invalid/pull/401"}, &runstate.MergeDrop{At: moment, Reason: "a requirement went unmet"})
	queued := promoted("run-queued", runstate.PullRequest{Number: 402, MergeQueued: true}, nil)
	unasked := promoted("run-unasked", runstate.PullRequest{Number: 403}, nil)
	merged := promoted("run-merged", runstate.PullRequest{Number: 404, Merged: true}, nil)
	running := promoted("run-running", runstate.PullRequest{Number: 405}, nil)
	running.Status = runstate.StatusRunning
	// The fourth: a promotion whose record holds no request at all, named from
	// the record's own account of that before any sweep has asked the forge. A
	// local promotion carries no such account and is not counted.
	unrecorded := promoted("run-unrecorded", runstate.PullRequest{}, nil)
	unrecorded.PullRequest = nil
	unrecorded.Branch = "yoyodyne/item/unrecorded"
	unrecorded.ReviewDecision = runstate.ReviewApprove
	unrecorded.PublishFailure = runstate.LostPublication("run-unrecorded", "item-run-unrecorded", "main", unrecorded.Branch)
	local := promoted("run-local", runstate.PullRequest{}, nil)
	local.PullRequest = nil
	local.Branch = "yoyodyne/item/local"
	local.ReviewDecision = runstate.ReviewApprove
	recorded := []runstate.State{dropped, queued, unasked, merged, running, unrecorded, local}

	// The heartbeat's count and the attention line's entries are one derivation.
	if awaiting := AwaitingForge(recorded); len(awaiting) != 4 {
		t.Fatalf("AwaitingForge() = %d run(s), want the dropped, the queued, the unasked, and the unrecorded and not the merged, the local, or the one still running", len(awaiting))
	}
	sources := quietSources()
	sources.Runs = fakeRuns{prices: map[string]runstate.ItemPrice{}, recorded: recorded}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 4 {
		t.Fatalf("needs a human = %+v, want one entry per promotion awaiting the forge", standing.NeedsHuman)
	}
	for _, want := range []struct {
		run, mover, what string
	}{
		{"run-dropped", "the development manager's", "merge of pull request 401"},
		{"run-queued", "the forge's", "pull request #"},
		{"run-unasked", "the operator's", "pull request #"},
		{"run-unrecorded", "the harness's", "holds no pull request for branch yoyodyne/item/unrecorded"},
	} {
		found := false
		for _, attention := range standing.NeedsHuman {
			if attention.ID != want.run {
				continue
			}
			found = true
			if !strings.Contains(attention.What(), want.what) {
				t.Errorf("what = %q, want the unpublished promotion named with %q", attention.What(), want.what)
			}
			if !strings.HasPrefix(attention.Whose(), want.mover) || !strings.Contains(attention.Whose(), "yoyo reconcile") {
				t.Errorf("whose for %s = %q, want %s and the sweep that settles it", want.run, attention.Whose(), want.mover)
			}
		}
		if !found {
			t.Errorf("needs a human = %+v, want %s named", standing.NeedsHuman, want.run)
		}
	}
	if strings.Contains(standing.NeedsHumanProblem, "forge") {
		t.Fatalf("needs a human problem = %q, want the promotions read without complaint", standing.NeedsHumanProblem)
	}

	// A reading that cannot read the runs says so rather than reporting nothing
	// awaiting the forge.
	sources.Runs = fakeRuns{prices: map[string]runstate.ItemPrice{}, failRecorded: errors.New("the records are unreadable")}
	standing = ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.NeedsHumanProblem, "the promotions awaiting the forge could not be read") {
		t.Fatalf("needs a human problem = %q, want the unreadable records named", standing.NeedsHumanProblem)
	}
}

// Work marked for a conversation says a different thing on each line: why
// nothing pulls it, and who has to open the conversation.
func TestHandedOffWorkIsNamedOnBothLines(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "item-1", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect),
		}}},
		ready: []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "rather than a developer run") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose(), "the architect's") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// A parked item is a decision somebody already took. It is a refusal and it is
// not waiting on anybody, so listing it as needing a human would send an
// operator to act on something that is settled.
func TestParkedWorkIsRefusedAndNeedsNobody(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "item-1", Status: "open", Parking: domain.WorkItemParking("the design is being reworked"),
		}}},
		ready: []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "parked") || standing.NotStartable[0].Kind != backlog.HeldParked {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("needs a human = %+v, want nothing", standing.NeedsHuman)
	}
}

// A wiring gap is reported as a wiring gap. A surface assembled without a source
// must not report the state that source describes as empty.
func TestAnUnwiredSourceIsSaidRatherThanAssumedEmpty(t *testing.T) {
	t.Parallel()
	standing := ReadStanding(context.Background(), Sources{Now: func() time.Time { return moment }})
	for _, problem := range []string{
		standing.RunningProblem,
		standing.WorkingProblem,
		standing.NotStartableProblem,
		standing.NeedsHumanProblem,
	} {
		if !strings.Contains(problem, "nothing was wired") {
			t.Fatalf("problem = %q, want a stated wiring gap", problem)
		}
	}
	// The item lists follow the queue: absent where it was not read, never an
	// empty backlog assembled from nothing.
	if standing.AdmittedItems != nil || standing.StartableItems != nil {
		t.Fatalf("an unread queue names items: admitted %v, startable %v", standing.AdmittedItems, standing.StartableItems)
	}
}

// A conversation whose hold could not be observed is not counted either way,
// and the count says it is partial rather than passing as complete.
func TestAConversationThatCannotBeAskedIsSaidBesideTheCount(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Agent: "architect", Role: domain.RoleArchitect, UpdatedAt: moment},
			{ConversationID: "chat-2", Agent: "reviewer", Role: domain.RoleReviewer, UpdatedAt: moment},
		},
		held:        map[string]bool{"reviewer": true},
		failObserve: map[string]error{"architect": errors.New("the holder stamp would not open")},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 {
		t.Fatalf("working = %+v, want the one that answered", standing.Working)
	}
	if !strings.Contains(standing.WorkingProblem, "architect") {
		t.Fatalf("working problem = %q", standing.WorkingProblem)
	}
	if !strings.Contains(standing.Render(), "  not fully read: ") {
		t.Fatalf("rendered:\n%s", standing.Render())
	}
}

// The 2026-09-07 shape read end to end. Three items are held: one nobody has
// decided about, one whose decision was recorded days ago, and one a run is
// already carrying. The counts in the head describe exactly the items listed
// under it, so the head cannot say something the entries beneath it contradict —
// and the attention line names each mover separately, which is what would have
// told the operator the gap was not his development manager's.
func TestHeldWorkIsCountedByWhoseMoveItIsAndOnlyWhereItStopsSomething(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-c", WorkItemID: "yoyodyne-ifd.152", Status: runstate.StatusRunning, StartedAt: moment.Add(-time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {
			{ID: "yoyodyne-ifd.150", Title: "Decided days ago", Status: "blocked"},
			{ID: "yoyodyne-ifd.151", Title: "Nobody has decided", Status: "blocked"},
			{ID: "yoyodyne-ifd.152", Title: "A run is carrying it", Status: "blocked"},
		}},
	}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{
		heldRun("run-a", "yoyodyne-ifd.150", stopped),
		heldRun("run-b", "yoyodyne-ifd.151", stopped),
		heldRun("run-c0", "yoyodyne-ifd.152", stopped),
	}}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.150": {Decisions: []runstate.TriageDecision{{
			Decision: runstate.TriageDecisionRerun, RunID: "run-a",
		}}},
		// And the item a run is already carrying, so that leaving it out of the
		// counts is the in-flight rule rather than an absent decision.
		"yoyodyne-ifd.152": {Decisions: []runstate.TriageDecision{{
			Decision: runstate.TriageDecisionRerun, RunID: "run-c0",
		}}},
	}

	standing := ReadStanding(context.Background(), sources)
	if standing.AwaitingDecision != 1 || standing.AwaitingCarryOut != 1 {
		t.Fatalf("standing counts %d awaiting a decision and %d awaiting carry-out, want one of each: %#v",
			standing.AwaitingDecision, standing.AwaitingCarryOut, standing.NotStartable)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"Not startable (2 of 3 admitted items; 1 awaits the development manager's decision, 1 awaits the harness carrying out a decision already recorded):\n",
		"1 admitted item awaits the development manager's decision — the development manager's",
		"1 admitted item awaits carry-out of a decision already recorded — the harness's",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The item a run is carrying is on the running line and on neither count, so
	// the head describes the entries printed under it and nothing else.
	if strings.Count(rendered, "yoyodyne-ifd.152") != 1 {
		t.Fatalf("the item a run is carrying is counted as held:\n%s", rendered)
	}
}

// The decisions the harness has not carried out are counted on the held-work
// line by what became of them: refused by a gate, or never attempted by any
// pass. The second is what yoyodyne-ifd.192 and .187 sat in for a week with no
// surface saying so. A finding waiting on a gate shut for everything, and one
// about a decision since replaced, are neither.
func TestTheHeldWorkLineCountsUnattemptedDecisionsBesideRefusedOnes(t *testing.T) {
	t.Parallel()

	stopped := moment.Add(-24 * time.Hour)
	decided := stopped.Add(time.Hour)
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {
			{ID: "yoyodyne-ifd.150", Title: "Refused", Status: "blocked"},
			{ID: "yoyodyne-ifd.151", Title: "Never attempted", Status: "blocked"},
			{ID: "yoyodyne-ifd.152", Title: "Waiting on the hold", Status: "blocked"},
		}},
	}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{
		heldRun("run-a", "yoyodyne-ifd.150", stopped),
		heldRun("run-b", "yoyodyne-ifd.151", stopped),
		heldRun("run-c", "yoyodyne-ifd.152", stopped),
	}}
	finding := func(run string, unattempted, waiting bool) runstate.TriageCarryOut {
		attempts := 1
		if unattempted {
			attempts = 0
		}
		return runstate.TriageCarryOut{RunID: run, Decision: runstate.TriageDecisionRerun, Gate: runstate.TriageGateBudget,
			Refusal: "said", Clears: "cleared", Unattempted: unattempted, Waiting: waiting, Attempts: attempts, RefusedAt: decided.Add(time.Minute)}
	}
	rerun := func(run string) []runstate.TriageDecision {
		return []runstate.TriageDecision{{Decision: runstate.TriageDecisionRerun, RunID: run, DecidedAt: decided}}
	}
	sources.Decisions = recordedDecisions{
		"yoyodyne-ifd.150": {Decisions: rerun("run-a"), CarryOuts: []runstate.TriageCarryOut{finding("run-a", false, false)}},
		"yoyodyne-ifd.151": {Decisions: rerun("run-b"), CarryOuts: []runstate.TriageCarryOut{finding("run-b", true, false)}},
		"yoyodyne-ifd.152": {Decisions: rerun("run-c"), CarryOuts: []runstate.TriageCarryOut{finding("run-c", false, true)}},
	}

	standing := ReadStanding(context.Background(), sources)
	if standing.CarryOutsRefused != 1 || standing.CarryOutsUnattempted != 1 {
		t.Fatalf("standing counts %d refused and %d unattempted, want one of each", standing.CarryOutsRefused, standing.CarryOutsUnattempted)
	}
	// The refused one is the development manager's to answer rather than the
	// harness's to carry out, so it is counted with the decisions waiting on her
	// (yoyodyne-8ff); the other two are still the harness's.
	want := "Not startable (3 of 3 admitted items; 1 awaits the development manager's decision, 2 await the harness carrying out a decision already recorded; decisions not carried out: 1 refused, 1 unattempted):\n"
	if rendered := standing.Render(); !strings.Contains(rendered, want) {
		t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
	}
}

// A parked item that is also held reads as parked, because releasing the hold
// would not make it pullable. Counting it as held would put it on a total
// nothing under the line accounts for.
func TestAParkedItemThatIsAlsoHeldIsNotCountedAsHeld(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"blocked": {{
			ID: "yoyodyne-ifd.153", Title: "Parked and stopped", Status: "blocked",
			Parking: "the design is being reworked",
		}}},
	}}
	sources.Stoppages = fakeStoppages{runs: []runstate.State{
		heldRun("run-d", "yoyodyne-ifd.153", moment.Add(-24*time.Hour)),
	}}
	sources.Decisions = recordedDecisions{}

	standing := ReadStanding(context.Background(), sources)
	if standing.AwaitingDecision != 0 || standing.AwaitingCarryOut != 0 {
		t.Fatalf("a parked item was counted as held: %d / %d", standing.AwaitingDecision, standing.AwaitingCarryOut)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, "Not startable (1 of 1 admitted item):\n") {
		t.Fatalf("rendered:\n%s\nwant the head to say nothing about held work", rendered)
	}
}

// heldRun is a run that stopped on a durable blocker and left its change behind,
// which is what holds an item for a person.
func heldRun(runID, workItemID string, stopped time.Time) runstate.State {
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusFailed,
		StartedAt:    stopped.Add(-time.Hour),
		UpdatedAt:    stopped,
		Branch:       "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath: "/state/worktrees/" + runID,
		Blocker:      "Yoyodyne stopped this item: a configured check still failed after every permitted attempt.",
	}
}

// A hold the brake placed names, on the attention line, who is deciding it and
// what the harness does next: the development manager while she decides, the
// harness while a probe runs — with the probe named — and the operator only
// once she has escalated it. The operator's own hold is theirs as it always
// was, and says nothing about a probe.
func TestABrakeHoldNamesWhoIsDecidingAndTheProbe(t *testing.T) {
	t.Parallel()

	trippedAt := moment.Add(-20 * time.Minute)
	summonedAt := trippedAt.Add(time.Minute)
	brake := func(revise func(*runstate.IntakeBrake)) fakeIntakeHolds {
		trip := runstate.IntakeBrake{
			Blocked:        []runstate.BrakeBlockedRun{{RunID: "run-1", WorkItemID: "yoyodyne-ifd.398", Reason: "review required repair"}},
			SummonedAt:     &summonedAt,
			CooldownEndsAt: trippedAt.Add(30 * time.Minute),
		}
		revise(&trip)
		return fakeIntakeHolds{held: true, hold: runstate.IntakeHold{
			HeldAt: trippedAt, HeldBy: runstate.IntakeHolderBrake,
			Reason: "3 run(s) blocked in a row with nothing landing between them, which is the configured brake at 3",
			Brake:  &trip,
		}}
	}
	for _, scenario := range []struct {
		name  string
		holds fakeIntakeHolds
		want  []string
	}{
		{
			name:  "deciding",
			holds: brake(func(*runstate.IntakeBrake) {}),
			// The line is bounded, so what has to fit is the clause that names the
			// mover: who is deciding, and when the probe starts if nobody does.
			want: []string{
				"intake is held, since " + trippedAt.UTC().Format(time.RFC3339) + ": the harness's own brake placed it after 3 run(s) blocked in a row",
				"— the development manager's — she decides what happens to it, and a probe run starts by itself at " + trippedAt.Add(30*time.Minute).UTC().Format(time.RFC3339) + " if she has not",
			},
		},
		{
			name: "probing",
			holds: brake(func(trip *runstate.IntakeBrake) {
				trip.Probe = &runstate.IntakeProbe{WorkItemID: "yoyodyne-ifd.410", StartedAt: moment.Add(-time.Minute)}
				trip.Probes = 1
			}),
			want: []string{
				"— the harness's — a probe run of yoyodyne-ifd.410 is in flight; intake reopens if it lands",
			},
		},
		{
			name: "escalated",
			holds: brake(func(trip *runstate.IntakeBrake) {
				decidedAt := summonedAt.Add(time.Minute)
				trip.Decision, trip.DecidedAt, trip.DecisionReason = runstate.BrakeDecisionEscalate, &decidedAt, "the same check fails everywhere"
			}),
			want: []string{
				"— the operator's — the development manager escalated it, and nothing new is chosen until `yoyo release` lifts it",
			},
		},
	} {
		sources := quietSources()
		sources.IntakeHolds = scenario.holds
		rendered := ReadStanding(context.Background(), sources).Render()
		for _, want := range scenario.want {
			if !strings.Contains(rendered, want) {
				t.Fatalf("%s: rendered:\n%s\nmissing: %q", scenario.name, rendered, want)
			}
		}
		if scenario.name != "escalated" && strings.Contains(rendered, "the operator's") {
			t.Fatalf("%s: rendered:\n%s\nwant a brake hold nobody escalated never reported as the operator's", scenario.name, rendered)
		}
	}
}

// The failure this line was reporting wrongly: an epic and the child that
// carries its execution both sitting ready, with the scheduling pass leaving the
// epic alone forever. The tracker reports both as pullable, so a status that
// asked only the tracker showed the epic as work about to be started and merely
// stalled — and sent whoever read it to investigate a stall that is not one.
func TestACoveredEpicIsNotStartableWithTheCoveringChildNamed(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	epic := beads.WorkItem{ID: "yoyodyne-ifd.121", Title: "A readable README", Status: "open"}
	// Parentage as an edge rather than as a field, which is the only shape this
	// project's own tracker ever states it in.
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Split it", Status: "open",
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	admitted := []beads.WorkItem{epic, child}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": admitted},
		ready:    admitted,
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || standing.NotStartable[0].WorkItemID != epic.ID {
		t.Fatalf("not startable = %+v, want the covered epic and only it", standing.NotStartable)
	}
	refused := standing.NotStartable[0]
	if !strings.Contains(refused.Reason, child.ID) {
		t.Fatalf("reason = %q, want the child that covers it named", refused.Reason)
	}
	// The refusal is the backlog's own words rather than a second wording of the
	// same fact, which is what the scheduling pass defers the epic with, and it
	// is counted in the pile the queue's vocabulary names for it rather than
	// among the stalled.
	if refused.Reason != backlog.CoveredReason([]string{child.ID}) {
		t.Fatalf("reason = %q, want the shared derivation's wording", refused.Reason)
	}
	if refused.Kind != backlog.HeldCovered {
		t.Fatalf("kind = %q, want %q", refused.Kind, backlog.HeldCovered)
	}
	// The child is the work, and it is what the harness pulls next.
	if len(standing.StartableItems) != 1 || standing.StartableItems[0].WorkItemID != child.ID || standing.Startable != 1 {
		t.Fatalf("startable = %d %+v, want the child and only it", standing.Startable, standing.StartableItems)
	}
	// A covered epic is not waiting on anybody. Nothing about it clears when a
	// switch is lifted or a slot frees, so it must not put the harness's stall on
	// the line that says whose move it is.
	for _, waiting := range standing.NeedsHuman {
		t.Fatalf("needs a human = %+v, want a covered epic to ask nothing of anybody", waiting)
	}
	if rendered := standing.Render(); !strings.Contains(rendered, epic.ID+" — "+refused.Reason) {
		t.Fatalf("rendered:\n%s\nwant the epic refused with its children named", rendered)
	}
}

// A claimed child is the strongest cover there is and the one a reading of the
// queue alone cannot see: it has left the backlog, and it is a run in flight
// over the very same change.
func TestAnEpicWhoseChildIsAlreadyRunningIsNotStartable(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite it", Status: "open"}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite it", Status: backlog.StatusClaimed, Parent: epic.ID}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {epic}, backlog.StatusClaimed: {child}},
		ready:    []beads.WorkItem{epic},
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || standing.NotStartable[0].WorkItemID != epic.ID {
		t.Fatalf("not startable = %+v, want the epic its running child covers", standing.NotStartable)
	}
	if !strings.Contains(standing.NotStartable[0].Reason, child.ID) {
		t.Fatalf("reason = %q, want the child that is carrying the work named", standing.NotStartable[0].Reason)
	}
}

// Coverage is re-read at every reading rather than remembered, so a container
// whose last unfinished child has closed is ordinary work again — and a status
// that went on refusing it would hide real work behind a decomposition that
// finished.
func TestAContainerWhoseChildrenHaveClosedIsStartableAgain(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite it", Status: "open"}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {epic}},
		ready:    []beads.WorkItem{epic},
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want the container startable once nothing covers it", standing.NotStartable)
	}
}

// A running line and a working line name the model beside the effort level
// where one was recorded, and read exactly as they always did where none was.
func TestTheStatusLinesSayTheEffortBesideTheModel(t *testing.T) {
	t.Parallel()

	standing := Standing{
		Running: []RunningRun{
			{WorkItemID: "item-a", Model: "opus", Effort: "medium", Phase: runstate.PhaseDeveloping, UnknownCost: "nothing priced yet"},
			{WorkItemID: "item-b", Model: "opus", Phase: runstate.PhaseDeveloping, UnknownCost: "nothing priced yet"},
		},
		Working: []WorkingTurn{
			{Agent: "architect", Role: "architect", Model: "fable", Effort: "high", Turns: 3},
			{Agent: "product-manager", Role: "product-manager", Model: "opus", Turns: 3},
		},
	}
	rendered := standing.renderRunning() + standing.renderWorking()
	for _, want := range []string{
		"  item-a — developing, on opus at medium effort, ",
		"  item-b — developing, 0s elapsed",
		"  architect — architect, on fable at high effort, a turn in flight",
		"  product-manager — product-manager, a turn in flight",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
}

// fakeGates stands in for the harness's record of what a person has done.
type fakeGates struct {
	discharged map[string][]string
	fail       error
}

func (f fakeGates) DischargedGates() (map[string][]string, error) { return f.discharged, f.fail }

// An item held by a step only a person can take is on two lines and says a
// different thing on each: the queue's line says why nothing pulls it, and the
// attention line says whose move it is and what records the act. A reader given
// only the first has been told the item is waiting, which is what a wait on
// other work also looks like.
func TestWorkHeldByAPersonsStepIsNamedAsWaitingOnThem(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default", Status: "open",
			Description: "human-gate: soak-reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	standing := ReadStanding(context.Background(), sources)

	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "waiting on a person") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs human = %+v", standing.NeedsHuman)
	}
	attention := standing.NeedsHuman[0]
	for _, want := range []string{"yoyodyne-ifd.209.7", "soak-reviewed", "judged the parity soak"} {
		if !strings.Contains(attention.What(), want) {
			t.Fatalf("what = %q, want it to mention %q", attention.What(), want)
		}
	}
	// The command it names is one the operator can copy. An act is recorded
	// against the item that declared the gate, so a line naming only the gate
	// would send them to type a command that passes a different item's step or is
	// refused as already passed.
	for _, want := range []string{"closing an item", "yoyo gate record soak-reviewed --for yoyodyne-ifd.209.7"} {
		if !strings.Contains(attention.Whose(), want) {
			t.Fatalf("whose = %q, want it to mention %q", attention.Whose(), want)
		}
	}

	// Once the act is on the record the item is startable and nobody is waiting.
	sources.Gates = fakeGates{discharged: map[string][]string{"yoyodyne-ifd.209.7": {"soak-reviewed"}}}
	passed := ReadStanding(context.Background(), sources)
	if len(passed.NotStartable) != 0 || len(passed.NeedsHuman) != 0 {
		t.Fatalf("not startable = %+v, needs human = %+v", passed.NotStartable, passed.NeedsHuman)
	}
}

// What could not be read about the gates is said where it changed the answer,
// and not where it changed nothing. A caveat printed on every quiet reading is a
// caveat nobody reads by the time it matters.
func TestAnUnreadableGateRecordIsSaidOnlyWhereItOverstatesTheWait(t *testing.T) {
	t.Parallel()

	quiet := quietSources()
	quiet.Gates = fakeGates{fail: errors.New("the state store would not answer")}
	if problem := ReadStanding(context.Background(), quiet).NotStartableProblem; problem != "" {
		t.Fatalf("not startable problem = %q, want nothing said where no gate was declared", problem)
	}

	gated := quiet
	gated.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Status: "open",
			Description: "human-gate: soak-reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	problem := ReadStanding(context.Background(), gated).NotStartableProblem
	if !strings.Contains(problem, "would not answer") || !strings.Contains(problem, "1 gated item") {
		t.Fatalf("not startable problem = %q", problem)
	}
}

// A declaration nothing could read holds the item and is on the attention line
// too, saying the one thing that differs: no act records this one, and its
// author has to correct the line. An operator told only "waiting on a person"
// would go looking for a gate name to record and find none.
func TestADeclarationNothingCouldReadIsNamedAsTheAuthorsMove(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default", Status: "open",
			Description: "human-gate: Soak Reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	// Every act anybody could have recorded is on the record, and it still holds.
	sources.Gates = fakeGates{discharged: map[string][]string{"yoyodyne-ifd.209.7": {"soak-reviewed", "soak"}}}
	standing := ReadStanding(context.Background(), sources)

	if len(standing.NotStartable) != 1 {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs human = %+v", standing.NeedsHuman)
	}
	attention := standing.NeedsHuman[0]
	if !strings.Contains(attention.What(), "nothing could read") {
		t.Fatalf("what = %q", attention.What())
	}
	if !strings.Contains(attention.Whose(), "author") {
		t.Fatalf("whose = %q, want the author's move rather than an act to record", attention.Whose())
	}
}

func TestStatusProjectsCodexEffortSourcesFromRunAndTurnRecords(t *testing.T) {
	t.Parallel()
	for _, description := range []string{"high, from the agent", "high, from the Codex configuration", "not reported, from the Codex configuration"} {
		sources := quietSources()
		sources.Runs = fakeRuns{incomplete: []runstate.State{{
			RunID: "run-a", WorkItemID: "item-a", Backend: domain.BackendCodex, ProviderModel: "gpt-6.1-sol",
			ProviderEffortDescription: description, Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment,
		}}}
		sources.Conversations = fakeConversations{
			recorded: []runstate.Conversation{{ConversationID: "chat-1", Agent: "architect", Role: domain.RoleArchitect,
				Backend: domain.BackendCodex, ProviderModel: "gpt-6-astra", ProviderEffortDescription: description, UpdatedAt: moment}},
			held: map[string]bool{"architect": true},
		}
		standing := ReadStanding(context.Background(), sources)
		if len(standing.Running) != 1 || len(standing.Working) != 1 {
			t.Fatalf("standing = %+v", standing)
		}
		if standing.Running[0].EffortDescription != description || standing.Working[0].EffortDescription != description {
			t.Fatalf("projection lost the source: %+v", standing)
		}
		rendered := standing.renderRunning() + standing.renderWorking()
		if strings.Count(rendered, "effort "+description) != 2 {
			t.Fatalf("rendered = %s", rendered)
		}
	}
}
