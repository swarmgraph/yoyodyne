package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A hold with team mode off would keep its item from starting with nothing left to
// end it, so the next check ends it, and a decision is refused rather than promised.
func TestAMergeHoldLeftBehindEndsOnceTeamModeIsOff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store, err := runstate.NewTrackerMergeStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHold(runstate.TrackerMergeHold{
		WorkItemID: "t-a", Title: "Share the tracker", HeldAt: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC), TargetBranch: "main",
		Differences: []runstate.TrackerMergeDifference{{Field: "status", ThisMachine: "open", Shared: "closed"}},
	}); err != nil {
		t.Fatal(err)
	}
	var parts components
	parts.stateRoot = root
	parts.config = config.Config{}
	parts.config.Product.ID = "yoyodyne"

	// With team mode on it stands, and decisions are taken.
	parts.config.Team.Enabled = true
	var said bytes.Buffer
	endMergeHoldsLeftBehind(ctx, parts, &said)
	holds, err := readmodel.HeldForAPerson(ctx, runs, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := holds.Reason("t-a"); !held {
		t.Fatal("team mode on: the hold was ended")
	}
	if conversationTrackerMerge(parts) == nil {
		t.Error("team mode on: decisions are refused")
	}

	parts.config.Team.Enabled = false
	endMergeHoldsLeftBehind(ctx, parts, &said)
	holds, err = readmodel.HeldForAPerson(ctx, runs, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reason, held := holds.Reason("t-a"); held {
		t.Errorf("team mode off: still held: %s", reason)
	}
	if !strings.Contains(said.String(), "team mode is off") || !strings.Contains(said.String(), `"Share the tracker" (t-a)`) {
		t.Errorf("said %q, want the ended hold named", said.String())
	}
	if conversationTrackerMerge(parts) != nil {
		t.Error("team mode off: a decision would be recorded that nothing carries out")
	}
	if trackerShareFrom(parts) != nil {
		t.Error("team mode off: the shared export would be read")
	}
}
