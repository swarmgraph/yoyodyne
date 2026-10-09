package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// overBoundBytes is bigger than the 256 KiB bound on the whole of the reviewer's
// copy. The attempt is committed before it is measured, so a new file is held
// to that bound rather than to the smaller one on an uncommitted new file.
const overBoundBytes = 300 << 10

// writeNewFile is a developer that adds one file of the given size.
func writeNewFile(path string, size int) func(backend.RunRequest) error {
	return func(request backend.RunRequest) error {
		target := filepath.Join(request.WorkingDirectory, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, []byte(strings.Repeat("x\n", size/2)), 0o600)
	}
}

func TestAChangeOverTheReviewBoundIsStoppedBeforeAnyCheckOrReview(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(writeNewFile("internal/big/big.go", overBoundBytes), approveVerdict)
	// The check leaves a mark wherever it runs, so a check that ran cannot hide.
	marker := filepath.Join(t.TempDir(), "checked")
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"touch '" + marker + "'"})
	docket := &memoryDocket{}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "too large for the reviewer's copy") {
		t.Fatalf("Run() error = %v, want the change stopped for the review bound", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("a check ran on a change the reviewer could not have been shown (stat error = %v)", statErr)
	}
	if developers, reviewers := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer)); developers != 1 || reviewers != 0 {
		t.Fatalf("developer invocations = %d, reviewer invocations = %d; want one developer attempt, nothing handed back, and no review", developers, reviewers)
	}

	assertSavedStopClass(t, store, outcome.RunID, runstate.StopReviewBound)
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != runstate.PhaseDeveloping || state.RepairAttempts != 0 || state.ChecksPassed != nil || state.ReviewDecision != "" {
		t.Fatalf("ended at phase %q, repairs %d, checks %+v, review %q; want it ended at developing with nothing spent past the attempt",
			state.Phase, state.RepairAttempts, state.ChecksPassed, state.ReviewDecision)
	}
	// The sizes and the bound are on the run's record, the item, and the docket.
	size, bound := "internal/big/big.go, source, 307200 bytes, diff ", "over the whole 262144-byte patch bound"
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %+v, want one entry for the stopped run", docket.entries)
	}
	for name, text := range map[string]string{
		"run record blocker": state.Blocker, "run record failure": state.Failure,
		"item": tracker.BlockReason, "docket entry": docket.entries[0].Blocker,
	} {
		if !strings.Contains(text, size) || !strings.Contains(text, bound) {
			t.Errorf("%s = %q, want it to carry %q and %q", name, text, size, bound)
		}
	}
	if outcome.Preservation == nil || outcome.Preservation.Lost() {
		t.Fatalf("preservation = %+v, want the change kept", outcome.Preservation)
	}
	if _, statErr := os.Stat(filepath.Join(state.WorktreePath, "internal", "big", "big.go")); statErr != nil {
		t.Fatalf("the over-bound change is gone from its worktree: %v", statErr)
	}
}

func TestAChangeWhoseOnlyOverBoundFilesAreTestDataIsCheckedAndReviewed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		if err := writeFeature(request); err != nil {
			return err
		}
		return writeNewFile("internal/big/testdata/render.golden", overBoundBytes)(request)
	}, `{"decision":"approve","approves":"implementation","summary":"sound","fixtures":["internal/big/testdata/render.golden"]}`)
	marker := filepath.Join(t.TempDir(), "checked")
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"touch '" + marker + "'"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Integration == nil {
		t.Fatalf("Run() = %+v, %v; want omitted test data to proceed to its checks, review, and promotion as before", outcome, err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the checks did not run: %v", statErr)
	}
	if reviewers := len(provider.RequestsForRole(domain.RoleReviewer)); reviewers != 1 {
		t.Fatalf("reviewer invocations = %d, want one", reviewers)
	}
}
