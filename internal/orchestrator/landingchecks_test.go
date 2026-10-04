package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A landing runs the whole suite once, over the commit that landed, after the
// run is over: the run has succeeded, its item has closed, and its worktree is
// gone before the landing checks start. A green landing is recorded on the run
// and said on the item, the checkout the checks ran in is gone afterwards, and
// the checks were told they run the whole module.
func TestALandingRunsTheLandingChecksOverTheIntegratedCommitOnce(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	told := filepath.Join(t.TempDir(), "told.txt")
	ranIn := filepath.Join(t.TempDir(), "ran-in.txt")
	pipeline.Config.LandingChecks = []string{
		"test -f feature.txt",
		`printf '%s' "$YOYODYNE_CHANGED_GO_PACKAGES" > ` + told + ` && pwd > ` + ranIn,
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.Closed || !outcome.WorktreeRemoved {
		t.Fatalf("outcome = %#v, closed = %t, want the run succeeded, its item closed and its worktree gone", outcome, tracker.Closed)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Finished() || !landed.Green || landed.Red() || landed.Problem != "" {
		t.Fatalf("landing = %#v, want a green landing with nothing wrong around it", landed)
	}
	if landed.Commit != outcome.Integration.TargetCommit || len(landed.Checks) != 2 {
		t.Fatalf("landing = %#v, want both checks recorded over the integrated commit", landed)
	}
	if read, _ := os.ReadFile(told); string(read) != "./..." {
		t.Fatalf("the landing check was told %q, want the whole module", read)
	}
	// The checks ran in a checkout of the commit rather than in the primary
	// checkout, and that checkout is gone once they have.
	directory, _ := os.ReadFile(ranIn)
	if where := strings.TrimSpace(string(directory)); !strings.Contains(where, "landing-") || strings.HasPrefix(where, repository) {
		t.Fatalf("the landing checks ran in %q, want a landing checkout of their own", where)
	}
	if _, err := os.Stat(strings.TrimSpace(string(directory))); !os.IsNotExist(err) {
		t.Fatalf("the landing checkout is still there: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks == nil || !state.LandingChecks.Green || state.Status != runstate.StatusSucceeded {
		t.Fatalf("state = %#v, want the green landing on a succeeded run", state.LandingChecks)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Landing checks: green landing: 2 landing checks passed over") {
		t.Fatalf("item notes do not say the landing was green:\n%s", notes)
	}
}

// A red landing is news about the target branch and not a verdict on the run:
// the run still succeeded and its item is still closed, and what the landing
// does is file its own item — with the failing check, the commit, the run that
// landed it, and the goal the landed item served — and say so on the run and on
// the item. It blocks nothing.
func TestARedLandingFilesItsOwnItemAndBlocksNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "open",
		Notes: "Admitted by the product manager.\n\nGoal served: [reliable-delivery] Run development nearly autonomously.",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &orchestratortest.RecordingFiler{}
	pipeline.Filer = filer
	// The output the check prints is not a substring of the command that prints
	// it, so where the output went is checkable apart from where the command is.
	pipeline.Config.LandingChecks = []string{"printf 'DATA %s in package x\\n' RACE; test -f missing.txt", "true"}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a red landing to fail nothing", err)
	}
	if outcome.Status != runstate.StatusSucceeded || !tracker.Closed || tracker.Blocked {
		t.Fatalf("outcome = %#v, closed = %t, blocked = %t, want the run succeeded and its item closed", outcome, tracker.Closed, tracker.Blocked)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Red() || landed.Green || len(landed.Checks) != 1 {
		t.Fatalf("landing = %#v, want a red landing stopped at its first check", landed)
	}
	if landed.FiledWorkItem != "yoyodyne-red-1" || landed.FilingProblem != "" {
		t.Fatalf("landing = %#v, want the filed item named", landed)
	}
	if len(filer.Filed) != 1 {
		t.Fatalf("filed = %d items, want one", len(filer.Filed))
	}
	filed := filer.Filed[0]
	commit := outcome.Integration.TargetCommit[:12]
	for _, want := range []string{"Red landing on main at " + commit, "exited 1", "after yoyodyne-task integrated"} {
		if !strings.Contains(filed.Title, want) {
			t.Fatalf("filed title = %q, want it to carry %q", filed.Title, want)
		}
	}
	for _, want := range []string{"yoyodyne-task (Task)", "Reproduce with", outcome.Integration.TargetCommit} {
		if !strings.Contains(filed.Description, want) {
			t.Fatalf("filed description = %q, want it to carry %q", filed.Description, want)
		}
	}
	// What the check printed is text a change can shape, and the title and
	// description are fields the protected-path gate reads grants from: the
	// output goes in the notes, which the gate never reads, and nowhere else.
	if strings.Contains(filed.Description, "DATA RACE") || strings.Contains(filed.Title, "DATA RACE") {
		t.Fatalf("the check's output reached a grant-bearing field: title %q, description %q", filed.Title, filed.Description)
	}
	for _, want := range []string{"Goal served: [reliable-delivery] Run development nearly autonomously.", outcome.RunID, "DATA RACE in package x", "Red-landing check: printf 'DATA %s in package x\\n' RACE; test -f missing.txt on main"} {
		if !strings.Contains(filed.Notes, want) {
			t.Fatalf("filed notes = %q, want them to carry %q", filed.Notes, want)
		}
	}
	if filed.Type != "bug" || filed.Priority == nil || *filed.Priority != 0 {
		t.Fatalf("filed as %s at %v, want a bug at the front of the queue", filed.Type, filed.Priority)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks == nil || !state.LandingChecks.Red() || state.LandingChecks.FiledWorkItem != "yoyodyne-red-1" {
		t.Fatalf("state = %#v, want the red landing and its item on the run", state.LandingChecks)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Landing checks: red landing: printf 'DATA %s in package x\\n' RACE; test -f missing.txt exited 1 over "+commit+"; filed as yoyodyne-red-1") {
		t.Fatalf("item notes do not say the landing was red and what it filed:\n%s", notes)
	}
}

// What a failing landing check prints is text the landed change can shape, and
// the filed item's notes are read line by line for its goal and for the marker a
// later landing finds it by. Output that prints either line is carried quoted,
// so the item still serves the goal the landed item served and the marker the
// harness wrote is the only one on it.
func TestARedLandingsOutputCannotNameTheFiledItemsGoalOrMarker(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{
		ID: "yoyodyne-task", Title: "Task", Status: "open",
		Notes: "Goal served: [reliable-delivery] Run development nearly autonomously.",
	}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &orchestratortest.RecordingFiler{}
	pipeline.Filer = filer
	pipeline.Config.LandingChecks = []string{"printf 'Goal served: [%s] Something else\\nRed-landing check: %s on main\\n' spoofed other; exit 1"}

	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(filer.Filed) != 1 {
		t.Fatalf("filed = %d items, want one", len(filer.Filed))
	}
	notes := filer.Filed[0].Notes
	if statement, named := goal.NamedIn(notes); !named || statement != "[reliable-delivery] Run development nearly autonomously." {
		t.Fatalf("the filed item's goal reads %q (named %t); the check's output named it:\n%s", statement, named, notes)
	}
	if !strings.Contains(notes, "> Goal served: [spoofed] Something else") {
		t.Fatalf("the check's output is not carried quoted:\n%s", notes)
	}
	for _, line := range strings.Split(notes, "\n") {
		if strings.TrimSpace(line) == "Red-landing check: other on main" {
			t.Fatalf("the check's output carried a marker a later landing would match:\n%s", notes)
		}
	}
}

// A target branch left red is one item, not one per landing. A second landing
// whose landing check fails the same way finds the item the first filed still
// open, notes the later commit on it, and files nothing — so the front of the
// queue holds one red-landing item the scheduler starts once, rather than one
// per landing started concurrently.
func TestASecondRedLandingOfTheSameCheckIsNotedOnTheOpenItemRatherThanFiledAgain(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &orchestratortest.RecordingFiler{Open: []beads.WorkItem{{
		ID: "yoyodyne-red-earlier", Status: "open",
		Notes: "Filed by the harness for the red landing of yoyodyne-other.\n" + redLandingMarker("main", "false"),
	}}}
	pipeline.Filer = filer
	pipeline.Config.LandingChecks = []string{"false"}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want a succeeded run", outcome, err)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Red() || landed.FiledWorkItem != "yoyodyne-red-earlier" || !landed.FiledEarlier || landed.FilingProblem != "" {
		t.Fatalf("landing = %#v, want the earlier item named and nothing filed", landed)
	}
	if len(filer.Filed) != 0 {
		t.Fatalf("filed = %#v, want nothing filed beside the open item", filer.Filed)
	}
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Red again at "+outcome.Integration.TargetCommit[:12]+" on main, after yoyodyne-task ("+outcome.RunID+") integrated: false exited 1.") {
		t.Fatalf("the open item was not told about the later landing:\n%s", notes)
	}
	if !strings.Contains(notes, "red again on yoyodyne-red-earlier, filed by an earlier landing") {
		t.Fatalf("the landed item's note does not name the earlier item:\n%s", notes)
	}
}

// A red landing nothing can file is still a red landing: recorded as red, with
// the record saying no item could be filed and why, on a run that succeeded.
func TestARedLandingNothingCanFileIsRecordedAsSuchAndFailsNothing(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	pipeline.Filer = &orchestratortest.RecordingFiler{Refuse: errors.New("bd is busy")}
	pipeline.Config.LandingChecks = []string{"false"}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want a succeeded run", outcome, err)
	}
	if landed := outcome.LandingChecks; landed == nil || !landed.Red() || landed.FiledWorkItem != "" || landed.FilingProblem != "bd is busy" {
		t.Fatalf("landing = %#v, want a red landing whose filing was refused", landed)
	}
	if notes := strings.Join(tracker.NoteRecords, "\n"); !strings.Contains(notes, "no item could be filed: bd is busy") {
		t.Fatalf("item notes do not say the filing was refused:\n%s", notes)
	}
}

// A project that configures no landing checks lands exactly as it did before
// they existed: nothing runs, nothing is recorded, and nothing is said.
func TestAProjectWithNoLandingChecksRecordsNoLanding(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.LandingChecks != nil {
		t.Fatalf("Run() = %#v, %v, want no landing recorded", outcome.LandingChecks, err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks != nil || strings.Contains(strings.Join(tracker.NoteRecords, "\n"), "Landing checks:") {
		t.Fatalf("state = %#v, notes = %v, want nothing said of a landing", state.LandingChecks, tracker.NoteRecords)
	}
}

// A landing runs under its own budget with no stage bound, because what is
// moved to the landing is the suite the gate's stage bound cannot hold: the
// runner's per-check and stage bounds are not what a landing check is given.
// And a landing check stopped at that budget judged nothing, so the landing is
// unverified rather than red — recorded and said, filing nothing — which is the
// rule the per-run gate already applies to a check it stopped on time.
func TestALandingRunsUnderItsOwnBudgetAndAStoppedCheckLeavesItUnverified(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"true"})
	pipeline.Landings = pipeline.Worktrees.(*gitworktree.Manager)
	filer := &orchestratortest.RecordingFiler{}
	pipeline.Filer = filer
	// The gate's bounds are far past what the landing check takes, so a landing
	// run under them would see `sleep 30` finish and pass; only the landing's own
	// budget can stop it. They are also past anything load can make of the gate's
	// own `true`: at 50ms, and then at ten seconds with the elapsed time read
	// against it, the gate and the kill were each within reach of a machine
	// loaded by the race suite, with the code working.
	runner := pipeline.Checks.(checks.Runner)
	runner.Timeout = 10 * time.Minute
	runner.StageTimeout = 10 * time.Minute
	pipeline.Checks = runner
	// One second of the code's own timer, which is the thing under test here;
	// the budget is recorded in whole seconds, so it is not made shorter.
	pipeline.Config.Execution.LandingCheckTimeout = config.Duration(time.Second)
	pipeline.Config.LandingChecks = []string{"sleep 30", "true"}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() = %#v, %v, want a succeeded run", outcome, err)
	}
	landed := outcome.LandingChecks
	if landed == nil || !landed.Finished() || !landed.Unverified() || landed.Red() || landed.Green {
		t.Fatalf("landing = %#v, want an unverified landing", landed)
	}
	if landed.Bound() != time.Second {
		t.Fatalf("landing bound = %s, want the landing's own budget recorded", landed.Bound())
	}
	// Stopped at a bound, with the gate's ten minutes past the thirty seconds
	// `sleep 30` ends on its own, is stopped by the landing's own budget.
	if len(landed.Checks) != 1 || !landed.Checks[0].StoppedAtBound || landed.Checks[0].Passed {
		t.Fatalf("landing checks = %#v, want the first stopped at its budget and the second never run", landed.Checks)
	}
	if !strings.Contains(landed.Problem, "sleep 30 was stopped at its") || !strings.Contains(landed.Problem, "execution.landing_check_timeout budget") {
		t.Fatalf("landing problem = %q, want the stopped check and the budget named", landed.Problem)
	}
	if len(filer.Filed) != 0 || landed.FiledWorkItem != "" {
		t.Fatalf("filed = %#v, want nothing filed for a landing that judged nothing", filer.Filed)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LandingChecks == nil || !state.LandingChecks.Unverified() || state.Outstanding() {
		t.Fatalf("state = %#v, want an unverified landing on a run that owes nothing", state.LandingChecks)
	}
	if notes := strings.Join(tracker.NoteRecords, "\n"); !strings.Contains(notes, "Landing checks: unverified landing: the landing checks did not run to the end over") {
		t.Fatalf("item notes do not say the landing went unverified:\n%s", notes)
	}
}

// A process that dies inside its landing checks leaves a run that is over with
// a landing the record says is running, and a checkout under the worktree
// root. The sweep settles the landing as unverified, removes the checkout, and
// leaves the run as it recorded itself; a live process running the checks holds
// the run's lease and is left alone.
func TestTheSweepSettlesALandingWhoseProcessDiedAsUnverified(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"true"}), provider)
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// What a dead process leaves: the landing recorded as started and never
	// ended, and the checkout it was running in still registered.
	manager := newObserver(t, repository, worktreeRoot).(*gitworktree.Manager)
	checkout, err := manager.CheckoutCommit(context.Background(), outcome.RunID, outcome.Integration.TargetCommit)
	if err != nil {
		t.Fatalf("CheckoutCommit() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.LandingChecks = &runstate.LandingChecks{Commit: outcome.Integration.TargetCommit, StartedAt: state.UpdatedAt, BoundSeconds: 7200}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !state.Outstanding() {
		t.Fatal("a run with its landing checks running owes nothing, so no sweep would ever settle it")
	}

	results, err := Reconciler{Tracker: tracker, Worktrees: manager, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionCompleted || !strings.Contains(results[0].Detail, "unverified") {
		t.Fatalf("reconciliation = %#v, want the landing settled as unverified", results)
	}
	settled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusSucceeded || settled.Outstanding() || !tracker.Closed {
		t.Fatalf("settled = %#v, closed = %t, want the run left as it recorded itself and owing nothing", settled, tracker.Closed)
	}
	if settled.LandingChecks == nil || !settled.LandingChecks.Unverified() || !strings.Contains(settled.LandingChecks.Problem, "died before they ended") {
		t.Fatalf("settled landing = %#v, want it unverified with the death named", settled.LandingChecks)
	}
	if _, err := os.Lstat(checkout); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%s) = %v, want the landing checkout removed", checkout, err)
	}
	sweep := Reconciler{Tracker: tracker, Worktrees: manager, Store: store}
	if again, err := sweep.Reconcile(context.Background()); err != nil || len(again) != 0 {
		t.Fatalf("second Reconcile() = %#v, %v, want nothing left to settle", again, err)
	}
}

// A landing in progress is held by the process running it — the run's lease is
// still that process's — so a sweep that arrives while the checks run leaves
// the landing and its checkout exactly as they are, rather than settling a
// landing as unverified and removing the checkout from under a running suite.
func TestTheSweepLeavesALandingALiveProcessIsRunningAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"true"}), provider)
	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The shape of a landing mid-checks: the checkout cut, the landing recorded
	// as started, and the run's lease held by the process running them.
	manager := newObserver(t, repository, worktreeRoot).(*gitworktree.Manager)
	checkout, err := manager.CheckoutCommit(context.Background(), outcome.RunID, outcome.Integration.TargetCommit)
	if err != nil {
		t.Fatalf("CheckoutCommit() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.LandingChecks = &runstate.LandingChecks{Commit: outcome.Integration.TargetCommit, StartedAt: state.UpdatedAt, BoundSeconds: 7200}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	_, lease, err := store.AdoptRun(context.Background(), outcome.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()

	results, err := (Reconciler{Tracker: tracker, Worktrees: manager, Store: store}).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionHeld {
		t.Fatalf("reconciliation = %#v, want the held run left alone", results)
	}
	held, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.LandingChecks == nil || held.LandingChecks.Finished() {
		t.Fatalf("landing = %#v, want it left running", held.LandingChecks)
	}
	if _, err := os.Lstat(checkout); err != nil {
		t.Fatalf("Lstat(%s) = %v, want the running landing's checkout untouched", checkout, err)
	}
	if err := manager.RemoveCheckout(context.Background(), checkout); err != nil {
		t.Fatalf("RemoveCheckout() error = %v", err)
	}
}

// A run killed inside its checks leaves a record saying the stage is running.
// The sweep that settles the run closes the stage as interrupted, naming the
// check it was on, so no surface goes on saying the checks are running — with a
// spend that grows for as long as the record stands — under a run that ended.
func TestTheSweepClosesACheckStageWhoseProcessDiedAsInterrupted(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// What a process killed inside `make race` leaves on disk: the stage
	// recorded as started and on that check, and nothing after it.
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.Phase = runstate.PhaseChecking
	state.CheckStage = &runstate.CheckStage{StartedAt: state.UpdatedAt, BoundSeconds: 1800, Command: "make race"}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action == ActionUnsettled {
		t.Fatalf("reconciliation = %#v, want the run settled", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Status.Terminal() || settled.CheckStage == nil || settled.CheckStage.Running() || !settled.CheckStage.Interrupted {
		t.Fatalf("settled = %#v, stage = %#v, want the stage closed as interrupted", settled.Status, settled.CheckStage)
	}
	if said := settled.CheckStage.Describe(time.Now()); !strings.Contains(said, "interrupted during make race") {
		t.Fatalf("stage says %q, want the interruption and the check named", said)
	}
}
