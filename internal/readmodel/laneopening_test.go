package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/ownership"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const tokenReport = "report-00000000000000000000000000000077"

// A report written with all three opening lists reaches every surface first:
// the instance the dashboard's row and card read, the line `yoyo status`
// prints, and the "Needs a human" line, once per item, with the operator as its
// mover. A later pass that leaves the Blocked on a human entry out does not
// remove it, and the operator's act recorded on the record it cites does.
func TestALaneReportsThreeOpeningListsReachEverySurfaceAndTheBlockerIsCarried(t *testing.T) {
	t.Parallel()

	sources := programManagerSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{
		"closed": {{ID: "yoyodyne-ifd.7", Title: "Retire the old maintenance job", Labels: []string{"reliability"}, ClosedAt: moment.Add(-2 * time.Hour)},
			{ID: "yoyodyne-ifd.6", Title: "Closed last week", Labels: []string{"reliability"}, ClosedAt: moment.Add(-8 * 24 * time.Hour)}},
		"open": {{ID: "yoyodyne-ifd.8", Title: "Fix the checkout timeout under load"},
			{ID: "yoyodyne-ifd.9", Title: "Renew the forge token"}},
	}}}
	admitting := completedPass(factoryConversation, moment.Add(-30*time.Minute))
	admitting.Admitted = []string{"yoyodyne-ifd.8"}
	sources.Passes = fakePasses{passes: []runstate.Sweep{admitting, completedPass(writingConversation, moment.Add(-30*time.Minute))}}
	digest := filedBy("report-00000000000000000000000000000078", "factory-pgm")
	digest.Message = "the checkout timeout needs a prevention item outside the lane"
	token := filedBy(tokenReport, "factory-pgm")
	sources.Reports = fakeReports{
		reports:   []report.Report{digest, token},
		handlings: []report.Handling{{ReportID: digest.ID, Role: domain.RoleProductManager, RecordedAt: moment.Add(-10 * time.Minute)}},
	}
	asked := askedBy("exchange-0123456789abcdef", "factory-pgm", "")
	asked.Answerer, asked.Question, asked.OpenedAt = exchange.Party{Role: domain.RoleDevelopmentManager}, "is developer slot 2 stuck?", moment.Add(-time.Hour)
	sources.Exchanges = fakeExchanges{exchanges: []exchange.Exchange{asked}}

	store, err := runstate.NewLaneReportStore(t.TempDir(), "yoyodyne", CheckLaneReportMover)
	if err != nil {
		t.Fatal(err)
	}
	store.UseWaitingRecords(WaitingRecordCheck(sources))
	sources.LaneReports = store
	write := func(agent string, turn int, blocked []runstate.LaneReportHumanBlock) {
		t.Helper()
		_, err := store.Write(context.Background(), runstate.LaneReport{
			ProductID: "yoyodyne", Agent: agent,
			Report:     runstate.LaneReportContent{BlockedOnAHuman: blocked, Summary: "One paragraph that says everything at once, with no line breaks at all.", Remaining: []string{}, Blockers: []runstate.LaneReportBlocker{}},
			Stamp:      runstate.LaneReportStamp{ConversationID: factoryConversation, Turn: turn},
			RecordedAt: moment.Add(time.Duration(turn-30) * time.Minute),
		})
		if err != nil {
			t.Fatalf("Write(%s, %d) error = %v", agent, turn, err)
		}
	}
	entry := runstate.LaneReportHumanBlock{Item: "yoyodyne-ifd.9", What: "renew the forge token in the keychain", Reason: ownership.OperatorCredential, Cites: tokenReport, Since: moment.Add(-5 * time.Hour)}
	write("factory-pgm", 1, []runstate.LaneReportHumanBlock{entry})
	write("writing-pgm", 2, []runstate.LaneReportHumanBlock{entry})

	// The row and the card read the instance the standing carries.
	standing := ReadStanding(context.Background(), sources)
	var factory ProgramManager
	for _, instance := range standing.ProgramManagers {
		if instance.Agent == "factory-pgm" {
			factory = instance
		}
	}
	if len(factory.Completed) != 2 || factory.Completed[0].What != "its report handled by the Lead Product Manager" || factory.Completed[1].Item != "yoyodyne-ifd.7" || factory.Completed[1].Title != "Retire the old maintenance job" {
		t.Fatalf("Completed = %+v, want the report handled and the lane item closed today, and not last week's", factory.Completed)
	}
	handed := map[string]string{}
	for _, entry := range factory.HandedOff {
		handed[entry.Item+entry.Record] = entry.To
	}
	if len(factory.HandedOff) != 4 || handed["yoyodyne-ifd.8"] != "a developer" || handed[digest.ID] != "the Lead Product Manager" || handed[asked.ID] != "the development manager" || handed[tokenReport] != "the Lead Product Manager" {
		t.Fatalf("Handed off = %+v, want the admission to a developer, both reports to the Lead Product Manager, and the ask to the development manager", factory.HandedOff)
	}
	if len(factory.BlockedOnAHuman) != 1 || factory.BlockedOnAHuman[0].Title != "Renew the forge token" || factory.BlockedOnAHuman[0].Mover != ownership.MoverOperator || factory.Status != ProgramManagerBlocked {
		t.Fatalf("Blocked on a human = %+v, status %s; want the token entry, titled, the operator's, and the instance blocked", factory.BlockedOnAHuman, factory.Status)
	}
	card, err := ReadProgramManagerReport(sources, "factory-pgm")
	if err != nil || card.Report == nil || len(card.Instance.Completed) != 2 || len(card.Instance.HandedOff) != 4 || len(card.Instance.BlockedOnAHuman) != 1 || card.Instance.BlockedOnAHuman[0].Title != "Renew the forge token" {
		t.Fatalf("the card's query = %+v, %v; want the three lists beside the report", card.Instance, err)
	}

	// yoyo status prints the three as lists under the instance's line, one
	// line per entry, whatever the summary's layout.
	rendered := standing.RenderProgramManagers()
	for _, want := range []string{
		"  factory-pgm — lane reliability — blocked: blocked on a human for 1 thing\n    Completed (last 24 hours):\n      - ",
		"      - (P0, reliability) Retire the old maintenance job (yoyodyne-ifd.7) — closed, ",
		"    Handed off (last 24 hours):\n",
		"      - (P0) Fix the checkout timeout under load (yoyodyne-ifd.8) — admitted, to a developer, ",
		"    Blocked on a human:\n      - (P0) Renew the forge token (yoyodyne-ifd.9): renew the forge token in the keychain — a credential or account only a person holds; since ",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("RenderProgramManagers() does not carry %q:\n%s", want, rendered)
		}
	}

	// The "Needs a human" line carries the item once, as the operator's.
	blocked := func(standing Standing) []Attention {
		var found []Attention
		for _, waiting := range standing.NeedsHuman {
			if waiting.Kind == AttentionLaneHumanBlock {
				found = append(found, waiting)
			}
		}
		return found
	}
	if found := blocked(standing); len(found) != 1 || found[0].Mover != ownership.MoverOperator || found[0].WorkItemID != "yoyodyne-ifd.9" || !found[0].Named() {
		t.Fatalf("the Needs a human line carries %+v, want the token item once, named, as the operator's", found)
	}

	// A pass that leaves the entry out does not remove it.
	write("factory-pgm", 3, nil)
	write("writing-pgm", 4, nil)
	carried, _, _ := store.Current("factory-pgm")
	if len(carried.Report.BlockedOnAHuman) != 1 || !carried.Report.BlockedOnAHuman[0].Carried || !carried.Report.BlockedOnAHuman[0].Since.Equal(entry.Since) {
		t.Fatalf("after a pass that left it out the report carries %+v, want the entry carried with its moment", carried.Report.BlockedOnAHuman)
	}
	if found := blocked(ReadStanding(context.Background(), sources)); len(found) != 1 {
		t.Fatalf("after a pass that left it out the line carries %d entries, want 1", len(found))
	}

	// The operator's act recorded on the report it cites clears it at once on
	// every surface, and the next pass stops carrying it.
	cleared := sources.Reports.(fakeReports)
	cleared.handlings = append(cleared.handlings, report.Handling{ReportID: tokenReport, Role: domain.RoleProductManager, RecordedAt: moment})
	sources.Reports = cleared
	store.UseWaitingRecords(WaitingRecordCheck(sources))
	after := ReadStanding(context.Background(), sources)
	if found := blocked(after); len(found) != 0 {
		t.Fatalf("after the act was recorded the line carries %+v, want nothing", found)
	}
	write("factory-pgm", 5, nil)
	if current, _, _ := store.Current("factory-pgm"); len(current.Report.BlockedOnAHuman) != 0 {
		t.Fatalf("the pass after the act still carries %+v", current.Report.BlockedOnAHuman)
	}
}

// An instance with nothing in the three lists still shows each, as "none".
func TestEmptyOpeningListsAreSaidAsNone(t *testing.T) {
	t.Parallel()

	rendered := ProgramManager{Agent: "factory-pgm"}.RenderOpeningLists("  ")
	if rendered != "  Completed (last 24 hours): none\n  Handed off (last 24 hours): none\n  Blocked on a human: none\n" {
		t.Fatalf("RenderOpeningLists() = %q", rendered)
	}
}
