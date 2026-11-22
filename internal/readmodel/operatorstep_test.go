package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A handling on the operator's list whose step names a file the repository
// does not have stays his for three days, and is checked again after them: it
// goes back to the role that recorded it, saying why, and is no longer said to
// him.
func TestAnEntryOnTheOperatorsListOlderThanThreeDaysIsCheckedAgain(t *testing.T) {
	t.Parallel()
	reported := filedReport("report-00000000000000000000000000000011", report.SeverityWarning, moment.Add(-200*time.Hour))
	handling := report.Handling{
		ReportID: reported.ID, Role: domain.RoleProductManager, RunID: "chat-1",
		Reason:        "edit the role-definition folder .yoyodyne/roles/developer/ to grant the path",
		NeedsOperator: true, OperatorReason: ownership.OperatorProtectedFile,
	}
	world := ownership.StepWorld{Repository: t.TempDir()}

	handling.RecordedAt = moment.Add(-48 * time.Hour)
	recent := StepRecheck{Now: moment, World: world}
	if actions := OperatorActions([]report.Report{reported}, []report.Handling{handling}, recent); len(actions) != 1 || actions[0].Returned {
		t.Fatalf("OperatorActions(two days) = %#v, want it still his", actions)
	}

	handling.RecordedAt = moment.Add(-80 * time.Hour)
	if actions := OperatorActions([]report.Report{reported}, []report.Handling{handling}, recent); len(actions) != 0 {
		t.Fatalf("OperatorActions(over three days) = %#v, want it no longer his", actions)
	}
	returned := ReturnedHandlings([]report.Report{reported}, []report.Handling{handling}, recent)
	if len(returned) != 1 || returned[0].DecidedBy != domain.RoleProductManager || !strings.Contains(returned[0].ReturnedWhy, ".yoyodyne/roles/developer/ does not exist") {
		t.Fatalf("ReturnedHandlings() = %#v, want it back with the Lead Product Manager, saying the folder is not there", returned)
	}

	// The status line reads the same: the entry is hers, saying why.
	sources := quietSources()
	sources.Repository = world.Repository
	sources.Reports = fakeReports{reports: []report.Report{reported}, handlings: []report.Handling{handling}}
	standing := ReadStanding(context.Background(), sources)
	var entry *Attention
	for index, waiting := range standing.NeedsHuman {
		if waiting.ID == "report:"+reported.ID {
			entry = &standing.NeedsHuman[index]
		}
	}
	if entry == nil || entry.Mover != MoverProductManager || entry.Mover.IsOperator() {
		t.Fatalf("NeedsHuman = %#v, want the entry with the Lead Product Manager", standing.NeedsHuman)
	}
	if what, whose := entry.What(), entry.Whose(); !strings.Contains(what, "failed its check while it waited") || !strings.Contains(whose, "does not exist") {
		t.Fatalf("entry says %q, %q, want why it came back", what, whose)
	}
}

// A request naming a hold the operator placed himself leaves his list by
// itself once the hold is gone, whatever its age, and stays while it stands.
func TestAnOwnHoldRequestLeavesTheOperatorsListOnceTheHoldIsGone(t *testing.T) {
	t.Parallel()
	reported := filedReport("report-00000000000000000000000000000012", report.SeverityWarning, moment.Add(-2*time.Hour))
	handling := report.Handling{
		ReportID: reported.ID, Role: domain.RoleProductManager, RunID: "chat-1",
		Reason: "lift your hold on new work with yoyo intake release", RecordedAt: moment.Add(-time.Hour),
		NeedsOperator: true, OperatorReason: ownership.OperatorOwnHold,
	}
	read := func(intake fakeIntakeHolds) *Attention {
		sources := quietSources()
		sources.IntakeHolds = intake
		sources.Reports = fakeReports{reports: []report.Report{reported}, handlings: []report.Handling{handling}}
		standing := ReadStanding(context.Background(), sources)
		for index, waiting := range standing.NeedsHuman {
			if waiting.ID == "report:"+reported.ID {
				return &standing.NeedsHuman[index]
			}
		}
		t.Fatalf("NeedsHuman = %#v, want the request listed", standing.NeedsHuman)
		return nil
	}
	held := fakeIntakeHolds{held: true, hold: runstate.IntakeHold{HeldBy: runstate.IntakeHolderOperator}}
	if entry := read(held); !entry.Mover.IsOperator() {
		t.Fatalf("entry with the hold standing = %s, want his", entry.Mover)
	}
	if entry := read(fakeIntakeHolds{}); entry.Mover.IsOperator() || !strings.Contains(entry.Whose(), "none stands now") {
		t.Fatalf("entry with the hold gone = %s, %q, want it back with the Lead Product Manager", entry.Mover, entry.Whose())
	}
	brake := fakeIntakeHolds{held: true, hold: runstate.IntakeHold{HeldBy: runstate.IntakeHolderBrake}}
	if entry := read(brake); entry.Mover.IsOperator() {
		t.Fatalf("entry with only the brake's hold = %s, want it not his", entry.Mover)
	}
}

// An escalation of a stopped run to the operator is checked again as a
// handling is: one whose step names a file the repository does not have stays
// his for three days and then goes back to the development manager, saying
// why; one naming a hold of his that is gone, or a restart the product does
// itself, goes back at once.
func TestAnEscalationToTheOperatorIsCheckedAgainWhileItWaits(t *testing.T) {
	t.Parallel()
	world := ownership.StepWorld{Repository: t.TempDir(), OwnHold: func() (bool, error) { return false, nil }}
	recheck := StepRecheck{Now: moment, World: world}
	escalated := func(reason ownership.OperatorReason, why string, decidedAt time.Time) ([]OperatorAction, []EndedEscalation) {
		t.Helper()
		run := heldRun("run-153a", "yoyodyne-ifd.153", decidedAt.Add(-time.Hour))
		decision := runstate.TriageDecision{
			Decision: runstate.TriageDecisionEscalate, RunID: "run-153a", Reason: why,
			DecidedBy: "development-manager", Conversation: "chat-dm", Turn: 9, DecidedAt: decidedAt,
			OperatorReason: reason,
		}
		standing, ended, problem := Escalations([]runstate.State{run}, recordedDecisions{"yoyodyne-ifd.153": {Decisions: []runstate.TriageDecision{decision}}}, nil, nil, recheck)
		if problem != "" {
			t.Fatalf("Escalations() problem = %q", problem)
		}
		return standing, ended
	}
	missing := "the grant needs .yoyodyne/roles/developer/ changed by hand"
	if standing, ended := escalated(ownership.OperatorProtectedFile, missing, moment.Add(-48*time.Hour)); len(standing) != 1 || len(ended) != 0 {
		t.Fatalf("Escalations(two days) = %#v, %#v, want it still his", standing, ended)
	}
	for _, tc := range []struct {
		name, why, says string
		reason          ownership.OperatorReason
		decidedAt       time.Time
	}{
		{"a missing folder after three days", missing, ".yoyodyne/roles/developer/ does not exist", ownership.OperatorProtectedFile, moment.Add(-80 * time.Hour)},
		{"a hold that is gone", "the run waits on the operator lifting his hold on new work", "none stands now", ownership.OperatorOwnHold, moment.Add(-time.Hour)},
		{"a restart the product does itself", "the scheduler has to be restarted onto the new build", "operating rule 6de828a8", ownership.OperatorRepositorySetting, moment.Add(-time.Hour)},
	} {
		standing, ended := escalated(tc.reason, tc.why, tc.decidedAt)
		if len(standing) != 0 || len(ended) != 1 || ended[0].ReturnedTo != MoverDevelopmentManager || !strings.Contains(ended[0].Why, tc.says) {
			t.Errorf("%s: Escalations() = %#v, %#v, want it back with the development manager saying %q", tc.name, standing, ended, tc.says)
		}
	}
}
