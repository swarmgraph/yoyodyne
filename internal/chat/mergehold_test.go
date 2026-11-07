package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeMergeHolds struct {
	decided []runstate.TrackerVersion
	by      string
	refuse  error
}

func (f *fakeMergeHolds) DecideHold(_ context.Context, workItemID string, keep runstate.TrackerVersion, _, by string, _ time.Time) (runstate.TrackerMergeHold, error) {
	if f.refuse != nil {
		return runstate.TrackerMergeHold{}, f.refuse
	}
	f.decided = append(f.decided, keep)
	f.by = by
	return runstate.TrackerMergeHold{WorkItemID: workItemID, Title: "Held"}, nil
}

func TestTheDevelopmentManagerRecordsWhichVersionStands(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "Held", Status: "open"}}}
	holds := &fakeMergeHolds{}
	options := triageOptions(t, tracker, nil, trackerReply("The closure there is right.",
		`{"action":"keep_version","id":"yoyodyne-ifd.311","decision":"shared","reason":"the work finished there"}`))
	options.Reports = &fakeReports{}
	options.TrackerMerge = holds
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the decision applied", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "next forge check carries it out") || !strings.Contains(reply.Actions[0].Summary, `"Held" (yoyodyne-ifd.311)`) {
		t.Errorf("summary = %q, want it said as recorded rather than made", reply.Actions[0].Summary)
	}
	if len(holds.decided) != 1 || holds.decided[0] != runstate.TrackerVersionShared || !strings.Contains(holds.by, "turn") {
		t.Errorf("decided %v by %q", holds.decided, holds.by)
	}

	options = triageOptions(t, tracker, nil, trackerReply("Deciding.",
		`{"action":"keep_version","id":"yoyodyne-ifd.311","decision":"both","reason":"no"}`))
	options.Reports = &fakeReports{}
	options.TrackerMerge = holds
	if reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket."); refusalOf(reply, err) == "" {
		t.Error("a version outside the two was not refused")
	}
	if len(holds.decided) != 1 {
		t.Errorf("the refused block recorded a decision: %v", holds.decided)
	}
}

func TestADecisionAboutAnItemNothingHoldsIsSaid(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{"yoyodyne-ifd.311": {ID: "yoyodyne-ifd.311", Title: "Not held", Status: "open"}}}
	options := triageOptions(t, tracker, nil, trackerReply("Deciding.",
		`{"action":"keep_version","id":"yoyodyne-ifd.311","decision":"shared","reason":"why"}`))
	options.Reports = &fakeReports{}
	options.TrackerMerge = &fakeMergeHolds{refuse: runstate.ErrNoTrackerMergeHold}
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied || !strings.Contains(reply.Actions[0].Failure, "is not held for two machines' versions") {
		t.Fatalf("actions = %#v, want the decision refused in those words", reply.Actions)
	}
}
