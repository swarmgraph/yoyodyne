package cli

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
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
