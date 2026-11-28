package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The forge check is given the members of an open naming, with whether its epic is
// delivered, and none once the operator ends it or when no epic is named; a
// comment it records owes one correction however often it is recorded.
func TestMergedPullRequestsAreReadOnlyForAnOpenNaming(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	read := func() (epicscope.Record, epicprogress.View) {
		t.Helper()
		record, err := epicscope.Read(root, "yoyodyne")
		if err != nil {
			t.Fatal(err)
		}
		view, err := epicprogress.Read(root, "yoyodyne", record)
		if err != nil {
			t.Fatal(err)
		}
		return record, view
	}
	if record, view := read(); len(epicMembers(record, view, nil)) != 0 {
		t.Fatal("members are given with no epic named")
	}
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-ep", From: "terminal", Description: "Rename the flag.", Members: []string{"yoyodyne-ep.1"}}, at); err != nil {
		t.Fatal(err)
	}
	record, view := read()
	members := epicMembers(record, view, map[string]bool{"yoyodyne-ep": true})
	if len(members) != 1 || members[0].WorkItemID != "yoyodyne-ep.1" || members[0].Epic != "yoyodyne-ep" || !members[0].Delivered || !members[0].NamedAt.Equal(at) {
		t.Fatalf("members = %#v, want the one member of the delivered epic", members)
	}
	// An epic status could not place is skipped for this reading.
	if skipped := epicMembers(record, view, map[string]bool{}); len(skipped) != 0 {
		t.Fatalf("members = %#v, want none of an epic status could not place", skipped)
	}

	store, err := epicprogress.Open(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	comment := orchestrator.MergedComment{Member: members[0], PullRequest: 7,
		Item: runstate.ForgeFeedbackItem{ID: "comment-21", Author: "teammate", Body: "The flag still has the old name.", Path: "flag.go", Line: 3}}
	for want := range []bool{true, false} {
		made, err := oweMergedComment(store, comment)
		if err != nil || made != (want == 0) {
			t.Fatalf("recording %d: made %t, %v", want, made, err)
		}
	}
	_, view = read()
	entry, _ := view.Entry(epicprogress.Of(record.OpenEpics()[0]))
	if len(entry.Comments) != 1 || entry.Comments[0].Owed == nil || entry.Comments[0].Owed.Missing != "teammate said on merged pull request #7 of yoyodyne-ep.1 on flag.go line 3: The flag still has the old name." {
		t.Fatalf("comments = %#v, want one owed correction saying what was asked", entry.Comments)
	}

	if _, err := operator.End("yoyodyne-ep", "done", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if record, view := read(); len(epicMembers(record, view, nil)) != 0 {
		t.Fatal("members are given after the operator ended the naming")
	}
}

// A comment read once the epic was delivered becomes one parked item addressed to
// the Lead Product Manager, however often it is read: again after its identifier
// was written, and again after a stop between filing the item and writing it,
// which finds the item by its marker.
func TestACommentAfterDeliveryFilesOneParkedItem(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	operator, err := epicscope.ForOperator(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	if _, err := operator.Start(epicscope.Naming{Epic: "yoyodyne-ep", From: "terminal", Description: "Rename the flag.", Members: []string{"yoyodyne-ep.1"}}, at); err != nil {
		t.Fatal(err)
	}
	store, err := epicprogress.Open(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	member := orchestrator.EpicMember{Epic: "yoyodyne-ep", NamedAt: at, WorkItemID: "yoyodyne-ep.1", Delivered: true}
	comment := func(id string) orchestrator.MergedComment {
		return orchestrator.MergedComment{Member: member, PullRequest: 7,
			Item: runstate.ForgeFeedbackItem{ID: id, Author: "teammate", Body: "The flag still has the old name.\nPlease fix it.", URL: "https://example.invalid/pr/7#c"}}
	}
	filer := &orchestratortest.RecordingFiler{}
	for round := 0; round < 2; round++ {
		made, err := fileDeliveredComment(context.Background(), store, filer, comment("comment-31"))
		if err != nil || made != (round == 0) {
			t.Fatalf("round %d: made %t, %v", round, made, err)
		}
	}
	if len(filer.Filed) != 1 {
		t.Fatalf("filed %d items, want one", len(filer.Filed))
	}
	filed := filer.Filed[0]
	if !filed.Parking.Parked() || !strings.Contains(string(filed.Parking), "Lead Product Manager") || !strings.Contains(filed.Description, "> Please fix it.") ||
		!strings.Contains(filed.Description, "yoyo epic add yoyodyne-ep <this item>") || !strings.Contains(filed.Notes, epicprogress.LaterMarker("yoyodyne-ep", "comment-31")) {
		t.Fatalf("filed %#v, want a parked item for the Lead Product Manager naming the add, quoting the comment and carrying its marker", filed)
	}
	record, err := epicscope.Read(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	view, err := epicprogress.Read(root, "yoyodyne", record)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := view.Entry(epicprogress.Of(record.OpenEpics()[0]))
	if len(entry.Comments) != 1 || entry.Comments[0].Later == nil || entry.Comments[0].Later.Item != filer.Open[0].ID || entry.Comments[0].Owed != nil {
		t.Fatalf("comments = %#v, want one naming the item filed and owing no piece", entry.Comments)
	}

	// A stop after the item was filed and before it was written into the progress:
	// the next reading finds that item by its marker and files none.
	if _, err := store.DeliveredCommentRead(epicprogress.Of(record.OpenEpics()[0]), epicprogress.Comment{ID: "comment-32", Member: "yoyodyne-ep.1", PullRequest: 7}, "teammate said: and the help text"); err != nil {
		t.Fatal(err)
	}
	filer.Open = append(filer.Open, beads.WorkItem{ID: "yoyodyne-filed", Status: "open", Notes: "Filed by the harness.\n" + epicprogress.LaterMarker("yoyodyne-ep", "comment-32")})
	made, err := fileDeliveredComment(context.Background(), store, filer, comment("comment-32"))
	if err != nil || made || len(filer.Filed) != 1 {
		t.Fatalf("after a stop: made %t, %v, %d filed; want the marked item found", made, err, len(filer.Filed))
	}
	view, _ = epicprogress.Read(root, "yoyodyne", record)
	entry, _ = view.Entry(epicprogress.Of(record.OpenEpics()[0]))
	if kept, _ := entry.Comment("comment-32"); kept.Later == nil || kept.Later.Item != "yoyodyne-filed" {
		t.Fatalf("comment = %#v, want the item found by its marker", kept)
	}

	// Filing it starts no role's pass: no pass is asked of any role, and the one
	// item filed is parked, so nothing selects it.
	requests, err := runstate.NewPassRequestStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager, domain.RoleArchitect, domain.RoleProgramManager} {
		if asked, err := requests.List(string(role)); err != nil || len(asked) != 0 {
			t.Fatalf("the %s was asked for %#v, %v; want no pass", role, asked, err)
		}
	}
}
