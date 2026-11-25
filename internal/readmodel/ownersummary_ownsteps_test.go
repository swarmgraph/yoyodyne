package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/ownership"
)

// ownStep is a report's handling that needs the operator for reason, asking
// needs of him about item, as OperatorActions derives one.
func ownStep(reportID, item string, reason ownership.OperatorReason, needs string, since time.Time) Attention {
	action := &OperatorAction{
		Key: operatorActionKey(reportID), Subject: reportID, ReportID: reportID, WorkItemID: item,
		Needs: needs, Reason: reason, RecordedIn: "the handling of " + reportID, FoundBy: "the Lead Product Manager, handling the report",
		Ends: reportFindingEnds, Since: since,
	}
	return resolved(Attention{Kind: AttentionOperatorAction, ID: action.Key, WorkItemID: item, OperatorAction: action})
}

func ownStepsSummary(now time.Time, steps ...Attention) OwnerSummary {
	in := summaryFixture(now)
	in.Standing.NeedsHuman = append(in.Standing.NeedsHuman[:3:3], steps...)
	return Summarize(in)
}

// An epic add the operator owes under his own hold and a credential step on
// another item are two lines, each naming its item, its own reason, and the act
// its record asks for; neither is said in the other's reason.
func TestTheSummarySaysAnOwnHoldEpicAddApartFromACredentialStep(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	epicAdd := "add the item to the epic it was named for: `yoyo epic add ex-32of5 --epic ex-32of`"
	slack := "connect Slack with `yoyo slack connect` and sign in"
	summary := ownStepsSummary(now,
		ownStep("report-epic", "ex-32of5", ownership.OperatorOwnHold, epicAdd, now.Add(-time.Hour)),
		ownStep("report-slack", "ex-slack", ownership.OperatorCredential, slack, now.Add(-2*time.Hour)),
	)

	persons := 0
	for _, entry := range summary.Decisions.Entries {
		if strings.HasPrefix(entry.Says, "Person needed") {
			persons++
			if entry.Count != 1 || strings.Contains(entry.Says, "times") {
				t.Fatalf("a step only the operator can take was folded with another: %+v", entry)
			}
		}
	}
	if persons != 2 {
		t.Fatalf("want two lines for two different steps, got %d: %+v", persons, summary.Decisions.Entries)
	}

	epic := entryWith(t, summary.Decisions, "ex-32of5")
	if epic.Next != epicAdd || epic.WaitingOn != "you, because it is "+ownership.OperatorOwnHold.Says() {
		t.Fatalf("the epic add line = %+v", epic)
	}
	if strings.Contains(epic.Line, ownership.OperatorCredential.Says()) {
		t.Fatalf("the epic add is said in the credential's reason: %s", epic.Line)
	}
	if !strings.Contains(epic.Line, "`yoyo epic add ex-32of5 --epic ex-32of`") {
		t.Fatalf("the epic add line does not name the command: %s", epic.Line)
	}

	credential := entryWith(t, summary.Decisions, "ex-slack")
	if credential.Next != slack || credential.WaitingOn != "you, because it is "+ownership.OperatorCredential.Says() {
		t.Fatalf("the credential line = %+v", credential)
	}

	// `yoyo status` prints the line every other surface reads off the entry.
	rendered := summary.Render(false)
	for _, line := range []string{epic.Line, credential.Line} {
		if !strings.Contains(rendered, "    - "+line+"\n") {
			t.Fatalf("status does not print %q:\n%s", line, rendered)
		}
	}
}

// Two requests to add different items are two lines; the same request about
// the same item recorded twice is one line saying how many times.
func TestTheSummaryFoldsOwnStepsOnlyForTheSameActOnTheSameItem(t *testing.T) {
	t.Parallel()
	now := summaryNow()
	addA := "add ex-a to the epic: `yoyo epic add ex-a --epic ex-e`"
	addB := "add ex-b to the epic: `yoyo epic add ex-b --epic ex-e`"
	summary := ownStepsSummary(now,
		ownStep("report-a", "ex-a", ownership.OperatorOwnHold, addA, now.Add(-3*time.Hour)),
		ownStep("report-b", "ex-b", ownership.OperatorOwnHold, addB, now.Add(-2*time.Hour)),
		ownStep("report-a2", "ex-a", ownership.OperatorOwnHold, addA, now.Add(-time.Hour)),
	)

	a := entryWith(t, summary.Decisions, "ex-a")
	if a.Count != 2 || !strings.Contains(a.Says, "2 times") || a.Next != addA || len(a.Detail) != 2 {
		t.Fatalf("the same act on the same item is one line: %+v", a)
	}
	b := entryWith(t, summary.Decisions, "ex-b")
	if b.Count != 1 || b.Next != addB || strings.Contains(b.Says, "ex-a") {
		t.Fatalf("the add of another item is its own line: %+v", b)
	}

	// The same words about a different item stay apart too.
	same := "add the item to its epic with `yoyo epic add`"
	summary = ownStepsSummary(now,
		ownStep("report-c", "ex-c", ownership.OperatorOwnHold, same, now.Add(-2*time.Hour)),
		ownStep("report-d", "ex-d", ownership.OperatorOwnHold, same, now.Add(-time.Hour)),
	)
	if c, d := entryWith(t, summary.Decisions, "ex-c"), entryWith(t, summary.Decisions, "ex-d"); c.Count != 1 || d.Count != 1 {
		t.Fatalf("one act on two items is two lines: %+v, %+v", c, d)
	}

	// A step recorded about no item names the report that records it.
	summary = ownStepsSummary(now, ownStep("report-x", "", ownership.OperatorCredential, "renew the forge token", now.Add(-time.Hour)))
	if x := entryWith(t, summary.Decisions, "report report-x"); x.Next != "renew the forge token" {
		t.Fatalf("a step about no item = %+v", x)
	}
}
