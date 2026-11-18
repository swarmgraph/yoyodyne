package orchestrator

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// A wait recorded on the stopped item's own parent, or an item above it, before
// such waits were refused can never end, so the sweep puts its entry back on the
// development manager's docket as undecided, saying why. A wait on unrelated
// work is left standing (yoyodyne-ifd.428.99.27).
func TestAWaitOnTheStoppedItemsParentOrAboveIsPutBackOnTheDocket(t *testing.T) {
	t.Parallel()

	tree := map[string]beads.WorkItem{
		docketedItem:      {ID: docketedItem, Status: "open", Parent: "yoyodyne-area.7"},
		"yoyodyne-area.7": {ID: "yoyodyne-area.7", Status: "open", Parent: "yoyodyne-area"},
		"yoyodyne-area":   {ID: "yoyodyne-area", Status: "open"},
		"yoyodyne-other":  {ID: "yoyodyne-other", Status: "open"},
	}
	for _, test := range []struct {
		name     string
		waitsOn  string
		putBack  bool
		ancestry string
	}{
		{name: "parent", waitsOn: "yoyodyne-area.7", putBack: true, ancestry: docketedItem + " is under yoyodyne-area.7"},
		{name: "grandparent", waitsOn: "yoyodyne-area", putBack: true, ancestry: docketedItem + " is under yoyodyne-area.7, which is under yoyodyne-area"},
		{name: "unrelated", waitsOn: "yoyodyne-other", putBack: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
			if err != nil {
				t.Fatalf("NewDocketStore() error = %v", err)
			}
			recorded := &recordedDecisions{}
			docketer := Docketer{
				Docket:    store,
				Runs:      recordedRuns{states: []runstate.State{stoppedState()}},
				Decisions: recorded,
				Reruns:    recorded,
				Caps:      docketedCaps,
				Triage:    docketedTriage,
				Clock:     docketClock{},
			}
			built, err := docketer.Build()
			if err != nil || len(built.Entries) != 1 {
				t.Fatalf("Build() = %#v, %v, want the stoppage docketed", built, err)
			}
			entry := built.Entries[0]
			waited := docketedNow
			if took, err := store.Close(triage.Closure{
				SchemaVersion: triage.ClosureSchemaVersion,
				Key:           entry.Key,
				ProductID:     entry.ProductID,
				RunID:         entry.RunID,
				WorkItemID:    entry.WorkItemID,
				Decision:      "wait",
				Reason:        "nothing to do until that closes",
				DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
				ClosedAt:      waited,
				WaitsOn:       test.waitsOn,
			}); err != nil || !took {
				t.Fatalf("Close(wait) = %t, %v", took, err)
			}

			reads := map[string]int{}
			ended, err := docketer.EndWaitsOnAncestors(func(id string) (beads.WorkItem, bool, error) {
				reads[id]++
				item, found := tree[id]
				return item, found, nil
			})
			if err != nil {
				t.Fatalf("EndWaitsOnAncestors() error = %v", err)
			}
			for id, count := range reads {
				if count > 1 {
					t.Fatalf("%s read %d times, want each item read once", id, count)
				}
			}
			entries, err := store.List()
			if err != nil || len(entries) != 1 {
				t.Fatalf("List() = %#v, %v", entries, err)
			}
			closure := entries[0].Closed
			if !test.putBack {
				if ended != 0 || closure == nil || closure.WaitsOn != test.waitsOn || !closure.Holds(docketedNow) {
					t.Fatalf("ended = %d, closure = %#v, want the wait on unrelated work left standing", ended, closure)
				}
				return
			}
			if ended != 1 || closure == nil || closure.Decision != triage.WaitNeverEndsDecision {
				t.Fatalf("ended = %d, closure = %#v, want the entry put back", ended, closure)
			}
			if !entries[0].Undecided(docketedNow) {
				t.Fatalf("entry = %#v, want it undecided, back on the development manager's docket", entries[0])
			}
			for _, says := range []string{test.ancestry, "can never end", "nothing to do until that closes", "needs a new decision"} {
				if !strings.Contains(closure.Reason, says) {
					t.Fatalf("reason = %q, want it to say %q", closure.Reason, says)
				}
			}
			// A second sweep finds no wait standing to end.
			if again, err := docketer.EndWaitsOnAncestors(func(id string) (beads.WorkItem, bool, error) {
				item, found := tree[id]
				return item, found, nil
			}); err != nil || again != 0 {
				t.Fatalf("EndWaitsOnAncestors() again = %d, %v, want nothing put back twice", again, err)
			}
		})
	}
}
