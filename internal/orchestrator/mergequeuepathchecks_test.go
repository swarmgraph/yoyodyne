package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The checks the queue runs on an entry's candidate follow from the paths its
// change makes, never from the kind it was recorded as: a change wholly inside
// the artifact homes runs the document checks, and any other runs the
// configured checks whole, an entry recorded as a document among them.
func TestTheQueueChoosesAnEntrysChecksFromThePathsItChanges(t *testing.T) {
	t.Parallel()

	full, documents := []string{"make test", "make race"}, []string{"make docs"}
	for _, tc := range []struct {
		name               string
		recordedAsDocument bool
		paths              []string
		unreadable         bool
		want               []string
	}{
		{name: "documents only", recordedAsDocument: true, paths: []string{"docs/designs/a.md", "docs/designs/README.md"}, want: documents},
		{name: "code only", paths: []string{"internal/a/a.go"}, want: full},
		{name: "documents and code recorded as a document", recordedAsDocument: true, paths: []string{"docs/designs/a.md", "internal/a/a.go"}, want: full},
		{name: "a document whose paths cannot be read", recordedAsDocument: true, unreadable: true, want: full},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cfg config.Config
			cfg.Product.Specifications, cfg.Product.Designs, cfg.Product.Decisions, cfg.Product.Invariants = "docs/product", "docs/designs", "docs/decisions", "docs/decisions/invariants"
			cfg.Checks, cfg.DocumentChecks = full, documents
			pipeline := &Pipeline{Config: cfg, Store: orderRuns{}}
			const runID = "run-0123456789abcdef0123456789abcdef"
			entry := runstate.MergeQueueEntry{EntryID: "entry", Order: 1, WorkItemID: "yoyodyne-1", RunID: runID, ApprovedHead: "head"}
			if tc.recordedAsDocument {
				entry.WorkItemID = runID
			}
			paths := orderPaths{paths: map[string][]string{"head": tc.paths}}.CandidatePaths
			if tc.unreadable {
				paths = func(context.Context, string, string) ([]string, error) {
					return nil, errors.New("git is not answering")
				}
			}
			got := pipeline.mergeQueueChecks(context.Background(), paths, entry, nil)
			if want := runstate.NewMergeQueueCheckConfiguration(tc.want); !got.Same(want) {
				t.Fatalf("mergeQueueChecks() = %v, want %v", got.Commands, tc.want)
			}
		})
	}
}
