package composition

// The gates themselves, run over this repository rather than over a fixture.
// This is the point of the package: the fixtures above prove each gate finds
// what it is for, and these are what make a defect in this project's own shell,
// YAML, JSON, or workflows fail a declared check instead of reaching a reviewer
// with nothing having read it.
//
// They run under `make test`, which is one of the four checks this project
// declares, so they run on every verify pass of every run — including each
// repair — and on the machine of whoever made the change, rather than as a CI
// step somebody reads a red X on the day after they broke it. That distinction
// is the whole of yoyodyne-ifd.165: a check a run does not apply is not a gate
// a run has.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory, and repositoryConfigPath the configuration whose checks list they
// are held against. The files are read where they live rather than from a copy,
// because a copy is exactly what cannot carry the defect.
const (
	repositoryRoot       = "../.."
	repositoryConfigPath = "../../.yoyodyne/config.yaml"
)

// installScriptSuitePath is the install script's own suite. It runs
// scripts/install.sh against a fabricated release and a fabricated machine —
// curl and uname are stubs on PATH, the releases are files in a temporary
// directory, HOME and the install directory are inside it — so it reaches no
// network, needs no published release, and writes nothing outside its root.
// The script is the first thing a newcomer runs and the one part of the install
// path that is not the binary, and until this ran from here the only thing
// that had executed it was a CI step after review.
const installScriptSuitePath = "../../scripts/install-test.sh"

// adoptionWalkthroughPath is the README's "Getting started" executed against a
// throwaway project. `make adoption` runs the whole of it; what is run from
// here is its refusal of a scratch root inside a git repository, which needs
// none of what the rest of the walk needs.
const adoptionWalkthroughPath = "../../scripts/walk-adoption.sh"

// TestThisRepositoryIsAllAccountedFor is the audit. Every file this repository
// carries belongs to a content class, every class recognizes something, and
// every check a class credits its coverage to is one the project actually
// declares.
func TestThisRepositoryIsAllAccountedFor(t *testing.T) {
	t.Parallel()

	files := repositoryFiles(t)
	declared, err := config.Load(repositoryConfigPath)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", repositoryConfigPath, err)
	}
	// A configuration with no checks is refused by the harness, so reaching here
	// with an empty list means the list was read from somewhere it is not.
	if len(declared.Checks) == 0 {
		t.Fatalf("%s declares no checks; the ledger is being held against nothing", repositoryConfigPath)
	}
	problems, err := Check(repositoryRoot, Classes, files, declared.Checks)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what somebody has to act
		// on is a file or a class, and a tally sends them looking for it.
		t.Errorf("this repository and its recorded composition disagree: %s", problem)
	}
}

func TestEveryShellFileInThisRepositoryParses(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	shell := repositoryMembers(t, ShellClass)
	// The scripts and the account tool are the shell this project has; a census
	// that found none of it would report nothing wrong with all of it. (The
	// status tool used to be the other one, until `yoyo status` absorbed it.)
	if !slices.Contains(shell, "scripts/cut-release.sh") || !slices.Contains(shell, "bin/yoyo-account") {
		t.Fatalf("the shell this repository carries was not found; the census collected %v", shell)
	}
	for _, problem := range ShellSyntax(repositoryRoot, shell) {
		t.Errorf("a shell file in this repository will not parse: %s", problem)
	}
}

func TestEveryStructuredFileInThisRepositoryDecodes(t *testing.T) {
	t.Parallel()

	structured := append(repositoryMembers(t, "yaml"), repositoryMembers(t, "json")...)
	if len(structured) == 0 {
		t.Fatal("no YAML or JSON was found in this repository; the census is looking in the wrong place")
	}
	for _, problem := range StructuredData(repositoryRoot, structured) {
		t.Errorf("a structured file in this repository does not decode: %s", problem)
	}
}

// TestEveryWorkflowInThisRepositoryIsShapedLikeOne covers the content class with
// the longest gap between writing it and running it. The release workflow is
// triggered by a tag push, so what is wrong with it is discovered during a
// publication — which is the one moment a botched workflow costs the most.
func TestEveryWorkflowInThisRepositoryIsShapedLikeOne(t *testing.T) {
	t.Parallel()

	workflows := Workflows(repositoryFiles(t))
	if len(workflows) == 0 {
		t.Fatalf("no workflow was found under %s; this repository has two", WorkflowDirectory)
	}
	for _, problem := range WorkflowShape(repositoryRoot, workflows) {
		t.Errorf("a workflow in this repository is not shaped like one: %s", problem)
	}
}

// TestEveryWorkflowInThisRepositoryPinsWhatItInstalls covers the other way a
// workflow goes red without this repository having changed. The adoption job
// installs the tracker, and while it did so at `@latest` a release somebody
// else cut failed every open pull request at a step none of them had touched.
// The version is pinned now; what this holds is that it stays a version rather
// than drifting back to whatever is newest, which a reviewer reading a diff of
// shell inside YAML does not reliably notice.
func TestEveryWorkflowInThisRepositoryPinsWhatItInstalls(t *testing.T) {
	t.Parallel()

	workflows := Workflows(repositoryFiles(t))
	if len(workflows) == 0 {
		t.Fatalf("no workflow was found under %s; this repository has two", WorkflowDirectory)
	}
	for _, problem := range WorkflowVersionPins(repositoryRoot, workflows) {
		t.Errorf("a workflow in this repository installs a tool at a version somebody else decides: %s", problem)
	}
}

// TestTheInstallScriptDoesWhatItSaysOnAMachineThatHasNothing runs the install
// script's suite, for the same reason the release verb's and the notes writer's suites are
// run from Go: the script is shell, what it claims is the README's first line, and a suite that runs only
// in CI is evidence that arrives after the change was reviewed. The suite's
// Makefile-coupling checks are the part worth having here in particular — a
// platform dropped from PLATFORMS or an archive renamed in `dist` would
// otherwise pass every Go check and break every real install.
func TestTheInstallScriptDoesWhatItSaysOnAMachineThatHasNothing(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	requireTool(t, "tar")
	path, err := filepath.Abs(installScriptSuitePath)
	if err != nil {
		t.Fatalf("Abs(%s) error = %v", installScriptSuitePath, err)
	}
	// The suite fabricates its machine under a temporary root of its own; it is
	// given a fresh one rather than the process's so that two suites running
	// beside each other cannot share a name. It is also run from that root
	// rather than from this package's directory: the suite is heredocs on
	// heredocs, and bash before 5.1 writes each one to a scratch file in /tmp
	// with no regard for TMPDIR, falling back to the current directory where
	// /tmp is not writable -- which is what an agent sandbox on macOS is. A
	// scratch file that lands here, however briefly, is one the census in
	// TestEveryStructuredFileInThisRepositoryDecodes can list and then fail to
	// open.
	root := t.TempDir()
	suite := exec.Command("bash", path)
	suite.Dir = root
	suite.Env = append(os.Environ(), "TMPDIR="+root)
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", installScriptSuitePath, err, report)
	}
	// The suite exits 0 on a machine that produced every case; a closing line
	// that says anything else would be a pass recorded against nothing.
	if !strings.Contains(string(report), "install.sh does what it says") {
		t.Fatalf("%s exited 0 without its closing line:\n%s", installScriptSuitePath, report)
	}
}

// TestTheAdoptionWalkthroughRefusesAScratchRootInsideARepository holds the one
// part of the walkthrough a declared check can reach. `make adoption` is not
// one of the four, and the walk itself needs a tracker and a scratch clone that
// a run's worktree is not given -- but the refusal happens before any of that,
// so it costs a temporary repository and nothing else. It is worth the line
// because the case it refuses is the one a run is in: a run's TMPDIR points
// inside the repository's .git, and before this refusal existed that read as a
// broken adoption path rather than as a misplaced temporary directory.
func TestTheAdoptionWalkthroughRefusesAScratchRootInsideARepository(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	requireTool(t, "git")
	root := t.TempDir()
	if made, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init in %s error = %v:\n%s", root, err, made)
	}
	// The refusal is immediate, so the walk is given a deadline rather than the
	// run of the machine: a refusal that stopped working would otherwise walk
	// the whole adoption path from inside `make test`, which is minutes, a
	// tracker, and a network this has no business needing.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	walkthrough, err := filepath.Abs(adoptionWalkthroughPath)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", adoptionWalkthroughPath, err)
	}
	walk := exec.CommandContext(ctx, "bash", walkthrough)
	walk.Env = append(os.Environ(), "TMPDIR="+root)
	// Run from a scratch directory rather than from this package's: bash 3.2
	// writes a here-document to a scratch file, and puts it in the working
	// directory when TMPDIR is not writable -- which the TMPDIR above, inside a
	// repository the walk refuses, may be by the time the shell needs one. Run
	// from here, that file was listed by the census in the tests beside this one
	// a moment before the shell unlinked it.
	walk.Dir = t.TempDir()
	report, err := walk.CombinedOutput()
	if err == nil {
		t.Fatalf("%s walked a scratch root inside a git repository:\n%s", adoptionWalkthroughPath, report)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("%s did not refuse the root (%v); its prerequisite refusals exit 2:\n%s", adoptionWalkthroughPath, err, report)
	}
	// The refusal has to say which of the two things it is, because a walk that
	// simply stopped is what this replaced.
	for _, said := range []string{"outside every git repository", "TMPDIR"} {
		if !strings.Contains(string(report), said) {
			t.Errorf("the refusal never says %q, so it does not name what to do:\n%s", said, report)
		}
	}
}

// repositoryFiles is the census of this checkout.
func repositoryFiles(t *testing.T) []string {
	t.Helper()

	requireTool(t, "git")
	files, err := Files(repositoryRoot)
	if err != nil {
		t.Fatalf("Files() error = %v", err)
	}
	// A census that found nothing would report a repository with nothing wrong
	// with it, which is the one way every gate here passes while checking none of
	// what it is for.
	if !slices.Contains(files, "Makefile") || !slices.Contains(files, "go.mod") {
		t.Fatalf("the census did not find this repository; it collected %d files", len(files))
	}
	return files
}

// repositoryMembers is the files of one content class in this checkout.
func repositoryMembers(t *testing.T, class string) []string {
	t.Helper()

	members, _, err := Classify(repositoryRoot, Classes, repositoryFiles(t))
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	return members[class]
}
