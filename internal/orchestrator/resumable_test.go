package orchestrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// shapeName is a resume shape's function name, which is how the tests below
// key each shape.
func shapeName(shape func(runstate.State) bool) string {
	full := runtime.FuncForPC(reflect.ValueOf(shape).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

// preservedRun is a running run with the worktree, branch, base, target and
// developer session that most shapes need, and nothing on its record saying it
// paused or stopped.
func preservedRun(phase runstate.Phase) runstate.State {
	return runstate.State{
		RunID:             "run-1",
		WorkItemID:        "item-1",
		Status:            runstate.StatusRunning,
		Phase:             phase,
		WorktreePath:      "/worktrees/item-1",
		Branch:            "yoyodyne/item-1",
		BaseCommit:        "abc123",
		TargetBranch:      "main",
		ProviderSessionID: "session-1",
	}
}

// resumableRuns is one run of each shape Pipeline.Run takes up, keyed by the
// predicate that recognizes it.
func resumableRuns() map[string]runstate.State {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	runs := map[string]runstate.State{}

	usage := preservedRun(runstate.PhaseDeveloping)
	usage.ProviderSessionID = ""
	usage.UsageLimitResetsAt = &at
	runs["pausedForUsageLimit"] = usage

	directive := preservedRun(runstate.PhaseDeveloping)
	directive.DirectivePause = &runstate.DirectivePause{}
	runs["pausedForDirective"] = directive

	dependency := preservedRun(runstate.PhaseDeveloping)
	dependency.DependencyPause = &runstate.DependencyPause{}
	runs["pausedForDependency"] = dependency

	tracker := preservedRun(runstate.PhaseChecking)
	tracker.TrackerPause = &runstate.TrackerPause{Boundary: runstate.RetryDependencyRead}
	runs["pausedForTracker"] = tracker

	held := preservedRun(runstate.PhaseReviewing)
	held.OperatorHeldSince = &at
	runs["pausedForOperatorHold"] = held

	provider := preservedRun(runstate.PhaseReviewing)
	provider.ProviderStop = "idle"
	runs["stoppedProviderIsResumable"] = provider

	redeploy := preservedRun(runstate.PhaseChecking)
	redeploy.RedeployStop = &runstate.RedeployStop{Phase: runstate.PhaseChecking, At: at}
	runs["stoppedForRedeployIsResumable"] = redeploy

	checks := preservedRun(runstate.PhaseChecking)
	checks.CheckStageContinuations = []runstate.CheckStageContinuation{{}}
	runs["continuedAtCheckStage"] = checks

	repair := preservedRun(runstate.PhaseReviewing)
	repair.RepairAttempts = 1
	runs["resumableRepair"] = repair

	recovery := preservedRun(runstate.PhaseDeveloping)
	recovery.RecoveryContinuation = &runstate.RecoveryContinuation{RequestID: "request-1", Action: runstate.RecoverContinue, Reason: "continue it", RequestedAt: at}
	runs["recoveryContinuationDue"] = recovery

	lostHost := preservedRun(runstate.PhaseReviewing)
	lostHost.IntegrationResumptions = []runstate.IntegrationResumption{{Cause: runstate.CauseQueuedHeadBehind, ResumedAt: at}}
	runs["updateLostItsHost"] = lostHost

	return runs
}

// Every shape resumable considers takes up a run of that shape, and a test run
// exists for every shape, so a shape added without one fails here.
func TestResumableTakesUpEveryShape(t *testing.T) {
	t.Parallel()
	runs := resumableRuns()
	listed := map[string]bool{}
	for _, shape := range resumeShapes {
		name := shapeName(shape)
		listed[name] = true
		run, ok := runs[name]
		if !ok {
			t.Errorf("%s is a resume shape with no run in resumableRuns that exercises it", name)
			continue
		}
		if !shape(run) {
			t.Errorf("%s does not recognize its own test run %#v", name, run)
		}
		if !resumable(run) {
			t.Errorf("resumable() = false for a run %s recognizes", name)
		}
	}
	for name := range runs {
		if !listed[name] {
			t.Errorf("resumableRuns has a run for %s, which is not one of resumeShapes", name)
		}
	}
}

// What none of the shapes recognizes is not resumed.
func TestResumableRefusesWhatNoShapeRecognizes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

	writePark := preservedRun(runstate.PhaseDeveloping)
	writePark.TrackerPause = &runstate.TrackerPause{Boundary: runstate.RetryTrackerWrite}

	noWorktree := preservedRun(runstate.PhaseDeveloping)
	noWorktree.WorktreePath = ""
	noWorktree.DirectivePause = &runstate.DirectivePause{}

	ended := preservedRun(runstate.PhaseDeveloping)
	ended.Status = runstate.StatusFailed
	ended.UsageLimitResetsAt = &at

	refusedRecovery := preservedRun(runstate.PhaseDeveloping)
	refusedRecovery.RecoveryContinuation = &runstate.RecoveryContinuation{Action: runstate.RecoverContinue, Refused: "no"}

	for name, run := range map[string]runstate.State{
		"a run with nothing on its record":             preservedRun(runstate.PhaseDeveloping),
		"a run parked on a tracker write":              writePark,
		"a paused run with no worktree":                noWorktree,
		"an ended run carrying a usage-limit pause":    ended,
		"a run whose continuation request was refused": refusedRecovery,
	} {
		if resumable(run) {
			t.Errorf("resumable() = true for %s: %#v", name, run)
		}
	}
}

// pauseMarkerField matches the names runstate.State gives the fields a pause
// or a stop is recorded in.
var pauseMarkerField = regexp.MustCompile(`(Pause|Stop|HeldSince|Hold|ResetsAt|Continuation|Continuations|Resumptions)$`)

// pauseMarkersTakenUp names, for every pause marker on runstate.State, the
// resume shape that takes up a run carrying it.
var pauseMarkersTakenUp = map[string]string{
	"UsageLimitResetsAt":      "pausedForUsageLimit",
	"DirectivePause":          "pausedForDirective",
	"DependencyPause":         "pausedForDependency",
	"TrackerPause":            "pausedForTracker",
	"OperatorHeldSince":       "pausedForOperatorHold",
	"ProviderStop":            "stoppedProviderIsResumable",
	"RedeployStop":            "stoppedForRedeployIsResumable",
	"CheckStageContinuations": "continuedAtCheckStage",
	"RecoveryContinuation":    "recoveryContinuationDue",
	"IntegrationResumptions":  "updateLostItsHost",
}

// pauseMarkersNotTakenUp are the fields matching pauseMarkerField that
// Pipeline.Run deliberately does not resume from, with why: either a run
// carrying one is continued somewhere else, or the field is a history of what
// already happened rather than a pause of its own.
var pauseMarkersNotTakenUp = map[string]string{
	"IntegrationStop":     "an integration stop is resumed by the integration resumer (integrationresume.go), not by a new dispatch",
	"RecoveryHold":        "a hold the development manager left standing waits on that decision; what it orders is a continuation (recoveryContinuationDue)",
	"Readopted":           "not a marker but the redeploy stop a resumed run cleared, kept for reading later stalls",
	"RepairContinuations": "the history of repair grants; a run re-entered in its repair loop is resumed by resumableRepair",
	"SweepContinuations":  "the history of the sweep continuing a usage-limit wait; the wait itself is UsageLimitResetsAt",
}

// A pause kind is recorded as a field on the run's state and recognized by a
// predicate in this package. Every one of either must be considered by
// resumable, or be named as deliberately continued somewhere else, so a new
// pause kind cannot be added without deciding whether a dispatch resumes it.
func TestEveryPauseMarkerIsConsideredByResumable(t *testing.T) {
	t.Parallel()
	listed := map[string]bool{}
	for _, shape := range resumeShapes {
		listed[shapeName(shape)] = true
	}

	stateType := reflect.TypeOf(runstate.State{})
	var fields []string
	for i := 0; i < stateType.NumField(); i++ {
		if name := stateType.Field(i).Name; pauseMarkerField.MatchString(name) {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	for _, field := range fields {
		if _, ok := pauseMarkersNotTakenUp[field]; ok {
			continue
		}
		shape, ok := pauseMarkersTakenUp[field]
		switch {
		case !ok:
			t.Errorf("runstate.State.%s looks like a pause marker that resumable does not consider: add the predicate that resumes it to resumeShapes and map it in pauseMarkersTakenUp, or say in pauseMarkersNotTakenUp what continues it instead", field)
		case !listed[shape]:
			t.Errorf("runstate.State.%s is mapped to %s, which is not one of resumeShapes", field, shape)
		}
	}
	for field := range pauseMarkersTakenUp {
		if _, ok := stateType.FieldByName(field); !ok {
			t.Errorf("pauseMarkersTakenUp names runstate.State.%s, which does not exist", field)
		}
	}
	for field := range pauseMarkersNotTakenUp {
		if _, ok := stateType.FieldByName(field); !ok {
			t.Errorf("pauseMarkersNotTakenUp names runstate.State.%s, which does not exist", field)
		}
	}

	// Every pausedFor… predicate declared in this package's own code is one
	// of resumeShapes.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(files, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "pausedFor") {
				continue
			}
			if !listed[function.Name.Name] {
				t.Errorf("%s declares %s, a pause kind resumable does not consider: add it to resumeShapes in resumable.go", name, function.Name.Name)
			}
		}
	}
}
