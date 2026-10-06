package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestRunInitWritesAProjectThatOwnsItsConfiguration(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	path := filepath.Join(project, config.DirectoryName, config.FileName)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Product.ID != "example-project" {
		t.Errorf("product id = %q, want the directory name", resolved.Config.Product.ID)
	}
	if resolved.Config.Extends != "" {
		t.Errorf("extends = %q, want a configuration that inherits nothing", resolved.Config.Extends)
	}
	if len(resolved.Sources) != 1 || resolved.Sources[0] != resolved.Path {
		t.Errorf("sources = %v, want only the project file", resolved.Sources)
	}
	// The personas are the project's own files, not a reference back into the
	// executable, so an operator can edit them where they were written.
	for name, agent := range resolved.Config.Agents {
		personaPath := filepath.Join(project, config.DirectoryName, filepath.FromSlash(agent.Persona.Path))
		if _, err := os.Stat(personaPath); err != nil {
			t.Errorf("agent %q persona was not copied into the project: %v", name, err)
		}
	}
	// A generated project cannot run work until its checks are named, and the
	// command says so rather than leaving it to be discovered by a refused run.
	if len(resolved.Config.Checks) != 0 {
		t.Errorf("checks = %v, want an empty list for the operator to fill in", resolved.Config.Checks)
	}
	if !strings.Contains(stdout.String(), "checks") {
		t.Errorf("stdout = %q, want it to name the checks that still have to be written", stdout.String())
	}
}

// A newcomer's first question in a directory of somebody else's documents is
// whose they are and whether they may touch one, and init is where that gets
// answered: every artifact home the configuration names gets an index saying
// what is filed there, who owns it, and the hand-edit policy.
func TestRunInitWritesAnIndexAtTheDoorOfEveryArtifactHome(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--product", "example"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	cfg, err := config.Load(filepath.Join(project, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	homes := artifacthome.Homes(cfg)
	if len(homes) == 0 {
		t.Fatal("a generated configuration names no artifact home")
	}
	// The invariants directory is named rather than left to the loop, because it
	// is the home a reader doubts: it carries its own identity scheme instead of
	// artifact frontmatter, and it still gets an index like every other home.
	invariants := false
	for _, home := range homes {
		invariants = invariants || home.Directory == cfg.Product.Invariants
	}
	if !invariants {
		t.Fatalf("homes = %#v, want the invariants home %q among the ones init writes an index for", homes, cfg.Product.Invariants)
	}
	for _, home := range homes {
		path := filepath.Join(project, filepath.FromSlash(home.Path()))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("init wrote no index for %s: %v", home.Directory, err)
		}
		for _, answer := range []string{"**Purpose.**", "**Owner.**", "**Editing by hand.**"} {
			if !strings.Contains(string(content), answer) {
				t.Errorf("%s does not state %s", home.Path(), answer)
			}
		}
		if !strings.Contains(stdout.String(), home.Path()) {
			t.Errorf("stdout = %q, want it to name %s among what it wrote", stdout.String(), home.Path())
		}
	}
}

// An index that is already there is the project's own prose, so init leaves it
// alone rather than refusing the whole initialization over it -- a repository
// that already has a word of its own at the door of `docs/` must still be
// initializable.
func TestRunInitLeavesAnIndexSomebodyAlreadyWroteAlone(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	existing := filepath.Join(project, "docs", "designs", "README.md")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	written := "# Designs\n\nOurs, written by hand.\n"
	if err := os.WriteFile(existing, []byte(written), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--product", "example", "--force"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	content, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != written {
		t.Errorf("init replaced an index somebody wrote:\n%s", content)
	}
	// The homes that had none still got one, so one file left alone does not
	// leave the rest of the repository undocumented.
	if _, err := os.Stat(filepath.Join(project, "docs", "product", "README.md")); err != nil {
		t.Errorf("init wrote no index for the specifications home: %v", err)
	}
}

// The three-step adoption path breaks if the operator has to hand-write a YAML
// list to get past step two, so init reads what the repository already
// announces and proposes checks from it. What it wrote and what it only found
// are reported separately, because a candidate is deliberately not written.
func TestRunInitProposesChecksFromTheProjectsOwnFiles(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.MkdirAll(filepath.Join(project, "tests"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "tests", "test_calc.py"), []byte("import unittest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Checks   []string `json:"checks"`
		Detected struct {
			Checks []struct {
				Command string `json:"command"`
				Source  string `json:"source"`
			} `json:"checks"`
			Candidates []struct {
				Command string `json:"command"`
				Reason  string `json:"reason"`
			} `json:"candidates"`
		} `json:"detected"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if want := []string{"go test ./...", "go vet ./..."}; !reflect.DeepEqual(result.Checks, want) {
		t.Errorf("checks = %v, want %v", result.Checks, want)
	}
	for _, detected := range result.Detected.Checks {
		if detected.Source != "go.mod" {
			t.Errorf("check %q source = %q, want the file it was derived from", detected.Command, detected.Source)
		}
	}
	if len(result.Detected.Candidates) == 0 {
		t.Error("the Python tests with no runner named were decided rather than offered")
	}

	// The written file is the thing an operator reads, so what it says is
	// asserted there rather than only in the report.
	path := filepath.Join(project, config.DirectoryName, config.FileName)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Checks) != 2 || loaded.Checks[0] != "go test ./..." {
		t.Errorf("checks = %v, want the proposed list", loaded.Checks)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{"# from go.mod", config.UndecidedMarker, "python3 -m pytest -q"} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("the generated configuration does not contain %q", want)
		}
	}
	// The Go commands are a usable gate on their own, so nothing here has to be
	// chosen before work can run and the file does not say otherwise.
	if strings.Contains(string(contents), config.CandidateMarker) {
		t.Error("a configuration that already runs demands a choice anyway")
	}
}

// The adoption walkthrough chooses a candidate by deleting one "#" and then asks
// `config show --effective` whether that took. Both halves are checked here as
// well, so the claim rests on the supplied checks rather than only on a script
// none of them runs.
func TestUncommentingACandidateMakesItTheEffectiveChecksList(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.MkdirAll(filepath.Join(project, "tests"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "tests", "test_calc.py"), []byte("import unittest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	path := filepath.Join(project, config.DirectoryName, config.FileName)
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	const chosen = "python3 -m unittest discover -q -s tests -t ."
	if !strings.Contains(string(generated), "#  - "+chosen+"\n") {
		t.Fatalf("no candidate was offered for %q:\n%s", chosen, generated)
	}
	// Exactly the gesture the file asks for: open the empty list, delete one
	// leading "#", change nothing else.
	edited := strings.Replace(string(generated), "checks: []\n", "checks:\n", 1)
	edited = strings.Replace(edited, "#  - "+chosen+"\n", "  - "+chosen+"\n", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "show", "--config", path, "--effective"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("config show --effective code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), chosen) {
		t.Errorf("config show --effective does not report the uncommented check:\n%s", stdout.String())
	}
}

// A repository that announces nothing keeps the placeholder it always had, and
// is told where the per-language examples are.
func TestRunInitKeepsThePlaceholderWhenNothingIsDetected(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	contents, err := os.ReadFile(filepath.Join(project, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "checks: []\n") {
		t.Error("a project with nothing detected did not keep its empty checks list")
	}
	if strings.Contains(string(contents), config.CandidateMarker) {
		t.Error("a project with nothing detected was told to choose between nothing")
	}
	for _, want := range []string{"#   # Go\n", "#   # Python\n", "docs/configuration.md#checks"} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("the generated configuration does not contain %q", want)
		}
	}
	if !strings.Contains(stdout.String(), "nothing in this project proposed one") {
		t.Errorf("stdout = %q, want it to say that nothing was proposed", stdout.String())
	}
}

// The tracker syncs over the project's own Git remote, and nothing else in the
// three-step adoption path configures it. An unconfigured tracker diverges per
// machine silently, so init closes that at install time rather than leaving it
// to be discovered at the first divergence.
func TestRunInitPointsTheTrackerAtTheProjectsGitRemote(t *testing.T) {
	t.Parallel()

	runner := &trackerRunner{
		gitRemoteURL: "git@github.com:acme/thing.git",
		beads: map[string]string{
			"dolt remote list": `[]`,
			"dolt remote add origin git@github.com:acme/thing.git": `Added remote "origin"`,
		},
		beadsAfterAdd: `[{"name":"origin","url":"git+ssh://git@github.com/acme/thing.git"}]`,
	}
	tracker := configureTrackerRemote(context.Background(), t.TempDir(), "", runner)
	if tracker.Status != trackerRemoteConfigured || tracker.URL != "git+ssh://git@github.com/acme/thing.git" {
		t.Fatalf("tracker = %#v, want the Git remote configured", tracker)
	}
	if !strings.Contains(describeTrackerRemote(tracker), "git+ssh://git@github.com/acme/thing.git") {
		t.Errorf("report = %q, want it to name what it configured", describeTrackerRemote(tracker))
	}
}

// A project that deliberately syncs its tracker somewhere else must not have
// that undone by a later init, and one that names a URL must have it applied.
func TestRunInitLeavesAConfiguredTrackerAloneUnlessOneIsNamed(t *testing.T) {
	t.Parallel()

	configured := `[{"name":"origin","url":"git+ssh://git@github.com/acme/tracker.git"}]`
	unchanged := &trackerRunner{
		gitRemoteURL: "git@github.com:acme/thing.git",
		beads:        map[string]string{"dolt remote list": configured},
	}
	tracker := configureTrackerRemote(context.Background(), t.TempDir(), "", unchanged)
	if tracker.Status != trackerRemoteUnchanged || tracker.URL != "git+ssh://git@github.com/acme/tracker.git" {
		t.Fatalf("tracker = %#v, want the tracker's own remote left alone", tracker)
	}
	if !strings.Contains(describeTrackerRemote(tracker), "--tracker-remote") {
		t.Errorf("report = %q, want it to name the flag that would change it", describeTrackerRemote(tracker))
	}

	// The case the flag exists for: a tracker that already syncs somewhere,
	// pointed at a repository of its own. What is already there must not make
	// the named URL a no-op, and bd replaces a remote it already holds rather
	// than refusing the name -- which is checked against bd itself in
	// TestSyncRemoteConformance, since a scripted runner can only restate it.
	named := &trackerRunner{
		gitRemoteURL: "git@github.com:acme/thing.git",
		beads: map[string]string{
			"dolt remote list": configured,
			"dolt remote add origin https://example.invalid/acme/tracker.git": "added",
		},
		beadsAfterAdd: `[{"name":"origin","url":"git+https://example.invalid/acme/tracker.git"}]`,
	}
	tracker = configureTrackerRemote(context.Background(), t.TempDir(), "https://example.invalid/acme/tracker.git", named)
	if tracker.Status != trackerRemoteConfigured || tracker.URL != "git+https://example.invalid/acme/tracker.git" {
		t.Fatalf("tracker = %#v, want the named URL configured over the one already there", tracker)
	}
	// A named URL is the operator's decision, so nothing about the project's own
	// Git remote is consulted before applying it.
	for _, args := range named.args {
		if args[0] == "git" {
			t.Errorf("a named tracker remote still asked Git where the project points: %v", args)
		}
	}
}

// bd is a separate tool with its own initialization, and everything init
// promises is already on disk by the time the tracker is configured. A tracker
// that could not be configured is reported with what to run, and does not turn
// a written configuration into a failed init.
func TestRunInitReportsATrackerItCouldNotConfigure(t *testing.T) {
	t.Parallel()

	refusing := &trackerRunner{gitRemoteURL: "git@github.com:acme/thing.git", beadsError: "no beads database in this directory"}
	tracker := configureTrackerRemote(context.Background(), t.TempDir(), "", refusing)
	if tracker.Status != trackerRemoteFailed || !strings.Contains(tracker.Reason, "no beads database") {
		t.Fatalf("tracker = %#v, want bd's own refusal reported", tracker)
	}
	if report := describeTrackerRemote(tracker); !strings.Contains(report, "bd dolt remote add origin") {
		t.Errorf("report = %q, want it to name what the operator would run", report)
	}

	// A project with nothing to sync to is not a failure, and the tracker is
	// left untouched rather than pointed at a URL nobody named.
	alone := &trackerRunner{}
	tracker = configureTrackerRemote(context.Background(), t.TempDir(), "", alone)
	if tracker.Status != trackerRemoteSkipped || !strings.Contains(tracker.Reason, "No such remote") {
		t.Fatalf("tracker = %#v, want the skip and Git's reason for it", tracker)
	}
	if len(alone.args) != 1 {
		t.Errorf("a project with no Git remote still ran %v", alone.args)
	}
}

// The whole command is exercised over a real repository with no remote, so the
// wiring from the flags through to the report is covered rather than only the
// step in isolation.
func TestRunInitReportsTheTrackerAlongsideWhatItWrote(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	git(t, project, "init", "-b", "main")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Tracker trackerRemote `json:"tracker"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Tracker.Status != trackerRemoteSkipped || result.Tracker.Reason == "" {
		t.Fatalf("tracker = %#v, want a skip that says why", result.Tracker)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--force"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "bd dolt remote add origin") {
		t.Errorf("stdout = %q, want the tracker reported beside what was written", stdout.String())
	}
}

// trackerRunner answers the two commands the tracker step runs: Git, for where
// the project points, and bd, for what the tracker is configured with.
type trackerRunner struct {
	gitRemoteURL string
	beads        map[string]string
	// beadsAfterAdd is what `dolt remote list` answers once a remote has been
	// added, which is how the read-back after a write is exercised.
	beadsAfterAdd string
	beadsError    string
	added         bool
	args          [][]string
}

func (r *trackerRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.args = append(r.args, append([]string{command.Name}, command.Args...))
	if command.Name == "git" {
		if r.gitRemoteURL == "" {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "error: No such remote 'origin'"}, nil
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.gitRemoteURL + "\n"}, nil
	}
	if r.beadsError != "" {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: r.beadsError}, nil
	}
	request := strings.Join(command.Args, " ")
	if strings.HasPrefix(request, "dolt remote add") {
		r.added = true
	}
	if r.added && request == "dolt remote list --json" {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.beadsAfterAdd}, nil
	}
	response, known := r.beads[strings.TrimSuffix(request, " --json")]
	if !known {
		return execution.ProcessResult{}, fmt.Errorf("unexpected bd command %v", command.Args)
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: response}, nil
}

func TestRunInitRefusesToOverwriteWithoutForce(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--product", "example"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	path := filepath.Join(project, config.DirectoryName, config.FileName)
	edited := "# edited by hand\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr = %q, want it to name the flag that would overwrite", stderr.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) != edited {
		t.Error("a refused init overwrote the existing configuration")
	}

	// The same refusal reported as JSON still exits nonzero, so a script does
	// not have to parse the payload to notice that nothing was written.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example", "--json"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	var failure struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if failure.Status != "failed" || !strings.Contains(failure.Error, "--force") {
		t.Fatalf("failure = %+v", failure)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example", "--force", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Status string   `json:"status"`
		Bundle string   `json:"bundle"`
		Config string   `json:"config"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Status != "written" || result.Config != path || result.Bundle != config.BuiltinV1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Files) < 2 {
		t.Fatalf("files = %v, want the configuration and its personas", result.Files)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestRunInitRefusesAProductItCannotName(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "Not An Id")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--product") {
		t.Errorf("stderr = %q, want it to name the flag that supplies an id", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, config.DirectoryName)); !os.IsNotExist(err) {
		t.Error("a refused init left a configuration directory behind")
	}
}

// The contributor who cannot commit tool config to somebody else's repository:
// everything lands on this machine, the repository is left exactly as it was,
// and `yoyo` run anywhere inside that repository finds the configuration with
// nothing typed after it.
func TestRunInitExternalWritesOutsideTheRepositoryAndIsStillFoundFromIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", home)
	project := externalProject(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--external"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	// Nothing was added to the repository at all -- neither the configuration
	// directory nor the indexes at the door of its artifact homes, which are the
	// untracked files a guest in somebody else's repository came here unable to
	// add.
	for _, untouched := range []string{config.DirectoryName, "docs"} {
		if _, err := os.Stat(filepath.Join(project, untouched)); !os.IsNotExist(err) {
			t.Errorf("an external init wrote %s into the repository", untouched)
		}
	}

	// It lands in the machine home's project directory for the product id, and
	// the project is bound to the repository, which is how it is found again.
	path := filepath.Join(home, "projects", "their-project", config.FileName)
	if _, err := os.Stat(filepath.Join(home, "projects", "their-project", "repository.json")); err != nil {
		t.Errorf("an external init bound nothing: %v", err)
	}
	if !strings.Contains(stdout.String(), path) {
		t.Errorf("stdout = %q, want it to name %q", stdout.String(), path)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The configuration is outside the repository it describes, so it names that
	// repository outright: there is no project above the file for a relative path
	// to resolve against. It is named as the checkout resolves rather than as it
	// was typed, which on a machine whose temporary directory is a symlink is not
	// the same string.
	checkout, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if loaded.Product.Repository != checkout {
		t.Errorf("product.repository = %q, want the checkout %q", loaded.Product.Repository, checkout)
	}
	for name, agent := range loaded.Agents {
		if strings.TrimSpace(agent.Persona.Text) == "" {
			t.Errorf("agent %q persona did not resolve beside the external configuration", name)
		}
	}

	nested := filepath.Join(project, "internal", "deeply", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, start := range []string{project, nested} {
		discovered, err := config.Discover(start)
		if err != nil {
			t.Fatalf("Discover(%q) error = %v", start, err)
		}
		if discovered != path {
			t.Errorf("Discover(%q) = %q, want %q", start, discovered, path)
		}
	}
}

// An external configuration is keyed by the repository it describes, so a
// directory in none is refused before anything is written: a configuration
// nothing could find again is worse than one that was never written.
func TestRunInitExternalRefusesADirectoryInNoRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", home)
	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--external"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--external") {
		t.Errorf("stderr = %q, want the refusal to say what --external needs", stderr.String())
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused external init left %d entries in the machine home", len(entries))
	}
}

// The flow both the README and the configuration guide print: `init --external`
// and then `yoyo doctor`, with nothing named on either. What is held here is
// what those documents promise about the second command -- that it diagnoses the
// configuration this machine keeps for the repository the operator is standing
// in, and that the artifact-home indexes an external init deliberately did not
// write are a warning about an installation that works rather than something
// stopping work.
//
// The verdict itself is not asserted: a generated configuration still owes a
// checks list, so this installation cannot run work yet for a reason that has
// nothing to do with where its configuration is kept.
func TestDoctorDiagnosesAnExternalConfigurationFoundFromTheRepository(t *testing.T) {
	// The one thing a diagnosis writes is the harness's own state root, pointed
	// somewhere disposable here; it is also the machine home the configuration
	// is kept in.
	home := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", home)
	project := externalProject(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--external"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("init code = %d, stderr = %q", code, stderr.String())
	}
	path := filepath.Join(home, "projects", "their-project", config.FileName)

	t.Chdir(project)
	stdout.Reset()
	stderr.Reset()
	// Exit 2 is the command failing to do what it was asked, and is the one code
	// that would mean no diagnosis was made at all.
	if code := Run([]string{"doctor", "--json"}, &stdout, &stderr, "test"); code == 2 {
		t.Fatalf("doctor code = 2, stderr = %q", stderr.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, output = %q", err, stdout.String())
	}
	if report.Config != path {
		t.Errorf("doctor diagnosed %q, want the configuration this machine keeps for this repository, %q", report.Config, path)
	}
	indexes, found := externalFinding(report, "artifact-readmes")
	if !found {
		t.Fatalf("doctor made no finding about the artifact homes: %#v", report.Findings)
	}
	if indexes.Status != doctor.StatusWarning {
		t.Errorf("artifact-readmes = %s, want a warning about an installation that works", indexes.Status)
	}
}

func externalFinding(report doctor.Report, check string) (doctor.Finding, bool) {
	for _, finding := range report.Findings {
		if finding.Check == check {
			return finding, true
		}
	}
	return doctor.Finding{}, false
}

// externalProject is a repository for an external configuration to be keyed by.
// It needs the marker Git leaves and nothing else: what discovery reads is the
// filesystem rather than a working Git.
func externalProject(t *testing.T) string {
	t.Helper()

	project := filepath.Join(t.TempDir(), "their-project")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	return project
}

// Inheritance is still a capability rather than the shipped shape: a project
// that extends the bundle keeps loading exactly as it did.
func TestExtendingTheBundleStillWorks(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Extends != config.BuiltinV1 {
		t.Fatalf("extends = %q, want %q", resolved.Config.Extends, config.BuiltinV1)
	}
	if len(resolved.Sources) != 2 || resolved.Sources[0] != config.BuiltinV1 {
		t.Fatalf("sources = %v, want the bundle then the project file", resolved.Sources)
	}
	if strings.TrimSpace(resolved.Config.Agents["reviewer"].Persona.Text) == "" {
		t.Error("an inherited persona no longer resolves")
	}
}
