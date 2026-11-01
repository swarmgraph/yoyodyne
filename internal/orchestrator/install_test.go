package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// npmTestCheck is the check an npm project's developer runs first: it passes
// only where the packages the install step puts in node_modules are there.
const npmTestCheck = "sh scripts/test-packages.sh"

// npmProjectRepository is the TypeScript fixture made into a project whose
// checks need packages a fresh checkout lacks: a package.json and lock file
// tracked by Git, node_modules ignored by it, and a check that fails without
// the package installed. files adds to or replaces what the project carries.
func npmProjectRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "npm-project")
	if err := os.CopyFS(repository, os.DirFS(nonGoFixture)); err != nil {
		t.Fatalf("CopyFS(%q) error = %v", nonGoFixture, err)
	}
	project := map[string]string{
		".gitignore":                   "node_modules/\n",
		"package.json":                 `{"name":"npm-project","version":"1.0.0","private":true,"dependencies":{"greeting":"file:vendor/greeting"}}` + "\n",
		"package-lock.json":            `{"name":"npm-project","lockfileVersion":3,"requires":true,"packages":{}}` + "\n",
		"vendor/greeting/package.json": `{"name":"greeting","version":"1.0.0","main":"index.js"}` + "\n",
		"vendor/greeting/index.js":     "module.exports = 'hello';\n",
		"scripts/test-packages.sh":     "#!/bin/sh\nset -eu\nif [ ! -f node_modules/greeting/package.json ]; then\n  echo 'node_modules/greeting is missing: the packages were not installed' >&2\n  exit 1\nfi\necho 'greeting is installed'\n",
		// What `npm ci` does with a lock file naming one package, for a machine
		// that may not have npm: it fetches into the cache it is told and puts
		// the package in node_modules.
		"scripts/npm-ci.sh": "#!/bin/sh\nset -eu\n: \"${YOYODYNE_INSTALL_CACHE:?the install cache was not named}\"\nmkdir -p \"$YOYODYNE_INSTALL_CACHE/_cacache\" node_modules/greeting\nprintf fetched > \"$YOYODYNE_INSTALL_CACHE/_cacache/greeting\"\ncp vendor/greeting/package.json vendor/greeting/index.js node_modules/greeting/\necho 'added 1 package in 1s'\n",
	}
	for name, content := range files {
		project[name] = content
	}
	for name, content := range project {
		target := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runPipelineGit(t, repository, "init", "-b", "main")
	runPipelineGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runPipelineGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	disablePipelineMaintenance(t, repository)
	t.Cleanup(func() { removeLinkedPipelineWorktrees(t, repository) })
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "initial")
	return repository
}

// npmProjectPipeline is the fixture's pipeline with the npm step declared, the
// developer's first check added, and a state directory to keep the cache in.
func npmProjectPipeline(t *testing.T, repository string, tracker *orchestratortest.Tracker, provider *orchestratortest.Backend, run string) (Pipeline, *runstate.Store) {
	t.Helper()
	pipeline, store := nonGoFixturePipeline(t, repository, tracker, provider)
	pipeline.StateRoot = t.TempDir()
	pipeline.Config.Checks = append(pipeline.Config.Checks, npmTestCheck)
	pipeline.Config.Install = []config.InstallStep{{
		Name:      "npm packages",
		Run:       run,
		Inputs:    []string{"package.json", "package-lock.json"},
		Outputs:   []string{"node_modules"},
		TimeLimit: config.Duration(5 * time.Minute),
	}}
	return pipeline, store
}

func npmProjectItem(id string) *orchestratortest.Tracker {
	return &orchestratortest.Tracker{Item: beads.WorkItem{
		ID:                 id,
		Title:              "Add a farewell greeting",
		Description:        "Follow docs/design.md",
		AcceptanceCriteria: "src/farewell.ts exports farewell()",
		Status:             "open",
	}}
}

// developerRunsItsFirstCheck is a developer whose first act is the project's
// check before any edit, as the rule on verifying before submitting asks; it
// keeps what that check said, then makes its change.
func developerRunsItsFirstCheck(firstCheck *string, firstCheckErr *error) func(backend.RunRequest) error {
	return func(request backend.RunRequest) error {
		if *firstCheck == "" && *firstCheckErr == nil {
			command := exec.Command("/bin/sh", "-c", npmTestCheck)
			command.Dir = request.WorkingDirectory
			output, err := command.CombinedOutput()
			*firstCheck, *firstCheckErr = string(output), err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "src", "farewell.ts"), []byte(nonGoSource("  ")), 0o600)
	}
}

// A project declaring an npm step has it run in the run's worktree after the
// worktree is made and before the developer's first turn: the developer's
// first check finds node_modules, the step alone is told the cache folder kept
// for the project under the state directory, the run records the step, and
// what the step installed reaches no commit.
func TestAnNpmProjectsDeveloperFindsItsPackagesInstalledOnItsFirstCheck(t *testing.T) {
	t.Parallel()

	repository := npmProjectRepository(t, nil)
	tracker := npmProjectItem("npm-project-1")
	var firstCheck string
	var firstCheckErr error
	provider := orchestratortest.RoleBackend(developerRunsItsFirstCheck(&firstCheck, &firstCheckErr), approveVerdict)
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, "sh scripts/npm-ci.sh")

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if firstCheckErr != nil || !strings.Contains(firstCheck, "greeting is installed") {
		t.Fatalf("the developer's first check = %q, %v; want it to find node_modules/greeting", firstCheck, firstCheckErr)
	}
	if outcome.Status != runstate.StatusSucceeded || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v", outcome)
	}

	developerRequests := provider.RequestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(developerRequests))
	}
	prompt := developerRequests[0].Prompt
	for _, want := range []string{"# This project's dependencies", "npm packages: installs node_modules from package.json, package-lock.json", "not installed within the same turn", "before the checks that judge your change and before your next repair round"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("developer prompt does not say %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "YOYODYNE_INSTALL_CACHE") {
		t.Fatalf("developer prompt names the install cache, which only the step is told:\n%s", prompt)
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Install) != 1 {
		t.Fatalf("install records = %#v, want the one step", state.Install)
	}
	record := state.Install[0]
	want, err := installDigest(repository, []string{"package.json", "package-lock.json"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "npm packages" || record.InputDigest != want || record.Result != runstate.InstallPassed || record.StartedAt.IsZero() || record.DurationMilliseconds < 0 {
		t.Fatalf("install record = %#v, want the step, digest %s, and a pass", record, want)
	}
	if record.Directory != state.WorktreePath {
		t.Fatalf("install record directory = %q, want the run's worktree %q", record.Directory, state.WorktreePath)
	}
	if !strings.Contains(record.Output, "added 1 package") {
		t.Fatalf("install record output = %q, want what the step printed", record.Output)
	}

	root, err := filepath.EvalSymlinks(pipeline.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, filepath.FromSlash(home.InstallCacheDirectoryWithin(root, string(pipeline.Config.Product.ID))))
	if content, err := os.ReadFile(filepath.Join(cache, "_cacache", "greeting")); err != nil || string(content) != "fetched" {
		t.Fatalf("install cache %s holds %q, %v; want what the step fetched into it", cache, content, err)
	}
	if info, err := os.Stat(cache); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("install cache %s = %v, %v; want a folder only the harness's user may open", cache, info, err)
	}
	worktree, err := filepath.EvalSymlinks(state.WorktreePath)
	if err == nil && strings.HasPrefix(cache+string(filepath.Separator), worktree+string(filepath.Separator)) {
		t.Fatalf("install cache %s is inside the run's worktree %s", cache, worktree)
	}

	if files := gitOutput(t, repository, "ls-files", "node_modules"); strings.TrimSpace(files) != "" {
		t.Fatalf("the integrated change carries %q; nothing the step installed may reach a commit", files)
	}
}

// The same project, installed by npm itself, offline, from a package the
// project carries: what the operator's own projects declare. It needs npm, so
// a machine without it skips it; the test above holds the harness's side of it
// either way.
func TestAnNpmCiStepInstallsBeforeTheDeveloperStarts(t *testing.T) {
	t.Parallel()

	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skipf("npm is not installed here: %v", err)
	}
	// The lock file npm itself writes for the project, made offline with a
	// cache of the test's own so nothing in the user's npm cache is touched.
	scratch := t.TempDir()
	project := filepath.Join(scratch, "lock")
	if err := os.MkdirAll(filepath.Join(project, "vendor", "greeting"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"package.json":                 `{"name":"npm-project","version":"1.0.0","private":true,"dependencies":{"greeting":"file:vendor/greeting"}}` + "\n",
		"vendor/greeting/package.json": `{"name":"greeting","version":"1.0.0","main":"index.js"}` + "\n",
		"vendor/greeting/index.js":     "module.exports = 'hello';\n",
	} {
		if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := exec.Command(npm, "install", "--package-lock-only", "--offline", "--no-audit", "--no-fund", "--cache", filepath.Join(scratch, "cache"))
	lock.Dir = project
	if output, err := lock.CombinedOutput(); err != nil {
		t.Skipf("npm could not write a lock file offline here, so npm ci cannot be asked: %v: %s", err, output)
	}
	lockFile, err := os.ReadFile(filepath.Join(project, "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}

	repository := npmProjectRepository(t, map[string]string{"package-lock.json": string(lockFile)})
	tracker := npmProjectItem("npm-project-2")
	var firstCheck string
	var firstCheckErr error
	provider := orchestratortest.RoleBackend(developerRunsItsFirstCheck(&firstCheck, &firstCheckErr), approveVerdict)
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, `npm ci --cache "$YOYODYNE_INSTALL_CACHE" --prefer-offline --no-audit --no-fund`)

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, loadErr := store.Load(outcome.RunID)
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if len(state.Install) != 1 || state.Install[0].Result != runstate.InstallPassed {
		t.Fatalf("install records = %#v, want npm ci to have passed", state.Install)
	}
	if firstCheckErr != nil || !strings.Contains(firstCheck, "greeting is installed") {
		t.Fatalf("the developer's first check = %q, %v; want it to find node_modules/greeting", firstCheck, firstCheckErr)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
}

// A step that fails on the dependency files the run started from, which no run
// on this machine has ever installed, is the step's or the machine's to fix: the
// run is held for the development manager before any developer turn, with what
// the step printed on the record and the item's blocker, the worktree kept, no
// try again, and no repair attempt spent.
func TestAnInstallStepThatNeverWorkedHereHoldsTheRunBeforeTheDeveloperStarts(t *testing.T) {
	t.Parallel()

	repository := npmProjectRepository(t, nil)
	tracker := npmProjectItem("npm-project-3")
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, "echo 'npm error code ENOTFOUND: registry.npmjs.org could not be reached' >&2; exit 1")
	pipeline.Sleep = func(context.Context, time.Duration) error {
		t.Error("the harness waited to try again a step that has never installed here")
		return nil
	}

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("developer invocations = %d, want none after the install step failed", got)
	}
	if outcome.Status == runstate.StatusSucceeded || outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v, want the run stopped", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Install) != 1 || state.Install[0].Result != runstate.InstallFailed || state.Install[0].ExitCode != 1 {
		t.Fatalf("install records = %#v, want the one failure and no try again", state.Install)
	}
	if !strings.Contains(state.Install[0].Output, "ENOTFOUND") {
		t.Fatalf("install output = %q, want what the step printed", state.Install[0].Output)
	}
	assertInstallHeld(t, state, tracker, runstate.CauseInstallNeverWorked, "never installed on this machine")
	if !strings.Contains(tracker.BlockReason, "before any developer turn") {
		t.Fatalf("blocker = %q, want it to say the run was held before any developer turn", tracker.BlockReason)
	}
}

// assertInstallHeld holds a run's record and its item to an install step's
// hold for the development manager: blocked naming the step and the case, the
// cause recorded as outside the work, the worktree kept, and no repair attempt
// or wait left on the record.
func assertInstallHeld(t *testing.T, state runstate.State, tracker *orchestratortest.Tracker, cause runstate.EnvironmentalCause, says string) {
	t.Helper()
	if state.StopClass != cause.StopClass() || state.Environmental == nil || state.Environmental.Cause != cause {
		t.Fatalf("stop class %q, environmental %#v; want %q", state.StopClass, state.Environmental, cause)
	}
	if state.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want none spent", state.RepairAttempts)
	}
	if state.InstallWait != nil {
		t.Fatalf("install wait = %#v, want none left on a held run", state.InstallWait)
	}
	if !strings.Contains(state.Failure, `"npm packages"`) || !strings.Contains(state.Failure, says) || !strings.Contains(state.Failure, "held for the development manager") {
		t.Fatalf("failure = %q, want it to name the step and say %q", state.Failure, says)
	}
	if !tracker.Blocked || !strings.Contains(tracker.BlockReason, "npm packages") || !strings.Contains(tracker.BlockReason, "ENOTFOUND") || !strings.Contains(tracker.BlockReason, "no repair attempt was spent") {
		t.Fatalf("blocked = %v, blocker = %q; want the item blocked naming the step and what it printed", tracker.Blocked, tracker.BlockReason)
	}
	if _, err := os.Stat(state.WorktreePath); err != nil {
		t.Fatalf("worktree %s: %v, want it kept", state.WorktreePath, err)
	}
}

// installedBeforeOnThisMachine records that the fixture's npm step installed
// the dependency files repository carries, as an earlier run on this machine
// would have left it.
func installedBeforeOnThisMachine(t *testing.T, pipeline Pipeline, repository string) {
	t.Helper()
	digest, err := installDigest(repository, []string{"package.json", "package-lock.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.recordInstalled("npm packages", digest); err != nil {
		t.Fatalf("recordInstalled() error = %v", err)
	}
}

// A step that fails on dependency files that installed on this machine before
// is the network's or the registry's: the harness waits five minutes and tries
// again, saying on the status line which step and when, and a try that passes
// lets the developer start as if nothing had happened, with no repair spent.
func TestAnInstallStepThatInstalledBeforeIsTriedAgainAfterFiveMinutes(t *testing.T) {
	t.Parallel()

	repository := npmProjectRepository(t, nil)
	tracker := npmProjectItem("npm-project-5")
	var firstCheck string
	var firstCheckErr error
	provider := orchestratortest.RoleBackend(developerRunsItsFirstCheck(&firstCheck, &firstCheckErr), approveVerdict)
	// The registry answers from the second try on.
	marker := filepath.Join(t.TempDir(), "tried")
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, "if [ ! -f '"+marker+"' ]; then touch '"+marker+"'; echo 'npm error code ETIMEDOUT' >&2; exit 1; fi; sh scripts/npm-ci.sh")
	installedBeforeOnThisMachine(t, pipeline, repository)
	clock := &movableClock{at: time.Now()}
	pipeline.Clock = clock
	var slept time.Duration
	var waiting []runstate.InstallWait
	var line string
	pipeline.Sleep = func(_ context.Context, duration time.Duration) error {
		if slept == 0 {
			running, err := store.Incomplete()
			if err != nil || len(running) != 1 || running[0].InstallWait == nil {
				t.Errorf("runs in flight while waiting = %#v, %v; want the one run with its wait recorded", running, err)
			} else {
				waiting = append(waiting, *running[0].InstallWait)
				line = readmodel.ReadStanding(context.Background(), readmodel.Sources{Runs: store, Now: clock.Now}).Running[0].Install
			}
		}
		slept += duration
		clock.advance(duration)
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("Run() outcome = %#v, want the run to carry on once the step installed", outcome)
	}
	if slept != runstate.InstallRetryInterval {
		t.Fatalf("waited %s before trying again, want %s", slept, runstate.InstallRetryInterval)
	}
	if len(waiting) != 1 || waiting[0].Name != "npm packages" || waiting[0].Retry != 1 {
		t.Fatalf("waits recorded = %#v, want the first try again of the npm step", waiting)
	}
	next := waiting[0].NextAt.Local().Format("15:04 MST")
	if !strings.Contains(line, `"npm packages"`) || !strings.Contains(line, "trying again at "+next) || !strings.Contains(line, "network or the package registry") {
		t.Fatalf("status line while waiting = %q, want it to name the step, the cause, and %s", line, next)
	}
	if firstCheckErr != nil || !strings.Contains(firstCheck, "greeting is installed") {
		t.Fatalf("the developer's first check = %q, %v; want it to find node_modules/greeting", firstCheck, firstCheckErr)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Install) != 2 || state.Install[0].Result != runstate.InstallFailed || state.Install[1].Result != runstate.InstallPassed {
		t.Fatalf("install records = %#v, want a failure and then a pass", state.Install)
	}
	if state.InstallWait != nil || state.RepairAttempts != 0 {
		t.Fatalf("install wait %#v, repair attempts %d; want the wait cleared and no repair spent", state.InstallWait, state.RepairAttempts)
	}
}

// Once every try again has failed too, the run is held for the development
// manager with its work kept, never ended on the first failure, and no repair
// attempt spent.
func TestAnInstallStepThatKeepsFailingOnFilesThatInstalledBeforeIsHeldAfterThreeTries(t *testing.T) {
	t.Parallel()

	repository := npmProjectRepository(t, nil)
	tracker := npmProjectItem("npm-project-6")
	provider := orchestratortest.RoleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := npmProjectPipeline(t, repository, tracker, provider, "echo 'npm error code ENOTFOUND: registry.npmjs.org could not be reached' >&2; exit 1")
	installedBeforeOnThisMachine(t, pipeline, repository)
	clock := &movableClock{at: time.Now()}
	pipeline.Clock = clock
	var slept time.Duration
	pipeline.Sleep = func(_ context.Context, duration time.Duration) error {
		slept += duration
		clock.advance(duration)
		return nil
	}

	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	if got := len(provider.RequestsForRole(domain.RoleDeveloper)); got != 0 {
		t.Fatalf("developer invocations = %d, want none while the step cannot install", got)
	}
	if want := runstate.InstallRetries * runstate.InstallRetryInterval; slept != want {
		t.Fatalf("waited %s in all, want %s: %d tries again, %s apart", slept, want, runstate.InstallRetries, runstate.InstallRetryInterval)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Install) != runstate.InstallRetries+1 {
		t.Fatalf("install records = %d, want the first try and %d more", len(state.Install), runstate.InstallRetries)
	}
	assertInstallHeld(t, state, tracker, runstate.CauseInstallUnavailable, "installed on this machine before")
	if !strings.Contains(tracker.BlockReason, "tried the step 4 times, 5m0s apart") {
		t.Fatalf("blocker = %q, want it to say how many times the step was tried", tracker.BlockReason)
	}
}

// What has installed on this machine is kept per step and per input digest,
// once each, among the product's records under the state directory, and a
// state directory with no record yet is a machine nothing has installed on.
func TestWhatHasInstalledOnThisMachineIsKeptPerStepAndDigest(t *testing.T) {
	t.Parallel()

	pipeline := Pipeline{StateRoot: t.TempDir()}
	pipeline.Config.Product.ID = "npm-project"
	digest := "sha256:" + strings.Repeat("a", 64)
	if known, err := pipeline.installedBefore("npm packages", digest); err != nil || known {
		t.Fatalf("installedBefore() on a fresh machine = %v, %v; want false", known, err)
	}
	for range 2 {
		if err := pipeline.recordInstalled("npm packages", digest); err != nil {
			t.Fatalf("recordInstalled() error = %v", err)
		}
	}
	if known, err := pipeline.installedBefore("npm packages", digest); err != nil || !known {
		t.Fatalf("installedBefore() after a pass = %v, %v; want true", known, err)
	}
	for _, other := range [][2]string{{"pip packages", digest}, {"npm packages", "sha256:" + strings.Repeat("b", 64)}} {
		if known, _ := pipeline.installedBefore(other[0], other[1]); known {
			t.Fatalf("installedBefore(%q, %s) = true, want only the pair that passed", other[0], other[1])
		}
	}
	root, err := filepath.EvalSymlinks(pipeline.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(home.InstalledFileWithin(root, "npm-project"))))
	if err != nil || strings.Count(string(content), "\n") != 1 {
		t.Fatalf("installed record = %q, %v; want the one pair once", content, err)
	}
}

// Which case a failure is, decided from the recorded digests: inputs the run
// changed are the change's, unchanged inputs that installed here before are
// outside the work, and unchanged inputs that never installed here are the
// step's or the machine's.
func TestAnInstallFailureIsSortedByWhoseItIs(t *testing.T) {
	t.Parallel()

	started, changed := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	worktree := "/worktrees/run"
	first := runstate.InstallRun{Name: "npm packages", Directory: worktree, InputDigest: started, Result: runstate.InstallPassed}
	afterTheChange := runstate.InstallRun{Name: "npm packages", Directory: worktree, InputDigest: changed, Result: runstate.InstallFailed}
	history := []runstate.InstallRun{first, afterTheChange}
	if got := startingInstallDigest(afterTheChange, history); got != started {
		t.Fatalf("starting digest = %s, want the first run's in that checkout %s", got, started)
	}
	if got := runstate.SortInstallFailure(afterTheChange, startingInstallDigest(afterTheChange, history), true); got != runstate.InstallCauseChange {
		t.Fatalf("a failure on inputs the run changed sorted as %q, want the change's", got)
	}

	again := runstate.InstallRun{Name: "npm packages", Directory: worktree, InputDigest: started, Result: runstate.InstallFailed}
	if got := runstate.SortInstallFailure(again, startingInstallDigest(again, history), true); got != runstate.InstallCauseOutside {
		t.Fatalf("a failure on unchanged inputs that installed before sorted as %q, want outside the work", got)
	}

	fresh := runstate.InstallRun{Name: "npm packages", Directory: "/worktrees/other", InputDigest: changed, Result: runstate.InstallFailed}
	if got := runstate.SortInstallFailure(fresh, startingInstallDigest(fresh, nil), false); got != runstate.InstallCauseNeverInstalled {
		t.Fatalf("a failure on unchanged inputs that never installed here sorted as %q, want never installed", got)
	}
}

// A project that declares no step is told nothing about one.
func TestNoInstallStepAddsNothingToTheDevelopersPrompt(t *testing.T) {
	t.Parallel()

	if got := withInstallSection("the prompt\n", nil); got != "the prompt\n" {
		t.Fatalf("withInstallSection(no steps) = %q, want the prompt unchanged", got)
	}
}

// The digest follows the inputs' content and nothing else, and an input a
// change removed is digested as missing rather than refused.
func TestTheInstallDigestFollowsTheInputsContent(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", "{}")
	write("package-lock.json", "{}")
	inputs := []string{"package.json", "package-lock.json"}
	first, err := installDigest(directory, inputs)
	if err != nil {
		t.Fatal(err)
	}
	write("unrelated.txt", "x")
	if again, _ := installDigest(directory, inputs); again != first {
		t.Fatalf("digest changed with a file that is not an input: %s then %s", first, again)
	}
	write("package-lock.json", `{"lockfileVersion":3}`)
	changed, err := installDigest(directory, inputs)
	if err != nil || changed == first {
		t.Fatalf("digest = %s, %v after the lock file changed, want a new one", changed, err)
	}
	if err := os.Remove(filepath.Join(directory, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	if missing, err := installDigest(directory, inputs); err != nil || missing == changed || missing == first {
		t.Fatalf("digest without the lock file = %s, %v, want a third digest", missing, err)
	}
}

// What a record keeps of a step's output is its last 200 lines, and of those
// the last 16 KiB.
func TestAnInstallRecordKeepsTheEndOfTheOutput(t *testing.T) {
	t.Parallel()

	var lines []string
	for index := range 500 {
		lines = append(lines, "line "+strings.Repeat("x", index%3)+string(rune('a'+index%26)))
	}
	tail := runstate.InstallOutputTail(lines)
	if got := strings.Count(tail, "\n") + 1; got != runstate.InstallOutputLines {
		t.Fatalf("kept %d lines, want %d", got, runstate.InstallOutputLines)
	}
	if !strings.HasSuffix(tail, lines[len(lines)-1]) {
		t.Fatalf("tail does not end on the last line")
	}
	long := []string{strings.Repeat("é", 20000)}
	if got := runstate.InstallOutputTail(long); len(got) > runstate.MaxInstallOutputBytes || !strings.HasPrefix(got, "é") {
		t.Fatalf("tail of a long line = %d bytes starting %q, want at most %d starting on a whole character", len(got), got[:2], runstate.MaxInstallOutputBytes)
	}
}
