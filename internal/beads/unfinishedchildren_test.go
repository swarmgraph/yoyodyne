package beads

import (
	"context"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// UnfinishedChildren is `bd list --parent`, which bd answers with the children
// of that item and leaves closed work out of; held against bd itself, so a bd
// that started listing closed children, or grandchildren, or other items, is
// caught here rather than as a wait refused or allowed on the wrong reading
// (yoyodyne-ifd.428.99.30).
func TestUnfinishedChildrenConformance(t *testing.T) {
	t.Parallel()

	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()

	create := func(title, parent string) WorkItem {
		t.Helper()
		item, err := client.Create(ctx, NewWorkItem{Title: title, Description: "d", Type: "task", Parent: parent})
		if err != nil {
			t.Fatalf("Create(%q) error = %v", title, err)
		}
		return item
	}
	area := create("An area of work", "")
	open := create("Open work in the area", area.ID)
	blocked := create("Stopped work in the area", area.ID)
	if _, err := client.Block(ctx, blocked.ID, "waiting on a person"); err != nil {
		t.Fatalf("Block() error = %v", err)
	}
	finished := create("Finished work in the area", area.ID)
	if _, err := client.Complete(ctx, finished.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	unrelated := create("Unrelated work", "")
	finishedArea := create("A finished area", "")
	last := create("Its last work", finishedArea.ID)
	if _, err := client.Complete(ctx, last.ID, "landed"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	ids := func(items []WorkItem) []string {
		var listed []string
		for _, item := range items {
			listed = append(listed, item.ID)
		}
		slices.Sort(listed)
		return listed
	}
	children, err := client.UnfinishedChildren(ctx, area.ID)
	if err != nil {
		t.Fatalf("UnfinishedChildren(area) error = %v", err)
	}
	want := []string{open.ID, blocked.ID}
	slices.Sort(want)
	if got := ids(children); !slices.Equal(got, want) {
		t.Fatalf("UnfinishedChildren(area) = %v, want only its unfinished children %v", got, want)
	}
	for _, none := range []WorkItem{unrelated, finishedArea, open} {
		children, err := client.UnfinishedChildren(ctx, none.ID)
		if err != nil || len(children) != 0 {
			t.Fatalf("UnfinishedChildren(%s) = %v, %v, want none", none.ID, ids(children), err)
		}
	}
}
