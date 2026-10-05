package beads

import (
	"context"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestRelevantGoalsMetadataConformance(t *testing.T) {
	project := newTracker(t)
	client := Client{Runner: execution.OSProcessRunner{}, Dir: project, Timeout: conformanceTimeout}
	ctx := context.Background()
	goals := []string{"Keep the work traceable.", "Use ordinary words."}
	item, err := client.Create(ctx, NewWorkItem{Title: "Record relevant goals", Description: "Preserve the list.", Type: "task", RelevantGoals: goals})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := client.Show(ctx, item.ID)
	if err != nil || !slices.Equal(shown.RelevantGoals, goals) {
		t.Fatalf("shown = %#v, %v", shown, err)
	}
	for _, listing := range []func(context.Context) ([]WorkItem, error){func(ctx context.Context) ([]WorkItem, error) { return client.List(ctx, "open") }, client.Ready} {
		items, err := listing(ctx)
		if err != nil || len(items) != 1 || !slices.Equal(items[0].RelevantGoals, goals) {
			t.Fatalf("listing = %#v, %v", items, err)
		}
	}
	updated, err := client.Update(ctx, item.ID, WorkItemChange{AppendNotes: "An unrelated note."})
	if err != nil || !slices.Equal(updated.RelevantGoals, goals) {
		t.Fatalf("unrelated update = %#v, %v", updated, err)
	}
	updated, err = client.Update(ctx, item.ID, WorkItemChange{RelevantGoals: goals[:1]})
	if err != nil || !slices.Equal(updated.RelevantGoals, goals[:1]) {
		t.Fatalf("replaced = %#v, %v", updated, err)
	}
	updated, err = client.Update(ctx, item.ID, WorkItemChange{RelevantGoals: []string{}})
	if err != nil || len(updated.RelevantGoals) != 0 {
		t.Fatalf("cleared = %#v, %v", updated, err)
	}
}
