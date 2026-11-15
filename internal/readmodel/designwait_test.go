package readmodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeNeedsDesignLabelled struct {
	record runstate.NeedsDesignLabelled
	err    error
}

func (f fakeNeedsDesignLabelled) Load() (runstate.NeedsDesignLabelled, bool, error) {
	return f.record, len(f.record.Items) > 0, f.err
}

func designWaitSources(items []beads.WorkItem, since map[string]time.Time) Sources {
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{byStatus: map[string][]beads.WorkItem{"open": items}, ready: items}}
	record := runstate.NeedsDesignLabelled{Items: map[string]runstate.NeedsDesignWait{}}
	for id, at := range since {
		record.Items[id] = runstate.NeedsDesignWait{Since: at}
	}
	sources.NeedsDesignLabelled = fakeNeedsDesignLabelled{record: record}
	return sources
}

func designWaitsIn(entries []Attention) []Attention {
	var waits []Attention
	for _, entry := range entries {
		if entry.Kind == AttentionHeldWork && entry.HeldWork != nil && entry.HeldWork.Awaiting == HeldAwaitingDesign {
			waits = append(waits, entry)
		}
	}
	return waits
}

// Every item carrying the label is among the factory problems and on the
// attention line, as the architect's, with since when in local time and its
// age; a P0 comes ahead of lower priorities however much longer they waited.
func TestAnItemWaitingOnDesignIsAFactoryProblemOfTheArchitectsWithItsAge(t *testing.T) {
	t.Parallel()

	labels := []string{domain.NeedsDesignLabel}
	items := []beads.WorkItem{
		{ID: "yoyodyne-low", Title: "Low and old", Status: "open", Priority: 2, Labels: labels},
		{ID: "yoyodyne-top", Title: "Top and recent", Status: "open", Priority: 0, Labels: labels},
		{ID: "yoyodyne-unseen", Title: "Not yet seen by a pull", Status: "open", Priority: 2, Labels: labels},
		{ID: "yoyodyne-plain", Title: "Ordinary work", Status: "open", Priority: 0},
	}
	sources := designWaitSources(items, map[string]time.Time{
		"yoyodyne-low": moment.Add(-72 * time.Hour),
		"yoyodyne-top": moment.Add(-26*time.Hour - 30*time.Minute),
	})

	standing := ReadStanding(context.Background(), sources)
	factory := designWaitsIn(standing.FactoryProblems)
	if len(factory) != 3 {
		t.Fatalf("factory problems waiting on design = %+v, want the three labelled items", factory)
	}
	for index, want := range []string{"yoyodyne-top", "yoyodyne-low", "yoyodyne-unseen"} {
		if factory[index].WorkItemID != want {
			t.Fatalf("entry %d is %s, want %s: P0 first, then the longest wait, then a wait not yet recorded", index, factory[index].WorkItemID, want)
		}
	}
	top := factory[0]
	if top.Mover != MoverArchitect {
		t.Fatalf("mover = %q, want the architect's", top.Mover)
	}
	wantSince := "since " + moment.Add(-26*time.Hour-30*time.Minute).Local().Format("2006-01-02 15:04 MST")
	if what := top.What(); !strings.HasPrefix(what, "p0 yoyodyne-top waiting on the architect's design") ||
		!strings.Contains(what, wantSince) || !strings.Contains(what, "26 hours 30 minutes ago") {
		t.Fatalf("what = %q, want the priority, the item, %q, and its age", what, wantSince)
	}
	if !strings.Contains(top.Whose(), "remove") || top.Label() != "waiting on design" {
		t.Fatalf("whose = %q, label = %q", top.Whose(), top.Label())
	}
	if what := factory[2].What(); !strings.Contains(what, "not recorded yet") {
		t.Fatalf("an item no pull has seen says %q, want its time said as not recorded", what)
	}
	if line := designWaitsIn(standing.NeedsHuman); len(line) != 3 {
		t.Fatalf("attention line waiting on design = %+v, want the same three entries `yoyo status` prints", line)
	}

	// The JSON the dashboard reads carries the time and reads back whole.
	encoded, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Attention
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("the entry does not read back: %v\n%s", err, encoded)
	}
	if decoded.HeldWork == nil || decoded.HeldWork.Since == nil || decoded.HeldWork.Priority == nil || *decoded.HeldWork.Priority != 0 {
		t.Fatalf("decoded = %+v, want since and priority carried", decoded.HeldWork)
	}
}

// The entry leaves when the label comes off, whatever the record still says.
func TestAnItemLeavesTheFactoryProblemsWhenItsLabelComesOff(t *testing.T) {
	t.Parallel()

	labelled := beads.WorkItem{ID: "yoyodyne-item", Title: "Item", Status: "open", Priority: 0, Labels: []string{domain.NeedsDesignLabel}}
	since := map[string]time.Time{"yoyodyne-item": moment.Add(-2 * time.Hour)}
	before := ReadStanding(context.Background(), designWaitSources([]beads.WorkItem{labelled}, since))
	if len(designWaitsIn(before.FactoryProblems)) != 1 {
		t.Fatalf("factory problems = %+v, want the labelled item", before.FactoryProblems)
	}

	unlabelled := labelled
	unlabelled.Labels = nil
	after := ReadStanding(context.Background(), designWaitSources([]beads.WorkItem{unlabelled}, since))
	if waits := designWaitsIn(after.FactoryProblems); len(waits) != 0 {
		t.Fatalf("factory problems after the label came off = %+v, want none", waits)
	}
	if waits := designWaitsIn(after.NeedsHuman); len(waits) != 0 {
		t.Fatalf("attention line after the label came off = %+v, want none", waits)
	}
}

// The queue's refusal says since when, as the scheduler's does.
func TestTheQueuesRefusalOfALabelledItemSaysSinceWhen(t *testing.T) {
	t.Parallel()

	at := moment.Add(-5 * time.Hour)
	sources := designWaitSources([]beads.WorkItem{
		{ID: "yoyodyne-item", Title: "Item", Status: "open", Labels: []string{domain.NeedsDesignLabel}},
	}, map[string]time.Time{"yoyodyne-item": at})
	queue, _, _, _, err := readQueue(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Entries) != 1 {
		t.Fatalf("queue = %+v", queue.Entries)
	}
	want := "labelled " + domain.NeedsDesignLabel + " since " + at.Local().Format("2006-01-02 15:04 MST") + ","
	if hold := queue.Entries[0].Hold(); !strings.HasPrefix(hold, want) {
		t.Fatalf("hold = %q, want it to begin %q", hold, want)
	}
}
