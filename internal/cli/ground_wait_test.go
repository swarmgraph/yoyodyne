package cli

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Waiting after a carried-out re-run (yoyodyne-ifd.428.71) was reported to leave
// the original stopped-run entry on the docket. Exercise the durable records
// and the conversation's real closer, including a re-run whose earlier closure
// was never written: the later wait must leave the original entry off the
// undecided docket in either case.
func TestAWaitAfterACarriedOutRerunLeavesTheOriginalStoppageOffTheUndecidedDocket(t *testing.T) {
	t.Parallel()

	for _, closeRerun := range []bool{true, false} {
		name := "with the earlier closure"
		if !closeRerun {
			name = "without the earlier closure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			runs := stoppedRunState(t)
			store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("NewDocketStore() error = %v", err)
			}
			original := stoppedRunOf("yoyodyne-task")
			docketed := original.CompletedAt.Add(time.Minute)
			docketer := docketerOverDocket(runs, store)
			docketer.Clock = stoppedClock{at: docketed}
			built, err := docketer.Build()
			if err != nil || len(built.Entries) != 1 {
				t.Fatalf("Build() = %#v, %v, want the original stoppage", built, err)
			}
			entry := built.Entries[0]
			decided := docketed.Add(time.Minute)
			if _, err := runs.Triage().RecordRerun(ctx, entry.WorkItemID,
				triageDecided(runstate.TriageDecisionRerun, entry.RunID), decided, docketer.Caps); err != nil {
				t.Fatalf("RecordRerun() error = %v", err)
			}
			closer := conversationDocketLog{store: store, clock: stoppedClock{at: decided}, revisitAfter: 2 * time.Hour}
			closure := chat.DocketClosure{
				RunID: entry.RunID, Classes: []triage.Class{triage.ClassStoppedRun},
				Decision: "rerun", Reason: "the stopped work needs a fresh run",
				DecidedBy: "the development manager in conversation chat-0123456789abcdef",
			}
			if closeRerun {
				if closed, err := closer.Close(ctx, closure); err != nil || closed != 1 {
					t.Fatalf("Close(rerun) = %d, %v, want the original entry closed", closed, err)
				}
			}

			// The re-run's claim and its fresh run are the durable evidence the
			// docket reads that the decision was carried out, rather than merely
			// recorded. A new stoppage remains a question of its own.
			carried := decided.Add(time.Minute)
			fresh := original
			fresh.RunID = "run-fedcba9876543210fedcba9876543210"
			fresh.WorktreePath = "/state/worktrees/fresh"
			fresh.Branch = "yoyodyne/task/fresh"
			fresh.StartedAt = carried
			fresh.UpdatedAt = carried.Add(time.Minute)
			fresh.CompletedAt = &fresh.UpdatedAt
			preserved := runstate.PreservedArtifacts{
				Branch: original.Branch, WorktreePath: original.WorktreePath,
				Disposition: runstate.PreservedKept,
			}
			if _, err := runs.Reruns().Claim(ctx, runstate.Rerun{
				DocketKey: entry.Key, PriorRunID: entry.RunID, WorkItemID: entry.WorkItemID,
				Reason: closure.Reason, ClaimedAt: carried, Preserved: preserved,
			}); err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			if err := runs.Create(fresh); err != nil {
				t.Fatalf("Create(fresh run) error = %v", err)
			}
			if _, err := runs.Reruns().Settle(ctx, entry.Key, fresh.RunID, preserved); err != nil {
				t.Fatalf("Settle() error = %v", err)
			}
			docketer.Clock = stoppedClock{at: fresh.UpdatedAt}
			before, err := docketer.Build()
			if err != nil {
				t.Fatalf("Build() before wait error = %v", err)
			}
			wantOpen := 2
			if closeRerun {
				wantOpen = 1
			}
			if len(before.Entries) != wantOpen {
				t.Fatalf("entries before wait = %#v, want %d stoppages", before.Entries, wantOpen)
			}
			if !closeRerun {
				original := before.Entries[0]
				if original.Key != entry.Key || original.Rerun == nil || original.Rerun.RunID != fresh.RunID {
					t.Fatalf("original entry = %#v, want the open stoppage carrying its re-run", original)
				}
			}

			waited := fresh.UpdatedAt.Add(time.Minute)
			if _, err := runs.Triage().RecordDecision(ctx, entry.WorkItemID,
				triageDecided(runstate.TriageDecisionWait, entry.RunID), waited); err != nil {
				t.Fatalf("RecordDecision(wait) error = %v", err)
			}
			closer.clock = stoppedClock{at: waited}
			closure.Decision = "wait"
			closure.Reason = "the original stoppage has already been run again"
			closure.Revisit = true
			if _, err := closer.Close(ctx, closure); err != nil {
				t.Fatalf("Close(wait) error = %v", err)
			}
			docketer.Clock = stoppedClock{at: waited}
			for pass := 0; pass < 2; pass++ {
				rebuilt, err := docketer.Build()
				if err != nil {
					t.Fatalf("Build() after wait error = %v", err)
				}
				if len(rebuilt.Entries) != 1 || rebuilt.Entries[0].RunID != fresh.RunID {
					t.Fatalf("undecided entries = %#v, want only the fresh run's stoppage", rebuilt.Entries)
				}
				if !closeRerun && (len(rebuilt.Waiting) != 1 || rebuilt.Waiting[0].Key != entry.Key) {
					t.Fatalf("waiting = %#v, want the original stoppage demoted by the wait", rebuilt.Waiting)
				}
			}
			entries, err := store.List()
			if err != nil || len(entries) != 2 {
				t.Fatalf("List() = %#v, %v, want both stoppages still recorded", entries, err)
			}
		})
	}
}
