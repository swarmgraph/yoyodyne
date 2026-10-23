package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// A handling recorded before the closed list was required, naming the operator
// with no reason from it, is listed under the role that handled it as open for
// that role to finish, and never as his; the handling beside it that gives a
// reason from the list stays his, unchanged.
func TestAHandlingNamingTheOperatorWithNoReasonIsListedWithTheRoleThatHandledIt(t *testing.T) {
	t.Parallel()

	reasoned := filedReport("report-00000000000000000000000000000001", report.SeverityWarning, moment.Add(-3*time.Hour))
	unreasoned := filedReport("report-00000000000000000000000000000002", report.SeverityWarning, moment.Add(-2*time.Hour))
	unreasoned.WorkItemID = "yoyodyne-ifd.383"
	reports := []report.Report{reasoned, unreasoned}
	handlings := []report.Handling{
		{ReportID: reasoned.ID, Role: domain.RoleProductManager, RunID: "chat-1", Reason: "the operator has to add the hook to .claude/settings.json by hand", RecordedAt: moment.Add(-90 * time.Minute), NeedsOperator: true, OperatorReason: ownership.OperatorProtectedFile},
		{ReportID: unreasoned.ID, Role: domain.RoleArchitect, RunID: "chat-2", Reason: "the operator should decide whether the design is worth keeping", RecordedAt: moment.Add(-80 * time.Minute), NeedsOperator: true},
	}

	// Only the reasoned handling is a finding for him, which is what the channel
	// says to him directly.
	if actions := OperatorActions(reports, handlings); len(actions) != 1 || actions[0].ReportID != reasoned.ID || actions[0].Returned {
		t.Fatalf("OperatorActions() = %#v, want only the handling with a closed-list reason", actions)
	}
	returned := ReturnedHandlings(reports, handlings)
	if len(returned) != 1 || returned[0].ReportID != unreasoned.ID || !returned[0].Returned || returned[0].DecidedBy != domain.RoleArchitect {
		t.Fatalf("ReturnedHandlings() = %#v, want the unreasoned handling, the architect's", returned)
	}

	sources := quietSources()
	sources.Reports = fakeReports{reports: reports, handlings: handlings}
	standing := ReadStanding(context.Background(), sources)
	var his, architects *Attention
	for index, waiting := range standing.NeedsHuman {
		if waiting.Kind != AttentionOperatorAction {
			continue
		}
		switch waiting.ID {
		case "report:" + reasoned.ID:
			his = &standing.NeedsHuman[index]
		case "report:" + unreasoned.ID:
			architects = &standing.NeedsHuman[index]
		}
	}
	if his == nil || architects == nil {
		t.Fatalf("NeedsHuman = %#v, want both handlings listed", standing.NeedsHuman)
	}

	if !his.Mover.IsOperator() || his.Ownership.Reason != ownership.OperatorProtectedFile || his.Label() != "person needed" ||
		!strings.Contains(his.What(), reasoned.ID+" needs your hand: the operator has to add the hook") ||
		!strings.HasPrefix(his.Whose(), "the operator's — only a person can act on this") {
		t.Fatalf("reasoned handling = %s, %q, %q, %q, want it the operator's unchanged", his.Mover, his.Label(), his.What(), his.Whose())
	}

	if architects.Mover != MoverArchitect || architects.Ownership.NeedsHand() || architects.Label() != "report to handle again" {
		t.Fatalf("unreasoned handling = %s with %+v labelled %q, want the architect's to handle again", architects.Mover, architects.Ownership, architects.Label())
	}
	if what := architects.What(); !strings.Contains(what, unreasoned.ID+" is open for the architect to finish") ||
		!strings.Contains(what, "the operator should decide whether the design is worth keeping") ||
		!strings.Contains(what, "about yoyodyne-ifd.383") || strings.Contains(what, "needs your hand") {
		t.Fatalf("unreasoned handling says %q, want it open for the architect to finish and not asking for the operator's hand", what)
	}
	if whose := architects.Whose(); !strings.HasPrefix(whose, "the architect's — the finding names the operator, but it was handled before a handling had to say why a person must act, and it gives no such reason, so it is not his: handle the report again") || strings.Contains(whose, "closed list") {
		t.Fatalf("unreasoned handling is %q, want the architect's move", whose)
	}

	rendered := standing.Render()
	yours := strings.Index(rendered, YoursToDecide+" (1):")
	architectHead := strings.Index(rendered, "Waiting on the architect (1):")
	if yours < 0 || architectHead < 0 || strings.Contains(rendered[yours:architectHead], unreasoned.ID) ||
		!strings.Contains(rendered[architectHead:], unreasoned.ID+" is open for the architect to finish") {
		t.Fatalf("rendered:\n%s\nwant only the reasoned handling under %q and the other under the architect", rendered, YoursToDecide)
	}
	if waits := standing.RenderOperatorWaits(moment); strings.Contains(waits, unreasoned.ID) || !strings.Contains(waits, reasoned.ID) {
		t.Fatalf("operator waits:\n%s\nwant only the reasoned handling", waits)
	}

	// The dashboard reads the entry from the JSON, which carries the role it is
	// with and reads back held to the registry.
	encoded, err := json.Marshal(*architects)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("reading %s back: %v", encoded, err)
	}
	if decoded.Mover != MoverArchitect || decoded.OperatorAction == nil || !decoded.OperatorAction.Returned {
		t.Fatalf("decoded = %+v, want the architect's returned handling", decoded)
	}
}

// A handling that records no role it was made under is the Lead Product
// Manager's, who handles the pile.
func TestAReturnedHandlingRecordingNoRoleIsTheLeadProductManagers(t *testing.T) {
	t.Parallel()
	reported := filedReport("report-00000000000000000000000000000004", report.SeverityNote, moment.Add(-time.Hour))
	returned := ReturnedHandlings([]report.Report{reported}, []report.Handling{
		{ReportID: reported.ID, RunID: "chat-3", Reason: "needs him", RecordedAt: moment, NeedsOperator: true},
	})
	if len(returned) != 1 || returned[0].DecidedBy != domain.RoleProductManager {
		t.Fatalf("ReturnedHandlings() = %#v, want the Lead Product Manager's", returned)
	}
	if entry := operatorActionAttention(returned[0]); entry.Mover != MoverProductManager {
		t.Fatalf("entry is %s's, want the Lead Product Manager's", entry.Mover)
	}
}
