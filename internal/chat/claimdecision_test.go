package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// spentClaimRun is the fresh run a re-run claim was spent on.
const spentClaimRun = "run-fedcba9876543210fedcba9876543210"

// claimOptions is her conversation with a real record of re-run claims, one of
// them spent on spentClaimRun.
func claimOptions(t *testing.T, tracker Tracker, action string) (Options, *runstate.RerunStore, string) {
	t.Helper()

	store, err := runstate.NewRerunStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewRerunStore() error = %v", err)
	}
	key := "stopped_run:" + stoppedRun
	ctx := context.Background()
	if _, err := store.Claim(ctx, runstate.Rerun{
		DocketKey: key, PriorRunID: stoppedRun, WorkItemID: "yoyodyne-ifd.435.1",
		Reason:    "the development manager decided a re-run: the ground moved",
		ClaimedAt: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Settle(ctx, key, spentClaimRun, runstate.PreservedArtifacts{Disposition: runstate.PreservedGone}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	options := triageOptions(t, tracker, nil, trackerReply("Deciding the spent claim.", action))
	options.Stoppages = fakeStoppedRuns{items: map[string]string{spentClaimRun: "yoyodyne-ifd.435.1", stoppedRun: "yoyodyne-ifd.435.1"}}
	options.Claims = store
	return options, store, key
}

func spentClaimItem() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.435.1": {ID: "yoyodyne-ifd.435.1", Title: "duplicate-terminal handling", Status: "open"},
	}}
}

// Her decision to give a spent claim back is recorded on the claim, with who
// decided it, where, and why, and noted on the item. Recording it gives nothing
// back: the harness carries it out.
func TestTheDevelopmentManagerDecidesToGiveASpentClaimBack(t *testing.T) {
	t.Parallel()

	tracker := spentClaimItem()
	options, store, key := claimOptions(t, tracker,
		`{"action":"triage","id":"yoyodyne-ifd.435.1","run":"`+spentClaimRun+`","decision":"give-back-claim","reason":"the replacement run died at its tracker claim and bought nothing"}`)
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	claim, _, err := store.Find(key)
	if err != nil || !claim.GiveBackOutstanding() || claim.ClaimDecision.DecidedBy != RoleTitle(domain.RoleDevelopmentManager) ||
		!strings.Contains(claim.ClaimDecision.Reason, "bought nothing") || claim.ClaimDecision.Conversation == "" {
		t.Fatalf("claim = %#v, %v, want her give-back decision recorded with who, where, and why", claim.ClaimDecision, err)
	}
	if !claim.Spent() {
		t.Fatal("recording the decision gave the claim back; the harness carries it out")
	}
	if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "spent re-run claim to be given back") {
		t.Fatalf("updates = %#v, want the decision noted on the item", tracker.updates)
	}
}

// Leaving it spent is recorded the same way, and leaves it spent.
func TestTheDevelopmentManagerLeavesASpentClaimSpent(t *testing.T) {
	t.Parallel()

	options, store, key := claimOptions(t, spentClaimItem(),
		`{"action":"triage","id":"yoyodyne-ifd.435.1","run":"`+spentClaimRun+`","decision":"leave-claim-spent","reason":"the fresh run reached the item and spent the claim on real work"}`)
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	claim, _, _ := store.Find(key)
	if claim.ClaimDecision == nil || claim.ClaimDecision.Decision != runstate.RerunClaimLeaveSpent || claim.GiveBackOutstanding() {
		t.Fatalf("claim = %#v, want it left spent on her decision", claim.ClaimDecision)
	}
}

// A run no spent claim names is refused, and nothing is recorded.
func TestAClaimDecisionAboutARunNoClaimNamesIsRefused(t *testing.T) {
	t.Parallel()

	const otherRun = "run-11112222333344445555666677778888"
	tracker := spentClaimItem()
	options, store, key := claimOptions(t, tracker,
		`{"action":"triage","id":"yoyodyne-ifd.435.1","run":"`+otherRun+`","decision":"give-back-claim","reason":"it bought nothing"}`)
	options.Stoppages = fakeStoppedRuns{items: map[string]string{otherRun: "yoyodyne-ifd.435.1"}}
	reply := triageSend(t, options)
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want it refused", reply.Actions)
	}
	if claim, _, _ := store.Find(key); claim.ClaimDecision != nil {
		t.Fatalf("claim = %#v, want nothing recorded", claim.ClaimDecision)
	}
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v, want nothing noted", tracker.updates)
	}
}
