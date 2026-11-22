package cli

// The tests here hold the release path — scripts/cut-release.sh, and the notes
// it is gated on — to three things the Go checks could not otherwise see.
//
// The verb is shell, so until this file nothing in `make check` executed it: its
// own suite ran only as a separate CI step, and a separate step is something
// somebody reads a red X on a day later than the one they broke it. The first
// two tests run those suites from here, so every line of the release path is
// exercised by the same `make test` as everything else, on the machine of
// whoever changed it.
//
// The third holds the verb's list of the tracker's derived exports to the list
// a run declares, in the one direction the two actually imply. They answer
// different questions — what a release cut may excuse from its clean-tree check
// and leave uncommitted under the tag, and what a run may tolerate the primary
// checkout acquiring — and it is the first that has to sit inside the second.
//
// The fourth holds the release workflow to publishing the composed notes as
// the whole of the release page's body, built at the tagged commit. The
// workflow is YAML on a tag trigger, so the composition check can say it is
// shaped like a workflow and nothing more; what its publish step hands the
// forge is a claim only a test that reads the step can hold.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// cutReleaseTestPath is the verb's own suite. It fabricates scratch
// repositories with a stub walkthrough and a stub Makefile and executes every
// refusal against them, so it needs no network, no provider, and no
// cross-compile — which is what makes it cheap enough to run from here.
const cutReleaseTestPath = "../../scripts/cut-release-test.sh"

// releaseNotesTestPath is the notes writer's own suite, and the release page
// body composer's with it. It fabricates a scratch repository and a tracker
// export, so this repository's real history and real export are never read —
// which is what makes its assertions about a range stable enough to run here.
const releaseNotesTestPath = "../../scripts/release-notes-test.sh"

// cutReleaseScriptPath is the verb itself.
const cutReleaseScriptPath = "../../scripts/cut-release.sh"

// runSourcePath is where a run declares what the primary checkout may acquire
// while it works, which is the wider of the two lists.
const runSourcePath = "run.go"

// releaseWorkflowPath is the workflow a tag push fires, which builds the
// archives and publishes the release page.
const releaseWorkflowPath = "../../.github/workflows/release.yml"

var derivedExportsPattern = regexp.MustCompile(`(?m)^derived_exports=\(([^)]*)\)`)

var allowedPrimaryChangesPattern = regexp.MustCompile(`AllowedPrimaryChanges:\s*\[\]string\{([^}]*)\}`)

var quotedPathPattern = regexp.MustCompile(`"([^"]*)"`)

// TestTheReleaseVerbRefusesWhatItShould runs the verb's suite. The refusals are
// the whole value of a release gate, and a refusal nobody exercises is a
// refusal nobody has — least of all under a delegated daily cadence, where the
// first person to find out is whoever the cut did not happen for.
func TestTheReleaseVerbRefusesWhatItShould(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", cutReleaseTestPath, tool)
		}
	}
	suite := shellSuite(t, cutReleaseTestPath)
	// The suite runs make against the repositories it fabricates. Reached
	// through `make test`, those invocations would otherwise inherit this
	// make's jobserver, which is not theirs to join.
	suite.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", cutReleaseTestPath, err, report)
	}
}

// TestAReleasesNotesSayWhatLanded runs the notes writer's suite. The cut is
// gated on a notes file existing; what that file says is this suite's claim,
// and none of it is Go — so without this the placement rule, the section order,
// and the body the release page is published with would be exercised by a CI
// step alone, and first executed for real during a publication.
func TestAReleasesNotesSayWhatLanded(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", releaseNotesTestPath, tool)
		}
	}
	suite := shellSuite(t, releaseNotesTestPath)
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", releaseNotesTestPath, err, report)
	}
}

// shellSuite is a shell suite run from a scratch directory of its own rather
// than from this package's. The suites find this repository from their own
// path, so the working directory is nothing to them — but it is something to
// the shell: bash 3.2 writes a here-document to a scratch file, in the working
// directory when TMPDIR is not writable, which a sandbox without a usable /tmp
// is. Run from here, that file was listed by the composition census a moment
// before the shell unlinked it, and reported as content no class recognizes.
func shellSuite(t *testing.T, script string) *exec.Cmd {
	t.Helper()

	path, err := filepath.Abs(script)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", script, err)
	}
	suite := exec.Command("bash", path)
	suite.Dir = t.TempDir()
	return suite
}

// TestTheReleaseVerbExcusesOnlyExportsARunAllows holds the cut's list inside
// the run's. A path the cut passes over in its clean-tree check — dirty under
// the tag it places, with nobody asked to look at it — has to be one this
// repository already treats as churn a run may find under it; otherwise the
// cut is excusing something nothing else considers derived, and the archives
// could differ from the commit the tag names in a file somebody wrote. The
// containment is deliberately one-way: a run may come to tolerate a path the
// cut has no business excusing, and that is a widening rather than a
// disagreement.
func TestTheReleaseVerbExcusesOnlyExportsARunAllows(t *testing.T) {
	t.Parallel()

	verb := releaseQuotedPaths(t, derivedExportsPattern,
		releaseFileText(t, cutReleaseScriptPath), cutReleaseScriptPath, "derived_exports=(...)")
	declared := releaseQuotedPaths(t, allowedPrimaryChangesPattern,
		releaseFileText(t, runSourcePath), runSourcePath, "AllowedPrimaryChanges: []string{...}")
	if len(verb) == 0 {
		t.Fatalf("%s names no derived exports, which is not a state this check could be right about",
			cutReleaseScriptPath)
	}
	for _, path := range verb {
		if !slices.Contains(declared, path) {
			t.Errorf("%s excuses %q from its clean-tree check, which %s does not declare among %v; the cut may only pass over paths a run already treats as churn",
				cutReleaseScriptPath, path, runSourcePath, declared)
		}
	}
}

// TestTheReleaseWorkflowPublishesTheCuratedNotesAlone holds the workflow's
// publish step to handing the forge the composed notes file and nothing else,
// and its build to the tagged commit. On v0.5.0 the step passed
// `--generate-notes` beside `--notes-file`, the forge appended a changelog of
// some six hundred commits under 84,765 characters of curated notes, and
// refused the whole body as longer than the 125,000 characters it accepts; the
// notes grow with the backlog, so that would have refused every release from
// there. The archives' half is the same lesson from the same night: a binary
// records the commit it was built from, and the archives the local cut built
// before its housekeeping commit named one the tag did not.
func TestTheReleaseWorkflowPublishesTheCuratedNotesAlone(t *testing.T) {
	t.Parallel()

	// Comments are dropped before looking, so the workflow may say in words why
	// the flag is absent without this reading that as the flag.
	var commands []string
	for _, line := range strings.Split(releaseFileText(t, releaseWorkflowPath), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			commands = append(commands, line)
		}
	}
	workflow := strings.Join(commands, "\n")

	if !strings.Contains(workflow, "gh release create") {
		t.Fatalf("%s no longer publishes with `gh release create`, so what it hands the forge cannot be held here", releaseWorkflowPath)
	}
	if !strings.Contains(workflow, "--notes-file") {
		t.Errorf("%s publishes without --notes-file, so the release page's body is not the composed notes", releaseWorkflowPath)
	}
	if strings.Contains(workflow, "--generate-notes") {
		t.Errorf("%s passes --generate-notes, which appends the forge's commit-derived changelog under the notes; that is not what the notes are, and it is what refused v0.5.0's page for length", releaseWorkflowPath)
	}
	if !strings.Contains(workflow, `git rev-parse "$TAG^{commit}"`) {
		t.Errorf("%s no longer confirms the checkout is the tagged commit before building, so the archives it publishes may name a commit the tag does not", releaseWorkflowPath)
	}
	if !strings.Contains(workflow, `RELEASE_NOTES_FROM="origin/$DEFAULT_BRANCH" bash scripts/release-body.sh`) {
		t.Errorf("%s no longer hands scripts/release-body.sh the default branch to read the notes from, so a release tagged at the commit its notes name, which they landed after, publishes the install instructions alone", releaseWorkflowPath)
	}
	if !strings.Contains(workflow, `make dist-verify VERSION="$TAG"`) {
		t.Errorf("%s no longer builds the archives with `make dist-verify VERSION=\"$TAG\"`, which is the target that asserts the binary names the tag", releaseWorkflowPath)
	}
}

// releaseFileText reads one file of this checkout, reached from the package the
// test lives in.
func releaseFileText(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

// releaseQuotedPaths pulls the quoted paths out of the declaration pattern
// names, sorted, so what is compared is the set rather than the order two
// languages happen to spell it in.
func releaseQuotedPaths(t *testing.T, pattern *regexp.Regexp, content, path, shape string) []string {
	t.Helper()

	declaration := pattern.FindStringSubmatch(content)
	if declaration == nil {
		t.Fatalf("%s no longer holds a %s, so the two lists cannot be held to each other", path, shape)
	}
	var paths []string
	for _, quoted := range quotedPathPattern.FindAllStringSubmatch(declaration[1], -1) {
		paths = append(paths, quoted[1])
	}
	slices.Sort(paths)
	return paths
}
