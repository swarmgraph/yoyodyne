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
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
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

// The developer always writes the same oversized file, so the change it hands
// back after the first refusal is refused again: the first refusal goes back to
// the developer for one repair attempt, and the second stops the run for the
// development manager with nothing checked or reviewed.
func TestAChangeOverTheReviewBoundTwiceIsStoppedBeforeAnyCheckOrReview(t *testing.T) {
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
	developers := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developers) != 2 || len(provider.RequestsForRole(domain.RoleReviewer)) != 0 {
		t.Fatalf("developer invocations = %d, reviewer invocations = %d; want the first attempt, one hand-back, and no review",
			len(developers), len(provider.RequestsForRole(domain.RoleReviewer)))
	}
	// The size limits are in the first attempt's instructions, and the hand-back
	// names the file, its size, and the limit it was compared against.
	if first := developers[0].Prompt; !strings.Contains(first, "How large a change the reviewer can be shown") || !strings.Contains(first, "256 KiB of diff for the whole change") {
		t.Errorf("first developer prompt does not state the size limits")
	}
	size, bound := "internal/big/big.go, source, 307200 bytes, diff ", "over the whole 262144-byte patch bound"
	if handed := developers[1].Prompt; !strings.Contains(handed, "# Too large to review: repair required") ||
		!strings.Contains(handed, size) || !strings.Contains(handed, bound) || !strings.Contains(handed, "repair attempt 1 of 2") {
		t.Errorf("hand-back prompt does not carry the refusal with the file, its size, and the limit:\n%s", handed)
	}

	assertSavedStopClass(t, store, outcome.RunID, runstate.StopReviewBound)
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != runstate.PhaseDeveloping || state.RepairAttempts != 1 || state.ChecksPassed != nil || state.ReviewDecision != "" || !state.ReviewBoundReturned {
		t.Fatalf("ended at phase %q, repairs %d, checks %+v, review %q, returned %v; want it ended at developing with only the one hand-back spent",
			state.Phase, state.RepairAttempts, state.ChecksPassed, state.ReviewDecision, state.ReviewBoundReturned)
	}
	// The sizes and the bound are on the run's record, the item, and the docket.
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
	if !strings.Contains(tracker.BlockReason, "already handed back to its developer once") {
		t.Errorf("item blocker = %q, want it to say the change was handed back once already", tracker.BlockReason)
	}
	if outcome.Preservation == nil || outcome.Preservation.Lost() {
		t.Fatalf("preservation = %+v, want the change kept", outcome.Preservation)
	}
	if _, statErr := os.Stat(filepath.Join(state.WorktreePath, "internal", "big", "big.go")); statErr != nil {
		t.Fatalf("the over-bound change is gone from its worktree: %v", statErr)
	}
}

// The first refusal goes back to the developer, which makes the change fit; the
// change is then checked, reviewed, and promoted as any other.
func TestAChangeOverTheReviewBoundOnceGoesBackToItsDeveloper(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		big := filepath.Join(request.WorkingDirectory, "internal", "big", "big.go")
		if strings.Contains(request.Prompt, "# Too large to review: repair required") {
			if err := os.Remove(big); err != nil {
				return err
			}
			return writeFeature(request)
		}
		return writeNewFile("internal/big/big.go", overBoundBytes)(request)
	}, approveVerdict)
	marker := filepath.Join(t.TempDir(), "checked")
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"touch '" + marker + "'"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Integration == nil {
		t.Fatalf("Run() = %+v, %v; want the change made to fit, then checked, reviewed, and promoted", outcome, err)
	}
	if developers, reviewers := len(provider.RequestsForRole(domain.RoleDeveloper)), len(provider.RequestsForRole(domain.RoleReviewer)); developers != 2 || reviewers != 1 {
		t.Fatalf("developer invocations = %d, reviewer invocations = %d; want one hand-back and one review", developers, reviewers)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the checks did not run on the change once it fitted: %v", statErr)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.RepairAttempts != 1 || !state.ReviewBoundReturned || state.ReviewBoundRefusal != nil {
		t.Fatalf("repairs %d, returned %v, refusal %+v; want the one hand-back spent and the refusal cleared once the change fitted",
			state.RepairAttempts, state.ReviewBoundReturned, state.ReviewBoundRefusal)
	}
}

// A run interrupted after the hand-back was recorded resumes on the same
// refusal rather than on the first attempt's prompt.
func TestAResumedRunIsHandedTheReviewBoundRefusalAgain(t *testing.T) {
	t.Parallel()

	state := runstate.State{RepairAttempts: 1, ReviewBoundRefusal: &runstate.ReviewBoundRefusal{
		Files: []string{"internal/big/big.go, source, 307200 bytes, diff 307300 bytes, over the whole 262144-byte patch bound"}, Omitted: 3,
	}}
	prompt, err := resumedDeveloperPrompt(state, "", "", "", t.TempDir(), nil, protectedpath.Set{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Too large to review: repair required", "repair attempt 1 of 2", "over the whole 262144-byte patch bound", "and 3 more source or test files"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("resumed prompt lacks %q", want)
		}
	}
	if !state.HandedBack() {
		t.Error("a run carrying a review-bound refusal is not read as inside its repair loop")
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
