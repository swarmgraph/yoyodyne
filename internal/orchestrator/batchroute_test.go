package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func batchRouteOn(t *testing.T, f teamPullRequestFixture) (Reconciler, *runstate.BatchRecordStore) {
	t.Helper()
	records, err := runstate.NewBatchRecordStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultBatchPullRequest()
	settings.Enabled = true
	reconciler := f.reconciler(t)
	reconciler.BatchRoute = &BatchRoute{Settings: settings, Records: records}
	return reconciler, records
}

func batchOf(f teamPullRequestFixture, number int, head string, items ...string) runstate.BatchRecord {
	record := runstate.BatchRecord{
		TargetBranch: teamTarget, UpstreamBranch: teamUpstream, BatchBranch: "yoyodyne/batch/" + teamTarget,
		PullRequest: &runstate.BatchPullRequestRef{Number: number, OpenedAt: time.Now()},
		Head:        head, UpdatedAt: time.Now(),
	}
	for _, id := range items {
		record.Items = append(record.Items, runstate.BatchRecordItem{WorkItemID: id, LandingCommit: f.landing[id]})
	}
	return record
}

func TestEachItemsNotesSayWhereItStandsOnTheBatchRouteWhenItChanges(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	reconciler, records := batchRouteOn(t, f)
	notesOf := func() string { return f.tracker.items["yoyodyne-a"].Notes }

	// No batch yet: the item is unpublished, said once however often a check reads.
	for range 2 {
		if _, err := reconciler.NoteBatchStates(context.Background()); err != nil {
			t.Fatalf("NoteBatchStates() error = %v", err)
		}
	}
	if got := strings.Count(notesOf(), "Batch pull request: unpublished"); got != 1 {
		t.Fatalf("the notes say unpublished %d times, want once:\n%s", got, notesOf())
	}
	// In the open batch, and then approved on its head.
	head := strings.Repeat("e", 40)
	record := batchOf(f, 12, head, "yoyodyne-a")
	if err := records.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.NoteBatchStates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notesOf(), "Batch pull request: in the open batch: pull request #12 carries it at "+head[:12]) || !strings.Contains(notesOf(), "waiting on a person's approval") {
		t.Fatalf("notes = %q", notesOf())
	}
	record.Approval = &runstate.BatchApproval{By: "pat", Commit: head, At: time.Now()}
	if err := records.Save(record); err != nil {
		t.Fatal(err)
	}
	settled, err := reconciler.NoteBatchStates(context.Background())
	if err != nil || len(settled) != 2 || settled[0].WorkItemID != "yoyodyne-a" || !settled[0].Written || settled[0].State != runstate.BatchApproved || !strings.Contains(notesOf(), "Batch pull request: approved:") {
		t.Fatalf("settled = %#v, %v; notes = %q", settled, err, notesOf())
	}
	// The same state again writes nothing.
	before := f.tracker.notes["yoyodyne-a"]
	if _, err := reconciler.NoteBatchStates(context.Background()); err != nil || f.tracker.notes["yoyodyne-a"] != before {
		t.Fatalf("a repeat check wrote a note: %v", err)
	}
}

func TestTheBatchRouteWritesNothingWhileOff(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	settled, err := f.reconciler(t).NoteBatchStates(context.Background())
	if err != nil || settled != nil || f.tracker.notes["yoyodyne-a"] != 0 {
		t.Fatalf("NoteBatchStates() with no route = %v, %v, %d notes", settled, err, f.tracker.notes["yoyodyne-a"])
	}
	if _, ours := f.reconciler(t).batchPullRequestIs(teamTarget, 5); ours {
		t.Fatal("a route that is off holds a batch")
	}
}

func TestTheBatchRecordEndsWhenItsPullRequestMergesAndTheItemsSayMerged(t *testing.T) {
	t.Parallel()

	f := newTeamPullRequestFixture(t, "yoyodyne-a")
	f.open(5, f.landing["yoyodyne-a"])
	f.mustTrack(t, 5, false, "yoyodyne-a")
	reconciler, records := batchRouteOn(t, f)
	if err := records.Save(batchOf(f, 5, strings.Repeat("e", 40), "yoyodyne-a")); err != nil {
		t.Fatal(err)
	}
	mergeCommit := strings.Repeat("9", 40)
	f.reader.requests[5] = publish.TeamPullRequest{State: "MERGED", Merged: true, BaseBranch: teamUpstream, MergeCommit: mergeCommit}
	settled, err := reconciler.SettleTeamPullRequests(context.Background())
	if err != nil || len(settled) != 1 || settled[0].Failure != "" {
		t.Fatalf("SettleTeamPullRequests() = %#v, %v", settled, err)
	}
	record, found, err := records.Load(teamTarget)
	if err != nil || !found || record.Merged == nil || record.Merged.Commit != mergeCommit || record.StateOf("yoyodyne-a", f.landing["yoyodyne-a"]) != runstate.BatchMerged {
		t.Fatalf("record = %#v, %v, %v; want it merged", record, found, err)
	}
	if notes := f.tracker.items["yoyodyne-a"].Notes; !strings.Contains(notes, "Batch pull request: merged as pull request #5.") {
		t.Fatalf("notes = %q, want the batch merge said", notes)
	}
}
