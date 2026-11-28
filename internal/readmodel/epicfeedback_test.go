package readmodel

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
)

// A comment left on a member's merged pull request once the epic was delivered is
// shown beside the delivery, naming the item it became and the add that would put
// it in; adding that item opens the epic again, and the line is gone.
func TestFeedbackAfterDeliveryIsShownBesideTheEpicAndAddingItOpensTheEpic(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.landed(stateOne, stateTwo)
	b.deliver(hours(2))
	b.entry.Comments = []epicprogress.Comment{{ID: "comment-31", Member: stateOne, PullRequest: 7, At: hours(3), Became: "read after delivery",
		Later: &epicprogress.Later{Says: "teammate said: the flag still has the old name"}}}
	filing := b.wants(EpicIntegrated, "Feedback after delivery, on merged pull request #7 of "+stateOne+" (comment comment-31): teammate said: the flag still has the old name; the harness files it")
	if len(filing.Feedback) != 1 || filing.Feedback[0].WorkItemID != "" {
		t.Fatalf("feedback = %#v, want one not yet filed", filing.Feedback)
	}
	b.entry.Comments[0].Later.Item = stateOther
	b.wants(EpicIntegrated, "it is "+stateOther+", which starts nothing", "`yoyo epic add "+stateEpic+" "+stateOther+"`")

	b.status(stateOther, "closed")
	if closed := b.wants(EpicIntegrated); len(closed.Feedback) != 0 {
		t.Fatalf("feedback = %#v, want none once its item is closed", closed.Feedback)
	}
	b.status(stateOther, "open")
	b.ready = []string{stateOther}
	_, err := operatorOf(t, b.root).Add(stateEpic, stateOther, hours(4))
	b.must(err)
	opened := b.wants(EpicWorking, "opened again since")
	if len(opened.Feedback) != 0 || strings.Contains(opened.render(), "Feedback after delivery") {
		t.Fatalf("an epic opened again still shows %#v", opened.Feedback)
	}
}

// The Lead Product Manager's turns are given the same line, while the item is open
// and outside the epic, also once the epic opened again for another reason; once
// the operator ends the naming nothing is given.
func TestFeedbackWaitingIsWordedForTheLeadProductManagersTurns(t *testing.T) {
	t.Parallel()
	b := newStateBench(t)
	b.entry.Comments = []epicprogress.Comment{{ID: "comment-31", Member: stateOne, PullRequest: 7, At: hours(3), Became: "read after delivery",
		Later: &epicprogress.Later{Says: "teammate said: rename the flag", Item: stateOther}}}
	b.write()
	lines := func() []string {
		t.Helper()
		record, err := epicscope.Read(b.root, "yoyodyne")
		b.must(err)
		view, err := epicprogress.Read(b.root, "yoyodyne", record)
		b.must(err)
		return EpicFeedbackWaiting(record, view, map[string]beads.WorkItem{stateOther: b.items[stateOther]})
	}
	if got := lines(); len(got) != 1 || !strings.HasPrefix(got[0], "Epic "+stateEpic+": feedback after delivery") || !strings.Contains(got[0], "`yoyo epic add "+stateEpic+" "+stateOther+"`") {
		t.Fatalf("lines = %q", got)
	}
	// Not delivered, the epic still shows it beside its other waits.
	b.wants(EpicBlockedOutside, "Feedback after delivery")
	b.status(stateOther, "closed")
	if got := lines(); len(got) != 0 {
		t.Fatalf("lines = %q, want none once the item is closed", got)
	}
	b.status(stateOther, "open")
	b.end(hours(5))
	if got := lines(); len(got) != 0 {
		t.Fatalf("lines = %q, want none once the naming ended", got)
	}
}
