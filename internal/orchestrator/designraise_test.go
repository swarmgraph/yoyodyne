package orchestrator

// A developer's raise that the item waits on the architect's design
// (docs/designs/needs-design-gate.md): the run ends in the round it was raised
// with the change kept on its branch, no repair attempt is spent, and the item
// comes back open, labelled needs-design, with the developer's question on it.
// The label then holds it from the next pull until the architect removes it.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

const designQuestion = "should a replacement that is interrupted mid-write leave the old file or nothing at all?"

func TestADevelopersRaiseReturnsItsItemLabelledNeedsDesign(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	ran := filepath.Join(t.TempDir(), "check-ran")
	provider := orchestratortest.RoleBackend(writeFeature, approveVerdict)
	provider.DeveloperFinalText = "The behaviour on an interrupted write is undecided.\n\n" +
		landingBlock(`{"outcome":"needs-design","why":"`+designQuestion+`"}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"touch " + ran})
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertSavedStopClass(t, store, outcome.RunID, runstate.StopNeedsDesign)
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("status = %q, want a raise to be a run that did its job: %#v", outcome.Status, outcome)
	}
	// The price: nothing checked, reviewed, integrated, or published, and no
	// repair attempt spent.
	if outcome.Integration != nil || outcome.PullRequest != nil {
		t.Fatalf("a raise integrated or published: %#v, %#v", outcome.Integration, outcome.PullRequest)
	}
	if _, err := os.Stat(ran); err == nil || len(outcome.Checks) != 0 {
		t.Errorf("the checks ran on a run whose developer raised the item for design")
	}
	if outcome.RepairAttempts != 0 || outcome.ReviewDecision != "" {
		t.Errorf("repair attempts = %d, review = %q, want the run ended where it was raised", outcome.RepairAttempts, outcome.ReviewDecision)
	}
	if outcome.Landing != landing.OutcomeNeedsDesign || !outcome.RaisedForDesign() || outcome.Escalated() {
		t.Fatalf("landing = %q, raised for design = %t, escalated = %t", outcome.Landing, outcome.RaisedForDesign(), outcome.Escalated())
	}
	// The item: open, labelled, unparked, with the question on it, never closed.
	if outcome.WorkItemClosed || tracker.Closed {
		t.Fatalf("the item closed against a raise for design; calls = %v", tracker.Calls)
	}
	if tracker.Item.Status != "open" || !domain.NeedsDesign(tracker.Item.Labels) {
		t.Fatalf("item = %#v, want it open and labelled %s", tracker.Item, domain.NeedsDesignLabel)
	}
	if tracker.Item.Parking.Parked() {
		t.Errorf("the item was parked %q; the label is what holds it", tracker.Item.Parking)
	}
	if !strings.Contains(tracker.ReopenReason, designQuestion) || !strings.Contains(tracker.ReopenReason, "architect's design") ||
		!strings.Contains(tracker.ReopenReason, outcome.Branch) {
		t.Errorf("reason = %q, want the question, who answers it, and where the change was kept", tracker.ReopenReason)
	}
	if !strings.Contains(tracker.Notes, "Landing claim: needs-design") {
		t.Errorf("notes = %q, want the outcome note to record the raise", tracker.Notes)
	}
	// The change is kept on its branch and worktree.
	if outcome.Branch == "" || outcome.WorktreePath == "" {
		t.Fatalf("branch %q, worktree %q, want the change kept", outcome.Branch, outcome.WorktreePath)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Errorf("the developer's change is not in the kept worktree: %v", err)
	}
	// Nothing is put to the development manager.
	if entries, err := docket.List(); err != nil || len(entries) != 0 {
		t.Errorf("docket = %#v, %v, want nothing docketed for a raise for design", entries, err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.RaisedForDesign() || state.Discharges() || state.Parks() || state.Escalated() {
		t.Errorf("record: raised = %t, discharges = %t, parks = %t, escalated = %t",
			state.RaisedForDesign(), state.Discharges(), state.Parks(), state.Escalated())
	}

	// The label the raise put on holds the item from the next pull.
	queue := backlog.Order([]beads.WorkItem{tracker.Item}, []string{tracker.Item.ID}, backlog.ReadHolds(nil), nil)
	if _, ok := queue.Next(); ok {
		t.Fatal("the queue offered an item a developer returned labelled needs-design")
	}
}

// A tracker that will not take the item back leaves it claimed by a run that has
// ended, which is a failure rather than something to record as a success.
func TestARaiseForDesignThatCannotReturnItsItemFails(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	tracker.ReopenErr = errors.New("the tracker refused the write")
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	provider.DeveloperFinalText = landingBlock(`{"outcome":"needs-design","why":"` + designQuestion + `"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || outcome.Status == runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want the run to report the item it could not return", outcome, err)
	}
	if !strings.Contains(err.Error(), "labelled needs-design") {
		t.Errorf("error = %v, want it to name what could not be done", err)
	}
}

// The claim is read strictly: a raise names no impediment, and its question is
// required.
func TestARaiseForDesignTakesAQuestionAndNoImpediment(t *testing.T) {
	t.Parallel()

	if _, err := landing.Decode(`{"outcome":"needs-design","why":"q","blocked_by":"yoyodyne-ifd.1"}`); err == nil {
		t.Error("a raise for design naming an impediment was accepted")
	}
	if _, err := landing.Decode(`{"outcome":"needs-design","why":""}`); err == nil {
		t.Error("a raise for design with no question was accepted")
	}
	claim, err := landing.Decode(`{"outcome":"needs-design","why":"` + designQuestion + `"}`)
	if err != nil || claim.Discharges() || claim.Parks() || claim.Escalates() || !claim.RaisesDesign() {
		t.Fatalf("claim = %#v, %v, want an undischarging, unparking raise", claim, err)
	}
	if described := claim.Describe(); !strings.Contains(described, "needs the architect's design") || !strings.Contains(described, designQuestion) {
		t.Errorf("Describe() = %q, want the raise and its question", described)
	}
	if !strings.Contains(landing.Contract, `{"outcome":"needs-design"`) {
		t.Error("the developer's contract does not show the raise")
	}
}
