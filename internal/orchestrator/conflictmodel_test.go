package orchestrator

// execution.conflict_resolution_model over a change the merge queue handed
// back for a conflict: the model the first attempt asks for, the attempts it
// never reaches, and the move to the stronger model after a resolution fails.

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// conflictModelRun is a run whose approved change the queue handed back for a
// one-file conflict, on an item of the priority given, with the setting
// naming kind ("" leaves it out). resolve is what the developer does to the
// conflict on each attempt after the first.
func conflictModelRun(t *testing.T, kind config.ModelKind, priority int, resolve func(string) error) (*queueRun, *orchestratortest.Backend, Outcome, runstate.State) {
	t.Helper()
	r, provider := newCatchUpRun(t, resolve)
	r.tracker.Item.Priority = priority
	r.pipeline.Config.Execution.ConflictResolutionModel = kind
	admitted := r.run(t)
	if admitted.MergeQueue == nil {
		t.Fatalf("Run() = %#v, want the approved change admitted to the queue", admitted)
	}
	conflictOnTarget(t, r.repository, "something else\n")
	if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
		t.Fatalf("Pass() = %#v, want the conflict handed back", pass)
	}
	put, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(put.ConflictHandbacks) != 1 || put.ConflictHandbacks[0].Lines == nil {
		t.Fatalf("hand-backs = %+v, want one with its conflicting lines counted", put.ConflictHandbacks)
	}
	return r, provider, admitted, put
}

// endedCopy is another, finished run that took part in the trial, for the
// trial's earlier attempts.
func endedCopy(state runstate.State) runstate.State {
	ended := state
	ended.RunID = "run-00000000000000000000000000000001"
	ended.Status = runstate.StatusFailed
	ended.CompletedAt = &ended.UpdatedAt
	return ended
}

func developerModels(provider *orchestratortest.Backend) []string {
	var models []string
	for _, request := range provider.RequestsForRole(domain.RoleDeveloper) {
		models = append(models, request.Model)
	}
	return models
}

func TestWithTheSettingLeftOutAConflictIsResolvedOnTheRunsOwnModel(t *testing.T) {
	t.Parallel()

	r, provider, admitted, _ := conflictModelRun(t, "", 2, resolveBothSides)
	if _, err := continueRun(t, r, admitted.RunID); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if models := developerModels(provider); len(models) != 2 || models[0] != "opus" || models[1] != "opus" {
		t.Fatalf("developer attempts asked for %v, want the run's own opus for both", models)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil || state.ConflictModel != nil {
		t.Fatalf("conflict model record = %+v, %v; want none with the setting left out", state.ConflictModel, err)
	}
}

func TestASmallFirstConflictIsTriedOnTheCheaperModelAsTheTrialsFirstAttempt(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := conflictModelRun(t, config.ModelKindCheaper, 2, resolveBothSides)
	rejoined, err := continueRun(t, r, admitted.RunID)
	if err != nil || rejoined.MergeQueue == nil {
		t.Fatalf("Continue() = %#v, %v; want the resolved change back in the queue", rejoined, err)
	}
	if models := developerModels(provider); len(models) != 2 || models[0] != "opus" || models[1] != "sonnet" {
		t.Fatalf("developer attempts asked for %v, want opus to write the change and the cheaper sonnet to resolve it", models)
	}
	// The resolution's first review is the run's own before it rejoins or,
	// where it rejoined with none, the queue's review of its candidate, which
	// the landing says approved it.
	if pass := r.catchUpPass(t, rejoined.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassLanded {
		t.Fatalf("Pass() = %#v, want the resolved change landed", pass)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	record := state.ConflictModel
	if record == nil || record.Arm != runstate.ConflictModelCheaper || record.Ordinal != 1 || record.Model != "sonnet" ||
		record.Handback != put.ConflictHandbacks[0].ID || record.FirstReview != "approve" || record.ChecksFailed {
		t.Fatalf("conflict model record = %+v, want the trial's first attempt on sonnet, approved at its first review", record)
	}
	if !strings.Contains(record.Reason, "attempt 1 of the 20 in the trial") || !strings.Contains(record.Reason, "1 file(s)") {
		t.Fatalf("reason = %q, want it to say the attempt's place and the conflict's size", record.Reason)
	}
	// Once the attempt is answered the run asks for its own model again.
	if state.DeveloperModel == "sonnet" {
		t.Fatalf("the run's own model became %q", state.DeveloperModel)
	}
}

func TestTheSecondTrialAttemptIsMadeOnTheStrongerModel(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := conflictModelRun(t, config.ModelKindCheaper, 2, resolveBothSides)
	earlier := endedCopy(put)
	earlier.ConflictModel = &runstate.ConflictModel{Handback: "earlier", Arm: runstate.ConflictModelCheaper, Ordinal: 1, Model: "sonnet", Reason: "eligible"}
	if err := r.runs.Create(earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := continueRun(t, r, admitted.RunID); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if models := developerModels(provider); len(models) != 2 || models[1] != "opus" {
		t.Fatalf("developer attempts asked for %v, want the second trial attempt on opus", models)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil || state.ConflictModel == nil || state.ConflictModel.Arm != runstate.ConflictModelStronger || state.ConflictModel.Ordinal != 2 {
		t.Fatalf("conflict model record = %+v, %v; want the stronger side as attempt 2", state.ConflictModel, err)
	}
}

func TestADefectOnACheaperResolutionStopsTheTrialForTheNextAttempt(t *testing.T) {
	t.Parallel()

	r, provider, admitted, put := conflictModelRun(t, config.ModelKindCheaper, 2, resolveBothSides)
	earlier := endedCopy(put)
	earlier.ConflictModel = &runstate.ConflictModel{Handback: "earlier", Arm: runstate.ConflictModelCheaper, Ordinal: 1, Model: "sonnet", Reason: "eligible",
		DefectAfterLanding: &runstate.LandedDefect{Note: "lost the target's side"}}
	if err := r.runs.Create(earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := continueRun(t, r, admitted.RunID); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if models := developerModels(provider); len(models) != 2 || models[1] != "opus" {
		t.Fatalf("developer attempts asked for %v, want the run's own opus once the trial stopped", models)
	}
	state, err := r.runs.Load(admitted.RunID)
	if err != nil || state.ConflictModel == nil || state.ConflictModel.Arm != runstate.ConflictModelStopped || state.ConflictModel.Model != "" ||
		!strings.Contains(state.ConflictModel.Reason, "lost the target's side") {
		t.Fatalf("conflict model record = %+v, %v; want the attempt recorded as after the stop", state.ConflictModel, err)
	}
}

func TestTheSettingNeverReachesAPriorityZeroItemOrARunThatMovedToTheStrongerModel(t *testing.T) {
	t.Parallel()

	t.Run("priority 0", func(t *testing.T) {
		t.Parallel()
		r, provider, admitted, _ := conflictModelRun(t, config.ModelKindCheaper, 0, resolveBothSides)
		if _, err := continueRun(t, r, admitted.RunID); err != nil {
			t.Fatalf("Continue() error = %v", err)
		}
		if models := developerModels(provider); models[len(models)-1] != "opus" {
			t.Fatalf("developer attempts asked for %v, want opus for a priority 0 item", models)
		}
		if state, _ := r.runs.Load(admitted.RunID); state.ConflictModel != nil {
			t.Fatalf("conflict model record = %+v on a priority 0 item", state.ConflictModel)
		}
	})
	t.Run("moved", func(t *testing.T) {
		t.Parallel()
		r, provider, admitted, put := conflictModelRun(t, config.ModelKindCheaper, 2, resolveBothSides)
		put.CheaperFirst = &runstate.CheaperFirst{Arm: runstate.CheaperFirstCheaper, Reason: "eligible", StartModel: "sonnet", StrongerModel: "opus",
			Move: &runstate.ModelMove{At: put.UpdatedAt, From: "sonnet", To: "opus", Reason: "its checks failed", RepairAttempt: 1}}
		put.DeveloperModel = "opus"
		if err := r.runs.Save(put); err != nil {
			t.Fatal(err)
		}
		if _, err := continueRun(t, r, admitted.RunID); err != nil {
			t.Fatalf("Continue() error = %v", err)
		}
		if models := developerModels(provider); models[len(models)-1] != "opus" {
			t.Fatalf("developer attempts asked for %v, want a run that moved kept on opus", models)
		}
		if state, _ := r.runs.Load(admitted.RunID); state.ConflictModel != nil {
			t.Fatalf("conflict model record = %+v on a run that had moved", state.ConflictModel)
		}
	})
}

func TestAResolutionOnTheCheaperModelThatFailsItsChecksMovesTheRunToTheStronger(t *testing.T) {
	t.Parallel()

	for _, cheaperFirst := range []bool{false, true} {
		resolutions := 0
		// The cheaper model's resolution leaves the markers in; the repair after
		// it resolves both sides.
		resolve := func(dir string) error {
			resolutions++
			if resolutions == 1 {
				return nil
			}
			return resolveBothSides(dir)
		}
		r, provider := newCatchUpRun(t, resolve)
		r.tracker.Item.Priority = 2
		r.pipeline.Config.Execution.ConflictResolutionModel = config.ModelKindCheaper
		r.pipeline.Config.Execution.DeveloperCheaperFirst = config.DeveloperCheaperFirst{Enabled: cheaperFirst}
		admitted := r.run(t)
		if admitted.MergeQueue == nil {
			t.Fatalf("cheaper first %v: Run() = %#v, want the change admitted", cheaperFirst, admitted)
		}
		conflictOnTarget(t, r.repository, "something else\n")
		if pass := r.catchUpPass(t, admitted.MergeQueue.Key()); pass.Outcome != runstate.MergeQueuePassHandedBack {
			t.Fatalf("cheaper first %v: Pass() = %#v, want the conflict handed back", cheaperFirst, pass)
		}
		if _, err := continueRun(t, r, admitted.RunID); err != nil {
			t.Fatalf("cheaper first %v: Continue() error = %v", cheaperFirst, err)
		}
		models := developerModels(provider)
		if len(models) != 3 || models[1] != "sonnet" || models[2] != "opus" {
			t.Fatalf("cheaper first %v: developer attempts asked for %v, want the resolution on sonnet and its repair on opus", cheaperFirst, models)
		}
		state, err := r.runs.Load(admitted.RunID)
		if err != nil || state.ConflictModel == nil || !state.ConflictModel.ChecksFailed || state.ConflictModel.FirstReview != "" {
			t.Fatalf("cheaper first %v: conflict model record = %+v, %v; want the failed checks noted and no review counted", cheaperFirst, state.ConflictModel, err)
		}
		if cheaperFirst {
			// The run started on sonnet, so the failure is its one move to opus.
			if move := state.CheaperFirst.Move; move == nil || move.From != "sonnet" || move.To != "opus" || !strings.Contains(move.Reason, "its checks failed") {
				t.Fatalf("move = %+v, want the run moved once from sonnet to opus for its failed checks", state.CheaperFirst.Move)
			}
		}
	}
}
